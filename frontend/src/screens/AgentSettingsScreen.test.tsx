import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

import { createMockClient, type CoredClient } from "../api";
import { mockBlockLedger } from "../api/mock";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";

/**
 * AGENT SETTINGS, DRIVEN THROUGH THE REAL SHELL (W2 breadth): the Gateway and
 * Alt+G doors, the autonomy table, the release of an agent-created ledger, and
 * the intent log's verification. Every write is asserted by what the client
 * was asked, and every refusal by the server's own sentence on screen.
 */

afterEach(() => cleanup());

const COMPANY = { id: "company-demo", name: "Demo Traders", homeStateCode: "29" };

function renderShell(client: CoredClient) {
  return render(
    <CompanyProvider client={client} initialCompany={COMPANY}>
      <AppShell />
    </CompanyProvider>,
  );
}

async function openFromGateway(): Promise<void> {
  const gw = await screen.findByRole("combobox", { name: /MASTERS/ });
  fireEvent.change(gw, { target: { value: "Agent Settings" } });
  fireEvent.keyDown(gw, { key: "Enter" });
  await screen.findByRole("heading", { name: "Agent Settings" });
}

describe("Agent Settings", () => {
  it("the Gateway entry opens it, and Alt+G reaches it too", async () => {
    renderShell(createMockClient());
    await openFromGateway();
    cleanup();
    renderShell(createMockClient());
    fireEvent.keyDown(screen.getByRole("application"), { key: "©", code: "KeyG", altKey: true });
    const input = await screen.findByRole("combobox");
    fireEvent.change(input, { target: { value: "Agent Settings" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(await screen.findByRole("heading", { name: "Agent Settings" })).toBeTruthy();
  });

  it("shows the default when no row exists, and saves a level with an exact paise band", async () => {
    const client = createMockClient();
    renderShell(client);
    await openFromGateway();
    expect(await screen.findByTestId("agent-autonomy-empty")).toBeTruthy();
    expect(screen.getByTestId("agent-autonomy-note").textContent).toMatch(/untrusted input/);
    fireEvent.change(screen.getByLabelText("Action"), { target: { value: "create payment" } });
    fireEvent.change(screen.getByLabelText("Level"), { target: { value: "2" } });
    fireEvent.change(screen.getByLabelText("Amount band"), { target: { value: "5,000.50" } });
    fireEvent.click(screen.getByTestId("agent-autonomy-save"));
    const row = await screen.findByTestId("agent-autonomy-row");
    expect(row.textContent).toContain("create payment");
    expect(row.textContent).toContain("Rs 5000.50");
    expect(row.textContent).toContain("L2");
    const res = await client.listAgentAutonomy("company-demo");
    // Rupees to paise EXACTLY — no float on the way.
    expect(res.rows[0]?.amount_max_paise).toBe(500050);
  });

  it("refuses a malformed band before asking the server", async () => {
    const client = createMockClient();
    renderShell(client);
    await openFromGateway();
    await screen.findByTestId("agent-autonomy-empty");
    fireEvent.change(screen.getByLabelText("Amount band"), { target: { value: "5000.555" } });
    fireEvent.click(screen.getByTestId("agent-autonomy-save"));
    expect((await screen.findByTestId("agent-settings-notice")).textContent).toMatch(/must be rupees/);
    expect((await client.listAgentAutonomy("company-demo")).rows).toHaveLength(0);
  });

  it("shows the server's refusal to a non-Owner instead of the table", async () => {
    renderShell(createMockClient({ agent: { owner: false } }));
    await openFromGateway();
    expect((await screen.findByTestId("agent-autonomy-error")).textContent).toMatch(/Only an Owner/);
    expect(screen.queryByTestId("agent-autonomy-save")).toBeNull();
  });

  it("lists an agent-created ledger as blocked, with its bank details, and releases it", async () => {
    const client = createMockClient();
    const { ledgers } = await client.listLedgers("company-demo");
    const target = ledgers.find((l) => !l.is_system);
    if (!target) throw new Error("the demo company has no ordinary ledger");
    mockBlockLedger(client, "company-demo", target.id, "created by the agent in run R-1; a person must review it");
    renderShell(client);
    await openFromGateway();
    const row = await screen.findByTestId("agent-blocked-row");
    expect(row.textContent).toContain(target.name);
    expect(row.textContent).toContain("run R-1");
    fireEvent.click(within(row).getByTestId("agent-unblock"));
    await waitFor(() => expect(screen.getByTestId("agent-settings-notice").textContent).toMatch(/is unblocked/));
    expect(await screen.findByTestId("agent-blocked-empty")).toBeTruthy();
    const after = (await client.listLedgers("company-demo")).ledgers.find((l) => l.id === target.id);
    expect(after?.blocked).toBe(false);
  });

  it("verifies the intent log on demand", async () => {
    renderShell(createMockClient());
    await openFromGateway();
    fireEvent.click(await screen.findByTestId("agent-verify-anchors"));
    expect((await screen.findByTestId("agent-verify-result")).textContent).toMatch(/Intact/);
  });
});
