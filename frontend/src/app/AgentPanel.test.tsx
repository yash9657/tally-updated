import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

import { createMockClient, type CoredClient, type StartAgentRunRequest } from "../api";
import { mockAddAgentApproval } from "../api/mock";
import { AppShell } from "./AppShell";
import { CompanyProvider } from "./CompanyContext";
import { _setAgentPanelPollMs } from "./AgentPanel";
import {
  RUN_ID,
  paymentApproval,
  parkedRun,
  parkedRunEvents,
  salesApproval,
} from "../screens/agentFixtures";

/**
 * THE AGENT SIDE PANEL (Ctrl+K), DRIVEN THROUGH THE REAL SHELL (W2).
 *
 * The three properties that make it safe to put a text box over a keystroke
 * application are the ones pinned here: its keys STAY in it (an Enter typed in
 * an instruction must not drill the Gateway), it NEVER renders a voucher form
 * (a proposal opens in the native screen), and it says so when the server has
 * no model rather than looking broken.
 */

beforeEach(() => _setAgentPanelPollMs(10));
afterEach(() => {
  cleanup();
  _setAgentPanelPollMs(1000);
});

const COMPANY = { id: "company-demo", name: "Demo Traders", homeStateCode: "29" };

function renderShell(client: CoredClient) {
  return render(
    <CompanyProvider client={client} initialCompany={COMPANY}>
      <AppShell />
    </CompanyProvider>,
  );
}

const app = () => screen.getByRole("application");
const ctrlK = (target: Element = app()) =>
  fireEvent.keyDown(target, { key: "k", code: "KeyK", ctrlKey: true });

describe("Ctrl+K — the panel opens and closes from any screen", () => {
  it("opens on the Gateway, and Ctrl+K again closes it", async () => {
    renderShell(createMockClient());
    await screen.findByRole("combobox", { name: /MASTERS/ });
    ctrlK();
    expect(await screen.findByTestId("agent-panel")).toBeTruthy();
    ctrlK(screen.getByTestId("agent-instruction"));
    await waitFor(() => expect(screen.queryByTestId("agent-panel")).toBeNull());
  });

  it("Esc inside the panel closes the PANEL and not the screen underneath", async () => {
    renderShell(createMockClient());
    await screen.findByRole("combobox", { name: /MASTERS/ });
    fireEvent.keyDown(app(), { key: "F5" });
    await screen.findByRole("heading", { name: /Payment Voucher/ });
    ctrlK();
    const box = await screen.findByTestId("agent-instruction");
    fireEvent.keyDown(box, { key: "Escape" });
    await waitFor(() => expect(screen.queryByTestId("agent-panel")).toBeNull());
    expect(screen.getByRole("heading", { name: /Payment Voucher/ })).toBeTruthy();
  });

  it("⛔ keys typed in the panel do not drive the screen underneath", async () => {
    renderShell(createMockClient());
    await screen.findByRole("combobox", { name: /MASTERS/ });
    ctrlK();
    const box = await screen.findByTestId("agent-instruction");
    fireEvent.keyDown(box, { key: "Enter" });
    fireEvent.keyDown(box, { key: "F8" });
    fireEvent.keyDown(box, { key: "a", code: "KeyA", ctrlKey: true });
    expect(screen.getByRole("heading", { name: "Gateway of Tally" })).toBeTruthy();
    // CONTROL: the same F8 with the panel closed DOES open a Sales voucher, so
    // the assertion above is about the panel and not about a dead key.
    fireEvent.keyDown(box, { key: "Escape" });
    await waitFor(() => expect(screen.queryByTestId("agent-panel")).toBeNull());
    fireEvent.keyDown(app(), { key: "F8" });
    expect(await screen.findByRole("heading", { name: /Sales Voucher/ })).toBeTruthy();
  });
});

describe("starting a run", () => {
  it("⛔ with no model configured (the shipped default) it says exactly that", async () => {
    renderShell(createMockClient());
    ctrlK();
    fireEvent.change(await screen.findByTestId("agent-instruction"), {
      target: { value: "Pay the rent" },
    });
    fireEvent.click(screen.getByTestId("agent-start"));
    expect((await screen.findByTestId("agent-panel-message")).textContent).toMatch(
      /No agent model is configured on this server \(LLM_PROVIDER is unset\)/,
    );
  });

  it("sends the instruction, the untrusted input with its provenance, and ONLY the ticked delegation", async () => {
    const base = createMockClient({ agent: { model: true } });
    const sent: StartAgentRunRequest[] = [];
    const client: CoredClient = {
      ...base,
      async startAgentRun(c, req) {
        sent.push(req);
        return base.startAgentRun(c, req);
      },
    };
    renderShell(client);
    ctrlK();
    fireEvent.change(await screen.findByTestId("agent-instruction"), {
      target: { value: "Pay the rent from HDFC" },
    });
    fireEvent.change(screen.getByTestId("agent-input"), {
      target: { value: "NEFT/HDFC/RENT MAY" },
    });
    fireEvent.click(screen.getByRole("checkbox", { name: "create payment" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Maximum amount" }), {
      target: { value: "30000.5" },
    });
    fireEvent.click(screen.getByTestId("agent-start"));
    await screen.findByText(/Run started/);
    expect(sent).toEqual([
      {
        instruction: "Pay the rent from HDFC",
        channel: "panel",
        delegate: ["create payment"],
        inputs: [{ label: "pasted bank", provenance: "bank", text: "NEFT/HDFC/RENT MAY" }],
        max_amount_paise: 3000050,
      },
    ]);
    expect(await screen.findByTestId("agent-run-status")).toBeTruthy();
    expect(screen.getAllByTestId("agent-step").map((s) => s.textContent)).toContain("Run started");
  });

  it("refuses a maximum that is not rupees before any request", async () => {
    const base = createMockClient({ agent: { model: true } });
    let calls = 0;
    renderShell({ ...base, async startAgentRun(c, r) { calls++; return base.startAgentRun(c, r); } });
    ctrlK();
    fireEvent.change(await screen.findByTestId("agent-instruction"), { target: { value: "x" } });
    fireEvent.change(screen.getByRole("textbox", { name: "Maximum amount" }), {
      target: { value: "25k" },
    });
    fireEvent.click(screen.getByTestId("agent-start"));
    expect((await screen.findByTestId("agent-panel-message")).textContent).toMatch(/must be rupees/);
    expect(calls).toBe(0);
  });
});

describe("watching a run and opening what it waits on", () => {
  async function parked(): Promise<CoredClient> {
    const client = createMockClient({
      agent: { runs: [parkedRun()], events: { [RUN_ID]: parkedRunEvents() } },
    });
    const rent = await client.createLedger("company-demo", {
      name: "Office Rent",
      group_name: "Indirect Expenses",
    });
    mockAddAgentApproval(client, paymentApproval({ officeRentID: rent.ledger_id }));
    return client;
  }

  it("lists the run's steps from the event stream and shows the approval card", async () => {
    renderShell(await parked());
    ctrlK();
    const steps = await screen.findAllByTestId("agent-step");
    await waitFor(() => expect(screen.getAllByTestId("agent-step")).toHaveLength(4));
    expect(steps[0]?.textContent).toBe("Run started");
    expect(screen.getAllByTestId("agent-step").map((s) => s.textContent)).toContain(
      "Previewed post.vouchers.payment: Dr 25000.00 / Cr 25000.00",
    );
    const card = await screen.findByTestId("agent-approval-card");
    expect(within(card).getByTestId("proposal-reasons").textContent).toMatch(/L1/);
    expect(within(card).getAllByTestId("ledger-effect-row")).toHaveLength(2);
    // ⛔ A CARD, NOT A FORM: nothing in the panel is an editable amount.
    expect(within(screen.getByTestId("agent-panel")).queryByTestId("single-amount:0")).toBeNull();
  });

  it("opens the proposal in the NATIVE F5 Payment screen and closes itself", async () => {
    renderShell(await parked());
    ctrlK();
    fireEvent.click(await screen.findByTestId("agent-open-proposal"));
    await waitFor(() => expect(screen.queryByTestId("agent-panel")).toBeNull());
    expect(await screen.findByTestId("agent-proposal")).toBeTruthy();
    expect(screen.getByRole("heading", { name: /Payment Voucher/ })).toBeTruthy();
    await waitFor(() =>
      expect((screen.getByTestId("single-amount:0") as HTMLInputElement).value).toBe("25000.0000"),
    );
  });

  it("⛔ refuses to open a proposal over a voucher being keyed, and says why", async () => {
    renderShell(await parked());
    fireEvent.keyDown(app(), { key: "F7" });
    await screen.findByRole("heading", { name: /Journal Voucher/ });
    ctrlK();
    fireEvent.click(await screen.findByTestId("agent-open-proposal"));
    expect((await screen.findByTestId("agent-panel-message")).textContent).toMatch(
      /Leave the form you are keying first/,
    );
    expect(screen.getByRole("heading", { name: /Journal Voucher/ })).toBeTruthy();
    expect(screen.queryByTestId("agent-proposal")).toBeNull();
  });

  it("an invoice-class proposal is sent to the inbox, not improvised into a form", async () => {
    const client = createMockClient({
      agent: { runs: [parkedRun()], events: { [RUN_ID]: [] }, approvals: [salesApproval()] },
    });
    renderShell(client);
    ctrlK();
    expect((await screen.findByTestId("agent-card-inbox-only")).textContent).toMatch(
      /As Voucher" is not built.*Approval Inbox/,
    );
    expect(screen.queryByTestId("agent-open-proposal")).toBeNull();
  });

  it("cancels a run that is still going", async () => {
    renderShell(await parked());
    ctrlK();
    fireEvent.click(await screen.findByTestId("agent-cancel"));
    expect((await screen.findByTestId("agent-panel-message")).textContent).toMatch(
      /nothing it parked will ever commit/,
    );
    await waitFor(() => expect(screen.getByTestId("agent-run-status").textContent).toBe("cancelled"));
  });
});
