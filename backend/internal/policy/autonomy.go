package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
//
// ⛔ EVERY CHANGE LEAVES AN APPEND-ONLY RECORD. The row itself is upserted (it
// is the lookup table), so without a record "who made payments under Rs 5,000
// autonomous, and when" would be answerable only for the latest change. The
// record is a master_audit row (entity_type agent_autonomy, before and after),
// written in the same transaction: master_audit is already append-only by
// trigger, RLS-scoped, and read per entity type, so no reader of ledger or
// budget history ever sees these rows.
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
	var prior *int16
	err = tx.QueryRow(ctx, `
		SELECT level FROM agent_autonomy
		 WHERE company_id = $1 AND user_id IS NOT DISTINCT FROM $2::uuid AND acl_key = $3
		   AND amount_max_paise IS NOT DISTINCT FROM $4::bigint`,
		companyID, user, aclKey, amountMaxPaise).Scan(&prior)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("policy: read autonomy: %w", err)
	}
	var rowID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO agent_autonomy (id, company_id, user_id, acl_key, amount_max_paise, level, set_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT ON CONSTRAINT agent_autonomy_one_per_band
		DO UPDATE SET level = EXCLUDED.level, set_by = EXCLUDED.set_by, updated_at = now()
		RETURNING id`,
		id, companyID, user, aclKey, amountMaxPaise, int16(lvl), setBy).Scan(&rowID)
	if err != nil {
		return fmt.Errorf("policy: set autonomy: %w", err)
	}
	doc := func(level *int16) []byte {
		if level == nil {
			return nil
		}
		m := map[string]any{"acl_key": aclKey, "level": Level(*level).String(), "amount_max_paise": amountMaxPaise}
		if userID != uuid.Nil {
			m["user_id"] = userID.String()
		}
		b, _ := json.Marshal(m)
		return b
	}
	op := "CREATE"
	if prior != nil {
		op = "ALTER"
	}
	newLevel := int16(lvl)
	auditID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO master_audit (id, company_id, entity_type, entity_id, op, before_doc, after_doc, created_by)
		VALUES ($1, $2, 'agent_autonomy', $3, $4::voucher_op, $5, $6, $7)`,
		auditID, companyID, rowID, op, doc(prior), doc(&newLevel), "user:"+setBy.String()); err != nil {
		return fmt.Errorf("policy: record autonomy change: %w", err)
	}
	return nil
}

// AutonomyRow is one agent_autonomy row as the settings screen shows it.
type AutonomyRow struct {
	ID             uuid.UUID  `json:"id"`
	UserID         *uuid.UUID `json:"user_id,omitempty"`
	ACLKey         string     `json:"acl_key"`
	AmountMaxPaise *int64     `json:"amount_max_paise,omitempty"`
	Level          int        `json:"level"`
	SetBy          uuid.UUID  `json:"set_by"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ListLevels returns every autonomy row of the company in the caller's
// company-scoped transaction, ordered for display (key, tenant-wide first,
// narrowest band first).
func ListLevels(ctx context.Context, tx pgx.Tx) ([]AutonomyRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, user_id, acl_key, amount_max_paise, level, set_by, updated_at
		  FROM agent_autonomy
		 ORDER BY acl_key, (user_id IS NOT NULL), user_id, amount_max_paise ASC NULLS LAST`)
	if err != nil {
		return nil, fmt.Errorf("policy: list autonomy: %w", err)
	}
	defer rows.Close()
	out := []AutonomyRow{}
	for rows.Next() {
		var r AutonomyRow
		var lvl int16
		if err := rows.Scan(&r.ID, &r.UserID, &r.ACLKey, &r.AmountMaxPaise, &lvl, &r.SetBy, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Level = int(lvl)
		out = append(out, r)
	}
	return out, rows.Err()
}
