/**
 * store.test.ts — the decisions in the outbox store, tested where they live.
 *
 * ⚠️ WHAT THIS FILE DELIBERATELY DOES NOT TEST: the IndexedDB adapter. MEASURED,
 * jsdom exposes no `indexedDB` at all and fake-indexeddb is not a dependency
 * here, so an "IndexedDB test" in this environment would exercise the MEMORY
 * fallback while appearing to cover the real thing — a refusal for the wrong
 * reason (HANDOFF rule 5), and the worst kind because it reads as coverage. The
 * adapter's proof is the browser drive; the SCOPING and SNAPSHOT rules, which are
 * where the decisions are, are proved here.
 */

import { beforeEach, describe, expect, it } from "vitest";

import type { SyncIntent } from "../api/types";

import { OfflineNode } from "./outbox";
import {
  clearOutboxStores,
  indexedDbAvailable,
  memoryOutboxStore,
  openOutboxStore,
  outboxKey,
} from "./store";

const ALICE = "user:alice";
const BOB = "user:bob";
const CO_A = "company-a";
const CO_B = "company-b";

function nodeWith(n: number): { outbox: SyncIntent[]; knownHeads: Record<string, number>; seq: number } {
  const node = new OfflineNode({ nodeID: "node-a", fy: "2025-26" });
  for (let i = 0; i < n; i++) node.createVoucher(1756300000000 + i, "Journal", { narration: `v${i}` });
  return node.snapshot();
}

beforeEach(async () => {
  await clearOutboxStores();
});

describe("the store is scoped so no operator inherits another's queue", () => {
  it("gives two SUBJECTS at one company separate queues", async () => {
    // ⛔ The failure this prevents: the next person to sign in on a shared
    // back-office PC finding un-committed vouchers they never keyed, and
    // committing them under their own name.
    const alice = memoryOutboxStore(CO_A, ALICE);
    const bob = memoryOutboxStore(CO_A, BOB);

    await alice.save(nodeWith(3));
    expect((await alice.load())?.outbox).toHaveLength(3);
    expect(await bob.load()).toBeNull();
  });

  it("gives two COMPANIES for one subject separate queues", async () => {
    const atA = memoryOutboxStore(CO_A, ALICE);
    const atB = memoryOutboxStore(CO_B, ALICE);

    await atA.save(nodeWith(2));
    expect((await atA.load())?.outbox).toHaveLength(2);
    expect(await atB.load()).toBeNull();
  });

  it("keys on BOTH halves, so neither alone can collide", () => {
    // A property rather than a literal key format: whatever the spelling, the
    // four combinations must be four distinct keys.
    const keys = new Set([
      outboxKey(CO_A, ALICE),
      outboxKey(CO_A, BOB),
      outboxKey(CO_B, ALICE),
      outboxKey(CO_B, BOB),
    ]);
    expect(keys.size).toBe(4);
  });
});

describe("what the store hands back", () => {
  it("round-trips an outbox with its clock state and sequence", async () => {
    const store = memoryOutboxStore(CO_A, ALICE);
    const snap = nodeWith(2);
    await store.save(snap);

    const back = await store.load();
    expect(back).not.toBeNull();
    expect(back?.outbox.map((i) => i.idempotency_key)).toEqual(
      snap.outbox.map((i) => i.idempotency_key),
    );
    // ⛔ seq must survive. It is what makes the NEXT provisional number unique;
    // a restored node that restarts at 1 re-issues labels the operator has
    // already seen on different vouchers.
    expect(back?.seq).toBe(snap.seq);
    expect(back?.knownHeads).toEqual(snap.knownHeads);
  });

  it("a restored node keeps numbering where it left off", async () => {
    const store = memoryOutboxStore(CO_A, ALICE);
    const first = new OfflineNode({ nodeID: "node-a", fy: "2025-26" });
    first.createVoucher(1756300000000, "Journal", {});
    first.createVoucher(1756300001000, "Journal", {});
    await store.save(first.snapshot());

    const restored = new OfflineNode({
      nodeID: "node-a",
      fy: "2025-26",
      restored: (await store.load()) ?? undefined,
    });
    restored.createVoucher(1756300002000, "Journal", {});

    const labels = restored.pending().map((i) => i.provisional_no);
    expect(new Set(labels).size).toBe(labels.length);
    expect(labels).toHaveLength(3);
  });

  it("does not let a caller mutate the stored copy through its own snapshot", async () => {
    const store = memoryOutboxStore(CO_A, ALICE);
    const snap = nodeWith(1);
    await store.save(snap);
    snap.outbox.length = 0;
    snap.seq = 999;

    const back = await store.load();
    expect(back?.outbox).toHaveLength(1);
    expect(back?.seq).not.toBe(999);
  });
});

describe("sign-out and the honest durability flag", () => {
  it("clearOutboxStores drops EVERY queue, not only the current one", async () => {
    // Signing out is the moment the machine stops being this operator's.
    const alice = memoryOutboxStore(CO_A, ALICE);
    const bob = memoryOutboxStore(CO_B, BOB);
    await alice.save(nodeWith(1));
    await bob.save(nodeWith(1));

    await clearOutboxStores();

    expect(await alice.load()).toBeNull();
    expect(await bob.load()).toBeNull();
  });

  it("reports durable=false when there is no IndexedDB, rather than pretending", () => {
    // ⛔ THE POINT OF THIS ASSERTION. In this environment there is no IndexedDB
    // (measured), so openOutboxStore MUST hand back the fallback and say so. A
    // store that claimed durability here would be a queue that silently forgets
    // the operator's work on reload — worse than no queue, because they have
    // already trusted it.
    expect(indexedDbAvailable()).toBe(false);
    const store = openOutboxStore(CO_A, ALICE);
    expect(store.durable).toBe(false);
  });
});
