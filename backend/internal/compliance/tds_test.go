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
