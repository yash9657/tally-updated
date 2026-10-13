import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

import { ApiError, createMockClient, type AgentApproval, type CoredClient } from "../api";
import { mockAgentDecisions } from "../api/mock";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { alterApproval, blindApproval, masterApproval, paymentApproval, salesApproval } from "./agentFixtures";

/**
 * THE APPROVAL INBOX, DRIVEN THROUGH THE REAL SHELL (W2).
 *
 * ⛔ THE SHELL AND NOT THE SCREEN, for the reason offlineQueueing.test.tsx gives:
 * Ctrl+A, Alt+X and Alt+B reach this screen only through AppShell's
 * SCREEN_ACTIONS map and the keymap's screen-scoped bindings, and a key missing
 * from either is dead with every screen-level test green (the Alt+O lesson,
 * which this repository has now paid for five times). Every decision here is
 * made by a KEY on the document, and asserted by what the server was sent.
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

async function openInboxFromGateway(): Promise<void> {
  const gw = await screen.findByRole("combobox", { name: /MASTERS/ });
  fireEvent.change(gw, { target: { value: "Approval" } });
  fireEvent.keyDown(gw, { key: "Enter" });
  await screen.findByRole("heading", { name: "Approval Inbox" });
}

function app(): Element {
  return screen.getByRole("application");
}

const ctrlA = () => fireEvent.keyDown(app(), { key: "a", code: "KeyA", ctrlKey: true });
// ⚠️ macOS COMPOSES Option+X INTO "≈" and Option+B INTO "∫". Combos resolve on
// `code`, so the events carry the composed character AND the code, exactly as a
// Mac sends them (HANDOFF rule 6: never press("Alt+x")).
const altX = () => fireEvent.keyDown(app(), { key: "≈", code: "KeyX", altKey: true });
const altB = () => fireEvent.keyDown(app(), { key: "∫", code: "KeyB", altKey: true });

function clientWith(approvals: AgentApproval[]): CoredClient {
  return createMockClient({ agent: { approvals } });
}

describe("Approval Inbox — the doors", () => {
  it("the Gateway entry opens it, with the chrome titled and the decision keys on the bar", async () => {
    renderShell(clientWith([paymentApproval()]));
    await openInboxFromGateway();
    expect(await screen.findAllByTestId("inbox-row")).toHaveLength(1);
    // The bar names the decisions as what they do: "Approve", never "Accept".
    expect(screen.getByRole("button", { name: "Ctrl+A Approve" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Alt+X Reject" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Alt+B Ask back" })).toBeTruthy();
    expect(screen.getByText("Approval Inbox", { selector: "h1" })).toBeTruthy();
  });

  it("Alt+G Go To reaches it too", async () => {
    renderShell(clientWith([paymentApproval()]));
    fireEvent.keyDown(app(), { key: "©", code: "KeyG", altKey: true });
    const input = await screen.findByRole("combobox");
    fireEvent.change(input, { target: { value: "Approval Inbox" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(await screen.findByRole("heading", { name: "Approval Inbox" })).toBeTruthy();
  });

  it("says so when nothing is waiting", async () => {
    renderShell(clientWith([]));
    await openInboxFromGateway();
    expect(await screen.findByTestId("inbox-empty")).toBeTruthy();
  });

  it("⛔ a 403 on the list says which authority is missing", async () => {
    const base = createMockClient();
    renderShell({
      ...base,
      async listAgentApprovals() {
        throw new ApiError(403, "forbidden");
      },
    });
    await openInboxFromGateway();
    expect((await screen.findByTestId("inbox-error")).textContent).toMatch(/display agent-approval/);
  });
});

describe("Approval Inbox — what the operator sees before deciding", () => {
  it("renders the reasons VERBATIM, the provenance warning, and the server's ledger effect", async () => {
    renderShell(clientWith([paymentApproval()]));
    await openInboxFromGateway();
    const reasons = await screen.findByTestId("proposal-reasons");
    expect(reasons.textContent).toContain(
      "autonomy for create payment is L1: every commit waits for a human",
    );
    expect(screen.getByTestId("proposal-provenance-field").textContent).toMatch(
      /narration was copied verbatim from bank input/,
    );
    expect(screen.getByTestId("proposal-tainted").textContent).toMatch(/bank/);
    const rows = screen.getAllByTestId("ledger-effect-row").map((r) => r.textContent);
    expect(rows).toEqual(["Office Rent25000.00", "HDFC Bank25000.00"]);
    expect(screen.getByTestId("ledger-effect-total").textContent).toBe("Total25000.0025000.00");
    expect(screen.getByTestId("inbox-open-hint").textContent).toMatch(/Enter: open in Payment \(single entry\)/);
  });

  it("↑/↓ moves the highlight, and the diff below follows it", async () => {
    renderShell(clientWith([paymentApproval(), salesApproval()]));
    await openInboxFromGateway();
    await screen.findAllByTestId("inbox-row");
    expect(screen.getByTestId("proposal-instruction").textContent).toMatch(/office rent/);
    fireEvent.keyDown(screen.getByTestId("inbox-list"), { key: "ArrowDown" });
    await waitFor(() =>
      expect(screen.getByTestId("proposal-instruction").textContent).toMatch(/Acme Retail/),
    );
    expect(screen.getAllByTestId("ledger-effect-row")[0]?.textContent).toBe("Acme Traders10000.00");
  });

  it("⛔ a SALES proposal says why it cannot be opened, and Enter does not leave the inbox", async () => {
    renderShell(clientWith([salesApproval()]));
    await openInboxFromGateway();
    const hint = await screen.findByTestId("inbox-open-hint");
    expect(hint.textContent).toMatch(/As Voucher" is not built/);
    fireEvent.keyDown(app(), { key: "Enter" });
    // Still the inbox: no voucher frame was pushed for a body the GST form
    // would re-derive.
    expect(screen.getByRole("heading", { name: "Approval Inbox" })).toBeTruthy();
    expect(screen.queryByTestId("agent-proposal")).toBeNull();
  });
});

describe("Approval Inbox — the decisions, by key", () => {
  it("Ctrl+A approves AS PREVIEWED (no edited body) and the row leaves the list", async () => {
    const client = clientWith([paymentApproval()]);
    renderShell(client);
    await openInboxFromGateway();
    await screen.findByTestId("ledger-effect");
    ctrlA();
    await waitFor(() => expect(mockAgentDecisions(client)).toHaveLength(1));
    expect(mockAgentDecisions(client)[0]).toEqual({ approvalID: "appr-pay-1", decision: "approve" });
    expect((await screen.findByTestId("inbox-notice")).textContent).toMatch(
      /Approved as previewed.*nothing is posted by this screen/,
    );
    expect(await screen.findByTestId("inbox-empty")).toBeTruthy();
  });

  it("⛔⛔ Ctrl+A on a proposal with NO PREVIEW is refused before any request — the diff is mandatory", async () => {
    const client = clientWith([blindApproval()]);
    renderShell(client);
    await openInboxFromGateway();
    expect(await screen.findByTestId("ledger-effect-missing")).toBeTruthy();
    ctrlA();
    await waitFor(() =>
      expect(screen.getByText(/has no preview to show, so it cannot be approved/)).toBeTruthy(),
    );
    expect(mockAgentDecisions(client)).toHaveLength(0);
    // CONTROL: the same proposal can still be REJECTED.
    altX();
    await waitFor(() => expect(mockAgentDecisions(client)).toHaveLength(1));
    expect(mockAgentDecisions(client)[0]?.decision).toBe("reject");
  });

  it("Alt+X rejects, carrying the note when one is typed", async () => {
    const client = clientWith([paymentApproval()]);
    renderShell(client);
    await openInboxFromGateway();
    fireEvent.change(await screen.findByTestId("inbox-note"), {
      target: { value: "Rent is paid by the landlord's agent this month" },
    });
    altX();
    await waitFor(() => expect(mockAgentDecisions(client)).toHaveLength(1));
    expect(mockAgentDecisions(client)[0]).toEqual({
      approvalID: "appr-pay-1",
      decision: "reject",
      note: "Rent is paid by the landlord's agent this month",
    });
  });

  it("Alt+B with no note is refused HERE; with one it asks the agent back", async () => {
    const client = clientWith([paymentApproval()]);
    renderShell(client);
    await openInboxFromGateway();
    await screen.findByTestId("inbox-note");
    altB();
    await waitFor(() => expect(screen.getByText(/Ask back needs a question/)).toBeTruthy());
    expect(mockAgentDecisions(client)).toHaveLength(0);
    fireEvent.change(screen.getByTestId("inbox-note"), { target: { value: "Which month is this for?" } });
    altB();
    await waitFor(() => expect(mockAgentDecisions(client)).toHaveLength(1));
    expect(mockAgentDecisions(client)[0]).toEqual({
      approvalID: "appr-pay-1",
      decision: "ask_back",
      note: "Which month is this for?",
    });
  });

  it("⛔ Enter typed in the NOTE does not drill into the voucher", async () => {
    renderShell(clientWith([paymentApproval()]));
    await openInboxFromGateway();
    const note = await screen.findByTestId("inbox-note");
    fireEvent.change(note, { target: { value: "hm" } });
    fireEvent.keyDown(note, { key: "Enter" });
    expect(screen.getByRole("heading", { name: "Approval Inbox" })).toBeTruthy();
    expect(screen.queryByTestId("agent-proposal")).toBeNull();
  });

  it("a proposal decided elsewhere says so (409), and the list is re-read", async () => {
    const base = clientWith([paymentApproval()]);
    let lists = 0;
    renderShell({
      ...base,
      async listAgentApprovals(c, q) {
        lists++;
        return base.listAgentApprovals(c, q);
      },
      async decideAgentApproval() {
        throw new ApiError(409, "this approval has already been decided", {
          code: "ALREADY_DECIDED",
          approval: { ...paymentApproval(), state: "rejected" },
        });
      },
    });
    await openInboxFromGateway();
    await screen.findByTestId("ledger-effect");
    const before = lists;
    ctrlA();
    expect((await screen.findByTestId("inbox-notice")).textContent).toMatch(
      /already decided \(rejected\)/,
    );
    await waitFor(() => expect(lists).toBeGreaterThan(before));
  });

  it("⛔ an approve RE-READS the proposal and refuses one whose preview is no longer the one on screen", async () => {
    const base = clientWith([paymentApproval()]);
    const changed = paymentApproval();
    changed.preview!.effect!.postings[0]!.amount_paise = 9900000;
    renderShell({
      ...base,
      async getAgentApproval() {
        return { approval: changed };
      },
    });
    await openInboxFromGateway();
    await screen.findByTestId("ledger-effect");
    ctrlA();
    expect((await screen.findByTestId("inbox-notice")).textContent).toMatch(
      /preview changed since it was shown; nothing was approved/,
    );
    expect(mockAgentDecisions(base)).toHaveLength(0);
  });

  it("an approval past its 72-hour window is recorded EXPIRED and says nothing will commit", async () => {
    const client = clientWith([
      paymentApproval({ officeRentID: "ledger-rent", deadline: "2000-01-01T00:00:00Z" }),
    ]);
    renderShell(client);
    await openInboxFromGateway();
    await screen.findByTestId("ledger-effect");
    ctrlA();
    expect((await screen.findByTestId("inbox-notice")).textContent).toMatch(
      /recorded as EXPIRED\. Nothing will be committed/,
    );
  });
});

describe("Approval Inbox — W2 breadth: diffs that are not ledger legs", () => {
  it("a master create shows the master's audit row, its bank details LOUDLY, the block — and can be approved", async () => {
    const client = clientWith([masterApproval()]);
    renderShell(client);
    await openInboxFromGateway();
    const m = await screen.findByTestId("effect-master");
    expect(m.textContent).toMatch(/Creates ledger/);
    expect(m.textContent).toMatch(/born BLOCKED/);
    expect(m.textContent).toContain("bank account number: 50100123456789");
    expect(screen.queryByTestId("ledger-effect-missing")).toBeNull();
    ctrlA();
    await waitFor(() => expect(mockAgentDecisions(client)).toHaveLength(1));
    expect(mockAgentDecisions(client)[0]).toEqual({ approvalID: "appr-master-1", decision: "approve" });
  });

  it("an alteration shows what it replaces and the net change per ledger", async () => {
    renderShell(clientWith([alterApproval()]));
    await openInboxFromGateway();
    expect((await screen.findByTestId("effect-replaces")).textContent).toMatch(/Replaces version 1/);
    const delta = screen.getAllByTestId("effect-delta-row").map((r) => r.textContent);
    expect(delta).toEqual(["HDFC Bank-25000.00-26000.00-1000.00", "Office Rent25000.0026000.001000.00"]);
  });
});

