// Package itc reconciles the INWARD side of GST: the supplier-filed statements
// (GSTR-2A / GSTR-2B), the Invoice Management System (IMS) action log, and the
// purchases actually posted in these books — and turns the three into a
// per-voucher input-tax-credit decision that GSTR-3B table 4 can be built from.
//
// It closes the gap gstr3b.go's own header declares in its limitation (1):
// "ITC IS REPORTED AS ALL-ELIGIBLE. No eligibility test, no rule 42/43
// apportionment, no reversals, no ineligible-ITC (section 17(5)) block, no
// GSTR-2A/2B matching. itc_net == itc_avl total, always."
//
// ---------------------------------------------------------------------------
// SHAPE: A PURE CORE WITH A THIN I/O RIM
// ---------------------------------------------------------------------------
// normalize.go, clock.go, match.go, ims.go and effect.go import NO database
// package and call NO clock. They are the same shape as compliance/tax.go,
// tds.go and tcs.go: given values in, values out, deterministically. Only
// store.go and service.go touch pgx, and gstr_adapter.go is the only file that
// imports compliance/gstr.
//
// NOTHING IN THE PURE CORE MAY CALL time.Now(). Every date boundary in this
// package is evaluated against an instant threaded from the caller. A boundary
// that reads the wall clock cannot be tested at the boundary, and every one of
// the boundaries here decides which TAX PERIOD a credit lands in.
//
// THERE ARE TWO SUCH INSTANTS AND THEY ARE NOT THE SAME. `asOn` — Policy.AsOn —
// is when the report is being LOOKED AT, and it decides the IMS projection
// (deemed acceptance and the IMS freeze) and nothing else. The s.16(4) expiry is
// evaluated instead against Policy.ClaimInstant, the instant the credit was or
// could first be TAKEN: the filing instant for a period already filed (so a
// filed return's numbers never move), and the later of the period's 3B due date
// and `asOn` for a period not yet filed (so the bar does fire on a filer
// catching up late). See the rule stated in full on Policy.ClaimInstant.
//
// ---------------------------------------------------------------------------
// RECONCILIATION NEVER WRITES TO THE BOOKS. NOT ONCE.
// ---------------------------------------------------------------------------
// This package INSERTs into gst_inward_statement, gst_inward_document,
// gst_ims_action and gst_return_filing, and into nothing else. It never posts a
// voucher, never writes a voucher_version, never writes a posting. Four reasons,
// each independently sufficient:
//
//  1. gstr3b.go issues its own SQL rather than delegating to GSTR-1 precisely
//     "so the two paths are independently computed". Auto-posting from a
//     supplier statement would make the BOOKS a function of the statement, and
//     a wrong statement would then be undetectable — the books would agree with
//     it by construction.
//  2. It would destroy the only exact books-vs-return invariant on the inward
//     side. Under the model this package implements (see MODEL (b) below) the
//     input-tax ledger balance stays exactly what suppliers charged, and the
//     reversal is a RETURN-side line. Post the reversal and
//     `sum(4A) + sum(4D) == ITCPostings` is false forever.
//  3. voucher_external_reference + reconciliation_queue (migration 0009)
//     HARD-FREEZE an IRN-pinned voucher against alter and delete. A
//     reconciliation that wanted to "correct" a purchase would 409 on exactly
//     the vouchers that matter most.
//  4. Reversals are not permanent. A supplier who files late restores the
//     credit next period; a posted reversal then has to be un-posted and the
//     books carry two entries for one non-event.
//
// CONSEQUENCE THE FILER MUST BE TOLD, and which gstr3b.go's header must state:
// table 4(B) reversals are RETURN-SIDE ONLY. The corresponding book entry
// (Dr Expense / Cr Input tax) is NOT posted by this software. The filer must
// pass it. It is emitted as the disclosure token `reversals-not-posted-to-books`.
//
// ---------------------------------------------------------------------------
// MODEL (b): 4(A) IS WHAT THE BOOKS POSTED; THE HAIRCUT LANDS IN 4(B)(2)
// ---------------------------------------------------------------------------
// GSTN's own portal auto-populates 4(A) from GSTR-2B (Circular 170/02/2022).
// This software does NOT. 4(A) here is every rupee of input tax the books
// posted, and the effect of a supplier who has not filed is a REVERSAL in
// 4(B)(2) "Others". 4(C) — the net — comes out the same either way.
//
// Why: model (a) (4A == the 2B figure) breaks the books tie permanently, and
// can only be re-established by an itemised bridge that itself has no
// independent check. Model (b) preserves ONE exact, always-true invariant, and
// an exact invariant is what makes a wrong number DETECTABLE rather than
// merely unlikely. It also makes 4(C) explainable as a subtraction from a
// figure the accountant can read straight off the trial balance.
//
// DISCLOSE THE DIVERGENCE: a filer comparing against the portal's pre-filled
// 4(A) will see a DIFFERENT 4(A) with the SAME 4(C).
//
// THE TWO EXACT IDENTITIES (both are hard gates, not aspirations):
//
//	(I1) sum(4A) + sum(4D) == input-tax POSTINGS, head by head.
//	(I2) sum(4A) - sum(4B) == 4C, head by head. A NEGATIVE 4(C) is LEGAL
//	     (reversals may exceed availment in a period) and must not be rejected.
//
// For (I1) to hold, every rupee of posted input tax must land in EXACTLY ONE of
// 4(A) or 4(D). That is why credit that NEVER WAS AVAILABLE — time-barred under
// s.16(4), or barred by the place-of-supply rule — is EXCLUDED FROM 4(A)
// ENTIRELY and reported in 4(D)(2). GSTN itself takes that view: it marks such
// a document itcavl="N" and keeps it out of the "ITC Available" section.
//
// 4(D) IS NOT SUBTRACTED FROM 4(C). In the post-Notification-14/2022 format
// 4(D) is "Other Details" and is informational; s.17(5) is reversed through
// 4(B)(1). Subtracting 4(D) as well DOUBLE-COUNTS the haircut and understates
// claimable ITC.
//
// 4(D)(1) "ITC reclaimed which was reversed in an earlier period" is
// DELIBERATELY NOT EMITTED in this wave: there is no earlier-period reversal
// store, and emitting it would break (I1) because it is a carry-in, not a
// function of this period's postings. Disclosure token `4d1-reclaim-not-modelled`.
//
// ---------------------------------------------------------------------------
// THE MOST IMPORTANT DISTINCTION IN THIS PACKAGE
// ---------------------------------------------------------------------------
// "MISSING IN 2B" AND "CANNOT BE COMPARED" ARE DIFFERENT WORDS.
//
// Every purchase posted before migration 0032 carries no supplier document
// number. Classifying those as MISSING_IN_2B would tell the filer their
// supplier did not file — a lie — and under model (b) it would REVERSE ITC that
// is perfectly good. NOT_COMPARABLE produces ZERO reversal and is reported as a
// data-completeness finding. See TestNotComparableIsNotMissingIn2B.
//
// ---------------------------------------------------------------------------
// CITATIONS AND THEIR GRADE
// ---------------------------------------------------------------------------
// This build machine has no network access. Every citation below is graded, and
// the grade is part of the citation:
//
//		[CITED-PRIMARY]   read from, or directly quoting, a GSTN/CBIC publication.
//		[CITED-SECONDARY] read from a professional-press rendering of one. MUST be
//		                  re-verified against the bare source before release.
//		[INFERRED]        not stated anywhere we read; a judgement, recorded as one.
//
//	  - GSTR-2B generation begins after the IFF / GSTR-5 / GSTR-6 due date ends at
//	    "13th midnight" — GSTN GSTR-2B Advisory (12-10-2021) Q.2. [CITED-PRIMARY]
//	  - The 2B WINDOW IS ON THE SUPPLIER'S FILING DATE, NOT THE DOCUMENT DATE: an
//	    invoice dated 15-05-2020 filed in the July-2020 GSTR-1 appears in the JULY
//	    2B — same advisory, Q.4. [CITED-PRIMARY] This is why the matcher must
//	    NEVER require a statement row's doc_date to fall inside the statement's
//	    return period, and must never call a prior-FY invoice in a current 2B a
//	    date mismatch.
//	  - "The latest action will overwrite the previous action" and the action set
//	    FREEZES when GSTR-3B is filed — GSTN IMS FAQ 12. [CITED-PRIMARY]
//	  - 2B is recomputable without limit before filing — GSTN IMS FAQ 27.
//	    [CITED-PRIMARY]
//	  - RCM supplies are NOT part of IMS but DO continue to appear in GSTR-2B —
//	    GSTN IMS FAQ 8 and 18. [CITED-PRIMARY]
//	  - Records in "docRejdata" are VIEW-ONLY and must not feed ITC — GSTN IMS
//	    FAQ 24. [CITED-PRIMARY]
//	  - Where an amendment and its original fall in the SAME statement period,
//	    only the AMENDED record counts for ITC — IMS advisory key point 7 /
//	    FAQ 13; for CREDIT NOTES the amendment always prevails — FAQ 14.
//	    [CITED-PRIMARY]
//	  - PENDING is forbidden, before the Oct-2025 tax period, on an original
//	    credit note, an upward amendment of a CN, a downward amendment of a CN
//	    whose original was rejected, and a downward amendment of an Invoice/DN
//	    whose original was accepted and whose 3B was filed — IMS advisory
//	    Flow(3) / FAQ 11. [CITED-PRIMARY]
//	  - From the OCTOBER-2025 tax period PENDING is allowed for those four for ONE
//	    tax period, expiring at the GSTR-3B due date of period+1 — GSTN Advisory
//	    631 of 08-10-2025, read via professional-press renderings (A2Z Taxcorp,
//	    TaxO). [CITED-SECONDARY] ***VERIFY BEFORE RELEASE.*** A hard-coded
//	    "PENDING is never allowed on a credit note" is WRONG for every period this
//	    software ships into, which is why the rule is effective-dated like the
//	    TDS/TCS rate tables rather than constant.
//	  - s.16(4): a credit expires on the earlier of 30 November following the end
//	    of the financial year of the document, or the date the annual return is
//	    filed. A DEBIT NOTE'S OWN DATE fixes the financial year (delinked from the
//	    underlying invoice w.e.f. 01-01-2021). [CITED-PRIMARY] The "30 November"
//	    wording is itself EFFECTIVE FROM 01-10-2022 — Finance Act 2022 s.100,
//	    commencement notified by Notification 18/2022-Central Tax; before it the
//	    test was the due date of the September return. The earlier wording is NOT
//	    MODELLED and cannot bite, because every year for which it could is inside
//	    the s.16(5) amnesty below. See the reasoning in full on Sec16_4Expiry.
//	  - s.16(5) AMNESTY: for the four financial years 2017-18, 2018-19, 2019-20
//	    and 2020-21 the credit is claimable in ANY return filed up to 30 November
//	    2021, "notwithstanding anything contained in sub-section (4)" — so it
//	    displaces the annual-return limb too, not just the 30-November one.
//	    Inserted by the Finance (No. 2) Act 2024, effective 27-09-2024 WITH
//	    RETROSPECTIVE EFFECT FROM 01-07-2017, which is why it is a function of the
//	    document's financial year and not effective-dated against the tax period.
//	    [CITED-PRIMARY] Implemented in Sec16_4Expiry / Sec16_5Applies. Without it
//	    a filer migrating historical books is told credit PARLIAMENT EXPRESSLY
//	    RESTORED is extinguished.
//	  - s.16(6) (credit for a period during which the registration stood cancelled
//	    and was later revoked) is NOT MODELLED — it needs cancellation and
//	    revocation order dates that are nowhere in this schema. [CITED-PRIMARY for
//	    the section; the non-implementation is a decision, not a citation.]
//	    Disclosure token `sec-16-6-revocation-not-modelled`, emitted
//	    unconditionally. See DISCLOSED GAP 7 below.
//	  - s.17(5) blocked credits: the CLAUSE LIST is [CITED-SECONDARY] and must be
//	    re-verified against the bare Act. That is precisely why the clause is
//	    stored as FREE TEXT (ledger.blocked_clause / voucher_version.
//	    itc_blocked_clause) and the CHECK constraint holds only the four
//	    ELIGIBILITY values — re-verifying the clause list is then a documentation
//	    change, not a migration.
//	  - The current 3B table-4 shape (4(D) informational, s.17(5) reversed through
//	    4(B)(1)) is Notification 14/2022 + Circular 170/02/2022. [CITED-PRIMARY]
//	  - The IST offset is [INFERRED]: no GSTN or CBIC document we read states a
//	    timezone anywhere. See clock.go's IST comment.
//
// ---------------------------------------------------------------------------
// THE 2B JSON SHAPE IS SECOND-HAND. THE PARSER IS TOLERANT ON PURPOSE.
// ---------------------------------------------------------------------------
// GSTN's own GSTR-2B JSON schema is behind a taxpayer login and IS NOT IN THIS
// REPOSITORY. The shape parse2b.go decodes is a GSP's rendering of it, learned
// second-hand. So the parser:
//
//   - IGNORES unknown keys rather than failing;
//   - treats a missing OPTIONAL key as absent, not as an error;
//   - accepts BOTH the `cgst`/`sgst`/`igst` and the `camt`/`samt`/`iamt`
//     spellings of the three tax heads, and both `cess` and `csamt`;
//   - accepts tax stated at INVOICE level AND tax nested under `itms[]`
//     (summing the nested form when the invoice level is absent);
//   - accepts dates as `DD-MM-YYYY` (the portal's usual form) and as
//     `YYYY-MM-DD`;
//   - emits the `statement-partially-parsed` disclosure when it had to skip
//     anything, rather than silently returning a short document list.
//
// A tolerant parser that says what it could not read is safe. A strict parser
// against a guessed schema rejects real files; a silent tolerant parser
// UNDER-REPORTS ITC, which looks exactly like a supplier who did not file.
//
// MONEY IS decimal.Decimal, DECODED FROM json.Number VIA decimal.NewFromString.
// There is no float64, no ParseFloat and no json.Unmarshal-into-float anywhere
// in this package, and that is asserted by grep in the acceptance gate. IEEE-754
// cannot represent 0.1; a GST return that goes through float is a return whose
// paisa are decorative.
//
// ---------------------------------------------------------------------------
// DISCLOSED GAPS (do not remove one without replacing the behaviour)
// ---------------------------------------------------------------------------
//
//  1. RATE-LEVEL (per-HSN, per-GST-rate) MATCHING IS OUT OF SCOPE. Matching is
//     at INVOICE grain: supplier GSTIN, document number, document type, document
//     date, taxable value. voucher_gst_line carries line-grain detail on our
//     side and 2B may nest `itms[]` on theirs, so a multi-rate invoice whose
//     TOTAL taxable value agrees but whose PER-RATE SPLIT is wrong will be
//     reported MATCHED. That is a real, reachable wrong answer and it is stated
//     here rather than discovered later.
//
//  2. PARTY GSTIN IS READ FROM THE CURRENT LEDGER MASTER, NOT AS OF THE INVOICE
//     DATE. There is no ledger-GSTIN history and masters.Service exposes no
//     ledger-update path today, so the master is immutable BY OMISSION rather
//     than by design. If a later wave adds ledger editing, every historical
//     match silently re-keys. Stated as an assumption; it is not defended by any
//     constraint.
//
//  3. CESS IS NOT MODELLED IN THE BOOKS (gstr3b.go limitation 3). The parser
//     reads the statement's cess so it is not lost, but no cess figure is
//     compared and none is emitted. Disclosure token `cess-not-modelled`.
//
//  4. s.17(5) CLASSIFICATION IS AN INPUT, NEVER AN INFERENCE. It CANNOT be
//     derived from an HSN: every 17(5) clause turns on USE and on the buyer's
//     own business, not on the goods. The identical motor car is blocked credit
//     for us and fully creditable for a taxi operator. An engine that guesses
//     will silently deny real credit or claim blocked credit, and both are
//     notices. It comes from ledger.itc_eligibility (the per-supplier default)
//     overridden by the hashed per-voucher field. Note also that GSTR-2B DOES
//     NOT FLAG s.17(5): GSTN sets itcavl="N" for exactly two reasons — s.16(4)
//     and the place-of-supply rule — and expressly disclaims the rest.
//
//  5. RULE 42/43 INPUTS ARE NEVER DERIVED FROM OUR OWN RETURN. E (exempt supply
//     value) and F (total turnover in the State) are CALLER-SUPPLIED and nil by
//     default. gstr3b.go already hard-zeroes 3.1(b)/(c)/(e) because "the books
//     carry no export/SEZ marker to classify on" — E is exactly that missing
//     number, and F is not modelled at all. An engine fed E=0 returns D1=0 for
//     EVERY taxpayer and looks like it works: a confidently wrong zero reversal,
//     which is the worst failure mode available. When the inputs are nil the
//     formula contributes nothing and `rule-42-43-not-computed` is disclosed.
//
//  6. THE OCR PREFILL PATH STILL CONFLATES TWO NUMBERS. insightd's
//     adapters/mapper.py sets `voucher_no = inferred.invoice_no`, so an
//     OCR-prefilled purchase carries the SUPPLIER's number in OUR numbering
//     column while a hand-keyed one carries an auto serial — two meanings in one
//     column. Until mapper.py sets the inward supplier_invoice_no instead,
//     OCR-sourced purchases classify NOT_COMPARABLE / NO_SUPPLIER_DOC_NO. That
//     is the SAFE outcome (zero reversal, reported as a data gap) but it is a
//     gap, not a design.
//
//  7. SECTION 16(6) IS NOT MODELLED. Where a registration was cancelled under
//     s.29 and the cancellation was later REVOKED, s.16(6) gives back the credit
//     for the intervening period on a return filed within thirty days of the
//     revocation order — or by the ordinary 30-November limb, whichever is
//     LATER. Every date it turns on (the cancellation order, its effective date,
//     the revocation order) is REGISTRATION-STATUS DATA THIS SYSTEM DOES NOT
//     HOLD: there is no cancellation table and no suspended-period field on any
//     master. It is therefore not computed and not guessed — guessing would
//     revive credit on registrations that were never cancelled, which is an
//     over-claim. Disclosure token `sec-16-6-revocation-not-modelled`, emitted
//     UNCONDITIONALLY, because the gap is unconditional. Its direction is the
//     safe one (credit under-claimed), but an under-claim the filer cannot see is
//     still tax they do not owe.
//
//     NOTE FOR THE NEXT WAVE: this token is declared in THIS package only. The
//     other ten are mirrored in compliance/gstr, which REJECTS a
//     SourceDisclosures token it does not know. Adding it to
//     gstr.KnownDisclosures is a two-line edit that this change did not own; see
//     DiscSec16_6NotModelled in effect.go and
//     TestDisclosureVocabularyMatchesGSTR, which pins the split and fails the
//     moment gstr gains the token.
//
//     SECTION 16(5) IS NOT ON THIS LIST BECAUSE IT IS IMPLEMENTED. See
//     Sec16_5Applies.
package itc
