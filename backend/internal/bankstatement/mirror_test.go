package bankstatement

import (
	"errors"
	"testing"
)

// A month of identical EMI debits. With every row moving the balance by the
// same amount, reading the file in the OTHER order with every sign inverted
// also walks — the mirror image of a Deposit/Withdrawal mapping entered the
// wrong way round (the wave-1 review's balance-proof finding).
const emiCSV = `Value Date,Narration,Withdrawal Amt.,Deposit Amt.,Closing Balance
05/04/2026,EMI HDFC LOAN,"1,000.00",,"9,000.00"
05/05/2026,EMI HDFC LOAN,"1,000.00",,"8,000.00"
05/06/2026,EMI HDFC LOAN,"1,000.00",,"7,000.00"
`

func TestASwappedMappingOnIdenticalRowsIsRefusedByTheDates(t *testing.T) {
	m := twoColMapping()
	st, err := ParseStatement(emiCSV, m, Declared{})
	if err != nil {
		t.Fatalf("the correct mapping must import: %v", err)
	}
	if st.Order != OldestFirst || st.OpeningBalance.StringFixed(2) != "10000.00" || st.ClosingBalance.StringFixed(2) != "7000.00" {
		t.Fatalf("correct mapping: order %s opening %s closing %s", st.Order, st.OpeningBalance.StringFixed(2), st.ClosingBalance.StringFixed(2))
	}

	swapped := m
	swapped.Deposit, swapped.Withdrawal = m.Withdrawal, m.Deposit
	st, err = ParseStatement(emiCSV, swapped, Declared{})
	if !errors.Is(err, ErrBalanceWalk) {
		dirs := ""
		for _, l := range st.Lines {
			dirs += l.DrCr
		}
		t.Fatalf("a swapped Deposit/Withdrawal mapping imported: err=%v order=%s proof=%s opening=%s lines=%s",
			err, st.Order, st.Proof.Method, st.OpeningBalance.StringFixed(2), dirs)
	}
}

// When the dates cannot decide either (one day, or a one-row file), the running
// balance has proved nothing about direction: the import needs declared
// balances rather than claiming proof "running".
func TestAnUndecidableMirrorNeedsDeclaredBalances(t *testing.T) {
	oneDay := `Value Date,Narration,Withdrawal Amt.,Deposit Amt.,Closing Balance
05/04/2026,SIP A,"1,000.00",,"9,000.00"
05/04/2026,SIP B,"1,000.00",,"8,000.00"
`
	if _, err := ParseStatement(oneDay, twoColMapping(), Declared{}); !errors.Is(err, ErrNoBalanceProof) {
		t.Fatalf("an undecidable mirror claimed a running proof: %v", err)
	}
	st, err := ParseStatement(oneDay, twoColMapping(), Declared{Opening: dec("10000"), Closing: dec("8000")})
	if err != nil || st.Proof.Method != ProofDeclared {
		t.Fatalf("with declared balances it must import on the declared proof: %v %s", err, st.Proof.Method)
	}
	swapped := twoColMapping()
	swapped.Deposit, swapped.Withdrawal = swapped.Withdrawal, swapped.Deposit
	if _, err := ParseStatement(oneDay, swapped, Declared{Opening: dec("10000"), Closing: dec("8000")}); !errors.Is(err, ErrBalanceProof) {
		t.Fatalf("control: the declared proof must refuse the swapped mapping: %v", err)
	}
}
