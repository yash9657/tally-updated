import { useEffect, useRef } from "react";
import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import { ApiError, createMockClient, NetworkError } from "../api";
import type { CoredClient, SyncCommitResponse, SyncIntent } from "../api";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createDefaultRegistry } from "../app/defaultScreens";
import { OfflineOutboxProvider, useOfflineOutbox } from "../offline/OfflineOutbox";
import { clearOutboxStores, memoryOutboxStore } from "../offline/store";

import { OfflineOutboxScreen } from "./OfflineOutboxScreen";

/**
 * OfflineOutboxScreen.test.tsx — the screen that says "this is NOT in the books".
 *
 * ⛔ WHAT THIS FILE IS FOR, over and above `offlineQueueing.test.tsx` (which
 * proves a failed post reaches this queue at all): the four things this screen
 * must never get wrong.
 *
 *   1. a whole-batch refusal is a BANNER and the queue is intact behind it;
 *   2. the PROV -> final mapping survives the row leaving the queue;
 *   3. `durable: false` is stated, loudly, because a queue that quietly forgets
 *      is worse than none;
 *   4. the number column is never headed like a voucher number.
 */

const CO = "co-1";
const SUBJ = "user:alice";

beforeEach(async () => {
  await clearOutboxStores();
});

/**
 * Render the screen over a real provider holding `intents` queued vouchers.
 *
 * `commit` receives THE INTENTS THE PROVIDER ACTUALLY SENT, which is what lets a
 * fake sequencer echo real idempotency keys — the pairing the PROV -> final
 * mapping depends on. A test that invented keys would prove the screen can render
 * a table, not that it can pair an outcome to the voucher it belongs to.
 */
function renderWithQueue(opts: {
  intents?: number;
  commit?: (intents: SyncIntent[]) => Promise<SyncCommitResponse>;
  durable?: boolean;
}) {
  const base = createMockClient();
  const client: CoredClient = {
    ...base,
    async syncCommit(_companyID: string, intents: SyncIntent[]) {
      if (!opts.commit) throw new ApiError(501, "the mock has no cloud sequencer");
      return opts.commit(intents);
    },
  };
  const store = { ...memoryOutboxStore(CO, SUBJ), durable: opts.durable ?? true };

  render(
    <OfflineOutboxProvider
      companyID={CO}
      subject={SUBJ}
      client={client}
      makeStore={() => store}
      now={() => 1756300000000}
    >
      <Seeder count={opts.intents ?? 0} />
      <OfflineOutboxScreen />
    </OfflineOutboxProvider>,
  );
}

/**
 * Appends N intents through the PROVIDER's own API rather than writing them into
 * the store by hand.
 *
 * ⛔ THE DIFFERENCE MATTERS: a hand-written store record would let this file pass
 * with a `queueCreate` that produces a malformed intent — a wrong idempotency-key
 * format, an un-emptied `voucher_no` — which is precisely the class of defect that
 * makes the server apply a re-commit twice instead of answering DUPLICATE.
 */
function Seeder({ count }: { count: number }): null {
  // ⛔⛔ A REF, NEVER `useState`, AND THIS COST AN HOUR. The obvious version holds
  // a `done` flag in state and lists it in the deps. MEASURED: in the FIRST test
  // of a file that works perfectly; in every test after it, `setDone(true)` never
  // lands — the effect re-enters with `done === false` forever, each pass appends
  // `count` more intents, and because the loop is driven by promise
  // continuations rather than timers it STARVES THE EVENT LOOP: vitest's own
  // per-test timeout can never fire, so the symptom is not a failing test but a
  // worker pegged at 130% CPU and a run that never ends. Found by counting calls
  // to `flush` and printing a stack, not by reading.
  //
  // ⚠️ THE GUARD MUST BE SYNCHRONOUS WITH THE EFFECT, and only a ref is. The
  // effect's deps include the context value, which changes identity on every
  // queued intent, so the effect re-runs BEFORE any state update from it can be
  // observed. A ref is written and read in the same tick and cannot be lost.
  const started = useRef(false);
  const outbox = useOfflineOutbox();
  useEffect(() => {
    if (started.current || !outbox?.ready || count === 0) return;
    started.current = true;
    void (async () => {
      for (let i = 0; i < count; i++) {
        await outbox.queueCreate("Journal", {
          narration: `queued voucher ${i}`,
          voucher_date: "2026-08-27",
        });
      }
    })();
  }, [count, outbox]);
  return null;
}

/** An all-COMMITTED answer for whatever was sent. */
function committedAll(finalNo: string) {
  return async (intents: SyncIntent[]): Promise<SyncCommitResponse> => ({
    outcomes: intents.map((i) => ({
      idempotency_key: i.idempotency_key,
      client_voucher_id: i.client_voucher_id,
      status: "COMMITTED" as const,
      final_voucher_no: finalNo,
      version_no: 1,
      reconciliation_id: "",
      detail: "",
    })),
  });
}

describe("⛔ the screen says, in every way it has, that nothing here is posted", () => {
  it("the number column is headed 'Provisional' and NEVER 'No.'", async () => {
    renderWithQueue({ intents: 1 });
    const table = await screen.findByTestId("offline-outbox-table");
    const headers = Array.from(table.querySelectorAll("th")).map((h) => h.textContent?.trim());
    expect(headers).toContain("Provisional");
    // ⛔ THE FAILURE THIS PREVENTS: a provisional number in a column an operator
    // reads as a document number, then quotes to a customer.
    expect(headers).not.toContain("No.");
    expect(headers).not.toContain("Voucher No.");
  });

  it("the queued row's number carries the PROV literal, not just a sequence", async () => {
    renderWithQueue({ intents: 1 });
    const table = await screen.findByTestId("offline-outbox-table");
    expect(table.textContent).toMatch(/PROV-/);
  });

  it("the empty state says an empty outbox is ORDINARY, and that refusals are not queued here", async () => {
    renderWithQueue({});
    const empty = await screen.findByTestId("offline-outbox-empty");
    // An operator who reads "nothing waiting" after a refusal must not conclude
    // the refusal was queued somewhere they cannot see.
    expect(empty.textContent).toMatch(/refused/i);
  });

  it("⛔ says the queue is UNENCRYPTED, whether or not anything is in it", async () => {
    renderWithQueue({});
    expect((await screen.findByTestId("offline-outbox-encryption-notice")).textContent).toMatch(
      /unencrypted/i,
    );
  });

  it("⛔ a NON-DURABLE store is an ALERT, not a footnote", async () => {
    renderWithQueue({ intents: 1, durable: false });
    const warn = await screen.findByTestId("offline-outbox-not-durable");
    expect(warn.getAttribute("role"), "it must reach a screen reader as an alert").toBe("alert");
    expect(warn.textContent).toMatch(/NOT survive a reload/i);
    expect(screen.queryByTestId("offline-outbox-durable")).toBeNull();
  });

  it("discloses that a mid-send drop may already have landed, and names where to check", async () => {
    renderWithQueue({ intents: 1 });
    const note = await screen.findByTestId("offline-outbox-midflight-notice");
    expect(note.textContent).toMatch(/Day Book/);
  });
});

describe("⛔⛔ a WHOLE-BATCH refusal is a banner over an INTACT queue", () => {
  it("a cutoff 403 keeps every row and never says they failed", async () => {
    renderWithQueue({
      intents: 3,
      commit: () =>
        Promise.reject(
          new ApiError(403, "voucher date is before the back-dated cutoff of 2026-04-01"),
        ),
    });
    await screen.findByTestId("offline-outbox-table");
    await waitFor(() => expect(screen.getAllByTestId("offline-outbox-row")).toHaveLength(3));
    fireEvent.click(screen.getByTestId("offline-outbox-commit"));

    const banner = await screen.findByTestId("offline-outbox-refusal");
    expect(banner.getAttribute("role")).toBe("alert");
    // ⛔ THE SERVER'S OWN SENTENCE, which names the date — the one fact that tells
    // the operator which row to fix.
    expect((await screen.findByTestId("offline-outbox-refusal-message")).textContent).toMatch(
      /2026-04-01/,
    );
    // ⛔ AND THE INVARIANT: all three are still queued.
    expect(screen.getAllByTestId("offline-outbox-row")).toHaveLength(3);
    expect(banner.textContent).toMatch(/still queued/i);
    // The dangerous wording, asserted absent.
    expect(banner.textContent).not.toMatch(/re-?key/i);
  });

  it("still offline on commit says nothing was sent — and the rows are all there", async () => {
    renderWithQueue({
      intents: 2,
      commit: () =>
        Promise.reject(new NetworkError("POST /sync/commit", new TypeError("Failed to fetch"))),
    });
    await screen.findByTestId("offline-outbox-table");
    await waitFor(() => expect(screen.getAllByTestId("offline-outbox-row")).toHaveLength(2));
    fireEvent.click(screen.getByTestId("offline-outbox-commit"));

    expect((await screen.findByTestId("offline-outbox-refusal")).textContent).toMatch(
      /nothing was sent/i,
    );
    expect(screen.getAllByTestId("offline-outbox-row")).toHaveLength(2);
  });

  it("the mock's honest 501 is shown VERBATIM rather than reworded", async () => {
    // The mock refuses to fake a sequencer on purpose. The screen must pass that
    // sentence through: it names the gate to run instead.
    renderWithQueue({ intents: 1 });
    await screen.findByTestId("offline-outbox-table");
    fireEvent.click(screen.getByTestId("offline-outbox-commit"));
    expect((await screen.findByTestId("offline-outbox-refusal-message")).textContent).toMatch(
      /no cloud sequencer/,
    );
    expect(screen.getAllByTestId("offline-outbox-row")).toHaveLength(1);
  });
});

describe("a successful commit", () => {
  it("⭐ keeps the PROVISIONAL -> FINAL mapping after the row has left the queue", async () => {
    renderWithQueue({ intents: 1, commit: committedAll("INV/2026-27/42") });
    await screen.findByTestId("offline-outbox-table");
    // ⚠️ READ THE CELL, NOT A REGEX OVER THE TABLE. `textContent` concatenates
    // cells with no separator, so `/…PROV-[\w-]+/` runs straight past the end of
    // the number and swallows the next three columns — which is how this
    // assertion first "failed" against a screen that was rendering correctly.
    const provisional = screen
      .getAllByTestId("offline-outbox-row")[0]!
      .querySelectorAll("td")[0]!.textContent!;
    expect(provisional, "a provisional number must be on the row").toMatch(/PROV-/);

    fireEvent.click(screen.getByTestId("offline-outbox-commit"));

    const outcomes = await screen.findByTestId("offline-outbox-outcomes");
    // ⛔ BOTH NUMBERS, TOGETHER. The operator may already have quoted the
    // provisional; this mapping is the only thing that lets them correct it.
    expect(outcomes.textContent).toMatch(/INV\/2026-27\/42/);
    expect(outcomes.textContent).toContain(provisional);
    // And the row has left the queue, because the server has it now.
    await waitFor(() => {
      expect(screen.queryByTestId("offline-outbox-table")).toBeNull();
    });
  });

  it("a FROZEN outcome points at the Reconciliation Queue rather than dead-ending", async () => {
    renderWithQueue({
      intents: 1,
      commit: async (intents) => ({
        outcomes: intents.map((i) => ({
          idempotency_key: i.idempotency_key,
          client_voucher_id: i.client_voucher_id,
          status: "FROZEN" as const,
          final_voucher_no: "",
          version_no: 0,
          reconciliation_id: "recon-1",
          detail: "voucher frozen by IRN IRN-ABC-123",
        })),
      }),
    });
    await screen.findByTestId("offline-outbox-table");
    fireEvent.click(screen.getByTestId("offline-outbox-commit"));

    const outcomes = await screen.findByTestId("offline-outbox-outcomes");
    // ⛔ "FROZEN" alone sends an operator looking for a setting to turn off.
    expect(outcomes.textContent).toMatch(/Alt\+R/);
    expect(outcomes.textContent).toMatch(/IRN-ABC-123/);
  });

  it("⛔ a partial answer settles ONLY what came back", async () => {
    // A server that answered for one of three intents leaves the other two
    // queued. Settling on the count, or clearing wholesale, would destroy work
    // the sequencer never judged.
    renderWithQueue({
      intents: 3,
      commit: async (intents) => ({
        outcomes: intents.slice(0, 1).map((i) => ({
          idempotency_key: i.idempotency_key,
          client_voucher_id: i.client_voucher_id,
          status: "COMMITTED" as const,
          final_voucher_no: "INV/2026-27/7",
          version_no: 1,
          reconciliation_id: "",
          detail: "",
        })),
      }),
    });
    await screen.findByTestId("offline-outbox-table");
    await waitFor(() => expect(screen.getAllByTestId("offline-outbox-row")).toHaveLength(3));
    fireEvent.click(screen.getByTestId("offline-outbox-commit"));

    await waitFor(() => {
      expect(screen.getAllByTestId("offline-outbox-row")).toHaveLength(2);
    });
  });
});

describe("discard", () => {
  it("⛔ removes ONE row — the escape hatch the cutoff case requires", async () => {
    // One voucher dated inside a closed period refuses the ENTIRE batch, so
    // without this the whole queue is un-committable and the operator's only way
    // out is signing out, which destroys all of it.
    renderWithQueue({ intents: 2 });
    await screen.findByTestId("offline-outbox-table");
    await waitFor(() => expect(screen.getAllByTestId("offline-outbox-row")).toHaveLength(2));

    fireEvent.click(screen.getAllByText("Discard")[0]!);

    await waitFor(() => {
      expect(screen.getAllByTestId("offline-outbox-row")).toHaveLength(1);
    });
    // ⛔ AND IT SAYS THE BOOKS DID NOT CHANGE. A "discarded" with no such
    // statement invites the belief that something was un-posted.
    expect((await screen.findByRole("status")).textContent).toMatch(
      /nothing in the books changed/i,
    );
  });
});

describe("⛔ the registration point NO OTHER TEST IN THIS REPOSITORY CAN SEE", () => {
  it("shows the placeholder rather than this screen when no company is open", () => {
    // `COMPANY_SCOPED_SCREENS` is consulted ONLY when company === null, and every
    // other screen test passes an initialCompany — so deleting the entry breaks
    // nothing in the suite unless this test exists. It is the same hole track D
    // closed for the reconciliation screen; this is the THIRD screen to have it.
    //
    // ⚠️ NO `initialCompany` PROP AT ALL, not `initialCompany={null}`.
    // `CompanyProvider` does not bootstrap unless asked, so omitting it is a
    // DETERMINISTIC company-less shell; passing an explicit null sends it down the
    // bootstrap path, which mints a company and then this tests the opposite of
    // what it claims. (Measured: that version hung the file.)
    render(
      <CompanyProvider client={createMockClient()}>
        <AppShell registry={createDefaultRegistry()} />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "œ", code: "KeyQ", altKey: true });

    // With no company there is no queue to show, and an empty table would read as
    // "nothing is waiting" — which is a claim about somebody's books.
    expect(screen.getByRole("status").textContent).toMatch(/preparing company/i);
    expect(screen.queryByTestId("offline-outbox-screen")).toBeNull();
    // The frame is still titled, so the operator can see where they are.
    expect(screen.getByRole("banner").getAttribute("aria-label")).toContain("Offline Outbox");
  });
});
