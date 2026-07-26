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
    note: "Order, delivery and stock vouchers are not built yet — F4–F9 cover the six accounting vouchers, and Alt+F5/Alt+F6 the debit/credit notes.",
  },
  // Debit / Credit Note — REAL voucher screens with their own numbering series
  // and their own GSTR-1 treatment (cdnr, table 9B). They were bound as
  // "unavailable" until the classes existed; they are the correct way to
  // reverse part of an invoice, so they must be reachable by their real keys.
  "Alt+F5": { kind: "voucher", voucherType: "debit-note" },
  "Alt+F6": { kind: "voucher", voucherType: "credit-note" },
  "Alt+F7": {
    kind: "unavailable",
    feature: "Stock Journal (Alt+F7)",
    note: "Inventory vouchers are not built yet (no godown/stock movement plane).",
  },
  "Alt+F8": {
    kind: "unavailable",
    feature: "Delivery Note (Alt+F8)",
    note: "Order processing / tracking documents are not built yet.",
  },
  "Alt+F9": {
    kind: "unavailable",
    feature: "Receipt Note (Alt+F9)",
    note: "Order processing / tracking documents are not built yet.",
  },
  "Ctrl+F4": {
    kind: "unavailable",
    feature: "Payroll voucher (Ctrl+F4)",
    note: "The payroll module is not built yet.",
  },
  "Ctrl+F7": {
    kind: "unavailable",
    feature: "Physical Stock (Ctrl+F7)",
    note: "Inventory vouchers are not built yet.",
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
    case "createMaster":
      return `Create ${action.masterKind} on the fly`;
    case "unavailable":
      return action.feature;
    default:
      return "";
  }
}
