import type { ComponentType } from "react";
import type { NavSnapshot } from "../keystroke/navMachine";

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
