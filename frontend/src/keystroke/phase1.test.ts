import { describe, it, expect } from "vitest";
import { createActor } from "xstate";
import { navMachine, projectSnapshot } from "./navMachine";
import type { NavSnapshot } from "./navMachine";
import { buttonBarLabels } from "./buttonBar";

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
      // further Enter — with nothing highlighted inside it — the voucher.
      { type: "SELECT", menuItem: "Sundry Debtors" },
      { type: "KEY", combo: "Enter" }, // report-detail
      { type: "KEY", combo: "Enter" }, // -> voucher (alter)
    ]);
    expect(afterVoucher.screen).toBe("voucher");
    expect(afterVoucher.mode).toBe("alter");
    expect(afterVoucher.parentScreen).toBe("report-detail");
  });

  it("Day Book row Enter drills straight to the voucher (unchanged Phase 0 behaviour)", () => {
    const snap = drive([
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Day Book" },
      { type: "KEY", combo: "Enter" },
      { type: "KEY", combo: "Enter" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.mode).toBe("alter");
    expect(snap.parentScreen).toBe("report");
    expect(snap.parentReportName).toBe("Day Book");
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
