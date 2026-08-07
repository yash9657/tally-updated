import type { KeyCombo, KeyEventLike } from "./types";

/**
 * Canonical modifier order used when building a `KeyCombo` string: Ctrl, Alt,
 * Shift, then the base key. This guarantees a DOM `KeyboardEvent` and a corpus
 * `"combo"` string normalize to the SAME lookup key.
 */
const MODIFIER_ORDER = ["Ctrl", "Alt", "Shift"] as const;

/** Map common DOM `KeyboardEvent.key` values to Tally's combo vocabulary. */
function canonicalKeyName(key: string): string {
  const k = key.trim();
  switch (k) {
    case "Escape":
    case "Esc":
      return "Esc";
    case "Enter":
    case "Return":
      return "Enter";
    case " ":
    case "Space":
    case "Spacebar":
      return "Space";
    default:
      break;
  }
  // F-keys: keep as-is (already "F1".."F12").
  if (/^F\d{1,2}$/i.test(k)) return k.toUpperCase();
  // Single printable letters are upper-cased so "Ctrl+a" === "Ctrl+A".
  if (k.length === 1) return k.toUpperCase();
  return k;
}

/** Split a base key off a combo string, normalizing the modifier tokens. */
function parseComboString(combo: string): {
  ctrl: boolean;
  alt: boolean;
  shift: boolean;
  base: string;
} {
  const parts = combo
    .split("+")
    .map((p) => p.trim())
    .filter((p) => p.length > 0);
  let ctrl = false;
  let alt = false;
  let shift = false;
  let base = "";
  for (const part of parts) {
    const low = part.toLowerCase();
    if (low === "ctrl" || low === "control") ctrl = true;
    else if (low === "alt" || low === "option" || low === "opt") alt = true;
    else if (low === "shift") shift = true;
    else base = part;
  }
  return { ctrl, alt, shift, base };
}

function assemble(
  ctrl: boolean,
  alt: boolean,
  shift: boolean,
  base: string,
): KeyCombo {
  const mods: string[] = [];
  if (ctrl) mods.push("Ctrl");
  if (alt) mods.push("Alt");
  if (shift) mods.push("Shift");
  // Keep canonical order regardless of input ordering.
  mods.sort((a, b) => MODIFIER_ORDER.indexOf(a as never) - MODIFIER_ORDER.indexOf(b as never));
  return [...mods, base].join("+");
}

/**
 * Normalize a combo string (e.g. from a corpus file: "ctrl+a", "Alt+G", "F8")
 * into the canonical form ("Ctrl+A", "Alt+G", "F8").
 */
export function normalizeCombo(combo: string): KeyCombo {
  const { ctrl, alt, shift, base } = parseComboString(combo);
  return assemble(ctrl, alt, shift, canonicalKeyName(base));
}

/**
 * The base key of an event, preferring the PHYSICAL key when Alt is held.
 *
 * ===========================================================================
 * ⛔ WHY THIS EXISTS: EVERY Alt+<letter> IN THE PRODUCT WAS DEAD ON macOS
 * ===========================================================================
 * On a Mac, Option is a COMPOSE modifier. Option+L does not report `key: "l"`;
 * the OS composes a character and reports `key: "¬"`. Option+G reports "©",
 * Option+C "ç", Option+E "´", Option+D "∂", Option+Z "Ω", Option+I "ˆ".
 * Reading `key` alone, every one of those assembled a combo — "Alt+¬", "Alt+©"
 * — that appears in no keymap, so the key did nothing at all.
 *
 * Verified in a browser on 2026-08-07: Alt+G (Go To), Alt+C (Create Master),
 * Alt+E (Export), Alt+D (Delete), Alt+Z (e-Way Bill), Alt+I (e-Invoice) and
 * Alt+L (Edit Log) ALL failed to fire, on a build whose button bar and hint
 * strip advertised every one of them.
 *
 * ⚠️ AND NO TEST COULD HAVE CAUGHT IT. Playwright's `keyboard.press('Alt+l')`
 * synthesises `key: "l"` — the value a Mac never produces — so the entire
 * browser-evidence regime passed on keys a human on this platform cannot press.
 * That is why the fallback is keyed on `code` and not on a character blacklist:
 * the composed set is layout-specific (a German or French layout composes a
 * different one), and `code` is the only layout-independent identity there is.
 *
 * SCOPED TO Alt DELIBERATELY. Without a modifier, `key` is exactly right and
 * must win: an operator typing "ç" into a narration is typing a character, not
 * pressing a shortcut. Ctrl does not compose, so it needs no help. F-keys and
 * named keys are untouched — their `code` is neither a `Key` nor a `Digit` form
 * — so Alt+F5, Alt+F7 and the rest resolve exactly as before.
 */
function baseKeyOf(ev: KeyEventLike): string {
  if (ev.altKey && ev.code) {
    const letter = /^Key([A-Z])$/.exec(ev.code);
    if (letter) return letter[1]!;
    // Alt+2 duplicates a voucher, and Option+2 composes "™" on a Mac just as
    // readily as Option+L composes "¬".
    const digit = /^Digit([0-9])$/.exec(ev.code);
    if (digit) return digit[1]!;
  }
  return canonicalKeyName(ev.key);
}

/**
 * Normalize a DOM-like keyboard event into the canonical `KeyCombo`. Shift is
 * only treated as a modifier for non-printable / function keys, because for
 * printable characters the shifted symbol is already in `event.key`.
 */
export function comboFromEvent(ev: KeyEventLike): KeyCombo {
  const base = baseKeyOf(ev);
  const ctrl = Boolean(ev.ctrlKey || ev.metaKey);
  const alt = Boolean(ev.altKey);
  const isFnOrNamed = /^F\d{1,2}$/.test(base) || base.length > 1;
  const shift = Boolean(ev.shiftKey) && isFnOrNamed;
  return assemble(ctrl, alt, shift, base);
}
