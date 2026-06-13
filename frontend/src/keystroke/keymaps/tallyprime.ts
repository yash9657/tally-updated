import type { Keymap, KeymapAction, KeyCombo } from "../types";
import { normalizeCombo } from "../normalize";

/**
 * The TallyPrime keymap profile (RESEARCH.md sec 22, table 1).
 *
 * This is DATA: a combo → abstract-action table. An ERP 9 profile can be added
 * later by authoring a second table (e.g. Credit Note Ctrl+F8 vs Alt+F6) and
 * swapping it in — the navigation machine never changes. Keys are authored in
 * any human-friendly form and normalized at module load, so lookups are O(1)
 * against the same canonical strings the corpora produce.
 */
const RAW_BINDINGS: Record<string, KeymapAction> = {
  // Core voucher F-keys (F4 Contra .. F9 Purchase).
  F4: { kind: "voucher", voucherType: "contra" },
  F5: { kind: "voucher", voucherType: "payment" },
  F6: { kind: "voucher", voucherType: "receipt" },
  F7: { kind: "voucher", voucherType: "journal" },
  F8: { kind: "voucher", voucherType: "sales" },
  F9: { kind: "voucher", voucherType: "purchase" },

  // Navigation grammar.
  Enter: { kind: "enter" },
  Esc: { kind: "escape" },
  "Ctrl+A": { kind: "accept" }, // Accept / Save screen
  "Alt+G": { kind: "goto" }, // Go To (overlay, keeps prior screen)
  "Ctrl+G": { kind: "switchTo" }, // Switch To (replaces the stack)
  "Ctrl+H": { kind: "changeMode" }, // Change Mode picker (in vouchers)
  F2: { kind: "date" }, // Date (voucher) / Period (report)
  // Alt+C — create a master inline (e.g. a new ledger from a voucher field).
  // The default master is a ledger, the by-far most common inline create; the
  // navigation machine returns to the originating frame on accept/Esc.
  "Alt+C": { kind: "createMaster", masterKind: "ledger" },
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

/** The default, shipped-as-default TallyPrime keymap. */
export const tallyPrimeKeymap: Keymap = {
  profile: "tallyprime",
  bindings: buildBindings(RAW_BINDINGS),
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
