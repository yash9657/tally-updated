/**
 * port.golden.test.ts — proves frontend/src/offline/ agrees with
 * backend/internal/sync/ byte for byte.
 *
 * ⛔ WHY THIS IS THE MOST IMPORTANT TEST IN THE OFFLINE MODULE. The three things
 * it pins are all SILENT when they drift:
 *
 *   idempotency keys   the server's exactly-once replay matches on the string. A
 *                      port that spells one differently does not error — a
 *                      re-commit APPLIES A SECOND TIME instead of answering
 *                      DUPLICATE, so the operator gets two vouchers.
 *   client voucher ids the id IS the committed identity, and the node partition
 *                      is what stops two disconnected machines minting the same
 *                      one. crypto.randomUUID() would look fine here forever and
 *                      make collision a probability rather than an impossibility.
 *   HLC readings       the sequencer picks the conflict WINNER by their total
 *                      order. Drift changes who wins, and nothing reports it.
 *
 * The expectations are NOT typed in this file. They are measured from the Go
 * originals by internal/sync's TestOfflineNodeVectorsGolden and read from
 * ../api/goldens/offline-node-vectors.json — HANDOFF §0.1 item 4's rule that a
 * gate over a server decision is a generated golden, never a list in TypeScript.
 * Values typed here would be a second opinion about what Go does.
 */

import { describe, expect, it } from "vitest";

import vectors from "../api/goldens/offline-node-vectors.json";

import { HLC } from "./hlc";
import { OfflineNode } from "./outbox";

interface GoldenHLC {
  wall_millis: number;
  counter: number;
  node_id: string;
}
interface GoldenStep {
  kind: string;
  wall_millis: number;
  target_step: number;
  base_version: number;
  class: string;
  client_voucher_id: string;
  idempotency_key: string;
  provisional_no: string;
  hlc: GoldenHLC;
}
interface GoldenNodeCase {
  name: string;
  node_id: string;
  fy: string;
  seed: number;
  steps: GoldenStep[];
}
interface GoldenClockOp {
  kind: string;
  wall_millis: number;
  remote: GoldenHLC;
  got: GoldenHLC;
}
interface GoldenClockCase {
  name: string;
  node_id: string;
  ops: GoldenClockOp[];
}

/**
 * Indexed access that FAILS rather than yielding undefined. The golden's
 * target_step values are produced by the generator, so an out-of-range one means
 * the golden is malformed — which must be loud, not a silent `undefined` flowing
 * into an assertion that then compares two undefineds and passes.
 */
function at<T>(xs: readonly T[], i: number, what: string): T {
  const v = xs[i];
  if (v === undefined) throw new Error(`golden is malformed: no ${what} at index ${i}`);
  return v;
}

const nodes = vectors.nodes as GoldenNodeCase[];
const clocks = vectors.clocks as GoldenClockCase[];

describe("the offline node is a faithful port of internal/sync/node.go", () => {
  // A guard against the whole suite passing because the golden was empty or the
  // shape changed — the failure mode a golden-driven test is most prone to.
  it("reads a golden with real cases in it", () => {
    expect(nodes.length).toBeGreaterThanOrEqual(4);
    expect(clocks.length).toBeGreaterThanOrEqual(2);
    for (const c of nodes) expect(c.steps.length).toBeGreaterThan(0);
  });

  for (const c of nodes) {
    it(`reproduces Go exactly: ${c.name}`, () => {
      const node = new OfflineNode({ nodeID: c.node_id, fy: c.fy, seed: BigInt(c.seed) });
      const minted: string[] = [];

      for (const s of c.steps) {
        if (s.kind === "create") {
          minted.push(node.createVoucher(s.wall_millis, "Journal", { voucher_date: "2025-05-20" }));
        } else if (s.kind === "alter") {
          const target = at(minted, s.target_step, "minted id");
          node.alterVoucher(s.wall_millis, target, s.base_version, "Journal", {
            voucher_date: "2025-05-20",
          });
          minted.push(target);
        } else {
          const target = at(minted, s.target_step, "minted id");
          node.deleteVoucher(s.wall_millis, target, s.base_version, s.class);
          minted.push(target);
        }
      }

      const pending = node.pending();
      expect(pending).toHaveLength(c.steps.length);

      c.steps.forEach((s, i) => {
        const got = at(pending, i, "pending intent");
        expect(got.client_voucher_id, `step ${i} client_voucher_id`).toBe(s.client_voucher_id);
        expect(got.idempotency_key, `step ${i} idempotency_key`).toBe(s.idempotency_key);
        expect(got.provisional_no, `step ${i} provisional_no`).toBe(s.provisional_no);
        expect(got.hlc, `step ${i} hlc`).toEqual(s.hlc);
        expect(got.base_version, `step ${i} base_version`).toBe(s.base_version);
      });
    });
  }

  for (const c of clocks) {
    it(`reproduces the Go clock exactly: ${c.name}`, () => {
      const h = new HLC(c.node_id);
      for (const [i, op] of c.ops.entries()) {
        const got = op.kind === "tick" ? h.tick(op.wall_millis) : h.update(op.wall_millis, op.remote);
        expect(got, `op ${i} (${op.kind})`).toEqual(op.got);
      }
    });
  }
});

describe("the properties the vectors exist to protect", () => {
  it("mints DISJOINT id spaces for two node ids at the same wall millisecond", () => {
    // A property, not a list (HANDOFF rule 8): whatever the ids are, two nodes
    // must never produce the same one. This is what makes collision impossible
    // rather than merely unlikely, and it is the reason crypto.randomUUID is
    // wrong here.
    const a = new OfflineNode({ nodeID: "node-a", fy: "2025-26" });
    const b = new OfflineNode({ nodeID: "node-b", fy: "2025-26" });
    const ids = new Set<string>();
    for (let i = 0; i < 200; i++) {
      ids.add(a.createVoucher(1756300000000, "Journal", {}));
      ids.add(b.createVoucher(1756300000000, "Journal", {}));
    }
    expect(ids.size).toBe(400);
  });

  it("never lowers an id's time prefix when the clock runs backwards", () => {
    // The browser case: a laptop that sleeps on a train and wakes with a
    // backwards clock must not start minting ids that sort before earlier ones.
    const n = new OfflineNode({ nodeID: "node-a", fy: "2025-26" });
    const ids = [
      n.createVoucher(1756300005000, "Journal", {}),
      n.createVoucher(1756300000000, "Journal", {}),
      n.createVoucher(1756299000000, "Journal", {}),
    ];
    const sorted = [...ids].sort();
    expect(sorted).toEqual(ids);
  });

  it("keeps the provisional number out of voucher_no", () => {
    // ⛔ The server allocates the real number. Sending the provisional as
    // voucher_no is how a PROV- label ends up printed on a document.
    const n = new OfflineNode({ nodeID: "node-a", fy: "2025-26" });
    n.createVoucher(1, "Journal", { voucher_no: "SHOULD-NOT-SURVIVE", voucher_date: "2025-05-20" });
    const intent = at(n.pending(), 0, "pending intent");
    expect(intent.payload.voucher_no).toBe("");
    expect(intent.provisional_no).toContain("PROV-");
  });

  it("settles only the keys the server actually accounted for", () => {
    // A whole-batch refusal returns NO outcomes; clearing the outbox then would
    // destroy work the server never judged.
    const n = new OfflineNode({ nodeID: "node-a", fy: "2025-26" });
    n.createVoucher(1, "Journal", {});
    n.createVoucher(2, "Journal", {});
    const first = at(n.pending(), 0, "pending intent");
    n.settle([first.idempotency_key]);
    expect(n.size()).toBe(1);
    n.settle([]);
    expect(n.size()).toBe(1);
  });
});
