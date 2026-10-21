import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { WorkflowObserver } from "./WorkflowObserver";

it("requires evidence for execution and retains unsuccessful pilot trials", () => {
  const { unmount } = render(<WorkflowObserver target={new EventTarget()} />);
  expect(screen.getByText("Start trial")).toHaveProperty("disabled", true);
  for (const [label, value] of [["Pseudonymous operator code", "operator-1"], ["Task / matched variant", "receipt-1"], ["Verified starting book fingerprint", "book-v1"]] as const) {
    fireEvent.change(screen.getByLabelText(label), { target: { value } });
  }
  fireEvent.click(screen.getByText("Start trial"));
  expect(screen.getByText("Finish verified execution")).toHaveProperty("disabled", true);
  fireEvent.click(screen.getByText("Mark active work"));
  fireEvent.click(screen.getByText("Count approval"));
  fireEvent.click(screen.getByText("Record incomplete trial"));
  expect(screen.getByText(/1 retained trials; 0 verified executions/)).toBeTruthy();
  fireEvent.click(screen.getByText("Start trial"));
  fireEvent.change(screen.getByLabelText("Verified final book / file fingerprint"), { target: { value: "final-v1" } });
  fireEvent.click(screen.getByLabelText("Independent result check passed"));
  fireEvent.change(screen.getByLabelText("Deliverable"), { target: { value: "advice_or_draft" } });
  expect(screen.getByText("Finish verified execution")).toHaveProperty("disabled", true);
  unmount(); // Releases listeners even when an operator abandons a trial.
});
