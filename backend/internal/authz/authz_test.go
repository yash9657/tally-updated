package authz

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func date(s string) *time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &d
}

// TestEvaluateMatrix is THE authz matrix gate: a table over (security level x
// action x resource x scope) covering Owner-full, Data-Entry allow/disallow,
// disallow-beats-allow at equal specificity, exact-beats-wildcard, default-deny,
// back-dated cutoff, and WebAuthn step-up. Each row is non-vacuous: a "wantFlip"
// twin asserts the decision flips when the deciding input flips (see the
// dedicated flip tests below).
func TestEvaluateMatrix(t *testing.T) {
	owner := Level{IsOwner: true, DefaultAccess: "full"}
	ownerWA := Level{IsOwner: true, DefaultAccess: "full", RequiresWebAuthn: true}
	ownerCut := Level{IsOwner: true, DefaultAccess: "full", BackdatedCutoff: date("2025-04-01")}
	deNone := Level{DefaultAccess: "none"}
	deFull := Level{DefaultAccess: "full"}

	allowCreateSales := []Rule{{Effect: Allow, Action: Create, Resource: "sales"}}
	// disallow at SAME specificity as allow -> disallow wins.
	allowAndDisallowSales := []Rule{
		{Effect: Allow, Action: Create, Resource: "sales"},
		{Effect: Disallow, Action: Create, Resource: "sales"},
	}
	// exact disallow beats wildcard allow.
	wildAllowExactDisallow := []Rule{
		{Effect: Allow, Action: "*", Resource: "*"},
		{Effect: Disallow, Action: Delete, Resource: "voucher"},
	}
	// exact allow beats wildcard disallow.
	wildDisallowExactAllow := []Rule{
		{Effect: Disallow, Action: "*", Resource: "*"},
		{Effect: Allow, Action: Create, Resource: "sales"},
	}
	reportNamed := []Rule{
		{Effect: Allow, Action: Display, Resource: "report:trial-balance"},
		{Effect: Disallow, Action: Display, Resource: "report"},
	}
	// THE COARSE DISPLAY GRANT EVERY SEEDED TENANT CARRIES. Migration 0011 seeds
	// exactly this one rule on the 'Data Entry' level, and its action is `display`.
	// The rows below pin that it does NOT satisfy a `print` request — see
	// TestADisplayGrantIsNotAPrintGrant for why this premise is load-bearing.
	allowDisplayReport := []Rule{{Effect: Allow, Action: Display, Resource: "report"}}
	allowPrintReport := []Rule{{Effect: Allow, Action: Print, Resource: "report"}}

	cases := []struct {
		name       string
		level      Level
		rules      []Rule
		req        Request
		wantAllow  bool
		wantReason string // optional substring check (StepUpReason)
		// consequence states, in DOMAIN terms, what goes wrong in the product if
		// this row fails. It is printed with the failure.
		consequence string
	}{
		{"owner full create", owner, nil, Request{Action: Create, Resource: "sales"}, true, "", ""},
		{"owner full delete", owner, nil, Request{Action: Delete, Resource: "voucher"}, true, "", ""},
		{"owner webauthn missing -> stepup", ownerWA, nil,
			Request{Action: Create, Resource: "sales", HasWebAuthn: false}, false, StepUpReason, ""},
		{"owner webauthn present -> allow", ownerWA, nil,
			Request{Action: Create, Resource: "sales", HasWebAuthn: true}, true, "", ""},
		{"owner webauthn display (read) ok without stepup", ownerWA, nil,
			Request{Action: Display, Resource: "report", HasWebAuthn: false}, true, "", ""},
		{"owner cutoff backdated write denied", ownerCut, nil,
			Request{Action: Create, Resource: "sales", VoucherDate: date("2025-03-31")}, false, "", ""},
		{"owner cutoff on-or-after allowed", ownerCut, nil,
			Request{Action: Create, Resource: "sales", VoucherDate: date("2025-04-01")}, true, "", ""},
		{"owner cutoff does not block reads", ownerCut, nil,
			Request{Action: Display, Resource: "report", VoucherDate: date("2025-01-01")}, true, "", ""},

		{"data-entry default none denies", deNone, nil, Request{Action: Create, Resource: "sales"}, false, "", ""},
		{"data-entry allow rule allows", deNone, allowCreateSales, Request{Action: Create, Resource: "sales"}, true, "", ""},
		{"data-entry allow rule, other class denied", deNone, allowCreateSales, Request{Action: Create, Resource: "purchase"}, false, "", ""},
		{"data-entry default full allows unmatched", deFull, nil, Request{Action: Delete, Resource: "voucher"}, true, "", ""},

		{"disallow beats allow at equal specificity", deNone, allowAndDisallowSales, Request{Action: Create, Resource: "sales"}, false, "", ""},
		{"exact disallow beats wildcard allow", deFull, wildAllowExactDisallow, Request{Action: Delete, Resource: "voucher"}, false, "", ""},
		{"wildcard allow still applies elsewhere", deNone, wildAllowExactDisallow, Request{Action: Create, Resource: "sales"}, true, "", ""},
		{"exact allow beats wildcard disallow", deFull, wildDisallowExactAllow, Request{Action: Create, Resource: "sales"}, true, "", ""},
		{"wildcard disallow applies elsewhere", deFull, wildDisallowExactAllow, Request{Action: Delete, Resource: "voucher"}, false, "", ""},

		{"report:NAME exact allow", deNone, reportNamed, Request{Action: Display, Resource: "report", ResourceName: "trial-balance"}, true, "", ""},
		{"report bare disallow on other name", deNone, reportNamed, Request{Action: Display, Resource: "report", ResourceName: "balance-sheet"}, false, "", ""},

		// THE ACTION MUST MATCH, AND `display` IS NOT `print`.
		//
		// This is the premise the whole report half of migration 0038 rests on: the
		// seeded Data Entry level carries `allow display report` from migration 0011
		// and NO print rule, so print of every report is default-denied and 0038 has
		// no coarse print grant to counterweight. Until now that premise was pinned
		// only by a DB-backed test in internal/server, which t.Skips — invisibly —
		// whenever Postgres is down.
		{
			name:  "a display grant does not satisfy a print request",
			level: deNone, rules: allowDisplayReport,
			req: Request{Action: Print, Resource: "report", ResourceName: "trial-balance"},
			consequence: "a display grant became a print grant: every operator in every tenant " +
				"can take the statement out of the building as a PDF, because migration 0011 " +
				"seeds `allow display report` on the seeded Data Entry level of every company " +
				"and migration 0038 deliberately grants no report print at all",
		},
		{
			// The FLIP of the row above, in the table so the deny cannot be coming
			// from the resource, the level or the default access instead.
			name:  "the same rule with the print action allows it",
			level: deNone, rules: allowPrintReport,
			req:       Request{Action: Print, Resource: "report", ResourceName: "trial-balance"},
			wantAllow: true,
			consequence: "the deny above is not attributable to the ACTION at all, so it proves " +
				"nothing about display-versus-print and the whole matrix row is vacuous",
		},
		{
			// And the other direction: a print grant is not a licence to read on
			// screen. An owner who grants only print must not thereby grant display.
			name:  "a print grant does not satisfy a display request",
			level: deNone, rules: allowPrintReport,
			req: Request{Action: Display, Resource: "report", ResourceName: "trial-balance"},
			consequence: "action matching is one-directional or absent, so every rule in the " +
				"product grants more than the verb it names",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.level, tc.rules, tc.req)
			if got.Allow != tc.wantAllow {
				because := ""
				if tc.consequence != "" {
					because = "\nCONSEQUENCE: " + tc.consequence
				}
				t.Fatalf("Evaluate allow = %v, want %v (reason %q)%s",
					got.Allow, tc.wantAllow, got.Reason, because)
			}
			if tc.wantReason != "" && got.Reason != tc.wantReason {
				t.Fatalf("Evaluate reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if !got.Allow && got.HTTPStatus != 403 {
				t.Fatalf("deny HTTPStatus = %d, want 403", got.HTTPStatus)
			}
		})
	}
}

// TestNonVacuous_DisallowFlip proves the disallow-beats-allow row would FLIP if
// the disallow rule were removed: same request, allow-only rules -> allow.
func TestNonVacuous_DisallowFlip(t *testing.T) {
	level := Level{DefaultAccess: "none"}
	req := Request{Action: Create, Resource: "sales"}
	withDisallow := []Rule{
		{Effect: Allow, Action: Create, Resource: "sales"},
		{Effect: Disallow, Action: Create, Resource: "sales"},
	}
	if Evaluate(level, withDisallow, req).Allow {
		t.Fatal("expected deny with the disallow rule present")
	}
	withoutDisallow := []Rule{{Effect: Allow, Action: Create, Resource: "sales"}}
	if !Evaluate(level, withoutDisallow, req).Allow {
		t.Fatal("expected the decision to FLIP to allow once the disallow rule is removed (vacuous test otherwise)")
	}
}

// TestNonVacuous_CutoffFlip proves the cutoff row flips on the date alone.
func TestNonVacuous_CutoffFlip(t *testing.T) {
	level := Level{IsOwner: true, DefaultAccess: "full", BackdatedCutoff: date("2025-04-01")}
	before := Request{Action: Create, Resource: "sales", VoucherDate: date("2025-03-31")}
	after := Request{Action: Create, Resource: "sales", VoucherDate: date("2025-04-01")}
	if Evaluate(level, nil, before).Allow {
		t.Fatal("expected deny for a date before the cutoff")
	}
	if !Evaluate(level, nil, after).Allow {
		t.Fatal("expected the decision to FLIP to allow on/after the cutoff (vacuous test otherwise)")
	}
}

// TestNonVacuous_WebAuthnFlip proves the step-up row flips on the AMR alone.
func TestNonVacuous_WebAuthnFlip(t *testing.T) {
	level := Level{IsOwner: true, DefaultAccess: "full", RequiresWebAuthn: true}
	noWA := Request{Action: Alter, Resource: "sales", HasWebAuthn: false}
	withWA := Request{Action: Alter, Resource: "sales", HasWebAuthn: true}
	d := Evaluate(level, nil, noWA)
	if d.Allow || d.Reason != StepUpReason {
		t.Fatalf("expected STEPUP_REQUIRED deny without webauthn, got %+v", d)
	}
	if !Evaluate(level, nil, withWA).Allow {
		t.Fatal("expected the decision to FLIP to allow once webauthn AMR is present (vacuous test otherwise)")
	}
}

// TestADisplayGrantIsNotAPrintGrant is the hermetic, NO-DATABASE gate on the
// premise migration 0038's report half is built on.
//
// Tally models "may see it on screen" and "may take it out of the building" as
// different authorities, and this product follows it: `display` and `print` are
// separate actions. Migration 0011 seeds ONE coarse report rule on the Data Entry
// level of EVERY company — `allow display report` — and migration 0038 grants that
// level no report print at all, on the strength of the PDP matching the action.
// If the action matcher ever ignored the verb (or treated print as a kind of
// display), that single seeded rule would silently become a print grant in every
// tenant: the payroll register, the priced supplier reconciliation and the
// company's own cost prices, as PDFs, with nobody having decided anything.
//
// It is asserted HERE, hermetically, and not only through the HTTP surface,
// because every DB-backed test in internal/server calls t.Skip when Postgres is
// unavailable and `go test` still prints ok and exits 0 — so on a bad day the
// load-bearing claim was guarded by nothing at all.
func TestADisplayGrantIsNotAPrintGrant(t *testing.T) {
	level := Level{DefaultAccess: "none"}
	req := Request{Action: Print, Resource: "report", ResourceName: "trial-balance"}

	if d := Evaluate(level, []Rule{{Effect: Allow, Action: Display, Resource: "report"}}, req); d.Allow {
		t.Fatalf("`allow display report` ALLOWED a print request (%q): a display grant became a "+
			"print grant, so migration 0011's coarse rule hands every Data Entry operator in "+
			"every tenant a PDF of the payroll register, the priced supplier reconciliation and "+
			"the company's own cost prices — by inheritance, with nobody deciding to", d.Reason)
	}
	// THE FLIP, so the deny above is attributable to the ACTION and not to the
	// resource, the report name, the level or the default access.
	if d := Evaluate(level, []Rule{{Effect: Allow, Action: Print, Resource: "report"}}, req); !d.Allow {
		t.Fatalf("`allow print report` DENIED the same print request (%q): the deny above is not "+
			"about the action at all, so this test proves nothing about display versus print — and "+
			"separately, an owner who deliberately grants report print cannot deliver it", d.Reason)
	}
	// The narrow form an owner would actually write for one report, and the report
	// name it does not name.
	oneReport := []Rule{{Effect: Allow, Action: Print, Resource: "report:outstandings"}}
	if d := Evaluate(level, oneReport, Request{Action: Print, Resource: "report", ResourceName: "outstandings"}); !d.Allow {
		t.Fatalf("`allow print report:outstandings` denied print of the outstandings (%q): the one "+
			"row migration 0038's header promises an owner can write to let an operator print the "+
			"ageing they chase does not work", d.Reason)
	}
	if d := Evaluate(level, oneReport, Request{Action: Print, Resource: "report", ResourceName: "payroll-register"}); d.Allow {
		t.Fatalf("`allow print report:outstandings` also allowed print of the payroll register "+
			"(%q): a rule naming ONE report reached another, so an owner granting the ageing hands "+
			"over every employee's pay with it", d.Reason)
	}
}

// TestEvaluateService grants a service principal full access.
func TestEvaluateService(t *testing.T) {
	d := EvaluateService()
	if !d.Allow {
		t.Fatalf("service principal must be allowed, got %+v", d)
	}
}

// silence unused import in builds where uuid is only used by store_test.
var _ = uuid.Nil
