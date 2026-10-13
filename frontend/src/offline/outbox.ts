/**
 * outbox.ts — one offline node: an append-only outbox of voucher intents.
 *
 * ⛔⛔ A PORT OF `backend/internal/sync/node.go`, AND THE IDEMPOTENCY-KEY FORMATS
 * ARE THE PART NOT TO IMPROVISE. The server's exactly-once replay keys off those
 * strings: `sync.Service.lookupApplied` matches on (company, idempotency_key), so
 * a key spelled differently here does not fail loudly — it makes a re-commit
 * apply a second time instead of answering DUPLICATE. The three formats are
 *
 *     <nodeID>:CREATE:<clientVoucherID>
 *     <nodeID>:ALTER:<targetID>:<seq>
 *     <nodeID>:DELETE:<targetID>:<seq>
 *
 * and `node.go` lines 148 / 169 / 186 are the authority.
 *
 * WHAT THIS CLASS DOES NOT DO: it never talks to the network and never mutates
 * the server. Recording a change appends an intent, exactly as in Go. Draining
 * the outbox is the caller's job, in ONE batch — see `SyncBatchRefusal` for why
 * batching is not merely an optimisation.
 */

import type { SyncIntent, SyncOp, VoucherRequest } from "../api/types";

import { HLC } from "./hlc";
import { NodeIdGen } from "./nodeId";

/**
 * The head version this node believes each voucher is at. Seeded by `learn()`
 * from anything the node has observed, and read to fill an ALTER/DELETE's
 * `base_version`. A base the server has moved past is what parks a CONFLICT.
 */
export type KnownHeads = Record<string, number>;

/** Options for constructing a node. `fy` tags the provisional number. */
export interface OfflineNodeOptions {
  nodeID: string;
  /** Financial-year tag, e.g. "2025-26". */
  fy: string;
  seed?: bigint;
  /** Restored state, when the node is being rehydrated from IndexedDB. */
  restored?: {
    outbox?: SyncIntent[];
    knownHeads?: KnownHeads;
    seq?: number;
  };
}

/**
 * One offline desktop node. Owns an HLC, a node-partitioned id generator, a
 * small read-model of head versions, and the outbox.
 */
export class OfflineNode {
  readonly nodeID: string;
  private readonly fy: string;
  private readonly hlc: HLC;
  private readonly gen: NodeIdGen;
  private knownHeads: KnownHeads;
  private outbox: SyncIntent[];
  private seq: number;

  constructor(opts: OfflineNodeOptions) {
    this.nodeID = opts.nodeID;
    this.fy = opts.fy;
    this.hlc = new HLC(opts.nodeID);
    this.gen = new NodeIdGen(opts.nodeID, opts.seed ?? 0n);
    this.knownHeads = { ...(opts.restored?.knownHeads ?? {}) };
    this.outbox = [...(opts.restored?.outbox ?? [])];
    this.seq = opts.restored?.seq ?? 0;
  }

  /**
   * The client-only provisional display number.
   *
   * ⛔ IT IS NEVER SENT AS `voucher_no` — the server allocates the real number.
   * The `PROV` literal and the node id in the middle are deliberate: a number
   * read down a phone line to a customer is then self-evidently not a document
   * number. Format is `node.go:137`.
   */
  private provisionalNo(): string {
    this.seq++;
    return `INV/${this.fy}/PROV-${this.nodeID}-${this.seq}`;
  }

  private nextSeq(): number {
    this.seq++;
    return this.seq;
  }

  /**
   * Record a brand-new voucher created offline. Returns the client voucher id,
   * which IS the identity the server will keep — the ledger accepts a
   * caller-supplied id.
   *
   * The node optimistically records its own head at version 1, exactly as Go
   * does, so an immediate ALTER of a voucher that has not committed yet carries a
   * sane base.
   */
  createVoucher(wallMillis: number, cls: string, payload: VoucherRequest): string {
    const id = this.gen.next(wallMillis);
    const body: VoucherRequest = { ...payload, voucher_no: "" };
    this.outbox.push({
      idempotency_key: `${this.nodeID}:CREATE:${id}`,
      client_voucher_id: id,
      op: "CREATE",
      class: cls,
      provisional_no: this.provisionalNo(),
      hlc: this.hlc.tick(wallMillis),
      node_id: this.nodeID,
      base_version: 0,
      payload: body,
    });
    this.knownHeads[id] = 1;
    return id;
  }

  /**
   * Record an edit superseding `targetID` at the head version this node last
   * observed. If the server's head has moved past it by commit time, the
   * sequencer parks this intent as a CONFLICT.
   */
  alterVoucher(
    wallMillis: number,
    targetID: string,
    baseVersion: number,
    cls: string,
    payload: VoucherRequest,
  ): void {
    const body: VoucherRequest = { ...payload, voucher_no: "" };
    this.outbox.push({
      idempotency_key: `${this.nodeID}:ALTER:${targetID}:${this.nextSeq()}`,
      client_voucher_id: targetID,
      op: "ALTER",
      class: cls,
      provisional_no: this.provisionalNo(),
      hlc: this.hlc.tick(wallMillis),
      node_id: this.nodeID,
      base_version: baseVersion,
      payload: body,
    });
  }

  /** Record a tombstone of `targetID`. A stale base parks as a CONFLICT. */
  deleteVoucher(wallMillis: number, targetID: string, baseVersion: number, cls: string): void {
    this.outbox.push({
      idempotency_key: `${this.nodeID}:DELETE:${targetID}:${this.nextSeq()}`,
      client_voucher_id: targetID,
      op: "DELETE",
      class: cls,
      provisional_no: this.provisionalNo(),
      hlc: this.hlc.tick(wallMillis),
      node_id: this.nodeID,
      base_version: baseVersion,
      // A tombstone carries no postings and no stock lines, exactly as in Go.
      // The CLASS travels on the intent, not in the body: the server resolves it
      // with buildVoucherInput(w, ir.Class, ir.Payload), which is why
      // VoucherRequest has no class field — the online path puts it in the URL.
      payload: {},
    });
  }

  /** Record that `voucherID` was observed at head version `v`. */
  learn(voucherID: string, v: number): void {
    this.knownHeads[voucherID] = v;
  }

  /** The head version this node believes `voucherID` is at, or 0. */
  headOf(voucherID: string): number {
    return this.knownHeads[voucherID] ?? 0;
  }

  /** The pending intents, oldest first. A copy — the outbox is append-only. */
  pending(): SyncIntent[] {
    return [...this.outbox];
  }

  /** How many intents are waiting. */
  size(): number {
    return this.outbox.length;
  }

  /**
   * Drop the intents the server has accounted for, by idempotency key.
   *
   * ⛔ CALL THIS ONLY WITH KEYS THAT CAME BACK IN AN OUTCOME. A whole-batch
   * refusal (cutoff / step-up) returns NO outcomes, and clearing the outbox then
   * would destroy work the server never even judged — see `SyncBatchRefusal`.
   */
  settle(keys: readonly string[]): void {
    const done = new Set(keys);
    this.outbox = this.outbox.filter((i) => !done.has(i.idempotency_key));
  }

  /** Serialisable state, for the IndexedDB store. */
  snapshot(): { outbox: SyncIntent[]; knownHeads: KnownHeads; seq: number } {
    return { outbox: [...this.outbox], knownHeads: { ...this.knownHeads }, seq: this.seq };
  }
}

/** The three ops, for exhaustiveness in screens. */
export const SYNC_OPS: readonly SyncOp[] = ["CREATE", "ALTER", "DELETE"] as const;
