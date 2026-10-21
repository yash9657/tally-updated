package profile

import "testing"

func TestTenantCardRequiresEverySourceReport(t *testing.T) {
	reports := map[string]bool{}
	if !CanReadTenantCard(func(report string) bool { reports[report] = true; return true }) {
		t.Fatal("an unrestricted reader lost the tenant card")
	}
	for report := range reports {
		if CanReadTenantCard(func(r string) bool { return r != report }) {
			t.Errorf("card is readable without source report %s", report)
		}
	}
}

func TestTenantCardFailsClosedForUndeclaredSources(t *testing.T) {
	original := Facts
	t.Cleanup(func() { Facts = original })
	for _, extra := range []FactDef{
		{Key: "new_section", Job: "new.job", Statistic: true},
		{Key: "new_section", Job: JobTenant},
	} {
		Facts = append(append([]FactDef(nil), original...), extra)
		if CanReadTenantCard(func(string) bool { return true }) {
			t.Errorf("card accepted undeclared source %+v", extra)
		}
	}
}
