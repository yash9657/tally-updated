package actions

import (
	"testing"

	"github.com/khata/cored/internal/authz"
)

// TestEveryRowCarriesTheRiskItsKeyImplies re-derives the checked-in Risk column
// from ClassifyRisk, so the column in registry_gen.go cannot drift from the
// rule, and refuses any row the rule could not classify.
//
// Mutation proofs (run for D121): hand-edit one "money" row in registry_gen.go
// to "operational" -> this test fails naming the row; delete "payment" from
// resourceRisk -> the payment rows come back RiskUnclassified and this test
// fails on them.
func TestEveryRowCarriesTheRiskItsKeyImplies(t *testing.T) {
	counts := map[Risk]int{}
	for _, s := range Specs {
		want := ClassifyRisk(s.Action, s.Resource)
		if s.Risk != want {
			t.Errorf("%s: registry says risk %q, ClassifyRisk(%s, %s) says %q — regenerate:\n"+
				"  UPDATE_GOLDENS=1 go test ./internal/server/ -run TestActionRegistryGolden",
				s.ID, s.Risk, s.Action, s.Resource, want)
		}
		if s.Risk == RiskUnclassified || s.Risk == "" {
			t.Errorf("%s (%s) has no risk class. A new resource must be placed in risk.go's "+
				"resourceRisk deliberately; the policy layer structurally denies an unclassified action",
				s.ID, s.ACLKey)
		}
		counts[s.Risk]++
	}
	// The vacuity floor, in the shape the rest of this file uses: a registry
	// in which the money class is empty is a registry in which the CaMeL rule
	// guards nothing.
	if counts[RiskMoney] == 0 || counts[RiskDestructive] == 0 || counts[RiskGovernance] == 0 {
		t.Fatalf("risk census %v: the money, destructive and governance classes must all be non-empty", counts)
	}
	t.Logf("risk census across %d rows: %v", len(Specs), counts)
}

// TestTheClassesThePolicyForbidsAreTheOnesTheNonNegotiablesName pins the rows
// PLAN-AGENTIC §3 W2 names by kind — "no delete / ACL / period-lock /
// delegation actions" — to the two structurally forbidden classes.
func TestTheClassesThePolicyForbidsAreTheOnesTheNonNegotiablesName(t *testing.T) {
	cases := []struct {
		act  authz.Action
		res  authz.Resource
		want Risk
		why  string
	}{
		{authz.Delete, "sales", RiskDestructive, "every delete"},
		{authz.Delete, "voucher", RiskDestructive, "every delete"},
		{authz.Alter, "security", RiskGovernance, "the ACL"},
		{authz.Create, "security", RiskGovernance, "the ACL"},
		{authz.Alter, "company", RiskGovernance, "the year-end period lock rides `alter company`"},
		{authz.Create, "agent-approval", RiskGovernance, "delegation / the checker seat"},
		{authz.Display, "backup", RiskGovernance, "the whole book in one file"},
		{authz.Create, "payment", RiskMoney, "the CaMeL rule's subject"},
		{authz.Create, "sales", RiskMoney, "the CaMeL rule's subject"},
		{authz.Print, "sales", RiskExport, "print is its own authority"},
		{authz.Create, "no-such-resource", RiskUnclassified, "fail closed on the unknown"},
	}
	for _, c := range cases {
		if got := ClassifyRisk(c.act, c.res); got != c.want {
			t.Errorf("ClassifyRisk(%s, %s) = %q, want %q (%s)", c.act, c.res, got, c.want, c.why)
		}
	}
}
