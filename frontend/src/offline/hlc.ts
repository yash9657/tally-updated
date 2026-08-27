/**
 * hlc.ts — a Hybrid Logical Clock for one offline node.
 *
 * ⛔⛔ THIS IS A PORT OF `backend/internal/sync/hlc.go`, NOT A DESIGN. Every rule
 * below is the Go file's rule, and the Go file is the one the partition fuzzer
 * (`internal/sync/fuzz_test.go`) drives. If the two ever disagree, the Go side is
 * right and this file is the bug — the cloud sequencer picks the conflict winner
 * by the total order these timestamps define, so a divergence here does not fail
 * loudly, it silently changes WHO WINS a conflict.
 *
 * THE CLOCK NEVER READS THE SYSTEM WALL CLOCK. The wall-millis value is always
 * supplied by the caller, exactly as in Go, which is what makes a node's event
 * stream reproducible in a test. Callers in the app pass `Date.now()`; callers in
 * a test pass a fixed sequence and get identical output every run.
 */

/** One hybrid-logical-clock reading. Mirrors `sync.HLCTimestamp` on the wire. */
export interface HLCTimestamp {
  wall_millis: number;
  counter: number;
  node_id: string;
}

/**
 * A hybrid logical clock for one node: the largest wall time it has observed
 * (its own ticks or a remote merge) plus a logical counter that breaks ties
 * within one wall millisecond.
 *
 * Not safe for concurrent use — one node owns one clock and drives it
 * sequentially, as in Go.
 */
export class HLC {
  private wall = 0;
  private counter = 0;

  constructor(private readonly nodeID: string) {}

  /**
   * Advance the clock for a LOCAL event and return that event's timestamp.
   *
   * A wall time ahead of the last one is adopted and resets the counter; a
   * stalled OR BACKWARDS wall keeps the last wall and increments the counter
   * instead. That is what guarantees per-node monotonicity whatever the machine's
   * clock does — and it matters more in a browser than it did in Go, because a
   * laptop that sleeps on a train and wakes in another timezone is the ordinary
   * case for this feature rather than an edge one.
   */
  tick(wallMillis: number): HLCTimestamp {
    if (wallMillis > this.wall) {
      this.wall = wallMillis;
      this.counter = 0;
    } else {
      this.counter++;
    }
    return { wall_millis: this.wall, counter: this.counter, node_id: this.nodeID };
  }

  /**
   * Merge a remote timestamp on receive: advance to the max of (own wall, the
   * supplied local wall, the remote wall) and pick a counter strictly greater
   * than any counter seen at that wall. The standard HLC merge rule, and a
   * clause-for-clause port of `HLC.Update`.
   */
  update(wallMillis: number, remote: HLCTimestamp): HLCTimestamp {
    let maxWall = this.wall;
    if (wallMillis > maxWall) maxWall = wallMillis;
    if (remote.wall_millis > maxWall) maxWall = remote.wall_millis;

    if (maxWall === this.wall && maxWall === remote.wall_millis) {
      // Both local and remote at the leading wall: beat both counters.
      this.counter = remote.counter > this.counter ? remote.counter + 1 : this.counter + 1;
    } else if (maxWall === this.wall) {
      // Local wall leads: bump our own counter.
      this.counter++;
    } else if (maxWall === remote.wall_millis) {
      // Remote wall leads: adopt it and step past the remote counter.
      this.wall = maxWall;
      this.counter = remote.counter + 1;
    } else {
      // The supplied local wall leads both: fresh wall, counter resets.
      this.wall = maxWall;
      this.counter = 0;
    }
    return { wall_millis: this.wall, counter: this.counter, node_id: this.nodeID };
  }

  /** The clock's current reading without advancing it. For display only. */
  peek(): HLCTimestamp {
    return { wall_millis: this.wall, counter: this.counter, node_id: this.nodeID };
  }
}

/**
 * Does `a` sort strictly before `b` under the cloud total order —
 * (wall_millis, counter, node_id) ascending?
 *
 * ⚠️ THIS IS THE SERVER'S TIEBREAK, MIRRORED FOR DISPLAY ONLY. The sequencer
 * decides conflicts with `sync.LessHLC`; nothing the client computes with this
 * changes an outcome. It is here so the queue can show intents in the order the
 * server will consider them, which is the order an operator needs to understand
 * why one of two edits lost.
 */
export function lessHLC(a: HLCTimestamp, b: HLCTimestamp): boolean {
  if (a.wall_millis !== b.wall_millis) return a.wall_millis < b.wall_millis;
  if (a.counter !== b.counter) return a.counter < b.counter;
  return a.node_id < b.node_id;
}
