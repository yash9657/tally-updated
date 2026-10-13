package bankstatement

// THE MATCHER'S DATABASE HALF (T4 slice 2b) — loading what the matcher compares,
// and recording the resolutions a person asserts.
//
// Design memo: docs/design/t4-slice2-model.md §2.1, §4, §5.
//
// ===========================================================================
// WHAT IS DERIVED AND WHAT IS STORED, AND WHY THE LINE IS DRAWN HERE
// ===========================================================================
// PROPOSALS ARE DERIVED ON EVERY READ. Nothing in this file writes a candidate.
// The memo calls bank_statement_match "the candidate links"; migration 0088's
// header records why slice 2b departs from that word, and the short version is
// that a stored candidate goes stale silently: a voucher altered, deleted, or
// cleared by the manual loop between the proposal and the acceptance leaves the
// operator choosing from a list that no longer describes the books, while it
// still looks authoritative.
//
// ONLY RESOLUTIONS ARE STORED — what a person decided.
//
// ===========================================================================
// ⛔ THE CANDIDATE FILTER IS "HAS NO bank_reconciliation ROW AT ALL"
// ===========================================================================
// NOT "is un-cleared as at the statement date", which is what the BRS asks and
// is a DIFFERENT question. A leg cleared on a date after the period is still
// cleared; offering it as a candidate would let a second clearance be asserted
// over the first, and migration 0045's one-per-leg constraint would then turn an
// ordinary-looking acceptance into an error the operator cannot explain. Memo
// §5: "already-cleared legs are not candidates; no double-clearing."

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrLineNotFound is a statement line id that is not on this account.
	ErrLineNotFound = errors.New("bankstatement: that statement line is not on this bank account")
	// ErrLineAlreadyResolved is the second resolution of one line. It is an
	// ERROR rather than an overwrite: a line is one thing the bank did, and
	// silently re-pointing it would discard an assertion a person made without
	// telling them.
	ErrLineAlreadyResolved = errors.New("bankstatement: that statement line has already been resolved")
	// ErrDateDisagrees is a resolve whose asserted bank date is not the
	// statement line's own value date. See LinkMatch for why it is refused
	// rather than reconciled in either direction.
	ErrDateDisagrees = errors.New("bankstatement: the asserted bank date is not this statement row's date")
)

// Resolution is one recorded decision about a statement line.
type Resolution struct {
	LineID uuid.UUID
	Status string
	Tier   MatchTier
	Note   string
	By     string
	At     string
	// The matched leg. Zero-valued for a set-aside.
	VoucherID uuid.UUID
	LegNo     int
	VoucherNo string
	Class     string
	// BankDate is the date the clearance actually records.
	BankDate string
	// ⚠️ BankDateDiffers is TRUE when the clearance's bank date is no longer the
	// statement line's own value date — which happens when somebody retyped the
	// Bank Date cell on a leg that had been matched. The match still points at a
	// real clearance, so nothing is broken, but the line and the tick no longer
	// agree about WHEN the bank did it, and silence would let the screen show a
	// date that is not on the statement it claims to come from. It is the same
	// class as BROKEN BY ALTER, one column over.
	BankDateDiffers bool
	// LineValueDate is what the statement says, kept beside BankDate so the
	// screen can show both when they differ rather than asking the reader to
	// trust one.
	LineValueDate string
}

// MatchView is everything the matching screen needs in one call.
type MatchView struct {
	LedgerID   uuid.UUID
	LedgerName string
	From       string
	To         string
	Proposals  Proposals
	// Resolved is what has already been decided, newest first.
	Resolved []Resolution
	// Counts summarises Proposals for the exception report (Ctrl+J).
	Counts Counts
}

// unresolvedLinesSQL loads the statement rows nobody has decided about yet.
const unresolvedLinesSQL = `
	SELECT l.id, l.line_no, l.value_date, l.description, l.amount, l.dr_cr::text,
	       COALESCE(l.bank_reference, ''), COALESCE(l.cheque_number, '')
	  FROM bank_statement_line l
	 WHERE l.company_id = $1
	   AND l.ledger_id  = $2
	   AND ($3::date IS NULL OR l.value_date >= $3::date)
	   AND ($4::date IS NULL OR l.value_date <= $4::date)
	   AND NOT EXISTS (
	       SELECT 1 FROM bank_statement_match m
	        WHERE m.company_id = l.company_id AND m.line_id = l.id
	   )
	 ORDER BY l.value_date, l.line_no, l.id`

// candidateLegsSQL loads bank-ledger legs that have NO reconciliation at all.
//
// The leg ordinal is computed EXACTLY as reporting.bankReconSQL and
// banking.legSQL compute it — row_number() over the same partition, same
// ORDER BY, 0-based. All three must agree: if the proposer and the writer
// disagreed about which leg is leg 1, an operator would accept one match and the
// statement would clear another.
//
// $5 is the date window in days, so the candidate set is bounded by the same
// number the matcher will compare against rather than by a second, unrelated one.
const candidateLegsSQL = `
	WITH legs AS (
	    SELECT vv.voucher_id,
	           vv.voucher_no,
	           COALESCE(vv.payload->>'class', '')     AS class,
	           vv.voucher_date                        AS voucher_date,
	           COALESCE(vv.payload->>'narration', '') AS narration,
	           COALESCE(vv.payload->>'party', '')     AS party,
	           p.amount,
	           p.dr_cr::text AS dr_cr,
	           row_number() OVER (
	               PARTITION BY vv.voucher_id, p.ledger_id
	               ORDER BY p.id
	           ) - 1 AS leg_no
	      FROM posting p
	      JOIN voucher_version vv ON vv.id = p.voucher_version_id
	      JOIN voucher_head vh
	        ON vh.company_id = vv.company_id
	       AND vh.voucher_id = vv.voucher_id
	       AND vh.current_version_id = vv.id
	     WHERE vh.is_deleted = false
	       AND p.ledger_id = $2
	       AND vv.company_id = $1
	       -- make_interval() rather than ($5 || ' days')::interval: the string
	       -- form makes pgx infer $5 as TEXT, which fails to encode an int.
	       AND ($3::date IS NULL OR vv.voucher_date >= $3::date - make_interval(days => $5))
	       AND ($4::date IS NULL OR vv.voucher_date <= $4::date + make_interval(days => $5))
	)
	SELECT l.voucher_id, l.leg_no, l.voucher_no, l.class, l.voucher_date,
	       l.narration, l.party, l.amount, l.dr_cr
	  FROM legs l
	 WHERE NOT EXISTS (
	       SELECT 1 FROM bank_reconciliation br
	        WHERE br.company_id = $1
	          AND br.voucher_id = l.voucher_id
	          AND br.ledger_id  = $2
	          AND br.leg_no     = l.leg_no
	   )
	 ORDER BY l.voucher_date, l.voucher_no, l.leg_no`

// resolvedSQL loads the decisions already recorded for this account.
const resolvedSQL = `
	SELECT m.line_id, m.status, m.tier, COALESCE(m.note, ''), m.matched_by,
	       to_char(m.matched_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
	       COALESCE(br.voucher_id::text, ''), COALESCE(br.leg_no, 0),
	       COALESCE(to_char(br.bank_date, 'YYYY-MM-DD'), ''),
	       to_char(l.value_date, 'YYYY-MM-DD'),
	       COALESCE(vv.voucher_no, ''), COALESCE(vv.payload->>'class', '')
	  FROM bank_statement_match m
	  JOIN bank_statement_line l
	    ON l.company_id = m.company_id AND l.id = m.line_id
	  LEFT JOIN bank_reconciliation br
	    ON br.company_id = m.company_id AND br.id = m.reconciliation_id
	  LEFT JOIN voucher_head vh
	    ON vh.company_id = br.company_id AND vh.voucher_id = br.voucher_id
	  LEFT JOIN voucher_version vv
	    ON vv.id = vh.current_version_id
	 WHERE m.company_id = $1
	   AND m.ledger_id  = $2
	   AND ($3::date IS NULL OR l.value_date >= $3::date)
	   AND ($4::date IS NULL OR l.value_date <= $4::date)
	 ORDER BY m.matched_at DESC, m.id DESC`

// MatchReport derives the proposals for one bank account over a period and
// returns them beside what has already been resolved.
//
// IT WRITES NOTHING. There is no argument that would make it write, and that is
// the property the whole slice rests on.
func (s *Service) MatchReport(
	ctx context.Context, companyID, ledgerID uuid.UUID, from, to string, windowDays int,
) (MatchView, error) {
	if err := scope(ctx, companyID); err != nil {
		return MatchView{}, err
	}
	if windowDays <= 0 {
		windowDays = DefaultDateWindowDays
	}
	out := MatchView{LedgerID: ledgerID, From: from, To: to}

	var (
		lines []StatementRow
		legs  []BookLeg
	)
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// The ledger must exist, belong to this company and be a BANK ledger —
		// refused BY NAME, T1's diagnostic-refusal habit. An empty proposal set
		// would read as "nothing to match" and send the operator hunting.
		var isBank bool
		err := tx.QueryRow(ctx, `
			SELECT l.name, is_bank_ledger(l.company_id, l.id)
			  FROM ledger l WHERE l.company_id = $1 AND l.id = $2`,
			companyID, ledgerID).Scan(&out.LedgerName, &isBank)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("bankstatement: ledger %s not found", ledgerID)
		}
		if err != nil {
			return fmt.Errorf("bankstatement: load bank ledger: %w", err)
		}
		if !isBank {
			return fmt.Errorf(
				"bankstatement: ledger %q is not under a bank group; only a bank account has a statement to match",
				out.LedgerName)
		}

		lines, err = loadUnresolvedLines(ctx, tx, companyID, ledgerID, from, to)
		if err != nil {
			return err
		}
		legs, err = loadCandidateLegs(ctx, tx, companyID, ledgerID, from, to, windowDays)
		if err != nil {
			return err
		}
		out.Resolved, err = loadResolutions(ctx, tx, companyID, ledgerID, from, to)
		return err
	})
	if err != nil {
		return MatchView{}, err
	}

	out.Proposals = ProposeMatches(lines, legs, windowDays)
	out.Counts = out.Proposals.Summarise()
	return out, nil
}

func loadUnresolvedLines(
	ctx context.Context, tx pgx.Tx, companyID, ledgerID uuid.UUID, from, to string,
) ([]StatementRow, error) {
	rows, err := tx.Query(ctx, unresolvedLinesSQL, companyID, ledgerID, nullDateStr(from), nullDateStr(to))
	if err != nil {
		return nil, fmt.Errorf("bankstatement: statement lines: %w", err)
	}
	defer rows.Close()
	var out []StatementRow
	for rows.Next() {
		var r StatementRow
		if err := rows.Scan(&r.ID, &r.LineNo, &r.ValueDate, &r.Description,
			&r.Amount, &r.DrCr, &r.BankReference, &r.ChequeNumber); err != nil {
			return nil, fmt.Errorf("bankstatement: scan statement line: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadCandidateLegs(
	ctx context.Context, tx pgx.Tx, companyID, ledgerID uuid.UUID, from, to string, windowDays int,
) ([]BookLeg, error) {
	rows, err := tx.Query(ctx, candidateLegsSQL,
		companyID, ledgerID, nullDateStr(from), nullDateStr(to), windowDays)
	if err != nil {
		return nil, fmt.Errorf("bankstatement: candidate legs: %w", err)
	}
	defer rows.Close()
	var out []BookLeg
	for rows.Next() {
		var l BookLeg
		if err := rows.Scan(&l.VoucherID, &l.LegNo, &l.VoucherNo, &l.Class,
			&l.VoucherDate, &l.Narration, &l.Party, &l.Amount, &l.DrCr); err != nil {
			return nil, fmt.Errorf("bankstatement: scan candidate leg: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func loadResolutions(
	ctx context.Context, tx pgx.Tx, companyID, ledgerID uuid.UUID, from, to string,
) ([]Resolution, error) {
	rows, err := tx.Query(ctx, resolvedSQL, companyID, ledgerID, nullDateStr(from), nullDateStr(to))
	if err != nil {
		return nil, fmt.Errorf("bankstatement: resolutions: %w", err)
	}
	defer rows.Close()
	var out []Resolution
	for rows.Next() {
		var (
			r         Resolution
			voucherID string
			tier      string
		)
		if err := rows.Scan(&r.LineID, &r.Status, &tier, &r.Note, &r.By, &r.At,
			&voucherID, &r.LegNo, &r.BankDate, &r.LineValueDate,
			&r.VoucherNo, &r.Class); err != nil {
			return nil, fmt.Errorf("bankstatement: scan resolution: %w", err)
		}
		r.Tier = MatchTier(tier)
		if voucherID != "" {
			if id, perr := uuid.Parse(voucherID); perr == nil {
				r.VoucherID = id
			}
		}
		// See Resolution.BankDateDiffers — a tick retyped after matching.
		r.BankDateDiffers = r.Status == "matched" &&
			r.BankDate != "" && r.LineValueDate != "" && r.BankDate != r.LineValueDate
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// THE WRITES
// ---------------------------------------------------------------------------

// LinkMatch records that a clearance came from a statement line, INSIDE the
// caller's transaction.
//
// ⛔ IT TAKES A tx RATHER THAN OPENING ONE, AND THAT IS THE POINT. The clearance
// and the link must land together or not at all: a bank_reconciliation row
// without its match row is a tick nobody can trace back to the statement, and a
// match row without its clearance is refused by 0088's CHECK. banking.SetBankDate
// calls this inside the same transaction that writes the clearance.
//
// ⛔ THE TIER IS DERIVED HERE AND NEVER TAKEN FROM THE CALLER. It re-runs the
// matcher over this one (line, leg) pair against the rows as they are NOW, so
// the stored word is the server's own verdict. A leg the matcher does not offer
// at any tier is recorded 'manual' — which is the honest word for "a person said
// so", and a perfectly good reason to reconcile.
func LinkMatch(
	ctx context.Context, tx pgx.Tx,
	companyID, ledgerID, lineID, reconciliationID, voucherID uuid.UUID,
	legNo int, bankDate, by string, windowDays int,
) error {
	if windowDays <= 0 {
		windowDays = DefaultDateWindowDays
	}

	line, err := loadOneLine(ctx, tx, companyID, ledgerID, lineID)
	if err != nil {
		return err
	}

	// ⛔ THE ASSERTED DATE MUST BE THE LINE'S OWN VALUE DATE, AND A DISAGREEMENT
	// IS REFUSED RATHER THAN RESOLVED IN EITHER DIRECTION.
	//
	// The value date is the bank's own date for this transaction, and it is what
	// bank_date means on a match (memo §1.1). If the two disagree, exactly one
	// of two things has happened and BOTH are the client being wrong: it sent a
	// date the operator never saw, or it sent a line the operator did not pick.
	//
	// Silently preferring the line's date would write a clearance the operator
	// never asserted; silently preferring the body's would file a bank row under
	// a date that is not on the statement it came from. Neither is visible
	// afterwards — every figure would be a real figure — so the request is
	// refused NAMING BOTH DATES, and the check lives in the transaction where no
	// caller can skip it.
	if got := line.ValueDate.Format("2006-01-02"); got != bankDate {
		return fmt.Errorf(
			"%w: this bank row is dated %s on the statement but the request asserts %s; "+
				"a match records the bank's own date and nothing else",
			ErrDateDisagrees, got, bankDate)
	}
	leg, err := loadOneLeg(ctx, tx, companyID, ledgerID, voucherID, legNo)
	if err != nil {
		return err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("bankstatement: new match id: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO bank_statement_match
		    (id, company_id, line_id, ledger_id, status, reconciliation_id, tier, matched_by)
		VALUES ($1, $2, $3, $4, 'matched', $5, $6, $7)`,
		id, companyID, lineID, ledgerID, reconciliationID,
		string(TierFor(line, leg, windowDays)), by)
	if err != nil {
		// 0088's one-per-line UNIQUE and one-per-clearance index both land here.
		// They are the CALLER's error and they mean something specific, so they
		// are named rather than flattened.
		return fmt.Errorf("%w: %s", ErrLineAlreadyResolved, err.Error())
	}
	return nil
}

// SetAside records that a bank row corresponds to NOTHING in the books.
//
// ⛔ IT WRITES NO CLEARANCE AND MOVES NO BALANCE, and 0088's CHECK is what makes
// that structural rather than a promise. A set-aside is the operator saying "I
// have looked at this row and there is no voucher for it" — the bank charge, the
// standing order, the drawings. That is a real category (RESEARCH.md:873), not a
// weaker kind of match, and the Bank Reconciliation Statement will not show it at
// all, because the BRS reads bank_reconciliation and this writes none.
func (s *Service) SetAside(
	ctx context.Context, companyID, ledgerID, lineID uuid.UUID, note, by string,
) error {
	if err := scope(ctx, companyID); err != nil {
		return err
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := loadOneLine(ctx, tx, companyID, ledgerID, lineID); err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("bankstatement: new match id: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO bank_statement_match
			    (id, company_id, line_id, ledger_id, status, tier, note, matched_by)
			VALUES ($1, $2, $3, $4, 'set-aside', 'manual', $5, $6)`,
			id, companyID, lineID, ledgerID, nullText(note), by)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrLineAlreadyResolved, err.Error())
		}
		return nil
	})
}

// UndoSetAside removes a set-aside, returning the line to unmatched.
//
// ⛔ IT REFUSES TO TOUCH A 'matched' ROW, and the refusal is the feature. Un-doing
// a MATCH means withdrawing a clearance, which is the bank-reconciliation DELETE
// route's job and cascades the match row away with it (0088's FK). If this
// deleted a matched row instead, the clearance would survive with nothing
// pointing at it: the leg would stay ticked, the line would return to the
// unmatched list, and the same bank row could be reconciled a second time
// against a second voucher. Both would look right.
func (s *Service) UndoSetAside(
	ctx context.Context, companyID, ledgerID, lineID uuid.UUID,
) error {
	if err := scope(ctx, companyID); err != nil {
		return err
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// ⚠️ THE LINE MUST EXIST ON THIS ACCOUNT, and that check is separate from
		// the idempotency below. "This row was never set aside" is genuinely a
		// no-op — the caller's intent is satisfied. "There is no such row on this
		// account" is the caller being wrong about WHICH row, and answering 200
		// to it would tell an operator their line had been restored when nothing
		// of the kind had happened.
		if _, err := loadOneLine(ctx, tx, companyID, ledgerID, lineID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			DELETE FROM bank_statement_match
			 WHERE company_id = $1 AND ledger_id = $2 AND line_id = $3
			   AND status = 'set-aside'`,
			companyID, ledgerID, lineID)
		if err != nil {
			return fmt.Errorf("bankstatement: undo set-aside: %w", err)
		}
		if tag.RowsAffected() == 0 {
			// Distinguish the two reasons, because they need different actions.
			var status string
			qerr := tx.QueryRow(ctx, `
				SELECT status FROM bank_statement_match
				 WHERE company_id = $1 AND ledger_id = $2 AND line_id = $3`,
				companyID, ledgerID, lineID).Scan(&status)
			if qerr == nil && status == "matched" {
				return fmt.Errorf(
					"bankstatement: that line is MATCHED to a voucher, not set aside; " +
						"un-tick the bank date on the reconciliation screen to undo it, " +
						"which withdraws the clearance and the match together")
			}
			// Nothing was set aside. The caller's intent is satisfied.
		}
		return nil
	})
}

func loadOneLine(
	ctx context.Context, tx pgx.Tx, companyID, ledgerID, lineID uuid.UUID,
) (StatementRow, error) {
	var r StatementRow
	err := tx.QueryRow(ctx, `
		SELECT l.id, l.line_no, l.value_date, l.description, l.amount, l.dr_cr::text,
		       COALESCE(l.bank_reference, ''), COALESCE(l.cheque_number, '')
		  FROM bank_statement_line l
		 WHERE l.company_id = $1 AND l.ledger_id = $2 AND l.id = $3`,
		companyID, ledgerID, lineID).
		Scan(&r.ID, &r.LineNo, &r.ValueDate, &r.Description, &r.Amount, &r.DrCr,
			&r.BankReference, &r.ChequeNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		// ⚠️ THE LEDGER IS PART OF THE LOOKUP, so a line belonging to ANOTHER
		// bank account of the same company is refused here rather than by
		// 0088's three-column FK. Both refuse it; this one names it.
		return StatementRow{}, fmt.Errorf("%w: line %s", ErrLineNotFound, lineID)
	}
	if err != nil {
		return StatementRow{}, fmt.Errorf("bankstatement: load statement line: %w", err)
	}
	return r, nil
}

// loadOneLeg reads ONE bank leg of the CURRENT version, by the same ordinal the
// proposer and banking.SetBankDate use.
func loadOneLeg(
	ctx context.Context, tx pgx.Tx, companyID, ledgerID, voucherID uuid.UUID, legNo int,
) (BookLeg, error) {
	var l BookLeg
	err := tx.QueryRow(ctx, `
		WITH legs AS (
		    SELECT vv.voucher_id, vv.voucher_no,
		           COALESCE(vv.payload->>'class', '')     AS class,
		           vv.voucher_date,
		           COALESCE(vv.payload->>'narration', '') AS narration,
		           COALESCE(vv.payload->>'party', '')     AS party,
		           p.amount, p.dr_cr::text AS dr_cr,
		           row_number() OVER (
		               PARTITION BY vv.voucher_id, p.ledger_id
		               ORDER BY p.id
		           ) - 1 AS leg_no
		      FROM posting p
		      JOIN voucher_version vv ON vv.id = p.voucher_version_id
		      JOIN voucher_head vh
		        ON vh.company_id = vv.company_id
		       AND vh.voucher_id = vv.voucher_id
		       AND vh.current_version_id = vv.id
		     WHERE vh.is_deleted = false
		       AND vv.company_id = $1
		       AND p.ledger_id   = $2
		       AND vv.voucher_id = $3
		)
		SELECT voucher_id, leg_no, voucher_no, class, voucher_date,
		       narration, party, amount, dr_cr
		  FROM legs WHERE leg_no = $4`,
		companyID, ledgerID, voucherID, legNo).
		Scan(&l.VoucherID, &l.LegNo, &l.VoucherNo, &l.Class, &l.VoucherDate,
			&l.Narration, &l.Party, &l.Amount, &l.DrCr)
	if errors.Is(err, pgx.ErrNoRows) {
		return BookLeg{}, fmt.Errorf("bankstatement: voucher %s has no leg %d on this bank account",
			voucherID, legNo)
	}
	if err != nil {
		return BookLeg{}, fmt.Errorf("bankstatement: load bank leg: %w", err)
	}
	return l, nil
}

// nullDateStr turns an empty period bound into a SQL NULL, so "" means
// "unbounded" rather than "the zero date".
func nullDateStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
