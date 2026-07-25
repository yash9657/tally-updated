package nlq

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/khata/cored/internal/query"
)

// TestSchemaEnumsMatchDSLConstants is an INDEPENDENT-ORACLE consistency check:
// the emit_query JSON Schema is hand-written (schema.go), and here we assert its
// entity/func/op enums equal query's exported Go constants (dsl.go). If either
// side drifts — a new op added to dsl.go but not the schema, or a typo in the
// schema — this fails. The oracle is the Go constants, NOT the schema echoing
// itself.
func TestSchemaEnumsMatchDSLConstants(t *testing.T) {
	var schema struct {
		Properties struct {
			Entity  struct{ Enum []string } `json:"entity"`
			Metrics struct {
				Items struct {
					Properties struct {
						Func struct{ Enum []string } `json:"func"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"metrics"`
			Filters struct {
				Items struct {
					Properties struct {
						Op struct{ Enum []string } `json:"op"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"filters"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(emitQuerySchema), &schema); err != nil {
		t.Fatalf("emitQuerySchema is not valid JSON: %v", err)
	}

	// entity enum: the four query entity constants PLUS the "" escape hatch that
	// nlq adds so the model can signal "cannot answer from the catalog".
	wantEntities := []string{
		string(query.EntityLedgerBalances),
		string(query.EntityVouchers),
		string(query.EntityPostings),
		string(query.EntityOutstandings),
		"",
	}
	assertSet(t, "entity enum", wantEntities, schema.Properties.Entity.Enum)

	wantFuncs := []string{
		string(query.AggSum), string(query.AggCount),
		string(query.AggMin), string(query.AggMax), string(query.AggAvg),
	}
	assertSet(t, "func enum", wantFuncs, schema.Properties.Metrics.Items.Properties.Func.Enum)

	wantOps := []string{
		string(query.OpEq), string(query.OpNe), string(query.OpGt), string(query.OpGe),
		string(query.OpLt), string(query.OpLe), string(query.OpLike), string(query.OpIn),
	}
	assertSet(t, "op enum", wantOps, schema.Properties.Filters.Items.Properties.Op.Enum)
}

// TestSystemPromptCarriesNoSQL is a defense-in-depth check that the runtime-built
// prompt exposes NAMES ONLY: no SQL fragment from the whitelist leaks into it.
func TestSystemPromptCarriesNoSQL(t *testing.T) {
	p := systemPrompt()
	for _, leak := range []string{"SELECT", "vv.taxable_value", "lb.debit", "voucher_head", "COALESCE", "::text"} {
		if strings.Contains(p, leak) {
			t.Errorf("system prompt leaks a SQL fragment %q", leak)
		}
	}
	// It must still mention the entities so the model can use them.
	for _, ent := range []string{"ledger_balances", "vouchers", "postings", "outstandings"} {
		if !strings.Contains(p, ent) {
			t.Errorf("system prompt omits entity %q", ent)
		}
	}
}

func assertSet(t *testing.T, label string, want, got []string) {
	t.Helper()
	w := append([]string(nil), want...)
	g := append([]string(nil), got...)
	sort.Strings(w)
	sort.Strings(g)
	if len(w) != len(g) {
		t.Errorf("%s: want %v, got %v", label, w, g)
		return
	}
	for i := range w {
		if w[i] != g[i] {
			t.Errorf("%s: want %v, got %v", label, w, g)
			return
		}
	}
}
