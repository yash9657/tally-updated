package sync

import (
	"sort"
	"testing"
)

// TestHLCTickMonotonic verifies that successive ticks are strictly increasing even
// when the wall clock stalls or runs backwards — per-node monotonicity is the
// foundation of the deterministic total order.
func TestHLCTickMonotonic(t *testing.T) {
	h := NewHLC("nodeA")

	walls := []int64{100, 100, 100, 90, 200, 200, 150}
	prev := HLCTimestamp{}
	first := true
	for _, w := range walls {
		ts := h.Tick(w)
		if !first && !LessHLC(prev, ts) {
			t.Fatalf("tick not strictly increasing: prev=%+v cur=%+v (wall=%d)", prev, ts, w)
		}
		prev = ts
		first = false
	}
}

// TestHLCStalledWallBumpsCounter verifies that repeated ticks at the same wall
// produce 0,1,2,... counters so the timestamps differ.
func TestHLCStalledWallBumpsCounter(t *testing.T) {
	h := NewHLC("n")
	a := h.Tick(500)
	b := h.Tick(500)
	c := h.Tick(500)
	if a.Counter != 0 || b.Counter != 1 || c.Counter != 2 {
		t.Fatalf("counters = %d,%d,%d want 0,1,2", a.Counter, b.Counter, c.Counter)
	}
	if a.WallMillis != 500 || b.WallMillis != 500 || c.WallMillis != 500 {
		t.Fatalf("walls drifted: %d,%d,%d", a.WallMillis, b.WallMillis, c.WallMillis)
	}
}

// TestHLCUpdateMerges verifies the merge rule advances past a remote timestamp and
// stays strictly greater than both inputs.
func TestHLCUpdateMerges(t *testing.T) {
	h := NewHLC("local")
	_ = h.Tick(100) // local at wall=100, counter=0

	remote := HLCTimestamp{WallMillis: 100, Counter: 5, NodeID: "remote"}
	merged := h.Update(100, remote)
	if !LessHLC(remote, merged) {
		t.Fatalf("merged %+v must exceed remote %+v", merged, remote)
	}
	if merged.WallMillis != 100 || merged.Counter <= 5 {
		t.Fatalf("merge at same wall must beat remote counter: got %+v", merged)
	}

	// A remote at a higher wall pulls the clock forward.
	ahead := HLCTimestamp{WallMillis: 1000, Counter: 2, NodeID: "remote"}
	m2 := h.Update(100, ahead)
	if m2.WallMillis != 1000 || !LessHLC(ahead, m2) {
		t.Fatalf("merge with ahead remote: got %+v", m2)
	}

	// A local wall higher than both resets the counter to 0.
	m3 := h.Update(5000, HLCTimestamp{WallMillis: 10, Counter: 0, NodeID: "r"})
	if m3.WallMillis != 5000 || m3.Counter != 0 {
		t.Fatalf("local wall lead must reset counter: got %+v", m3)
	}
}

// TestLessHLCTotalOrder verifies the (Wall, Counter, NodeID) ordering and that it
// is a strict total order over a sample of timestamps (sort is deterministic).
func TestLessHLCTotalOrder(t *testing.T) {
	in := []HLCTimestamp{
		{WallMillis: 200, Counter: 0, NodeID: "b"},
		{WallMillis: 100, Counter: 3, NodeID: "a"},
		{WallMillis: 100, Counter: 3, NodeID: "b"},
		{WallMillis: 100, Counter: 0, NodeID: "z"},
		{WallMillis: 200, Counter: 0, NodeID: "a"},
	}
	want := []HLCTimestamp{
		{WallMillis: 100, Counter: 0, NodeID: "z"},
		{WallMillis: 100, Counter: 3, NodeID: "a"},
		{WallMillis: 100, Counter: 3, NodeID: "b"},
		{WallMillis: 200, Counter: 0, NodeID: "a"},
		{WallMillis: 200, Counter: 0, NodeID: "b"},
	}
	sort.SliceStable(in, func(i, j int) bool { return LessHLC(in[i], in[j]) })
	for i := range want {
		if in[i] != want[i] {
			t.Fatalf("at %d: got %+v want %+v", i, in[i], want[i])
		}
	}

	// Tie: identical timestamps are not Less in either direction.
	x := HLCTimestamp{WallMillis: 1, Counter: 1, NodeID: "n"}
	if LessHLC(x, x) {
		t.Fatal("LessHLC must be irreflexive")
	}
}

// TestHLCDeterministicReplay verifies the same wall sequence yields identical
// timestamps across two independent clocks — the property the fuzzer relies on.
func TestHLCDeterministicReplay(t *testing.T) {
	walls := []int64{10, 10, 25, 25, 25, 5, 40}
	run := func() []HLCTimestamp {
		h := NewHLC("repl")
		out := make([]HLCTimestamp, 0, len(walls))
		for _, w := range walls {
			out = append(out, h.Tick(w))
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("replay diverged at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}
