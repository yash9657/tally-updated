package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// agentDispatcher implements agent.Dispatcher over the server's own router.
type agentDispatcher struct {
	s   *Server
	now func() time.Time
}

var _ agent.Dispatcher = agentDispatcher{}

// voucherIDsWrittenSQL finds the logical vouchers this transaction wrote.
//
// ⚠️ xmin IS HOW "WRITTEN BY THIS TRANSACTION" IS ASKED, and the comparison is
// on the low 32 bits: xmin is a 32-bit xid while txid_current_if_assigned() is
// epoch-extended. txid_current_if_assigned() is NULL for a transaction that
// has written nothing — the middleware's read transactions — so the hook is a
// no-op there without a second query.
const voucherIDsWrittenSQL = `
	SELECT DISTINCT voucher_id FROM voucher_version
	 WHERE company_id = $1
	   AND txid_current_if_assigned() IS NOT NULL
	   AND xmin::text = (txid_current_if_assigned() % 4294967296)::text`

const postingsWrittenSQL = `
	SELECT p.ledger_id, l.name, p.dr_cr::text, p.amount::text
	  FROM posting p JOIN ledger l ON l.id = p.ledger_id AND l.company_id = p.company_id
	 WHERE p.company_id = $1
	   AND txid_current_if_assigned() IS NOT NULL
	   AND p.xmin::text = (txid_current_if_assigned() % 4294967296)::text
	 ORDER BY p.dr_cr, l.name, p.amount`

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

	var (
		mu         sync.Mutex
		effect     = &agent.Effect{}
		voucherIDs []uuid.UUID
		fired      bool
		dupKey     bool
	)
	var hook *db.TxHook
	switch req.Mode {
	case agent.ModePreview:
		if !agent.Previewable(req.Spec.ID) {
			return agent.Result{}, fmt.Errorf("agent: %s is not previewable", req.Spec.ID)
		}
		hook = &db.TxHook{DryRun: true, BeforeCommit: func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, postingsWrittenSQL, companyID)
			if err != nil {
				return err
			}
			defer rows.Close()
			mu.Lock()
			defer mu.Unlock()
			for rows.Next() {
				var p agent.Posting
				if err := rows.Scan(&p.LedgerID, &p.Ledger, &p.DrCr, &p.Amount); err != nil {
					return err
				}
				paise, err := toPaise(p.Amount)
				if err != nil {
					return err
				}
				p.AmountPaise = paise
				// dr_cr is the 0002 enum ('D', 'C').
				if p.DrCr == "D" {
					effect.DebitPaise += paise
				} else {
					effect.CreditPaise += paise
				}
				effect.Postings = append(effect.Postings, p)
			}
			if err := rows.Err(); err != nil {
				return err
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM (`+voucherIDsWrittenSQL+`) v`, companyID).Scan(&n); err != nil {
				return err
			}
			effect.Vouchers += n
			return nil
		}}
	case agent.ModeCommit:
		if req.IdempotencyKey == "" {
			return agent.Result{}, errors.New("agent: a commit needs an idempotency key")
		}
		// ⛔ ANSWER A REPEATED KEY FROM THE RECORD, WITHOUT RUNNING THE
		// HANDLER. This is the crash-after-commit case: the voucher committed
		// and the durable step never recorded it, so exec:k runs again.
		var recorded []uuid.UUID
		var found bool
		if err := d.s.pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
			var err error
			recorded, found, err = agent.LookupCommit(ctx, tx, req.IdempotencyKey)
			return err
		}); err != nil {
			return agent.Result{}, err
		}
		if found {
			return agent.Result{Status: http.StatusOK, Replayed: true, VoucherIDs: recorded,
				Body: jsonBody(map[string]any{"replayed": true, "voucher_ids": recorded})}, nil
		}
		hook = &db.TxHook{BeforeCommit: func(ctx context.Context, tx pgx.Tx) error {
			mu.Lock()
			defer mu.Unlock()
			if fired {
				return nil
			}
			var assigned *int64
			if err := tx.QueryRow(ctx, `SELECT txid_current_if_assigned()`).Scan(&assigned); err != nil {
				return err
			}
			if assigned == nil {
				return nil // a read transaction (the middleware's): nothing to record
			}
			rows, err := tx.Query(ctx, voucherIDsWrittenSQL, companyID)
			if err != nil {
				return err
			}
			var ids []uuid.UUID
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			rows.Close()
			if len(ids) == 0 {
				return nil // this write transaction created no voucher
			}
			var approval any
			if req.ApprovalID != uuid.Nil {
				approval = req.ApprovalID
			}
			// ⛔ IN THE VOUCHER'S OWN TRANSACTION. If a racing dispatch of the
			// same key got here first, this INSERT violates the primary key,
			// the error rolls THIS transaction back, and the second voucher
			// never exists.
			if _, err := tx.Exec(ctx,
				`INSERT INTO agent_commit (company_id, idempotency_key, run_id, approval_id, action_id, voucher_ids)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				companyID, req.IdempotencyKey, req.RunID, approval, req.Spec.ID, ids); err != nil {
				dupKey = true
				return fmt.Errorf("agent: commit idempotency: %w", err)
			}
			fired = true
			voucherIDs = ids
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
			res.Effect = effect
		}
	case agent.ModeCommit:
		if dupKey {
			// Lost the race to a concurrent dispatch of the same key: answer
			// from the winner's record.
			var recorded []uuid.UUID
			_ = d.s.pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
				var err error
				recorded, _, err = agent.LookupCommit(ctx, tx, req.IdempotencyKey)
				return err
			})
			return agent.Result{Status: http.StatusOK, Replayed: true, VoucherIDs: recorded,
				Body: jsonBody(map[string]any{"replayed": true, "voucher_ids": recorded})}, nil
		}
		res.VoucherIDs = voucherIDs
		if res.OK() && !fired {
			// A 2xx that wrote no voucher is not a commit this layer can vouch
			// for; say so rather than record a success with nothing behind it.
			res.Status = http.StatusInternalServerError
			res.Body = jsonError("agent: the handler answered success but no voucher was written")
		}
	}
	return res, nil
}

func jsonError(msg string) []byte { return jsonBody(map[string]string{"error": msg}) }

func jsonBody(v any) []byte {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(v)
	return bytes.TrimSpace(buf.Bytes())
}
