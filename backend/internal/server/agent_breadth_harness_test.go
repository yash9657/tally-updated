package server_test

// agent_breadth_harness_test.go — helpers for the W2 BREADTH tests (D121 "Wave
// 2 breadth"): alterations, reversals, masters, stock vouchers, orders, bank
// reconciliation, profiles, the autonomy route and the parity gate.
//
// Every oracle here reads the database with the ADMIN role, independent of
// every agent code path — the same stance countVouchers takes.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
)

// adminInt runs a one-value integer query as the admin role.
func adminInt(t *testing.T, sql string, args ...any) int {
	t.Helper()
	c, err := pgx.Connect(context.Background(), adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	var n int
	if err := c.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("admin query %q: %v", sql, err)
	}
	return n
}

// adminString runs a one-value text query as the admin role.
func adminString(t *testing.T, sql string, args ...any) string {
	t.Helper()
	c, err := pgx.Connect(context.Background(), adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	var s string
	if err := c.QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		t.Fatalf("admin query %q: %v", sql, err)
	}
	return s
}

// booksFingerprint is every row class an agent commit can write, counted. An
// injection that wrote ANYTHING changes it.
type booksFingerprint struct {
	Versions, Masters, Orders, BankTicks, Commits, Stock int
}

func fingerprint(t *testing.T, companyID string) booksFingerprint {
	t.Helper()
	return booksFingerprint{
		Versions:  adminInt(t, `SELECT count(*) FROM voucher_version WHERE company_id = $1`, companyID),
		Masters:   adminInt(t, `SELECT count(*) FROM master_audit WHERE company_id = $1`, companyID),
		Orders:    adminInt(t, `SELECT count(*) FROM order_head WHERE company_id = $1`, companyID),
		BankTicks: adminInt(t, `SELECT count(*) FROM bank_reconciliation WHERE company_id = $1`, companyID),
		Commits:   adminInt(t, `SELECT count(*) FROM agent_commit WHERE company_id = $1`, companyID),
		Stock:     adminInt(t, `SELECT count(*) FROM stock_movement WHERE company_id = $1`, companyID),
	}
}

// postHuman posts a voucher as the owner (the ordinary UI path) and returns its id.
func postHuman(t *testing.T, srv *server.Server, b agentBooks, class string, body map[string]any) string {
	t.Helper()
	rec, resp := do(t, srv, http.MethodPost, b.base+"/vouchers/"+class, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("human %s: %d %s", class, rec.Code, rec.Body.String())
	}
	id, _ := resp["voucher_id"].(string)
	if id == "" {
		t.Fatalf("human %s returned no voucher id: %s", class, rec.Body.String())
	}
	return id
}

// runToQuiet starts a run with a scripted model and polls it until nothing is due.
func runToQuiet(t *testing.T, srv *server.Server, b agentBooks, s script, body map[string]any) (string, *fakeModel) {
	t.Helper()
	model := scriptedModel(t, s)
	srv.ConfigureAgent(server.AgentOptions{LLM: model.client(t)})
	runID := startRun(t, srv, b, body)
	pollUntilQuiet(t, srv)
	return runID, model
}

// onePending is the run's single pending approval.
func onePending(t *testing.T, srv *server.Server, b agentBooks, runID string) map[string]any {
	t.Helper()
	p := pendingApprovals(t, srv, b, runID)
	if len(p) != 1 {
		t.Fatalf("pending approvals = %d, want 1 (run %v)", len(p), getRun(t, srv, b, runID)["status"])
	}
	return p[0]
}

// approveAndDrain approves the run's single pending approval as the owner and
// polls the commit through.
func approveAndDrain(t *testing.T, srv *server.Server, b agentBooks, runID string) map[string]any {
	t.Helper()
	a := onePending(t, srv, b, runID)
	if code, resp := decide(t, srv, b, a["id"].(string), map[string]any{"decision": "approve"}); code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, resp)
	}
	pollUntilQuiet(t, srv)
	return a
}

// effectOf decodes an approval's preview effect.
func effectOf(t *testing.T, a map[string]any) map[string]any {
	t.Helper()
	p, _ := a["preview"].(map[string]any)
	e, _ := p["effect"].(map[string]any)
	if e == nil {
		t.Fatalf("approval has no preview effect: %v", a["preview"])
	}
	return e
}

// toolResult decodes the RESULT turn i the model saw.
func toolResult(t *testing.T, res []json.RawMessage, i int) map[string]any {
	t.Helper()
	if i >= len(res) {
		t.Fatalf("script: no result %d (have %d)", i, len(res))
	}
	var r struct {
		Tool map[string]any `json:"tool"`
	}
	if err := json.Unmarshal(res[i], &r); err != nil {
		t.Fatalf("script: result %d: %v", i, err)
	}
	return r.Tool
}

// ownerUserID is the owner's user id, read from a run's created_by.
func ownerUserID(t *testing.T, srv *server.Server, b agentBooks, runID string) string {
	t.Helper()
	id, _ := getRun(t, srv, b, runID)["created_by"].(string)
	if id == "" {
		t.Fatal("run has no created_by")
	}
	return id
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("not a uuid: %q", s)
	}
	return id
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
