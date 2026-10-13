package llm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// ---------------------------------------------------------------------------
// Per-tenant token budgets.
// ---------------------------------------------------------------------------
//
// ⛔ RESERVE-THEN-SETTLE, NOT COUNT-AFTERWARDS.
//
// The obvious design is to add the response's token usage to a running total
// after each call and refuse once the total passes the cap. It is wrong in a way
// that only shows up under the load it is supposed to protect against: usage is
// known only AFTER the tokens have been spent, so N concurrent calls all read
// the same under-cap total, all proceed, and the tenant overruns by N calls'
// worth. Worse, a call that never returns — a timeout, a cancelled context, a
// panic — is never counted at all, so the budget drifts DOWN over time and a
// tenant who trips the cap once can trip it forever afterwards on ghost tokens.
//
// Reserve-then-settle fixes both. Reserve takes an ESTIMATE against the cap
// before the socket opens (so concurrency is serialised by the database, not by
// hope), and exactly one of Settle or Release runs afterwards on every exit path
// — success, non-2xx, cancellation, panic — so the in-flight figure returns to
// zero whatever happens. client.go's defer is where that "exactly one" is
// enforced, and TestBudgetReserveSettleBalanced is where it is proved.

// Budget decides whether a tenant may spend, and hands back the handle that
// closes the loop.
//
// estTokens is an ESTIMATE and is documented as one: it is bytes/4 plus the
// requested completion cap, which over-counts for English and under-counts for
// Devanagari. That is acceptable because Settle replaces it with the served
// figure moments later; what must never happen is a reservation of ZERO, which
// would make the cap unenforceable under concurrency.
type Budget interface {
	Reserve(ctx context.Context, tenantID uuid.UUID, feature string, estTokens int) (Reservation, error)
}

// Reservation is the other half. ⛔ EXACTLY ONE of Settle or Release runs, once.
// Implementations must tolerate a second call (return nil, change nothing)
// rather than double-counting: a defensive no-op is cheaper than an audit of
// every future caller's control flow.
type Reservation interface {
	Settle(ctx context.Context, actual Usage) error
	Release(ctx context.Context) error
}

// NoBudget is the shipped default: it reserves everything and settles nothing.
//
// ⚠️ IT IS A DELIBERATE, NAMED "NO CAP" AND NOT AN ABSENT ONE. Config.Budget nil
// resolves to this, and the reason it is a type rather than a nil check is that
// "this deployment has no token cap" should be a sentence somebody wrote in the
// wiring code, greppable, rather than a field nobody filled in. Until PGBudget's
// limits are configured per tenant, an unbudgeted deployment is the honest
// state — and it is honest precisely because it is spelled.
type NoBudget struct{}

// Reserve always allows.
func (NoBudget) Reserve(context.Context, uuid.UUID, string, int) (Reservation, error) {
	return noReservation{}, nil
}

type noReservation struct{}

func (noReservation) Settle(context.Context, Usage) error { return nil }
func (noReservation) Release(context.Context) error       { return nil }

var (
	_ Budget      = NoBudget{}
	_ Reservation = noReservation{}
)

// ErrBudgetScopeMismatch is a PROGRAMMING fault, and it is a tenant-isolation
// control rather than a validation nicety.
//
// PGBudget takes the tenant id as an argument AND reads it from the context's
// company scope, and refuses when they disagree. Without that check, a caller
// holding company A's context could bill company B — the row would be written
// under A's RLS scope with B's id in the argument, or refused by the policy at
// some later, more confusing point. The rule this package follows is the one
// internal/platform/db's header states: the company in context is the authority,
// and code that wants to act on a different one is wrong at the call site.
var ErrBudgetScopeMismatch = errors.New("llm: budget tenant id does not match the company in context")

// ErrBudgetNotConfigured is the loud refusal for a PGBudget with no limit.
var ErrBudgetNotConfigured = errors.New("llm: PGBudget has no DefaultLimitTokens configured")

// PGBudget is the per-tenant token budget over internal/platform/db.
//
// One row per (company, period, feature) in llm_token_budget (migration 0091),
// under the same FORCE RLS every tenant table in this schema carries. The window
// is a CALENDAR MONTH keyed by its first day.
//
// ⚠️ DefaultLimitTokens IS REQUIRED AND ZERO IS REFUSED. A budget package whose
// unset limit meant "unlimited" would be a cap that silently is not one; a
// budget package whose unset limit meant "zero" would refuse every call on a
// deployment that merely forgot to configure it. Neither is acceptable, so
// neither is the default: Reserve returns ErrBudgetNotConfigured until an
// operator states a number. A deployment that wants no cap wires NoBudget, which
// says so.
type PGBudget struct {
	Pool *db.Pool
	// DefaultLimitTokens seeds a period's row the first time a tenant spends in
	// it. Per-tenant overrides are an UPDATE on the row; this is only the
	// starting point.
	DefaultLimitTokens int64
	// Now is the injected clock that decides WHICH MONTH is being spent. Nil
	// means time.Now — but a test that wants to prove the window rolls over
	// must be able to move it, which is the same argument
	// internal/nlq/translate.go:66 makes for the prompt clock.
	Now func() time.Time
}

var _ Budget = (*PGBudget)(nil)

func (b *PGBudget) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// periodStart is the first day of the month, in UTC.
//
// ⚠️ UTC AND NOT ASIA/KOLKATA, DELIBERATELY, and the difference is 5.5 hours of
// spend at each month boundary. A token budget is an infrastructure cap, not a
// statutory period: nothing about it is reported to anybody, reconciled against
// anything, or compared with a GST return. The financial year handling in this
// repo (query.FYStartMonth) exists because the books' periods ARE statutory —
// this is the opposite case, and using one clock for both would invite somebody
// to "fix" the books' periods to match this one.
func periodStart(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}

type pgReservation struct {
	pool     *db.Pool
	rowID    uuid.UUID
	tenantID uuid.UUID
	est      int64
	done     bool
}

// Reserve takes estTokens against this tenant's cap for the current month.
func (b *PGBudget) Reserve(ctx context.Context, tenantID uuid.UUID, feature string, estTokens int) (Reservation, error) {
	if b.Pool == nil {
		return nil, fmt.Errorf("%w: PGBudget has no pool", ErrBudgetNotConfigured)
	}
	if b.DefaultLimitTokens <= 0 {
		return nil, ErrBudgetNotConfigured
	}
	if feature == "" {
		return nil, fmt.Errorf("llm: budget reserve with an empty feature")
	}
	// The tenant-isolation check. See ErrBudgetScopeMismatch.
	cid, ok := db.CompanyFromContext(ctx)
	if !ok {
		return nil, db.ErrNoCompany
	}
	if cid != tenantID {
		return nil, fmt.Errorf("%w: context=%s argument=%s", ErrBudgetScopeMismatch, cid, tenantID)
	}
	// ⚠️ A ZERO RESERVATION IS RAISED TO ONE. Reserving nothing would let an
	// unbounded number of calls through a cap that is already full, because
	// `used + 0 <= limit` is true whenever `used <= limit`.
	est := int64(estTokens)
	if est < 1 {
		est = 1
	}

	period := periodStart(b.now())
	rowID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("llm: budget row id: %w", err)
	}

	var reserved uuid.UUID
	err = b.Pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Ensure the period row exists. ON CONFLICT DO NOTHING rather than a
		// SELECT-then-INSERT: two concurrent first calls in a month would
		// otherwise race, and the loser would see a unique violation instead of
		// a budget answer.
		if _, err := tx.Exec(ctx, `
			INSERT INTO llm_token_budget (id, company_id, period_start, feature, limit_tokens)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (company_id, period_start, feature) DO NOTHING`,
			rowID, tenantID, period, feature, b.DefaultLimitTokens); err != nil {
			return err
		}
		// The reservation itself, as ONE conditional UPDATE. The condition is
		// evaluated by the database under the row lock the UPDATE takes, so
		// concurrent reservations serialise; a read-then-write in Go would not.
		// No row back == the cap would have been exceeded.
		row := tx.QueryRow(ctx, `
			UPDATE llm_token_budget
			   SET reserved_tokens = reserved_tokens + $1,
			       updated_at      = now()
			 WHERE company_id   = $2
			   AND period_start = $3
			   AND feature      = $4
			   AND reserved_tokens + prompt_tokens + completion_tokens + $1 <= limit_tokens
			RETURNING id`, est, tenantID, period, feature)
		if err := row.Scan(&reserved); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrBudgetExhausted
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &pgReservation{pool: b.Pool, rowID: reserved, tenantID: tenantID, est: est}, nil
}

// Settle replaces the estimate with what was actually served.
//
// ⚠️ GREATEST(reserved_tokens - est, 0) rather than a bare subtraction. The
// counter is protected by a CHECK in migration 0091, and a NEGATIVE reserved
// figure — which a double Settle or a Settle after a Release would produce —
// would abort the transaction and turn a metering slip into a failed request.
// The floor makes the arithmetic idempotent-ish; `done` makes it idempotent.
func (r *pgReservation) Settle(ctx context.Context, actual Usage) error {
	if r.done {
		return nil
	}
	r.done = true
	prompt := int64(actual.PromptTokens)
	completion := int64(actual.CompletionTokens)
	cached := int64(actual.CachedPromptTokens)
	if cached > prompt {
		// Cached is a SUBSET of prompt (see Usage). A provider reporting
		// otherwise is reporting something we do not understand, and storing
		// it would make cached_prompt_tokens/prompt_tokens exceed 1 in every
		// report built on this table.
		cached = prompt
	}
	return r.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE llm_token_budget
			   SET reserved_tokens      = GREATEST(reserved_tokens - $1, 0),
			       prompt_tokens        = prompt_tokens + $2,
			       cached_prompt_tokens = cached_prompt_tokens + $3,
			       completion_tokens    = completion_tokens + $4,
			       calls                = calls + 1,
			       updated_at           = now()
			 WHERE company_id = $5 AND id = $6`,
			r.est, prompt, cached, completion, r.tenantID, r.rowID)
		return err
	})
}

// Release hands the estimate back without recording a call.
//
// It is the path a failed request takes, and it deliberately does NOT increment
// `calls`: a call that was never served is not a call, and counting it would
// make the per-feature cost-per-call figure in §6 wrong in the direction that
// flatters us (dividing real tokens by an inflated call count).
func (r *pgReservation) Release(ctx context.Context) error {
	if r.done {
		return nil
	}
	r.done = true
	return r.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE llm_token_budget
			   SET reserved_tokens = GREATEST(reserved_tokens - $1, 0),
			       released        = released + 1,
			       updated_at      = now()
			 WHERE company_id = $2 AND id = $3`,
			r.est, r.tenantID, r.rowID)
		return err
	})
}
