package server

// THE TDS / TCS REGISTER over HTTP — Item Zero step 4.
//
//	GET .../tds-register?from=&to=  -> Display / report:tds-register
//
// THE PATH SEGMENT IS A FLAT LITERAL EQUAL TO THE REPORT NAME, which
// PLAN-TALLY-PARITY.md §2.4 requires and TestReportVocabularyMatchesRouter
// enforces: a name in authz.KnownReportNames whose route does not resolve is a
// rule an administrator can write that can never match — a silent permit.
//
// IT IS A REPORT AND NOT A MASTER LIST, which is the decision that matters for
// authorization. It carries every supplier this company pays and every customer
// it collects from, with amounts — so migration 0068 writes an explicit
// `disallow display report:tds-register` at specificity 2 to beat migration
// 0011's coarse `allow display report`, exactly as 0046 had to for the bank
// statement.

import "net/http"

func (s *Server) handleTDSRegister(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	// BOTH DATES ARE REQUIRED, and there is no "this quarter" default.
	//
	// A register is read to answer "what do I owe for THIS quarter", and a
	// defaulted period would answer a question the operator did not ask while
	// looking exactly like the one they did. The bank-reconciliation route made
	// the same call about which account to print, for the same reason: a real,
	// correctly rendered report for the wrong period is worse than a refusal.
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest,
			"the register needs both from and to (yyyy-mm-dd): a period is not "+
				"defaulted, because a correctly rendered register for the wrong "+
				"quarter is worse than a refusal")
		return
	}

	reg, err := s.report.TDSRegisterFor(ctx, companyID, from, to)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	deductions := make([]map[string]any, 0, len(reg.Deductions))
	for _, d := range reg.Deductions {
		deductions = append(deductions, map[string]any{
			"date":          d.Date,
			"voucher_no":    d.VoucherNo,
			"voucher_id":    d.VoucherID.String(),
			"deductee":      d.Deductee,
			"section":       d.Section,
			"deductee_type": d.DeducteeType,
			"return_code":   d.ReturnCode,
			"base":          d.BaseRupees.StringFixed(4),
			"rate_pct":      d.RatePct.StringFixed(4),
			"tds":           d.TDSRupees.StringFixed(4),
			"pan_available": d.PANAvailable,
		})
	}
	collections := make([]map[string]any, 0, len(reg.Collections))
	for _, c := range reg.Collections {
		collections = append(collections, map[string]any{
			"date":       c.Date,
			"voucher_no": c.VoucherNo,
			"voucher_id": c.VoucherID.String(),
			"buyer":      c.Buyer,
			"section":    c.Section,
			"base":       c.BaseRupees.StringFixed(4),
			"rate_pct":   c.RatePct.StringFixed(4),
			"tcs":        c.TCSRupees.StringFixed(4),
			// NONE / EXEMPT / INVALID_NO_PAN — the only column that distinguishes
			// the three different meanings a zero in `tcs` can carry.
			"declaration":   c.Declaration,
			"pan_available": c.PANAvailable,
		})
	}

	// I-7, ON EVERY RESPONSE, AND NOW ABOUT THESE BOOKS RATHER THAN ABOUT THIS
	// SOFTWARE. It reads the same period the register just read, so the blockers
	// name the vouchers the operator is looking at.
	//
	// A failure here is a 422 like the register's own, NOT a silently omitted
	// field: a screen that renders a register with no return_file panel would
	// imply there is nothing to say about filing, which is the opposite of true.
	returnFile, err := s.report.ReturnFileStatusFor(ctx, companyID, from, to)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"from":                     reg.FromDate,
		"to":                       reg.ToDate,
		"tax_year_start":           reg.TaxYearStart,
		"deductions":               deductions,
		"collections":              collections,
		"total_base_deducted":      reg.TotalBaseDeducted.StringFixed(4),
		"total_tds":                reg.TotalTDS.StringFixed(4),
		"total_base_collected":     reg.TotalBaseCollected.StringFixed(4),
		"total_tcs":                reg.TotalTCS.StringFixed(4),
		"sub_threshold_deductions": reg.SubThresholdDeductions,
		"exempt_collections":       reg.ExemptCollections,
		// The caveats travel WITH the numbers. A screen may style them; it may
		// not decide whether to show them.
		"disclosures": reg.Disclosures,
		// ⚠️ I-7, ON EVERY RESPONSE. It is a FIELD and not a comment because the
		// person who needs to know is the operator looking for their return — the
		// same reasoning that prints the missing-QR notice on the face of an
		// e-invoice rather than filing it in a design note. See
		// internal/reporting/tds_return_file.go's header.
		"return_file": returnFile,
	})
}
