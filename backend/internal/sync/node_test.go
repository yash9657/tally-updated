package sync

import (
	"bytes"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/ledger"
)

// TestNodeIDGenVersionAndVariant verifies the generated ids are well-formed RFC
// 9562 v7 UUIDs (version nibble 7, variant 10xx).
func TestNodeIDGenVersionAndVariant(t *testing.T) {
	g := NewNodeIDGen("nodeA", 1)
	for i := 0; i < 50; i++ {
		id := g.Next(int64(1000 + i))
		if v := id.Version(); v != 7 {
			t.Fatalf("id %s version = %d, want 7", id, v)
		}
		// Variant 10xx -> byte[8] high two bits == 10.
		if id[8]&0xC0 != 0x80 {
			t.Fatalf("id %s variant bits = %#x, want 10xx", id, id[8]&0xC0)
		}
	}
}

// TestNodeIDGenStrictlyIncreasingWithinNode verifies one node's ids are strictly
// increasing in byte order (time-ordered) across wall ticks AND stalls.
func TestNodeIDGenStrictlyIncreasingWithinNode(t *testing.T) {
	g := NewNodeIDGen("nodeA", 7)
	var prev uuid.UUID
	first := true
	walls := []int64{100, 100, 100, 200, 200, 150, 300}
	for _, w := range walls {
		id := g.Next(w)
		if !first && bytes.Compare(prev[:], id[:]) >= 0 {
			t.Fatalf("ids not strictly increasing: prev=%s cur=%s (wall=%d)", prev, id, w)
		}
		prev = id
		first = false
	}
}

// TestNodeIDGenCrossNodeDisjoint verifies two different node ids never collide,
// even when minting at identical wall times and sequence positions.
func TestNodeIDGenCrossNodeDisjoint(t *testing.T) {
	ga := NewNodeIDGen("alpha", 0)
	gb := NewNodeIDGen("bravo", 0)
	seen := make(map[uuid.UUID]bool)
	for i := 0; i < 1000; i++ {
		w := int64(500 + i)
		a := ga.Next(w)
		b := gb.Next(w)
		if a == b {
			t.Fatalf("cross-node collision at i=%d: %s", i, a)
		}
		if seen[a] {
			t.Fatalf("duplicate id from alpha: %s", a)
		}
		if seen[b] {
			t.Fatalf("duplicate id from bravo: %s", b)
		}
		seen[a] = true
		seen[b] = true
	}
	if len(seen) != 2000 {
		t.Fatalf("expected 2000 distinct ids, got %d", len(seen))
	}
}

// TestNodeIDGenDeterministic verifies identical (nodeID, seed, wall sequence) yield
// identical ids — the fuzzer reproducibility guarantee.
func TestNodeIDGenDeterministic(t *testing.T) {
	run := func() []uuid.UUID {
		g := NewNodeIDGen("repro", 42)
		out := make([]uuid.UUID, 0, 16)
		for i := 0; i < 16; i++ {
			out = append(out, g.Next(int64(i%4)))
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("id stream diverged at %d: %s vs %s", i, a[i], b[i])
		}
	}
}

// TestNodeOutboxAppendCreate verifies CreateVoucher appends a CREATE intent with an
// empty payload VoucherNo, a provisional number, BaseVersion 0, and the node's id.
func TestNodeOutboxAppendCreate(t *testing.T) {
	n := NewNode("desk-1", "2025-26", 1)
	id := n.CreateVoucher(1000, ledger.VoucherInput{Class: ledger.Journal, VoucherNo: "SHOULD-BE-CLEARED"})

	ob := n.Outbox()
	if len(ob) != 1 {
		t.Fatalf("outbox len = %d, want 1", len(ob))
	}
	in := ob[0]
	if in.Op != OpCreate {
		t.Fatalf("op = %q, want CREATE", in.Op)
	}
	if in.ClientVoucherID != id {
		t.Fatalf("client voucher id mismatch")
	}
	if in.Payload.VoucherNo != "" {
		t.Fatalf("payload VoucherNo must be empty, got %q", in.Payload.VoucherNo)
	}
	if in.BaseVersion != 0 {
		t.Fatalf("create BaseVersion must be 0, got %d", in.BaseVersion)
	}
	if in.NodeID != "desk-1" {
		t.Fatalf("node id = %q", in.NodeID)
	}
	if in.ProvisionalNo == "" {
		t.Fatal("provisional number must be set")
	}
	if v, ok := n.KnownHead(id); !ok || v != 1 {
		t.Fatalf("known head after create = %d,%v, want 1,true", v, ok)
	}
}

// TestNodeOutboxAlterDelete verifies alter/delete append intents that carry the
// target id, the declared base version, and HLC timestamps strictly increasing in
// outbox order.
func TestNodeOutboxAlterDelete(t *testing.T) {
	n := NewNode("desk-2", "2025-26", 2)
	target := n.CreateVoucher(10, ledger.VoucherInput{Class: ledger.Journal})
	n.AlterVoucher(20, target, 1, ledger.VoucherInput{Class: ledger.Journal})
	n.DeleteVoucher(30, target, 2, ledger.Journal)

	ob := n.Outbox()
	if len(ob) != 3 {
		t.Fatalf("outbox len = %d, want 3", len(ob))
	}
	if ob[1].Op != OpAlter || ob[1].ClientVoucherID != target || ob[1].BaseVersion != 1 {
		t.Fatalf("alter intent wrong: %+v", ob[1])
	}
	if ob[2].Op != OpDelete || ob[2].ClientVoucherID != target || ob[2].BaseVersion != 2 {
		t.Fatalf("delete intent wrong: %+v", ob[2])
	}
	// HLC strictly increasing across the three appends.
	if !LessHLC(ob[0].HLC, ob[1].HLC) || !LessHLC(ob[1].HLC, ob[2].HLC) {
		t.Fatalf("intent HLCs not strictly increasing: %+v %+v %+v", ob[0].HLC, ob[1].HLC, ob[2].HLC)
	}
	// Idempotency keys are unique.
	keys := map[string]bool{}
	for _, in := range ob {
		if keys[in.IdempotencyKey] {
			t.Fatalf("duplicate idempotency key %q", in.IdempotencyKey)
		}
		keys[in.IdempotencyKey] = true
	}
}
