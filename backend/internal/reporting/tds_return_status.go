package reporting

// THE RETURN-FILE STATUS, FOR ONE COMPANY AND ONE PERIOD.
//
// ===========================================================================
// WHAT CHANGED, AND WHY THE OLD VERSION WAS A LIE WAITING TO HAPPEN
// ===========================================================================
// ReturnFileRefusal() took no arguments. It read no database, and it returned
// the same three blockers for every company in the world — including, verbatim,
// "Nothing in this software records any of it today" and "This company record
// holds none of them". Both were true when written. Migrations 0069 and 0070
// made the first one false, and this slice's write path makes the second one
// false for any company that fills the form in.
//
// That is the THIRD time a claim in this corner of the product has outlived its
// own truth (see tds_return_file.go's banner about the 403 blocker that was dead
// for two days, and the register disclosure that said the layout had not been
// read after it had). The pattern is always the same: a refusal phrased as a
// FACT ABOUT THE SOFTWARE rather than as a QUESTION ABOUT THESE BOOKS. A fact
// about the software goes stale silently. A question about the books cannot,
// because it is re-answered on every request.
//
// So this file answers, per request: what in THIS company's books is stopping a
// return for THIS period, naming the vouchers.
//
// ===========================================================================
// ⛔ THE BLOCKERS ARE ASSEMBLED FROM INDEPENDENT CHECKS, NOT FROM ONE ERROR
// ===========================================================================
// The temptation is to call Form140StatementFor and render whatever error comes
// back. That produces a bad screen, for two structural reasons:
//
//  1. IT ABORTS ON THE FIRST OFFENDER. groupByChallan returns inside its row
//     loop, so one call names ONE voucher. A company with thirty unpaid
//     deductions would fix one, reload, and be told about the next — thirty
//     times, with no idea how much work remained.
//  2. THE PROFILE PRE-EMPTS EVERYTHING. readDeductorProfile is called before any
//     deduction is read, so a company with no profile row would never learn that
//     its challans are missing too. The companies with the MOST to do would see
//     the SHORTEST list.
//
// So every category is counted independently and reported together, with a
// total and with named examples.
//
// ===========================================================================
// WHY Available STAYS FALSE HERE, AND WHY THAT IS NOT A DODGE
// ===========================================================================
// Nothing in this build EMITS a return file to an operator. Service.Form140File
// exists and works, but no route serves it, and the format needs four facts that
// live in neither the books nor this software: the file sequence number (unique
// across every file this deductor has ever sent), the RPU name, whether a
// regular statement was already filed for an earlier period, and under what
// token. Inferring any of them would be a false statement on a tax return.
//
// So Available is false, and the LAST blocker says exactly that rather than
// implying the books are still at fault. When a company has cleared every
// data blocker, it is told so — that is the difference between "you have work to
// do" and "your books are ready and this software is the thing that is not".
//
// Clients branch on Available and on nothing else; ReturnFileStatus's own doc
// comment says so, and format_version has been non-empty since the layout was
// implemented.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/compliance/form140"
)

// maxNamedExamples bounds how many vouchers a single blocker names.
//
// THREE, AND THEN AN HONEST COUNT OF THE REST. Naming every one of two hundred
// undeposited deductions turns the panel into a report of its own; naming none
// of them is what this whole change exists to fix. What is NOT acceptable is
// truncating silently — a list that stops at three and says nothing reads as a
// list of three, so the count of the remainder is always stated.
const maxNamedExamples = 3

// returnBlockerGroup accumulates one category of refusal.
type returnBlockerGroup struct {
	// sentences are the refusal messages, verbatim from the same functions that
	// refuse at emission time.
	sentences []string
	total     int
}

// add records one refusal, keeping only the half that names the operator's data.
//
// ⚠️ THE SENTINEL IS PASSED IN, AND IT MUST BE. Every refusal is
// fmt.Errorf("%w: <evidence>", sentinel, …), so the evidence begins exactly
// after the sentinel's own text — and the ONLY way to strip it exactly is to
// know which sentinel it was. Cutting at the first ": " instead looks right and
// is not: the sentinel messages contain colons of their own, so the blocker came
// out reading "…Record them under Alt+G → TDS Challans. tax was deducted but no
// challan is recorded against it, so it cannot be reported: voucher P1 …",
// restating in machine vocabulary the very thing the blocker's own What field
// already says. That defect was invisible in four passing tests and was caught
// by reading the generated golden.
func (g *returnBlockerGroup) add(sentinel, err error) {
	g.total++
	if len(g.sentences) < maxNamedExamples {
		g.sentences = append(g.sentences,
			strings.TrimPrefix(err.Error(), sentinel.Error()+": "))
	}
}

// evidence renders the named examples plus, always, the count of any remainder.
func (g *returnBlockerGroup) evidence() string {
	var b strings.Builder
	for i, s := range g.sentences {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(s)
		b.WriteString(".")
	}
	if g.total > len(g.sentences) {
		fmt.Fprintf(&b, "  … and %d more in this period, not listed here.",
			g.total-len(g.sentences))
	}
	return b.String()
}

// ReturnFileStatusFor answers, for one company and one period, whether a return
// file can be produced and what is stopping it.
//
// The period is the REGISTER'S period — an arbitrary from/to, because that is
// what the operator is looking at. A return is filed per QUARTER, so this cannot
// and does not claim "this period is filable"; it reports what the books in view
// would do to a return. The register's own disclosures carry the quarter rule.
// The dates are yyyy-mm-dd strings rather than time.Time, mirroring
// TDSRegisterFor exactly: the two are called with the same period from the same
// handler, and one of them parsing while the other did not would be an invitation
// to pass a differently-rounded bound to each.
func (s *Service) ReturnFileStatusFor(ctx context.Context, companyID uuid.UUID,
	from, to string) (ReturnFileStatus, error) {
	if err := scope(ctx, companyID); err != nil {
		return ReturnFileStatus{}, err
	}
	fromT, err := time.Parse("2006-01-02", from)
	if err != nil {
		return ReturnFileStatus{}, fmt.Errorf("reporting: from date %q is not yyyy-mm-dd", from)
	}
	toT, err := time.Parse("2006-01-02", to)
	if err != nil {
		return ReturnFileStatus{}, fmt.Errorf("reporting: to date %q is not yyyy-mm-dd", to)
	}

	var (
		haveProfile   bool
		undeposited   returnBlockerGroup
		brokenByAlter returnBlockerGroup
		noReasonFlag  returnBlockerGroup
		deductions    int
	)

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// 1. The deductor profile. Its ABSENCE is the refusal (0070's header), so
		//    this asks only whether the row exists — it does not re-validate the
		//    fields, because the write path validated them on the way in and
		//    form140 validates them again at emission.
		if _, err := readDeductorProfile(ctx, tx, companyID); err != nil {
			if !errors.Is(err, ErrNoDeductorProfile) {
				return err
			}
		} else {
			haveProfile = true
		}

		// 2. Every deduction in view, with whatever challan is behind it. Reuses
		//    the emitter's own read so the two cannot diverge on what "in this
		//    period" means or on how the allocation is joined.
		rows, err := readQuarterDeductions(ctx, tx, companyID, fromT, toT)
		if err != nil {
			return err
		}
		deductions = len(rows)

		for i := range rows {
			r := &rows[i]
			// THE SAME FUNCTION THE EMITTER CALLS, so the sentence shown is the
			// refusal that would actually be raised. See challanRefusalFor.
			if err := challanRefusalFor(r); err != nil {
				switch {
				case errors.Is(err, ErrTaxNotDeposited):
					undeposited.add(ErrTaxNotDeposited, err)
				case errors.Is(err, ErrAllocationBrokenByAlter):
					brokenByAlter.add(ErrAllocationBrokenByAlter, err)
				default:
					return err
				}
				continue
			}
			// And the Annexure 6 side, likewise via the emitter's own derivation.
			// Two distinct sentinels land here — form140.ErrNoValidReasonFlag for a
			// sub-threshold payment whose code does not admit Y, and
			// ErrNoReasonDocument for a no-PAN deduction whose code does not admit
			// C. They are ONE blocker to an operator (a payment needs a document
			// nobody has recorded) and are counted together deliberately.
			if _, err := deducteeFor(r); err != nil {
				switch {
				case errors.Is(err, form140.ErrNoValidReasonFlag):
					noReasonFlag.add(form140.ErrNoValidReasonFlag, err)
				case errors.Is(err, ErrNoReasonDocument):
					noReasonFlag.add(ErrNoReasonDocument, err)
				default:
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return ReturnFileStatus{}, err
	}

	return buildReturnFileStatus(haveProfile, deductions,
		undeposited, brokenByAlter, noReasonFlag), nil
}

// buildReturnFileStatus turns the counts into the status. Split out from the
// reads so the whole blocker-assembly rule set is testable without a database.
func buildReturnFileStatus(haveProfile bool, deductions int,
	undeposited, brokenByAlter, noReasonFlag returnBlockerGroup) ReturnFileStatus {
	var blockers []ReturnFileBlocker

	if !haveProfile {
		blockers = append(blockers, ReturnFileBlocker{
			What: "This company's TAN, PAN and responsible-person details",
			Where: "Your TAN allotment letter, and whoever signs your returns. The TAN " +
				"is NOT the GSTIN and cannot be derived from it. Record them under " +
				"F11 Company Features → TDS Return Profile.",
			Why: "The return's header carries the deductor's TAN, PAN, deductor " +
				"category, address, e-mail and telephone, plus the name, designation, " +
				"address and PAN of the person responsible for deduction. Every one is " +
				"mandatory, and until they are on file there is nothing to file AS — so " +
				"this blocker hides every other one until it is cleared.",
		})
	}

	if undeposited.total > 0 {
		blockers = append(blockers, ReturnFileBlocker{
			What: fmt.Sprintf("A CHALLAN for %d %s in this period", undeposited.total,
				plural(undeposited.total, "deduction", "deductions")),
			Where: "Your own bank challans: the BSR code of the receiving branch, the " +
				"challan serial number, the date of deposit, and the split across tax, " +
				"interest, fee and penalty. Record them under Alt+G → TDS Challans. " +
				undeposited.evidence(),
			Why: "Every deductee row of the return points at the challan that paid its " +
				"tax, so no deductee row can exist without one — that is structural, " +
				"not a matter of ordering. It is also the deposit and not the " +
				"deduction that stops interest running under s.396.",
		})
	}

	if brokenByAlter.total > 0 {
		blockers = append(blockers, ReturnFileBlocker{
			What: fmt.Sprintf("%s where the voucher was ALTERED after the tax was deposited",
				plural(brokenByAlter.total, "One deduction", "Deductions")),
			Where: "Re-allocate the deposit, or correct the voucher back, in the books " +
				"themselves. " + brokenByAlter.evidence(),
			Why: "The deposit was allocated against a figure that has since changed. " +
				"Neither row is wrong on its own — this is a real discrepancy between " +
				"what was paid and what is now owed, and a return built over it would " +
				"claim a deposit that does not match the deduction it is filed against.",
		})
	}

	if noReasonFlag.total > 0 {
		blockers = append(blockers, ReturnFileBlocker{
			What: fmt.Sprintf("An Annexure 6 reason for %s that cannot be derived from the books",
				plural(noReasonFlag.total, "one payment", "payments")),
			Where: "The document that justifies it — a s.395 certificate, a s.393(6) " +
				"declaration, a transporter's declaration, or a Board circular. Nothing " +
				"in this software records any of them yet. " + noReasonFlag.evidence(),
			Why: "Annexure 6 has thirteen reason flags and this build can honestly " +
				"derive exactly two: Y (paid below the threshold) and C (deducted at the " +
				"higher no-PAN rate). The other eleven each assert that a DOCUMENT " +
				"exists, and asserting a document exists is the one thing a tax return " +
				"must never do on its own initiative.",
		})
	}

	// ALWAYS, and last among the company-specific ones: the two things no
	// software here can do for you.
	blockers = append(blockers, ReturnFileBlocker{
		What:  "The department's File Validation Utility, and the .csi challan file",
		Where: "Protean/TIN — the FVU needs a Java runtime; the .csi needs a TIN portal login.",
		Why: "A return is validated AGAINST the challan file: the FVU checks every " +
			"deposit claimed in the return against what the bank actually reported, " +
			"and the TAN in the statement must match the one in the .csi. Neither " +
			"can be reached from here, so a generated file must be validated by " +
			"you before it is filed.",
	})

	// AND THE ONE THAT IS ABOUT THIS SOFTWARE RATHER THAN ABOUT YOUR BOOKS.
	// Stated separately, and only when the books are otherwise ready, so it can
	// never be mistaken for something the operator can fix by keying more data.
	booksReady := haveProfile && undeposited.total == 0 &&
		brokenByAlter.total == 0 && noReasonFlag.total == 0
	if booksReady {
		blockers = append(blockers, ReturnFileBlocker{
			What: "A way to DOWNLOAD the file — this one is ours, not yours",
			Where: "Not built yet. The layout is implemented and these books would " +
				"produce a statement; no screen or route hands you the bytes.",
			Why: "The file header also needs four facts that are in neither your books " +
				"nor this software: the file sequence number (unique across every file " +
				"you have ever sent), the preparing software's name, whether a regular " +
				"statement was already filed for an earlier period, and its token " +
				"number. They must be asked for, not inferred — inferring any of them " +
				"would put a false statement on a tax return.",
		})
	}

	summary := returnStatusSummary(haveProfile, booksReady, deductions,
		undeposited, brokenByAlter, noReasonFlag)

	return ReturnFileStatus{
		// FALSE, ALWAYS, IN THIS BUILD. See the file header: nothing serves the
		// bytes. Do not flip this to `booksReady` — a client branching on
		// Available would then offer a download that cannot happen.
		Available:     false,
		FormatVersion: form140.FormatVersion,
		FormatSource:  form140.FormatSource,
		Summary:       summary,
		Validated:     false,
		ValidationNote: "Nothing this software produces has been validated by the " +
			"department's File Validation Utility. The FVU needs a Java runtime that " +
			"is not available here, so even once a file can be generated it must be " +
			"validated by you before filing. That stays true after every blocker " +
			"below is cleared.",
		Blockers: blockers,
		WhatYouCanDoInstead: "Use this register to prepare the return in your filing " +
			"software or with your accountant: it carries every deduction and " +
			"collection for the period, per deductee, with the section, the four-digit " +
			"return code the deduction will be filed under, the rate and the amount.",
	}
}

// returnStatusSummary is the one sentence at the top of the panel. It is written
// per case rather than as one string with holes in it, because the three cases
// are genuinely different messages and a merged sentence hedged all three.
func returnStatusSummary(haveProfile, booksReady bool, deductions int,
	undeposited, brokenByAlter, noReasonFlag returnBlockerGroup) string {
	const layoutRead = "The Income Tax Department's prescribed file format has been read " +
		"and is implemented — the record layouts, the four-digit payment-code list and " +
		"the reason codes all come from it. "

	if booksReady {
		if deductions == 0 {
			return layoutRead + "THERE IS NOTHING TO FILE FOR THIS PERIOD: no deduction " +
				"or collection falls in it. Your deductor profile is on file, so a period " +
				"that does have deductions would be reported from these books."
		}
		return layoutRead + fmt.Sprintf("YOUR BOOKS ARE READY: all %d deductions in this "+
			"period have a challan behind them, every allocation still matches its "+
			"deduction, and every reason flag is derivable. What is missing is in THIS "+
			"SOFTWARE, not in your books — see the last blocker below.", deductions)
	}

	if !haveProfile {
		return layoutRead + "NO RETURN CAN BE FILED YET, and the first reason is that " +
			"this company has no TDS return profile — no TAN, no deductor category and " +
			"no responsible person. Until that is recorded there is nothing to file AS, " +
			"and it hides whatever else is outstanding."
	}

	var parts []string
	if undeposited.total > 0 {
		parts = append(parts, fmt.Sprintf("%d %s no challan recorded against it",
			undeposited.total, plural(undeposited.total, "deduction has", "deductions have")))
	}
	if brokenByAlter.total > 0 {
		parts = append(parts, fmt.Sprintf("%d %s altered after the tax was deposited",
			brokenByAlter.total, plural(brokenByAlter.total, "voucher was", "vouchers were")))
	}
	if noReasonFlag.total > 0 {
		parts = append(parts, fmt.Sprintf("%d %s an Annexure 6 reason that is not on file",
			noReasonFlag.total, plural(noReasonFlag.total, "payment needs", "payments need")))
	}
	return layoutRead + "NO RETURN CAN BE FILED YET, and the reason is in YOUR BOOKS " +
		"rather than in the specification: " + joinAnd(parts) + ". Each one is named below."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// joinAnd renders a list the way a sentence does, because "3 deductions have no
// challan, 1 voucher was altered" reads as two facts and needs to read as one.
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}
