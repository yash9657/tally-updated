import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import {
  AS_AT_ONLY_REPORTS,
  FULL_PERIOD_REPORTS,
  NO_DATE_DIMENSION_REPORTS,
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
