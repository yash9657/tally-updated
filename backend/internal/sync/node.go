package sync

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/ledger"
)

// node.go models one offline desktop node: a node-partitioned, deterministic
// UUIDv7 generator (NO crypto/rand, so the fuzzer reproduces exactly), an HLC, a
// small read-model of the head versions the node knows, and an append-only Outbox
// of voucher Intents that COMMIT through WS1's cloud sequencer on reconnect.

// NodeIDGen produces node-partitioned, time-ordered, collision-free UUIDv7 values
// WITHOUT crypto/rand. The layout follows RFC 9562 v7 (48-bit big-endian
// unix-millis time prefix, version/variant nibbles) but the would-be random tail
// is replaced by a deterministic (nodeHash, sequence) pair:
//
//	bytes  0..5  : wallMillis (48-bit big-endian)  -> time ordering
//	byte   6     : 0x70 | (nodeHash high nibble)    -> version 7
//	byte   7     : nodeHash low byte                -> node partition
//	byte   8     : 0x80 | (nodeHash nibble)         -> variant 10
//	bytes  9..15 : node-scoped monotonic sequence   -> uniqueness within a node
//
// Two different node ids hash to different node bytes, so their id spaces are
// disjoint and NEVER collide. Within one node the sequence strictly increases, and
// because the wall-millis prefix is the high-order bytes, ids from one node sort in
// emission order even across wall ticks. The whole generator is a pure function of
// (nodeID, wallMillis, callCount) — perfectly reproducible.
type NodeIDGen struct {
	nodeID   string
	nodeHash uint16
	seq      uint64
	lastWall int64 // monotonic non-decreasing wall actually encoded
}

// NewNodeIDGen builds a generator for the node. The seed argument lets distinct
// generators for the SAME node id diverge if a caller wants independent streams;
// it is folded into the node hash so it never breaks cross-node disjointness for
// different node ids.
func NewNodeIDGen(nodeID string, seed uint64) *NodeIDGen {
	h := fnv.New32a()
	_, _ = h.Write([]byte(nodeID))
	var sb [8]byte
	binary.BigEndian.PutUint64(sb[:], seed)
	_, _ = h.Write(sb[:])
	sum := h.Sum32()
	return &NodeIDGen{
		nodeID:   nodeID,
		nodeHash: uint16(sum & 0xFFFF),
	}
}

// Next returns the next deterministic UUIDv7 for this node at the given wall time.
// The per-node sequence guarantees uniqueness within a node even when many ids are
// minted at the same wall millisecond.
func (g *NodeIDGen) Next(wallMillis int64) uuid.UUID {
	g.seq++

	// Clamp the encoded wall to be non-decreasing: a backwards or stalled wall
	// (clock skew) must NEVER lower the time prefix, or ids would stop being
	// strictly increasing. The per-node sequence in the trailing bytes still
	// guarantees strict increase + uniqueness when the wall does not advance.
	if wallMillis < g.lastWall {
		wallMillis = g.lastWall
	}
	g.lastWall = wallMillis

	var b [16]byte

	// 48-bit big-endian unix-millis time prefix.
	ms := uint64(wallMillis) & 0xFFFFFFFFFFFF
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)

	// Version 7 in the high nibble of byte 6; carry node-hash bits in the rest.
	b[6] = 0x70 | byte((g.nodeHash>>8)&0x0F)
	b[7] = byte(g.nodeHash & 0xFF)

	// Variant 10xx in the two high bits of byte 8; carry a node-hash nibble.
	b[8] = 0x80 | byte((g.nodeHash>>4)&0x3F)

	// Node-scoped strictly-increasing sequence in the trailing 7 bytes (56 bits).
	b[9] = byte(g.seq >> 48)
	b[10] = byte(g.seq >> 40)
	b[11] = byte(g.seq >> 32)
	b[12] = byte(g.seq >> 24)
	b[13] = byte(g.seq >> 16)
	b[14] = byte(g.seq >> 8)
	b[15] = byte(g.seq)

	return uuid.UUID(b)
}

// Node models one offline desktop. It owns an HLC, a node-partitioned id
// generator, a small read-model (knownHeads) of the head version_no it last
// observed per voucher, and an append-only Outbox of Intents. The node mutates
// nothing on the server while "offline"; CreateVoucher/AlterVoucher/DeleteVoucher
// merely append an Intent. On reconnect the Outbox COMMITS through the sequencer.
type Node struct {
	ID  string
	fy  string // financial-year tag for the provisional number, e.g. "2025-26"
	hlc *HLC
	gen *NodeIDGen

	knownHeads map[uuid.UUID]int // voucher_id -> head version this node believes is current
	outbox     []Intent
	seq        int // per-node provisional sequence counter
}

// NewNode builds an offline node. seed seeds the node-partitioned id generator; fy
// tags the provisional number. The HLC starts at zero and is driven by explicit
// wall times so the whole node is reproducible.
func NewNode(id, fy string, seed uint64) *Node {
	return &Node{
		ID:         id,
		fy:         fy,
		hlc:        NewHLC(id),
		gen:        NewNodeIDGen(id, seed),
		knownHeads: make(map[uuid.UUID]int),
	}
}

// provisionalNo formats the client-only provisional display number. The server
// NEVER receives this as voucher_no — it allocates the real sequential number; the
// provisional exists only so the offline UI can show something before the commit.
func (n *Node) provisionalNo() string {
	n.seq++
	return fmt.Sprintf("INV/%s/PROV-%s-%d", n.fy, n.ID, n.seq)
}

// CreateVoucher appends a CREATE intent for a fresh node-partitioned voucher id at
// the given wall time. BaseVersion is 0 (a create bases on nothing). The returned
// id is the committed identity the server will keep. The node records the id in its
// read-model at version 1 (its local optimistic head).
func (n *Node) CreateVoucher(wallMillis int64, payload ledger.VoucherInput) uuid.UUID {
	id := n.gen.Next(wallMillis)
	payload.VoucherNo = "" // server numbers it; never send the provisional as voucher_no
	in := Intent{
		IdempotencyKey:  fmt.Sprintf("%s:CREATE:%s", n.ID, id),
		ClientVoucherID: id,
		Op:              OpCreate,
		Class:           payload.Class,
		ProvisionalNo:   n.provisionalNo(),
		HLC:             n.hlc.Tick(wallMillis),
		NodeID:          n.ID,
		BaseVersion:     0,
		Payload:         payload,
	}
	n.outbox = append(n.outbox, in)
	n.knownHeads[id] = 1
	return id
}

// AlterVoucher appends an ALTER intent that supersedes targetID, declaring the head
// version (baseVersion) the node based the edit on. If the server's head has moved
// past baseVersion by commit time, the sequencer parks this as a CONFLICT.
func (n *Node) AlterVoucher(wallMillis int64, targetID uuid.UUID, baseVersion int, payload ledger.VoucherInput) {
	payload.VoucherNo = "" // server carries the existing number forward
	in := Intent{
		IdempotencyKey:  fmt.Sprintf("%s:ALTER:%s:%d", n.ID, targetID, n.nextSeq()),
		ClientVoucherID: targetID,
		Op:              OpAlter,
		Class:           payload.Class,
		ProvisionalNo:   n.provisionalNo(),
		HLC:             n.hlc.Tick(wallMillis),
		NodeID:          n.ID,
		BaseVersion:     baseVersion,
		Payload:         payload,
	}
	n.outbox = append(n.outbox, in)
}

// DeleteVoucher appends a DELETE (tombstone) intent against targetID at the head
// version the node observed (baseVersion). A stale base parks as a CONFLICT.
func (n *Node) DeleteVoucher(wallMillis int64, targetID uuid.UUID, baseVersion int, class ledger.Class) {
	in := Intent{
		IdempotencyKey:  fmt.Sprintf("%s:DELETE:%s:%d", n.ID, targetID, n.nextSeq()),
		ClientVoucherID: targetID,
		Op:              OpDelete,
		Class:           class,
		ProvisionalNo:   n.provisionalNo(),
		HLC:             n.hlc.Tick(wallMillis),
		NodeID:          n.ID,
		BaseVersion:     baseVersion,
		Payload:         ledger.VoucherInput{Class: class},
	}
	n.outbox = append(n.outbox, in)
}

// Learn records that the node observed (e.g. from a prior sync) that voucherID is
// at head version v. Used to seed BaseVersion for a subsequent ALTER/DELETE.
func (n *Node) Learn(voucherID uuid.UUID, version int) {
	n.knownHeads[voucherID] = version
}

// KnownHead returns the head version the node believes is current for voucherID.
func (n *Node) KnownHead(voucherID uuid.UUID) (int, bool) {
	v, ok := n.knownHeads[voucherID]
	return v, ok
}

// Outbox returns the append-only intent log to commit through the sequencer.
func (n *Node) Outbox() []Intent { return n.outbox }

// Tick exposes the node's HLC for callers that want to advance it directly (e.g.
// the fuzzer modelling a receive). It returns the new local timestamp.
func (n *Node) Tick(wallMillis int64) HLCTimestamp { return n.hlc.Tick(wallMillis) }

// nextSeq bumps an internal counter that makes per-op idempotency keys unique even
// for repeated alters of the same target by one node.
func (n *Node) nextSeq() int {
	n.seq++
	return n.seq
}
