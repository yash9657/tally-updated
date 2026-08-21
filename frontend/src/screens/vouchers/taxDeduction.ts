import type { Money, TCSRequest, TDSRequest, TaxSectionDoc } from "../../api";
import { normalizeMoney } from "./money";

/**
 * KEYING A TDS DEDUCTION OR A TCS COLLECTION FROM THE VOUCHER FORM — register
 * row D3.
 *
 * ⛔ WHAT D3 ACTUALLY WAS. The Go wire has carried `tds` and `tcs` since T6 was
 * built, the ledger derives and PERSISTS the deduction, the TDS/TCS Register
 * reads it and the whole challan mechanism depends on it — and there was no way
 * to key one. So on any books a real operator built there were NO deductions at
 * all, the challan's allocation picker was empty, and every row downstream of it
 * (D1, D2, D4) was untestable against real data. The client TYPE arrived first
 * and its own comment says so in as many words: "THE FORM IS STILL MISSING. Do
 * not read the presence of this interface as the feature being reachable."
 *
 * THIS FILE IS THE PURE HALF: the form state, the refusals, and the two request
 * blocks. It holds no React, so every rule below is testable without a screen —
 * and every rule below is a REFUSAL THE SERVER ALSO MAKES, never a looser one.
 *
 * ⛔ IT DERIVES NO TAX. The rate table is the server's (`compliance.TDSDerive`),
 * the deduction is derived and stored there, and this form sends only what was
 * keyed. A client-side "preview" of the tax would be a second engine — and the
 * only thing worse than no preview is one that disagrees with the posting by a
 * rupee. What the form DOES show is the SERVED rate and threshold for the chosen
 * section, which is the server's own figures echoed, not a computation.
 */

/** The TDS/TCS block as the form holds it. All strings; nothing is a number. */
export interface TaxDeductionForm {
  /** "" means the operator has not chosen — never a default. See below. */
  section: string;
  /** The party being paid (TDS) or the buyer (TCS). REQUIRED by the server. */
  partyLedgerId: string;
  /** What the operator picked, for display and for the refusal sentences. */
  partyName: string;
  /** The amount the deduction/collection applies to, as keyed. */
  taxableAmount: string;
  /** "IND_HUF" | "OTHER". Only 194C splits by it; "" means OTHER. */
  deducteeType: string;
  /**
   * THE GRID ROW THAT CARRIES THE WITHHELD TAX, by LEDGER NAME — "" for none.
   *
   * ⛔ THIS IS THE FIELD THAT MAKES THE VOUCHER'S ACCOUNTING TRUE. A `tds` block
   * on its own records the STATUTORY deduction and posts nothing: the expense is
   * debited in full and the bank credited in full, so the books say the whole
   * sum was paid out while the return says a fifth of it was withheld. The
   * withholding is a real credit to a real liability, and the operator keys it
   * as a line like any other — this names WHICH line it is, so the request can
   * carry `tds_ledger_id` and the server VERIFIES the posted leg against its own
   * derivation (side included: a TDS leg on the debit side is refused even when
   * the magnitude matches).
   *
   * ⚠️ LEAVING IT EMPTY IS LEGAL AND IS DISCLOSED, not refused. The server
   * derives and stores the deduction with no leg verified, which is what a
   * company that books its withholdings by a separate journal actually does. The
   * panel says so in as many words rather than pretending the voucher is
   * complete.
   */
  taxLegLedger: string;
  /**
   * ⚠️ DEFAULTS TO TRUE, and that is the safe direction on THIS field only.
   * Unticking it RAISES the rate (s.206AA on the deduction side, s.397(2)(b)(ii)
   * on the collection side), so a form that defaulted it to false would silently
   * over-deduct from every party whose PAN the operator never thought about.
   */
  panAvailable: boolean;
}

export function emptyTaxDeductionForm(): TaxDeductionForm {
  return {
    // ⛔ NO DEFAULT SECTION, EVER — the same rule the challan form's minor head
    // follows. Defaulting to 194C would assert on the company's behalf that a
    // payment is a contractor payment, which decides both the rate and the
    // return's payment code.
    section: "",
    partyLedgerId: "",
    partyName: "",
    taxableAmount: "",
    taxLegLedger: "",
    deducteeType: "OTHER",
    panAvailable: true,
  };
}

/** True when the operator has touched the block at all. */
export function isTaxDeductionKeyed(f: TaxDeductionForm): boolean {
  return (
    f.section !== "" ||
    f.partyLedgerId !== "" ||
    normalizeMoney(f.taxableAmount) !== "0.0000"
  );
}

/**
 * The refusal sentence for a half-keyed block, or "" when it is complete (or
 * untouched, which is the ordinary case — most payments deduct nothing).
 *
 * ⚠️ EVERY ONE OF THESE IS A REFUSAL THE SERVER ALSO MAKES, and none is looser.
 * They are made here so the operator learns while the figures are still on
 * screen rather than from a status line after the frame has popped — the same
 * call the master form's opening-breakup check makes.
 */
export function taxDeductionRefusal(
  f: TaxDeductionForm,
  kind: "TDS" | "TCS",
): string {
  if (!isTaxDeductionKeyed(f)) return "";
  const who = kind === "TDS" ? "deductee" : "buyer";
  if (f.section === "") {
    return (
      `This voucher has ${kind} on it but names no section, and there is NO DEFAULT: ` +
      "the section decides the rate AND the payment code the return files it under. " +
      "Nothing was sent."
    );
  }
  if (f.partyLedgerId === "") {
    return (
      `${kind} needs the ${who} — the ledger of the party being ` +
      `${kind === "TDS" ? "paid" : "collected from"}. It is half the key of the statutory ` +
      `aggregate and the whole of the return's ${who} annexure, and the server refuses ` +
      "without it. Nothing was sent."
    );
  }
  const amount = normalizeMoney(f.taxableAmount);
  if (amount === "0.0000") {
    return (
      `${kind} on ${f.partyName || "this party"} under ${f.section} has no amount to apply to. ` +
      "Nothing was sent."
    );
  }
  if (amount.startsWith("-")) {
    return (
      `The ${kind} taxable amount is ${amount}. It is a MAGNITUDE — a deduction is not ` +
      "a refund. Nothing was sent."
    );
  }
  return "";
}

/**
 * The refusal for a named tax leg that is not a line on the grid — checked
 * separately because it needs the grid, which the pure block above does not see.
 *
 * ⛔ IT IS A REFUSAL AND NOT A SHRUG. If the named ledger is not among the
 * postings, `tds_ledger_id` would point at a leg that is not there: the server's
 * verification loop would sum ZERO over it and compare zero against the derived
 * tax — refusing the voucher with a mismatch the operator cannot explain, or (if
 * the derivation is also zero, below the threshold) passing while verifying
 * nothing at all. Both are worse than saying which line is missing.
 */
export function taxLegRefusal(
  f: TaxDeductionForm,
  kind: "TDS" | "TCS",
  gridLedgers: readonly string[],
): string {
  if (!isTaxDeductionKeyed(f) || taxDeductionRefusal(f, kind) !== "") return "";
  const named = f.taxLegLedger.trim();
  if (named === "") return "";
  const present = gridLedgers.some(
    (l) => l.trim().toLowerCase() === named.toLowerCase(),
  );
  if (present) return "";
  return (
    `The ${kind} is said to be carried by "${named}", but no line on this voucher names ` +
    "that ledger. Key the line that credits it, or clear the field — a leg the server " +
    "cannot find is a verification that silently does not happen. Nothing was sent."
  );
}

/**
 * The `tds` block, or undefined when nothing was keyed.
 *
 * ⛔ `tds_ledger_id` IS DELIBERATELY NOT SENT. It is optional and it means "the
 * server should cross-check my posted TDS leg against its own derivation" — a
 * check that only makes sense once the operator has keyed that leg themselves.
 * This form does not post a TDS leg (the server never injects one either), so
 * sending an id here would ask for a comparison against a posting that is not
 * there, and the voucher would be refused for a contradiction the operator did
 * not create. When the entry grid grows a keyed TDS leg, THAT is when this field
 * arrives with it.
 */
export function buildTDSRequest(
  f: TaxDeductionForm,
  /**
   * Resolves the named grid row to the ledger uuid the POSTINGS were built with.
   * ⛔ IT MUST BE THE SAME RESOLUTION THE POSTINGS USED, not a second lookup:
   * the server compares `tds_ledger_id` against the posted legs, so an id that
   * matches no posting makes the check pass over nothing and the verification
   * silently stop happening. The caller passes a closure over the array it just
   * built — the discipline the cost-split offset follows.
   */
  resolveLegId: (ledgerName: string) => string,
): TDSRequest | undefined {
  if (!isTaxDeductionKeyed(f) || taxDeductionRefusal(f, "TDS") !== "") {
    return undefined;
  }
  const req: TDSRequest = {
    section: f.section,
    deductee_ledger_id: f.partyLedgerId,
    deductee_type: f.deducteeType === "IND_HUF" ? "IND_HUF" : "OTHER",
    pan_available: f.panAvailable,
    taxable_amount: normalizeMoney(f.taxableAmount) as Money,
  };
  const legID = f.taxLegLedger.trim() === "" ? "" : resolveLegId(f.taxLegLedger.trim());
  if (legID !== "") req.tds_ledger_id = legID;
  return req;
}

/**
 * The sentence a voucher that records a deduction WITHOUT posting one shows.
 * "" when a leg is named (or nothing is keyed).
 *
 * ⛔ IT IS A DISCLOSURE AND NOT A REFUSAL, because the state is legal and real:
 * a company may book its withholdings by a separate journal, and the server
 * stores the deduction either way. What must not happen is the operator not
 * KNOWING — a payment whose books say the full sum left and whose return says a
 * fifth was withheld is exactly the kind of undisclosed figure this product's
 * audit keeps finding.
 */
export function unpostedTaxLegNotice(
  f: TaxDeductionForm,
  kind: "TDS" | "TCS",
): string {
  if (!isTaxDeductionKeyed(f) || taxDeductionRefusal(f, kind) !== "") return "";
  if (f.taxLegLedger.trim() !== "") return "";
  return kind === "TDS"
    ? "No line on this voucher carries the withheld tax, so the deduction is recorded " +
        "for the return but the books show the full amount as paid out. Name the line " +
        "that credits your TDS liability above, and the server will check it against " +
        "its own derivation."
    : "No line on this voucher carries the collected tax, so the collection is recorded " +
        "for the return but the books show only the sale. Name the line that credits " +
        "your TCS liability above, and the server will check it.";
}

/**
 * The `tcs` block, or undefined when nothing was keyed.
 *
 * ⛔ THERE IS NO EXEMPTION FIELD AND THERE MUST NEVER BE ONE. A s.394(2)
 * exemption rests on a WRITTEN DECLARATION the collector holds; the post path
 * looks it up by (buyer, section, tax year). A flag here would make the
 * exemption an entry-time opinion — an operator could zero the tax on any sale
 * by ticking a box, with no document behind it.
 */
export function buildTCSRequest(
  f: TaxDeductionForm,
  resolveLegId: (ledgerName: string) => string,
): TCSRequest | undefined {
  if (!isTaxDeductionKeyed(f) || taxDeductionRefusal(f, "TCS") !== "") {
    return undefined;
  }
  const req: TCSRequest = {
    section: f.section,
    buyer_ledger_id: f.partyLedgerId,
    pan_available: f.panAvailable,
    taxable_amount: normalizeMoney(f.taxableAmount) as Money,
  };
  const legID = f.taxLegLedger.trim() === "" ? "" : resolveLegId(f.taxLegLedger.trim());
  if (legID !== "") req.tcs_ledger_id = legID;
  return req;
}

/**
 * The SERVED rate that will actually apply, as a sentence — echoed from the
 * catalog, never computed.
 *
 * ⛔ THE TWO NO-PAN RULES ARE DIFFERENT SHAPES. Deduction: max(rate, floor).
 * Collection: min(max(multiplier × rate, floor), ceiling). This function does
 * NOT evaluate either — it names the rule and the figures, because evaluating it
 * would be the client re-deriving a rate the server owns, and a preview that
 * disagrees with the posting by a rupee is worse than no preview.
 */
export function sectionSummary(
  doc: TaxSectionDoc | undefined,
  f: TaxDeductionForm,
  noPan: { kind: "TDS"; floorPct: string } | {
    kind: "TCS";
    multiplier: string;
    floorPct: string;
    ceilingPct: string;
  },
): string {
  if (!doc) return "";
  const rate = f.deducteeType === "IND_HUF" ? doc.rate_ind_huf_pct : doc.rate_other_pct;
  const threshold =
    doc.basis === "NIL"
      ? "no threshold — any positive sum attracts it"
      : `applies only ABOVE ${doc.threshold_rupees} (an amount exactly equal to it attracts none)`;
  // ⚠️ THE BASIS IS DISCLOSED AS AN APPROXIMATION. v1 applies every basis per
  // transaction, so an ANNUAL or MONTHLY limit under-deducts on a split payment.
  // The operator is reading a threshold and will take it for the law.
  const basisNote =
    doc.basis === "ANNUAL" || doc.basis === "MONTHLY"
      ? ` The statute measures this limit ${doc.basis === "ANNUAL" ? "over the year" : "per month"}; this build applies it PER PAYMENT, so splitting a payment under-deducts.`
      : "";
  const panNote = f.panAvailable
    ? ""
    : noPan.kind === "TDS"
      ? ` NO PAN on record: the rate becomes the HIGHER of ${rate}% and the ${noPan.floorPct}% statutory floor.`
      : ` NO PAN on record: the rate becomes ${noPan.multiplier}× ${rate}%, at least ${noPan.floorPct}% and at most ${noPan.ceilingPct}%.`;
  return `${doc.section}: ${rate}% — ${threshold}.${basisNote}${panNote} The server derives the tax; this is its own rate table, echoed.`;
}

/**
 * Re-state a STORED deduction or collection onto the form — register row D88.
 *
 * ⛔⛔ WITHOUT THIS, THE ORDINARY ROUND TRIP DELETES THE DEDUCTION. `tds_deduction`
 * is dropped and re-derived from the request body on EVERY alter, so a form that
 * cannot restate the block sends none — and the server obliges. The books do not
 * move (the withheld credit is still a posting, and the postings ARE restated),
 * so nothing an operator looks at changes: the return silently loses the
 * deduction and the challan that paid it keeps an allocation matching no row.
 *
 * ⛔ THE LIABILITY LEG IS RECOVERED FROM THE POSTINGS, BECAUSE NOTHING STORES IT.
 * `tds_ledger_id` is an INPUT to the server's cross-check — "does the posted
 * credit equal what I derived?" — and was never persisted, on the grounds that
 * the postings already say which line carries the tax. So this finds it: the ONE
 * credit of exactly the derived amount.
 *
 * ⚠️ AND WHEN IT CANNOT TELL, IT NAMES NOTHING — which is legal and DISCLOSED
 * (see {@link unpostedTaxLegNotice}), never guessed. Two credits of the same
 * amount, or a source that does not serve the derived figure (the mock does
 * not), leave the field empty: the panel then says the deduction is recorded for
 * the return while the books show the full sum as paid, and the operator names
 * the line. A guess here would send an id matching the WRONG posting, and the
 * server's verification would compare the derived tax against a credit that has
 * nothing to do with it.
 *
 * ⚠️ THE DERIVED FIGURES ARE NEVER COPIED ONTO THE FORM. Only what the operator
 * stated comes back: section, party, type, PAN, taxable base. `rate_pct` is the
 * server's, and a form that carried it would resend a second opinion about a
 * rate table it does not own.
 */
export function taxDeductionFromView(
  stored:
    | {
        section: string;
        pan_available: boolean;
        taxable_amount: string;
        deductee_ledger_id?: string;
        deductee_type?: string;
        buyer_ledger_id?: string;
        rate_pct?: string;
        tds_amount?: string;
        tcs_amount?: string;
      }
    | null
    | undefined,
  postings: readonly { ledger_id: string; ledger: string; dr_cr: string; amount: string }[],
  ledgerNameByID: (id: string) => string,
): TaxDeductionForm {
  if (!stored) return emptyTaxDeductionForm();
  const partyID = stored.deductee_ledger_id ?? stored.buyer_ledger_id ?? "";
  const derived = stored.tds_amount ?? stored.tcs_amount ?? "";
  // ⛔ CREDITS ONLY. A withholding and a collection are both credits, and the
  // server refuses a leg on the debit side by name even when the magnitude
  // matches — so a debit of the same amount is not a candidate.
  const credits = postings.filter(
    (p) =>
      p.dr_cr.toUpperCase().startsWith("C") &&
      derived !== "" &&
      normalizeMoney(p.amount) === normalizeMoney(derived),
  );
  return {
    section: stored.section,
    partyLedgerId: partyID,
    partyName: ledgerNameByID(partyID),
    taxableAmount: normalizeMoney(stored.taxable_amount),
    taxLegLedger: credits.length === 1 ? credits[0]!.ledger : "",
    deducteeType: stored.deductee_type === "IND_HUF" ? "IND_HUF" : "OTHER",
    panAvailable: stored.pan_available,
  };
}
