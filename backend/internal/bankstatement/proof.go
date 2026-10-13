package bankstatement

// THE BALANCE PROOF — no statement is imported without one.
//
// Design memo: docs/design/d123-w5-documents-bank.md §2.3. PLAN-AGENTIC.md W5's
// gate: "balance proof on every imported statement".
//
// WHAT CHANGED FROM SLICE 2a, AND WHY IT IS A REFUSAL NOW
// -------------------------------------------------------
// Slice 2a imported a file with no running balance and DISCLOSED that the walk
// had not run. That was honest, and it was not enough: the disclosure is read
// once, the rows are matched for a year, and a dropped row in an unwalked file
// is a missing receipt nobody will ever look for. W5 makes the proof mandatory.
// A statement is imported only when at least one of these holds:
//
//  1. RUNNING — every row carries the bank's running balance and the rows walk
//     from one to the next to the paisa, oldest-first OR newest-first (several
//     banks export newest-first; the walk direction is DISCOVERED from the
//     balances, never assumed from the dates, which cannot order two rows on
//     one day).
//  2. DECLARED — an opening AND a closing balance are known, from the file's
//     own summary or from the operator reading them off the printed statement,
//     and opening + deposits − withdrawals = closing, to the paisa.
//
// When both are available BOTH must hold, and they must agree with each other:
// the walk's derived opening must equal the declared one, and so must the
// closing. That is the check that catches the edge rows the walk alone cannot
// see (a first or last transaction missing from the export).
//
// When the file declares its own figures AND the operator supplies them, they
// must be equal. An operator who keyed a different figure is looking at a
// different statement or has mistyped, and either way the import is wrong.
//
// ZERO TOLERANCE, as in parse.go's walk. A tolerance is a configured, displayed
// number or it is nothing.

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

var (
	// ErrNoBalanceProof is a statement with neither a running balance on every
	// row nor a declared opening and closing balance: there is nothing to prove
	// it against, and it is refused rather than imported with a caveat.
	ErrNoBalanceProof = errors.New("bankstatement: the statement cannot be balance-proved")
	// ErrBalanceProof is a proof that RAN and FAILED: opening + deposits −
	// withdrawals is not the closing balance, or a declared figure disagrees with
	// the running balances, or a declared total/count disagrees with the rows.
	ErrBalanceProof = errors.New("bankstatement: the balance proof failed")
	// ErrDeclaredConflict is two sources declaring two different figures for the
	// same thing — the file twice, or the file and the operator.
	ErrDeclaredConflict = errors.New("bankstatement: two different figures are declared for one balance")
)

// Order is the row order the running balances revealed.
type Order string

const (
	OldestFirst Order = "oldest-first"
	NewestFirst Order = "newest-first"
	// OrderUnknown: no running balance, so the balances could not say. Dates
	// are not used to decide it — see the package header.
	OrderUnknown Order = ""
)

// ProofMethod is WHICH of the two proofs held. Stored on the import
// (migration 0101), because "how do we know this statement is complete?" must
// be answerable months after the file is gone.
type ProofMethod string

const (
	ProofRunning         ProofMethod = "running"
	ProofDeclared        ProofMethod = "declared"
	ProofRunningDeclared ProofMethod = "running+declared"
)

// Declared is an opening/closing balance supplied by the OPERATOR, read off
// the printed statement. Nil means "not supplied" — never zero.
type Declared struct {
	Opening *decimal.Decimal
	Closing *decimal.Decimal
}

// BalanceProof is the proof that held, with every figure it used.
type BalanceProof struct {
	Method ProofMethod
	// Opening and Closing are the figures the proof bound the rows between, in
	// the BANK's orientation (a positive balance is money the bank owes us).
	Opening decimal.Decimal
	Closing decimal.Decimal
	// Deposits and Withdrawals are MAGNITUDES summed over the rows.
	Deposits    decimal.Decimal
	Withdrawals decimal.Decimal
	// DeclaredBy says where the declared figures came from: "file", "operator",
	// "file+operator", or "" when the proof used none.
	DeclaredBy string
	// Checks lists every comparison that ran and held, in words, so the import
	// can say what was verified rather than that "it passed".
	Checks []string
}

// prove runs the mandatory proof over a parsed statement. st must already have
// been walked (walkBalances) — prove reads BalanceWalked, never re-derives it.
func prove(st *Statement, fileSummary Summary, operator Declared) error {
	var deposits, withdrawals decimal.Decimal
	var nDep, nWdr int64
	for _, l := range st.Lines {
		if l.DrCr == "D" {
			deposits = deposits.Add(l.Amount)
			nDep++
		} else {
			withdrawals = withdrawals.Add(l.Amount)
			nWdr++
		}
	}
	p := BalanceProof{Deposits: deposits, Withdrawals: withdrawals}

	opening, closing, by, err := mergeDeclared(fileSummary, operator)
	if err != nil {
		return err
	}
	p.DeclaredBy = by

	// The bank's column totals, when it printed them, are checked whichever
	// proof is used: they cost nothing and a mismatch is unambiguous. The
	// bank's "Debits" is money OUT, which is our withdrawal ("C" in our books).
	if t := fileSummary.DebitTotal; t != nil && !t.Abs().Equal(withdrawals) {
		return fmt.Errorf("%w: the file's own summary says total debits (withdrawals) of %s, but its rows add up to %s — "+
			"a row is missing or duplicated; the statement is NOT imported",
			ErrBalanceProof, t.StringFixed(2), withdrawals.StringFixed(2))
	}
	if t := fileSummary.CreditTotal; t != nil && !t.Abs().Equal(deposits) {
		return fmt.Errorf("%w: the file's own summary says total credits (deposits) of %s, but its rows add up to %s — "+
			"a row is missing or duplicated; the statement is NOT imported",
			ErrBalanceProof, t.StringFixed(2), deposits.StringFixed(2))
	}
	if n := fileSummary.DebitCount; n != nil && *n != nWdr {
		return fmt.Errorf("%w: the file's own summary counts %d debits, but it has %d withdrawal rows; the statement is NOT imported",
			ErrBalanceProof, *n, nWdr)
	}
	if n := fileSummary.CreditCount; n != nil && *n != nDep {
		return fmt.Errorf("%w: the file's own summary counts %d credits, but it has %d deposit rows; the statement is NOT imported",
			ErrBalanceProof, *n, nDep)
	}
	if fileSummary.DebitTotal != nil || fileSummary.CreditTotal != nil {
		p.Checks = append(p.Checks, "the rows add up to the totals the file's own summary prints")
	}

	haveDeclared := opening != nil && closing != nil
	if haveDeclared {
		want := opening.Add(deposits).Sub(withdrawals)
		if !want.Equal(*closing) {
			return fmt.Errorf(
				"%w: opening %s + deposits %s − withdrawals %s = %s, but the closing balance is %s (declared by the %s), "+
					"a difference of %s. A row is missing, duplicated, or a column is mapped wrongly — the statement is NOT imported",
				ErrBalanceProof, opening.StringFixed(2), deposits.StringFixed(2), withdrawals.StringFixed(2),
				want.StringFixed(2), closing.StringFixed(2), by, closing.Sub(want).StringFixed(2))
		}
		p.Checks = append(p.Checks, fmt.Sprintf(
			"opening %s + deposits %s − withdrawals %s = closing %s",
			opening.StringFixed(2), deposits.StringFixed(2), withdrawals.StringFixed(2), closing.StringFixed(2)))
	}

	switch {
	case st.BalanceWalked && haveDeclared:
		// THE EDGE CHECK. The walk proves the interior; only the declared
		// figures prove the first and last rows are there at all.
		if !st.OpeningBalance.Equal(*opening) {
			return fmt.Errorf("%w: the running balances imply an opening balance of %s, but the %s declares %s — "+
				"the first transaction of the period is missing or extra; the statement is NOT imported",
				ErrBalanceProof, st.OpeningBalance.StringFixed(2), by, opening.StringFixed(2))
		}
		if !st.ClosingBalance.Equal(*closing) {
			return fmt.Errorf("%w: the running balances end at %s, but the %s declares a closing balance of %s — "+
				"the last transaction of the period is missing or extra; the statement is NOT imported",
				ErrBalanceProof, st.ClosingBalance.StringFixed(2), by, closing.StringFixed(2))
		}
		p.Method = ProofRunningDeclared
		p.Opening, p.Closing = *opening, *closing
		p.Checks = append(p.Checks, "every row's running balance walks from the one before it, "+
			"and the walk starts and ends on the declared balances")
	case st.BalanceWalked:
		p.Method = ProofRunning
		p.Opening, p.Closing = st.OpeningBalance, st.ClosingBalance
		p.Checks = append(p.Checks, "every row's running balance walks from the one before it")
	case haveDeclared:
		p.Method = ProofDeclared
		p.Opening, p.Closing = *opening, *closing
	default:
		missing := "an opening AND a closing balance"
		switch {
		case opening != nil:
			missing = "a closing balance (only the opening is known)"
		case closing != nil:
			missing = "an opening balance (only the closing is known)"
		}
		return fmt.Errorf(
			"%w: this file carries no running balance, and %s is not stated in it. "+
				"Key the opening and closing balance printed on the statement, so that "+
				"opening + deposits − withdrawals = closing can be checked; without it a dropped "+
				"row would never be detected, so nothing has been imported",
			ErrNoBalanceProof, missing)
	}
	st.Proof = p
	return nil
}

// mergeDeclared combines the file's own figures with the operator's. Equal
// figures from both are recorded as "file+operator"; different ones refuse.
func mergeDeclared(file Summary, op Declared) (opening, closing *decimal.Decimal, by string, err error) {
	pick := func(name string, f, o *decimal.Decimal) (*decimal.Decimal, string, error) {
		switch {
		case f != nil && o != nil:
			if !f.Equal(*o) {
				return nil, "", fmt.Errorf(
					"%w: the file states the %s balance as %s but %s was keyed; one of them is not this statement's",
					ErrDeclaredConflict, name, f.StringFixed(2), o.StringFixed(2))
			}
			return f, "file+operator", nil
		case f != nil:
			return f, "file", nil
		case o != nil:
			return o, "operator", nil
		}
		return nil, "", nil
	}
	opening, byO, err := pick("opening", file.Opening, op.Opening)
	if err != nil {
		return nil, nil, "", err
	}
	closing, byC, err := pick("closing", file.Closing, op.Closing)
	if err != nil {
		return nil, nil, "", err
	}
	switch {
	case byO == byC:
		by = byO
	case byO == "":
		by = byC
	case byC == "":
		by = byO
	default:
		by = "file+operator"
	}
	// by stays "" when neither source declared anything: DeclaredBy must never
	// claim an operator (or a file) vouched for a figure nobody supplied.
	return opening, closing, by, nil
}
