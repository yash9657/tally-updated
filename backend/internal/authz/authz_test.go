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

	cases := []struct {
		name      string
		level     Level
		rules     []Rule
		req       Request
		wantAllow bool
		wantReason string // optional substring check (StepUpReason)
	}{
		{"owner full create", owner, nil, Request{Action: Create, Resource: "sales"}, true, ""},
		{"owner full delete", owner, nil, Request{Action: Delete, Resource: "voucher"}, true, ""},
		{"owner webauthn missing -> stepup", ownerWA, nil,
			Request{Action: Create, Resource: "sales", HasWebAuthn: false}, false, StepUpReason},
		{"owner webauthn present -> allow", ownerWA, nil,
			Request{Action: Create, Resource: "sales", HasWebAuthn: true}, true, ""},
		{"owner webauthn display (read) ok without stepup", ownerWA, nil,
			Request{Action: Display, Resource: "report", HasWebAuthn: false}, true, ""},
		{"owner cutoff backdated write denied", ownerCut, nil,
			Request{Action: Create, Resource: "sales", VoucherDate: date("2025-03-31")}, false, ""},
		{"owner cutoff on-or-after allowed", ownerCut, nil,
			Request{Action: Create, Resource: "sales", VoucherDate: date("2025-04-01")}, true, ""},
		{"owner cutoff does not block reads", ownerCut, nil,
			Request{Action: Display, Resource: "report", VoucherDate: date("2025-01-01")}, true, ""},

		{"data-entry default none denies", deNone, nil, Request{Action: Create, Resource: "sales"}, false, ""},
		{"data-entry allow rule allows", deNone, allowCreateSales, Request{Action: Create, Resource: "sales"}, true, ""},
		{"data-entry allow rule, other class denied", deNone, allowCreateSales, Request{Action: Create, Resource: "purchase"}, false, ""},
		{"data-entry default full allows unmatched", deFull, nil, Request{Action: Delete, Resource: "voucher"}, true, ""},

		{"disallow beats allow at equal specificity", deNone, allowAndDisallowSales, Request{Action: Create, Resource: "sales"}, false, ""},
		{"exact disallow beats wildcard allow", deFull, wildAllowExactDisallow, Request{Action: Delete, Resource: "voucher"}, false, ""},
		{"wildcard allow still applies elsewhere", deNone, wildAllowExactDisallow, Request{Action: Create, Resource: "sales"}, true, ""},
		{"exact allow beats wildcard disallow", deFull, wildDisallowExactAllow, Request{Action: Create, Resource: "sales"}, true, ""},
		{"wildcard disallow applies elsewhere", deFull, wildDisallowExactAllow, Request{Action: Delete, Resource: "voucher"}, false, ""},

		{"report:NAME exact allow", deNone, reportNamed, Request{Action: Display, Resource: "report", ResourceName: "trial-balance"}, true, ""},
		{"report bare disallow on other name", deNone, reportNamed, Request{Action: Display, Resource: "report", ResourceName: "balance-sheet"}, false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.level, tc.rules, tc.req)
			if got.Allow != tc.wantAllow {
				t.Fatalf("Evaluate allow = %v, want %v (reason %q)", got.Allow, tc.wantAllow, got.Reason)
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

// TestEvaluateService grants a service principal full access.
func TestEvaluateService(t *testing.T) {
	d := EvaluateService()
	if !d.Allow {
		t.Fatalf("service principal must be allowed, got %+v", d)
	}
}

// silence unused import in builds where uuid is only used by store_test.
var _ = uuid.Nil
