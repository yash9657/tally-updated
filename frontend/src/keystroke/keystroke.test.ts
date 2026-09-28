import { describe, it, expect } from "vitest";
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { createActor } from "xstate";
import { navMachine, projectSnapshot } from "./navMachine";
import { buttonBarLabels } from "./buttonBar";
import { entryModesFor } from "../screens/vouchers/voucherConfig";
import { runCase } from "./runner";
import type { CorpusCase } from "./runner";
import type { NavSnapshot } from "./navMachine";
import type { VoucherType } from "./types";

const here = dirname(fileURLToPath(import.meta.url));
const CORPUS_DIR = join(here, "..", "..", "..", "corpora", "keystroke");

function loadCases(): CorpusCase[] {
  const files = readdirSync(CORPUS_DIR).filter((f) => f.endsWith(".json"));
  return files.map((f) => {
    const raw = readFileSync(join(CORPUS_DIR, f), "utf8");
    return JSON.parse(raw) as CorpusCase;
  });
}

/**
 * Map a snapshot's overlay enum to its on-screen option labels for assertions.
 *
 * Change Mode is CLASS-AWARE: Ctrl+H offers Single/Double entry on Payment,
 * Receipt and Contra and the invoice-mode family on Sales/Purchase. This used to
 * return the same three invoice labels for every voucher, which is exactly what
 * the screen used to render — so the corpus agreed with a screen that was wrong.
 * Deriving it from the same `entryModesFor` the screen renders means the corpus
 * cannot drift from the product.
 */
function overlayOptions(snap: NavSnapshot): string[] | undefined {
  if (snap.overlay === "change-mode") {
    return entryModesFor(snap.voucherType ?? "sales").map((m) => m.label);
  }
  return undefined;
}

/** Assert a single corpus `expect` block against the resulting snapshot. */
function assertExpect(snap: NavSnapshot, exp: Record<string, unknown>): void {
  for (const [key, want] of Object.entries(exp)) {
    switch (key) {
      case "screen":
        expect(snap.screen).toBe(want);
        break;
      case "voucherType":
        expect(snap.voucherType).toBe(want);
        break;
      case "field":
        expect(snap.field).toBe(want);
        break;
      case "mode":
        expect(snap.mode).toBe(want);
        break;
      case "reportName":
        expect(snap.reportName).toBe(want);
        break;
      case "saved":
        expect(snap.saved ?? false).toBe(want);
        break;
      case "overlay":
        // `null` in a case means "no overlay is open". The snapshot represents
        // that as undefined, and a case that had to write `undefined` in JSON
        // could not (JSON has no such literal) — so null is the corpus spelling
        // and both are accepted. Asserting the CLOSED state matters: Tally's
        // Change Mode applies and dismisses in one keystroke, and a case that
        // could only assert "an overlay is open" could not pin that.
        if (want === null) {
          expect(snap.overlay ?? null).toBeNull();
        } else {
          expect(snap.overlay).toBe(want);
        }
        break;
      case "parentScreen":
        expect(snap.parentScreen).toBe(want);
        break;
      case "parentVoucherType":
        expect(snap.parentVoucherType).toBe(want);
        break;
      case "parentReportName":
        expect(snap.parentReportName).toBe(want);
        break;
      case "screenStackDepth":
        expect(snap.screenStackDepth).toBe(want);
        break;
      case "detailOf":
        // W2: the approval a proposal-review voucher frame is BOUND to. The
        // prefill is live only while the frame names it, which is what stops a
        // later plain F5 reopening the same proposal (navMachine's proposalFrame).
        expect(snap.detailOf).toBe(want);
        break;
      case "canReturnToVoucher":
        expect(snap.canReturnToVoucher).toBe(want);
        break;
      case "config":
        // The frame's view configuration (F12 / Ctrl+H / Alt+F1 / F5 / F2).
        // Asserted as a SUBSET so a case pins only the keys it cares about.
        for (const [k, v] of Object.entries(want as Record<string, unknown>)) {
          expect(
            (snap.config as Record<string, unknown> | undefined)?.[k],
          ).toEqual(v);
        }
        break;
      case "overlayOptions":
        expect(overlayOptions(snap)).toEqual(want);
        break;
      case "buttonBarContains": {
        const labels = buttonBarLabels(snap.screen, snap.voucherType);
        for (const item of want as string[]) {
          expect(labels).toContain(item);
        }
        break;
      }
      default:
        throw new Error(`Unhandled expect key in corpus: "${key}"`);
    }
  }
}

describe("keystroke conformance (golden corpora)", () => {
  const cases = loadCases();

  it("loads at least the known corpus cases", () => {
    expect(cases.length).toBeGreaterThanOrEqual(15);
  });

  for (const c of cases) {
    it(`${c.id}: ${c.description ?? ""}`, () => {
      const snap = runCase(c);
      assertExpect(snap, c.expect);
    });
  }

  /**
   * THE RUNNER'S OWN SAFETY CATCH, tested.
   *
   * `assertExpect` is a switch over the keys a corpus case may assert, and its
   * default arm THROWS. That arm is the only thing standing between the corpus
   * and silent vacuity: a case that asserts `overlayOption` (singular), or
   * `entryMode` at the top level instead of inside `config`, is a case that
   * checks NOTHING — and it would go green, forever, looking like coverage.
   *
   * The risk is live every time this switch grows, which this slice did (the
   * invoice modes). So the arm gets its own test rather than being trusted to
   * survive the next edit.
   */
  it("assertExpect THROWS on an expect key it does not recognise", () => {
    const snap = runCase({
      id: "unknown-expect-key-probe",
      events: [{ type: "key", combo: "F8" }],
      expect: {},
    });
    // A plausible TYPO, not gibberish: "overlayOption" is one character from a
    // key the runner really has, which is exactly the mistake that would slip
    // through review.
    expect(() =>
      assertExpect(snap, { overlayOption: ["Item Invoice"] }),
    ).toThrow(/Unhandled expect key in corpus/);
  });

  it("every expect key the corpus actually USES is handled (no vacuous fixture)", () => {
    // The complement of the test above: the default arm is only useful if no
    // SHIPPED fixture is silently landing on it. Running every case's expect
    // block through the assertion is what the loop above already does — this
    // adds the guarantee that a case with an EMPTY expect block, which would
    // assert nothing at all and pass, cannot exist.
    for (const c of cases) {
      expect(
        Object.keys(c.expect).length,
        `corpus case ${c.id} asserts nothing — an empty expect block passes vacuously`,
      ).toBeGreaterThan(0);
    }
  });
});

describe("navMachine unit semantics", () => {
  function drive(events: Array<{ type: "KEY"; combo: string } | { type: "SELECT"; menuItem: string }>) {
    const actor = createActor(navMachine, {
      input: { startScreen: "gateway", keymapProfile: "tallyprime" },
    });
    actor.start();
    for (const ev of events) actor.send(ev);
    const snap = projectSnapshot(actor.getSnapshot().context);
    actor.stop();
    return snap;
  }

  it("Esc pops the screen stack one level", () => {
    // gateway -> F8 (voucher) -> Esc -> gateway
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(snap.screen).toBe("gateway");
    expect(snap.screenStackDepth).toBe(1);
  });

  it("Esc cannot pop past the root frame", () => {
    const snap = drive([
      { type: "KEY", combo: "Esc" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(snap.screen).toBe("gateway");
    expect(snap.screenStackDepth).toBe(1);
  });

  it("Ctrl+A accepts the voucher and reopens a fresh saved form", () => {
    // gateway -> F8 sales -> advance a field -> Ctrl+A
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Enter" }, // date -> party-ledger
      { type: "KEY", combo: "Ctrl+A" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.voucherType).toBe<VoucherType>("sales");
    expect(snap.saved).toBe(true);
    // Fresh form: cursor returns to the date field.
    expect(snap.field).toBe("date");
  });

  it("F-keys switch the active voucher type in place (no stack growth)", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" }, // sales
      { type: "KEY", combo: "F5" }, // payment, replacing in place
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.voucherType).toBe<VoucherType>("payment");
    expect(snap.screenStackDepth).toBe(2);
  });

  it("Ctrl+H opens the Change Mode picker over the voucher", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Ctrl+H" },
    ]);
    expect(snap.overlay).toBe("change-mode");
    // Sales gets the invoice-mode family. Two of the three are real; only
    // "As Voucher" is still disclosed as not built.
    expect(overlayOptions(snap)).toEqual([
      "Item Invoice",
      "Accounting Invoice",
      "As Voucher — not built",
    ]);
  });

  // THE ITEM INVOICE IS THE DEFAULT-CHANGING ONE, so what F8 opens INTO is
  // pinned separately from what Ctrl+H offers. Shipping the grid must not
  // silently change the form an operator gets when they press F8 and start
  // typing — they get the accounting invoice until they ask for goods.
  it("a fresh Sales frame carries NO chosen mode (the accounting invoice is the default)", () => {
    const snap = drive([{ type: "KEY", combo: "F8" }]);
    expect(snap.config?.entryMode).toBeUndefined();
  });

  it("choosing Item Invoice applies it and closes the overlay in one keystroke", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Ctrl+H" },
      { type: "SELECT", menuItem: "Item Invoice" },
    ]);
    expect(snap.overlay ?? null).toBeNull();
    expect(snap.config?.entryMode).toBe("item-invoice");
  });

  it("the item-invoice choice SURVIVES Ctrl+A and is remembered per type", () => {
    // Same reasoning as the single/double case below: Ctrl+A replaces the frame
    // with a fresh form for bulk entry, so a frame-only choice would revert an
    // operator keying a run of invoices back to the accounting form after EVERY
    // save. And it is per TYPE — choosing goods entry for Sales says nothing
    // about how you want to key a Purchase.
    const sales = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Ctrl+H" },
      { type: "SELECT", menuItem: "Item Invoice" },
      { type: "KEY", combo: "Ctrl+A" },
    ]);
    expect(sales.config?.entryMode).toBe("item-invoice");

    const purchase = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Ctrl+H" },
      { type: "SELECT", menuItem: "Item Invoice" },
      { type: "KEY", combo: "F9" },
    ]);
    expect(purchase.voucherType).toBe("purchase");
    expect(purchase.config?.entryMode).toBeUndefined();
  });

  it("Ctrl+H offers SINGLE vs DOUBLE entry on a Payment, not the invoice modes", () => {
    // The picker is class-aware. It previously showed the same three invoice
    // labels on every voucher — and did nothing when one was chosen — so the
    // key Tally uses to switch entry style was a dead end on the three classes
    // where it matters most.
    const snap = drive([
      { type: "KEY", combo: "F5" },
      { type: "KEY", combo: "Ctrl+H" },
    ]);
    expect(overlayOptions(snap)).toEqual(["Single Entry", "Double Entry"]);
  });

  it("choosing a mode applies it and CLOSES the overlay in one keystroke", () => {
    const snap = drive([
      { type: "KEY", combo: "F5" },
      { type: "KEY", combo: "Ctrl+H" },
      { type: "SELECT", menuItem: "Double Entry" },
    ]);
    expect(snap.overlay ?? null).toBeNull();
    expect(snap.config?.entryMode).toBe("double");
  });

  it("a NOT-BUILT invoice mode dismisses without changing anything", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Ctrl+H" },
      { type: "SELECT", menuItem: "As Voucher — not built" },
    ]);
    expect(snap.overlay ?? null).toBeNull();
    expect(snap.config?.entryMode).toBeUndefined();
  });

  // THE CHOICE MUST OUTLIVE THE FRAME.
  //
  // entryMode used to live only on the active frame, and Ctrl+A replaces the
  // frame with a fresh form for bulk entry — so an operator who chose Double
  // Entry was reverted to single after EVERY save, and again on every F5/F6
  // round trip. Ctrl+H looked like it worked because the mode did apply; it
  // just never survived to the next voucher, which is the whole point of a
  // bulk-entry mode.
  it("the chosen entry mode SURVIVES Ctrl+A, which re-opens a fresh form", () => {
    const snap = drive([
      { type: "KEY", combo: "F5" },
      { type: "KEY", combo: "Ctrl+H" },
      { type: "SELECT", menuItem: "Double Entry" },
      { type: "KEY", combo: "Ctrl+A" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.config?.entryMode).toBe("double");
  });

  it("the chosen entry mode survives switching voucher type and back", () => {
    const snap = drive([
      { type: "KEY", combo: "F5" },
      { type: "KEY", combo: "Ctrl+H" },
      { type: "SELECT", menuItem: "Double Entry" },
      { type: "KEY", combo: "F6" },
      { type: "KEY", combo: "F5" },
    ]);
    expect(snap.config?.entryMode).toBe("double");
  });

  it("it is remembered PER TYPE — choosing for Payment says nothing about Receipt", () => {
    // Tally remembers per voucher type. One global would mean configuring how
    // you key payments silently reconfigured contras.
    const snap = drive([
      { type: "KEY", combo: "F5" },
      { type: "KEY", combo: "Ctrl+H" },
      { type: "SELECT", menuItem: "Double Entry" },
      { type: "KEY", combo: "F6" },
    ]);
    expect(snap.voucherType).toBe("receipt");
    expect(snap.config?.entryMode).toBeUndefined();
  });
});
