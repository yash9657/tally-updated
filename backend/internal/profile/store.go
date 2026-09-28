package profile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// store.go writes and reads entity_facts / entity_edges under migration
// 0096's bi-temporal rule: the only UPDATE is superseded_at NULL -> now(),
// and closing a valid-time interval is a superseded original PLUS an inserted
// closed copy, never an in-place edit of valid_to.

// Fact is one fact a job produced, before it is stored.
type Fact struct {
	Kind          Kind
	EntityKey     string
	EntityID      uuid.UUID // uuid.Nil for tenant / stream
	EntityName    string
	Key           string
	QualifierKey  string
	QualifierName string
	// Exactly one of Num / Text. Text is only for a class fact.
	Num          *decimal.Decimal
	Text         string
	EvidenceKind string // "none" | "voucher" | "bank_line"
	Evidence     []uuid.UUID
}

// maxEvidence mirrors entity_facts_evidence_capped. A job keeps the ids that
// DECIDED the fact (the settling vouchers of the slowest bills, the stream's
// own occurrences) — never "the first 50 by uuid", which would be evidence of
// nothing.
const maxEvidence = 50

// ErrAsOfBehind is returned when a refresh names an as-of EARLIER than the
// one the job's current facts already describe. Backfilling history would
// need the new rows' valid_to set to the later as-of and is owed (d122 §6);
// refusing is the honest alternative to silently making an older belief
// "current" over a newer one.
var ErrAsOfBehind = errors.New("profile: as_of is earlier than the job's current facts")

func num(d decimal.Decimal) *decimal.Decimal { return &d }

// writeFacts supersedes every CURRENT fact of the given jobs and inserts the
// new set as of asOf, in the caller's transaction.
func writeFacts(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, asOf time.Time, windowFrom time.Time, jobs []string, facts []Fact) error {
	for _, fc := range facts {
		d, ok := Def(fc.Key)
		if !ok {
			return fmt.Errorf("profile: fact key %q is not in the vocabulary", fc.Key)
		}
		if d.Kind != fc.Kind {
			return fmt.Errorf("profile: fact %q is a %s fact, written for a %s", fc.Key, d.Kind, fc.Kind)
		}
		if (d.Unit == UnitClass) != (fc.Num == nil) {
			return fmt.Errorf("profile: fact %q: a class fact carries text, every other a number", fc.Key)
		}
		if len(fc.Evidence) > maxEvidence {
			return fmt.Errorf("profile: fact %q carries %d evidence ids (cap %d)", fc.Key, len(fc.Evidence), maxEvidence)
		}
	}

	var latest *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT max(valid_from) FROM entity_facts
		 WHERE superseded_at IS NULL AND valid_to IS NULL AND job = ANY($1)`, jobs).Scan(&latest); err != nil {
		return fmt.Errorf("profile: latest as-of: %w", err)
	}
	if latest != nil && latest.After(asOf) {
		return fmt.Errorf("%w (%s < %s)", ErrAsOfBehind, asOf.Format(isoDate), latest.Format(isoDate))
	}

	// 1. Close the current rows. A row whose valid_from is BEFORE asOf was
	// true until asOf: supersede it and insert a closed copy [valid_from,
	// asOf). A row AT asOf is being recomputed: supersede it only.
	//
	// ⛔ clock_timestamp(), NOT now(). now() is the TRANSACTION START, and a
	// refresh that waited on the advisory lock started BEFORE the refresh
	// that held it committed — so now() stamped a superseded_at EARLIER than
	// the recorded_at of the row it superseded, and the
	// entity_facts_superseded_after_recorded CHECK refused it. Measured:
	// TestConcurrentRefreshesSerialise failed exactly so with 4 concurrent
	// refreshes. The wall clock at the UPDATE is after the other commit.
	if _, err := tx.Exec(ctx, `
		WITH closed AS (
		    UPDATE entity_facts SET superseded_at = clock_timestamp()
		     WHERE superseded_at IS NULL AND valid_to IS NULL AND job = ANY($1)
		 RETURNING *
		)
		INSERT INTO entity_facts
		    (id, company_id, entity_kind, entity_key, entity_id, entity_name, fact_key,
		     qualifier_key, qualifier_name, value_num, value_text, unit, source, job,
		     job_version, window_from, valid_from, valid_to, evidence_kind, evidence_ids,
		     reconciles_to)
		SELECT gen_random_uuid(), company_id, entity_kind, entity_key, entity_id, entity_name, fact_key,
		       qualifier_key, qualifier_name, value_num, value_text, unit, source, job,
		       job_version, window_from, valid_from, $2::date, evidence_kind, evidence_ids,
		       reconciles_to
		  FROM closed
		 WHERE valid_from < $2::date`, jobs, asOf); err != nil {
		return fmt.Errorf("profile: close current facts: %w", err)
	}

	// 2. Insert the new set.
	b := &pgx.Batch{}
	for _, fc := range facts {
		d, _ := Def(fc.Key)
		var entityID any
		if fc.EntityID != uuid.Nil {
			entityID = fc.EntityID
		}
		var val any
		var text any
		if fc.Num != nil {
			val = fc.Num.Round(4)
		} else {
			text = fc.Text
		}
		var reconciles any
		if d.ReconcilesTo != "" {
			reconciles = d.ReconcilesTo
		}
		ek := fc.EvidenceKind
		if ek == "" {
			ek = "none"
		}
		ev := fc.Evidence
		if ev == nil {
			ev = []uuid.UUID{}
		}
		b.Queue(`
			INSERT INTO entity_facts
			    (id, company_id, entity_kind, entity_key, entity_id, entity_name, fact_key,
			     qualifier_key, qualifier_name, value_num, value_text, unit, source, job,
			     job_version, window_from, valid_from, evidence_kind, evidence_ids, reconciles_to)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'computed',$13,$14,$15,$16,$17,$18,$19)`,
			uuid.Must(uuid.NewV7()), companyID, string(fc.Kind), fc.EntityKey, entityID, fc.EntityName, fc.Key,
			fc.QualifierKey, fc.QualifierName, val, text, string(d.Unit), d.Job,
			jobVersion, windowFrom, asOf, ek, ev, reconciles)
	}
	br := tx.SendBatch(ctx, b)
	for range facts {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("profile: insert fact: %w", err)
		}
	}
	return br.Close()
}

// Edge is one relationship a job produced.
type Edge struct {
	SrcKind         Kind
	SrcKey, SrcName string
	DstKind         Kind
	DstKey, DstName string
	EdgeKind        string // supplies_item | buys_item
	Weight          decimal.Decimal
	Evidence        []uuid.UUID
}

func writeEdges(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, asOf, windowFrom time.Time, job string, edges []Edge) error {
	if _, err := tx.Exec(ctx, `
		WITH closed AS (
		    UPDATE entity_edges SET superseded_at = clock_timestamp()
		     WHERE superseded_at IS NULL AND valid_to IS NULL AND job = $1
		 RETURNING *
		)
		INSERT INTO entity_edges
		    (id, company_id, src_kind, src_key, src_name, dst_kind, dst_key, dst_name, edge_kind,
		     weight, source, job, job_version, window_from, valid_from, valid_to, evidence_ids)
		SELECT gen_random_uuid(), company_id, src_kind, src_key, src_name, dst_kind, dst_key, dst_name, edge_kind,
		       weight, source, job, job_version, window_from, valid_from, $2::date, evidence_ids
		  FROM closed WHERE valid_from < $2::date`, job, asOf); err != nil {
		return fmt.Errorf("profile: close current edges: %w", err)
	}
	b := &pgx.Batch{}
	for _, e := range edges {
		ev := e.Evidence
		if len(ev) > maxEvidence {
			ev = ev[:maxEvidence]
		}
		if ev == nil {
			ev = []uuid.UUID{}
		}
		b.Queue(`
			INSERT INTO entity_edges
			    (id, company_id, src_kind, src_key, src_name, dst_kind, dst_key, dst_name, edge_kind,
			     weight, source, job, job_version, window_from, valid_from, evidence_ids)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'computed',$11,$12,$13,$14,$15)`,
			uuid.Must(uuid.NewV7()), companyID, string(e.SrcKind), e.SrcKey, e.SrcName, string(e.DstKind), e.DstKey,
			e.DstName, e.EdgeKind, e.Weight.Round(4), job, jobVersion, windowFrom, asOf, ev)
	}
	br := tx.SendBatch(ctx, b)
	for range edges {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("profile: insert edge: %w", err)
		}
	}
	return br.Close()
}

// StoredFact is a fact as read back.
type StoredFact struct {
	Kind          Kind   `json:"kind"`
	EntityKey     string `json:"entity_key"`
	EntityName    string `json:"entity_name"`
	Key           string `json:"fact_key"`
	QualifierKey  string `json:"qualifier_key,omitempty"`
	QualifierName string `json:"qualifier_name,omitempty"`
	// Value is the wire string: a number as StringFixed(4) — the SAME
	// rendering the query DSL uses, so a tool's value and the profile_facts
	// screen compare as strings — or the class text.
	Value        string      `json:"value"`
	Unit         Unit        `json:"unit"`
	Job          string      `json:"job"`
	ValidFrom    string      `json:"valid_from"`
	ValidTo      string      `json:"valid_to,omitempty"`
	ReconcilesTo string      `json:"reconciles_to,omitempty"`
	EvidenceKind string      `json:"evidence_kind"`
	Evidence     []uuid.UUID `json:"evidence_ids,omitempty"`
}

const isoDate = "2006-01-02"

// readFacts returns the facts BELIEVED NOW to be true AT asOf (both temporal
// axes): superseded_at IS NULL, valid_from <= asOf < valid_to. A zero asOf
// means "current" (valid_to IS NULL) — exactly the DSL entity's guard.
func readFacts(ctx context.Context, tx pgx.Tx, asOf time.Time, where string, args ...any) ([]StoredFact, error) {
	var temporal string
	if asOf.IsZero() {
		temporal = "f.valid_to IS NULL"
	} else {
		args = append(args, asOf)
		temporal = fmt.Sprintf("f.valid_from <= $%d::date AND (f.valid_to IS NULL OR f.valid_to > $%d::date)", len(args), len(args))
	}
	rows, err := tx.Query(ctx, `
		SELECT f.entity_kind, f.entity_key, f.entity_name, f.fact_key, f.qualifier_key, f.qualifier_name,
		       f.value_num, COALESCE(f.value_text, ''), f.unit, f.job,
		       to_char(f.valid_from, 'YYYY-MM-DD'), COALESCE(to_char(f.valid_to, 'YYYY-MM-DD'), ''),
		       COALESCE(f.reconciles_to, ''), f.evidence_kind, f.evidence_ids
		  FROM entity_facts f
		 WHERE f.superseded_at IS NULL AND `+temporal+` AND (`+where+`)
		 ORDER BY f.entity_kind, f.entity_name, f.entity_key, f.fact_key, f.qualifier_key`, args...)
	if err != nil {
		return nil, fmt.Errorf("profile: read facts: %w", err)
	}
	defer rows.Close()
	var out []StoredFact
	for rows.Next() {
		var (
			sf   StoredFact
			kind string
			unit string
			v    *decimal.Decimal
			text string
		)
		if err := rows.Scan(&kind, &sf.EntityKey, &sf.EntityName, &sf.Key, &sf.QualifierKey, &sf.QualifierName,
			&v, &text, &unit, &sf.Job, &sf.ValidFrom, &sf.ValidTo, &sf.ReconcilesTo, &sf.EvidenceKind, &sf.Evidence); err != nil {
			return nil, fmt.Errorf("profile: scan fact: %w", err)
		}
		sf.Kind, sf.Unit = Kind(kind), Unit(unit)
		if v != nil {
			sf.Value = v.StringFixed(4)
		} else {
			sf.Value = text
		}
		out = append(out, sf)
	}
	return out, rows.Err()
}
