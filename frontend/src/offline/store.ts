/**
 * store.ts — where the offline outbox lives between page loads.
 *
 * ⛔⛔ THE SECURITY ANSWER, WRITTEN DOWN RATHER THAN IMPLIED (the fourth question
 * HANDOFF §3.2 refuses to let this track skip): THIS IS UNENCRYPTED BOOKS ON A
 * SHARED MACHINE. Vouchers an operator keyed offline sit in the browser's own
 * storage in clear text, readable by anyone with the machine and the profile.
 *
 * Encrypting them client-side would be theatre: the key would have to live in
 * the same origin as the ciphertext, so anyone who can read one can read the
 * other. So the decision is to store them in the clear and TELL THE OPERATOR —
 * `OfflineOutboxScreen` says it on the screen. Somebody on a shared back-office
 * PC is entitled to know that keying ten invoices on the train leaves them on
 * the laptop until they commit.
 *
 * What DOES reduce the blast radius, and is implemented here:
 *
 *   1. the store is keyed by (company, subject) — a second operator signing in
 *      on the same browser gets their OWN queue and never inherits one;
 *   2. `clearOutboxStores` is called on sign-out, mirroring the rule
 *      CompanyContext.tsx:60 already documents for the company id ("without the
 *      clear the NEXT user to sign in…").
 *
 * ⚠️ AND WHY THERE ARE TWO IMPLEMENTATIONS. MEASURED: jsdom exposes NO
 * `indexedDB` at all (`typeof globalThis.indexedDB === "undefined"`), and
 * fake-indexeddb is not a dependency of this project. Rather than add one, the
 * scoping and snapshot rules — the parts with decisions in them — live in this
 * file's pure layer and are unit-tested, while the IndexedDB adapter is thin
 * enough that the browser drive is its proof.
 *
 * ⛔ THE FALLBACK IS NOT SILENT. A browser without IndexedDB (private mode, a
 * locked-down profile) gets `durable: false`, and the screen must SAY the queue
 * will not survive a reload. A queue that quietly forgets is worse than no queue
 * at all, because the operator has already trusted it with the work.
 */

import type { SyncIntent } from "../api/types";

import type { KnownHeads } from "./outbox";

/** `OfflineNode.snapshot()`, plus the node identity that minted it. */
export interface OutboxSnapshot {
  outbox: SyncIntent[];
  knownHeads: KnownHeads;
  seq: number;
  /**
   * ⛔⛔ THE NODE ID MUST PERSIST, AND THIS IS NOT TIDINESS. `NodeIdGen` is
   * DETERMINISTIC: the same nodeID and the same call count mint the SAME uuid,
   * and the idempotency key is `<nodeID>:CREATE:<clientVoucherID>`. So a node id
   * derived from anything two machines share — the company, the operator's
   * subject, a hash of both — would have the operator's laptop and the back
   * office mint the same key for two DIFFERENT vouchers, and the server's
   * exactly-once replay would answer `DUPLICATE` and silently drop the second.
   * The id is therefore random per browser profile, minted once, and kept here so
   * a reload does not mint another.
   */
  nodeID: string;
}

export interface OutboxStore {
  /**
   * Does anything written here survive a reload? False on the memory fallback.
   * ⛔ Render it — see the file header.
   */
  readonly durable: boolean;
  /** The store's identity, for display and for tests. */
  readonly key: string;
  load(): Promise<OutboxSnapshot | null>;
  save(snapshot: OutboxSnapshot): Promise<void>;
  clear(): Promise<void>;
}

const DB_NAME = "tally-offline-outbox";
const DB_VERSION = 1;
const OBJECT_STORE = "outboxes";

/**
 * The record key for one operator's queue at one company.
 *
 * ⛔ BOTH HALVES ARE REQUIRED AND THE SUBJECT IS THE ONE THAT IS EASY TO FORGET.
 * Keying on the company alone would hand the next person to sign in on this
 * browser the previous operator's un-committed vouchers — which they could then
 * commit, under their own name, without ever having seen them keyed.
 */
export function outboxKey(companyID: string, subject: string): string {
  return `${companyID}::${subject}`;
}

/**
 * An in-memory store. Used when IndexedDB is absent, and by unit tests.
 *
 * The map is module-scoped rather than per-instance so that two stores opened
 * for the same (company, subject) in one page see the same data — which is what
 * IndexedDB does, and a fallback that behaved differently would make the
 * fallback path a separate set of bugs.
 */
const memoryOutboxes = new Map<string, OutboxSnapshot>();

export function memoryOutboxStore(companyID: string, subject: string): OutboxStore {
  const key = outboxKey(companyID, subject);
  return {
    durable: false,
    key,
    async load() {
      const got = memoryOutboxes.get(key);
      return got ? structuredCloneSnapshot(got) : null;
    },
    async save(snapshot) {
      memoryOutboxes.set(key, structuredCloneSnapshot(snapshot));
    },
    async clear() {
      memoryOutboxes.delete(key);
    },
  };
}

/** Deep-ish copy so a caller mutating its snapshot cannot reach into the store. */
function structuredCloneSnapshot(s: OutboxSnapshot): OutboxSnapshot {
  return {
    outbox: s.outbox.map((i) => ({ ...i, hlc: { ...i.hlc }, payload: { ...i.payload } })),
    knownHeads: { ...s.knownHeads },
    seq: s.seq,
    // ⛔ FIELD-BY-FIELD, SO AN ADDITION MUST BE MADE HERE TOO OR IT IS SILENTLY
    // DROPPED — and a dropped nodeID is re-minted on reload, which orphans every
    // provisional number the operator has already read out loud.
    nodeID: s.nodeID,
  };
}

/** Is a real IndexedDB available in this environment? */
export function indexedDbAvailable(): boolean {
  try {
    return typeof globalThis.indexedDB !== "undefined" && globalThis.indexedDB !== null;
  } catch {
    // A locked-down profile can THROW on property access rather than return
    // undefined; that is still "not available".
    return false;
  }
}

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = globalThis.indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(OBJECT_STORE)) db.createObjectStore(OBJECT_STORE);
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error ?? new Error("indexedDB open failed"));
  });
}

function tx<T>(mode: IDBTransactionMode, fn: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return openDb().then(
    (db) =>
      new Promise<T>((resolve, reject) => {
        const t = db.transaction(OBJECT_STORE, mode);
        const req = fn(t.objectStore(OBJECT_STORE));
        req.onsuccess = () => resolve(req.result);
        req.onerror = () => reject(req.error ?? new Error("indexedDB request failed"));
        t.oncomplete = () => db.close();
      }),
  );
}

/** The real store. Its proof is the browser drive, not a unit test. */
export function indexedDbOutboxStore(companyID: string, subject: string): OutboxStore {
  const key = outboxKey(companyID, subject);
  return {
    durable: true,
    key,
    async load() {
      const got = await tx<OutboxSnapshot | undefined>("readonly", (s) => s.get(key));
      return got ?? null;
    },
    async save(snapshot) {
      await tx("readwrite", (s) => s.put(snapshot, key));
    },
    async clear() {
      await tx("readwrite", (s) => s.delete(key));
    },
  };
}

/**
 * The store to use. IndexedDB when the browser has it, the memory fallback
 * otherwise — and the caller can tell which by reading `durable`.
 */
export function openOutboxStore(companyID: string, subject: string): OutboxStore {
  return indexedDbAvailable()
    ? indexedDbOutboxStore(companyID, subject)
    : memoryOutboxStore(companyID, subject);
}

/**
 * Drop every queue this browser holds. Called on SIGN-OUT.
 *
 * ⛔ IT DELETES OTHER PEOPLE'S QUEUES TOO, DELIBERATELY. Signing out is the
 * moment the machine stops being this operator's, and leaving a queue keyed to a
 * subject nobody is signed in as is how un-committed books outlive the session
 * that made them. Anything not committed by sign-out is lost — which is why the
 * screen tells the operator the queue is not a filing cabinet.
 */
export async function clearOutboxStores(): Promise<void> {
  memoryOutboxes.clear();
  if (!indexedDbAvailable()) return;
  try {
    await tx("readwrite", (s) => s.clear());
  } catch {
    // Best effort: a failed clear must not block sign-out. The alternative —
    // refusing to sign out because storage misbehaved — strands the operator
    // signed in, which is worse than the residue.
  }
}
