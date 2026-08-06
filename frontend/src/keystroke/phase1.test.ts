import { describe, it, expect } from "vitest";
import { createActor } from "xstate";
import { navMachine, projectSnapshot } from "./navMachine";
import type { NavSnapshot } from "./navMachine";
import { buttonBarLabels } from "./buttonBar";
import { voucherRowKey } from "./voucherClass";

type Ev =
  | { type: "KEY"; combo: string }
  | { type: "SELECT"; menuItem: string };

function drive(events: Ev[]): NavSnapshot {
  const actor = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  });
  actor.start();
  for (const ev of events) actor.send(ev);
  const snap = projectSnapshot(actor.getSnapshot().context);
  actor.stop();
  return snap;
}

describe("Phase 1 navMachine extensions", () => {
  it("Alt+C in a voucher opens an inline master-create screen over it", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Alt+C" },
    ]);
    expect(snap.screen).toBe("master-create");
    expect(snap.masterKind).toBe("ledger");
    expect(snap.field).toBe("name");
    expect(snap.parentScreen).toBe("voucher");
    expect(snap.canReturnToVoucher).toBe(true);
  });

  it("Ctrl+A in master-create returns to the voucher with the created master selected", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Alt+C" },
      { type: "SELECT", menuItem: "Acme Traders" },
      { type: "KEY", combo: "Ctrl+A" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.voucherType).toBe("sales");
    expect(snap.screenStackDepth).toBe(2);
    expect(snap.selectedItem).toBe("Acme Traders");
  });

  it("Esc cancels master-create and returns to the voucher", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" },
      { type: "KEY", combo: "Alt+C" },
      { type: "KEY", combo: "Esc" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.screenStackDepth).toBe(2);
  });

  it("Go To opens any of the five Phase 1 reports", () => {
    for (const name of [
      "Balance Sheet",
      "Profit & Loss A/c",
      "Trial Balance",
      "Day Book",
      "Outstandings",
    ]) {
      const snap = drive([
        { type: "KEY", combo: "Alt+G" },
        { type: "SELECT", menuItem: name },
        { type: "KEY", combo: "Enter" },
      ]);
      expect(snap.screen).toBe("report");
      expect(snap.reportName).toBe(name);
      expect(snap.screenStackDepth).toBe(2);
    }
  });

  it("Enter on a statement report drills into a report-detail level, then a voucher", () => {
    const afterDetail = drive([
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Balance Sheet" },
      { type: "KEY", combo: "Enter" }, // report
      { type: "SELECT", menuItem: "Sundry Debtors" },
      { type: "KEY", combo: "Enter" }, // -> report-detail
    ]);
    expect(afterDetail.screen).toBe("report-detail");
    expect(afterDetail.reportName).toBe("Balance Sheet");
    expect(afterDetail.parentScreen).toBe("report");

    const afterVoucher = drive([
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Balance Sheet" },
      { type: "KEY", combo: "Enter" },
      // A statement drill needs a highlighted line (the report screen publishes
      // one for the active row); Enter then opens that line's detail, and a
      // further Enter — on a VOUCHER row inside it — the voucher itself. The
      // ledger level's rows carry their class, exactly as the Day Book's do.
      { type: "SELECT", menuItem: "Sundry Debtors" },
      { type: "KEY", combo: "Enter" }, // report-detail
      { type: "SELECT", menuItem: voucherRowKey("v-1", "sales") },
      { type: "KEY", combo: "Enter" }, // -> voucher (alter)
    ]);
    expect(afterVoucher.screen).toBe("voucher");
    expect(afterVoucher.mode).toBe("alter");
    expect(afterVoucher.parentScreen).toBe("report-detail");
  });

  it("Day Book row Enter drills straight to the voucher", () => {
    const snap = drive([
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Day Book" },
      { type: "KEY", combo: "Enter" },
      { type: "SELECT", menuItem: voucherRowKey("v-1", "sales") },
      { type: "KEY", combo: "Enter" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.mode).toBe("alter");
    expect(snap.parentScreen).toBe("report");
    expect(snap.parentReportName).toBe("Day Book");
  });

  /**
   * ⛔⛔ THE DRILL OPENS THE CLASS THE ROW NAMES — the whole of §4.8.
   *
   * Until this, every voucher-bearing drill pushed
   * `newVoucherFrame(DEFAULT_LAST_VOUCHER, "alter")`, and `DEFAULT_LAST_VOUCHER`
   * is `"sales"`. Since `acceptVoucher` routes the PUT by the FRAME's type, a
   * Receipt drilled from the Day Book and accepted with Ctrl+A was written back
   * through `alterSalesVoucher` — **the document was re-classed**, over a frame
   * that titled itself "Sales Voucher (Alteration)".
   *
   * ⚠️ Sales is asserted LAST and on purpose: it is the one class for which the
   * old default was accidentally right, so a test that only drove Sales certified
   * the bug. §5.5 listed the Day Book as "known-good, driven end to end" — it
   * was, with a Sales voucher (§9.1a item 7).
   */
  it("⛔ drills into the class the ROW names, not into Sales", () => {
    for (const type of [
      "receipt",
      "payment",
      "contra",
      "journal",
      "purchase",
      "credit-note",
      "delivery-note",
      "physical-stock",
      "sales",
    ] as const) {
      const snap = drive([
        { type: "KEY", combo: "Alt+G" },
        { type: "SELECT", menuItem: "Day Book" },
        { type: "KEY", combo: "Enter" },
        { type: "SELECT", menuItem: voucherRowKey("v-9", type) },
        { type: "KEY", combo: "Enter" },
      ]);
      expect(snap.screen).toBe("voucher");
      expect(snap.mode).toBe("alter");
      expect(snap.voucherType).toBe(type);
    }
  });

  /**
   * ⛔ AND THE ROW THAT NAMES NO CLASS IS NOT DRILLED AT ALL.
   *
   * Two real cases produce one: an EMPTY Day Book (`DrillRows` publishes no row
   * key when nothing is drillable) and a class this build has no entry screen for
   * (Payroll). Both used to push an alteration frame with NO loaded voucher —
   * and Ctrl+A on that POSTS a brand-new Sales voucher from a frame titled
   * "(Alteration)". A no-op is the same answer the statement drill already gives
   * an empty selection, and the shell says the key did not apply.
   */
  it("⛔ CONTROL: a row naming no class does not open a voucher frame", () => {
    const openReport = [
      { type: "KEY", combo: "Alt+G" } as const,
      { type: "SELECT", menuItem: "Day Book" } as const,
      { type: "KEY", combo: "Enter" } as const,
    ];
    // Nothing highlighted at all — the empty-period case.
    expect(drive([...openReport, { type: "KEY", combo: "Enter" }]).screen).toBe(
      "report",
    );
    // A bare voucher id — what a Payroll row publishes.
    expect(
      drive([
        ...openReport,
        { type: "SELECT", menuItem: "0f1e2d3c-0000-4000-8000-000000000001" },
        { type: "KEY", combo: "Enter" },
      ]).screen,
    ).toBe("report");
    // A prefixed key whose class is not a VoucherType. Refused rather than
    // trusted: `voucherType` indexes VOUCHER_CONFIGS with no guard, so a bad slug
    // reaching a frame crashes the chrome's own title bar.
    expect(
      drive([
        ...openReport,
        { type: "SELECT", menuItem: "voucher:payroll:v-1" },
        { type: "KEY", combo: "Enter" },
      ]).screen,
    ).toBe("report");
    // And a prefixed key with an EMPTY id — an alteration frame for no voucher.
    expect(
      drive([
        ...openReport,
        { type: "SELECT", menuItem: "voucher:receipt:" },
        { type: "KEY", combo: "Enter" },
      ]).screen,
    ).toBe("report");
  });

  it("Gateway Enter drills into a selected report", () => {
    const snap = drive([
      { type: "SELECT", menuItem: "Profit & Loss A/c" },
      { type: "KEY", combo: "Enter" },
    ]);
    expect(snap.screen).toBe("report");
    expect(snap.reportName).toBe("Profit & Loss A/c");
  });

  it("voucher button bar advertises Alt+C Create Master", () => {
    expect(buttonBarLabels("voucher", "sales")).toContain("Alt+C Create Master");
  });

  it("master-create and report-detail resolve a button bar", () => {
    expect(buttonBarLabels("master-create")).toContain("Ctrl+A Accept");
    expect(buttonBarLabels("report-detail")).toContain("F2 Period");
  });
});
