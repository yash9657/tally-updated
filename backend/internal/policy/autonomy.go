package policy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LookupLevel reads the autonomy level for (user, aclKey, amount) from
// migration 0093's table, inside the caller's company-scoped transaction.
//
// The most specific row wins: a row for this user beats the tenant-wide row
// (user_id NULL); among those, the NARROWEST band whose inclusive ceiling still
// contains the amount beats a wider one, and the any-amount band (NULL) is the
// widest of all. No row at all is DefaultLevel (L1).
//
// ⚠️ AN UNKNOWN AMOUNT MATCHES ONLY THE ANY-AMOUNT BAND. A banded row is a
// statement about amounts up to a ceiling; applying it to a commit whose amount
// Go could not compute would be applying it to an amount it never saw.
func LookupLevel(ctx context.Context, tx pgx.Tx, companyID, userID uuid.UUID, aclKey string, amountPaise *int64) (Level, error) {
	var lvl int16
	err := tx.QueryRow(ctx, `
		SELECT level FROM agent_autonomy
		 WHERE company_id = $1
		   AND acl_key = $2
		   AND (user_id = $3 OR user_id IS NULL)
		   AND (amount_max_paise IS NULL OR ($4::bigint IS NOT NULL AND amount_max_paise >= $4::bigint))
		 ORDER BY (user_id IS NULL) ASC, amount_max_paise ASC NULLS LAST
		 LIMIT 1`, companyID, aclKey, userID, amountPaise).Scan(&lvl)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultLevel, nil
	}
	if err != nil {
		return 0, fmt.Errorf("policy: read autonomy: %w", err)
	}
	if lvl < 0 || lvl > 3 {
		return 0, fmt.Errorf("policy: autonomy level %d out of range", lvl)
	}
	return Level(lvl), nil
}

// SetLevel upserts one autonomy row. userID uuid.Nil is the tenant-wide row;
// amountMaxPaise nil is the any-amount band.
func SetLevel(ctx context.Context, tx pgx.Tx, companyID, userID uuid.UUID, aclKey string, amountMaxPaise *int64, lvl Level, setBy uuid.UUID) error {
	if lvl < L0 || lvl > L3 {
		return fmt.Errorf("policy: level %d out of range", lvl)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	var user any
	if userID != uuid.Nil {
		user = userID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO agent_autonomy (id, company_id, user_id, acl_key, amount_max_paise, level, set_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT ON CONSTRAINT agent_autonomy_one_per_band
		DO UPDATE SET level = EXCLUDED.level, set_by = EXCLUDED.set_by, updated_at = now()`,
		id, companyID, user, aclKey, amountMaxPaise, int16(lvl), setBy)
	if err != nil {
		return fmt.Errorf("policy: set autonomy: %w", err)
	}
	return nil
}
