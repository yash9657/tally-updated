package statutory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the Postgres-backed StepStore (migration 0013, table
// statutory_step). It is the durable counterpart of MemStore: workflow progress
// survives a statutoryd process restart, so a crash between "generate" (the only
// external IRP call) and "freeze" can never mint a second IRN.
//
// It takes the raw *pgxpool.Pool rather than the internal/platform/db helpers on
// purpose: internal/statutory is a SEPARATE deployable plane and the architecture
// test (internal/archtest/boundaries_test.go) forbids it from importing ANY
// sibling internal package. This mirrors authn.PGLockoutStore, which is the same
// shape — a non-RLS table reached over a plain pool with no transaction and no
// GUC (statutory_step carries no RLS by design; see the 0013 header).
//
// # Interface note (no context)
//
// StepStore's methods predate context plumbing (runner.go:18-23:
// Load(workflowID) / Append(workflowID, r)), and widening the interface would
// churn MemStore, Run, GenerateIRNWorkflow and every existing test. This WS
// deliberately does NOT widen it; the methods therefore use
// context.Background() internally. Consequence to be aware of: a store call does
// not observe HTTP request cancellation. That is acceptable and arguably
// desirable here — abandoning a half-written step record is worse than finishing
// a sub-millisecond INSERT — but it is the reason a future dedicated refactor
// (ctx on StepStore) is worth doing.
//
// # JSONB normalization (deliberate, and why it is safe)
//
// `output` is JSONB, so Postgres reorders object keys and reformats whitespace:
// storing {"b":2,"a":1} and reading it back yields {"a": 1, "b": 2}. MemStore, by
// contrast, hands back the json.RawMessage byte-for-byte. PGStore is therefore
// SEMANTICALLY, not byte-wise, equivalent to MemStore.
//
// This does not endanger IRN determinism: every stored output is consumed by
// json.Unmarshal into a struct (irn_workflow.go's build/validate/generate/freeze
// steps all do this), and FakeProvider derives the IRN from a FRESH
// json.Marshal of the decoded EInvoicePayload struct (fake.go), never from the
// stored bytes. So a resumed workflow re-derives the identical IRN from
// normalized JSONB. Any conformance assertion between MemStore and PGStore must
// compare decoded values (or json.Compact-ed forms) — never raw bytes. If byte
// fidelity is ever required, the column type JSON (not JSONB) preserves input
// text verbatim.
//
// # Concurrency
//
// Append uses ON CONFLICT DO NOTHING with no row lock, which makes it idempotent
// but does NOT serialize two processes racing the same step: two statutoryd
// instances sharing one DSN could both run `generate` and call the real IRP
// twice, the loser's output being discarded. Single-instance statutoryd is the
// deployment shape (IMPLEMENTATION_PLAN.md §7 deferral 7 — multi-instance HA /
// leader election). Do not read this store's idempotency as distributed mutual
// exclusion.
//
// # Stricter than MemStore
//
// `output` is NOT NULL, so a Step whose Run returns (nil, nil) fails at persist
// time here where MemStore silently records an empty result. No current step
// does that (all four end in json.Marshal), and the failure is a transient
// runner error ("statutory: persist step %q"), i.e. the workflow stays
// resumable — but PGStore is strictly less forgiving.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore constructs a PGStore over an existing pgx pool. The caller owns the
// pool's lifecycle. The statutory_step table must already exist — statutoryd
// does NOT run migrations (cored owns all schema); cmd/statutoryd verifies the
// table on boot and refuses to start otherwise.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// compile-time proof that PGStore satisfies the durable-runner contract.
var _ StepStore = (*PGStore)(nil)

// Load returns the workflow's completed step results in append order (ORDER BY
// the identity column, which is monotonic per insert). An unknown workflow
// yields an empty slice and no error, matching MemStore.
func (s *PGStore) Load(workflowID string) ([]StepResult, error) {
	ctx := context.Background()
	rows, err := s.pool.Query(ctx, `
		SELECT step_name, output
		FROM statutory_step
		WHERE workflow_id = $1
		ORDER BY seq`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("statutory: load steps for %q: %w", workflowID, err)
	}
	defer rows.Close()

	var out []StepResult
	for rows.Next() {
		var r StepResult
		var raw json.RawMessage
		if err := rows.Scan(&r.Name, &raw); err != nil {
			return nil, fmt.Errorf("statutory: scan step for %q: %w", workflowID, err)
		}
		r.Output = raw
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("statutory: load steps for %q: %w", workflowID, err)
	}
	return out, nil
}

// Append records one completed step. Re-appending an already-recorded step name
// is a silent no-op (ON CONFLICT DO NOTHING on the (workflow_id, step_name)
// primary key) — the same semantics as MemStore's duplicate-name scan, which is
// what lets a fully-completed workflow be re-Run with zero step executions.
func (s *PGStore) Append(workflowID string, r StepResult) error {
	ctx := context.Background()
	output := r.Output
	if len(output) == 0 {
		// JSONB NOT NULL would reject an empty payload with an opaque driver
		// error; fail loudly instead. (MemStore tolerates this; PGStore cannot.)
		return fmt.Errorf("statutory: append step %q of %q: empty output", r.Name, workflowID)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO statutory_step (workflow_id, step_name, output)
		VALUES ($1, $2, $3)
		ON CONFLICT (workflow_id, step_name) DO NOTHING`,
		workflowID, r.Name, output)
	if err != nil {
		return fmt.Errorf("statutory: append step %q of %q: %w", r.Name, workflowID, err)
	}
	return nil
}
