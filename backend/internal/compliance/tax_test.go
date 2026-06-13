package compliance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"
)

// corpusCase mirrors the JSON shape of corpora/tax/*.json.
type corpusCase struct {
	ID             string `json:"id"`
	Description    string `json:"description"`
	RulesetVersion string `json:"rulesetVersion"`
	Input          struct {
		SupplierStateCode      string `json:"supplierStateCode"`
		PlaceOfSupplyStateCode string `json:"placeOfSupplyStateCode"`
		ReverseCharge          bool   `json:"reverseCharge"`
		Exempt                 bool   `json:"exempt"`
		Lines                  []struct {
			TaxableValue string `json:"taxableValue"`
			GSTRatePct   string `json:"gstRatePct"`
			HSN          string `json:"hsn"`
		} `json:"lines"`
	} `json:"input"`
	Expected struct {
		CGST         string `json:"cgst"`
		SGST         string `json:"sgst"`
		IGST         string `json:"igst"`
		TotalTax     string `json:"totalTax"`
		InvoiceTotal string `json:"invoiceTotal"`
	} `json:"expected"`
}

// corporaTaxDir locates the golden tax corpus relative to the repo root.
func corporaTaxDir(t *testing.T) string {
	t.Helper()
	// This file lives at backend/internal/compliance; the corpora live at
	// <repo>/corpora/tax. Walk up to the repo root (the dir containing corpora).
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "corpora", "tax")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate corpora/tax above %q", dir)
	return ""
}

func mustDec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", s, err)
	}
	return d
}

func TestTaxDerive_GoldenCorpus(t *testing.T) {
	dir := corporaTaxDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpora dir: %v", err)
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var c corpusCase
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("unmarshal %s: %v", path, err)
		}

		t.Run(c.ID, func(t *testing.T) {
			rs, err := LoadRuleSet(c.RulesetVersion)
			if err != nil {
				t.Fatalf("load ruleset %q: %v", c.RulesetVersion, err)
			}

			in := TaxInput{
				SupplierStateCode:      c.Input.SupplierStateCode,
				PlaceOfSupplyStateCode: c.Input.PlaceOfSupplyStateCode,
				ReverseCharge:          c.Input.ReverseCharge,
				Exempt:                 c.Input.Exempt,
			}
			for _, l := range c.Input.Lines {
				in.Lines = append(in.Lines, Line{
					TaxableValue: mustDec(t, l.TaxableValue),
					GSTRatePct:   mustDec(t, l.GSTRatePct),
					HSN:          l.HSN,
				})
			}

			got, err := TaxDerive(in, rs)
			if err != nil {
				t.Fatalf("TaxDerive: %v", err)
			}

			checks := []struct {
				name string
				got  decimal.Decimal
				want string
			}{
				{"cgst", got.CGST, c.Expected.CGST},
				{"sgst", got.SGST, c.Expected.SGST},
				{"igst", got.IGST, c.Expected.IGST},
				{"totalTax", got.TotalTax, c.Expected.TotalTax},
				{"invoiceTotal", got.InvoiceTotal, c.Expected.InvoiceTotal},
			}
			for _, chk := range checks {
				want := mustDec(t, chk.want)
				if !chk.got.Equal(want) {
					t.Errorf("%s: got %s, want %s", chk.name, chk.got.StringFixed(4), want.StringFixed(4))
				}
			}
		})
		count++
	}
	if count == 0 {
		t.Fatal("no corpus cases were read")
	}
	t.Logf("validated %d tax corpus cases", count)
}

func TestTaxDerive_RejectsEmptyLines(t *testing.T) {
	rs, _ := LoadRuleSet("gst-2024-09")
	if _, err := TaxDerive(TaxInput{SupplierStateCode: "27", PlaceOfSupplyStateCode: "27"}, rs); err == nil {
		t.Fatal("expected error for empty lines")
	}
}

func TestLoadRuleSet_Unknown(t *testing.T) {
	if _, err := LoadRuleSet("gst-1999-01"); err == nil {
		t.Fatal("expected ErrUnknownRuleset")
	}
}
