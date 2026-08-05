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
  F10: {
    kind: "unavailable",
    feature: "Other Vouchers (F10)",
    note: "Order vouchers are not built — an order is a COMMITMENT, not a movement. The four stock vouchers ARE, screen and all: Alt+F7 Stock Journal, Alt+F8 Delivery Note, Alt+F9 Receipt Note, Alt+F10 Physical Stock. F4–F9 cover the six accounting vouchers and Alt+F5/Alt+F6 the debit/credit notes.",
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
  "Ctrl+F8": {
    kind: "unavailable",
    feature: "Sales Order (Ctrl+F8)",
    note: "Order processing is not built yet.",
  },
  "Ctrl+F9": {
    kind: "unavailable",
    feature: "Purchase Order (Ctrl+F9)",
    note: "Order processing is not built yet.",
  },
};

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
      return "Export this screen (CSV)";
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
    case "createMaster":
      return `Create ${action.masterKind} on the fly`;
    case "unavailable":
      return action.feature;
    default:
      return "";
  }
}
