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
  ["Alt+R", "®", "KeyR"], // Reconciliation queue (track D)
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

/**
 * ⛔ ⌘A MUST NOT POST THE VOUCHER.
 *
 * `comboFromEvent` used to read `Boolean(ev.ctrlKey || ev.metaKey)`, so on a Mac
 * ⌘A — Select All, pressed to select a field's contents before retyping them —
 * resolved to "Ctrl+A" = `{ kind: "accept" }` and POSTED THE DOCUMENT MID-EDIT.
 * Confirmed in a browser on 2026-08-07 while editing row 3 of a voucher.
 *
 * No test pinned the fold, and nothing in src, docs or RESEARCH.md ever claimed
 * ⌘ was an intentional macOS spelling of Ctrl — the hint strips advertise
 * "Ctrl+A" and Tally's documented keys are Ctrl-based. So the fold is removed
 * rather than narrowed, and Meta becomes a modifier token that nothing binds.
 */
describe("Meta (⌘) is not a second spelling of Ctrl", () => {
  it("⌘A does NOT accept the voucher — it must not produce Ctrl+A", () => {
    const combo = comboFromEvent({ key: "a", code: "KeyA", metaKey: true });
    expect(combo, "⌘A must never assemble a Ctrl combo").not.toBe("Ctrl+A");
    expect(combo).toBe("Meta+A");
  });

  it("…and no action resolves for it, so the browser still Selects All", () => {
    // The string being right is not the point. The point is that the keymap
    // lookup FAILS, because that is what makes AppShell/useNavMachine decline
    // the event and let the OS shortcut happen. `voucher` is the screen where
    // this cost real data.
    const keymap = resolveKeymap("tallyprime");
    const meta = resolveAction(keymap, "voucher", comboFromEvent({
      key: "a", code: "KeyA", metaKey: true,
    }));
    expect(meta, "⌘A must resolve to NO action").toBeUndefined();

    // The control: the real accept key still accepts on the same screen.
    const ctrl = resolveAction(keymap, "voucher", comboFromEvent({
      key: "a", code: "KeyA", ctrlKey: true,
    }));
    expect(ctrl?.kind, "Ctrl+A must still Accept").toBe("accept");
  });

  it("every ⌘+letter the OS owns stays out of the Ctrl namespace", () => {
    // The fold did not only hit ⌘A. Each of these letters is bound to a real
    // Ctrl action in tallyprime.ts, so each was reachable by an OS shortcut:
    // Q quit-without-saving, P print, F filter, G Switch To, H Change Mode,
    // N calculator, plus B and J.
    for (const letter of ["A", "B", "F", "G", "H", "J", "N", "P", "Q"]) {
      const combo = comboFromEvent({
        key: letter.toLowerCase(), code: `Key${letter}`, metaKey: true,
      });
      expect(combo, `⌘${letter} must not be a Ctrl combo`).toBe(`Meta+${letter}`);
      expect(
        resolveAction(resolveKeymap("tallyprime"), "voucher", combo),
        `⌘${letter} must resolve to no action`,
      ).toBeUndefined();
    }
  });

  it("⌘ does not fire a BARE letter binding either", () => {
    // Why a distinct "Meta+" token instead of just ignoring `metaKey`: ignoring
    // it makes ⌘A assemble a bare "A", which is unbound today but would fire
    // the instant any screen binds a plain letter. "Meta+A" cannot collide.
    expect(comboFromEvent({ key: "a", code: "KeyA", metaKey: true })).not.toBe("A");
  });

  it("ctrlKey alone still produces Ctrl+…, on a Mac like anywhere else", () => {
    expect(comboFromEvent({ key: "a", code: "KeyA", ctrlKey: true })).toBe("Ctrl+A");
    expect(comboFromEvent({ key: "q", code: "KeyQ", ctrlKey: true })).toBe("Ctrl+Q");
    expect(comboFromEvent({ key: "F4", code: "F4", ctrlKey: true })).toBe("Ctrl+F4");
  });

  it("the Alt/Option `code` fallback is untouched when Meta is absent", () => {
    // Guard against this change quietly breaking the previous one: Option still
    // composes, and `code` still wins for Alt.
    expect(comboFromEvent({ key: "¬", code: "KeyL", altKey: true })).toBe("Alt+L");
    expect(comboFromEvent({ key: "©", code: "KeyG", altKey: true })).toBe("Alt+G");
    expect(comboFromEvent({ key: "™", code: "Digit2", altKey: true })).toBe("Alt+2");
    expect(comboFromEvent({ key: "F5", code: "F5", altKey: true })).toBe("Alt+F5");
    // And with no modifier at all, `key` still wins — typing is not a shortcut.
    expect(comboFromEvent({ key: "ç", code: "KeyC" })).toBe("Ç");
  });

  it("a ⌘ combo STRING and a ⌘ event still normalize alike", () => {
    // Same invariant the Alt case has: the two paths must agree, or a corpus row
    // and a live keypress would look up different rows of the same keymap.
    expect(normalizeCombo("cmd+a")).toBe(comboFromEvent({ key: "a", code: "KeyA", metaKey: true }));
    expect(normalizeCombo("Meta+A")).toBe("Meta+A");
    expect(normalizeCombo("Command+Shift+Esc")).toBe("Shift+Meta+Esc");
  });
});
