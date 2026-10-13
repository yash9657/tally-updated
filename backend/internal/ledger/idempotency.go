package ledger

// IDEMPOTENCY KEYS ON VOUCHER CREATE — one request, delivered any number of
// times, is one voucher.
//
// Migration 0100 (read its header first) and docs/design/d123-w5-documents-bank.md §4.
//
// WHAT THIS CLOSES THAT 0077 DID NOT
// ---------------------------------
// 0077 makes "accept this insightd draft" once-only, which needs a DRAFT ID.
// The duplicate-post class of parity row 30 is wider than drafts: it is the
// same REQUEST arriving twice — a retry after a timeout whose voucher did land,
// a double submit, an outbox replay — and a hand-keyed voucher or a body the
// Bill Inbox could not tie to a draft has no draft id to be refused on. An
// idempotency key is the client naming the request, so the server can tell a
// retry from a second bill.
//
// THE THREE RULES, EACH WITH THE DEFECT IT PREVENTS
// -------------------------------------------------
//  1. THE KEY IS WRITTEN INSIDE THE VOUCHER'S OWN TRANSACTION. A key checked
//     in one transaction and recorded in another is a check-then-act: two
//     concurrent requests both see "new" and both post. Here the second blocks
//     on 0100's primary key until the first commits, trips it, and rolls back
//     every row it wrote — voucher, numbering advance, tax, TDS, bill refs.
//  2. A SECOND REQUEST UNDER A KNOWN KEY IS ANSWERED WITH THE FIRST ONE'S
//     RESULT, not refused. The client retried because it did not know whether
//     its write landed; "409, already exists" makes it guess again, and a
//     client that guesses "no" posts under a NEW key. The replay is marked
//     (PostResult.Replayed) so a caller can say "already posted" rather than
//     "saved" when that distinction matters to an operator.
//  3. THE KEY IS BOUND TO THE REQUEST'S CONTENT. The same key with a different
//     body is refused with ErrIdempotencyKeyReused, never replayed: replaying
//     voucher A for request B tells an operator B was posted when nothing of B
//     reached the books.
//
// A FAILED POST CONSUMES NO KEY: its transaction rolls back with the key row in
// it, so a retry of a refused request is re-evaluated, not replayed as a
// refusal. A key on an ALTER or DELETE is dropped (post() clears it), exactly
// as SourceDraftID is — idempotency here is about not creating twice, and an
// alter is already a full restatement keyed by the voucher id.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/khata/cored/internal/platform/db"
)

var (
	// ErrIdempotencyKeyReused is a key already used for a DIFFERENT request.
	// The HTTP layer answers 422 and names the voucher the key belongs to.
	ErrIdempotencyKeyReused = errors.New("ledger: idempotency key already used for a different request")
	// ErrBadIdempotencyKey is a key 0100's CHECK would refuse, refused here
	// first so the message names the rule rather than a constraint.
	ErrBadIdempotencyKey = errors.New("ledger: invalid idempotency key")
)

// idempotencyIndex is 0100's primary key, matched BY NAME for the same reason
// draftAcceptanceIndex is: a 23505 from any other constraint in the post
// transaction must not be mistaken for a replay.
const idempotencyIndex = "voucher_idempotency_pkey"

// errIdempotentConflict is the in-transaction marker for "another request
// committed this key first". It never leaves the package.
var errIdempotentConflict = errors.New("ledger: idempotency key committed by a concurrent request")

// validateIdempotencyKey mirrors 0100's voucher_idempotency_key_shape.
func validateIdempotencyKey(k string) error {
	if n := len(k); n < 8 || n > 200 {
		return fmt.Errorf("%w: it must be 8 to 200 characters, got %d", ErrBadIdempotencyKey, n)
	}
	for _, r := range k {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%w: it must not contain white space or control characters", ErrBadIdempotencyKey)
		}
	}
	return nil
}

// requestFingerprint is sha256 over the voucher input AS THE CALLER SENT IT,
// minus the key itself.
//
// ⛔ IT MUST BE TAKEN BEFORE post() NORMALISES ANYTHING. post() fills defaults
// (the supplier state, the voucher type, the number) inside its transaction;
// a fingerprint of the normalised input could differ between a first request
// and its retry if a default moved in between — the company's state edited, a
// numbering series advanced — and the retry would then be refused as a reused
// key for a request that is byte-for-byte the same.
func requestFingerprint(in VoucherInput) (string, error) {
	in.IdempotencyKey = ""
	b, err := json.Marshal(in)
	if err != nil {
		return "", fmt.Errorf("ledger: fingerprint request: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// replayKnownKey answers a request whose key is already recorded. done is true
// when the caller must return (res, err) as-is: a replay, or a reuse refusal.
func (s *Service) replayKnownKey(ctx context.Context, key, fingerprint string) (res PostResult, done bool, err error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return PostResult{}, true, db.ErrNoCompany
	}
	var (
		storedSHA string
		voucherID uuid.UUID
		raw       []byte
	)
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT request_sha256, voucher_id, result
			  FROM voucher_idempotency
			 WHERE company_id = $1 AND idempotency_key = $2`,
			companyID, key).Scan(&storedSHA, &voucherID, &raw)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return PostResult{}, false, nil
	case err != nil:
		return PostResult{}, true, fmt.Errorf("ledger: look up idempotency key: %w", err)
	}
	if storedSHA != fingerprint {
		return PostResult{}, true, fmt.Errorf(
			"%w: key %q already created voucher %s from a request with different content; "+
				"nothing was posted — use a new key for a new voucher",
			ErrIdempotencyKeyReused, key, voucherID)
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return PostResult{}, true, fmt.Errorf("ledger: decode stored result for key %q: %w", key, err)
	}
	res.Replayed = true
	return res, true, nil
}

// recordIdempotencyKey writes the key row inside the post transaction.
func recordIdempotencyKey(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, key, fingerprint string, res PostResult) error {
	raw, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("ledger: encode result for idempotency key: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO voucher_idempotency (company_id, idempotency_key, request_sha256, voucher_id, result)
		VALUES ($1, $2, $3, $4, $5)`,
		companyID, key, fingerprint, res.VoucherID, raw)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == idempotencyIndex {
			return errIdempotentConflict
		}
		return fmt.Errorf("ledger: record idempotency key: %w", err)
	}
	return nil
}

// normaliseIdempotencyKey trims surrounding white space, which a header
// round-trip can add; interior white space is refused by validation.
func normaliseIdempotencyKey(k string) string { return strings.TrimSpace(k) }
