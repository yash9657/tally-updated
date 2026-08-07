import type {
  Keymap,
  KeymapAction,
  KeyCombo,
  ScreenKind,
} from "../types";
import { normalizeCombo } from "../normalize";

/**
 * The TallyPrime keymap profile (RESEARCH.md sec 4 "Function keys F1–F12",
 * "F-key combinations", "Navigation & accept keys", and sec 8 report buttons).
 *
 * This is DATA: a combo → abstract-action table. An ERP 9 profile can be added
 * later by authoring a second table (e.g. Credit Note Ctrl+F8 vs Alt+F6) and
 * swapping it in — the navigation machine never changes. Keys are authored in
 * any human-friendly form and normalized at module load, so lookups are O(1)
 * against the same canonical strings the corpora produce.
 *
 * COVERAGE RULE (why this table is much longer than the six voucher keys): a
 * Tally operator's hands know the whole board. A combo that does NOTHING is
 * worse than one that says "not in this edition", because the user cannot tell
 * a missing feature from a broken app. So EVERY documented TallyPrime combo is
 * bound here — to a real action where we have the feature, and to an explicit
 * `unavailable` action (which surfaces a status-line message) where we do not.
 */

/** Global bindings — the default for every screen. */
const RAW_BINDINGS: Record<string, KeymapAction> = {
  // ---- Core voucher F-keys (RESEARCH sec 4 table; sec 5 voucher shortcuts).
  F4: { kind: "voucher", voucherType: "contra" },
  F5: { kind: "voucher", voucherType: "payment" },
  F6: { kind: "voucher", voucherType: "receipt" },
  F7: { kind: "voucher", voucherType: "journal" },
  F8: { kind: "voucher", voucherType: "sales" },
  F9: { kind: "voucher", voucherType: "purchase" },

  // ---- Navigation grammar (the load-bearing keys).
  Enter: { kind: "enter" },
  Esc: { kind: "escape" },
  "Ctrl+A": { kind: "accept" }, // Accept / Save screen
  "Ctrl+Q": { kind: "quit" }, // Quit screen WITHOUT saving
  Tab: { kind: "nextField" },
  "Shift+Tab": { kind: "prevField" },
  PageUp: { kind: "prevVoucher" }, // previous saved voucher (alteration)
  PageDown: { kind: "nextVoucher" }, // next saved voucher (alteration)
  "Alt+D": { kind: "delete" }, // Delete the loaded voucher (alteration mode)
  "Alt+X": { kind: "cancelVoucher" }, // Cancel the voucher being entered
  "Alt+2": { kind: "duplicate" }, // Duplicate the loaded voucher
  "Alt+G": { kind: "goto" }, // Go To (overlay, keeps prior screen)
  "Ctrl+G": { kind: "switchTo" }, // Switch To (replaces the stack)
  "Ctrl+H": { kind: "changeMode" }, // Change Mode picker (in vouchers)
  F2: { kind: "date" }, // Date (voucher); reports override to period
  "Alt+F2": { kind: "period" }, // Change period (From/To)
  "Alt+F1": { kind: "detailed" }, // Detailed / condensed
  "Ctrl+F": { kind: "filter" }, // Filter rows
  "Alt+E": { kind: "exportScreen" }, // Export the active screen
  // ---- PRINT. Two combos, two DIFFERENT Tally actions (RESEARCH.md:2165:
  // ERP 9 Alt+P; TallyPrime "Ctrl+P (voucher) / Alt+P (print menu)").
  //
  // Both were MISSING from this table entirely — not bound to a real action and
  // not bound to `unavailable` — which made Ctrl+P the one documented combo that
  // fell through to the browser. It opened the BROWSER'S print dialog and printed
  // a screenshot of this dark-background terminal emulation: no Rule 46
  // particulars, no GSTIN block, no tax summary. That is a violation of this
  // file's own coverage rule in its worst form, because the key did not do
  // nothing — it did something wrong that looked like a document.
  //
  // Alt+P carries viaOptionsMenu so it can say that the print OPTIONS menu
  // (copies, page range, Print Before Save rules) is not built and that it
  // printed with defaults, rather than silently pretending to be Ctrl+P.
  "Ctrl+P": { kind: "print" },
  "Alt+P": { kind: "print", viaOptionsMenu: true },
  "Ctrl+N": { kind: "calculator" }, // Calculator panel
  F1: { kind: "help" }, // Help
  "Ctrl+F1": { kind: "help" }, // Context help
  F11: { kind: "features" }, // Company Features
  F12: { kind: "configure" }, // Configure (screen/report settings)
  // Alt+C — create a master inline (e.g. a new ledger from a voucher field).
  // The default master is a ledger, the by-far most common inline create; the
  // navigation machine returns to the originating frame on accept/Esc.
  "Alt+C": { kind: "createMaster", masterKind: "ledger" },

  // ---- Documented TallyPrime combos whose FEATURE is not in this edition.
  // Bound deliberately so the key answers instead of dying silently.
  // F3 — Switch Company. A REAL action since the admin API made a second
  // membership reachable: it lists the companies this user may open (GET
  // /v1/me) and re-scopes the session to the chosen one.
  F3: { kind: "switchCompany" },
  "Alt+F3": {
    kind: "unavailable",
    feature: "Select Company (Alt+F3)",
    note: "Opening a second company from disk is not built yet.",
  },
  "Ctrl+F3": {
    kind: "unavailable",
    feature: "Shut Company (Ctrl+F3)",
    note: "Company shut/close is not built yet.",
  },
  // F10 is TallyPrime's "Other Vouchers" MENU — the flattened list that Prime
  // consolidated the order and note vouchers under (RESEARCH.md:641). The MENU is
  // what is missing here, not the vouchers on it: this note used to say "Order
  // vouchers are not built", which was true when it was written and is now false in
  // a way that would send an operator looking for a feature they already have.
  // Every destination the menu would offer has its own working key, so the note
  // names them.
  F10: {
    kind: "unavailable",
    feature: "Other Vouchers (F10) — the MENU, not the vouchers on it",
    note: "This build has no F10 picker; every voucher it would list has its own key. Orders: Ctrl+F8 Sales Order, Ctrl+F9 Purchase Order — an order is a COMMITMENT and not a movement, so those open the order REGISTERS rather than a voucher form. Stock: Alt+F7 Stock Journal, Alt+F8 Delivery Note, Alt+F9 Receipt Note, Alt+F10 Physical Stock. F4–F9 are the six accounting vouchers and Alt+F5/Alt+F6 the debit/credit notes. Rejections In/Out are genuinely not built.",
  },
  // Debit / Credit Note — REAL voucher screens with their own numbering series
  // and their own GSTR-1 treatment (cdnr, table 9B). They were bound as
  // "unavailable" until the classes existed; they are the correct way to
  // reverse part of an invoice, so they must be reachable by their real keys.
  "Alt+F5": { kind: "voucher", voucherType: "debit-note" },
  "Alt+F6": { kind: "voucher", voucherType: "credit-note" },
  // ---- The NON-ACCOUNTING STOCK VOUCHERS (Alt+F7/F8/F9/F10).
  //
  // These four were `unavailable` in two stages: first because the SERVER could
  // not record a stock movement without also recording revenue, and then
  // because the movement plane existed but there was no ENTRY SCREEN — a
  // `{ kind: "voucher", voucherType: "delivery-note" }` binding would have
  // navigated to VoucherScreen, which resolves its layout through
  // configFor(voucherType), a Readonly<Record<VoucherType, VoucherConfig>> with
  // no entry for these classes. The key would not have "partly worked"; it
  // would have taken the operator to an undefined config.
  //
  // BOTH ARE NOW DONE, in the change that flipped these bindings: VoucherType
  // carries the four classes, VOUCHER_CONFIGS has a real config with a real
  // post/alter for each, and StockVoucherScreen renders the grid (items,
  // godowns, quantities and NO ledger column). They are advertised on the
  // voucher button bar in the same change, which buttons.test.ts permits only
  // because they no longer resolve to `unavailable`.
  "Alt+F7": { kind: "voucher", voucherType: "stock-journal" },
  "Alt+F8": { kind: "voucher", voucherType: "delivery-note" },
  "Alt+F9": { kind: "voucher", voucherType: "receipt-note" },
  // Alt+F10 is TallyPrime's Physical Stock key and it was bound to NOTHING at
  // one point, which this file's coverage rule calls the worst outcome of all:
  // the user cannot tell a missing feature from a broken app. Note that the
  // seeded Data Entry level is deliberately REFUSED this class (migration
  // 0031), so the key opens the screen for everyone and the POST is what
  // answers 403 — with a stated reason, not a blank form.
  "Alt+F10": { kind: "voucher", voucherType: "physical-stock" },
  "Ctrl+F4": {
    kind: "unavailable",
    feature: "Payroll voucher (Ctrl+F4)",
    note: "The payroll module is not built yet.",
  },
  // Ctrl+F7 was ERP 9's Physical Stock key. TallyPrime moved it to Alt+F10
  // (bound above), so this profile keeps Ctrl+F7 bound — an operator with ERP 9
  // in their fingers WILL press it — but says where the key went rather than
  // claiming Physical Stock lives here.
  "Ctrl+F7": {
    kind: "unavailable",
    feature: "Physical Stock (Ctrl+F7 — TallyPrime moved it to Alt+F10)",
    note: "In this profile the physical stock count is Alt+F10 — press that instead; it opens the real screen.",
  },
  // ---- THE ORDER REGISTERS (T3). Both were `unavailable` ("Order processing is
  // not built yet") for the whole life of the project up to here, in two stages
  // and for two different reasons — the same two-stage history the four stock
  // voucher keys above have.
  //
  // First the SERVER could not hold a commitment at all: the stock ledger has one
  // primitive (a positive quantity plus a direction) and an order is neither, so
  // pushing one through `stock_movement` would have corrupted closing stock — which
  // now feeds the Balance Sheet. Then the order tables, the fulfilment links, both
  // books and all fourteen client methods existed and there was NO SCREEN, so a
  // keyboard-only operator could not create an order at all and the F11 Features
  // row for T3 said `off` while every test passed.
  //
  // ⛔ TWO KEYS AND TWO SCREENS, NOT ONE WITH A TOGGLE. `sales-order` and
  // `purchase-order` are separate authorities on separate routes and the PDP reads
  // the PATH, so a single screen with a direction switch would be reaching a
  // resource the operator may be denied. Tally has two keys for its own reasons and
  // we need two for this one; they agree.
  // ---- THE e-WAY BILL (T5). TallyPrime files e-way bill actions under its
  // "Z: Exchange" top menu (RESEARCH.md:361) and raises the bill FROM the sales
  // voucher (RESEARCH.md:284). This build has no Exchange MENU, so Alt+Z goes
  // straight to the one thing that menu would offer here.
  //
  // ⚠️ Alt+Z WAS NOT PREVIOUSLY BOUND TO `unavailable`, so there is no stale note
  // to correct — checked, because §7.3 is explicit that the key you want is often
  // already bound with a claim that has rotted.
  //
  // ⚠️ AND THE "one thing that menu would offer" CLAIM ABOVE HAS SINCE EXPIRED:
  // the Exchange menu's OTHER destination is "Send for e-Invoicing"
  // (RESEARCH.md:698), and it now exists too, on Alt+I below. This note is
  // corrected rather than left to rot for exactly the reason §7.3 gives — a
  // sentence that was true when written is the kind that sends an operator
  // looking for a feature they already have. Alt+Z is NOT re-purposed into an
  // Exchange picker: it is browser-verified on the e-way bill and moving it
  // would spend that evidence to save one keystroke.
  "Alt+Z": { kind: "eWayBill" },
  // ---- THE e-INVOICE (§1.4 B). The Exchange menu's second destination. See the
  // `eInvoice` action in keystroke/types.ts for why it is a SCREEN and not a
  // voucher key, and why Alt+I rather than a re-used Alt+Z.
  "Alt+I": { kind: "eInvoice" },
  "Ctrl+F8": { kind: "orderRegister", direction: "sales" },
  "Ctrl+F9": { kind: "orderRegister", direction: "purchase" },
};

/**
 * THE TWELVE VOUCHER KEYS, HELD BACK ON A SCREEN THAT CARRIES A FORM.
 *
 * ⛔ AN UNBOUND KEY IS NOT INERT ON THESE SCREENS — that is the whole reason this
 * helper exists. The global table sends F5 to a Payment voucher, F9 to a Purchase
 * and F8 to a Sales, so a screen with a half-keyed form on it and no override gets
 * a VOUCHER FORM pushed over that form, and the frame holding the operator's work
 * is gone with it. That is not a missing feature but a destructive one, which is
 * the case this file's coverage rule exists for.
 *
 * The feature is built and is one Esc away in every case, and that is what each
 * note says — rather than pretending the voucher does not exist.
 *
 * ⚠️ WHY THIS IS A FUNCTION AND THE TWO SCREENS BELOW IT WERE HAND-WRITTEN. It was
 * ~120 lines of near-identical prose per screen, and the fifth copy is where the
 * copies start to drift: a paste that keeps the previous screen's noun tells the
 * operator the voucher keys are held back on a screen they are not on. Naming the
 * screen ONCE, as data, is what makes the fifth copy safe. `what` completes the
 * sentence "…would abandon the ___ you are keying".
 */
function voucherKeysHeldBack(
  screen: string,
  what: string,
): Record<string, KeymapAction> {
  const held: [string, string][] = [
    ["F4", "Contra voucher (F4)"],
    ["F5", "Payment voucher (F5)"],
    ["F6", "Receipt voucher (F6)"],
    ["F7", "Journal voucher (F7)"],
    ["F8", "Sales voucher (F8)"],
    ["F9", "Purchase voucher (F9)"],
    ["Alt+F5", "Debit Note (Alt+F5)"],
    ["Alt+F6", "Credit Note (Alt+F6)"],
    ["Alt+F7", "Stock Journal (Alt+F7)"],
    ["Alt+F8", "Delivery Note (Alt+F8)"],
    ["Alt+F9", "Receipt Note (Alt+F9)"],
    ["Alt+F10", "Physical Stock (Alt+F10)"],
  ];
  const out: Record<string, KeymapAction> = {};
  for (const [combo, feature] of held) {
    out[combo] = {
      kind: "unavailable",
      feature: `${feature} — not from ${screen}`,
      note:
        `The voucher keys are held back on ${screen}: opening a voucher over it ` +
        `would abandon the ${what} you are keying. Esc leaves the form, then ` +
        `${combo} works as it always does.`,
    };
  }
  return out;
}

/**
 * Per-screen OVERRIDES. Tally re-uses the same physical key for a different
 * action depending on the screen — replicating that is the whole point of
 * keystroke fidelity, so the divergences are declared as data here rather than
 * as `if (screen === ...)` branches inside the machine.
 */
const RAW_SCREEN_BINDINGS: Partial<
  Record<ScreenKind, Record<string, KeymapAction>>
> = {
  // Inside a report, F2 is the PERIOD (not a voucher date), F5 toggles
  // ledger-wise, Ctrl+H changes the VIEW (horizontal/vertical) and Alt+F5 is
  // the second documented "detailed" toggle (RESEARCH sec 8 button grammar).
  report: {
    F2: { kind: "period" },
    F5: { kind: "ledgerWise" },
    "Ctrl+H": { kind: "changeView" },
    "Alt+F5": { kind: "detailed" },
  },
  "report-detail": {
    F2: { kind: "period" },
    F5: { kind: "ledgerWise" },
    "Ctrl+H": { kind: "changeView" },
    "Alt+F5": { kind: "detailed" },
  },
  // THE BANK RECONCILIATION SCREEN. Every key here is taken from RESEARCH.md:879
  // ("Key shortcuts inside reconciliation"), which is the only source this repo
  // has for the screen's own button grammar. Nothing below is inferred.
  //
  // A NOTE ON HOW THIS SCREEN IS REACHED, because an earlier draft got it wrong.
  // It is tempting to bind F5 on a bank ledger's voucher report to "reconcile" —
  // that IS the gesture, but it is Tally.ERP 9's, and this is the TALLYPRIME
  // profile. RESEARCH.md:873 says Prime reaches reconciliation through
  // "Alt+G > Banking Activities", and RESEARCH.md:879 says F5 INSIDE
  // reconciliation is the amount-view toggle, not the way in. So the way in here
  // is the Gateway entry / Go To (Prime's own path) and F4 below picks the
  // account, exactly as :879 describes. Binding the ERP 9 gesture in the Prime
  // table would have been a keystroke this product invented, which is the one
  // thing its moat cannot afford.
  "bank-reconciliation": {
    // The statement is a POSITION AS AT A DATE, so F2 is the statement date —
    // the same overlay the Trial Balance and Balance Sheet use, which already
    // says only "To" applies to a position. Reconciling in arrears (June's
    // statement in July) is the ORDINARY case, so a screen fixed at today would
    // be wrong most of the time.
    F2: { kind: "period" },
    // F4 — "select bank ledger" (:879). This is the account picker, and it is
    // why the screen does not need a Gateway sub-menu of its own.
    F4: { kind: "bankReconciliation" },
    // F5 — "amount-view toggle" (:879). NOT built. It is bound anyway, and
    // that is this file's coverage rule doing its job: without an entry here F5
    // falls through to the GLOBAL binding and opens a PAYMENT VOUCHER on top of
    // the statement, which is not a missing feature but a wrong one.
    F5: {
      kind: "unavailable",
      feature: "Amount view (F5)",
      note: "The deposits/withdrawals amount-view toggle is not built. The statement shows one Amount column with its Dr/Cr side.",
    },
    "Alt+F5": { kind: "detailed" },
    "Ctrl+H": { kind: "changeView" },
    // Alt+O — IMPORT (RESEARCH.md:361 "O: Import", :415, :1221). Tally's
    // documented global for import; scoped to this screen because a statement
    // belongs to ONE bank account and only the screen knows which is selected.
    "Alt+O": { kind: "importBankStatement" },
    // The MATCHING keys (:879) belong to slice 2b and are still refusals — but
    // their notes now name 2b, not "slice 2", because HALF of slice 2 has
    // shipped: the import is real and only the matching is missing. A key that
    // says "import is not built" on a screen with a working import teaches an
    // operator to distrust every other refusal in the product.
    F8: {
      kind: "unavailable",
      feature: "Modify match (F8)",
      note: "Statements now import (Alt+O), but MATCHING them to vouchers is T4 slice 2b and is not built, so there are no matches to modify yet.",
    },
    F9: {
      kind: "unavailable",
      feature: "Set status manually (F9)",
      note: "Statuses come from matching imported rows to vouchers, which is T4 slice 2b and not built. Enter a Bank Date against a row instead — that IS the manual reconciliation.",
    },
    "Alt+F8": {
      kind: "unavailable",
      feature: "Unlink a match (Alt+F8)",
      note: "Nothing is matched yet: statements import (Alt+O) but matching is T4 slice 2b, so there is nothing to unlink.",
    },
    "Alt+S": {
      kind: "unavailable",
      feature: "Potential matches (Alt+S)",
      note: "Automatic matching is T4 slice 2b. Note its governing rule when it lands: an AMBIGUOUS match is never auto-cleared — it surfaces BOTH candidates and waits for you.",
    },
    // Ctrl+J and Ctrl+B are at RESEARCH.md:879 and are bound to NEITHER an
    // action nor a refusal anywhere in this file — so they are inert TODAY and
    // would silently inherit any future global binding for the same combo. The
    // slice-2 memo §6 asks for them to be bound while their absence is still
    // harmless, and Ctrl+J especially: an "exception report" is a matching
    // concept and will be reached for the week matching ships.
    "Ctrl+J": {
      kind: "unavailable",
      feature: "Exception report (Ctrl+J)",
      note: "The reconciliation exception report is not built. It reports on MATCHING, which is T4 slice 2b.",
    },
    "Ctrl+B": {
      kind: "unavailable",
      feature: "Basis of values (Ctrl+B)",
      note: "The basis-of-values panel is not built on this screen. The statement's own arithmetic is on its face: book balance, un-cleared items, bank balance, and a difference that must be exactly zero.",
    },
  },
  // THE TDS CHALLAN REGISTER (T6). This screen carries an ENTRY FORM, and that
  // is what every binding below is about: AN UNBOUND KEY HERE IS NOT INERT.
  // Without these entries the GLOBAL voucher keys fire — F5 opens a Payment
  // voucher, F9 a Purchase, F8 Sales — on top of a half-keyed counterfoil, and
  // the frame that held the form is gone with it. That is not a missing feature
  // but a destructive one, which is the case this file's coverage rule exists
  // for (see the F5 note on bank-reconciliation above).
  "tds-challans": {
    // Alt+C — RECORD A DEPOSIT. Tally's create-inline combo (RESEARCH.md:405)
    // scoped to this screen; the global `createMaster` would push a
    // ledger-create frame over the form instead.
    "Alt+C": { kind: "recordTDSChallan" },
    // The voucher keys, held back HERE and only here. The feature is built and
    // is one Esc away — which is what these notes say, rather than pretending
    // the voucher does not exist.
    F4: {
      kind: "unavailable",
      feature: "Contra voucher (F4) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then F4 works as it always does.",
    },
    F5: {
      kind: "unavailable",
      feature: "Payment voucher (F5) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then F5 works as it always does.",
    },
    F6: {
      kind: "unavailable",
      feature: "Receipt voucher (F6) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then F6 works as it always does.",
    },
    F7: {
      kind: "unavailable",
      feature: "Journal voucher (F7) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then F7 works as it always does.",
    },
    F8: {
      kind: "unavailable",
      feature: "Sales voucher (F8) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then F8 works as it always does.",
    },
    F9: {
      kind: "unavailable",
      feature: "Purchase voucher (F9) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then F9 works as it always does.",
    },
    "Alt+F5": {
      kind: "unavailable",
      feature: "Debit Note (Alt+F5) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then Alt+F5 works as it always does.",
    },
    "Alt+F6": {
      kind: "unavailable",
      feature: "Credit Note (Alt+F6) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then Alt+F6 works as it always does.",
    },
    "Alt+F7": {
      kind: "unavailable",
      feature: "Stock Journal (Alt+F7) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then Alt+F7 works as it always does.",
    },
    "Alt+F8": {
      kind: "unavailable",
      feature: "Delivery Note (Alt+F8) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then Alt+F8 works as it always does.",
    },
    "Alt+F9": {
      kind: "unavailable",
      feature: "Receipt Note (Alt+F9) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then Alt+F9 works as it always does.",
    },
    "Alt+F10": {
      kind: "unavailable",
      feature: "Physical Stock (Alt+F10) — not from this form",
      note: "The voucher keys are held back on the challan form: opening a voucher over it would abandon the counterfoil you are keying. Esc leaves the form, then Alt+F10 works as it always does.",
    },
    // F2 would otherwise read as "Date" from the global table, on a screen whose
    // dates are typed into named fields. This register's dimension is a TAX YEAR
    // and it is asked for ON THE FACE of the screen, because it is never derived
    // from the deposit date.
    F2: {
      kind: "unavailable",
      feature: "Period (F2)",
      note: "This register has no period. Its dimension is the TAX YEAR, typed on the screen itself — a challan paid on 7 April 2026 is normally for March of 2025-26, so the year is recorded rather than computed from the deposit date.",
    },
  },
  // ---- THE COST MASTERS SCREEN (T2). It carries a CREATE PANEL, so every
  // binding below is the same argument tds-challans makes: AN UNBOUND KEY HERE IS
  // NOT INERT. Without these entries the global voucher keys fire — F5 a Payment,
  // F9 a Purchase, F8 a Sales — over a half-keyed cost centre, and the frame that
  // held the panel goes with it.
  "cost-centres": {
    // Alt+C — CREATE an axis (cost category) or a centre. Tally's own route to
    // these masters is Alt+G > Create Master > Cost Centre (RESEARCH.md:525) and
    // Alt+C is its create-a-master-on-the-fly key (RESEARCH.md:405). The global
    // `createMaster` binding would push a LEDGER-create frame over this screen —
    // cost masters are deliberately not members of the MasterKind union.
    "Alt+C": { kind: "createCostMaster" },
    // F2 would read as "Date" from the global table. A master list has no date
    // dimension at all: the PERIOD belongs to the two cost reports, which is what
    // this note points at rather than leaving the key to claim it did something
    // (ALWAYS_MEANINGFUL contains `date`, so an unbound F2 here would produce a
    // status line for a key that changed nothing).
    F2: {
      kind: "unavailable",
      feature: "Period (F2)",
      note: "A cost category or centre has no period — it is a master, and it exists until it is altered. The PERIOD belongs to the two cost reports: the Cost Centre Summary (what each centre on one axis cost between two dates) and the Cost Category Summary (how much of the period's cost each axis accounts for). F2 sets it there.",
    },
    F4: {
      kind: "unavailable",
      feature: "Contra voucher (F4) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then F4 works as it always does.",
    },
    F5: {
      kind: "unavailable",
      feature: "Payment voucher (F5) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then F5 works as it always does.",
    },
    F6: {
      kind: "unavailable",
      feature: "Receipt voucher (F6) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then F6 works as it always does.",
    },
    F7: {
      kind: "unavailable",
      feature: "Journal voucher (F7) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then F7 works as it always does.",
    },
    F8: {
      kind: "unavailable",
      feature: "Sales voucher (F8) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then F8 works as it always does.",
    },
    F9: {
      kind: "unavailable",
      feature: "Purchase voucher (F9) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then F9 works as it always does.",
    },
    "Alt+F5": {
      kind: "unavailable",
      feature: "Debit Note (Alt+F5) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then Alt+F5 works as it always does.",
    },
    "Alt+F6": {
      kind: "unavailable",
      feature: "Credit Note (Alt+F6) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then Alt+F6 works as it always does.",
    },
    "Alt+F7": {
      kind: "unavailable",
      feature: "Stock Journal (Alt+F7) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then Alt+F7 works as it always does.",
    },
    "Alt+F8": {
      kind: "unavailable",
      feature: "Delivery Note (Alt+F8) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then Alt+F8 works as it always does.",
    },
    "Alt+F9": {
      kind: "unavailable",
      feature: "Receipt Note (Alt+F9) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then Alt+F9 works as it always does.",
    },
    "Alt+F10": {
      kind: "unavailable",
      feature: "Physical Stock (Alt+F10) — not from this screen",
      note: "The voucher keys are held back on the cost masters screen: opening a voucher over it would abandon the axis or centre you are keying. Esc leaves the panel, then Alt+F10 works as it always does.",
    },
  },
  // ---- THE COST CENTRE SUMMARY (T2) — ONE axis, over a period.
  //
  // These are ScreenKinds and not `report` frames, so NOTHING from the `report`
  // table above applies to them: every report key they need is bound here or it
  // falls through to the global voucher keys.
  "cost-centre-summary": {
    // F2 — the PERIOD, both bounds. A cost summary is a FLOW over a range, not a
    // position as at a date, and FrameOverlays classifies it as such so the F2
    // panel cannot tell this screen its dates do nothing while it reads them.
    F2: { kind: "period" },
    // F4 — CHOOSE THE AXIS. See the `selectCostCategory` action comment for the
    // combo's provenance (ours, placed where RESEARCH.md:879 puts the analogous
    // account picker, never claimed as documented). Binding it is protective
    // twice: the global F4 is a Contra voucher.
    F4: { kind: "selectCostCategory" },
    // Alt+C inside a report is Tally's NEW COLUMN (RESEARCH.md:771), documented
    // for this very report at :811 — "Alt+C adds another cost centre column to
    // compare (e.g. Staff A vs Staff B)". Not built, and bound anyway: the global
    // Alt+C would push a LEDGER-CREATE frame over the report.
    "Alt+C": {
      kind: "unavailable",
      feature: "New Column (Alt+C)",
      note: "Comparison columns are not built. In Tally this adds a second cost centre column side by side (RESEARCH.md:811); here one axis at a time is the whole report. Note that a column comparing two AXES would be the one comparison this report must never offer — two axes allocate the same money, so the two columns would not be comparable and their sum would be double the cost.",
    },
    // F5 — ledger-wise / group-wise. Meaningless here, and bound because the
    // global F5 opens a PAYMENT VOUCHER: not a missing feature but a wrong one,
    // which is what this file's coverage rule exists for.
    F5: {
      kind: "unavailable",
      feature: "Ledger-wise / group-wise (F5)",
      note: "This report has no ledger/group axis to flip: the COST CATEGORY is the grouping, and F4 chooses it. What each ledger cost within a centre is a break-up this build does not serve.",
    },
    // Alt+F1 / Alt+F5 — Detailed. Nothing to expand, and saying so is the point:
    // every row already carries its OWN figures beside its rolled-up TOTAL, which
    // is the one pair this report must never collapse.
    "Alt+F1": {
      kind: "unavailable",
      feature: "Detailed (Alt+F1)",
      note: "There is nothing to expand: every centre already shows its OWN allocations beside the TOTAL rolled up through its children, because neither is derivable from the other — a parent may hold allocations of its own AND have children.",
    },
    "Alt+F5": {
      kind: "unavailable",
      feature: "Detailed (Alt+F5)",
      note: "There is nothing to expand: every centre already shows its OWN allocations beside the TOTAL rolled up through its children, because neither is derivable from the other — a parent may hold allocations of its own AND have children.",
    },
  },
  // ---- THE COST CATEGORY SUMMARY (T2) — one row per axis, and the GAP.
  "cost-category-summary": {
    F2: { kind: "period" },
    // F4 — there is no axis picker HERE, and that is worth a sentence rather than
    // a Contra voucher: an operator who used F4 on the centre summary a moment ago
    // will press it, and the global binding would open a voucher over the report.
    F4: {
      kind: "unavailable",
      feature: "Cost Category (F4) — not on this report",
      note: "This report IS the list of cost categories — one row per axis. Press Enter on a row to open that axis's Cost Centre Summary, where F4 switches axes.",
    },
    "Alt+C": {
      kind: "unavailable",
      feature: "New Column (Alt+C)",
      note: "Comparison columns are not built (RESEARCH.md:771). Note what a column across axes would mean here: each row is a different axis through the SAME money, so nothing on this report may be added across rows — and a comparison column would invite exactly that.",
    },
    F5: {
      kind: "unavailable",
      feature: "Ledger-wise / group-wise (F5)",
      note: "This report has no ledger/group axis to flip: one row per cost category is its whole shape. Press Enter on a row for the centres on that axis.",
    },
    "Alt+F1": {
      kind: "unavailable",
      feature: "Detailed (Alt+F1)",
      note: "There is nothing to expand: every row already shows what this axis was measured AGAINST (its allocable base), what it accounts for, and the unallocated remainder. Enter on a row opens that axis's centres.",
    },
    "Alt+F5": {
      kind: "unavailable",
      feature: "Detailed (Alt+F5)",
      note: "There is nothing to expand: every row already shows what this axis was measured AGAINST (its allocable base), what it accounts for, and the unallocated remainder. Enter on a row opens that axis's centres.",
    },
  },
  // ---- THE TWO ORDER REGISTERS (T3). Each carries a create FORM with an item
  // grid on it, so the voucher keys are held back exactly as they are on the
  // challan form and the cost masters.
  //
  // ⚠️ Ctrl+F8 AND Ctrl+F9 ARE DELIBERATELY *NOT* HELD BACK HERE. They move
  // between the two registers, which is navigation an operator wants and not a
  // voucher pushed over a form — and on the register you are already on, the
  // machine leaves the stack alone rather than stacking a duplicate frame (see
  // `orderRegister` in navMachine.ts). They are the one pair of function keys on
  // these screens that stays live.
  "sales-orders": {
    // Alt+C — RECORD AN ORDER. Tally's create-inline combo (RESEARCH.md:405);
    // the global `createMaster` would push a ledger-create frame over the form.
    "Alt+C": { kind: "createOrder" },
    // Alt+W — PRE-CLOSE the highlighted order. DOCUMENTED at RESEARCH.md:622,
    // "Alt+W pre-closes outstanding orders with reason/date".
    "Alt+W": { kind: "closeOrder" },
    // F2 would read as "Date" from the global table. An order register has no
    // period: each order carries its own date, typed on the form, and what is
    // OUTSTANDING as at a date is the Sales Order Book — a report, with F2 on it.
    F2: {
      kind: "unavailable",
      feature: "Period (F2)",
      note: "The order register has no period — it lists this company's orders, and each one carries the date it was placed. What is still OUTSTANDING as at a date is the Sales Order Book (Alt+G > Sales Order Book), which is a report and has F2.",
    },
    ...voucherKeysHeldBack("the sales order register", "order"),
  },
  "purchase-orders": {
    "Alt+C": { kind: "createOrder" },
    "Alt+W": { kind: "closeOrder" },
    F2: {
      kind: "unavailable",
      feature: "Period (F2)",
      note: "The order register has no period — it lists this company's orders, and each one carries the date it was placed. What is still OUTSTANDING as at a date is the Purchase Order Book (Alt+G > Purchase Order Book), which is a report and has F2.",
    },
    ...voucherKeysHeldBack("the purchase order register", "order"),
  },
  // ---- BILLS OF MATERIALS (T3). A master list with a create form on it.
  boms: {
    // Alt+C — DEFINE A RECIPE. A BOM is a master in Tally's own vocabulary (it is
    // set on the finished good's item master, RESEARCH.md:626), so the
    // create-a-master key is the right one — but scoped, because
    // MasterCreateScreen knows nothing about components.
    "Alt+C": { kind: "createBom" },
    // Alt+X — EXPLODE the highlighted recipe. OURS, not documented Tally: in
    // Tally the explosion only ever happens inside a Manufacturing Journal
    // (RESEARCH.md:628) and there is no key to press on its own. The global Alt+X
    // is `cancelVoucher`, which is meaningless on a master list, so scoping it
    // here takes nothing away.
    "Alt+X": { kind: "explodeBom" },
    F2: {
      kind: "unavailable",
      feature: "Period (F2)",
      note: "A bill of materials has no period — it is a master, a recipe, and it exists until it is altered. It is not dated and it is not a transaction: nothing moves until the recipe is exploded into a Stock Journal, which is Esc then Alt+F7. Alt+X previews what those lines would be.",
    },
    ...voucherKeysHeldBack("the bill of materials screen", "recipe"),
  },
  // ---- THE e-WAY BILL (T5). It carries a FORM (the consignment), so every
  // binding below is the same argument the cost masters and the two order
  // registers make: AN UNBOUND KEY HERE IS NOT INERT. Without these entries the
  // global voucher keys fire — F5 a Payment, F9 a Purchase, F8 a Sales — over a
  // half-stated consignment, and the frame holding it goes with them.
  "e-way-bill": {
    // F2 would read as "Date" from the global table. A consignment has no
    // period: the bill is raised against ONE document on ONE day, and its
    // VALIDITY is COMPUTED by the server from the distance rather than chosen
    // by the operator. ALWAYS_MEANINGFUL contains `date`, so an unbound F2 here
    // would produce a status line for a key that changed nothing.
    //
    // ⛔ THE NOTE NAMES NO FIGURE. The km-per-day rates live in rule138.go with
    // their provenance, and a keymap note quoting them would be a second home
    // for a number that is explicitly pending re-verification.
    F2: {
      kind: "unavailable",
      feature: "Period (F2)",
      note: "An e-way bill has no period. It is raised against ONE voucher, and how long it stays valid is COMPUTED by the server from the distance you state and the cargo class you assert — it is not a date you choose. The rule applied, and the caveat on it, travel with the bill in its disclosures.",
    },
    // ⚠️ Alt+Z IS DELIBERATELY LEFT LIVE HERE rather than held back, exactly as
    // the two order registers leave Ctrl+F8 / Ctrl+F9 live on each other:
    // pressing the key for the screen you are already on is answered by
    // pushOnce plus ALWAYS_MEANINGFUL, not by a refusal.
    ...voucherKeysHeldBack("the e-way bill screen", "consignment"),
  },
  // ---- THE e-INVOICE (§1.4 B). It carries a FORM — a selected invoice and an
  // irreversible Ctrl+A — so the same argument applies: AN UNBOUND KEY HERE IS
  // NOT INERT. Without these entries the global voucher keys fire (F5 a Payment,
  // F9 a Purchase, F8 a Sales) over the screen, and the frame goes with them.
  //
  // ⛔ AND THE STAKES ARE HIGHER HERE THAN ON ANY OTHER FORM-CARRYING SCREEN.
  // Everywhere else a stray voucher key costs the operator their half-keyed
  // work. Here the screen it destroys is the one showing an ACKNOWLEDGEMENT
  // NUMBER THAT IS STORED NOWHERE AND CAN NEVER BE READ AGAIN.
  "e-invoice": {
    // F2 would read as "Date" from the global table. An e-invoice has no period:
    // it reports ONE document, and the date on it is the invoice's own. It is
    // not a range and not a choice.
    F2: {
      kind: "unavailable",
      feature: "Period (F2)",
      note: "An e-invoice has no period. It reports ONE invoice, on the date that invoice already carries — there is no range to set. Pick the invoice from the list; the list itself is the Day Book's period.",
    },
    // ⚠️ Alt+I IS DELIBERATELY LEFT LIVE HERE rather than held back, exactly as
    // Alt+Z is on the e-way bill screen: pressing the key for the screen you are
    // already on is answered by pushOnce plus ALWAYS_MEANINGFUL, not a refusal.
    ...voucherKeysHeldBack("the e-invoice screen", "invoice"),
  },
  // Alt+L on a voucher opens its EDIT LOG. RESEARCH sec 6 pins the PLACEMENT
  // (Tally shows the Edit Log from the voucher alteration screen and the Day
  // Book) but not the combo, so this is scoped to the voucher screen rather
  // than claimed as a documented global binding — see the KeymapAction comment.
  voucher: {
    "Alt+L": { kind: "editLog" },
  },
};

function buildBindings(
  raw: Record<string, KeymapAction>,
): Record<KeyCombo, KeymapAction> {
  const out: Record<KeyCombo, KeymapAction> = {};
  for (const [combo, action] of Object.entries(raw)) {
    out[normalizeCombo(combo)] = action;
  }
  return out;
}

function buildScreenBindings(
  raw: Partial<Record<ScreenKind, Record<string, KeymapAction>>>,
): Partial<Record<ScreenKind, Record<KeyCombo, KeymapAction>>> {
  const out: Partial<Record<ScreenKind, Record<KeyCombo, KeymapAction>>> = {};
  for (const [screen, table] of Object.entries(raw)) {
    if (table) out[screen as ScreenKind] = buildBindings(table);
  }
  return out;
}

/** The default, shipped-as-default TallyPrime keymap. */
export const tallyPrimeKeymap: Keymap = {
  profile: "tallyprime",
  bindings: buildBindings(RAW_BINDINGS),
  screenBindings: buildScreenBindings(RAW_SCREEN_BINDINGS),
};

/** Registry of available keymap profiles, keyed by `keymapProfile` name. */
export const keymapRegistry: Readonly<Record<string, Keymap>> = {
  tallyprime: tallyPrimeKeymap,
};

/**
 * Resolve a keymap profile by name, falling back to TallyPrime. Never throws,
 * so a malformed corpus `keymapProfile` degrades gracefully.
 */
export function resolveKeymap(profile: string | undefined): Keymap {
  if (profile && profile in keymapRegistry) {
    return keymapRegistry[profile] as Keymap;
  }
  return tallyPrimeKeymap;
}

/**
 * Resolve a combo to its action FOR A SCREEN: the screen's override table wins,
 * then the global table. This is the ONE place context-scoped keys are decided —
 * the machine, the shell (which needs the same answer to fire side effects) and
 * the F1 Help screen all call it, so they can never disagree.
 */
export function resolveAction(
  keymap: Keymap,
  screen: ScreenKind | undefined,
  combo: string,
): KeymapAction | undefined {
  const key = normalizeCombo(combo);
  if (screen) {
    const override = keymap.screenBindings?.[screen]?.[key];
    if (override) return override;
  }
  return keymap.bindings[key];
}

/**
 * Every combo the profile binds for a screen, as ordered rows. Feeds the F1
 * Help screen, so the reference a user reads is generated from the table the
 * engine actually dispatches (it cannot drift).
 */
export function keyCatalogFor(
  keymap: Keymap,
  screen: ScreenKind | undefined,
): { combo: KeyCombo; action: KeymapAction }[] {
  const merged = new Map<KeyCombo, KeymapAction>();
  for (const [combo, action] of Object.entries(keymap.bindings)) {
    merged.set(combo, action);
  }
  if (screen) {
    for (const [combo, action] of Object.entries(
      keymap.screenBindings?.[screen] ?? {},
    )) {
      merged.set(combo, action);
    }
  }
  return [...merged.entries()].map(([combo, action]) => ({ combo, action }));
}

/** A short human label for an action, used by Help and the status line. */
export function actionLabel(action: KeymapAction): string {
  switch (action.kind) {
    case "voucher":
      return `${action.voucherType[0]?.toUpperCase()}${action.voucherType.slice(1)} voucher`;
    case "enter":
      return "Next field / drill down";
    case "escape":
      return "Back / cancel";
    case "accept":
      return "Accept (save) the screen";
    case "quit":
      return "Quit the screen without saving";
    case "delete":
      return "Delete the loaded voucher";
    case "cancelVoucher":
      return "Cancel the voucher being entered";
    case "duplicate":
      return "Duplicate the loaded voucher";
    case "nextField":
      return "Next field";
    case "prevField":
      return "Previous field";
    case "prevVoucher":
      return "Previous saved voucher";
    case "nextVoucher":
      return "Next saved voucher";
    case "eWayBill":
      // ⛔ "e-Way Bill" AND NOT "e-Way Bill Number". The label appears on the
      // button bar and in the keyboard help, and a label promising a NUMBER is
      // how the local reference gets printed as a government one.
      return "e-Way Bill (Rule 138)";
    case "eInvoice":
      // ⛔ "e-Invoice" AND NOT "Generate IRN". Same argument as the label above,
      // one degree sharper: this deployment cannot reach the IRP at all, and a
      // label promising an IRN is how a locally-minted reference gets read as a
      // government one — from the button bar, where there is no room for the
      // disclosure the screen carries.
      return "e-Invoice (report to IRP)";
    case "goto":
      return "Go To (keeps this screen)";
    case "switchTo":
      return "Switch To (replaces this screen)";
    case "changeMode":
      return "Change Mode";
    case "changeView":
      return "Change View";
    case "date":
      return "Date";
    case "period":
      return "Period (From / To)";
    case "detailed":
      return "Detailed (expand all levels)";
    case "ledgerWise":
      return "Ledger-wise / group-wise";
    case "filter":
      return "Filter rows";
    case "exportScreen":
      // "the rows on screen" and NOT "this report": Alt+E writes what is
      // displayed AFTER Ctrl+F and F12 have been applied, which is the opposite
      // of what Ctrl+P does (it asks the server for the whole statement). Both
      // formats are named because F12 chooses between them, and neither is
      // e-mail or PDF — see EXPORT_NOT_BUILT_NOTE.
      return "Export the rows on screen (CSV or Excel XML)";
    case "calculator":
      return "Calculator";
    case "help":
      return "Help — keystroke reference";
    case "features":
      return "Company Features";
    case "configure":
      return "Configure this screen";
    case "switchCompany":
      return "Switch Company";
    case "editLog":
      return "Edit Log (every version of this voucher)";
    case "print":
      // Two combos reach this action and they must not read identically — Alt+P
      // is Tally's print OPTIONS menu, which this build does not have.
      return action.viaOptionsMenu
        ? "Print (with defaults — no options menu in this build)"
        : "Print this voucher / report";
    case "bankReconciliation":
      return "Bank Reconciliation (tick items off the bank statement)";
    case "importBankStatement":
      return "Import a bank statement (CSV)";
    case "recordTDSChallan":
      return "Record a TDS deposit (challan)";
    case "createCostMaster":
      // Both, named, because one key creates either and a label saying only
      // "cost centre" would hide the axis — the master an operator has to create
      // FIRST, since a centre cannot exist without one.
      return "Create a cost category (axis) or a cost centre";
    case "selectCostCategory":
      // "Axis" is spelled out: this key is what stops one axis's figures being
      // read under another's heading, and every figure on that report is scoped
      // to the one it chooses.
      return "Choose the cost category (the axis) this report is for";
    case "orderRegister":
      // "Orders" and not "Order voucher": an order is a COMMITMENT, not a
      // movement, and these keys open a REGISTER rather than a voucher form. An
      // operator told "Sales Order voucher" would be looking for a voucher.
      return action.direction === "sales"
        ? "Sales orders — what customers have committed to buy"
        : "Purchase orders — what we have committed to buy";
    case "createOrder":
      return "Record an order";
    case "closeOrder":
      // "Pre-close" is Tally's own word (RESEARCH.md:622) and "abandon" is what it
      // MEANS. Both are in the label because a key that read "close the order"
      // would be pressed by an operator who thinks they are marking it delivered —
      // which is the one thing it does not do.
      return "Pre-close (abandon) this order, with a reason — NOT 'mark it delivered'";
    case "createBom":
      return "Define a bill of materials (a recipe)";
    case "explodeBom":
      // "Preview" is load-bearing: exploding moves nothing, and a label that read
      // "make this" would suggest it had.
      return "Explode this recipe for a quantity (a preview — nothing moves)";
    case "createMaster":
      return `Create ${action.masterKind} on the fly`;
    case "unavailable":
      return action.feature;
    default:
      return "";
  }
}
