package compliance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"
)

// tdsCorpusCase mirrors the JSON shape of corpora/tds/*.json.
//
// NOTE: the TDS fixtures deliberately live in corpora/tds (a NEW sibling of
// corpora/tax) rather than corpora/tax/tds-*.json. Three independent consumers
// glob corpora/tax/*.json and assume the GST case shape — this package's
// TestTaxDerive_GoldenCorpus, corpora/validate.py's validate_tax, and insightd's
// test_gst_oracle_reconcile.py — and two of them are outside this workstream's
// file set. A separate directory keeps every one of them untouched and keeps
// MIN_COUNTS["tax"] a meaningful GST-fixture floor.
type tdsCorpusCase struct {
	ID             string `json:"id"`
	Description    string `json:"description"`
	RulesetVersion string `json:"rulesetVersion"`
	Input          struct {
		Section       string `json:"section"`
		DeducteeType  string `json:"deducteeType"`
		PANAvailable  bool   `json:"panAvailable"`
		TaxableAmount string `json:"taxableAmount"`
	} `json:"input"`
	Expected struct {
		TDS       string `json:"tds"`
		RatePct   string `json:"ratePct"`
		Threshold string `json:"threshold"`
		Basis     string `json:"basis"`
		Applied   bool   `json:"applied"`
	} `json:"expected"`
}

// corporaSubdir locates <repo>/corpora/<name> by walking up from the package
// directory. It is parameterised (unlike corporaTaxDir in tax_test.go, which is
// hard-wired to corpora/tax) so the TDS and TCS suites share one implementation.
func corporaSubdir(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "corpora", name)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate corpora/%s above %q", name, dir)
	return ""
}

func TestTDSDerive_GoldenCorpus(t *testing.T) {
	dir := corporaSubdir(t, "tds")
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
		var c tdsCorpusCase
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("unmarshal %s: %v", path, err)
		}
		seenSections[c.Input.Section] = true

		t.Run(c.ID, func(t *testing.T) {
			// Every fixture must carry its statutory citation: the corpus is the
			// oracle and an uncited rate is unreviewable.
			if len(c.Description) < 40 {
				t.Errorf("description too short to carry a citation: %q", c.Description)
			}
			rs, err := LoadTDSRuleSet(c.RulesetVersion)
			if err != nil {
				t.Fatalf("load tds ruleset %q: %v", c.RulesetVersion, err)
			}
			got, err := TDSDerive(TDSInput{
				Section:       c.Input.Section,
				DeducteeType:  c.Input.DeducteeType,
				PANAvailable:  c.Input.PANAvailable,
				TaxableAmount: mustDec(t, c.Input.TaxableAmount),
			}, rs)
			if err != nil {
				t.Fatalf("TDSDerive: %v", err)
			}
			if want := mustDec(t, c.Expected.TDS); !got.TDS.Equal(want) {
				t.Errorf("tds: got %s, want %s", got.TDS.StringFixed(4), want.StringFixed(4))
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
			// A non-applied case must derive exactly zero, never a stale amount.
			if !got.Applied && !got.TDS.IsZero() {
				t.Errorf("below-threshold case derived non-zero TDS %s", got.TDS)
			}
		})
		count++
	}
	if count == 0 {
		t.Fatal("no TDS corpus cases were read")
	}
	// The §8.2 acceptance floor. Guards against a fixture being deleted to make
	// a failing gate pass.
	if count < 8 {
		t.Fatalf("only %d TDS corpus cases; contract requires at least 8", count)
	}
	// Every shipped section must be exercised by at least one fixture, so a rate
	// added to the registry without a citation-carrying fixture fails the gate.
	rs, err := LoadTDSRuleSet("tds-2026-04")
	if err != nil {
		t.Fatalf("load tds-2026-04: %v", err)
	}
	for _, s := range TDSSections(rs) {
		if !seenSections[s] {
			t.Errorf("section %q is in the ruleset but has no corpus fixture", s)
		}
	}
	t.Logf("validated %d TDS corpus cases", count)
}

// TestTDSDerive_206AAIsFloorNotReplacement pins s.206AA's "higher of" semantics
// using a SYNTHETIC ruleset whose rate exceeds the floor. A flat-20% override
// (the naive reading) would silently REDUCE the deduction here.
func TestTDSDerive_206AAIsFloorNotReplacement(t *testing.T) {
	rs := TDSRuleSet{
		Version: "test-synthetic",
		Sections: map[string]sectionRule{
			"HIGH": {RateIndHUF: pct("30"), RateOther: pct("30"), PerTxnThreshold: pct("0"), Basis: BasisPerTxn},
			"LOW":  {RateIndHUF: pct("2"), RateOther: pct("2"), PerTxnThreshold: pct("0"), Basis: BasisPerTxn},
		},
		NoPANFloorPct: pct("20"),
	}
	high, err := TDSDerive(TDSInput{Section: "HIGH", DeducteeType: DeducteeOther, TaxableAmount: pct("1000")}, rs)
	if err != nil {
		t.Fatalf("derive HIGH: %v", err)
	}
	if !high.RatePct.Equal(pct("30")) {
		t.Fatalf("206AA must not reduce a >20%% rate: got %s, want 30", high.RatePct)
	}
	if !high.TDS.Equal(pct("300")) {
		t.Fatalf("HIGH tds: got %s, want 300", high.TDS)
	}
	low, err := TDSDerive(TDSInput{Section: "LOW", DeducteeType: DeducteeOther, TaxableAmount: pct("1000")}, rs)
	if err != nil {
		t.Fatalf("derive LOW: %v", err)
	}
	if !low.RatePct.Equal(pct("20")) {
		t.Fatalf("206AA must raise a <20%% rate to the floor: got %s, want 20", low.RatePct)
	}
}

// TestTDSDerive_ThresholdIsStrictlyGreater pins the "does not exceed" wording:
// an amount exactly equal to the limit attracts NO deduction, one paisa more
// attracts a deduction on the FULL amount (not just the excess).
func TestTDSDerive_ThresholdIsStrictlyGreater(t *testing.T) {
	rs, err := LoadTDSRuleSet("tds-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	at, err := TDSDerive(TDSInput{Section: "194C", DeducteeType: DeducteeOther, PANAvailable: true, TaxableAmount: pct("30000.0000")}, rs)
	if err != nil {
		t.Fatalf("derive at: %v", err)
	}
	if at.Applied || !at.TDS.IsZero() {
		t.Fatalf("exactly at threshold must not deduct: applied=%v tds=%s", at.Applied, at.TDS)
	}
	over, err := TDSDerive(TDSInput{Section: "194C", DeducteeType: DeducteeOther, PANAvailable: true, TaxableAmount: pct("30000.0001")}, rs)
	if err != nil {
		t.Fatalf("derive over: %v", err)
	}
	if !over.Applied {
		t.Fatal("one paisa over the threshold must deduct")
	}
	// 2% of 30000.0001 = 600.000002 -> 600.0000 at 4dp half-up. The deduction is
	// on the whole sum, not on the excess.
	if !over.TDS.Equal(pct("600.0000")) {
		t.Fatalf("over-threshold tds: got %s, want 600.0000", over.TDS)
	}
}

func TestTDSDerive_Rejects(t *testing.T) {
	rs, err := LoadTDSRuleSet("tds-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := TDSDerive(TDSInput{Section: "194ZZ", DeducteeType: DeducteeOther, TaxableAmount: pct("1")}, rs); !errors.Is(err, ErrUnknownSection) {
		t.Fatalf("unknown section: want ErrUnknownSection, got %v", err)
	}
	if _, err := TDSDerive(TDSInput{Section: "194C", DeducteeType: "PARTNERSHIP?", TaxableAmount: pct("1")}, rs); err == nil {
		t.Fatal("invalid deductee type must be rejected")
	}
	if _, err := TDSDerive(TDSInput{Section: "194C", DeducteeType: DeducteeOther, TaxableAmount: pct("-1")}, rs); err == nil {
		t.Fatal("negative amount must be rejected")
	}
	if _, err := TDSDerive(TDSInput{Section: "194C", DeducteeType: DeducteeOther, TaxableAmount: pct("1")}, TDSRuleSet{}); !errors.Is(err, ErrUnknownRuleset) {
		t.Fatalf("empty ruleset: want ErrUnknownRuleset, got %v", err)
	}
}

func TestLoadTDSRuleSet_Unknown(t *testing.T) {
	if _, err := LoadTDSRuleSet("tds-1999-04"); !errors.Is(err, ErrUnknownRuleset) {
		t.Fatalf("want ErrUnknownRuleset, got %v", err)
	}
}

// TestTDSRuleSet_DeducteeSplitOnlyWhere194C documents (and pins) the design
// note: 194C is the only v1 section whose rate depends on the deductee type.
func TestTDSRuleSet_DeducteeSplitOnlyWhere194C(t *testing.T) {
	rs, err := LoadTDSRuleSet("tds-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for name, rule := range rs.Sections {
		split := !rule.RateIndHUF.Equal(rule.RateOther)
		if name == "194C" && !split {
			t.Errorf("194C must split by deductee type (1%% ind/HUF vs 2%% other)")
		}
		if name != "194C" && split {
			t.Errorf("section %q splits by deductee type; v1 pins that only 194C does", name)
		}
	}
	// Money must never be a float: every rate/threshold is a decimal built from
	// a string literal. A zero-valued threshold decimal would silently make a
	// section unconditionally applicable, so pin the non-zero ones too.
	if !rs.Sections["194C"].PerTxnThreshold.Equal(decimal.RequireFromString("30000")) {
		t.Errorf("194C threshold drifted: %s", rs.Sections["194C"].PerTxnThreshold)
	}
}

// TestEveryTDSSectionHasAReturnCode makes the empty-code branch of TDSDerive
// unreachable for a shipped ruleset.
//
// TDSDerive tolerates a missing code — the DEDUCTION is still right, and
// refusing to compute it would be the wrong remedy for a gap in a static
// reporting table. This test is what turns that tolerance from a silent hole
// into a build failure: add a section without adding its Annexure 2 code and
// the suite says so, rather than a quarter's return going out with a blank
// field.
func TestEveryTDSSectionHasAReturnCode(t *testing.T) {
	rs, err := LoadTDSRuleSet("tds-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for name := range rs.Sections {
		for _, dt := range []string{DeducteeIndHUF, DeducteeOther} {
			code, err := TDSReturnCode(name, dt)
			if err != nil {
				t.Errorf("section %q (%s) has no Form 140 payment code: %v — a deduction that "+
					"cannot be reported is one the deductee gets no credit for", name, dt, err)
				continue
			}
			if len(code) != 4 {
				t.Errorf("section %q (%s) code %q is not four digits; Annexure 2 codes are "+
					"four-digit", name, dt, code)
			}
		}
	}
}

// TestDirectorsFeesAndProfessionalFeesShareALimbAndDifferByCode is THE test for
// §3.1 gap 2, and it is deliberately shaped around the trap rather than around
// the feature.
//
// s.393(1) [Table: Sl. No. 6(iii).D(b)] covers BOTH professional services and a
// director's remuneration, at the SAME 10%. Everything an engine can derive from
// the section is therefore identical between them. What differs is the Form 140
// payment code — 1027 against 1028 — and the threshold, which is ₹50,000 for
// professional services and Nil for a director.
//
// So an implementation that derives the code from the section passes every
// rate assertion and still files directors' fees as professional fees.
func TestDirectorsFeesAndProfessionalFeesShareALimbAndDifferByCode(t *testing.T) {
	rs, err := LoadTDSRuleSet("tds-2026-04")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// The premise: the two really are the same rate. If this ever fails, the
	// rest of this test is no longer testing what it claims to.
	p, d := rs.Sections["194J-P"], rs.Sections["194J-D"]
	if !p.RateOther.Equal(d.RateOther) {
		t.Fatalf("PREMISE BROKEN: 194J-P is %s%% and 194J-D is %s%%. They are the same statutory "+
			"limb 6(iii).D(b) and this test's whole point is that the rate cannot tell them "+
			"apart", p.RateOther, d.RateOther)
	}

	// The codes must differ.
	pc, err := TDSReturnCode("194J-P", DeducteeOther)
	if err != nil {
		t.Fatalf("professional code: %v", err)
	}
	dc, err := TDSReturnCode("194J-D", DeducteeOther)
	if err != nil {
		t.Fatalf("director code: %v", err)
	}
	if pc != "1027" {
		t.Errorf("professional services code = %q, want 1027 (Form 140 Annexure 2)", pc)
	}
	if dc != "1028" {
		t.Errorf("directors' fees code = %q, want 1028 (Form 140 Annexure 2)", dc)
	}
	if pc == dc {
		t.Fatalf("both natures of payment resolved to code %q. The code is being derived from "+
			"the SECTION, which cannot distinguish them, and every director's fee in the "+
			"quarter will be filed as a professional fee", pc)
	}

	// The thresholds must differ, and in the direction that costs money. A
	// ₹10,000 sitting fee attracts ₹1,000; the same sum as a professional fee
	// attracts nothing.
	fee := decimal.RequireFromString("10000")
	dres, err := TDSDerive(TDSInput{
		Section: "194J-D", DeducteeType: DeducteeOther, PANAvailable: true, TaxableAmount: fee,
	}, rs)
	if err != nil {
		t.Fatalf("derive director: %v", err)
	}
	if !dres.Applied || !dres.TDS.Equal(decimal.RequireFromString("1000")) {
		t.Errorf("a 10,000 director's fee deducted %s (applied=%v), want 1000: the threshold at "+
			"Sl. No. 6(iii) col. D '(ii) for (c) of Col. B' is Nil", dres.TDS, dres.Applied)
	}
	if dres.ReturnCode != "1028" {
		t.Errorf("the derived result carries code %q, want 1028", dres.ReturnCode)
	}

	pres, err := TDSDerive(TDSInput{
		Section: "194J-P", DeducteeType: DeducteeOther, PANAvailable: true, TaxableAmount: fee,
	}, rs)
	if err != nil {
		t.Fatalf("derive professional: %v", err)
	}
	if pres.Applied {
		t.Errorf("a 10,000 PROFESSIONAL fee deducted %s, want nothing: its threshold is 50,000. "+
			"If this fires, the Nil threshold has been applied to the wrong row", pres.TDS)
	}
}

// TestContractorReturnCodeSplitsByDeducteeType pins the second reason the code
// table is not a derivation: 194C's code depends on WHO was paid, not only on
// what for.
func TestContractorReturnCodeSplitsByDeducteeType(t *testing.T) {
	ind, err := TDSReturnCode("194C", DeducteeIndHUF)
	if err != nil {
		t.Fatalf("ind/HUF: %v", err)
	}
	oth, err := TDSReturnCode("194C", DeducteeOther)
	if err != nil {
		t.Fatalf("other: %v", err)
	}
	if ind != "1023" || oth != "1024" {
		t.Errorf("194C codes = (%q, %q), want (1023, 1024) per Form 140 Annexure 2", ind, oth)
	}
	if ind == oth {
		t.Error("both deductee types resolved to one contractor code; Annexure 2 splits them and " +
			"a single code files half the contractor payments under the wrong nature of payment")
	}
}

// TestTDSReturnCodeRefusesRatherThanGuessing pins that an unknown section is an
// error and not a plausible-looking code.
func TestTDSReturnCodeRefusesRatherThanGuessing(t *testing.T) {
	if _, err := TDSReturnCode("194Q", DeducteeOther); !errors.Is(err, ErrNoReturnCode) {
		t.Errorf("an unmodelled section returned %v, want ErrNoReturnCode", err)
	}
	if _, err := TDSReturnCode("194C", "PARTNERSHIP"); !errors.Is(err, ErrNoReturnCode) {
		t.Errorf("an unknown deductee type returned %v, want ErrNoReturnCode: guessing between "+
			"1023 and 1024 here would file against the wrong nature of payment", err)
	}
}
