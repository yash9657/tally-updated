/**
 * OfflineOutbox.tsx — the one live offline node in the running application.
 *
 * ⛔⛔ WHAT THIS FILE EXISTS TO FIX. `src/offline/` held a complete, golden-proved
 * port of `internal/sync/node.go` — outbox, HLC, node-partitioned UUIDv7,
 * provisional numbering, all three idempotency-key formats — and
 * `/usr/bin/grep -rn "OfflineNode" src/ | grep -v ^src/offline/` was EMPTY. Every
 * piece existed and NOTHING COULD PUT A VOUCHER IN IT. Verifying that the pieces
 * exist is not verifying that they compose.
 *
 * ## The three rules this provider is built around
 *
 * 1. ⛔ **A NODE ID IS RANDOM PER BROWSER PROFILE AND IT IS PERSISTED.**
 *    `NodeIdGen` is deterministic: the same node id and the same call count mint
 *    the SAME voucher uuid, and the CREATE idempotency key is
 *    `<nodeID>:CREATE:<clientVoucherID>`. A node id derived from the company, the
 *    subject, or a hash of both would have two machines mint one key for two
 *    DIFFERENT vouchers — and the server's exactly-once replay would answer
 *    `DUPLICATE` and silently drop the second. So it is `crypto.randomUUID()`,
 *    minted once, stored in the snapshot beside the queue.
 *
 * 2. ⛔ **A REFUSED BATCH SETTLES NOTHING.** `settle` may only be called with
 *    keys that came back in an outcome. See `SyncBatchRefusal`: the three
 *    request-level failures return no outcomes at all, every intent is still
 *    valid, and clearing on one destroys work the server never judged.
 *
 * 3. ⚠️ **A MISSING PROVIDER MUST NOT SILENTLY DISABLE THE QUEUE.**
 *    `useOfflineOutbox()` returns null outside a provider, and the accept path
 *    treats null as "cannot queue" and REFUSES rather than pretending. That keeps
 *    standalone screen tests honest — and it is why `AppShell.test.tsx` asserts
 *    the provider is actually mounted in the real shell, which is the only place
 *    the hole could hide.
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import type { CoredClient } from "../api";
import type {
  SyncBatchRefusal,
  SyncIntent,
  SyncOutcome,
  VoucherRequest,
} from "../api/types";
import { useOptionalCompanyContext } from "../app/CompanyContext";
import { useAuth } from "../auth";

import { classifyBatchRefusal } from "./commitRefusal";
import { fyTagFor } from "./fyTag";
import { OfflineNode } from "./outbox";
import { openOutboxStore, type OutboxStore } from "./store";

/**
 * What a drain produced. ⛔ THE TWO ARMS ARE NOT DEGREES OF THE SAME THING:
 * `outcomes` is a per-voucher verdict list and `refusal` is a statement about the
 * REQUEST, with the whole queue still intact behind it.
 */
export type CommitResult =
  | { readonly kind: "outcomes"; readonly outcomes: SyncOutcome[] }
  | { readonly kind: "refusal"; readonly refusal: SyncBatchRefusal }
  | { readonly kind: "empty" };

export interface OfflineOutboxValue {
  /** Has the stored queue been read back yet? Nothing may be queued before this. */
  readonly ready: boolean;
  /** ⛔ False on the memory fallback — the queue will NOT survive a reload. */
  readonly durable: boolean;
  /** This browser profile's node id. Shown so a provisional number is traceable. */
  readonly nodeID: string;
  /** The queued intents, oldest first. */
  readonly pending: readonly SyncIntent[];
  /**
   * Append a CREATE. Returns the PROVISIONAL number to show the operator.
   * ⛔ Call this only when {@link shouldQueueOffline} said to.
   */
  queueCreate(cls: string, payload: VoucherRequest): Promise<string>;
  /**
   * Append an ALTER at the head version this node last observed. A base the
   * server has moved past is what parks a CONFLICT — which is the honest outcome,
   * and the reason `baseVersion` is a REQUIRED argument rather than a default: an
   * ALTER queued at base 0 would park every time and tell the operator nothing.
   */
  queueAlter(
    targetID: string,
    baseVersion: number,
    cls: string,
    payload: VoucherRequest,
  ): Promise<string>;
  /** Record that a voucher was observed at a head version (seeds an ALTER's base). */
  learn(voucherID: string, version: number): void;
  /** Drain the WHOLE queue in one write. See CoredClient.syncCommit. */
  commit(): Promise<CommitResult>;
  /**
   * Drop one queued intent because the OPERATOR said so.
   *
   * ⚠️ THIS IS THE ONE LEGITIMATE CALLER OF `settle` THAT IS NOT AN OUTCOME, and
   * it is required by the cutoff case: one pre-cutoff voucher refuses the entire
   * batch, so the operator must be able to remove that row and commit the rest.
   * It is safe for exactly the reason a batch-refusal clear is not — a human named
   * this row, having seen it.
   */
  discard(idempotencyKey: string): Promise<void>;
}

const OfflineOutboxContext = createContext<OfflineOutboxValue | null>(null);

/** Null outside a provider. ⛔ The accept path must then REFUSE, never pretend. */
export function useOfflineOutbox(): OfflineOutboxValue | null {
  return useContext(OfflineOutboxContext);
}

export interface OfflineOutboxProviderProps {
  children: ReactNode;
  /** Overrides for tests; production reads all three from context. */
  companyID?: string | null;
  subject?: string | null;
  client?: CoredClient;
  /** Injected store, so a test need not have IndexedDB (jsdom has none). */
  makeStore?: (companyID: string, subject: string) => OutboxStore;
  /** Injected clock. Wall millis land in the HLC and the UUIDv7 prefix. */
  now?: () => number;
}

export function OfflineOutboxProvider({
  children,
  companyID: companyOverride,
  subject: subjectOverride,
  client: clientOverride,
  makeStore = openOutboxStore,
  now = () => Date.now(),
}: OfflineOutboxProviderProps) {
  // ⚠️ BOTH CONTEXTS ARE OPTIONAL HERE, AND THAT IS NOT LAXNESS. This provider is
  // mounted by AppShell above every screen, so it must not throw on the paths
  // where a company has not been chosen yet — and its overrides exist so a test
  // can drive a real queue with no shell around it, which a throwing accessor
  // would prevent before the overrides were ever read.
  const ctx = useOptionalCompanyContext();
  const auth = useAuth();

  const companyID = companyOverride ?? ctx?.company?.id ?? null;
  const subject = subjectOverride ?? auth?.session?.user.subject ?? null;
  const client = clientOverride ?? ctx?.client ?? null;

  const [ready, setReady] = useState(false);
  const [durable, setDurable] = useState(false);
  const [nodeID, setNodeID] = useState("");
  const [pending, setPending] = useState<readonly SyncIntent[]>([]);

  // The node and its store are refs, not state: `queueCreate` must append
  // synchronously against the CURRENT node, and a re-render between the failed
  // post and the append would otherwise hand it a stale one.
  const nodeRef = useRef<OfflineNode | null>(null);
  const storeRef = useRef<OutboxStore | null>(null);

  useEffect(() => {
    // ⛔ A CHANGE OF COMPANY OR OPERATOR IS A DIFFERENT QUEUE, NEVER A CONTINUED
    // ONE. Dropping the node here is what stops the previous operator's
    // un-committed vouchers being committed under the next one's name.
    nodeRef.current = null;
    storeRef.current = null;
    setReady(false);
    setPending([]);
    setNodeID("");
    if (!companyID || !subject) return;

    let live = true;
    const store = makeStore(companyID, subject);
    (async () => {
      const restored = await store.load().catch(() => null);
      if (!live) return;
      // ⛔ MINTED ONLY WHEN THERE IS NOTHING STORED. Re-minting on every load
      // would orphan every provisional number the operator has already read out
      // to a customer, and would break the id-space disjointness the ported
      // generator provides.
      const id = restored?.nodeID ?? newNodeID();
      nodeRef.current = new OfflineNode({
        nodeID: id,
        fy: fyTagFor(new Date(now())),
        ...(restored
          ? {
              restored: {
                outbox: restored.outbox,
                knownHeads: restored.knownHeads,
                seq: restored.seq,
              },
            }
          : {}),
      });
      storeRef.current = store;
      setNodeID(id);
      setDurable(store.durable);
      setPending(nodeRef.current.pending());
      setReady(true);
    })();
    return () => {
      live = false;
    };
    // `makeStore` and `now` are injected once per test; re-running on identity
    // changes would reload the queue on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [companyID, subject]);

  /** Persist, then republish. Both, in that order, after every mutation. */
  const flush = useCallback(async () => {
    const node = nodeRef.current;
    const store = storeRef.current;
    if (!node) return;
    const snap = node.snapshot();
    setPending(snap.outbox);
    if (store) {
      // ⚠️ BEST EFFORT, AND THE SCREEN ALREADY SAYS SO. A failed write must not
      // lose the intent from the in-memory queue as well — that would turn a
      // storage problem into lost work inside the same session.
      await store.save({ ...snap, nodeID: node.nodeID }).catch(() => undefined);
    }
  }, []);

  const requireNode = useCallback((): OfflineNode => {
    const node = nodeRef.current;
    if (!node) {
      throw new Error(
        "the offline queue is not ready (no company, no signed-in operator, or storage has not been read back yet)",
      );
    }
    return node;
  }, []);

  const queueCreate = useCallback(
    async (cls: string, payload: VoucherRequest) => {
      const node = requireNode();
      const before = node.size();
      node.createVoucher(now(), cls, payload);
      const appended = node.pending()[before];
      await flush();
      // The provisional number of the intent just appended — read back from the
      // outbox rather than recomputed, so the string shown is the string stored.
      return appended?.provisional_no ?? "";
    },
    [flush, now, requireNode],
  );

  const queueAlter = useCallback(
    async (targetID: string, baseVersion: number, cls: string, payload: VoucherRequest) => {
      const node = requireNode();
      const before = node.size();
      node.alterVoucher(now(), targetID, baseVersion, cls, payload);
      const appended = node.pending()[before];
      await flush();
      return appended?.provisional_no ?? "";
    },
    [flush, now, requireNode],
  );

  const learn = useCallback((voucherID: string, version: number) => {
    nodeRef.current?.learn(voucherID, version);
  }, []);

  const commit = useCallback(async (): Promise<CommitResult> => {
    const node = nodeRef.current;
    if (!node || node.size() === 0) return { kind: "empty" };
    if (!companyID) return { kind: "empty" };
    // ⛔ NO CLIENT IS NOT AN EMPTY QUEUE. It means this provider was built with
    // neither a CompanyProvider nor an explicit client, so there is nothing to
    // send to — and the queue must be left exactly as it is.
    if (!client) {
      return {
        kind: "refusal",
        refusal: {
          kind: "other",
          status: 0,
          message:
            "There is no connection configured for this session, so nothing was sent. Every voucher below is still queued.",
        },
      };
    }
    const intents = node.pending();
    let res;
    try {
      res = await client.syncCommit(companyID, intents);
    } catch (err: unknown) {
      // ⛔ NOTHING IS SETTLED HERE. Rule 2 of this file's header.
      return { kind: "refusal", refusal: classifyBatchRefusal(err) };
    }
    // ⛔ ONLY THE KEYS THAT CAME BACK. A server that answered for fewer intents
    // than were sent leaves the rest queued, which is the correct reading of a
    // partial answer.
    node.settle(res.outcomes.map((o) => o.idempotency_key));
    // A COMMITTED outcome is also an observation of the new head, so the node
    // learns it — an immediately following ALTER then carries a live base
    // instead of parking as a conflict.
    for (const o of res.outcomes) {
      if (o.status === "COMMITTED" && o.version_no > 0) {
        node.learn(o.client_voucher_id, o.version_no);
      }
    }
    await flush();
    return { kind: "outcomes", outcomes: res.outcomes };
  }, [client, companyID, flush]);

  const discard = useCallback(
    async (idempotencyKey: string) => {
      const node = nodeRef.current;
      if (!node) return;
      node.settle([idempotencyKey]);
      await flush();
    },
    [flush],
  );

  const value = useMemo<OfflineOutboxValue>(
    () => ({
      ready,
      durable,
      nodeID,
      pending,
      queueCreate,
      queueAlter,
      learn,
      commit,
      discard,
    }),
    [ready, durable, nodeID, pending, queueCreate, queueAlter, learn, commit, discard],
  );

  return (
    <OfflineOutboxContext.Provider value={value}>{children}</OfflineOutboxContext.Provider>
  );
}

/**
 * A fresh node id. ⛔ RANDOM, NEVER DERIVED — see rule 1 in the file header.
 * `crypto.randomUUID` needs a secure context; the fallback keeps the property
 * that matters (two profiles do not collide) without pretending to be as strong.
 */
function newNodeID(): string {
  const c: Crypto | undefined = globalThis.crypto;
  if (c && typeof c.randomUUID === "function") return c.randomUUID();
  const bytes = new Uint8Array(8);
  if (c && typeof c.getRandomValues === "function") c.getRandomValues(bytes);
  else for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
  return `node-${Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("")}`;
}
