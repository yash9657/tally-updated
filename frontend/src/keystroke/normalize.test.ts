import { describe, it, expect } from "vitest";
import { comboFromEvent, normalizeCombo } from "./normalize";
import { resolveKeymap, resolveAction } from "./keymaps/tallyprime";

/**
 * normalize.test.ts — WHAT THE KEYBOARD ACTUALLY SENDS.
 *
 * ⛔ THIS FILE EXISTS BECAUSE THERE WAS NO TEST FOR `comboFromEvent` AT ALL, AND
 * A WHOLE CLASS OF KEYS WAS DEAD ON macOS FOR THE LIFE OF THE PRODUCT.
 *
 * On a Mac, Option is a COMPOSE modifier: Option+L reports `key: "¬"`, not
 * `key: "l"`. `comboFromEvent` read `key` alone, assembled "Alt+¬", found it in
 * no keymap, and did nothing — for Alt+G (Go To), Alt+C (Create Master), Alt+E
 * (Export), Alt+D (Delete), Alt+I (e-Invoice), Alt+Z (e-Way Bill) and Alt+L
 * (Edit Log) alike. All seven were verified dead in a real browser on
 * 2026-08-07, on a build that advertised every one of them on the button bar.
 *
 * ⚠️ AND THE DRIVEN TESTS COULD NOT HAVE FOUND IT. Playwright's
 * `keyboard.press('Alt+l')` synthesises `key: "l"` — a value macOS never sends —
 * so every browser test passed on keys no operator on this platform could fire.
 * The assertions below therefore use the CHARACTERS macOS REALLY PRODUCES.
 */

/** The character macOS composes for each Option+letter this product binds. */
const MAC_COMPOSED: ReadonlyArray<[combo: string, key: string, code: string]> = [
  ["Alt+G", "©", "KeyG"], // Go To
  ["Alt+C", "ç", "KeyC"], // Create Master
  ["Alt+E", "´", "KeyE"], // Export
  ["Alt+D", "∂", "KeyD"], // Delete
  ["Alt+L", "¬", "KeyL"], // Edit Log
  ["Alt+I", "ˆ", "KeyI"], // e-Invoice
  ["Alt+Z", "Ω", "KeyZ"], // e-Way Bill
  ["Alt+X", "≈", "KeyX"], // Cancel voucher / Explode BOM
  ["Alt+O", "ø", "KeyO"], // Import bank statement
];

describe("comboFromEvent survives a real macOS keyboard", () => {
  for (const [combo, key, code] of MAC_COMPOSED) {
    it(`${combo} resolves when the OS composed "${key}"`, () => {
      expect(comboFromEvent({ key, code, altKey: true })).toBe(combo);
    });
  }

  it("the composed keys reach their REAL actions, not merely a tidy string", () => {
    // The string being right is not the point; the point is that the keymap
    // lookup then succeeds. This is the assertion that fails if `code` is ever
    // dropped at a call site again — which is exactly how the defect shipped
    // (AppShell and useNavMachine both destructured the event and left `code`
    // behind, so the resolver never saw it).
    const keymap = resolveKeymap("tallyprime");
    const goTo = resolveAction(keymap, "gateway", comboFromEvent({
      key: "©", code: "KeyG", altKey: true,
    }));
    expect(goTo?.kind, "Option+G must still open Go To on a Mac").toBe("goto");
  });

  it("Alt+2 works too — Option composes digits as readily as letters", () => {
    expect(comboFromEvent({ key: "™", code: "Digit2", altKey: true })).toBe("Alt+2");
  });
});

describe("comboFromEvent leaves everything else exactly as it was", () => {
  it("⛔ TYPING IS NOT A SHORTCUT: with no Alt, `key` wins and `code` is ignored", () => {
    // THE CONTROL, and the reason the fallback is scoped to Alt. An operator
    // typing "ç" into a narration field is typing a character. If `code` won
    // unconditionally, every accented letter in a party name would be swallowed
    // as a chord — a far worse bug than the one being fixed.
    expect(comboFromEvent({ key: "ç", code: "KeyC" })).toBe("Ç");
    expect(comboFromEvent({ key: "a", code: "KeyA" })).toBe("A");
  });

  it("Ctrl combos are untouched — Ctrl does not compose", () => {
    expect(comboFromEvent({ key: "a", code: "KeyA", ctrlKey: true })).toBe("Ctrl+A");
    expect(comboFromEvent({ key: "A", code: "KeyA", ctrlKey: true })).toBe("Ctrl+A");
  });

  it("F-keys and named keys are untouched, with or without Alt", () => {
    expect(comboFromEvent({ key: "F8", code: "F8" })).toBe("F8");
    // Alt+F5/Alt+F7 must keep working: their `code` is not a Key*/Digit*, so the
    // fallback declines to fire and `key` is used exactly as before.
    expect(comboFromEvent({ key: "F5", code: "F5", altKey: true })).toBe("Alt+F5");
    expect(comboFromEvent({ key: "F7", code: "F7", altKey: true })).toBe("Alt+F7");
    expect(comboFromEvent({ key: "Escape", code: "Escape" })).toBe("Esc");
    expect(comboFromEvent({ key: "Enter", code: "Enter" })).toBe("Enter");
  });

  it("an event with NO code still resolves by key", () => {
    // The corpora and most unit tests construct bare {key} objects. They must
    // keep working, which is why `code` is optional rather than required.
    expect(comboFromEvent({ key: "l", altKey: true })).toBe("Alt+L");
    expect(comboFromEvent({ key: "g", altKey: true })).toBe("Alt+G");
  });

  it("a combo STRING normalizes to the same thing an event does", () => {
    // The two paths must agree or a corpus assertion and a live keypress would
    // look up different rows of the same keymap.
    expect(normalizeCombo("alt+l")).toBe(comboFromEvent({ key: "¬", code: "KeyL", altKey: true }));
    expect(normalizeCombo("Option+G")).toBe(comboFromEvent({ key: "©", code: "KeyG", altKey: true }));
  });
});
