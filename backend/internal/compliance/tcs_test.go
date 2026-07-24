package compliance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	// 1% scrap: 2 x 1% = 2%, which is below the 5% floor -> 5%.
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
	if !withPAN.RatePct.Equal(pct("1")) {
		t.Fatalf("with PAN: got %s, want 1", withPAN.RatePct)
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
