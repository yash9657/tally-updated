import type { ComponentType } from "react";
import type { NavSnapshot } from "../keystroke/navMachine";
import type { FrameConfig } from "../keystroke/types";

/**
 * The screen registry — the seam other agents (vouchers, reports, masters) plug
 * their presentational components into WITHOUT editing the router or the shell.
 *
 * A screen component receives the current {@link NavSnapshot} (the machine's
 * projection of the active stack frame) and an `onSelect` callback to feed
 * selection-list / row picks back into the machine. It must be purely
 * presentational + data-fetching: ALL navigation is owned by the navMachine.
 *
 * The router resolves a component by a {@link ScreenRouteKey} derived from the
 * snapshot: a plain `ScreenKind` (e.g. "voucher", "master-create"), or a
 * report keyed by name (e.g. "report:Day Book"). Report and report-detail
 * screens fall back to a generic handler when no name-specific entry exists.
 */

export interface ScreenComponentProps {
  snapshot: NavSnapshot;
  onSelect: (item: string) => void;
  /**
   * Publish a view-configuration patch onto the ACTIVE FRAME — the same channel
   * the F2 / F12 / Ctrl+F overlays use.
   *
   * ⛔ IT EXISTS FOR ONE SHAPE: A SCREEN WHOSE DIMENSION IS TYPED ON ITS OWN
   * FACE AND MUST TRAVEL WITH A DRILL. The Payroll Register is the first: its
   * dimension is a WAGE MONTH rather than an F2 period (a From/To range would be
   * a span no social-security scheme has an answer for), so the month exists only
   * as screen state — and Enter on a row must open THAT month's payslip. Without
   * this the payslip lands on "type a month" and an operator re-keys the month
   * they just chose; re-key it slightly differently and they read one person's
   * July payslip under a register for June, with nothing on either page saying
   * so. Measured in a real browser, where the drill arrived with no month at all.
   *
   * ⚠️ IT IS NOT A GENERAL BACK-CHANNEL INTO THE MACHINE. A screen may publish
   * what it is CURRENTLY SHOWING; it may not navigate — all navigation stays with
   * the machine, which is the rule this seam has kept since Phase 0. Publish the
   * LOADED value and never the typed one, for the reason Ctrl+P sends the loaded
   * month: a value typed but never fetched has produced nothing on screen, and a
   * drill from it opens a page that does not match the one it came from.
   *
   * Optional, so the many screens with no dimension of their own ignore it and
   * every existing call site keeps compiling.
   */
  onConfig?: (patch: FrameConfig) => void;
}

export type ScreenComponent = ComponentType<ScreenComponentProps>;

/** A registry key: a bare ScreenKind, or "report:<Name>" / "report-detail:<Name>". */
export type ScreenRouteKey = string;

export class ScreenRegistry {
  private readonly map = new Map<ScreenRouteKey, ScreenComponent>();

  /** Register (or replace) the component for a route key. */
  register(key: ScreenRouteKey, component: ScreenComponent): this {
    this.map.set(key, component);
    return this;
  }

  /** Register many at once. */
  registerAll(entries: Record<ScreenRouteKey, ScreenComponent>): this {
    for (const [k, c] of Object.entries(entries)) this.map.set(k, c);
    return this;
  }

  /**
   * Resolve the component for a snapshot. Resolution order for reports:
   *   report:<Name>  ->  report  (generic fallback)
   * and likewise for report-detail. Other screens resolve by ScreenKind.
   */
  resolve(snapshot: NavSnapshot): ScreenComponent | undefined {
    const { screen, reportName } = snapshot;
    if ((screen === "report" || screen === "report-detail") && reportName) {
      const named = this.map.get(`${screen}:${reportName}`);
      if (named) return named;
    }
    return this.map.get(screen);
  }
}

/** Derive the route key the registry would use for a snapshot (for debugging). */
export function routeKeyFor(snapshot: NavSnapshot): ScreenRouteKey {
  if (
    (snapshot.screen === "report" || snapshot.screen === "report-detail") &&
    snapshot.reportName
  ) {
    return `${snapshot.screen}:${snapshot.reportName}`;
  }
  return snapshot.screen;
}
