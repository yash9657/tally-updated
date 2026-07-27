package ledger

// history.go — THE EDIT LOG, made readable.
//
// voucher_version has been an append-only, BLAKE3 hash-chained log since
// migration 0002: every create/alter/delete appends a row whose hash is
// BLAKE3(canonical payload || prev_hash), and a BEFORE UPDATE OR DELETE trigger
// forbids mutating it. That is the MCA Edit Log requirement satisfied as an
// architectural invariant rather than a feature.
//
// Until now NOTHING could read it. GetVoucher returns only the head version, so
// the property the whole design exists for — you can see every change, and you
// can tell if the record was tampered with — was invisible to any user.
//
// This file returns the chain AND a verification of it. The verification is the
// point: a log you cannot check is a claim, not evidence.
//
// WHY VERIFICATION CANNOT JUST RE-READ THE STORED BYTES. `payload` is a JSONB
// column, and Postgres does not preserve the input text: it reorders object keys
// and strips whitespace. So the bytes that were hashed are NOT recoverable with
// `payload::text`. They ARE recoverable by decoding the JSONB into
// CanonicalPayload and re-marshalling it, because encoding/json emits keys in
// struct-declaration order and every value in that struct is a string or an int
// (money is a fixed-4 string, so there is no float round-tripping). That
// re-derivation is what makes tampering detectable: edit the stored JSONB and
// the re-marshalled bytes change, so the recomputed hash no longer matches the
// recorded one.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// RawVersion is one stored row, exactly as the log holds it. Kept separate from
// VersionView so VerifyChain is a pure function over stored bytes — testable
// without a database, and used verbatim by the live path.
type RawVersion struct {
	VersionNo   int
	Op          string
	PayloadJSON []byte
	Hash        []byte
	PrevHash    []byte
}

// ChainVerification is the answer to "has this record been tampered with?".
//
// OK is true only when EVERY version recomputes to its recorded hash AND each
// version's prev_hash is the previous version's hash AND the version numbers run
// 1..n with no gaps. Anything else names the first version at which the chain
// fails, in words an auditor can act on.
type ChainVerification struct {
	OK              bool   `json:"ok"`
	VersionsChecked int    `json:"versions_checked"`
	// BrokenAtVersion is the version_no where verification first failed, or 0.
	BrokenAtVersion int    `json:"broken_at_version"`
	Reason          string `json:"reason"`
}

// VerifyChain recomputes the hash chain over the stored rows, which MUST be
// ordered by version_no ascending.
//
// The three failure modes are reported distinctly because they mean different
// things to whoever is looking: a payload that no longer matches its hash is a
// tampered RECORD; a prev_hash that does not point at its predecessor is a
// tampered or spliced CHAIN; a gap in version_no is a DELETED row. All three
// require defeating the append-only trigger, i.e. superuser access.
func VerifyChain(rows []RawVersion) ChainVerification {
	v := ChainVerification{VersionsChecked: len(rows)}
	if len(rows) == 0 {
		v.Reason = "no versions recorded for this voucher"
		return v
	}
	for i, r := range rows {
		want := i + 1
		if r.VersionNo != want {
			v.BrokenAtVersion = r.VersionNo
			v.Reason = fmt.Sprintf(
				"version numbering jumps to %d where %d was expected: a version has been removed from the log",
				r.VersionNo, want)
			return v
		}

		// Re-derive the bytes that were hashed (see the file header: JSONB does
		// not preserve the input text).
		var p CanonicalPayload
		if err := json.Unmarshal(r.PayloadJSON, &p); err != nil {
			v.BrokenAtVersion = r.VersionNo
			v.Reason = fmt.Sprintf("version %d: stored payload is not a canonical voucher payload: %v",
				r.VersionNo, err)
			return v
		}
		canonical, err := p.Marshal()
		if err != nil {
			v.BrokenAtVersion = r.VersionNo
			v.Reason = fmt.Sprintf("version %d: payload could not be re-canonicalised: %v", r.VersionNo, err)
			return v
		}

		// The link BEFORE the content, so a spliced chain is reported as a
		// spliced chain rather than as a payload mismatch it would also cause.
		if i == 0 {
			if len(r.PrevHash) != 0 {
				v.BrokenAtVersion = r.VersionNo
				v.Reason = "version 1 carries a previous hash, but it begins the chain"
				return v
			}
		} else if !bytes.Equal(r.PrevHash, rows[i-1].Hash) {
			v.BrokenAtVersion = r.VersionNo
			v.Reason = fmt.Sprintf(
				"version %d does not link to version %d: its recorded previous hash is not that version's hash",
				r.VersionNo, rows[i-1].VersionNo)
			return v
		}

		if got := ChainHash(canonical, r.PrevHash); !bytes.Equal(got, r.Hash) {
			v.BrokenAtVersion = r.VersionNo
			v.Reason = fmt.Sprintf(
				"version %d has been altered: its stored contents hash to %s, but the log records %s",
				r.VersionNo, hex.EncodeToString(got[:8]), hex.EncodeToString(r.Hash[:min(8, len(r.Hash))]))
			return v
		}
	}
	v.OK = true
	v.Reason = fmt.Sprintf("all %d version(s) verified: every entry hashes to its record and links to the previous one",
		len(rows))
	return v
}

// VersionView is one entry of the edit log as a reader sees it: what changed,
// who did it, when, and the hash that pins it.
type VersionView struct {
	VersionNo   int       `json:"version_no"`
	Op          string    `json:"op"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	NodeID      string    `json:"node_id"`
	Hash        string    `json:"hash"`
	PrevHash    string    `json:"prev_hash"`
	Class       string    `json:"class"`
	VoucherNo   string    `json:"voucher_no"`
	VoucherDate string    `json:"voucher_date"`
	Narration   string    `json:"narration"`
	Party       string    `json:"party"`
	// Postings AS RECORDED IN THIS VERSION — read from the hashed payload, not
	// from the posting table, so what is displayed is exactly what is signed.
	Postings        []CanonicalPosting        `json:"postings"`
	BillAllocations []CanonicalBillAllocation `json:"bill_allocations,omitempty"`
}

// VoucherHistory is the full edit log of one logical voucher.
type VoucherHistory struct {
	VoucherID    uuid.UUID         `json:"voucher_id"`
	IsDeleted    bool              `json:"is_deleted"`
	Versions     []VersionView     `json:"versions"`
	Verification ChainVerification `json:"verification"`
}

// VoucherHistory returns every recorded version of one logical voucher, oldest
// first, with the chain verification.
//
// It reads a DELETED voucher too — deliberately. A tombstone is the most
// interesting thing an edit log can show, and GetVoucher's `is_deleted = false`
// filter is what makes the deletion invisible today. ErrVoucherNotFound is
// reserved for a voucher this company has no versions for at all.
func (s *Service) VoucherHistory(ctx context.Context, voucherID uuid.UUID) (VoucherHistory, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return VoucherHistory{}, db.ErrNoCompany
	}

	h := VoucherHistory{VoucherID: voucherID, Versions: []VersionView{}}
	raw := []RawVersion{}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// The head row is a separate read because a voucher may be deleted (so
		// the tombstone is the head) and because is_deleted is not derivable
		// from the last op alone once re-delete idempotency is in play.
		err := tx.QueryRow(ctx,
			`SELECT is_deleted FROM voucher_head WHERE company_id = $1 AND voucher_id = $2`,
			companyID, voucherID).Scan(&h.IsDeleted)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("ledger: read voucher head: %w", err)
		}

		rows, err := tx.Query(ctx,
			`SELECT vv.version_no, vv.op::text, vv.created_by, vv.created_at, vv.node_id,
			        vv.hash, vv.prev_hash, vv.payload,
			        COALESCE(vv.voucher_no, ''),
			        COALESCE(to_char(vv.voucher_date, 'YYYY-MM-DD'), '')
			   FROM voucher_version vv
			  WHERE vv.company_id = $1 AND vv.voucher_id = $2
			  ORDER BY vv.version_no`,
			companyID, voucherID)
		if err != nil {
			return fmt.Errorf("ledger: query voucher versions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				vv          VersionView
				hash        []byte
				prevHash    []byte
				payloadJSON []byte
			)
			if err := rows.Scan(&vv.VersionNo, &vv.Op, &vv.CreatedBy, &vv.CreatedAt, &vv.NodeID,
				&hash, &prevHash, &payloadJSON, &vv.VoucherNo, &vv.VoucherDate); err != nil {
				return fmt.Errorf("ledger: scan voucher version: %w", err)
			}
			vv.Hash = hex.EncodeToString(hash)
			vv.PrevHash = hex.EncodeToString(prevHash)

			// Display the payload's OWN fields, not the row's denormalised
			// columns, so what is shown is what is hashed.
			var p CanonicalPayload
			if err := json.Unmarshal(payloadJSON, &p); err == nil {
				vv.Class = p.Class
				vv.Narration = p.Narration
				vv.Party = p.Party
				vv.Postings = p.Postings
				vv.BillAllocations = p.BillAllocations
				if p.VoucherNo != "" {
					vv.VoucherNo = p.VoucherNo
				}
				if p.VoucherDate != "" {
					vv.VoucherDate = p.VoucherDate
				}
			}
			if vv.Postings == nil {
				vv.Postings = []CanonicalPosting{}
			}

			h.Versions = append(h.Versions, vv)
			raw = append(raw, RawVersion{
				VersionNo: vv.VersionNo, Op: vv.Op,
				PayloadJSON: payloadJSON, Hash: hash, PrevHash: prevHash,
			})
		}
		return rows.Err()
	})
	if err != nil {
		return VoucherHistory{}, err
	}
	if len(h.Versions) == 0 {
		return VoucherHistory{}, ErrVoucherNotFound
	}
	h.Verification = VerifyChain(raw)
	return h, nil
}
