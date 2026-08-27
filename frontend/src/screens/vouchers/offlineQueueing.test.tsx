import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import { ApiError, createMockClient, NetworkError } from "../../api";
import type { CoredClient } from "../../api";
import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import { AUTH_STORAGE_KEY, AuthProvider } from "../../auth";
import { clearOutboxStores } from "../../offline/store";

/**
 * offlineQueueing.test.tsx — ⛔⛔ THE JUDGEMENT ON THE MAIN WRITE PATH, DRIVEN
 * THROUGH THE REAL SHELL.
 *
 * `queueDecision.test.ts` proves the decision function, and it proves that the
 * real transport raises `NetworkError` when `fetch` rejects. ⛔ NEITHER OF THOSE
 * PROVES THE APPLICATION IS WIRED TO EITHER. This file is the composition test:
 * it renders `<AppShell/>`, keys a real voucher, presses Ctrl+A, and asks what an
 * operator would see.
 *
 * ⛔ IT DRIVES THE SHELL AND NOT THE SCREEN, AND THAT IS THE WHOLE POINT. The
 * offline node, the store and the provider can all be present and correct while
 * `AppShell` mounts no `OfflineOutboxProvider` — in which case
 * `useOfflineOutbox()` returns null, the accept path refuses to queue, and EVERY
 * standalone screen test still passes, because those render a screen and not the
 * shell. That is exactly the class of hole this repository has shipped before
 * (`SCREEN_ACTIONS`, twice). Only a shell drive can see it.
 *
 * ⚠️ WHY A HAND-BUILT `NetworkError` AND NOT A REJECTED `fetch`. The mock client
 * is not an HTTP client — there is no fetch under it to fail — so the transport
 * half of the chain is proved where it lives (`queueDecision.test.ts`: a
 * `fetchImpl` that rejects becomes a `NetworkError`) and this file proves the
 * screen's half. The two halves meet on the type, which is why the type exists.
 */

const SESSION = {
  token: "test-token",
  user: { subject: "user:alice", display_name: "Alice" },
};

beforeEach(async () => {
  // ⛔ The memory store is module-scoped (it must be — two stores for one
  // (company, subject) in a page have to see the same data, which is what
  // IndexedDB does). Without this, one test's queue leaks into the next and a
  // "nothing was queued" assertion passes or fails on test ORDER.
  await clearOutboxStores();
  globalThis.sessionStorage?.clear();
  globalThis.sessionStorage?.setItem(AUTH_STORAGE_KEY, JSON.stringify(SESSION));
});

/**
 * The real shell, with a real signed-in subject.
 *
 * ⚠️ THE `AuthProvider` IS LOAD-BEARING HERE AND IS NOT DECORATION. The outbox is
 * keyed by (company, SUBJECT) so that a second operator on the same browser never
 * inherits the first one's un-committed vouchers — so with no session there is no
 * subject, no queue, and this file would test nothing while appearing to.
 */
function renderShell(client: CoredClient) {
  return render(
    <AuthProvider baseUrl="" fetchImpl={() => Promise.reject(new Error("no auth calls expected"))}>
      <CompanyProvider
        client={client}
        initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
      >
        <AppShell />
      </CompanyProvider>
    </AuthProvider>,
  );
}

/** A mock client whose Journal post fails in a stated way. */
function clientRejecting(err: unknown): CoredClient {
  const base = createMockClient();
  return {
    ...base,
    async postJournalVoucher() {
      throw err;
    },
  };
}

/** Key a balanced two-line Journal and accept it. */
async function keyJournalAndAccept(): Promise<Element> {
  const app = screen.getByRole("application");
  fireEvent.keyDown(app, { key: "F7" });
  await screen.findByTestId("double-entry-panel");

  const rows: [string, "DR" | "CR", string][] = [
    ["Cash", "DR", "100"],
    ["Sales", "CR", "100"],
  ];
  for (let i = 0; i < rows.length; i++) {
    if (i > 0) fireEvent.click(screen.getByTestId("de-add-line"));
    fireEvent.change(screen.getByTestId(`de-ledger:${i}`), { target: { value: rows[i]![0] } });
    fireEvent.change(screen.getByTestId(`de-side:${i}`), { target: { value: rows[i]![1] } });
    fireEvent.change(screen.getByTestId(`de-amount:${i}`), { target: { value: rows[i]![2] } });
  }
  fireEvent.keyDown(app, { key: "a", ctrlKey: true });
  return app;
}

/** The event a real Mac sends for Option+Q: the COMPOSED key plus the code. */
function altMacQ(target: Element): void {
  // ⚠️ macOS COMPOSES Option+Q INTO "œ" — normalize.test.ts pins that mapping for
  // this exact combo. Firing { key: "q", altKey: true } would prove the binding
  // works on machines nobody develops on.
  fireEvent.keyDown(target, { key: "œ", code: "KeyQ", altKey: true });
}

describe("⛔ a DROPPED CONNECTION queues the voucher", () => {
  it("says QUEUED, NOT POSTED — and names a provisional number that says PROV", async () => {
    renderShell(clientRejecting(new NetworkError("POST /v1/…/journal", new TypeError("Failed to fetch"))));
    await keyJournalAndAccept();

    const status = await screen.findByText(/QUEUED, NOT POSTED/i);
    // ⛔ THE THREE THINGS THE SENTENCE MUST CARRY. "Queued" alone leaves an
    // operator not knowing whether the books have it.
    expect(status.textContent).toMatch(/not in the books/i);
    expect(status.textContent, "the provisional number must be visible and say PROV").toMatch(/PROV-/);
    expect(status.textContent, "and it must name the way to find the queue again").toMatch(/Alt\+Q/);
  });

  it("⛔ DOES NOT REPORT THE VOUCHER AS SAVED — a provisional number is not a voucher number", async () => {
    renderShell(clientRejecting(new NetworkError("POST /v1/…/journal", new TypeError("Failed to fetch"))));
    await keyJournalAndAccept();
    await screen.findByText(/QUEUED, NOT POSTED/i);

    // The success path sets a saved number and a server-derived tax split, and
    // Ctrl+P / Alt+M read exactly those. A queued voucher has neither, so the
    // word this screen uses for a real post must be absent.
    expect(screen.queryByText(/^Saved$/)).toBeNull();
  });

  it("⭐ AND THE QUEUE IS REACHABLE FROM THE KEY THAT ADVERTISES IT: Alt+Q shows the row", async () => {
    // ⛔ THIS IS THE COMPOSITION TEST. Everything above could pass with an outbox
    // nothing can read: the provider, the screen, the Alt+Q binding, the Gateway
    // door and the navMachine case are five separate registrations and any one of
    // them missing makes the queue unreachable while the accept path still says
    // "queued". Verifying the pieces exist is not verifying they compose.
    renderShell(clientRejecting(new NetworkError("POST /v1/…/journal", new TypeError("Failed to fetch"))));
    const app = await keyJournalAndAccept();
    await screen.findByText(/QUEUED, NOT POSTED/i);

    altMacQ(app);

    const table = await screen.findByTestId("offline-outbox-table");
    expect(table.textContent).toMatch(/PROV-/);
    expect(screen.getAllByTestId("offline-outbox-row")).toHaveLength(1);
    // The class travels on the intent as the WIRE LABEL, which is what the server
    // does `ledger.Class(ir.Class)` with. A slug here would decode to a class the
    // ledger does not know.
    expect(table.textContent).toMatch(/Journal/);
  });
});

describe("⛔⛔ A REFUSAL IS NEVER QUEUED — the direction that loses money", () => {
  /**
   * Each of these reached the server, so the books judged it (or something on
   * their behalf did, and the client cannot tell which). The operator must see
   * the reason and must NOT be told their work is safely waiting.
   */
  const REFUSALS: ReadonlyArray<[label: string, err: unknown]> = [
    ["a 422 the books refused", new ApiError(422, "debit total must equal credit total")],
    [
      "a 403 closed period",
      new ApiError(403, "voucher date is before the back-dated cutoff of 2026-04-01"),
    ],
    ["a 403 denial", new ApiError(403, "level may not create journal")],
    ["a 500 the server may have half-applied", new ApiError(500, "internal error")],
    ["a 503", new ApiError(503, "unavailable")],
  ];

  for (const [label, err] of REFUSALS) {
    it(`${label} is REPORTED, and the outbox stays empty`, async () => {
      renderShell(clientRejecting(err));
      const app = await keyJournalAndAccept();

      // The screen says something, and it is not the queued sentence.
      await waitFor(() => {
        expect(screen.queryByText(/QUEUED, NOT POSTED/i)).toBeNull();
      });
      expect(await screen.findByText(/Error:/)).toBeTruthy();

      // ⛔ AND THE ORACLE THAT CAN ACTUALLY SEE THE DEFECT: the queue itself.
      // Asserting only the status line would pass for a build that queued the
      // voucher AND printed the error — which is the worst of both, because the
      // intent would then be committed later behind the operator's back.
      altMacQ(app);
      expect(await screen.findByTestId("offline-outbox-empty")).toBeTruthy();
      expect(screen.queryByTestId("offline-outbox-table")).toBeNull();
    });
  }

  it("⛔ a CLIENT fault is not queued either, and does not masquerade as an outage", async () => {
    // A bare TypeError is what a null deref inside the accept chain looks like,
    // and it is worded exactly like a browser's own network rejection. Queueing
    // it would tell the operator their invoice is safe because THIS APPLICATION
    // crashed.
    renderShell(clientRejecting(new TypeError("Failed to fetch")));
    const app = await keyJournalAndAccept();

    await screen.findByText(/Error:/);
    expect(screen.queryByText(/QUEUED, NOT POSTED/i)).toBeNull();
    altMacQ(app);
    expect(await screen.findByTestId("offline-outbox-empty")).toBeTruthy();
  });
});

describe("the queue survives the session it was keyed in", () => {
  it("two dropped posts queue TWO rows with DIFFERENT provisional numbers", async () => {
    // ⛔ The provisional's uniqueness is what makes it usable as a handle at all.
    // A restored node that restarted its sequence would re-issue a label the
    // operator has already written on a different voucher.
    renderShell(clientRejecting(new NetworkError("POST /v1/…/journal", new TypeError("Failed to fetch"))));
    const app = await keyJournalAndAccept();
    await screen.findByText(/QUEUED, NOT POSTED/i);
    fireEvent.keyDown(app, { key: "Escape" });
    await keyJournalAndAccept();

    altMacQ(app);
    await screen.findByTestId("offline-outbox-table");
    const rows = screen.getAllByTestId("offline-outbox-row");
    expect(rows).toHaveLength(2);
    const labels = rows.map((r) => r.textContent?.match(/PROV-[\w-]+/)?.[0]);
    expect(new Set(labels).size, "two vouchers must not share a provisional").toBe(2);
  });
});
