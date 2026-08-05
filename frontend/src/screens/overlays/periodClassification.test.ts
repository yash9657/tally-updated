import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import {
  AS_AT_ONLY_REPORTS,
  FULL_PERIOD_REPORTS,
  NO_DATE_DIMENSION_REPORTS,
  PERIOD_SCREEN_LABELS,
} from "./FrameOverlays";

/**
 * THE F2 PERIOD DIALOG TOLD TWO REPORTS THEIR DATES DID NOTHING, AND BOTH WERE
 * READING THEM.
 *
 * `PeriodOverlay` classified reports with two allowlists and defaulted anything
 * missing from both to the note "…has no date dimension in this build, so a
 * period is not applied to it." The TDS / TCS Register and the Stock Summary
 * were in neither list. On the register the contradiction was on screen at the
 * same moment: the page behind the dialog reads "Press F2 to set the period.
 * Both dates are needed", and the dialog on top of it said the period would be
 * ignored.
 *
 * ⚠️ IT WAS FOUND BY DRIVING THE APP, NOT BY READING IT, AND NOT BY ANY OF THE
 * 461 TESTS — the same way Alt+O's dead binding was found (HANDOFF-NEXT-THREE.md
 * §4.1). The pattern is worth naming: a DEFAULT BRANCH that produces a
 * plausible sentence is invisible to tests that only assert the sentences they
 * expect.
 *
 * This test removes the default. Every report registered in defaultScreens.tsx
 * must be classified EXPLICITLY as one of:
 *
 *   - FULL_PERIOD_REPORTS      — reads periodFrom and periodTo
 *   - AS_AT_ONLY_REPORTS       — reads periodTo alone (a position, not a flow)
 *   - NO_DATE_DIMENSION_REPORTS — genuinely reads neither
 *
 * so that adding a report and forgetting the dialog fails here instead of
 * shipping a screen that contradicts itself.
 */
describe("the F2 period classification", () => {
  const registry = readFileSync(
    join(__dirname, "..", "..", "app", "defaultScreens.tsx"),
    "utf8",
  );

  // Every `"report:NAME": Component` key in the registry.
  const registered = [...registry.matchAll(/"report:([^"]+)":/g)].map(
    (m) => m[1],
  );

  it("finds the registered report screens (guards the regex itself)", () => {
    // If this ever drops to zero the test below passes vacuously, which is the
    // one way a source-scanning test lies.
    expect(registered.length).toBeGreaterThanOrEqual(7);
    expect(registered).toContain("TDS / TCS Register");
    expect(registered).toContain("Stock Summary");
  });

  it("classifies EVERY registered report exactly once", () => {
    const all = [
      ...FULL_PERIOD_REPORTS,
      ...AS_AT_ONLY_REPORTS,
      ...NO_DATE_DIMENSION_REPORTS,
    ];
    for (const name of registered) {
      const hits = all.filter((n) => n === name).length;
      expect(
        hits,
        `"${name}" is classified ${hits} time(s). Every report screen must be ` +
          `in exactly one of FULL_PERIOD_REPORTS, AS_AT_ONLY_REPORTS or ` +
          `NO_DATE_DIMENSION_REPORTS — a report in none of them is told its ` +
          `period does nothing, which is how this bug shipped twice.`,
      ).toBe(1);
    }
  });

  it("classifies a report by what its screen actually reads", () => {
    // The classification is not a preference — it is a fact about the source.
    // A screen reading both bounds is a PERIOD; one reading only the upper
    // bound is a POSITION. Checked against the two screens that were wrong.
    const tds = readFileSync(
      join(__dirname, "..", "reports", "TDSRegisterScreen.tsx"),
      "utf8",
    );
    expect(tds).toContain("periodFrom");
    expect(tds).toContain("periodTo");
    expect(FULL_PERIOD_REPORTS).toContain("TDS / TCS Register");

    const stock = readFileSync(
      join(__dirname, "..", "reports", "StockSummaryScreen.tsx"),
      "utf8",
    );
    expect(stock).toContain("periodTo");
    expect(stock).not.toContain("periodFrom");
    expect(AS_AT_ONLY_REPORTS).toContain("Stock Summary");
  });
});

/**
 * THE SAME BUG, ONE STEP AROUND THE ALLOWLISTS: a report screen that is a
 * ScreenKind rather than a `report` frame.
 *
 * The suite above scans `"report:NAME"` keys out of defaultScreens.tsx, so it
 * cannot see the two T2 cost summaries at all — they are registered by ScreenKind
 * (`"cost-centre-summary"`), they carry no `snapshot.reportName`, and the three
 * allowlists are keyed by report NAME. Left alone, both would have matched nothing
 * in PeriodOverlay and been told "has no date dimension in this build" over
 * screens that refuse to render without BOTH dates. That is not a new failure
 * mode; it is the SAME one, and the regex-based guard is blind to it.
 *
 * `PERIOD_SCREEN_LABELS` closes it: a ScreenKind that reads a period declares the
 * name it is classified under, and this suite asserts the closure — every entry in
 * that table must be classified exactly once, and its classification must match
 * what the screen's source actually reads.
 */
describe("the F2 period classification for SCREEN-KIND reports", () => {
  const SCREEN_SOURCES: Record<string, string> = {
    "cost-centre-summary": join(
      __dirname,
      "..",
      "reports",
      "CostCentreSummaryScreen.tsx",
    ),
    "cost-category-summary": join(
      __dirname,
      "..",
      "reports",
      "CostCategorySummaryScreen.tsx",
    ),
  };

  it("finds the screen-kind report screens (guards the table itself)", () => {
    // An empty table would make every assertion below pass vacuously — the one
    // way a source-scanning test lies (see the sibling assertion above).
    const kinds = Object.keys(PERIOD_SCREEN_LABELS);
    expect(kinds.length).toBeGreaterThanOrEqual(2);
    expect(kinds).toContain("cost-centre-summary");
    expect(kinds).toContain("cost-category-summary");
  });

  it("classifies EVERY period-reading screen exactly once", () => {
    const all = [
      ...FULL_PERIOD_REPORTS,
      ...AS_AT_ONLY_REPORTS,
      ...NO_DATE_DIMENSION_REPORTS,
    ];
    for (const [kind, label] of Object.entries(PERIOD_SCREEN_LABELS)) {
      const hits = all.filter((n) => n === label).length;
      expect(
        hits,
        `the "${kind}" screen is classified as "${label}", which appears ` +
          `${hits} time(s) across FULL_PERIOD_REPORTS, AS_AT_ONLY_REPORTS and ` +
          `NO_DATE_DIMENSION_REPORTS. A screen in none of them is told its ` +
          `period does nothing while it reads it.`,
      ).toBe(1);
    }
  });

  it("classifies each one by what its screen actually reads", () => {
    // Not a preference — a fact about the source. Both cost summaries are FLOWS
    // between two dates: they sum the allocations of postings dated in the range,
    // so both bounds are meaningful and both are read.
    for (const [kind, path] of Object.entries(SCREEN_SOURCES)) {
      const src = readFileSync(path, "utf8");
      expect(src, `${kind} must read periodFrom`).toContain("periodFrom");
      expect(src, `${kind} must read periodTo`).toContain("periodTo");
      expect(FULL_PERIOD_REPORTS).toContain(PERIOD_SCREEN_LABELS[kind]);
    }
  });

  it("every screen in the table is a screen the F2 overlay can be opened on", () => {
    // The classification is only reachable if the machine opens the overlay for
    // that screen. A screen classified here and missing from navMachine's `period`
    // case would be a correct note nobody can ever see, and F2 would silently do
    // nothing on a screen whose heading asks for a period.
    const machine = readFileSync(
      join(__dirname, "..", "..", "keystroke", "navMachine.ts"),
      "utf8",
    );
    const periodCase = machine.slice(
      machine.indexOf('case "period": {'),
      machine.indexOf('case "filter": {'),
    );
    expect(periodCase.length).toBeGreaterThan(0);
    for (const kind of Object.keys(PERIOD_SCREEN_LABELS)) {
      expect(
        periodCase,
        `F2 does not open the period overlay on the "${kind}" screen`,
      ).toContain(`"${kind}"`);
    }
  });
});
