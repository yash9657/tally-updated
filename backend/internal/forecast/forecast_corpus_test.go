package forecast

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// forecast_corpus_test.go is the INDEPENDENT-ORACLE gate: it drives the pure
// Project / DetectAnomalies functions with hand-derived fixtures whose expected
// outputs were computed pen-and-paper (see each fixture's "description"). No DB,
// no clock — so the anomaly rules (which depend on created_at, always now() in
// the DB) are fully testable, positives AND near-miss negatives.

func corporaForecastDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "corpora", "forecast")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate corpora/forecast above %q", dir)
	return ""
}

// --- fixture wire shapes ---

type cashflowFixture struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	AsOn        string `json:"as_on"`
	HorizonDays int    `json:"horizon_days"`
	BucketDays  int    `json:"bucket_days"`
	OpeningCash string `json:"opening_cash"`
	Bills       []struct {
		Kind        string `json:"kind"`
		Pending     string `json:"pending"`
		DueDate     string `json:"due_date"`
		VoucherDate string `json:"voucher_date"`
		CreditDays  *int   `json:"credit_days"`
	} `json:"bills"`
	Expected struct {
		OverdueFolded bool `json:"overdue_folded"`
		Buckets []struct {
			From        string `json:"from"`
			To          string `json:"to"`
			Inflow      string `json:"inflow"`
			Outflow     string `json:"outflow"`
			Nett        string `json:"nett"`
			OpeningCash string `json:"opening_cash"`
			ClosingCash string `json:"closing_cash"`
		} `json:"buckets"`
		BeyondHorizon struct {
			Inflow, Outflow, Nett string
		} `json:"beyond_horizon"`
	} `json:"expected"`
}

type anomalyFixture struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Records []struct {
		VoucherID    string  `json:"voucher_id"`
		VoucherNo    string  `json:"voucher_no"`
		VoucherDate  string  `json:"voucher_date"`
		CreatedAt    string  `json:"created_at"`
		VersionNo    int     `json:"version_no"`
		TaxableValue *string `json:"taxable_value"`
	} `json:"records"`
	ExpectedFindings []struct {
		Kind      string `json:"kind"`
		VoucherNo string `json:"voucher_no"`
	} `json:"expected_findings"`
}

func TestForecastCorpus(t *testing.T) {
	dir := corporaForecastDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpora/forecast: %v", err)
	}
	var cashCount, anomCount int
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("%s: probe kind: %v", e.Name(), err)
		}
		switch probe.Kind {
		case "cashflow":
			cashCount++
			runCashflowFixture(t, e.Name(), raw)
		case "anomaly":
			anomCount++
			runAnomalyFixture(t, e.Name(), raw)
		default:
			t.Fatalf("%s: unknown fixture kind %q", e.Name(), probe.Kind)
		}
	}
	// Non-vacuity guard (tax_test.go pattern): the walk must have found cases.
	if cashCount == 0 {
		t.Fatal("no cashflow fixtures were read")
	}
	if anomCount == 0 {
		t.Fatal("no anomaly fixtures were read")
	}
	t.Logf("forecast corpus: %d cashflow + %d anomaly fixtures", cashCount, anomCount)
}

func runCashflowFixture(t *testing.T, name string, raw []byte) {
	t.Helper()
	var fx cashflowFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	t.Run(fx.ID, func(t *testing.T) {
		asOn := mustDate(t, fx.AsOn)
		bills := make([]ProjBill, 0, len(fx.Bills))
		for _, b := range fx.Bills {
			pb := ProjBill{Kind: FlowKind(b.Kind), Pending: mustDecimal(t, b.Pending), CreditDays: b.CreditDays}
			if b.DueDate != "" {
				d := mustDate(t, b.DueDate)
				pb.DueDate = &d
			}
			if b.VoucherDate != "" {
				pb.VoucherDate = mustDate(t, b.VoucherDate)
			}
			bills = append(bills, pb)
		}
		p, err := Project(bills, mustDecimal(t, fx.OpeningCash), asOn, fx.HorizonDays, fx.BucketDays)
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if p.OverdueFolded != fx.Expected.OverdueFolded {
			t.Errorf("overdue_folded = %v, want %v", p.OverdueFolded, fx.Expected.OverdueFolded)
		}
		if len(p.Buckets) != len(fx.Expected.Buckets) {
			t.Fatalf("bucket count = %d, want %d", len(p.Buckets), len(fx.Expected.Buckets))
		}
		for i, want := range fx.Expected.Buckets {
			got := p.Buckets[i]
			if got.From.Format("2006-01-02") != want.From || got.To.Format("2006-01-02") != want.To {
				t.Errorf("bucket %d range = [%s,%s), want [%s,%s)", i, got.From.Format("2006-01-02"), got.To.Format("2006-01-02"), want.From, want.To)
			}
			eqDec(t, name, i, "inflow", got.Inflow, want.Inflow)
			eqDec(t, name, i, "outflow", got.Outflow, want.Outflow)
			eqDec(t, name, i, "nett", got.Nett, want.Nett)
			eqDec(t, name, i, "opening_cash", got.OpeningCash, want.OpeningCash)
			eqDec(t, name, i, "closing_cash", got.ClosingCash, want.ClosingCash)
		}
		eqDec(t, name, -1, "beyond.inflow", p.Beyond.Inflow, fx.Expected.BeyondHorizon.Inflow)
		eqDec(t, name, -1, "beyond.outflow", p.Beyond.Outflow, fx.Expected.BeyondHorizon.Outflow)
		eqDec(t, name, -1, "beyond.nett", p.Beyond.Nett, fx.Expected.BeyondHorizon.Nett)

		// Conservation: sum(bucket flows) + beyond == sum of input pendings by kind.
		var wantIn, wantOut decimal.Decimal
		for _, b := range bills {
			if b.Pending.IsZero() {
				continue
			}
			if b.Kind == Outflow {
				wantOut = wantOut.Add(b.Pending)
			} else {
				wantIn = wantIn.Add(b.Pending)
			}
		}
		gotIn, gotOut := p.Beyond.Inflow, p.Beyond.Outflow
		for _, b := range p.Buckets {
			gotIn = gotIn.Add(b.Inflow)
			gotOut = gotOut.Add(b.Outflow)
		}
		if !gotIn.Equal(wantIn) {
			t.Errorf("conservation inflow: buckets+beyond = %s, bills = %s", gotIn, wantIn)
		}
		if !gotOut.Equal(wantOut) {
			t.Errorf("conservation outflow: buckets+beyond = %s, bills = %s", gotOut, wantOut)
		}
	})
}

func runAnomalyFixture(t *testing.T, name string, raw []byte) {
	t.Helper()
	var fx anomalyFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	t.Run(fx.ID, func(t *testing.T) {
		if len(fx.Records) == 0 {
			t.Fatal("anomaly fixture has no records (would be vacuous)")
		}
		recs := make([]VersionRecord, 0, len(fx.Records))
		for _, r := range fx.Records {
			vr := VersionRecord{
				VoucherID:   uuid.MustParse(r.VoucherID),
				VoucherNo:   r.VoucherNo,
				VoucherDate: mustDate(t, r.VoucherDate),
				CreatedAt:   mustTS(t, r.CreatedAt),
				VersionNo:   r.VersionNo,
			}
			if r.TaxableValue != nil {
				vr.TaxableValue = mustDecimal(t, *r.TaxableValue)
				vr.HasTaxable = true
			}
			recs = append(recs, vr)
		}
		got := DetectAnomalies(recs, DefaultThresholds())
		if len(got) != len(fx.ExpectedFindings) {
			t.Fatalf("findings = %d %+v, want %d", len(got), findingKeys(got), len(fx.ExpectedFindings))
		}
		for i, want := range fx.ExpectedFindings {
			if got[i].Kind != want.Kind || got[i].VoucherNo != want.VoucherNo {
				t.Errorf("finding %d = {%s,%s}, want {%s,%s}", i, got[i].Kind, got[i].VoucherNo, want.Kind, want.VoucherNo)
			}
		}
	})
}

func findingKeys(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Kind+"/"+f.VoucherNo)
	}
	return out
}

func eqDec(t *testing.T, name string, bucket int, field string, got decimal.Decimal, want string) {
	t.Helper()
	w := mustDecimal(t, want)
	if !got.Equal(w) {
		t.Errorf("%s bucket %d %s = %s, want %s", name, bucket, field, got.StringFixed(4), w.StringFixed(4))
	}
}

func mustDecimal(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", s, err)
	}
	return d
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return d
}

func mustTS(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse timestamp %q: %v", s, err)
	}
	return d
}
