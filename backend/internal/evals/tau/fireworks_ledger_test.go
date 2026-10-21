package tau_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/shopspring/decimal"
)

// A run holds the lock until cleanup. Reservations are persisted BEFORE the
// request; a crash or missing usage leaves them charged. Only validated provider
// usage can settle a reservation, always at the configured upper token price.
type spendLedger struct {
	path         string
	lock         *os.File
	Cap          decimal.Decimal `json:"cap_usd"`
	Rate         decimal.Decimal `json:"rate_per_million_usd"`
	Charged      decimal.Decimal `json:"charged_usd"`
	PriorReports []string        `json:"prior_reports,omitempty"`
	// Keep authorization history through every reservation/settlement. The
	// explicit cap-increase CLI is the only writer of these audit entries.
	CapIncreases []json.RawMessage `json:"cap_increases,omitempty"`
}

func openSpendLedger(path string, cap, rate decimal.Decimal) (*spendLedger, error) {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another evaluation holds the budget ledger")
	}
	l := &spendLedger{path: path, lock: lock}
	raw, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(raw, l)
	}
	if err == nil && (!l.Cap.Equal(cap) || !l.Rate.Equal(rate) || l.Charged.IsNegative() || l.Charged.GreaterThan(cap)) {
		err = fmt.Errorf("budget ledger cap/rate/charge mismatch; refusing to reset it")
	}
	if err != nil {
		l.close()
		return nil, err
	}
	return l, nil
}

func (l *spendLedger) close() { syscall.Flock(int(l.lock.Fd()), syscall.LOCK_UN); l.lock.Close() }

func (l *spendLedger) save(charge decimal.Decimal) error {
	raw, err := json.MarshalIndent(struct {
		Cap          decimal.Decimal   `json:"cap_usd"`
		Rate         decimal.Decimal   `json:"rate_per_million_usd"`
		Charged      decimal.Decimal   `json:"charged_usd"`
		PriorReports []string          `json:"prior_reports,omitempty"`
		CapIncreases []json.RawMessage `json:"cap_increases,omitempty"`
	}{l.Cap, l.Rate, charge, l.PriorReports, l.CapIncreases}, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(l.path), ".fireworks-budget-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), l.path); err != nil {
		return err
	}
	l.Charged = charge
	return nil
}

func (l *spendLedger) reserve(amount decimal.Decimal) error {
	next := l.Charged.Add(amount)
	if next.GreaterThan(l.Cap) {
		return fmt.Errorf("shared evaluation budget exhausted")
	}
	return l.save(next)
}

func (l *spendLedger) settle(reservation, charge decimal.Decimal) error {
	if charge.IsNegative() || charge.GreaterThan(reservation) {
		return fmt.Errorf("usage exceeded reservation; retained full reservation")
	}
	return l.save(l.Charged.Sub(reservation).Add(charge))
}

func TestSharedFireworksBudgetSurvivesRunsAndFailures(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ledger.json")
	os.WriteFile(p, []byte(`{"cap_usd":"1","rate_per_million_usd":"2","charged_usd":"0.2","prior_reports":["old-report"],"cap_increases":[{"old_cap_usd":"0.5","new_cap_usd":"1","authorization":"explicit user permission"}]}`), 0600)
	l, err := openSpendLedger(p, decimal.NewFromInt(1), decimal.NewFromInt(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openSpendLedger(p, l.Cap, l.Rate); err == nil {
		t.Fatal("concurrent evaluation admitted")
	}
	if err := l.reserve(decimal.RequireFromString("0.6")); err != nil {
		t.Fatal(err)
	}
	l.close() // Simulate a process exiting without a response: reservation survives.
	l, err = openSpendLedger(p, decimal.NewFromInt(1), decimal.NewFromInt(2))
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	if err := l.reserve(decimal.RequireFromString("0.3")); err == nil {
		t.Fatal("previous run's spending forgotten")
	}
	if err := l.settle(decimal.RequireFromString("0.6"), decimal.RequireFromString("0.1")); err != nil {
		t.Fatal(err)
	}
	if !l.Charged.Equal(decimal.RequireFromString("0.3")) {
		t.Fatal(l.Charged)
	}
	if err := l.settle(decimal.RequireFromString("0.1"), decimal.RequireFromString("0.2")); err == nil {
		t.Fatal("invalid usage released reservation")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var saved spendLedger
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.PriorReports) != 1 || saved.PriorReports[0] != "old-report" || len(saved.CapIncreases) != 1 {
		t.Fatalf("lost history: %s", raw)
	}
	var audit map[string]string
	if err := json.Unmarshal(saved.CapIncreases[0], &audit); err != nil || audit["authorization"] != "explicit user permission" {
		t.Fatalf("lost authorization: %s", raw)
	}
}
