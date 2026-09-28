import { useMemo } from "react";
import type { NavSnapshot } from "../keystroke/navMachine";
import {
  actionLabel,
  keyCatalogFor,
  resolveKeymap,
} from "../keystroke/keymaps/tallyprime";
import type { KeymapAction } from "../keystroke/types";

/**
 * F1 Help — the keystroke reference, GENERATED FROM THE KEYMAP the engine
 * dispatches. That is the whole design: the help a user reads and the table the
 * shell resolves keys against are the same data, so the documentation cannot
 * drift from the behaviour (the classic failure of a hand-written cheat sheet).
 *
 * It lists the keys for the screen you pressed F1 ON (the parent frame), splits
 * them into what works here and what this build does not have yet, and states
 * the missing ones plainly rather than hiding them.
 */
export function HelpScreen({ snapshot }: { snapshot: NavSnapshot }) {
  const forScreen = snapshot.parentScreen;
  const rows = useMemo(
    () => keyCatalogFor(resolveKeymap("tallyprime"), forScreen),
    [forScreen],
  );

  const available = rows.filter((r) => r.action.kind !== "unavailable");
  const missing = rows
    .filter((r) => r.action.kind === "unavailable")
    .map((r) => ({
      combo: r.combo,
      action: r.action as Extract<KeymapAction, { kind: "unavailable" }>,
    }));

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">Help — Keyboard</h1>
      <p className="mt-1 text-sm">
        Shortcuts for{" "}
        <span className="text-tally-title">{screenLabel(forScreen)}</span>. This
        list is generated from the live keymap. Esc goes back.
      </p>

      <h2 className="mt-4 font-bold text-tally-title">Works here</h2>
      <table className="mt-1 max-w-2xl text-sm" data-testid="help-available">
        <tbody>
          {available.map((r) => (
            <tr key={r.combo}>
              <td className="w-32 py-0.5 pr-4 font-bold text-tally-title">
                {r.combo}
              </td>
              <td className="py-0.5">{actionLabel(r.action)}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <h2 className="mt-4 font-bold text-tally-title">
        Bound, but not built yet in this edition
      </h2>
      <table className="mt-1 max-w-3xl text-sm" data-testid="help-unavailable">
        <tbody>
          {missing.map((r) => (
            <tr key={r.combo}>
              <td className="w-32 py-0.5 pr-4 font-bold text-tally-title">
                {r.combo}
              </td>
              <td className="py-0.5">
                {r.action.feature} — {r.action.note}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

function screenLabel(screen: NavSnapshot["screen"] | undefined): string {
  switch (screen) {
    case "voucher":
      return "the voucher screen";
    case "report":
    case "report-detail":
      return "reports";
    case "master-create":
      return "master creation";
    case "bill-inbox":
      return "the Bill Inbox";
    case "query":
      return "Ask your books";
    case "approval-inbox":
      return "the Approval Inbox";
    case "insights":
      return "the Insights inbox";
    case undefined:
      return "the Gateway";
    default:
      return "the Gateway";
  }
}
