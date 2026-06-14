package sync

// hlc.go implements a Hybrid Logical Clock (HLC) for the offline node model. The
// clock NEVER reads the system wall clock: the wall-millis value is always passed
// in by the caller. This makes every node's event stream fully deterministic and
// therefore reproducible by the property-based partition fuzzer — a given seed
// always yields the same HLC timestamps, the same total order, and the same
// deterministic conflict winner.
//
// HLCTimestamp is declared by WS1 in types.go; this file references it and owns
// the clock logic plus the LessHLC total-order tiebreak.

// HLC is a hybrid logical clock for one node. It tracks the largest wall time it
// has observed (its own ticks or a remote merge) and a logical counter that breaks
// ties when two events share a wall millisecond. It is NOT safe for concurrent use
// by multiple goroutines; one node owns one *HLC and drives it sequentially.
type HLC struct {
	nodeID  string
	wall    int64
	counter int64
}

// NewHLC constructs a fresh HLC for the given node id, starting at wall=0,
// counter=0.
func NewHLC(nodeID string) *HLC {
	return &HLC{nodeID: nodeID}
}

// Tick advances the clock for a LOCAL event at the supplied wall time and returns
// the event's timestamp. If the supplied wall time is ahead of the clock's last
// wall, the clock jumps to it and resets the counter to 0; otherwise (a stalled or
// backwards wall) the clock keeps its last wall and increments the counter so the
// timestamp is still strictly greater than the previous one. This guarantees
// per-node monotonicity regardless of wall-clock skew.
func (h *HLC) Tick(wallMillis int64) HLCTimestamp {
	if wallMillis > h.wall {
		h.wall = wallMillis
		h.counter = 0
	} else {
		h.counter++
	}
	return HLCTimestamp{WallMillis: h.wall, Counter: h.counter, NodeID: h.nodeID}
}

// Update merges a remote timestamp on receive: the clock advances to the max of
// (its wall, the supplied local wall, the remote wall) and picks a counter that is
// strictly greater than any counter seen at that wall, then returns the resulting
// timestamp. Standard HLC merge rule.
func (h *HLC) Update(wallMillis int64, remote HLCTimestamp) HLCTimestamp {
	maxWall := h.wall
	if wallMillis > maxWall {
		maxWall = wallMillis
	}
	if remote.WallMillis > maxWall {
		maxWall = remote.WallMillis
	}

	switch {
	case maxWall == h.wall && maxWall == remote.WallMillis:
		// Both local and remote at the leading wall: counter beats both.
		if remote.Counter > h.counter {
			h.counter = remote.Counter + 1
		} else {
			h.counter++
		}
	case maxWall == h.wall:
		// Local wall leads: just bump our counter.
		h.counter++
	case maxWall == remote.WallMillis:
		// Remote wall leads: adopt it and step past the remote counter.
		h.wall = maxWall
		h.counter = remote.Counter + 1
	default:
		// The supplied local wall leads both: fresh wall, counter resets.
		h.wall = maxWall
		h.counter = 0
	}
	return HLCTimestamp{WallMillis: h.wall, Counter: h.counter, NodeID: h.nodeID}
}

// LessHLC reports whether a sorts strictly before b under the cloud total order:
// (WallMillis, Counter, NodeID) ascending. This is the deterministic tiebreak the
// sequencer uses to pick the conflict winner — the (HLC, node)-min always wins.
func LessHLC(a, b HLCTimestamp) bool {
	if a.WallMillis != b.WallMillis {
		return a.WallMillis < b.WallMillis
	}
	if a.Counter != b.Counter {
		return a.Counter < b.Counter
	}
	return a.NodeID < b.NodeID
}
