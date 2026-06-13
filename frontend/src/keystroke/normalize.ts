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
 * Normalize a DOM-like keyboard event into the canonical `KeyCombo`. Shift is
 * only treated as a modifier for non-printable / function keys, because for
 * printable characters the shifted symbol is already in `event.key`.
 */
export function comboFromEvent(ev: KeyEventLike): KeyCombo {
  const base = canonicalKeyName(ev.key);
  const ctrl = Boolean(ev.ctrlKey || ev.metaKey);
  const alt = Boolean(ev.altKey);
  const isFnOrNamed = /^F\d{1,2}$/.test(base) || base.length > 1;
  const shift = Boolean(ev.shiftKey) && isFnOrNamed;
  return assemble(ctrl, alt, shift, base);
}
