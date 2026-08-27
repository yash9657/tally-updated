import { describe, expect, it } from "vitest";

import { createRestClient } from "./rest";
import type { SyncIntent } from "./types";

/**
 * syncCommitWire.test.ts — ⛔⛔ THE BYTES `POST /sync/commit` ACTUALLY SENDS.
 *
 * THE DEFECT THIS FILE EXISTS FOR, found on the first real commit from a browser
 * and invisible to every test that existed at the time:
 *
 *   `SyncIntent.payload` IS a `VoucherRequest` — the same type the online path
 *   posts, deliberately, so the server decodes both with one translator. Every
 *   `post*Voucher` in `rest.ts` sends `wireVoucher(req)`, which maps the
 *   frontend's legible `dr_cr` ("DR"/"CR") onto the backend's canonical "D"/"C".
 *   `syncCommit` sent the intents UNTOUCHED. Server-side `ledger.Debit` is "D",
 *   so a "DR" posting matched neither side, was summed as a credit, and the whole
 *   batch was refused 422 "debit total must equal credit total".
 *
 * ⛔ IT FAILED AT COMMIT TIME, NOT AT QUEUE TIME, which is the worst place for it:
 * an operator could key ten invoices offline, watch each one queue safely, and
 * then find that none of them could ever be committed — with the outbox correctly
 * refusing to clear, so the queue would never drain and nothing would explain why.
 *
 * ⛔ AND WHY NO EXISTING TEST COULD SEE IT. The mock client is not an HTTP client:
 * it never runs this adapter, and it accepts either spelling. The whole
 * queue-then-commit flow was proved against it and passed. ⭐ THE ONLY ORACLE THAT
 * WORKS HERE IS THE REQUEST BODY, so that is what this file asserts.
 */

/** A rest client whose transport records the bodies it was asked to send. */
function recordingClient() {
  const sent: { url: string; body: unknown }[] = [];
  const client = createRestClient({
    baseUrl: "http://x",
    fetchImpl: (input, init) => {
      sent.push({
        url: String(input),
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      });
      return Promise.resolve(
        new Response(JSON.stringify({ outcomes: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    },
  });
  return { client, sent };
}

const INTENT: SyncIntent = {
  idempotency_key: "node-a:CREATE:v1",
  client_voucher_id: "11111111-1111-7111-8111-111111111111",
  op: "CREATE",
  class: "Journal",
  provisional_no: "INV/2025-26/PROV-node-a-1",
  hlc: { wall_millis: 1756300000000, counter: 0, node_id: "node-a" },
  node_id: "node-a",
  base_version: 0,
  payload: {
    voucher_date: "2026-08-27",
    narration: "queued offline",
    postings: [
      { ledger_id: "led-dr", dr_cr: "DR", amount: "1180.0000" },
      { ledger_id: "led-cr", dr_cr: "CR", amount: "1180.0000" },
    ],
  },
};

describe("⛔⛔ POST /sync/commit sends the SAME wire form the online path sends", () => {
  it("normalises dr_cr on every posting: DR -> D, CR -> C", async () => {
    const { client, sent } = recordingClient();
    await client.syncCommit("co-1", [INTENT]);

    const body = sent[0]!.body as { intents: SyncIntent[] };
    const postings = body.intents[0]!.payload.postings!;
    // ⛔ THE ASSERTION THE OLD BUILD FAILED. "DR" reached the server verbatim and
    // matched neither ledger.Debit ("D") nor ledger.Credit ("C").
    expect(postings.map((p) => p.dr_cr)).toEqual(["D", "C"]);
  });

  it("normalises dr_cr on BILL ALLOCATIONS too — the same field, the same rule", async () => {
    // Bill allocations carry a dr_cr of their own and `wireVoucher` maps both.
    // Omitting them here would fix half the defect and leave a bill-by-bill
    // voucher queued offline permanently un-committable.
    const { client, sent } = recordingClient();
    await client.syncCommit("co-1", [
      {
        ...INTENT,
        payload: {
          ...INTENT.payload,
          bill_allocations: [
            {
              ledger_id: "led-party",
              ref_name: "INV-1",
              ref_type: "NEW",
              dr_cr: "DR",
              amount: "1180.0000",
            },
          ],
        },
      },
    ]);

    const body = sent[0]!.body as { intents: SyncIntent[] };
    expect(body.intents[0]!.payload.bill_allocations!.map((b) => b.dr_cr)).toEqual(["D"]);
  });

  it("leaves everything else on the intent exactly as the node recorded it", async () => {
    // ⛔ THE IDEMPOTENCY KEY IS THE ONE FIELD THAT MUST NOT BE TOUCHED. The
    // server's exactly-once replay matches on that string; a transport that
    // rewrote it would make a re-commit APPLY TWICE instead of answering
    // DUPLICATE. The same goes for the provisional number and the HLC, which
    // decide who wins a conflict.
    const { client, sent } = recordingClient();
    await client.syncCommit("co-1", [INTENT]);

    const got = (sent[0]!.body as { intents: SyncIntent[] }).intents[0]!;
    expect(got.idempotency_key).toBe(INTENT.idempotency_key);
    expect(got.client_voucher_id).toBe(INTENT.client_voucher_id);
    expect(got.provisional_no).toBe(INTENT.provisional_no);
    expect(got.hlc).toEqual(INTENT.hlc);
    expect(got.base_version).toBe(0);
    expect(got.class).toBe("Journal");
    // ⛔ And the provisional is NOT smuggled into voucher_no: the server allocates
    // the real number, and `OfflineNode` forces this empty for that reason.
    expect(got.payload.voucher_no ?? "").toBe("");
  });

  it("the whole outbox goes in ONE request, oldest first", async () => {
    const { client, sent } = recordingClient();
    const second: SyncIntent = { ...INTENT, idempotency_key: "node-a:CREATE:v2" };
    await client.syncCommit("co-1", [INTENT, second]);

    expect(sent).toHaveLength(1);
    expect(sent[0]!.url).toContain("/sync/commit");
    const body = sent[0]!.body as { intents: SyncIntent[] };
    expect(body.intents.map((i) => i.idempotency_key)).toEqual([
      "node-a:CREATE:v1",
      "node-a:CREATE:v2",
    ]);
  });

  it("a payload with no postings is passed through rather than mangled", async () => {
    // A DELETE tombstone carries `{}`. `wireVoucher` returns it untouched, and it
    // must: inventing an empty postings array would change what the server sees.
    const { client, sent } = recordingClient();
    await client.syncCommit("co-1", [
      { ...INTENT, op: "DELETE", idempotency_key: "node-a:DELETE:x:2", payload: {} },
    ]);

    const got = (sent[0]!.body as { intents: SyncIntent[] }).intents[0]!;
    expect(got.payload).toEqual({});
  });
});
