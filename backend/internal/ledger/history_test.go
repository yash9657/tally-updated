package ledger

// history_test.go — hermetic tests for the chain verifier.
//
// These build chains BY HAND with the real ChainHash, then damage them in each
// of the three ways a tamperer could, and assert the verifier names the right
// version and the right kind of damage. No database: the point is that
// VerifyChain is a pure function over stored bytes, so the live path and these
// tests exercise the SAME logic. The live half — defeating the append-only
// trigger as superuser and watching a real GET report the break — lives in
// internal/server/voucher_history_test.go.

import (
	"strings"
	"testing"
)

// chainOf builds a valid n-version chain, returning the rows AND the canonical
// payload bytes so a test can damage a chosen one.
func chainOf(t *testing.T, narrations ...string) []RawVersion {
	t.Helper()
	rows := make([]RawVersion, 0, len(narrations))
	var prev []byte
	for i, n := range narrations {
		op := "ALTER"
		if i == 0 {
			op = "CREATE"
		}
		p := CanonicalPayload{
			VoucherID:     "11111111-1111-1111-1111-111111111111",
			VoucherTypeID: "22222222-2222-2222-2222-222222222222",
			VersionNo:     i + 1,
			Op:            op,
			Class:         "journal",
			VoucherNo:     "7",
			VoucherDate:   "2026-04-01",
			Narration:     n,
			Postings: []CanonicalPosting{
				{LedgerID: "33333333-3333-3333-3333-333333333333", DrCr: "D", Amount: "100.0000"},
				{LedgerID: "44444444-4444-4444-4444-444444444444", DrCr: "C", Amount: "100.0000"},
			},
		}
		b, err := p.Marshal()
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		h := ChainHash(b, prev)
		rows = append(rows, RawVersion{
			VersionNo: i + 1, Op: op, PayloadJSON: b, Hash: h, PrevHash: prev,
		})
		prev = h
	}
	return rows
}

func TestVerifyChainAcceptsAnIntactChain(t *testing.T) {
	v := VerifyChain(chainOf(t, "created", "corrected", "corrected again"))
	if !v.OK {
		t.Fatalf("intact chain reported broken: %+v", v)
	}
	if v.VersionsChecked != 3 || v.BrokenAtVersion != 0 {
		t.Errorf("checked=%d brokenAt=%d, want 3 and 0", v.VersionsChecked, v.BrokenAtVersion)
	}
}

// A payload edited in place — the thing the append-only trigger exists to stop.
// Note the hash is left UNTOUCHED, which is precisely the tamperer's problem.
func TestVerifyChainDetectsAnEditedPayload(t *testing.T) {
	rows := chainOf(t, "created", "corrected", "corrected again")
	rows[1].PayloadJSON = []byte(strings.Replace(
		string(rows[1].PayloadJSON), `"narration":"corrected"`, `"narration":"tampered"`, 1))
	if strings.Contains(string(rows[1].PayloadJSON), "corrected\"") {
		t.Fatalf("test setup did not actually change the payload: %s", rows[1].PayloadJSON)
	}

	v := VerifyChain(rows)
	if v.OK {
		t.Fatal("an edited payload verified as intact")
	}
	if v.BrokenAtVersion != 2 {
		t.Errorf("brokenAtVersion = %d, want 2", v.BrokenAtVersion)
	}
	if !strings.Contains(v.Reason, "altered") {
		t.Errorf("reason %q does not say the version was altered", v.Reason)
	}
}

// Changing an AMOUNT is the tamper that matters commercially — and it must be
// caught for exactly the same reason, since the amount is inside the hash.
func TestVerifyChainDetectsAnEditedAmount(t *testing.T) {
	rows := chainOf(t, "created", "corrected")
	rows[0].PayloadJSON = []byte(strings.ReplaceAll(
		string(rows[0].PayloadJSON), `"100.0000"`, `"900.0000"`))
	v := VerifyChain(rows)
	if v.OK || v.BrokenAtVersion != 1 {
		t.Fatalf("an edited amount verified as intact or blamed the wrong version: %+v", v)
	}
}

// A spliced chain: version 3's prev_hash no longer points at version 2. Reported
// as a broken LINK, not as an altered payload — they mean different things.
func TestVerifyChainDetectsASplicedLink(t *testing.T) {
	rows := chainOf(t, "created", "corrected", "corrected again")
	rows[2].PrevHash = rows[0].Hash

	v := VerifyChain(rows)
	if v.OK {
		t.Fatal("a spliced chain verified as intact")
	}
	if v.BrokenAtVersion != 3 {
		t.Errorf("brokenAtVersion = %d, want 3", v.BrokenAtVersion)
	}
	if !strings.Contains(v.Reason, "does not link") {
		t.Errorf("reason %q does not describe a broken link", v.Reason)
	}
}

// A removed version leaves a gap in the numbering. Without this check a
// tamperer could delete the inconvenient middle version and re-link around it.
func TestVerifyChainDetectsARemovedVersion(t *testing.T) {
	rows := chainOf(t, "created", "corrected", "corrected again")
	rows = []RawVersion{rows[0], rows[2]}

	v := VerifyChain(rows)
	if v.OK {
		t.Fatal("a chain with a removed version verified as intact")
	}
	if v.BrokenAtVersion != 3 {
		t.Errorf("brokenAtVersion = %d, want 3 (the row that now sits where 2 should be)", v.BrokenAtVersion)
	}
	if !strings.Contains(v.Reason, "removed from the log") {
		t.Errorf("reason %q does not describe a removed version", v.Reason)
	}
}

func TestVerifyChainRejectsAFirstVersionCarryingAPreviousHash(t *testing.T) {
	rows := chainOf(t, "created", "corrected")
	rows[0].PrevHash = rows[1].Hash
	if v := VerifyChain(rows); v.OK || v.BrokenAtVersion != 1 {
		t.Fatalf("version 1 with a prev_hash verified as intact: %+v", v)
	}
}

func TestVerifyChainOnAnEmptyLog(t *testing.T) {
	if v := VerifyChain(nil); v.OK {
		t.Fatal("an empty log verified as intact — nothing was checked")
	}
}

// The property that makes verification possible at all: a payload decoded from
// storage and re-marshalled reproduces the bytes that were hashed. Postgres
// JSONB does not preserve input text, so if this ever stopped holding, every
// intact chain in the database would start reporting as tampered.
func TestCanonicalPayloadRoundTripsToTheSameBytes(t *testing.T) {
	rows := chainOf(t, "created")
	original := rows[0].PayloadJSON

	// Key order deliberately shuffled and whitespace inserted, the way JSONB
	// would hand the bytes back.
	scrambled := `{ "narration" : "created", "op":"CREATE",
	  "voucher_date":"2026-04-01", "version_no":1, "party":"",
	  "voucher_id":"11111111-1111-1111-1111-111111111111",
	  "class":"journal", "voucher_no":"7",
	  "voucher_type_id":"22222222-2222-2222-2222-222222222222",
	  "postings":[{"amount":"100.0000","dr_cr":"D","ledger_id":"33333333-3333-3333-3333-333333333333"},
	              {"ledger_id":"44444444-4444-4444-4444-444444444444","amount":"100.0000","dr_cr":"C"}]}`

	v := VerifyChain([]RawVersion{{
		VersionNo:   1,
		Op:          "CREATE",
		PayloadJSON: []byte(scrambled),
		Hash:        rows[0].Hash,
		PrevHash:    nil,
	}})
	if !v.OK {
		t.Fatalf("re-canonicalising a reordered payload did not reproduce the hashed bytes: %s\noriginal: %s",
			v.Reason, original)
	}
}
