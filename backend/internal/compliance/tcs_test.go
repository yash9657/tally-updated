package compliance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// tcsCorpusCase mirrors the JSON shape of corpora/tcs/*.json. It reuses
// corporaSubdir from tds_test.go (same package, same declaration scope).
type tcsCorpusCase struct {
	ID             string `json:"id"`
	Description    string `json:"description"`
	RulesetVersion string `json:"rulesetVersion"`
	Input          struct {
		Section       string `json:"section"`
		PANAvailable  bool   `json:"panAvailable"`
		TaxableAmount string `json:"taxableAmount"`
	} `json:"input"`
	Expected struct {
		TCS       string `json:"tcs"`
		RatePct   string `json:"ratePct"`
		Threshold string `json:"threshold"`
		Basis     string `json:"basis"`
		Applied   bool   `json:"applied"`
	} `json:"expected"`
}

func TestTCSDerive_GoldenCorpus(t *testing.T) {
	dir := corporaSubdir(t, "tcs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpora dir: %v", err)
	}

	count := 0
	seenSections := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var c tcsCorpusCase
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("unmarshal %s: %v", path, err)
		}
		seenSections[c.Input.Section] = true

		t.Run(c.ID, func(t *testing.T) {
			if len(c.Description) < 40 {
				t.Errorf("description too short to carry a citation: %q", c.Description)
			}
			rs, err := LoadTCSRuleSet(c.RulesetVersion)
			if err != nil {
				t.Fatalf("load tcs ruleset %q: %v", c.RulesetVersion, err)
			}
			got, err := TCSDerive(TCSInput{
				Section:       c.Input.Section,
				PANAvailable:  c.Input.PANAvailable,
				TaxableAmount: mustDec(t, c.Input.TaxableAmount),
			}, rs)
			if err != nil {
				t.Fatalf("TCSDerive: %v", err)
			}
			if want := mustDec(t, c.Expected.TCS); !got.TCS.Equal(want) {
				t.Errorf("tcs: got %s, want %s", got.TCS.StringFixed(4), want.StringFixed(4))
			}
			if want := mustDec(t, c.Expected.RatePct); !got.RatePct.Equal(want) {
				t.Errorf("ratePct: got %s, want %s", got.RatePct, want)
			}
			if want := mustDec(t, c.Expected.Threshold); !got.Threshold.Equal(want) {
				t.Errorf("threshold: got %s, want %s", got.Threshold, want)
			}
			if string(got.Basis) != c.Expected.Basis {
				t.Errorf("basis: got %q, want %q", got.Basis, c.Expected.Basis)
			}
			if got.Applied != c.Expected.Applied {
				t.Errorf("applied: got %v, want %v", got.Applied, c.Expected.Applied)
			}
			if !got.Applied && !got.TCS.IsZero() {
				t.Errorf("below-threshold case derived non-zero TCS %s", got.TCS)
			}
		})
		count++
	}
	if count == 0 {
		t.Fatal("no TCS corpus cases were read")
	}
	if count < 2 {
		t.Fatalf("only %d TCS corpus cases; contract requires at least 2", count)
	}
	rs, err := LoadTCSRuleSet("tcs-2026-04")
	if err != nil {
		t.Fatalf("load tcs-2026-04: %v", err)
	}
	for s := range rs.Sections {
		if !seenSections[s] {
			t.Errorf("section %q is in the ruleset but has no corpus fixture", s)
		}
	}
	t.Logf("validated %d TCS corpus cases", count)
}

// TestTCSNoPANIsNot206AA pins the divergence flagged in tcs.go's header: the
// collection-side no-PAN rule is s.206CC (higher of TWICE the rate, or 5%), NOT
// s.206AA's flat 20% floor. A copy-paste of the TDS rule would make BOTH cases
// below come out at 20%.
func TestTCSNoPANIsNot206AA(t *testing.T) {
	rs, err := LoadTCSRuleSet("tcs-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// 2% scrap: 2 x 2% = 4%, which is below the 5% floor -> 5%.
	scrap, err := TCSDerive(TCSInput{Section: "206C(1)-SCRAP", TaxableAmount: pct("100000")}, rs)
	if err != nil {
		t.Fatalf("derive scrap: %v", err)
	}
	if !scrap.RatePct.Equal(pct("5")) {
		t.Fatalf("206CC floor: got %s, want 5 (NOT 20 — that is 206AA)", scrap.RatePct)
	}
	// A synthetic 4% section: 2 x 4% = 8%, which EXCEEDS the 5% floor, so the
	// doubling — not the floor — governs. A flat-floor implementation returns 5.
	synth := TCSRuleSet{
		Version:         "test-synthetic",
		Sections:        map[string]tcsSectionRule{"S": {RatePct: pct("4"), Threshold: pct("0"), Basis: BasisPerTxn}},
		NoPANMultiplier: pct("2"),
		NoPANFloorPct:   pct("5"),
	}
	got, err := TCSDerive(TCSInput{Section: "S", TaxableAmount: pct("100000")}, synth)
	if err != nil {
		t.Fatalf("derive synthetic: %v", err)
	}
	if !got.RatePct.Equal(pct("8")) {
		t.Fatalf("206CC doubling: got %s, want 8", got.RatePct)
	}
	if !got.TCS.Equal(pct("8000")) {
		t.Fatalf("synthetic tcs: got %s, want 8000", got.TCS)
	}
	// With a PAN the rate is untouched.
	withPAN, err := TCSDerive(TCSInput{Section: "206C(1)-SCRAP", PANAvailable: true, TaxableAmount: pct("100000")}, rs)
	if err != nil {
		t.Fatalf("derive withPAN: %v", err)
	}
	if !withPAN.RatePct.Equal(pct("2")) {
		t.Fatalf("with PAN: got %s, want 2", withPAN.RatePct)
	}
}

// TestTCSScrapRateIs2Pct pins the corrected rate on its own, so the reason for
// the value survives independently of the corpus fixtures.
//
// s.394(1) [Table: Sl. No. 4] of the Income-tax Act 2025 as amended by the
// Finance Act 2026: "Sale of scrap. Seller. [2%]", footnote 88 "Substituted for
// '1%' by the Finance Act, 2026, w.e.f. 1-4-2026".
//
// This shipped at 1% and under-collected by half on every scrap sale from
// 1 April 2026. The earlier verification pass (docs/design/t6-model.md §9.1)
// marked it correct because it read the Act AS ENACTED rather than as amended.
func TestTCSScrapRateIs2Pct(t *testing.T) {
	rs, err := LoadTCSRuleSet("tcs-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := TCSDerive(TCSInput{
		Section: "206C(1)-SCRAP", PANAvailable: true, TaxableAmount: pct("500000"),
	}, rs)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if !got.RatePct.Equal(pct("2")) {
		t.Fatalf("scrap rate: got %s, want 2 (s.394(1) Sl. No. 4 as amended by FA 2026)", got.RatePct)
	}
	if !got.TCS.Equal(pct("10000")) {
		t.Fatalf("scrap tcs on 5,00,000: got %s, want 10000", got.TCS)
	}
}

// TestTCSNoPANCeiling pins s.397(2)(b)(ii)'s "not exceeding 20%".
//
// It CANNOT be expressed as a corpus fixture: both shipped sections are at or
// below 2%, so doubling never reaches the ceiling and the shipped table can
// never exercise it. A synthetic high-rate ruleset is the only way to reach the
// clause — which is exactly why the ceiling was missing until now.
//
// s.394(1) [Table: Sl. No. 7(b)] is itself a 20% row, so the case is real and
// not hypothetical: doubling it would yield 40% against a statutory maximum
// of 20%.
func TestTCSNoPANCeiling(t *testing.T) {
	// A 20% section, as s.394(1) Sl. No. 7(b) genuinely is.
	synth := TCSRuleSet{
		Version:         "test-synthetic-ceiling",
		Sections:        map[string]tcsSectionRule{"HIGH": {RatePct: pct("20"), Threshold: pct("0"), Basis: BasisPerTxn}},
		NoPANMultiplier: pct("2"),
		NoPANFloorPct:   pct("5"),
		NoPANCeilingPct: pct("20"),
	}
	got, err := TCSDerive(TCSInput{Section: "HIGH", TaxableAmount: pct("100000")}, synth)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if !got.RatePct.Equal(pct("20")) {
		t.Fatalf("no-PAN rate: got %s, want 20 (capped); doubling would give 40", got.RatePct)
	}
	if !got.TCS.Equal(pct("20000")) {
		t.Fatalf("no-PAN tcs: got %s, want 20000", got.TCS)
	}

	// The ceiling must bound only the NO-PAN computation. A collectee WITH a
	// PAN on a 20% section pays exactly 20% — the same number, reached without
	// the clause, so assert the rate is untouched rather than merely equal.
	withPAN, err := TCSDerive(TCSInput{Section: "HIGH", PANAvailable: true, TaxableAmount: pct("100000")}, synth)
	if err != nil {
		t.Fatalf("derive withPAN: %v", err)
	}
	if !withPAN.RatePct.Equal(pct("20")) {
		t.Fatalf("with PAN on a 20%% section: got %s, want 20", withPAN.RatePct)
	}

	// A ruleset that declares NO ceiling must keep the uncapped behaviour
	// rather than silently collapsing every no-PAN rate to zero.
	noCeiling := synth
	noCeiling.NoPANCeilingPct = decimal.Zero
	uncapped, err := TCSDerive(TCSInput{Section: "HIGH", TaxableAmount: pct("100000")}, noCeiling)
	if err != nil {
		t.Fatalf("derive uncapped: %v", err)
	}
	if !uncapped.RatePct.Equal(pct("40")) {
		t.Fatalf("undeclared ceiling must not cap: got %s, want 40", uncapped.RatePct)
	}
}

// TestTCSRegistryOmits206C1H pins the deliberate omission. 206C(1H) (TCS on
// sale of goods) was omitted w.e.f. 1-Apr-2025; shipping it would collect a tax
// that no longer exists. If someone "completes" the table, this fails loudly.
func TestTCSRegistryOmits206C1H(t *testing.T) {
	rs, err := LoadTCSRuleSet("tcs-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for name := range rs.Sections {
		if strings.Contains(strings.ToUpper(name), "1H") {
			t.Fatalf("206C(1H) was omitted w.e.f. 1-Apr-2025 and must not ship; found section %q", name)
		}
	}
	if _, err := TCSDerive(TCSInput{Section: "206C(1H)", TaxableAmount: pct("6000000")}, rs); !errors.Is(err, ErrUnknownSection) {
		t.Fatalf("206C(1H) must be unknown, got %v", err)
	}
}

func TestTCSDerive_Rejects(t *testing.T) {
	rs, err := LoadTCSRuleSet("tcs-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := TCSDerive(TCSInput{Section: "206C(1)-SCRAP", TaxableAmount: pct("-1")}, rs); err == nil {
		t.Fatal("negative amount must be rejected")
	}
	if _, err := TCSDerive(TCSInput{Section: "206C(1)-SCRAP", TaxableAmount: pct("1")}, TCSRuleSet{}); !errors.Is(err, ErrUnknownRuleset) {
		t.Fatalf("empty ruleset: want ErrUnknownRuleset, got %v", err)
	}
	if _, err := LoadTCSRuleSet("tcs-1999-04"); !errors.Is(err, ErrUnknownRuleset) {
		t.Fatalf("want ErrUnknownRuleset, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// s.394(2) manufacturing declarations — §3.1 gap 4
// ---------------------------------------------------------------------------

// validScrapDeclaration is a declaration that meets every condition, so each
// test below can break exactly one thing and attribute the outcome to it.
func validScrapDeclaration() *TCSDeclaration {
	return &TCSDeclaration{
		Section:          "206C(1)-SCRAP",
		BuyerIsResident:  true,
		ForManufacturing: true,
		NotForTrading:    true,
		BuyerPANValid:    true,
	}
}

// TestValidDeclarationExemptsScrap is the happy path of gap 4.
func TestValidDeclarationExemptsScrap(t *testing.T) {
	rs, err := LoadTCSRuleSet("tcs-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// CONTROL FIRST: the same sale with NO declaration collects. Without it, an
	// engine that never collects on scrap would pass the exemption assertion.
	base, err := TCSDerive(TCSInput{
		Section: "206C(1)-SCRAP", PANAvailable: true, TaxableAmount: pct("500000"),
	}, rs)
	if err != nil {
		t.Fatalf("derive control: %v", err)
	}
	if !base.Applied || !base.TCS.Equal(pct("10000")) {
		t.Fatalf("CONTROL: scrap with no declaration collected %s (applied=%v), want 10000 at 2%%",
			base.TCS, base.Applied)
	}
	if base.Declaration != DeclarationNone {
		t.Errorf("no declaration furnished, but outcome = %q, want NONE", base.Declaration)
	}

	got, err := TCSDerive(TCSInput{
		Section: "206C(1)-SCRAP", PANAvailable: true, TaxableAmount: pct("500000"),
		Declaration: validScrapDeclaration(),
	}, rs)
	if err != nil {
		t.Fatalf("derive with declaration: %v", err)
	}
	if got.Applied || !got.TCS.IsZero() {
		t.Errorf("a valid s.394(2) declaration must exempt the collection entirely; collected %s "+
			"(applied=%v)", got.TCS, got.Applied)
	}
	if got.Declaration != DeclarationExempt {
		t.Errorf("outcome = %q, want EXEMPT. A zero with outcome NONE is indistinguishable from "+
			"a below-threshold sale, and the two are different facts in the return", got.Declaration)
	}
}

// TestDeclarationWithoutPANTriggersThePenalRate is the coupling that is easy to
// get half-right, and the one nothing in the gap list records.
//
// s.397(2)(f)(i): a declaration without a valid PAN "becomes invalid". (g): the
// collector then collects per clause (b)(ii) — the PENAL no-PAN rate. So the
// buyer is WORSE off than if they had never furnished one:
//
//	valid declaration      -> 0%
//	no declaration at all  -> 2%   (the section rate)
//	invalid declaration    -> 5%   (max(2 x 2%, 5%), capped at 20%)
//
// An implementation that treats an invalid declaration as "no declaration"
// collects 2% and is wrong; one that treats it as "still a declaration"
// collects nothing and is worse.
func TestDeclarationWithoutPANTriggersThePenalRate(t *testing.T) {
	rs, err := LoadTCSRuleSet("tcs-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	d := validScrapDeclaration()
	d.BuyerPANValid = false

	got, err := TCSDerive(TCSInput{
		// PANAvailable is TRUE on the collectee record on purpose: the ONLY
		// reason the penal rate applies here is the declaration's own invalidity
		// under s.397(2)(f)(i). If the engine keyed off PANAvailable alone this
		// would come out at 2%.
		Section: "206C(1)-SCRAP", PANAvailable: true, TaxableAmount: pct("500000"),
		Declaration: d,
	}, rs)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got.Declaration != DeclarationInvalidNoPAN {
		t.Errorf("outcome = %q, want INVALID_NO_PAN", got.Declaration)
	}
	if !got.RatePct.Equal(pct("5")) {
		t.Errorf("rate = %s%%, want 5%%. s.397(2)(g) sends an invalid declaration down the "+
			"clause (b)(ii) path: max(2 x 2%%, 5%%) = 5%%. A 2%% here means the invalid "+
			"declaration was treated as no declaration; a 0%% means it was honoured anyway",
			got.RatePct)
	}
	if !got.TCS.Equal(pct("25000")) {
		t.Errorf("collected %s, want 25000 (5%% of 5,00,000)", got.TCS)
	}
	if !got.Applied {
		t.Error("an invalid declaration must still COLLECT; applied=false means the exemption " +
			"was granted to a declaration the statute says is invalid")
	}
}

// TestDeclarationRefusedOnMotorVehicles pins the Sl. Nos. 1-5 boundary, which
// our two shipped sections straddle.
func TestDeclarationRefusedOnMotorVehicles(t *testing.T) {
	rs, err := LoadTCSRuleSet("tcs-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	d := validScrapDeclaration()
	d.Section = "206C(1F)-MV"

	_, err = TCSDerive(TCSInput{
		Section: "206C(1F)-MV", PANAvailable: true, TaxableAmount: pct("1500000"),
		Declaration: d,
	}, rs)
	if !errors.Is(err, ErrDeclarationNotAvailable) {
		t.Fatalf("a declaration against a MOTOR VEHICLE returned %v, want "+
			"ErrDeclarationNotAvailable: s.394(2) reaches Table Sl. Nos. 1 to 5 and motor "+
			"vehicles are Sl. No. 6. Silently exempting would forgo 1%% of every car sold, and "+
			"silently collecting would contradict a document the operator holds", err)
	}
}

// TestDeclarationConditionsAreConjunctive walks each condition of s.394(2) and
// s.397(2)(f)(i) one at a time.
func TestDeclarationConditionsAreConjunctive(t *testing.T) {
	rs, err := LoadTCSRuleSet("tcs-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, tc := range []struct {
		name   string
		break_ func(*TCSDeclaration)
	}{
		{"buyer is not resident", func(d *TCSDeclaration) { d.BuyerIsResident = false }},
		{"goods not for manufacturing", func(d *TCSDeclaration) { d.ForManufacturing = false }},
		{"goods also for trading", func(d *TCSDeclaration) { d.NotForTrading = false }},
		{"furnished for another section", func(d *TCSDeclaration) { d.Section = "206C(1F)-MV" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := validScrapDeclaration()
			tc.break_(d)
			_, err := TCSDerive(TCSInput{
				Section: "206C(1)-SCRAP", PANAvailable: true, TaxableAmount: pct("500000"),
				Declaration: d,
			}, rs)
			if !errors.Is(err, ErrDeclarationNotAvailable) {
				t.Errorf("%s: got %v, want ErrDeclarationNotAvailable. s.394(2)'s conditions are "+
					"conjunctive and a declaration failing any one of them exempts nothing",
					tc.name, err)
			}
		})
	}
}
