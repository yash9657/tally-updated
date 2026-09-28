import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

import { ApiError, createMockClient, type CoredClient, type VoucherRequest } from "../api";
import { mockAddAgentApproval, mockAgentDecisions } from "../api/mock";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { paymentApproval } from "./agentFixtures";

/**
 * AN AGENT PROPOSAL IN THE NATIVE F5 PAYMENT SCREEN (W2) — the whole of "the
 * panel pre-fills the same native screens so Ctrl+A remains the accept key".
 *
 * ⛔⛔ THE ASSERTION THAT MATTERS MOST IN THIS FILE IS A NEGATIVE ONE: in proposal
 * mode, Ctrl+A POSTS NOTHING. Every test counts `postPaymentVoucher` calls. A
 * proposal posted directly by the form leaves its approval PENDING, and the next
 * approval anywhere puts the same payment on the books a second time — once by
 * the human, once by the agent's commit — with nothing on either saying so.
 *
 * Driven through the real shell (Gateway > Approval Inbox > Enter), because the
 * prefill depends on three things no screen test can see together: the
 * LoadedProposal provider in AppShell, the machine's proposalFrame binding the
 * frame to the approval, and VoucherScreen honouring that binding.
 */

afterEach(() => cleanup());

const COMPANY = { id: "company-demo", name: "Demo Traders", homeStateCode: "29" };

interface Spied {
  client: CoredClient;
  base: CoredClient;
  posts: VoucherRequest[];
}

async function setup(decide?: CoredClient["decideAgentApproval"]): Promise<Spied> {
  // The seeded demo company has HDFC Bank; the proposal pays an expense ledger
  // that exists in these books, created here so its id is real.
  const seeded = createMockClient();
  const rent = await seeded.createLedger("company-demo", {
    name: "Office Rent",
    group_name: "Indirect Expenses",
  });
  mockAddAgentApproval(seeded, paymentApproval({ officeRentID: rent.ledger_id }));
  const posts: VoucherRequest[] = [];
  const client: CoredClient = {
    ...seeded,
    async postPaymentVoucher(c, req) {
      posts.push(req);
      return seeded.postPaymentVoucher(c, req);
    },
    ...(decide ? { decideAgentApproval: decide } : {}),
  };
  return { client, base: seeded, posts };
}

function renderShell(client: CoredClient) {
  return render(
    <CompanyProvider client={client} initialCompany={COMPANY}>
      <AppShell />
    </CompanyProvider>,
  );
}

const app = () => screen.getByRole("application");
const ctrlA = () => fireEvent.keyDown(app(), { key: "a", code: "KeyA", ctrlKey: true });

async function openProposal(): Promise<void> {
  const gw = await screen.findByRole("combobox", { name: /MASTERS/ });
  fireEvent.change(gw, { target: { value: "Approval" } });
  fireEvent.keyDown(gw, { key: "Enter" });
  await screen.findByTestId("ledger-effect");
  fireEvent.keyDown(app(), { key: "Enter" });
  await screen.findByTestId("agent-proposal");
}

describe("an agent proposal opened in F5 Payment", () => {
  it("prefills the ORDINARY single-entry form from the server's preview", async () => {
    const { client } = await setup();
    renderShell(client);
    await openProposal();
    expect(screen.getByRole("heading", { name: /Payment Voucher/ })).toBeTruthy();
    await waitFor(() =>
      expect((screen.getByRole("combobox", { name: "Account" }) as HTMLInputElement).value).toBe(
        "HDFC Bank",
      ),
    );
    expect((screen.getByTestId("single-ledger:0") as HTMLInputElement).value).toBe("Office Rent");
    expect((screen.getByTestId("single-amount:0") as HTMLInputElement).value).toBe("25000.0000");
    expect((screen.getByTestId("voucher-narration") as HTMLInputElement).value).toBe("May office rent");
    // The reasons and the diff are above the form, and the form is the native one.
    expect(screen.getByTestId("proposal-reasons").textContent).toContain("L1");
    expect(screen.getByTestId("single-entry-panel")).toBeTruthy();
    expect(screen.getByTestId("proposal-accept-hint").textContent).toMatch(/exactly as previewed/);
  });

  it("⛔⛔ Ctrl+A with no edit APPROVES (no edited body) and posts NO voucher", async () => {
    const { client, base, posts } = await setup();
    renderShell(client);
    await openProposal();
    await waitFor(() =>
      expect((screen.getByTestId("single-amount:0") as HTMLInputElement).value).toBe("25000.0000"),
    );
    ctrlA();
    await waitFor(() => expect(mockAgentDecisions(base)).toHaveLength(1));
    expect(mockAgentDecisions(base)[0]).toEqual({ approvalID: "appr-pay-1", decision: "approve" });
    expect(posts).toHaveLength(0);
    expect((await screen.findByText(/Approved as previewed/)).textContent).toMatch(
      /Nothing is posted by this screen/,
    );
    // The machine did NOT move: still the payment screen, now cleared and no
    // longer in proposal mode.
    await waitFor(() => expect(screen.queryByTestId("agent-proposal")).toBeNull());
    expect((screen.getByTestId("single-amount:0") as HTMLInputElement).value).toBe("");
  });

  it("an EDIT in the native form is approved as the edit — with the form's own body", async () => {
    const { client, base, posts } = await setup();
    renderShell(client);
    await openProposal();
    const amount = await screen.findByTestId("single-amount:0");
    await waitFor(() => expect((amount as HTMLInputElement).value).toBe("25000.0000"));
    fireEvent.change(amount, { target: { value: "24000" } });
    expect(screen.getByTestId("proposal-accept-hint").textContent).toMatch(/approves YOUR version/);
    ctrlA();
    await waitFor(() => expect(mockAgentDecisions(base)).toHaveLength(1));
    const d = mockAgentDecisions(base)[0]!;
    expect(d.decision).toBe("approve");
    const body = d.edited_body as unknown as VoucherRequest;
    // Single entry put the SUM on the bank leg and the edit on the particular —
    // the form's own builder, not a copy of the agent's body with a number swapped.
    const lines = (body.postings ?? []).map((p) => `${p.dr_cr} ${p.amount}`).sort();
    expect(lines).toEqual(["CR 24000.0000", "DR 24000.0000"]);
    expect(body.voucher_date).toBe("2026-05-10");
    expect(posts).toHaveLength(0);
  });

  it("⛔ a REFUSED decision keeps proposal mode — the next Ctrl+A cannot post the figures", async () => {
    let calls = 0;
    const { client, posts } = await setup(async () => {
      calls++;
      throw new ApiError(403, "you may not approve an action you may not take yourself: denied");
    });
    renderShell(client);
    await openProposal();
    await waitFor(() =>
      expect((screen.getByTestId("single-amount:0") as HTMLInputElement).value).toBe("25000.0000"),
    );
    ctrlA();
    expect(await screen.findByText(/refused \(403\): you may not approve/)).toBeTruthy();
    expect(screen.getByTestId("agent-proposal")).toBeTruthy();
    ctrlA();
    await waitFor(() => expect(calls).toBe(2));
    expect(posts).toHaveLength(0);
  });

  it("⛔ Esc then a plain F5 is a plain F5 — the proposal does not follow the operator", async () => {
    const { client } = await setup();
    renderShell(client);
    await openProposal();
    fireEvent.keyDown(app(), { key: "Escape" });
    await screen.findByRole("heading", { name: "Approval Inbox" });
    fireEvent.keyDown(app(), { key: "Escape" });
    await screen.findByRole("combobox", { name: /MASTERS/ });
    fireEvent.keyDown(app(), { key: "F5" });
    await screen.findByRole("heading", { name: /Payment Voucher/ });
    expect(screen.queryByTestId("agent-proposal")).toBeNull();
    expect((screen.getByTestId("single-amount:0") as HTMLInputElement).value).toBe("");
  });

  it("Alt+X on the proposal sets it aside WITHOUT deciding it", async () => {
    const { client, base, posts } = await setup();
    renderShell(client);
    await openProposal();
    fireEvent.keyDown(app(), { key: "≈", code: "KeyX", altKey: true });
    await waitFor(() => expect(screen.queryByTestId("agent-proposal")).toBeNull());
    expect(mockAgentDecisions(base)).toHaveLength(0);
    expect(posts).toHaveLength(0);
  });
});
