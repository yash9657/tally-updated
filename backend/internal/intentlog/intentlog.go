// Package intentlog is the agent plane's attributable, immutable record
// (PLAN-AGENTIC §2.6): one row per event of an agent run, BLAKE3 hash-chained
// per run, append-only by trigger AND by grant (migration 0092), anchored
// periodically at the tenant level.
//
// # WHY THE HASH IS COMPUTED HERE AND NOT IN SQL
//
// The same rule internal/ledger/hash.go follows for voucher_version: the
// database stores bytes it cannot produce. A row edited with the trigger
// disabled (a superuser, session_replication_role = replica, a restore from a
// doctored dump) still carries the old hash, and Verify — which holds the
// canonical encoding — recomputes and disagrees. A hash the database computed
// for itself would be recomputed by the same database for the edited row.
//
// # WHY THE CHAIN IS PER RUN
//
// A tenant-wide chain serialises every agent write in a company behind one head
// row (HANDOFF-AGENTIC-AFTER-W0 §8). A run's appends are already serialised by
// the run's advisory lock, so a per-run chain costs no extra contention. What a
// per-run chain cannot see by itself is a TRUNCATED tail — a chain with its last
// links removed is still a valid chain — and that is what the anchor is for:
// Anchor commits to the head of every run in the company, chained to the
// previous anchor, and VerifyAnchors recomputes each root from the rows.
//
// # ⛔ NO PROMPT OR COMPLETION TEXT
//
// PLAN-AGENTIC §2.7 and the trajectory package's rule. A payload carries ids,
// verdicts, paise amounts computed by Go, and hashes of model-visible text.
// This package cannot enforce that (it cannot tell a party name from a prompt)
// — the caller (internal/agent) builds every payload from typed structs whose
// fields are enumerated in its intent.go, which is where review looks.
package intentlog

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/zeebo/blake3"
)

// Kind is the closed set of event kinds. It mirrors 0092's CHECK exactly;
// TestKindsMatchTheMigration pins the two against each other.
type Kind string

const (
	KindRunStarted        Kind = "run_started"
	KindDecisionStep      Kind = "decision_step"
	KindToolCall          Kind = "tool_call"
	KindPreview           Kind = "preview"
	KindVerifierError     Kind = "verifier_error"
	KindPolicy            Kind = "policy"
	KindApprovalRequested Kind = "approval_requested"
	KindApprovalDecided   Kind = "approval_decided"
	KindCommit            Kind = "commit"
	KindCommitRefused     Kind = "commit_refused"
	KindRunFinished       Kind = "run_finished"
	KindRunFailed         Kind = "run_failed"
)

// Kinds is every Kind, in the migration's order.
var Kinds = []Kind{
	KindRunStarted, KindDecisionStep, KindToolCall, KindPreview, KindVerifierError,
	KindPolicy, KindApprovalRequested, KindApprovalDecided, KindCommit,
	KindCommitRefused, KindRunFinished, KindRunFailed,
}

// ActorKind is who produced the event.
type ActorKind string

const (
	ActorAgent  ActorKind = "agent"
	ActorUser   ActorKind = "user"
	ActorSystem ActorKind = "system"
)

// Event is what a caller appends.
type Event struct {
	RunID     uuid.UUID
	Kind      Kind
	ActorKind ActorKind
	// ActorID is the agent id for ActorAgent, the user id for ActorUser, and a
	// fixed system name ("poller") for ActorSystem.
	ActorID string
	// OnBehalfOf is REQUIRED for ActorAgent (0092's CHECK): an agent event that
	// does not name its principal is not attributable.
	OnBehalfOf uuid.UUID
	ActionID   string
	ACLKey     string
	// Payload is marshalled with encoding/json, then canonicalised. ⛔ It must
	// contain no JSON number with a fraction or an exponent (see canonical):
	// decimals travel as strings, as money does everywhere in this codebase.
	Payload any
}

// Entry is one stored row.
type Entry struct {
	ID         uuid.UUID       `json:"id"`
	CompanyID  uuid.UUID       `json:"-"`
	RunID      uuid.UUID       `json:"run_id"`
	Seq        int             `json:"seq"`
	Kind       Kind            `json:"kind"`
	ActorKind  ActorKind       `json:"actor_kind"`
	ActorID    string          `json:"actor_id"`
	OnBehalfOf uuid.UUID       `json:"on_behalf_of,omitempty"`
	ActionID   string          `json:"action_id,omitempty"`
	ACLKey     string          `json:"acl_key,omitempty"`
	Payload    json.RawMessage `json:"payload"`
	PrevHash   []byte          `json:"-"`
	Hash       []byte          `json:"-"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ErrChainBroken is what Verify returns (wrapped) for a row whose hash does not
// recompute or whose link does not point at its predecessor.
var ErrChainBroken = errors.New("intentlog: hash chain broken")

// canonical returns the stable byte form of a JSON payload: decoded with
// UseNumber and re-encoded, which sorts object keys and compacts whitespace.
//
// ⛔ WHY THIS AND NOT THE MARSHALLED BYTES: the column is JSONB, and JSONB
// reorders keys and reformats whitespace (the same normalisation pgstore.go
// documents for statutory_step). Verify re-reads the payload FROM THE DATABASE,
// so the bytes it hashes are Postgres's rendering, not ours; the only encoding
// both sides agree on is one derived from the decoded value.
//
// ⛔ WHY A FRACTIONAL NUMBER IS REFUSED: JSONB stores a number as NUMERIC and
// re-renders it (1e2 comes back as 100), so a payload holding one would hash
// one way on insert and another on verify, and the chain would report tampering
// that never happened — indistinguishable from real tampering. Integers survive
// exactly; decimals are strings, as every money value in this codebase already is.
func canonical(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("intentlog: payload is not JSON: %w", err)
	}
	if err := refuseFractions(v); err != nil {
		return nil, err
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("intentlog: canonicalise payload: %w", err)
	}
	return out, nil
}

func refuseFractions(v any) error {
	switch x := v.(type) {
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return fmt.Errorf("intentlog: payload number %s has a fraction or exponent; send decimals as strings", x)
		}
	case map[string]any:
		for _, e := range x {
			if err := refuseFractions(e); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range x {
			if err := refuseFractions(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// chainDomain separates this chain's hashes from every other BLAKE3 use in the
// product (voucher_version, the trajectory tool hashes). Bumped with the
// encoding, never silently.
const chainDomain = "khata.intentlog.v1"

// computeHash is BLAKE3 over a length-prefixed encoding of every field that
// identifies the event, then the previous hash.
//
// ⚠️ LENGTH-PREFIXED, NOT DELIMITED. A separator-joined encoding lets
// ("ab","c") and ("a","bc") collide whenever a field can contain the
// separator — and actor_id and acl_key are strings a future caller may fill
// with anything. A uint32 length before every field makes the encoding
// injective.
func computeHash(e Entry, canonPayload []byte) []byte {
	var b bytes.Buffer
	put := func(s []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		b.Write(n[:])
		b.Write(s)
	}
	put([]byte(chainDomain))
	put(e.RunID[:])
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], uint64(e.Seq))
	put(seq[:])
	put([]byte(e.Kind))
	put([]byte(e.ActorKind))
	put([]byte(e.ActorID))
	put(e.OnBehalfOf[:])
	put([]byte(e.ActionID))
	put([]byte(e.ACLKey))
	var at [8]byte
	binary.BigEndian.PutUint64(at[:], uint64(e.CreatedAt.UTC().UnixMicro()))
	put(at[:])
	put(canonPayload)
	put(e.PrevHash)
	sum := blake3.Sum256(b.Bytes())
	return sum[:]
}

// Append adds one event to its run's chain, inside the caller's transaction
// (which must be company-scoped: RLS decides the tenant, and companyID must be
// the tenant in scope or the INSERT's WITH CHECK refuses it).
//
// ⚠️ IT DOES NOT LOCK THE HEAD ROW, BECAUSE IT CANNOT AND NEED NOT. SELECT ...
// FOR UPDATE needs UPDATE privilege, which 0092 deliberately withholds. Appends
// to one run are serialised by the run's advisory lock (internal/durable);
// the (company, run, seq) UNIQUE turns any append that raced past it into a
// loud unique violation rather than a fork in the chain.
func Append(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, ev Event, now time.Time) (Entry, error) {
	if ev.RunID == uuid.Nil {
		return Entry{}, errors.New("intentlog: event has no run id")
	}
	if ev.ActorKind == ActorAgent && ev.OnBehalfOf == uuid.Nil {
		return Entry{}, errors.New("intentlog: an agent event must name the principal it acted for")
	}
	raw, err := json.Marshal(ev.Payload)
	if err != nil {
		return Entry{}, fmt.Errorf("intentlog: marshal payload: %w", err)
	}
	canon, err := canonical(raw)
	if err != nil {
		return Entry{}, err
	}

	e := Entry{
		CompanyID:  companyID,
		RunID:      ev.RunID,
		Kind:       ev.Kind,
		ActorKind:  ev.ActorKind,
		ActorID:    ev.ActorID,
		OnBehalfOf: ev.OnBehalfOf,
		ActionID:   ev.ActionID,
		ACLKey:     ev.ACLKey,
		Payload:    canon,
		// Microseconds, because timestamptz stores microseconds and the hash
		// covers the stored value: a nanosecond in the hash would be lost on
		// the way in and every row would fail to verify.
		CreatedAt: now.UTC().Truncate(time.Microsecond),
	}
	var lastSeq int
	var lastHash []byte
	err = tx.QueryRow(ctx,
		`SELECT seq, hash FROM agent_intent_log
		  WHERE company_id = $1 AND run_id = $2
		  ORDER BY seq DESC LIMIT 1`, companyID, ev.RunID).Scan(&lastSeq, &lastHash)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		e.Seq = 1
	case err != nil:
		return Entry{}, fmt.Errorf("intentlog: read run head: %w", err)
	default:
		e.Seq = lastSeq + 1
		e.PrevHash = lastHash
	}
	e.Hash = computeHash(e, canon)
	id, err := uuid.NewV7()
	if err != nil {
		return Entry{}, err
	}
	e.ID = id

	var onBehalf any
	if e.OnBehalfOf != uuid.Nil {
		onBehalf = e.OnBehalfOf
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO agent_intent_log
		   (id, company_id, run_id, seq, kind, actor_kind, actor_id, on_behalf_of,
		    action_id, acl_key, payload, prev_hash, hash, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),NULLIF($10,''),$11,$12,$13,$14)`,
		e.ID, companyID, e.RunID, e.Seq, string(e.Kind), string(e.ActorKind), e.ActorID, onBehalf,
		e.ActionID, e.ACLKey, canon, e.PrevHash, e.Hash, e.CreatedAt)
	if err != nil {
		return Entry{}, fmt.Errorf("intentlog: insert: %w", err)
	}
	return e, nil
}

// List returns a run's events with seq > afterSeq, in chain order.
func List(ctx context.Context, tx pgx.Tx, companyID, runID uuid.UUID, afterSeq int) ([]Entry, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, run_id, seq, kind, actor_kind, actor_id,
		        COALESCE(on_behalf_of, '00000000-0000-0000-0000-000000000000'::uuid),
		        COALESCE(action_id, ''), COALESCE(acl_key, ''), payload, prev_hash, hash, created_at
		   FROM agent_intent_log
		  WHERE company_id = $1 AND run_id = $2 AND seq > $3
		  ORDER BY seq`, companyID, runID, afterSeq)
	if err != nil {
		return nil, fmt.Errorf("intentlog: list: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var kind, actor string
		if err := rows.Scan(&e.ID, &e.RunID, &e.Seq, &kind, &actor, &e.ActorID, &e.OnBehalfOf,
			&e.ActionID, &e.ACLKey, &e.Payload, &e.PrevHash, &e.Hash, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("intentlog: scan: %w", err)
		}
		e.CompanyID = companyID
		e.Kind, e.ActorKind = Kind(kind), ActorKind(actor)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Verify recomputes a run's whole chain from the stored rows and returns the
// number of links checked. The first bad link is named in the error, which
// wraps ErrChainBroken.
func Verify(ctx context.Context, tx pgx.Tx, companyID, runID uuid.UUID) (int, error) {
	entries, err := List(ctx, tx, companyID, runID, 0)
	if err != nil {
		return 0, err
	}
	var prev []byte
	for i, e := range entries {
		if e.Seq != i+1 {
			return i, fmt.Errorf("%w: run %s has seq %d at position %d (a link is missing)", ErrChainBroken, runID, e.Seq, i+1)
		}
		if !bytes.Equal(e.PrevHash, prev) {
			return i, fmt.Errorf("%w: run %s seq %d does not point at its predecessor", ErrChainBroken, runID, e.Seq)
		}
		canon, err := canonical(e.Payload)
		if err != nil {
			return i, fmt.Errorf("%w: run %s seq %d: %v", ErrChainBroken, runID, e.Seq, err)
		}
		if want := computeHash(e, canon); !bytes.Equal(want, e.Hash) {
			return i, fmt.Errorf("%w: run %s seq %d (%s) does not recompute — the row was altered after it was written", ErrChainBroken, runID, e.Seq, e.Kind)
		}
		prev = e.Hash
	}
	return len(entries), nil
}

// AnchorRow is one agent_intent_anchor row.
type AnchorRow struct {
	ID         uuid.UUID
	AnchoredAt time.Time
	RunCount   int
	EventCount int64
	PrevRoot   []byte
	Root       []byte
}

type runHead struct {
	runID uuid.UUID
	seq   int
	hash  []byte
}

// headsAsOf reads every run's head (its highest seq) among the rows created at
// or before t. Deterministic in the rows, which is what lets VerifyAnchors
// recompute an old anchor exactly.
func headsAsOf(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, t time.Time) ([]runHead, int64, error) {
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT ON (run_id) run_id, seq, hash
		   FROM agent_intent_log
		  WHERE company_id = $1 AND created_at <= $2
		  ORDER BY run_id, seq DESC`, companyID, t)
	if err != nil {
		return nil, 0, fmt.Errorf("intentlog: read heads: %w", err)
	}
	defer rows.Close()
	var heads []runHead
	var events int64
	for rows.Next() {
		var h runHead
		if err := rows.Scan(&h.runID, &h.seq, &h.hash); err != nil {
			return nil, 0, err
		}
		events += int64(h.seq)
		heads = append(heads, h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	sort.Slice(heads, func(i, j int) bool { return bytes.Compare(heads[i].runID[:], heads[j].runID[:]) < 0 })
	return heads, events, nil
}

func anchorRoot(prev []byte, heads []runHead) []byte {
	h := blake3.New()
	_, _ = h.Write([]byte(chainDomain + ".anchor"))
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(prev)))
	_, _ = h.Write(n[:])
	_, _ = h.Write(prev)
	for _, r := range heads {
		_, _ = h.Write(r.runID[:])
		var s [8]byte
		binary.BigEndian.PutUint64(s[:], uint64(r.seq))
		_, _ = h.Write(s[:])
		_, _ = h.Write(r.hash)
	}
	return h.Sum(nil)
}

// Anchor writes one anchor over every run head in the company as of now,
// chained to the previous anchor.
func Anchor(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, now time.Time) (AnchorRow, error) {
	at := now.UTC().Truncate(time.Microsecond)
	var prev []byte
	err := tx.QueryRow(ctx,
		`SELECT root FROM agent_intent_anchor WHERE company_id = $1
		  ORDER BY anchored_at DESC, id DESC LIMIT 1`, companyID).Scan(&prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AnchorRow{}, fmt.Errorf("intentlog: read last anchor: %w", err)
	}
	heads, events, err := headsAsOf(ctx, tx, companyID, at)
	if err != nil {
		return AnchorRow{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return AnchorRow{}, err
	}
	row := AnchorRow{ID: id, AnchoredAt: at, RunCount: len(heads), EventCount: events, PrevRoot: prev,
		Root: anchorRoot(prev, heads)}
	_, err = tx.Exec(ctx,
		`INSERT INTO agent_intent_anchor (id, company_id, anchored_at, run_count, event_count, prev_root, root)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		row.ID, companyID, row.AnchoredAt, row.RunCount, row.EventCount, row.PrevRoot, row.Root)
	if err != nil {
		return AnchorRow{}, fmt.Errorf("intentlog: insert anchor: %w", err)
	}
	return row, nil
}

// VerifyAnchors recomputes every anchor of a company from the intent rows and
// returns how many were checked. A truncated run tail, a deleted anchor or an
// edited head hash all fail here even when each surviving run chain is valid.
func VerifyAnchors(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (int, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, anchored_at, run_count, event_count, prev_root, root
		   FROM agent_intent_anchor WHERE company_id = $1
		  ORDER BY anchored_at, id`, companyID)
	if err != nil {
		return 0, fmt.Errorf("intentlog: list anchors: %w", err)
	}
	var anchors []AnchorRow
	for rows.Next() {
		var a AnchorRow
		if err := rows.Scan(&a.ID, &a.AnchoredAt, &a.RunCount, &a.EventCount, &a.PrevRoot, &a.Root); err != nil {
			rows.Close()
			return 0, err
		}
		anchors = append(anchors, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var prev []byte
	for i, a := range anchors {
		if !bytes.Equal(a.PrevRoot, prev) {
			return i, fmt.Errorf("%w: anchor %s does not chain to its predecessor (an anchor was removed)", ErrChainBroken, a.ID)
		}
		heads, events, err := headsAsOf(ctx, tx, companyID, a.AnchoredAt)
		if err != nil {
			return i, err
		}
		if len(heads) != a.RunCount || events != a.EventCount || !bytes.Equal(anchorRoot(prev, heads), a.Root) {
			return i, fmt.Errorf("%w: anchor %s at %s does not recompute (runs %d/%d, events %d/%d) — intent rows anchored by it changed",
				ErrChainBroken, a.ID, a.AnchoredAt.Format(time.RFC3339), len(heads), a.RunCount, events, a.EventCount)
		}
		prev = a.Root
	}
	return len(anchors), nil
}
