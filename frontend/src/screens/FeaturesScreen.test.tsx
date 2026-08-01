import { describe, it, expect, afterEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
} from "@testing-library/react";

import { FeaturesScreen } from "./FeaturesScreen";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api";

/**
 * F11 > TDS Deductor Details — Item Zero step 2, layer 5.
 *
 * The rest of this screen is a static statement of fact and needs no test. The
 * panel does: it is the ONLY way an operator can record the s.402(11)/(37)
 * status, and without that row the TDS post path refuses every voucher.
 *
 * WHAT THESE TESTS CANNOT PROVE, said plainly: the mock client does not derive
 * TDS, so nothing here exercises the refusal the panel exists to clear. That is
 * covered server-side by TestRecordingTheStatusUnblocksTDSPosting, which drives
 * the real post path over HTTP.
 */

afterEach(cleanup);

function renderFeatures() {
  return render(
    <CompanyProvider
      client={createMockClient()}
      initialCompany={{ id: "company-demo", name: "Demo Traders" }}
    >
      <FeaturesScreen />
    </CompanyProvider>,
  );
}

describe("F11 TDS Deductor Details", () => {
  it("says nothing is recorded, and warns that TDS will be refused", async () => {
    renderFeatures();
    expect(
      await screen.findByText(/Nothing recorded yet .* will be refused/),
    ).toBeTruthy();
  });

  it("records a status and states which year it GOVERNS, not the year keyed", async () => {
    renderFeatures();
    await screen.findByLabelText("Deductor type");

    fireEvent.change(screen.getByLabelText("Tax year beginning"), {
      target: { value: "2025" },
    });
    fireEvent.change(screen.getByLabelText("Deductor type"), {
      target: { value: "COMPANY" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Record" }));

    // THE ASSERTION THAT MATTERS. A 2025-26 row governs payments made in
    // 2026-27 (s.402(37)(b), "the tax year immediately preceding"). An operator
    // who is not told this keys the payment's own year and gets a refusal that
    // looks like a bug.
    await waitFor(() =>
      expect(
        screen.getByText(/Recorded for 2025-26 .* governs payments made in 2026-27/),
      ).toBeTruthy(),
    );
    // And it appears in the list with the same statement.
    expect(screen.getByText(/governs payments in 2026-27/)).toBeTruthy();
  });

  it("asks for turnover ONLY for the categories the test can reach", async () => {
    renderFeatures();
    const picker = await screen.findByLabelText("Deductor type");

    // A COMPANY is a specified and designated person whatever it turned over,
    // so the fields must be ABSENT — not merely optional. A figure typed
    // against a COMPANY is a 400, so offering the box would invite the error.
    fireEvent.change(picker, { target: { value: "COMPANY" } });
    expect(screen.queryByLabelText("Prior-year turnover")).toBeNull();

    // An INDIVIDUAL takes the turnover test, so both operands appear —
    // BUSINESS and PROFESSION carry different limits, so the activity is asked
    // for rather than inferred.
    fireEvent.change(picker, { target: { value: "INDIVIDUAL" } });
    expect(screen.getByLabelText("Prior-year turnover")).toBeTruthy();
    expect(screen.getByLabelText("Business or profession")).toBeTruthy();
  });

  it("surfaces the server's refusal instead of failing silently", async () => {
    renderFeatures();
    const picker = await screen.findByLabelText("Deductor type");

    // An INDIVIDUAL with no turnover: the row cannot be evaluated, and the
    // server says which fact is missing. Reaching the form past the conditional
    // fields is done by picking the category and then clearing the box.
    fireEvent.change(picker, { target: { value: "INDIVIDUAL" } });
    fireEvent.change(screen.getByLabelText("Prior-year turnover"), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Record" }));

    await waitFor(() =>
      expect(screen.getByText(/subject to the turnover test/)).toBeTruthy(),
    );
  });

  it("still renders the honest feature inventory around the panel", async () => {
    renderFeatures();
    // The TDS row stays "Not built": there is no register and no Form 140, and
    // TCS is not on the post path. Recording a deductor status does not make
    // the feature shipped, and this screen must not imply that it does.
    expect(await screen.findByText("TDS / TCS")).toBeTruthy();
    expect(screen.getAllByText("Not built").length).toBeGreaterThan(0);
  });
});
