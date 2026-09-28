package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// DetectorVersion is written on every row. Bump it when a detector's rule or
// arithmetic changes, so an insight raised under the old rule stays
// distinguishable (entity_facts' job_version, one table along).
const DetectorVersion = "w4-v1"

// Runner is the database capability the package needs (*db.Pool satisfies it).
type Runner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
	RunInReadOnlyTx(ctx context.Context, fn db.TxFunc) error
}

// WriteOutcome says what Store did with one candidate.
type WriteOutcome string

const (
	Inserted   WriteOutcome = "inserted"
	Unchanged  WriteOutcome = "unchanged"  // an open row with the same fingerprint and the same numbers
	Superseded WriteOutcome = "superseded" // an open row with different numbers was superseded by a new one
	Refused    WriteOutcome = "refused"    // the evidence did not reproduce the payload (never stored)
)

// store writes one candidate inside tx: shape check, EVIDENCE RE-RUN, then the
// fingerprint dedupe. It returns the id of the open row that now carries the
// fingerprint (the existing one when Unchanged).
//
// ⛔ THE EVIDENCE IS RE-RUN HERE, BEFORE THE INSERT, EVERY TIME. That is what
// makes "an insight that cannot reproduce its numbers is never stored" a
// property of the write path rather than of a test suite: the DB test proves
// the constants are right on a seeded book, this proves it on the tenant's
// book on the night it ran. A refusal is returned as Refused with the
// mismatch, and the caller counts it; it is not an error that stops the night.
func store(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, c Candidate) (uuid.UUID, WriteOutcome, error) {
	if err := checkShape(c); err != nil {
		return uuid.Nil, "", err
	}
	// A savepoint, so a type error in one detector's evidence (a bug) aborts
	// only its own statement and the night's other insights still land.
	sp, err := tx.Begin(ctx)
	if err != nil {
		return uuid.Nil, "", err
	}
	if verr := Verify(ctx, sp, c); verr != nil {
		_ = sp.Rollback(ctx)
		if errors.Is(verr, ErrEvidenceMismatch) || strings.Contains(verr.Error(), "run evidence") {
			return uuid.Nil, Refused, verr
		}
		return uuid.Nil, "", verr
	}
	if err := sp.Commit(ctx); err != nil {
		return uuid.Nil, "", err
	}

	payload, _ := json.Marshal(c.Payload)
	args, _ := json.Marshal(c.EvidenceArgs)
	if c.EvidenceArgs == nil {
		args = []byte("[]")
	}
	ids := c.VoucherIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}

	var (
		oldID      uuid.UUID
		oldPayload []byte
		oldIDs     []uuid.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT id, payload_json::text, voucher_ids FROM insight
		 WHERE fingerprint = $1 AND state = 'open'
		 FOR UPDATE`, c.Fingerprint).Scan(&oldID, &oldPayload, &oldIDs)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return uuid.Nil, "", fmt.Errorf("insights: find open: %w", err)
	default:
		if jsonEqual(oldPayload, payload) && equalStrings(sortedIDs(oldIDs), sortedIDs(ids)) {
			return oldID, Unchanged, nil
		}
		if _, err := tx.Exec(ctx, `
			UPDATE insight SET state = 'superseded', state_changed_at = clock_timestamp(),
			       state_changed_by = 'detector'
			 WHERE id = $1`, oldID); err != nil {
			return uuid.Nil, "", fmt.Errorf("insights: supersede: %w", err)
		}
	}

	td, _ := ByCode(c.Type)
	id := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `
		INSERT INTO insight
		  (id, company_id, insight_type, cadence, entity_kind, entity_key, entity_name,
		   fingerprint, as_of, impact_inr, actionability, payload_json, evidence_sql,
		   evidence_args, voucher_ids, trigger_voucher_id, detector_version, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::date, $10, $11, $12::jsonb, $13,
		        $14::jsonb, $15, $16, $17, $18)`,
		id, companyID, c.Type, string(td.Cadence), c.EntityKind, c.EntityKey, c.EntityName,
		c.Fingerprint, c.AsOf.Format(isoDate), c.Impact.Round(4), td.Actionability,
		string(payload), c.EvidenceSQL, string(args), ids, c.TriggerVoucherID,
		DetectorVersion, c.ExpiresAt); err != nil {
		return uuid.Nil, "", fmt.Errorf("insights: insert %s: %w", c.Type, err)
	}
	if oldID != uuid.Nil {
		return id, Superseded, nil
	}
	return id, Inserted, nil
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}

const isoDate = "2006-01-02"

// Insight is a stored row, as the inbox reads it.
type Insight struct {
	ID            uuid.UUID         `json:"id"`
	Type          string            `json:"type"`
	Title         string            `json:"title"`
	Action        string            `json:"action"`
	Cadence       string            `json:"cadence"`
	EntityKind    string            `json:"entity_kind"`
	EntityKey     string            `json:"entity_key"`
	EntityName    string            `json:"entity_name"`
	Fingerprint   string            `json:"fingerprint"`
	AsOf          string            `json:"as_of"`
	DetectedAt    time.Time         `json:"detected_at"`
	Impact        decimal.Decimal   `json:"impact_inr"`
	Actionability decimal.Decimal   `json:"actionability"`
	Payload       map[string]string `json:"payload"`
	Fields        []FieldView       `json:"fields"`
	Summary       string            `json:"summary"`
	EvidenceSQL   string            `json:"evidence_sql"`
	EvidenceArgs  []string          `json:"evidence_args"`
	VoucherIDs    []uuid.UUID       `json:"voucher_ids"`
	TriggerID     *uuid.UUID        `json:"trigger_voucher_id"`
	State         string            `json:"state"`
	Verdicts      map[string]int    `json:"verdicts"`
}

// FieldView is one payload value with its label and unit, for a screen.
type FieldView struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Unit  Unit   `json:"unit"`
	Value string `json:"value"`
}

const selectInsight = `
	SELECT i.id, i.insight_type, i.cadence, i.entity_kind, i.entity_key, i.entity_name,
	       i.fingerprint, to_char(i.as_of, 'YYYY-MM-DD'), i.detected_at, i.impact_inr,
	       i.actionability, i.payload_json::text, i.evidence_sql, i.evidence_args::text,
	       i.voucher_ids, i.trigger_voucher_id, i.state,
	       COALESCE((SELECT jsonb_object_agg(v, n)::text FROM
	                   (SELECT verdict AS v, count(*) AS n FROM insight_feedback f
	                     WHERE f.insight_id = i.id GROUP BY verdict) x), '{}')
	  FROM insight i`

func scanInsight(row pgx.Row) (Insight, error) {
	var (
		in               Insight
		payload, args, v string
	)
	if err := row.Scan(&in.ID, &in.Type, &in.Cadence, &in.EntityKind, &in.EntityKey, &in.EntityName,
		&in.Fingerprint, &in.AsOf, &in.DetectedAt, &in.Impact, &in.Actionability, &payload,
		&in.EvidenceSQL, &args, &in.VoucherIDs, &in.TriggerID, &in.State, &v); err != nil {
		return Insight{}, err
	}
	_ = json.Unmarshal([]byte(payload), &in.Payload)
	_ = json.Unmarshal([]byte(args), &in.EvidenceArgs)
	_ = json.Unmarshal([]byte(v), &in.Verdicts)
	if td, ok := ByCode(in.Type); ok {
		in.Title, in.Action = td.Title, td.Action
		for _, f := range td.Fields {
			in.Fields = append(in.Fields, FieldView{Key: f.Key, Label: f.Label, Unit: f.Unit, Value: in.Payload[f.Key]})
		}
		in.Summary = RenderTemplate(td.Template, in.Payload)
	}
	return in, nil
}

// Candidate reconstructs the evidence contract of a stored row, so the gate
// (and "Verify now" on the screen) can re-run it.
func (in Insight) Candidate() Candidate {
	return Candidate{Type: in.Type, EntityKind: in.EntityKind, EntityKey: in.EntityKey, EntityName: in.EntityName,
		Fingerprint: in.Fingerprint, Payload: in.Payload, EvidenceSQL: in.EvidenceSQL,
		EvidenceArgs: in.EvidenceArgs, VoucherIDs: in.VoucherIDs, Impact: in.Impact}
}

// ListFilter narrows List.
type ListFilter struct {
	State string // "" = open
	Type  string
	Limit int
}

// List returns insights, highest impact first.
func List(ctx context.Context, r Runner, f ListFilter) ([]Insight, error) {
	state := f.State
	if state == "" {
		state = "open"
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var out []Insight
	err := r.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, selectInsight+`
			 WHERE i.state = $1 AND ($2 = '' OR i.insight_type = $2)
			 ORDER BY i.impact_inr DESC, i.detected_at DESC, i.id
			 LIMIT $3`, state, f.Type, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			in, err := scanInsight(rows)
			if err != nil {
				return err
			}
			out = append(out, in)
		}
		return rows.Err()
	})
	return out, err
}

// ErrNotFound is returned for an unknown insight id (or one in another tenant,
// which RLS makes the same thing).
var ErrNotFound = errors.New("insights: not found")

// Get reads one insight.
func Get(ctx context.Context, r Runner, id uuid.UUID) (Insight, error) {
	var in Insight
	err := r.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		in, err = scanInsight(tx.QueryRow(ctx, selectInsight+` WHERE i.id = $1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return in, err
}

// VerifyStored re-runs a stored insight's evidence in a READ ONLY transaction.
func VerifyStored(ctx context.Context, r Runner, id uuid.UUID) error {
	in, err := Get(ctx, r, id)
	if err != nil {
		return err
	}
	return r.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return Verify(ctx, tx, in.Candidate())
	})
}

// Verdict values (the migration's CHECK set).
const (
	VerdictUseful    = "useful"
	VerdictNotUseful = "not_useful"
	VerdictWrong     = "wrong"
)

// ErrBadVerdict is returned for a verdict outside the three.
var ErrBadVerdict = errors.New("insights: verdict must be useful, not_useful or wrong")

// Feedback records one verdict. "wrong" also DISMISSES the insight: a false
// positive must leave the inbox at once, and it is what p_real learns from.
// "not_useful" dismisses it too (the owner has seen it and does not want it);
// "useful" leaves it open until the condition clears.
func Feedback(ctx context.Context, r Runner, companyID, id uuid.UUID, verdict, note, by string) error {
	if verdict != VerdictUseful && verdict != VerdictNotUseful && verdict != VerdictWrong {
		return ErrBadVerdict
	}
	if len(note) > 500 {
		note = note[:500]
	}
	return r.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var typ, state string
		err := tx.QueryRow(ctx, `SELECT insight_type, state FROM insight WHERE id = $1 FOR UPDATE`, id).Scan(&typ, &state)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO insight_feedback (id, company_id, insight_id, insight_type, verdict, given_by, note)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			uuid.Must(uuid.NewV7()), companyID, id, typ, verdict, by, note); err != nil {
			return err
		}
		if verdict != VerdictUseful && state == "open" {
			_, err = tx.Exec(ctx, `UPDATE insight SET state = 'dismissed', state_changed_at = clock_timestamp(),
			                              state_changed_by = $2 WHERE id = $1`, id, by)
		}
		return err
	})
}

// Suppression is one row of insight_suppression.
type Suppression struct {
	ID        uuid.UUID  `json:"id"`
	Type      string     `json:"type"`
	EntityKey string     `json:"entity_key"`
	Until     *time.Time `json:"until"`
	Reason    string     `json:"reason"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
}

// Suppress stops a type (entityKey "") or a type for one entity from reaching
// a digest until `until` (nil = until lifted). A second suppression of the
// same target replaces the first.
func Suppress(ctx context.Context, r Runner, companyID uuid.UUID, typ, entityKey string, until *time.Time, reason, by string) (uuid.UUID, error) {
	if _, ok := ByCode(typ); !ok {
		return uuid.Nil, fmt.Errorf("insights: unknown type %q", typ)
	}
	if strings.TrimSpace(reason) == "" {
		return uuid.Nil, errors.New("insights: a suppression needs a reason")
	}
	id := uuid.Must(uuid.NewV7())
	err := r.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM insight_suppression WHERE insight_type = $1 AND entity_key = $2`, typ, entityKey); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO insight_suppression (id, company_id, insight_type, entity_key, until, reason, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, companyID, typ, entityKey, until, reason, by)
		return err
	})
	return id, err
}

// Unsuppress lifts a suppression.
func Unsuppress(ctx context.Context, r Runner, id uuid.UUID) error {
	return r.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM insight_suppression WHERE id = $1`, id)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// Suppressions lists the active suppressions.
func Suppressions(ctx context.Context, r Runner, now time.Time) ([]Suppression, error) {
	var out []Suppression
	err := r.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, insight_type, entity_key, until, reason, created_by, created_at
			  FROM insight_suppression WHERE until IS NULL OR until > $1
			 ORDER BY created_at, id`, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s Suppression
			if err := rows.Scan(&s.ID, &s.Type, &s.EntityKey, &s.Until, &s.Reason, &s.CreatedBy, &s.CreatedAt); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	return out, err
}
