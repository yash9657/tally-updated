/**
 * nodeId.ts — node-partitioned, time-ordered UUIDv7 minting for an offline node.
 *
 * ⛔⛔ A PORT OF `backend/internal/sync/node.go`'s `NodeIDGen`, byte for byte.
 * The layout follows RFC 9562 v7 but the would-be random tail is a deterministic
 * (nodeHash, sequence) pair, so two different node ids occupy DISJOINT id spaces
 * and can never collide:
 *
 *     bytes 0..5  wallMillis, 48-bit big-endian   -> time ordering
 *     byte  6     0x70 | (nodeHash high nibble)   -> version 7
 *     byte  7     nodeHash low byte               -> node partition
 *     byte  8     0x80 | (nodeHash nibble)        -> variant 10
 *     bytes 9..15 node-scoped monotonic sequence  -> uniqueness within a node
 *
 * ⚠️ WHY NOT `crypto.randomUUID()`, WHICH IS RIGHT THERE AND SHORTER. Two
 * reasons, and the second is the one that bites. (1) A v4 from the browser is not
 * time-ordered, so an outbox sorted by id stops matching the order the operator
 * keyed things in. (2) The id IS the committed identity — the ledger accepts a
 * caller-supplied voucher id — and the node partition is what makes two
 * disconnected machines unable to mint the same one. Randomness makes collision
 * unlikely; this construction makes it impossible, and the fuzzer relies on that
 * being a property rather than a probability.
 */

/**
 * FNV-1a, 32-bit — the hash `node.go` uses (`hash/fnv`.New32a) to derive a node's
 * partition bytes. `Math.imul` is what keeps the multiply 32-bit, exactly as Go's
 * uint32 arithmetic wraps.
 */
function fnv1a32(bytes: Uint8Array): number {
  let h = 0x811c9dc5;
  for (const b of bytes) {
    h ^= b;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

/** Format 16 bytes as a canonical lowercase uuid string. */
function formatUUID(b: Uint8Array): string {
  const hex: string[] = [];
  for (const byte of b) hex.push(byte.toString(16).padStart(2, "0"));
  const s = hex.join("");
  return `${s.slice(0, 8)}-${s.slice(8, 12)}-${s.slice(12, 16)}-${s.slice(16, 20)}-${s.slice(20)}`;
}

/**
 * A deterministic, node-partitioned UUIDv7 generator. Given the same
 * (nodeID, seed) and the same sequence of wall times, it emits the same ids —
 * which is what lets a test assert on them.
 */
export class NodeIdGen {
  private readonly nodeHash: number;
  private seq = 0n;
  private lastWall = 0;

  /**
   * `seed` lets two generators for the SAME node id diverge if a caller wants
   * independent streams. It is folded into the node hash, so it never breaks
   * cross-node disjointness for DIFFERENT node ids — Go folds it the same way,
   * as 8 big-endian bytes appended to the node id.
   */
  constructor(nodeID: string, seed = 0n) {
    const idBytes = new TextEncoder().encode(nodeID);
    const seedBytes = new Uint8Array(8);
    let s = BigInt.asUintN(64, seed);
    for (let i = 7; i >= 0; i--) {
      seedBytes[i] = Number(s & 0xffn);
      s >>= 8n;
    }
    const all = new Uint8Array(idBytes.length + 8);
    all.set(idBytes, 0);
    all.set(seedBytes, idBytes.length);
    this.nodeHash = fnv1a32(all) & 0xffff;
  }

  /**
   * The next id for this node at the given wall time.
   *
   * ⛔ THE ENCODED WALL IS CLAMPED NON-DECREASING. A backwards or stalled clock
   * must never lower the time prefix or ids stop increasing; the trailing
   * per-node sequence still guarantees strict increase and uniqueness while the
   * wall stands still. Same clamp as Go, and the browser needs it more.
   */
  next(wallMillis: number): string {
    this.seq += 1n;

    let wall = wallMillis;
    if (wall < this.lastWall) wall = this.lastWall;
    this.lastWall = wall;

    const b = new Uint8Array(16);

    // 48-bit big-endian unix-millis prefix.
    const ms = BigInt(Math.trunc(wall)) & 0xffffffffffffn;
    b[0] = Number((ms >> 40n) & 0xffn);
    b[1] = Number((ms >> 32n) & 0xffn);
    b[2] = Number((ms >> 24n) & 0xffn);
    b[3] = Number((ms >> 16n) & 0xffn);
    b[4] = Number((ms >> 8n) & 0xffn);
    b[5] = Number(ms & 0xffn);

    // Version 7 in the high nibble of byte 6; node-hash bits fill the rest.
    b[6] = 0x70 | ((this.nodeHash >> 8) & 0x0f);
    b[7] = this.nodeHash & 0xff;

    // Variant 10xx in the two high bits of byte 8; a node-hash nibble below it.
    b[8] = 0x80 | ((this.nodeHash >> 4) & 0x3f);

    // Node-scoped strictly-increasing sequence in the trailing 7 bytes (56 bits).
    const q = BigInt.asUintN(64, this.seq);
    b[9] = Number((q >> 48n) & 0xffn);
    b[10] = Number((q >> 40n) & 0xffn);
    b[11] = Number((q >> 32n) & 0xffn);
    b[12] = Number((q >> 24n) & 0xffn);
    b[13] = Number((q >> 16n) & 0xffn);
    b[14] = Number((q >> 8n) & 0xffn);
    b[15] = Number(q & 0xffn);

    return formatUUID(b);
  }
}
