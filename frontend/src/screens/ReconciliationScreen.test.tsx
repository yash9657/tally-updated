import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createDefaultRegistry } from "../app/defaultScreens";
import { ApiError, createMockClient } from "../api";
import type {
  CoredClient,
  ReconciliationEntry,
  ReconciliationPayloadResponse,
  VoucherView,
} from "../api";

/**
 * ReconciliationScreen.test.tsx — the screen that finally shows an operator why
 * their change was refused (track D).
 *
 * ⛔ THE ROWS THIS SCREEN RENDERS HAVE EXISTED IN PRODUCTION-SHAPED DATA THE
 * WHOLE TIME. The ledger's freeze rule has raised them since migration 0009 and
 * GET /reconciliation-queue has served them since; nothing in this app ever
 * called it. So these tests are not only about track D's offline work — they
 * cover a reader that was missing for a shipped feature.
 */

/** The event a real Mac sends for Option+R: the COMPOSED key plus the code. */
function altMacR(target: Element): void {
  // ⚠️ macOS COMPOSES Option+R INTO "®" — normalize.test.ts pins that mapping for
  // this exact combo. Firing { key: "r", altKey: true } would prove the binding
  // works on machines nobody develops on (HANDOFF rule 6).
  fireEvent.keyDown(target, { key: "®", code: "KeyR", altKey: true });
}

const CONFLICT_ENTRY: ReconciliationEntry = {
  id: "recon-conflict-1",
  voucher_id: "voucher-1",
  attempted_op: "ALTER",
  kind: "CONFLICT",
  ref_value: "node-a",
  frozen_voucher_no: "",
  detail: "stale base version 1; head is at 2",
  created_at: "2026-08-27T09:00:00.000000Z",
};

const FROZEN_ENTRY: ReconciliationEntry = {
  id: "recon-frozen-1",
  voucher_id: "voucher-2",
  attempted_op: "DELETE",
  kind: "IRN",
  ref_value: "IRN-ABC-123",
  frozen_voucher_no: "INV/2026-27/7",
  detail: "DELETE blocked: voucher frozen by IRN IRN-ABC-123 (no INV/2026-27/7)",
  created_at: "2026-08-27T08:00:00.000000Z",
};

const PARKED: ReconciliationPayloadResponse = {
  reconciliation_id: CONFLICT_ENTRY.id,
  voucher_id: "voucher-1",
  attempted_op: "ALTER",
  kind: "CONFLICT",
  ref_value: "node-a",
  frozen_voucher_no: "",
  detail: CONFLICT_ENTRY.detail,
  created_at: CONFLICT_ENTRY.created_at,
  has_payload: true,
  intent: {
    idempotency_key: "node-a:ALTER:voucher-1:2",
    client_voucher_id: "voucher-1",
    op: "ALTER",
    class: "Sales",
    provisional_no: "INV/2025-26/PROV-node-a-3",
    node_id: "node-a",
    base_version: 1,
    hlc: { wall_millis: 1756300000000, counter: 0, node_id: "node-a" },
    payload: {
      class: "Sales",
      voucher_type_id: "",
      party_ledger_id: "ledger-party",
      item_ledger_id: "",
      voucher_no: "",
      voucher_date: "2026-08-20",
      narration: "keyed on the train",
      party: "Acme Traders",
      created_by: "node-a",
      source_guid: "",
      source_draft_id: "",
      postings: [{ ledger_id: "ledger-party", dr_cr: "D", amount: "1180.0000" }],
      bill_allocations: [],
      inventory: [],
      physical_count: [],
      also_carries: ["GST parameters"],
    },
  },
};

/** A freeze row's payload answer: the entry exists and never held one. */
const FROZEN_PAYLOAD: ReconciliationPayloadResponse = {
  reconciliation_id: FROZEN_ENTRY.id,
  voucher_id: "voucher-2",
  attempted_op: "DELETE",
  kind: "IRN",
  ref_value: "IRN-ABC-123",
  frozen_voucher_no: "INV/2026-27/7",
  detail: FROZEN_ENTRY.detail,
  created_at: FROZEN_ENTRY.created_at,
  has_payload: false,
  payload_absent_because:
    "this entry was raised by the freeze rule, which parks no keyed payload; " +
    "the operator's own version exists only on the node that keyed it",
};

const LIVE: VoucherView = {
  voucher_id: "voucher-1",
  voucher_no: "INV/2026-27/42",
  voucher_date: "2026-08-21",
  narration: "the version that won",
  party: "Acme Traders",
  version_no: 2,
  postings: [{ ledger_id: "ledger-party", ledger: "Acme Traders", dr_cr: "D", amount: "2360.0000" }],
} as unknown as VoucherView;

interface Calls {
  resolved: string[];
  queueReads: number;
}

/**
 * A stub client — the pattern TallyImportScreen.test.tsx documents. The mock
 * REFUSES these routes on purpose (it parks nothing, because it refuses no alter
 * or delete), so a test that needs entries injects them.
 */
function stubClient(opts: {
  entries?: ReconciliationEntry[];
  entriesAfterResolve?: ReconciliationEntry[];
  payload?: ReconciliationPayloadResponse | Error;
  live?: VoucherView | Error;
  resolveError?: Error;
}): { client: CoredClient; calls: Calls } {
  const calls: Calls = { resolved: [], queueReads: 0 };
  const base = createMockClient();
  const client: CoredClient = {
    ...base,
    async reconciliationQueue() {
      calls.queueReads += 1;
      const after = opts.entriesAfterResolve;
      if (after && calls.resolved.length > 0) return { entries: after };
      return { entries: opts.entries ?? [] };
    },
    async reconciliationPayload() {
      if (opts.payload instanceof Error) throw opts.payload;
      if (!opts.payload) throw new ApiError(404, "no open reconciliation entry with that id");
      return opts.payload;
    },
    async getVoucher() {
      if (opts.live instanceof Error) throw opts.live;
      if (!opts.live) throw new ApiError(404, "voucher not found");
      return opts.live;
    },
    async resolveReconciliation(_companyID: string, reconID: string) {
      if (opts.resolveError) throw opts.resolveError;
      calls.resolved.push(reconID);
      return { reconciliation_id: reconID, resolved: true };
    },
  };
  return { client, calls };
}

function renderShell(client: CoredClient): Element {
  render(
    <CompanyProvider client={client} initialCompany={{ id: "co-1", name: "Demo Traders" }}>
      <AppShell registry={createDefaultRegistry()} />
    </CompanyProvider>,
  );
  return screen.getByRole("application");
}

async function openScreen(client: CoredClient): Promise<void> {
  const app = renderShell(client);
  altMacR(app);
  await waitFor(() => expect(screen.getByTestId("reconciliation-screen")).toBeTruthy());
}

describe("Alt+R reaches the screen at all", () => {
  it("opens the reconciliation queue on the composed macOS key", async () => {
    // ⛔ THREE REGISTRATION POINTS SHIP THIS KEY COMPLETELY DEAD, with no error
    // and no status line: the keymap binding, the navMachine case and the
    // registry entry. This test is what fails if any of them is missing.
    const { client } = stubClient({ entries: [] });
    await openScreen(client);
    expect(screen.getByTestId("reconciliation-empty")).toBeTruthy();
  });

  it("reads the queue when it opens, rather than showing a stale empty list", async () => {
    const { client, calls } = stubClient({ entries: [CONFLICT_ENTRY] });
    await openScreen(client);
    expect(calls.queueReads).toBeGreaterThan(0);
  });
});

describe("the screen is COMPANY-SCOPED, and the company-less shell survives it", () => {
  /**
   * ⛔⛔ THIS IS THE TEST HANDOFF §0.1 ITEM 2 ASKED TRACK D TO ADD.
   *
   * `COMPANY_SCOPED_SCREENS` in AppShell is consulted ONLY when
   * `company === null`, and every other test here passes an `initialCompany` — so
   * deleting the entry would break nothing observable. Without it the screen
   * renders company-less, `useCompanyID()` THROWS during render, and the whole
   * shell comes down rather than showing a placeholder.
   *
   * `CompanyProvider` does not bootstrap unless asked, so a provider with no
   * `initialCompany` is a deterministic company-less shell.
   */
  it("shows the placeholder rather than throwing when no company is open", () => {
    render(
      <CompanyProvider client={createMockClient()}>
        <AppShell registry={createDefaultRegistry()} />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");
    altMacR(app);

    expect(screen.getByRole("status").textContent).toMatch(/preparing company/i);
    expect(screen.queryByTestId("reconciliation-screen")).toBeNull();
    // The frame is still titled, so the operator can see where they are. Asserted
    // on the chrome's aria-label because the screen deliberately has not rendered.
    expect(screen.getByRole("banner").getAttribute("aria-label")).toContain("Reconciliation");
  });
});

describe("a CONFLICT shows both sides", () => {
  it("renders the live pane and the keyed pane together", async () => {
    const { client } = stubClient({ entries: [CONFLICT_ENTRY], payload: PARKED, live: LIVE });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /compare/i }));

    await waitFor(() => expect(screen.getByTestId("reconciliation-compare")).toBeTruthy());
    const parked = screen.getByTestId("pane-parked").textContent ?? "";
    const live = screen.getByTestId("pane-live").textContent ?? "";

    expect(live).toContain("INV/2026-27/42");
    expect(live).toContain("the version that won");
    expect(parked).toContain("keyed on the train");
  });

  it("labels the provisional number as PROVISIONAL, never as a number", async () => {
    // ⛔ Showing a PROV- label in the field a real voucher number occupies is how
    // somebody reads it down a phone line to a customer.
    const { client } = stubClient({ entries: [CONFLICT_ENTRY], payload: PARKED, live: LIVE });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /compare/i }));

    await waitFor(() => expect(screen.getByTestId("pane-parked")).toBeTruthy());
    const parked = screen.getByTestId("pane-parked");
    expect(parked.textContent).toMatch(/provisional/i);
    expect(parked.textContent).toContain("PROV-node-a-3");
    // And the live pane is the ONLY one that says "Number".
    expect(screen.getByTestId("pane-live").textContent).toMatch(/number/i);
  });

  it("prints the parked amount EXACTLY as the server sent it", async () => {
    // ⛔ THE FORMATTING TRAP. The server already renders the parked money the way
    // the live pane's money is rendered (fixed-4) precisely so the two columns
    // are comparable. Re-formatting either side here is how "1180" ends up beside
    // "1180.0000" and an operator discards their work over a difference that does
    // not exist.
    const { client } = stubClient({ entries: [CONFLICT_ENTRY], payload: PARKED, live: LIVE });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /compare/i }));

    await waitFor(() => expect(screen.getByTestId("pane-parked")).toBeTruthy());
    expect(screen.getByTestId("pane-parked").textContent).toContain("1180.0000");
    expect(screen.getByTestId("pane-live").textContent).toContain("2360.0000");
  });

  it("names the blocks the diff has no room for instead of dropping them", async () => {
    const { client } = stubClient({ entries: [CONFLICT_ENTRY], payload: PARKED, live: LIVE });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /compare/i }));

    await waitFor(() => expect(screen.getByTestId("pane-parked")).toBeTruthy());
    // Silently omitting GST parameters is how two panes look identical and differ.
    expect(screen.getByTestId("pane-parked").textContent).toContain("GST parameters");
  });

  it("says so when the change that WON was a deletion", async () => {
    // A 404 on the live head is information, not a fault: the operator's edit lost
    // to a delete, and re-keying would resurrect something removed on purpose.
    const { client } = stubClient({
      entries: [CONFLICT_ENTRY],
      payload: PARKED,
      live: new ApiError(404, "voucher not found"),
    });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /compare/i }));

    await waitFor(() => expect(screen.getByTestId("pane-live")).toBeTruthy());
    expect(screen.getByTestId("pane-live").textContent).toMatch(/deletion/i);
  });
});

describe("⛔ the screen never offers to force a stale version through", () => {
  it("offers no retry, force, re-apply or re-submit affordance on a CONFLICT", async () => {
    /**
     * The base version is stale PERMANENTLY — the head has moved and will not
     * move back — so a retried ALTER would be refused again by construction. A
     * button for it could not work, and the failure is worse than a dead button:
     * an operator who believes their version can still win does not re-key it,
     * and the work is lost quietly.
     *
     * Asserted as a PROPERTY over every button on the screen (rule 8) rather than
     * by naming the buttons that DO exist, so a helpful addition later cannot slip
     * past by being worded differently.
     */
    const { client } = stubClient({ entries: [CONFLICT_ENTRY], payload: PARKED, live: LIVE });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /compare/i }));
    await waitFor(() => expect(screen.getByTestId("reconciliation-detail")).toBeTruthy());

    const forbidden = /\b(retry|re-?try|force|re-?apply|apply anyway|overwrite|re-?submit|push mine|keep mine and apply)\b/i;
    for (const b of screen.getAllByRole("button")) {
      expect(forbidden.test(b.textContent ?? ""), `button offers to force: "${b.textContent}"`).toBe(
        false,
      );
    }
  });

  it("tells the operator that keeping their version means a NEW voucher", async () => {
    const { client } = stubClient({ entries: [CONFLICT_ENTRY], payload: PARKED, live: LIVE });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /compare/i }));
    await waitFor(() => expect(screen.getByTestId("reconciliation-detail")).toBeTruthy());

    expect(screen.getByTestId("reconciliation-detail").textContent).toMatch(/new/i);
  });
});

describe("a FROZEN entry is EXPLAINED, not retried", () => {
  it("names what pinned the voucher, not merely that it is frozen", async () => {
    // ⛔ "This is frozen" without "because an IRN is registered against it" is
    // unactionable: the operator goes looking for a setting that lifts it.
    const { client } = stubClient({ entries: [FROZEN_ENTRY], payload: FROZEN_PAYLOAD });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /explain/i }));
    await waitFor(() => expect(screen.getByTestId("reconciliation-detail")).toBeTruthy());

    const text = screen.getByTestId("reconciliation-detail").textContent ?? "";
    // ⛔ ASSERT ON A WORD ONLY THE SCREEN'S OWN LABEL USES. A mutation proof
    // caught this test passing while reasonLabel had been reduced to the bare
    // word "Frozen": the server's `detail` string ALREADY contains "IRN"
    // ("...frozen by IRN IRN-ABC-123..."), so matching /IRN/ was reading the
    // server's prose back and checking nothing this screen decides. "E-invoiced"
    // appears nowhere in the wire data — it is the translation into what an
    // operator can act on, which is the whole job of reasonLabel.
    expect(text).toMatch(/e-invoiced/i);
    expect(text).toContain("INV/2026-27/7");
    // And it names the way FORWARD, which is a credit note rather than an edit.
    expect(text).toMatch(/credit note/i);
  });

  it("shows no compare panes at all — there is nothing to choose between", async () => {
    const { client } = stubClient({ entries: [FROZEN_ENTRY], payload: FROZEN_PAYLOAD });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /explain/i }));
    await waitFor(() => expect(screen.getByTestId("reconciliation-detail")).toBeTruthy());

    expect(screen.queryByTestId("reconciliation-compare")).toBeNull();
  });

  it("says WHERE the keyed version is, rather than rendering an empty pane", async () => {
    // has_payload=false is a real answer: a freeze row never held one, so the
    // operator's own version is on the machine that keyed it and nowhere else.
    const { client } = stubClient({ entries: [FROZEN_ENTRY], payload: FROZEN_PAYLOAD });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /explain/i }));
    await waitFor(() => expect(screen.getByTestId("reconciliation-detail")).toBeTruthy());

    expect(screen.getByTestId("reconciliation-detail").textContent).toMatch(
      /only on the node that keyed it/i,
    );
  });
});

describe("clearing an entry", () => {
  it("calls the resolve route and RE-READS the queue as its oracle", async () => {
    /**
     * ⛔ THE RE-READ IS THE POINT. A screen that spliced the row out of its own
     * state would look identical whether resolved_at was stamped or the write did
     * nothing at all — which is exactly the mutation that turns the server-side
     * test red at the queue listing.
     */
    const { client, calls } = stubClient({
      entries: [CONFLICT_ENTRY],
      entriesAfterResolve: [],
      payload: PARKED,
      live: LIVE,
    });
    await openScreen(client);
    const readsBefore = calls.queueReads;
    fireEvent.click(screen.getByRole("button", { name: /compare/i }));
    await waitFor(() => expect(screen.getByTestId("reconciliation-detail")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /discard my version/i }));

    await waitFor(() => expect(calls.resolved).toEqual([CONFLICT_ENTRY.id]));
    await waitFor(() => expect(calls.queueReads).toBeGreaterThan(readsBefore));
    await waitFor(() => expect(screen.getByTestId("reconciliation-empty")).toBeTruthy());
  });

  it("does not claim it re-applied anything", async () => {
    // Resolving records that a human dealt with the item. Nothing replays the
    // parked intent — so a screen implying otherwise promises what no route does.
    const { client } = stubClient({
      entries: [FROZEN_ENTRY],
      entriesAfterResolve: [],
      payload: FROZEN_PAYLOAD,
    });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /explain/i }));
    await waitFor(() => expect(screen.getByTestId("reconciliation-detail")).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: /dealt with this/i }));

    await waitFor(() => expect(screen.getByRole("status")).toBeTruthy());
    expect(screen.getByRole("status").textContent).toMatch(/still refused/i);
  });

  it("treats a 404 as a stale view rather than a failure", async () => {
    const { client } = stubClient({
      entries: [CONFLICT_ENTRY],
      payload: PARKED,
      live: LIVE,
      resolveError: new ApiError(404, "no open reconciliation entry with that id"),
    });
    await openScreen(client);
    fireEvent.click(screen.getByRole("button", { name: /compare/i }));
    await waitFor(() => expect(screen.getByTestId("reconciliation-detail")).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: /discard my version/i }));

    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    expect(screen.getByRole("alert").textContent).toMatch(/no longer open|somebody else/i);
  });
});

describe("an empty queue reads as ordinary, not as a fault", () => {
  it("says nothing has been refused rather than implying something is missing", async () => {
    const { client } = stubClient({ entries: [] });
    await openScreen(client);
    expect(screen.getByTestId("reconciliation-empty").textContent).toMatch(/ordinary state/i);
  });

  it("surfaces a read failure as an alert instead of an empty list", async () => {
    // ⛔ A failed read rendered as "nothing to reconcile" is the dangerous
    // direction: it tells the operator their books refused nothing when in fact
    // the screen could not find out.
    const base = createMockClient();
    const client: CoredClient = {
      ...base,
      async reconciliationQueue() {
        throw new ApiError(403, "forbidden");
      },
    };
    await openScreen(client);
    expect(screen.getByRole("alert").textContent).toMatch(/could not be read/i);
  });
});
