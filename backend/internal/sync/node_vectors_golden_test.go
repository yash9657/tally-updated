package sync

// node_vectors_golden_test.go — the executable contract between node.go and its
// TypeScript port (frontend/src/offline/).
//
// ⛔ WHY A GENERATED GOLDEN AND NOT A LIST OF EXPECTED STRINGS IN TYPESCRIPT.
// HANDOFF §0.1 item 4: a gate over a SERVER decision is a generated golden. The
// idempotency-key formats, the provisional-number format, the node-partitioned
// UUIDv7 layout and the HLC's tie-breaking are all decided HERE, and the client
// must agree byte for byte — not because it is tidy, but because the server's
// exactly-once replay matches on the key string. A port that spells a key
// differently does not fail loudly: a re-commit APPLIES A SECOND TIME instead of
// answering DUPLICATE. Vectors typed by hand into the TS test would be a second
// opinion about what Go does; these are a measurement of it.
//
// ✅ AND IT NEEDS NO POSTGRES, which is the property track C's golden has and
// B1's does not. This is pure computation over node.go, so it CANNOT hide behind
// a skip — a generator that silently skips is indistinguishable from a passing
// one in `go test`'s exit code (§0.1 item 4's warning).
//
// Regenerate with:
//
//	UPDATE_GOLDENS=1 go test ./internal/sync/ -run TestOfflineNodeVectorsGolden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/ledger"
)

const nodeVectorsGoldenPath = "../../../frontend/src/api/goldens/offline-node-vectors.json"

type vecHLC struct {
	WallMillis int64  `json:"wall_millis"`
	Counter    int64  `json:"counter"`
	NodeID     string `json:"node_id"`
}

// vecStep is one recorded change. TargetStep names an EARLIER step's minted id
// (-1 for a create), so the port can replay the scenario without the ids being
// inputs — they are outputs, and comparing them is the point.
type vecStep struct {
	Kind        string `json:"kind"` // create | alter | delete
	WallMillis  int64  `json:"wall_millis"`
	TargetStep  int    `json:"target_step"`
	BaseVersion int    `json:"base_version"`
	Class       string `json:"class"`

	ClientVoucherID string `json:"client_voucher_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	ProvisionalNo   string `json:"provisional_no"`
	HLC             vecHLC `json:"hlc"`
}

type vecCase struct {
	Name   string    `json:"name"`
	NodeID string    `json:"node_id"`
	FY     string    `json:"fy"`
	Seed   uint64    `json:"seed"`
	Steps  []vecStep `json:"steps"`
}

// vecClockOp drives the HLC directly, including the merge path a node takes when
// it learns a remote timestamp.
type vecClockOp struct {
	Kind       string `json:"kind"` // tick | update
	WallMillis int64  `json:"wall_millis"`
	Remote     vecHLC `json:"remote"`
	Got        vecHLC `json:"got"`
}

type vecClockCase struct {
	Name   string       `json:"name"`
	NodeID string       `json:"node_id"`
	Ops    []vecClockOp `json:"ops"`
}

func buildNodeVectors() []vecCase {
	// A minimal balanced body. The payload is NOT what these vectors are about —
	// the ids, keys, provisional numbers and clock readings are — so it stays
	// constant and small.
	body := func() ledger.VoucherInput {
		return ledger.VoucherInput{Class: ledger.Journal, VoucherDate: "2025-05-20"}
	}

	cases := []vecCase{
		{
			Name: "one node, three creates at one wall millisecond",
			// The sequence, not the clock, is what keeps these distinct — the case
			// that catches a port using crypto.randomUUID or a wall-only id.
			NodeID: "node-a", FY: "2025-26", Seed: 0,
			Steps: []vecStep{
				{Kind: "create", WallMillis: 1756300000000, TargetStep: -1},
				{Kind: "create", WallMillis: 1756300000000, TargetStep: -1},
				{Kind: "create", WallMillis: 1756300000000, TargetStep: -1},
			},
		},
		{
			Name:   "a second node id occupies a disjoint space at the same wall",
			NodeID: "node-b", FY: "2025-26", Seed: 0,
			Steps: []vecStep{
				{Kind: "create", WallMillis: 1756300000000, TargetStep: -1},
				{Kind: "create", WallMillis: 1756300000000, TargetStep: -1},
			},
		},
		{
			Name:   "create then alter then delete the same voucher",
			NodeID: "node-a", FY: "2025-26", Seed: 0,
			Steps: []vecStep{
				{Kind: "create", WallMillis: 1756300000000, TargetStep: -1},
				{Kind: "alter", WallMillis: 1756300001000, TargetStep: 0, BaseVersion: 1},
				{Kind: "delete", WallMillis: 1756300002000, TargetStep: 0, BaseVersion: 2, Class: "Journal"},
			},
		},
		{
			// ⚠️ THE BROWSER CASE. A laptop that sleeps and wakes with a backwards
			// clock must not lower the id's time prefix nor repeat an HLC reading.
			Name:   "a clock that goes BACKWARDS between two creates",
			NodeID: "node-a", FY: "2025-26", Seed: 0,
			Steps: []vecStep{
				{Kind: "create", WallMillis: 1756300005000, TargetStep: -1},
				{Kind: "create", WallMillis: 1756300000000, TargetStep: -1},
				{Kind: "create", WallMillis: 1756300000000, TargetStep: -1},
			},
		},
		{
			Name:   "a non-zero seed diverges the id stream for the SAME node id",
			NodeID: "node-a", FY: "2026-27", Seed: 42,
			Steps: []vecStep{
				{Kind: "create", WallMillis: 1756300000000, TargetStep: -1},
				{Kind: "create", WallMillis: 1756300000000, TargetStep: -1},
			},
		},
	}

	for ci := range cases {
		c := &cases[ci]
		n := NewNode(c.NodeID, c.FY, c.Seed)
		minted := make([]uuid.UUID, len(c.Steps))
		for si := range c.Steps {
			s := &c.Steps[si]
			switch s.Kind {
			case "create":
				minted[si] = n.CreateVoucher(s.WallMillis, body())
			case "alter":
				n.AlterVoucher(s.WallMillis, minted[s.TargetStep], s.BaseVersion, body())
				minted[si] = minted[s.TargetStep]
			case "delete":
				n.DeleteVoucher(s.WallMillis, minted[s.TargetStep], s.BaseVersion, ledger.Class(s.Class))
				minted[si] = minted[s.TargetStep]
			}
			in := n.Outbox()[si]
			s.ClientVoucherID = in.ClientVoucherID.String()
			s.IdempotencyKey = in.IdempotencyKey
			s.ProvisionalNo = in.ProvisionalNo
			s.HLC = vecHLC{WallMillis: in.HLC.WallMillis, Counter: in.HLC.Counter, NodeID: in.HLC.NodeID}
		}
	}
	return cases
}

func buildClockVectors() []vecClockCase {
	cases := []vecClockCase{
		{
			Name: "ticks forward, stalled and backwards", NodeID: "node-a",
			Ops: []vecClockOp{
				{Kind: "tick", WallMillis: 1000},
				{Kind: "tick", WallMillis: 1000}, // stalled -> counter
				{Kind: "tick", WallMillis: 900},  // backwards -> counter, wall held
				{Kind: "tick", WallMillis: 2000}, // forward -> counter resets
			},
		},
		{
			Name: "merges a remote timestamp", NodeID: "node-a",
			Ops: []vecClockOp{
				{Kind: "tick", WallMillis: 1000},
				{Kind: "update", WallMillis: 1000, Remote: vecHLC{WallMillis: 1000, Counter: 5, NodeID: "node-b"}},
				{Kind: "update", WallMillis: 1000, Remote: vecHLC{WallMillis: 3000, Counter: 2, NodeID: "node-b"}},
				{Kind: "update", WallMillis: 9000, Remote: vecHLC{WallMillis: 3000, Counter: 0, NodeID: "node-b"}},
				{Kind: "tick", WallMillis: 9000},
			},
		},
	}
	for ci := range cases {
		c := &cases[ci]
		h := NewHLC(c.NodeID)
		for oi := range c.Ops {
			o := &c.Ops[oi]
			var got HLCTimestamp
			if o.Kind == "tick" {
				got = h.Tick(o.WallMillis)
			} else {
				got = h.Update(o.WallMillis, HLCTimestamp{
					WallMillis: o.Remote.WallMillis, Counter: o.Remote.Counter, NodeID: o.Remote.NodeID,
				})
			}
			o.Got = vecHLC{WallMillis: got.WallMillis, Counter: got.Counter, NodeID: got.NodeID}
		}
	}
	return cases
}

func TestOfflineNodeVectorsGolden(t *testing.T) {
	golden := struct {
		Note   string         `json:"note"`
		Nodes  []vecCase      `json:"nodes"`
		Clocks []vecClockCase `json:"clocks"`
	}{
		Note: "GENERATED by internal/sync's TestOfflineNodeVectorsGolden by DRIVING node.go " +
			"and hlc.go. Do not edit by hand. frontend/src/offline/ is a port of those two " +
			"files and consumes this to prove it agrees byte for byte: the server's " +
			"exactly-once replay matches on idempotency_key, so a port that spells a key " +
			"differently makes a re-commit apply twice instead of answering DUPLICATE.",
		Nodes:  buildNodeVectors(),
		Clocks: buildClockVectors(),
	}

	got, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	got = append(got, '\n')

	if os.Getenv("UPDATE_GOLDENS") != "" {
		if err := os.MkdirAll(filepath.Dir(nodeVectorsGoldenPath), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(nodeVectorsGoldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("regenerated %s (%d bytes)", nodeVectorsGoldenPath, len(got))
		return
	}

	want, err := os.ReadFile(nodeVectorsGoldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v\n\nIf this is the first run, generate it:\n"+
			"  UPDATE_GOLDENS=1 go test ./internal/sync/ -run TestOfflineNodeVectorsGolden",
			nodeVectorsGoldenPath, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is STALE. node.go or hlc.go changed the values the TypeScript port "+
			"is pinned to — regenerate it AND re-run the frontend suite, because the port "+
			"in frontend/src/offline/ must move with it:\n"+
			"  UPDATE_GOLDENS=1 go test ./internal/sync/ -run TestOfflineNodeVectorsGolden",
			nodeVectorsGoldenPath)
	}
}
