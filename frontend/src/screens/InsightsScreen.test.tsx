import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

import { ApiError, createMockClient, type CoredClient, type Insight } from "../api";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";

/**
 * THE INSIGHTS INBOX, DRIVEN THROUGH THE REAL SHELL (W4, d125). Every verdict
 * is a KEY on the focused list and is asserted by what the server recorded;
 * the drill is Tab, ↓ and the shell's own Enter, asserted by the voucher
 * screen that opens.
 */

afterEach(() => cleanup());

const COMPANY = { id: "company-demo", name: "Demo Traders", homeStateCode: "29" };

function insight(over: Partial<Insight> = {}): Insight {
  return {
    id: "ins-1",
    type: "credit_limit_breach",
    title: "Credit limit crossed",
    action: "Open the party's ledger; hold further credit sales or raise the limit.",
    cadence: "realtime",
    entity_kind: "party",
    entity_key: "l-1",
    entity_name: "Ganga Stores",
    fingerprint: "credit_limit_breach:l-1",
    as_of: "2026-09-28",
    detected_at: "2026-09-28T10:00:00Z",
    impact_inr: "59384.0000",
    actionability: "1",
    payload: { party: "Ganga Stores", voucher_no: "SAL-7", credit_limit: "10000.00", balance: "69384.00", excess: "59384.00" },
    fields: [
      { key: "credit_limit", label: "credit limit", unit: "inr", value: "10000.00" },
      { key: "excess", label: "amount over the limit", unit: "inr", value: "59384.00" },
    ],
    summary: "Ganga Stores now owes Rs 69384.00 against a credit limit of Rs 10000.00 — Rs 59384.00 over, after voucher SAL-7.",
    evidence_sql: "SELECT 1",
    evidence_args: ["l-1"],
    voucher_ids: [],
    trigger_voucher_id: null,
    state: "open",
    verdicts: {},
    ...over,
  };
}

function renderShell(client: CoredClient) {
  return render(
    <CompanyProvider client={client} initialCompany={COMPANY}>
      <AppShell />
    </CompanyProvider>,
  );
}

async function openFromGateway(): Promise<void> {
  const gw = await screen.findByRole("combobox", { name: /MASTERS/ });
  fireEvent.change(gw, { target: { value: "Insights" } });
  fireEvent.keyDown(gw, { key: "Enter" });
  await screen.findByRole("heading", { name: "Insights" });
}

describe("Insights inbox", () => {
  it("the Gateway opens it and every figure is the server's string", async () => {
    renderShell(createMockClient({ insights: [insight()] }));
    await openFromGateway();
    expect(await screen.findAllByTestId("insights-row")).toHaveLength(1);
    expect(screen.getByTestId("insight-field-excess").textContent).toBe("59384.00");
    expect(screen.getByTestId("insights-detail").textContent).toContain("Rs 59384.00 over");
  });

  it("Alt+G reaches it too", async () => {
    renderShell(createMockClient({ insights: [insight()] }));
    fireEvent.keyDown(screen.getByRole("application"), { key: "©", code: "KeyG", altKey: true });
    const input = await screen.findByRole("combobox");
    fireEvent.change(input, { target: { value: "Insights" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(await screen.findByRole("heading", { name: "Insights" })).toBeTruthy();
  });

  it("U records useful and keeps it open; W records wrong and dismisses it", async () => {
    const client = createMockClient({ insights: [insight(), insight({ id: "ins-2", entity_name: "Kaveri", fingerprint: "b" })] });
    renderShell(client);
    await openFromGateway();
    const list = await screen.findByTestId("insights-list");
    fireEvent.keyDown(list, { key: "u" });
    await waitFor(() => expect(screen.getByTestId("insights-notice").textContent).toContain("Marked useful"));
    expect((await client.getInsight("company-demo", "ins-1")).verdicts.useful).toBe(1);
    fireEvent.keyDown(screen.getByTestId("insights-list"), { key: "w" });
    await waitFor(() => expect(screen.getByTestId("insights-notice").textContent).toContain("wrong and dismissed"));
    expect((await client.getInsight("company-demo", "ins-1")).state).toBe("dismissed");
    await waitFor(() => expect(screen.getAllByTestId("insights-row")).toHaveLength(1));
  });

  it("S refuses without a reason, then suppresses the type; L lifts it", async () => {
    const client = createMockClient({ insights: [insight()] });
    renderShell(client);
    await openFromGateway();
    fireEvent.keyDown(await screen.findByTestId("insights-list"), { key: "s" });
    expect(screen.getByTestId("insights-notice").textContent).toContain("needs a reason");
    fireEvent.change(screen.getByTestId("insights-note"), { target: { value: "limits under review" } });
    fireEvent.keyDown(screen.getByTestId("insights-list"), { key: "s" });
    await waitFor(async () =>
      expect((await client.listInsightSuppressions("company-demo")).suppressions).toHaveLength(1),
    );
    await screen.findByTestId("insights-suppressions");
    fireEvent.keyDown(screen.getByTestId("insights-list"), { key: "l" });
    await waitFor(async () =>
      expect((await client.listInsightSuppressions("company-demo")).suppressions).toHaveLength(0),
    );
  });

  it("D composes the digest and says it is the fixed template", async () => {
    renderShell(createMockClient({ insights: [insight()] }));
    await openFromGateway();
    fireEvent.keyDown(await screen.findByTestId("insights-list"), { key: "d" });
    const digest = await screen.findByTestId("insights-digest");
    expect(digest.textContent).toContain("fixed template");
    expect(screen.getAllByTestId("digest-item")).toHaveLength(1);
  });

  it("V re-runs the evidence and shows the stored query", async () => {
    renderShell(createMockClient({ insights: [insight()] }));
    await openFromGateway();
    fireEvent.keyDown(await screen.findByTestId("insights-list"), { key: "v" });
    expect((await screen.findByTestId("insights-evidence")).textContent).toContain("to the paisa");
    expect(screen.getByTestId("insights-evidence-sql").textContent).toContain("SELECT 1");
  });

  it("Tab then Enter drills into a voucher behind the insight", async () => {
    // The insight's voucher is a real (mock) voucher, posted first, so the
    // drill reads it by id and finds its Day Book row exactly as the server
    // path would.
    const probe = createMockClient({});
    const led1 = (await probe.createLedger("company-demo", { name: "Ganga Stores", group_name: "Sundry Debtors" })).ledger_id;
    const led2 = (await probe.createLedger("company-demo", { name: "Sales A", group_name: "Sales Accounts" })).ledger_id;
    const posted = await probe.postSalesVoucher("company-demo", {
      voucher_date: "2026-04-10",
      created_by: "asha",
      postings: [
        { ledger_id: led1, dr_cr: "D", amount: "69384.0000" },
        { ledger_id: led2, dr_cr: "C", amount: "69384.0000" },
      ],
    });
    const seeded: CoredClient = {
      ...probe,
      ...createMockClient({ insights: [insight({ voucher_ids: [posted.voucher_id] })] }),
      getVoucher: probe.getVoucher.bind(probe),
      getDayBook: probe.getDayBook.bind(probe),
    };
    renderShell(seeded);
    await openFromGateway();
    const vlist = await screen.findByTestId("insights-vouchers");
    fireEvent.keyDown(screen.getByTestId("insights-list"), { key: "Tab" });
    expect(document.activeElement).toBe(vlist);
    // The highlight publishes the Day Book row; the shell's Enter drills it.
    await new Promise((r) => setTimeout(r, 50));
    fireEvent.keyDown(screen.getByRole("application"), { key: "Enter" });
    await waitFor(() => expect(document.body.textContent).toContain("Alteration"));
  });

  it("⛔ a 403 says which authority is missing", async () => {
    const base = createMockClient();
    renderShell({
      ...base,
      async listInsights() {
        throw new ApiError(403, "forbidden");
      },
    });
    await openFromGateway();
    expect((await screen.findByTestId("insights-error")).textContent).toContain("display insights");
  });
});
