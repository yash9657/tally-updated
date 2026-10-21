import { expect, it } from "vitest";
import { measureWorkflow, type TimingContext } from "./workflowTiming";

const context: TimingContext = { task: "receipt-allocation", industry: "services", bookFingerprint: "seed-v1", path: "agent_ui", source: "browser_automation", operator: "automation" };
it("counts DOM interactions without claiming active human time or draft completion", () => {
  const target = document.createElement("div");
  const clock = { time: 0 };
  const m = measureWorkflow(target, context, () => clock.time);
  m.setActive(true);
  target.dispatchEvent(new MouseEvent("click"));
  target.dispatchEvent(new KeyboardEvent("keydown", { key: "private-input-not-recorded" }));
  m.intervention("approval");
  clock.time = 100;
  const r = m.finish({ completed: true, qualityVerified: true, finalFingerprint: "draft", deliverable: "advice_or_draft" });
  expect(r).toMatchObject({ clicks: 1, keystrokes: 1, approvals: 1, elapsedMs: 100, activeHumanMs: null, completed: false });
  expect(JSON.stringify(r)).not.toContain("private-input");
  expect(() => m.finish({ completed: true, qualityVerified: true, finalFingerprint: "x", deliverable: "state_change" })).toThrow();
});
it("includes explicitly observed review time and excludes waiting in an operator pilot", () => {
  let time = 0;
  const m = measureWorkflow(new EventTarget(), { ...context, source: "operator_pilot" }, () => time);
  m.setActive(true); time = 25; m.setActive(false);
  time = 100; m.setActive(true); time = 120;
  expect(m.finish({ completed: true, qualityVerified: true, finalFingerprint: "verified-books", deliverable: "state_change" })).toMatchObject({ activeHumanMs: 45, elapsedMs: 120, completed: true });
});
