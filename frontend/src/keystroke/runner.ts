import { createActor } from "xstate";
import { navMachine, projectSnapshot } from "./navMachine";
import type { NavSnapshot } from "./navMachine";
import type { ScreenKind } from "./types";

/** A single event in a keystroke corpus case. */
export type CorpusEvent =
  | { type: "key"; combo: string }
  | { type: "select"; menuItem: string };

/** The shape of a `/corpora/keystroke/*.json` case (fields we consume). */
export interface CorpusCase {
  id: string;
  description?: string;
  keymapProfile?: string;
  startScreen?: ScreenKind;
  events: CorpusEvent[];
  expect: Record<string, unknown>;
}

/**
 * Drive the navigation machine through a corpus case's events and return the
 * final snapshot. This is the single execution path the conformance suite uses,
 * mirroring how the live UI feeds the machine.
 */
export function runCase(c: CorpusCase): NavSnapshot {
  const actor = createActor(navMachine, {
    input: {
      startScreen: c.startScreen ?? "gateway",
      keymapProfile: c.keymapProfile ?? "tallyprime",
    },
  });
  actor.start();
  for (const ev of c.events) {
    if (ev.type === "key") {
      actor.send({ type: "KEY", combo: ev.combo });
    } else if (ev.type === "select") {
      actor.send({ type: "SELECT", menuItem: ev.menuItem });
    }
  }
  const snap = projectSnapshot(actor.getSnapshot().context);
  actor.stop();
  return snap;
}
