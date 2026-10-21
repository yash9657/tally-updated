/** Opt-in local pilot instrumentation. Never captures keys, text, money or IDs
 * from the DOM and never sends telemetry. Automation time is not human time. */
export type TimingContext = {
  task: string;
  industry: "retail" | "wholesale" | "services" | "manufacturing";
  bookFingerprint: string;
  path: "manual_ui" | "agent_ui";
  source: "operator_pilot" | "browser_automation";
  operator: string; // pseudonymous participant code, not a login or name
};

export function measureWorkflow(target: EventTarget, context: TimingContext, now = () => performance.now()) {
  const started = now();
  let activeStart: number | null = null;
  let activeMs = 0;
  let clicks = 0;
  let keystrokes = 0;
  let approvals = 0;
  let clarifications = 0;
  let corrections = 0;
  let finished = false;
  const input = (event: Event) => {
    if (context.source === "operator_pilot" && !event.isTrusted) return;
    if (event.type === "click") clicks++;
    else keystrokes++;
  };
  target.addEventListener("click", input, true);
  target.addEventListener("keydown", input, true);
  const setActive = (active: boolean) => {
    if (finished) throw new Error("measurement already finished");
    if (activeStart !== null && !active) { activeMs += now() - activeStart; activeStart = null; }
    if (activeStart === null && active) activeStart = now();
  };
  return {
    // Operator/observer marks active work including reading, thinking, review
    // and corrections; input idle time is deliberately not used as a proxy.
    setActive,
    intervention(kind: "approval" | "clarification" | "correction") {
      if (finished) throw new Error("measurement already finished");
      if (kind === "approval") approvals++;
      if (kind === "clarification") clarifications++;
      if (kind === "correction") corrections++;
    },
    finish(result: { completed: boolean; qualityVerified: boolean; finalFingerprint: string | null; deliverable: "state_change" | "downloaded_file" | "advice_or_draft" }) {
      setActive(false);
      finished = true;
      target.removeEventListener("click", input, true);
      target.removeEventListener("keydown", input, true);
      const completed = result.completed && result.qualityVerified && Boolean(result.finalFingerprint) && result.deliverable !== "advice_or_draft";
      return { ...context, ...result, completed, elapsedMs: now() - started,
        activeHumanMs: context.source === "operator_pilot" ? activeMs : null,
        clicks, keystrokes, approvals, clarifications, corrections };
    },
  };
}
