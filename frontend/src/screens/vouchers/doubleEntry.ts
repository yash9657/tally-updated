import type { DraftAllocation } from "./costAllocation";
import type { DraftBillRef } from "./billAllocation";
// ⛔ THE EXACT BigInt-SCALED HELPERS, NOT money.ts's parseFloat-based ones.
// A voucher must balance TO THE PAISA — the server's double-entry constraint has
// no tolerance — so the comparison that decides whether to send must be done in
// integers. money.ts routes through Number.parseFloat, and two doubles that
// differ in the fifteenth place would let this module call a voucher balanced
// that the server then refuses, with the operator's whole form as the cost.
import { formatFixed4, parseFixed4 } from "./itemInvoice";

/**
 * DOUBLE-ENTRY VOUCHER ENTRY — the pure half. The grid UI is in
 * DoubleEntryPanel.tsx.
 *
 * ===========================================================================
 * WHY THIS MODULE EXISTS: F7 OPENED A FORM THAT COULD NOT PRODUCE A VOUCHER
 * ===========================================================================
 *
 * `journal` is `entry: "double"` and VoucherScreen's `double` branch rendered
 * NOTHING — after Date / No. / Party / Narration the panel chain fell through
 * to `null`. `buildPlainRequest` forwards `f.postings`, which was only ever
 * populated by hydrating a LOADED voucher or an OCR draft, so nothing on the
 * screen could add a posting. Measured by driving the shell on 2026-08-06: the
 * F7 form rendered EXACTLY ONE INPUT, "Dr/Cr Ledger" — no amount field, no
 * second line, no side selector.
 *
 * That is not "a Journal is awkward to key". It is **a Journal cannot be keyed
 * at all**, on a FeaturesScreen row that read `on` — the hidden kind of parity
 * gap (HANDOFF §5.1), and worse than an honest `off` because an evaluator hits
 * it in week one and concludes the product is broken rather than incomplete.
 * A Journal is how every adjustment, provision, depreciation and reclass is
 * entered, so this blocked every business at every period end.
 *
 * ⛔ AND THE HOLE WAS WIDER THAN §4.5 SAID — it is FOUR classes, not one.
 * `effectiveEntryMode` honours a Ctrl+H choice of "double" on the single-entry
 * classes too, so **Payment, Receipt and Contra in Double Entry mode rendered
 * the same empty form**. §4.5 named only the Journal because only the Journal
 * *defaults* to double entry; the other three needed one keystroke to reach the
 * identical dead end. Pinned by test — see doubleEntry.test.ts.
 *
 * ===========================================================================
 * ⛔ THE SINGLE-ENTRY PANEL IS NOT THE MODEL TO COPY
 * ===========================================================================
 *
 * Single entry INFERS the primary leg and puts the SUM on it: the operator
 * names one account, lists what it moved against, and never types the total.
 * A double-entry voucher STATES BOTH SIDES — that is the entire point of it,
 * and it is the opposite operation. So:
 *
 *  - every row carries its OWN side (`DR`/`CR`); nothing is inferred;
 *  - no leg is synthesised, so the rows map to the request's `postings` array
 *    ONE-FOR-ONE FROM INDEX 0 — hence `buildCostAllocations(rows, 0)` at the
 *    call site, where single entry passes 1 (HANDOFF §1.2);
 *  - the form must REFUSE an unbalanced body itself. The server refuses it too,
 *    but a 422 does not survive the round trip in a shape the operator can act
 *    on — it names a constraint, not the two figures and their difference.
 */

/** One keyed Dr/Cr line. */
export interface DoubleEntryLine {
  /**
   * The ledger NAME. Carried as a name (never a uuid) because that is what the
   * operator picks and what a hydrated row can be re-rendered from; the real
   * uuid is resolved on accept through `ensureLedger`, exactly as a
   * single-entry particular is.
   *
   * ⚠️ DELIBERATELY NOT A CACHED uuid ON THE ROW. Hydrating an alter with the
   * loaded `ledger_id` would round-trip one paisa cheaper and would go STALE
   * the moment the operator edits the name — the edit would be silently
   * ignored and the voucher would re-post the ledger they just changed away
   * from. `ledger_company_id_name_key` makes name -> id a bijection within a
   * company, so resolving by name is exact, and `ensureLedger` memoises it.
   */
  ledger: string;
  /** THE SIDE THIS LINE IS POSTED ON. Stated, never inferred. */
  side: "DR" | "CR";
  /** fixed-4 on the wire; free-typed here. */
  amount: string;
  /**
   * How this line's posting is split across cost centres (T2).
   *
   * ⛔ IT LIVES ON THE ROW so the 0-based `posting_seq` it is sent with is
   * derived from the row's own position at the moment the postings array is
   * assembled. See `buildCostAllocations`, which takes the primary-leg offset
   * explicitly — and which this grid calls with 0, because there is no primary
   * leg to skip.
   */
  allocations: readonly DraftAllocation[];
  /**
   * Which bills this line settles or opens.
   *
   * ⛔ ON THE ROW, and for the same reason single entry puts it there: the
   * bill-bearing leg of a Journal is a LINE, not the party field. A Journal
   * that writes off a customer's balance credits the debtor on one of these
   * rows; the party field names no ledger at all.
   */
  billRefs: readonly DraftBillRef[];
  /**
   * ⭐⭐ T8's THREE FOREX VALUES, AND STEP 8 IS WHERE THEY BECAME EDITABLE.
   *
   * ⛔⛔ THEY WERE MISSING FROM THIS TYPE AND THAT WAS DEFECT NUMBER NINE — the
   * one §6.1 of `HANDOFF-T8-STEPS-6-TO-8.md` tells step 8 to go and look for by
   * asking *"what does an alter through this form omit?"*. It omitted these.
   *
   * `postingsFromView` in VoucherScreen.tsx carries the triple for the
   * SINGLE-ENTRY path and has a long comment saying it is the seventh time that
   * line has had to be written. `DraftBillRef` carries it for the bill references.
   * **The DOUBLE-ENTRY grid did not** — so opening a foreign JOURNAL (F7 always
   * uses this grid), or a foreign Receipt in Double Entry mode, fixing a typo and
   * saving turned a $1,000 bill into a ₹83,651.23 bill. ⚠️ AND NOTHING REFUSED
   * IT: the voucher still balances in base, the Balance Sheet does not move by a
   * paisa, and the only trace is that Outstandings stops showing the bill as
   * foreign. Measured before it was fixed — see the L39 audit entry.
   *
   * ⚠️ ALL THREE OR NONE, KEYED OFF THE CURRENCY. Migration 0080's
   * `posting_forex_all_or_none` CHECK refuses a two-of-three row, because a
   * foreign amount with no rate cannot be checked against the base and a rate
   * with no amount prices nothing. `doubleEntryForexRefusal` turns that into a
   * sentence the FORM says before any request is made.
   *
   * ⚠️ AND `rate` IS A TEN-DECIMAL RATE, NOT MONEY. Never round-trip it through
   * `parseFixed4` / `formatFixed4` like `amount` above — see api/rate.ts, whose
   * header explains that a rate through the money helpers becomes a DIFFERENT
   * rate that round-trips perfectly and prices the bill wrong for ever.
   *
   * `currencyCode` is display only and is never sent; the server resolves the id.
   */
  currencyId?: string;
  currencyCode?: string;
  foreignAmount?: string;
  rate?: string;
}

/** A fresh, empty line. Debit first — a Journal is read Dr-then-Cr. */
export function emptyDoubleEntryLine(): DoubleEntryLine {
  return { ledger: "", side: "DR", amount: "", allocations: [], billRefs: [] };
}

/**
 * ⛔ THE ALL-OR-NONE REFUSAL, SAID BY THE FORM BEFORE ANY REQUEST IS MADE.
 *
 * Migration 0080's CHECK and `ledger.forexTriple.validate` both refuse a
 * two-of-three row, and the server's message names which value is missing. This
 * says the same thing without a round trip — and, more importantly, without
 * `ensureLedger` having already created every account the operator named, which
 * is defect D39's shape and the reason every refusal on this form is evaluated
 * in one expression before anything is resolved.
 *
 * ⚠️ IT RETURNS "" FOR A GRID WITH NO CURRENCY ANYWHERE, which is every voucher
 * in the field today, so this costs a base-currency entry nothing.
 */
export function doubleEntryForexRefusal(
  lines: readonly DoubleEntryLine[],
): string {
  for (const [i, l] of keyedLines(lines).entries()) {
    const cur = (l.currencyId ?? "").trim();
    const fgn = (l.foreignAmount ?? "").trim();
    const rate = (l.rate ?? "").trim();
    const n = i + 1;
    if (cur === "" && fgn === "" && rate === "") continue;
    if (cur === "") {
      return (
        `Line ${n} names a foreign amount or a rate and no CURRENCY. A foreign ` +
        `figure needs all three of currency, foreign amount and rate, or none of ` +
        `them — a line with no currency IS in the company's own currency.`
      );
    }
    if (fgn === "" || Number(fgn) === 0) {
      return (
        `Line ${n} names a currency and no FOREIGN AMOUNT. State how much of ` +
        `that currency the line is for, or clear the currency to key it in the ` +
        `company's own.`
      );
    }
    if (Number(fgn) < 0) {
      return (
        `Line ${n} has a negative foreign amount. Direction is carried by Dr/Cr, ` +
        `so every amount on a line is unsigned.`
      );
    }
    if (rate === "" || Number(rate) === 0) {
      return (
        `Line ${n} names a currency and no EXCHANGE RATE. The rate is how many ` +
        `of the company's own units ONE unit of that currency is worth, and it ` +
        `is stored on the voucher for ever — a later rate never restates it.`
      );
    }
    if (Number(rate) < 0) {
      return (
        `Line ${n} has a negative exchange rate. A rate is base units per ONE ` +
        `foreign unit and is always above zero.`
      );
    }
  }
  return "";
}

/**
 * The lines that will actually be SENT: a ledger AND a non-zero amount.
 *
 * ⚠️ EXPORTED, AND EVERY CALLER MUST USE IT rather than filtering inline. The
 * cost splits' `posting_seq` is this array's index, so the filter that decides
 * which rows are sent and the loop that builds the postings must be THE SAME
 * ARRAY. Two independently-written filters that agree today are how an
 * allocation lands on the row above the one it was keyed against.
 *
 * A blank trailing row is normal — the grid always offers one more line — so
 * dropping empties here is what lets it do that without forcing the operator to
 * fill it. A row with a ledger and no amount is likewise a row in progress, not
 * a zero posting: the server refuses a zero-amount posting outright.
 */
export function keyedLines(
  lines: readonly DoubleEntryLine[],
): DoubleEntryLine[] {
  return lines.filter(
    (l) => l.ledger.trim() !== "" && parseFixed4(l.amount) !== 0n,
  );
}

/** Both sides of a grid, and what is between them. All fixed-4 strings. */
export interface DoubleEntryTotals {
  debit: string;
  credit: string;
  /** debit − credit. Zero (and only zero) is saveable. Signed. */
  difference: string;
  /** True when the two sides agree EXACTLY and something was actually keyed. */
  balanced: boolean;
}

/**
 * Total each side, in integer ten-thousandths.
 *
 * ⚠️ `balanced` REQUIRES A NON-ZERO TOTAL. An empty grid has debits equal to
 * credits (both zero) and is therefore "balanced" arithmetically, which would
 * let an empty form post a voucher with no postings. Zero == zero is the
 * degenerate case every balance check has, and it is the one that ships.
 */
export function doubleEntryTotals(
  lines: readonly DoubleEntryLine[],
): DoubleEntryTotals {
  let dr = 0n;
  let cr = 0n;
  for (const l of keyedLines(lines)) {
    const amt = parseFixed4(l.amount);
    if (l.side === "DR") dr += amt;
    else cr += amt;
  }
  return {
    debit: formatFixed4(dr),
    credit: formatFixed4(cr),
    difference: formatFixed4(dr - cr),
    balanced: dr === cr && dr !== 0n,
  };
}

/**
 * A REFUSAL for the whole grid, or "" when it is saveable.
 *
 * ⛔ THIS IS THE CONTROL THE ACCEPTANCE TEST TURNS ON: an unbalanced grid must
 * be refused BY THE FORM, before any request is made. The server refuses it
 * too — that is not in question — but its 422 names a database constraint,
 * arrives after the round trip, and tells the operator neither figure nor the
 * difference between them.
 *
 * ⚠️ AND IT MUST BE CALLED BEFORE `ensureLedger`, NOT AFTER. `ensureLedger`
 * CREATES a ledger that does not exist yet, so validating after resolution
 * would mean every rejected Ctrl+A on a misspelled row leaves a real account
 * behind in the chart. Pressing Ctrl+A five times on an unbalanced journal
 * would silently create five ledgers and post nothing. See
 * buildDoubleEntryRequest, which validates first and says so.
 *
 * Each message names BOTH figures and the difference, because "does not
 * balance" without them makes the operator do the arithmetic the machine
 * already did.
 */
export function doubleEntryRefusal(
  lines: readonly DoubleEntryLine[],
): string {
  const rows = keyedLines(lines);
  if (rows.length === 0) {
    return (
      "Nothing has been keyed. A double-entry voucher states both sides " +
      "explicitly: name a ledger, choose Dr or Cr, and give an amount on at " +
      "least one line of each side."
    );
  }
  if (rows.length < 2) {
    return (
      "A double-entry voucher needs at least two lines — one debit and one " +
      "credit. A single line cannot balance against anything."
    );
  }

  // A negative amount is refused rather than reinterpreted. Tally's own answer
  // to "this should have been on the other side" is to key it on the other
  // side, and a -500 debit that silently becomes a 500 credit would make the
  // Dr/Cr selector a lie about what was posted.
  for (const l of rows) {
    if (parseFixed4(l.amount) < 0n) {
      return (
        `Line "${l.ledger.trim()}" has a NEGATIVE amount ` +
        `(${formatFixed4(parseFixed4(l.amount))}). A posting's amount is ` +
        `unsigned and the Dr/Cr selector carries its direction — key it on ` +
        `the other side instead.`
      );
    }
  }

  const t = doubleEntryTotals(lines);
  if (parseFixed4(t.debit) === 0n && parseFixed4(t.credit) === 0n) {
    return "Every line is zero — there is nothing to post.";
  }
  if (parseFixed4(t.debit) === 0n || parseFixed4(t.credit) === 0n) {
    const present = parseFixed4(t.debit) === 0n ? "credits" : "debits";
    const missing = parseFixed4(t.debit) === 0n ? "debit" : "credit";
    return (
      `Every line is on the same side — this voucher has ${present} only ` +
      `(${parseFixed4(t.debit) === 0n ? t.credit : t.debit}) and cannot ` +
      `balance. Change at least one line to ${missing.toUpperCase()}.`
    );
  }
  if (!t.balanced) {
    const diff = parseFixed4(t.difference);
    const over = diff > 0n ? "Debits exceed credits" : "Credits exceed debits";
    return (
      `${over} by ${formatFixed4(diff < 0n ? -diff : diff)} — debits total ` +
      `${t.debit} against credits of ${t.credit}. A voucher must balance ` +
      `EXACTLY, to the paisa; there is no tolerance.`
    );
  }
  return "";
}

/**
 * Re-hydrate the grid from a fetched voucher.
 *
 * ⛔⛔ OMITTING THIS WOULD BE DATA LOSS OF THE WHOLE VOUCHER, NOT OF A DETAIL,
 * AND IT IS THE MOST DANGEROUS LINE IN THIS TRACK.
 *
 * `posting` is keyed on `voucher_version_id`, so an ALTER RE-STATES: the new
 * version's postings are exactly what the request body carries. Before this
 * track the double branch sent `f.postings` verbatim through
 * `buildPlainRequest`, so an alter round-tripped whatever the GET returned.
 * The grid now OWNS that path — `buildDoubleEntryRequest` builds `postings`
 * from these rows and nothing else — so a hydration that returned an empty
 * grid would make "open a Journal, fix a typo in the narration, save" post a
 * voucher with NO POSTINGS AT ALL, silently reversing the entry out of every
 * balance.
 *
 * That is precisely the shape HANDOFF §4.3 documents four times over, and the
 * reason it keeps recurring is that zero rows is a LEGAL state: no trigger
 * fires, nothing fails to add up, and the only trace is a Profit & Loss that
 * quietly stops agreeing with itself. Pinned by
 * `TestAnAlteredJournalRebuiltFromTheGetKeepsItsPostings`, WITH the control
 * that an alter omitting them destroys the entry.
 *
 * ⚠️ EVERY posting is taken, both sides, in the response's own order — unlike
 * `linesFromPostings`, which keeps only the many side because single entry
 * synthesises the other. Here there is no side to synthesise.
 *
 * ⚠️ THE SEQ IS CAPTURED BEFORE ANYTHING IS DROPPED, because
 * `cost_allocations[].posting_seq` indexes the response's FULL postings array.
 * Nothing is dropped here today, so index and seq agree — the `map` states the
 * seq anyway rather than relying on that, since a later filter added above this
 * line would otherwise re-attach every split to the wrong row silently.
 */
export function doubleEntryLinesFromView(
  postings: readonly {
    ledger: string;
    ledger_id: string;
    dr_cr: string;
    amount: string;
    // ⭐⭐ T8's TRIPLE, READ BACK OFF THE VIEW — AND THIS IS DEFECT NUMBER NINE's
    // FIX. These four fields were absent from this signature, so an alter through
    // the Dr/Cr grid rebuilt every row WITHOUT its currency and the server —
    // which re-states `posting` wholesale from the request body — wrote a rupee
    // voucher over a foreign one. See DoubleEntryLine's own comment.
    currency_id?: string;
    currency?: string;
    foreign_amount?: string;
    rate?: string;
  }[],
  allocationsFor: (seq: number) => DraftAllocation[],
  billRefsFor: (ledgerID: string) => DraftBillRef[],
): DoubleEntryLine[] {
  // ⚠️ A ledger posted on more than one line would otherwise be given its bill
  // references TWICE — once per row — and the rebuilt voucher would claim to
  // settle double what it settles, which the server's sum-to-posting invariant
  // then refuses with the operator's whole form as the cost. The references
  // belong to the LEDGER, not to the row, so the first row carrying it takes
  // them. Same rule as `linesFromPostings`, for the same reason.
  const claimed = new Set<string>();
  const rows: DoubleEntryLine[] = postings.map((p, seq) => ({
    ledger: p.ledger,
    side: (p.dr_cr.toUpperCase().startsWith("D") ? "DR" : "CR") as "DR" | "CR",
    amount: p.amount,
    allocations: allocationsFor(seq),
    billRefs: claimed.has(p.ledger_id)
      ? []
      : (claimed.add(p.ledger_id), billRefsFor(p.ledger_id)),
    // ⛔ ALL THREE OR NONE, KEYED OFF THE CURRENCY, exactly as `postingsFromView`
    // does it: emitting a `rate` beside an absent `currency_id` would 422 a
    // request that looks complete on this side. ⚠️ And `rate` is passed through
    // UNTOUCHED — it is a ten-decimal rate and re-formatting it with this file's
    // money helpers would hand back 83.6512 and reprice the bill.
    ...(p.currency_id
      ? {
          currencyId: p.currency_id,
          currencyCode: p.currency ?? "",
          foreignAmount: p.foreign_amount ?? "",
          rate: p.rate ?? "",
        }
      : {}),
  }));
  return rows.length > 0 ? rows : [emptyDoubleEntryLine()];
}
