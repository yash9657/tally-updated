package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/policy"
)

// agent_dispatch.go is the agent plane's ONLY write path: it runs a request
// through the SAME chi router, the SAME companyAuthMiddleware and the SAME
// handler a person's browser reaches, in process. PLAN-AGENTIC §1: "The agent
// never gains a write path the human does not have. Every write is an
// ActionSpec the UI also uses, evaluated by the same PDP, validated by the same
// code, versioned into the same chain."
//
// # HOW AN IN-PROCESS REQUEST AUTHENTICATES, AND WHY IT IS NOT A SIDE DOOR
//
// There is no token to present — in production the signer is an external IdP
// cored cannot mint for. So the dispatcher puts an agentCall on the request's
// context under an UNEXPORTED key (no header, no body and no query string can
// produce one), and authenticate() turns it into the claims of the run's human
// — their subject, with iat = the run's start. Everything after that is the
// ordinary path, unmodified: LookupUserBySubject (an inactive user is
// refused), Membership (a removed member is refused), Level + Rules +
// authz.Evaluate (ACL(user), the back-dated cutoff, the year-end lock, the
// WebAuthn step-up), and sessionRevoked (a user locked out after starting the
// run has its requests revoked). The ONLY addition is one more refusal:
// companyAuthMiddleware applies internal/policy to the agentCall — ACL(agent)
// ∩ delegation ∩ the structural rules — and requires the route's own
// (action, resource) to equal the ActionSpec the call declared.

type agentCallKey struct{}

// agentCall is the in-process agent identity carried on a request.
type agentCall struct {
	caller agent.Caller
	spec   actions.ActionSpec
	// at is the ENGINE clock at dispatch, so the router judges the
	// delegation expiry on the same clock the run was started and gated on.
	at time.Time
}

func withAgentCall(ctx context.Context, c *agentCall) context.Context {
	return context.WithValue(ctx, agentCallKey{}, c)
}

func agentCallFrom(ctx context.Context) *agentCall {
	c, _ := ctx.Value(agentCallKey{}).(*agentCall)
	return c
}

// agentClaims is authenticate()'s answer for an in-process agent request.
func (c *agentCall) claims() *authn.Claims {
	cl := &authn.Claims{Subject: c.caller.Subject, IssuedAt: c.caller.IssuedAt.Unix()}
	if c.caller.WebAuthn {
		// Only a commit whose APPROVER stepped up carries this: the human's
		// step-up at the moment of approval is what a WebAuthn level requires
		// for the write, and the agent can never produce it for itself.
		cl.AMR = []string{"webauthn"}
	}
	return cl
}

// agentGate is the one refusal companyAuthMiddleware adds for an agent call:
// route identity must equal the declared spec, and internal/policy must allow.
// Returns "" to allow.
func agentGate(c *agentCall, act authz.Action, res authz.Resource, resName string, now time.Time) string {
	if c.spec.Action != act || c.spec.Resource != res || c.spec.ResourceName != resName {
		return fmt.Sprintf("agent request routed to %s but declared %s", actions.RenderACLKey(act, res, resName), c.spec.ACLKey)
	}
	if r := policy.EffectiveRights(c.caller.Principal, c.spec, now); !r.Allow {
		return "agent: " + r.Reason
	}
	return ""
}

// attributedTo is the created_by a write is stamped with: the body's claim for
// a person's request, and — for an agent's in-process call — ALWAYS
// "agent:<run> for <user>", whatever the body said.
//
// ⛔ THE AGENT DOES NOT CHOOSE ITS OWN ATTRIBUTION. The body's created_by is
// free text the model assembled; left alone, an agent-posted voucher read
// "agent" (wave 1) or whatever an injected instruction asked it to write —
// "Ramesh", say. The stamp names the run (whose intent log says what was asked,
// what was shown, and who approved it) and the human the run acted for.
func attributedTo(r *http.Request, claimed string) string {
	if call := agentCallFrom(r.Context()); call != nil {
		return AgentAttribution(call.caller.Principal.RunID, call.caller.Principal.OnBehalfOf)
	}
	return claimed
}

// AgentAttribution is the created_by an agent's write carries.
func AgentAttribution(runID, onBehalfOf uuid.UUID) string {
	return "agent:" + runID.String() + " for " + onBehalfOf.String()
}

// agentRunOf is the run an in-process agent request belongs to (uuid.Nil for a
// person's request).
func agentRunOf(r *http.Request) uuid.UUID {
	if call := agentCallFrom(r.Context()); call != nil {
		return call.caller.Principal.RunID
	}
	return uuid.Nil
}

// agentDispatcher implements agent.Dispatcher over the server's own router.
type agentDispatcher struct {
	s   *Server
	now func() time.Time
}

var _ agent.Dispatcher = agentDispatcher{}

func toPaise(amount string) (int64, error) {
	d, err := decimal.NewFromString(amount)
	if err != nil {
		return 0, err
	}
	return d.Mul(decimal.NewFromInt(100)).Round(0).IntPart(), nil
}

func (d agentDispatcher) Dispatch(ctx context.Context, c agent.Caller, req agent.Request) (agent.Result, error) {
	companyID := c.Principal.CompanyID
	call := &agentCall{caller: c, spec: req.Spec, at: d.now()}

	// Defence in depth before any request is built: the router applies the
	// same gate, but a refusal here costs nothing and names the reason.
	if r := policy.EffectiveRights(c.Principal, req.Spec, d.now()); !r.Allow {
		return agent.Result{Status: http.StatusForbidden, Body: jsonError("agent: " + r.Reason)}, nil
	}
	if req.Spec.IsWrite && req.Mode == agent.ModeRead {
		return agent.Result{}, fmt.Errorf("agent: a write dispatched as a read")
	}
	if !req.Spec.IsWrite && req.Mode != agent.ModeRead {
		return agent.Result{}, fmt.Errorf("agent: a read dispatched as %s", req.Mode)
	}
	if req.Mode != agent.ModeRead && !agent.Previewable(req.Spec.ID) {
		// ⛔ THE SAME ALLOWLIST FOR COMMIT AS FOR PREVIEW. A commit replays a
		// recorded preview, so it can only name a previewable spec — but the
		// readTxEffect contract below (one hooked transaction, read back) is a
		// property of the handler, and this is where it is relied on.
		return agent.Result{}, fmt.Errorf("agent: %s is not previewable", req.Spec.ID)
	}

	// A bank-reconciliation write changes a row an un-tick then DELETES, so
	// the before-image must be read before the handler runs (agent_effect.go).
	var tick *bankTick
	if req.Mode != agent.ModeRead && req.Spec.Resource == "bank-reconciliation" {
		t, err := d.bankTickBefore(ctx, companyID, req.Params)
		if err != nil {
			return agent.Result{}, err
		}
		tick = t
	}

	var (
		mu       sync.Mutex
		effect   = &agent.Effect{}
		written  txEffect
		fired    bool
		dupKey   bool
		overCeil string
		stale    string
		runEnded bool
		hookErr  error
	)
	var hook *db.TxHook
	switch req.Mode {
	case agent.ModePreview:
		hook = &db.TxHook{DryRun: true, BeforeCommit: func(ctx context.Context, tx pgx.Tx) error {
			te, err := readTxEffect(ctx, tx, companyID, tick)
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			mergeEffect(effect, te.effect)
			return nil
		}}
	case agent.ModeCommit:
		if req.IdempotencyKey == "" {
			return agent.Result{}, errors.New("agent: a commit needs an idempotency key")
		}
		// ⛔ ANSWER A REPEATED KEY FROM THE RECORD, WITHOUT RUNNING THE
		// HANDLER. This is the crash-after-commit case: the write committed
		// and the durable step never recorded it, so exec:k runs again.
		rec, found, err := d.lookupCommit(ctx, companyID, req.IdempotencyKey)
		if err != nil {
			return agent.Result{}, err
		}
		if found {
			return replayed(rec), nil
		}
		hook = &db.TxHook{BeforeCommit: func(ctx context.Context, tx pgx.Tx) error {
			mu.Lock()
			defer mu.Unlock()
			if fired {
				return nil
			}
			te, err := readTxEffect(ctx, tx, companyID, tick)
			if err != nil {
				return err
			}
			if te.kind == "" {
				return nil // this transaction wrote nothing a commit records
			}
			// ⛔ THE RUN MUST STILL BE LIVE, UNDER A ROW LOCK, IN THIS
			// TRANSACTION. FOR SHARE makes a concurrent Cancel's UPDATE wait
			// for this commit (the write then predates the cancel), and a
			// Cancel that committed first is seen here and rolls the write
			// back. The engine's per-step status read cannot close that gap.
			if req.RunID != uuid.Nil {
				var st string
				if err := tx.QueryRow(ctx, `SELECT status FROM agent_run WHERE id = $1 FOR SHARE`, req.RunID).Scan(&st); err != nil {
					return err
				}
				if st != "running" && st != "parked" {
					runEnded = true
					return fmt.Errorf("agent: run %s is %s; nothing it proposed may commit", req.RunID, st)
				}
			}
			// ⛔ AN APPROVED ALTERATION RESTATES THE VERSION IT WAS SHOWN. The
			// ledger appends an ALTER onto whatever the head is now; if a
			// person altered the voucher after the preview, committing the
			// agent's restatement would silently discard their edit. The new
			// version must be exactly one past the one the approver saw.
			for _, v := range te.versions {
				if v.Op != "ALTER" {
					continue
				}
				want, ok := req.ExpectVersions[v.VoucherID]
				if !ok {
					stale = fmt.Sprintf("voucher %s was altered but its preview recorded no version to restate", v.VoucherID)
					return errors.New("agent: " + stale)
				}
				if v.VersionNo != want+1 {
					stale = fmt.Sprintf("voucher %s has changed since it was previewed (the approver saw version %d; it is now at version %d) — preview it again",
						v.VoucherID, want, v.VersionNo-1)
					return errors.New("agent: " + stale)
				}
			}
			// ⛔ THE DELEGATED CEILING, ON WHAT THIS TRANSACTION ACTUALLY
			// WROTE. The gate judged the ceiling on the preview; an approver's
			// edit is re-previewed at the decision, but this is the one place
			// the amount of the body that COMMITS is known for certain. Above
			// the ceiling the write rolls back.
			if req.MaxDebitPaise != nil && te.effect.DebitPaise > *req.MaxDebitPaise {
				overCeil = fmt.Sprintf("the committed debit of %d paise exceeds this run's delegated ceiling of %d paise",
					te.effect.DebitPaise, *req.MaxDebitPaise)
				return errors.New("agent: " + overCeil)
			}
			var approval any
			if req.ApprovalID != uuid.Nil {
				approval = req.ApprovalID
			}
			voucherIDs := []uuid.UUID{}
			if te.kind == agent.ObjectVoucher {
				voucherIDs = te.objects
			}
			// ⛔ IN THE WRITE'S OWN TRANSACTION. If a racing dispatch of the
			// same key got here first, this INSERT violates the primary key,
			// the error rolls THIS transaction back, and the second write
			// never exists.
			if _, err := tx.Exec(ctx,
				`INSERT INTO agent_commit (company_id, idempotency_key, run_id, approval_id, action_id,
				                           voucher_ids, object_kind, object_ids)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				companyID, req.IdempotencyKey, req.RunID, approval, req.Spec.ID,
				voucherIDs, string(te.kind), te.objects); err != nil {
				// ⛔ ONLY A UNIQUE VIOLATION IS "LOST THE RACE". Any other
				// failure — a deadlock, a serialization failure, a statement
				// timeout, the run FK — also rolls the write back, but there
				// is no winner's record to answer from; treating it as a replay
				// once recorded an approved payment as posted when nothing was.
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) && pgErr.Code == "23505" {
					dupKey = true
				} else {
					hookErr = err
				}
				return fmt.Errorf("agent: commit idempotency: %w", err)
			}
			fired = true
			written = te
			return nil
		}}
	}

	// ⛔ A FRESH CONTEXT, NOT THE CALLER'S. The poller's ctx already carries a
	// company scope (Resume set it), and a value that rode in on it would reach
	// the router as if the middleware had established it. The in-process
	// request starts with NOTHING but the agent call and (for preview/commit)
	// the hook, exactly as a browser request starts with nothing but its
	// headers; only the caller's cancellation is carried over.
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	hctx := withAgentCall(base, call)
	if hook != nil {
		hctx = db.WithTxHook(hctx, hook)
	}
	target := req.Path
	if req.Query != "" {
		target += "?" + req.Query
	}
	r := httptest.NewRequest(req.Spec.Method, target, bytes.NewReader(req.Body)).WithContext(hctx)
	if len(req.Body) > 0 {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	d.s.router.ServeHTTP(w, r)

	res := agent.Result{Status: w.Code, Body: bytes.TrimSpace(w.Body.Bytes())}
	switch req.Mode {
	case agent.ModePreview:
		committed, rolledBack := hook.Counts()
		// ⛔ THE DRY RUN IS CHECKED, NOT ASSUMED. A handler that somehow
		// committed during a preview is a defect that must stop the run.
		if committed != 0 {
			return agent.Result{}, fmt.Errorf("agent: preview of %s COMMITTED %d transaction(s)", req.Spec.ID, committed)
		}
		if res.OK() {
			if rolledBack == 0 {
				return agent.Result{}, fmt.Errorf("agent: preview of %s ran no hooked transaction", req.Spec.ID)
			}
			if effect.Postings == nil {
				// An order, a master or a stock voucher posts no legs; the wire
				// says so with an empty list, never null (the inbox draws it).
				effect.Postings = []agent.Posting{}
			}
			res.Effect = effect
		}
	case agent.ModeCommit:
		if runEnded {
			return agent.Result{Status: http.StatusConflict, Body: jsonError("agent: the run was cancelled; nothing it proposed may commit")}, nil
		}
		if stale != "" {
			return agent.Result{Status: http.StatusConflict, Body: jsonError("agent: " + stale)}, nil
		}
		if overCeil != "" {
			return agent.Result{Status: http.StatusForbidden, Body: jsonError("agent: " + overCeil)}, nil
		}
		if hookErr != nil {
			// TRANSIENT: returned as an error so exec:k records nothing and is
			// retried with the SAME idempotency key.
			return agent.Result{}, fmt.Errorf("agent: recording the commit of %s failed; the write was rolled back: %w", req.Spec.ID, hookErr)
		}
		if dupKey {
			// Lost the race to a concurrent dispatch of the same key: answer
			// from the winner's record — which must exist; if it cannot be
			// read, that is a transient failure, never an empty "replayed".
			rec, found, err := d.lookupCommit(ctx, companyID, req.IdempotencyKey)
			if err != nil {
				return agent.Result{}, fmt.Errorf("agent: reading the winning commit of %s: %w", req.IdempotencyKey, err)
			}
			if !found {
				return agent.Result{}, fmt.Errorf("agent: commit key %s collided but no record of it is visible", req.IdempotencyKey)
			}
			return replayed(rec), nil
		}
		if fired {
			res.ObjectKind, res.ObjectIDs = written.kind, written.objects
			if written.kind == agent.ObjectVoucher {
				res.VoucherIDs = written.objects
			}
		}
		if res.OK() && !fired {
			// A 2xx that wrote nothing is not a commit this layer can vouch
			// for; say so rather than record a success with nothing behind it.
			res.Status = http.StatusInternalServerError
			res.Body = jsonError("agent: the handler answered success but nothing was written")
		}
	}
	return res, nil
}

// replayed is the answer to a known idempotency key: the original commit, said
// again, marked as a replay.
func replayed(rec agent.CommitRecord) agent.Result {
	return agent.Result{Status: http.StatusOK, Replayed: true, VoucherIDs: rec.VoucherIDs,
		ObjectKind: rec.ObjectKind, ObjectIDs: rec.ObjectIDs,
		Body: jsonBody(map[string]any{"replayed": true, "voucher_ids": rec.VoucherIDs,
			"object_kind": rec.ObjectKind, "object_ids": rec.ObjectIDs})}
}

func (d agentDispatcher) lookupCommit(ctx context.Context, companyID uuid.UUID, key string) (agent.CommitRecord, bool, error) {
	var rec agent.CommitRecord
	var found bool
	err := d.s.pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		rec, found, err = agent.LookupCommit(ctx, tx, key)
		return err
	})
	return rec, found, err
}

// bankTickBefore reads a bank-reconciliation leg's tick before the write that
// changes it. ledgerID and voucherID are uuids (concretePath required them);
// legNo is the leg's ordinal.
func (d agentDispatcher) bankTickBefore(ctx context.Context, companyID uuid.UUID, params map[string]string) (*bankTick, error) {
	ledgerID, err := uuid.Parse(params["ledgerID"])
	if err != nil {
		return nil, fmt.Errorf("agent: bank reconciliation needs a ledger id")
	}
	voucherID, err := uuid.Parse(params["voucherID"])
	if err != nil {
		return nil, fmt.Errorf("agent: bank reconciliation needs a voucher id")
	}
	legNo, err := strconv.Atoi(params["legNo"])
	if err != nil || legNo < 0 {
		return nil, fmt.Errorf("agent: bank reconciliation needs a leg number")
	}
	t := &bankTick{voucherID: voucherID, ledgerID: ledgerID, legNo: legNo}
	err = d.s.pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		t.date, err = readBankTick(ctx, tx, voucherID, ledgerID, legNo)
		return err
	})
	return t, err
}

// mergeEffect folds one transaction's effect into a preview's running total
// (a previewable handler writes in one transaction, but the middleware's
// reads are hooked too and contribute nothing).
func mergeEffect(into *agent.Effect, e agent.Effect) {
	into.Postings = append(into.Postings, e.Postings...)
	into.DebitPaise += e.DebitPaise
	into.CreditPaise += e.CreditPaise
	into.Vouchers += e.Vouchers
	into.Versions = append(into.Versions, e.Versions...)
	into.Replaces = append(into.Replaces, e.Replaces...)
	into.Delta = append(into.Delta, e.Delta...)
	into.Stock = append(into.Stock, e.Stock...)
	into.Masters = append(into.Masters, e.Masters...)
	into.Orders = append(into.Orders, e.Orders...)
	into.BankDates = append(into.BankDates, e.BankDates...)
}

func jsonError(msg string) []byte { return jsonBody(map[string]string{"error": msg}) }

func jsonBody(v any) []byte {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(v)
	return bytes.TrimSpace(buf.Bytes())
}
