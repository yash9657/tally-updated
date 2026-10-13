package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// evidence.go is the gate PLAN-AGENTIC W4 names first: "every insight row
// carries evidence_sql and voucher_ids[] that reproduce its numbers".
//
// ⛔ THE CONTRACT. A detector's evidence SQL is a Go constant (never model
// text), bound with the candidate's EvidenceArgs as $1..$n (all text; the SQL
// casts), run in the caller's RLS-scoped transaction. It returns EXACTLY ONE
// ROW whose columns are:
//
//   - every Evidenced() key of the type's Fields, named exactly, and
//   - voucher_ids (uuid[]): the vouchers the numbers were computed from, in
//     any order (compared as a set).
//
// Verify compares each column to the payload with decimal EQUALITY (not
// rounding: an evidence query that returns 12500.004 against a payload of
// 12500.00 is a disagreement, and the SQL must state its own rounding) and
// requires the voucher sets to be identical. A payload key the SQL does not
// return, or a column the payload does not have, is a failure too: a figure
// nobody can re-derive is exactly what this file exists to stop.

// ErrEvidenceMismatch is returned when the evidence SQL does not reproduce
// the payload.
var ErrEvidenceMismatch = errors.New("insights: evidence does not reproduce the payload")

// Candidate is a detector's output before it is stored.
type Candidate struct {
	Type       string
	EntityKind string
	EntityKey  string
	EntityName string
	// Fingerprint is the dedupe identity (type + entity + period where the
	// insight is periodic).
	Fingerprint string
	AsOf        time.Time
	Impact      decimal.Decimal
	Payload     map[string]string
	EvidenceSQL string
	// EvidenceArgs are bound as $1..$n, as text.
	EvidenceArgs     []string
	VoucherIDs       []uuid.UUID
	TriggerVoucherID *uuid.UUID
	ExpiresAt        *time.Time
}

// Canonical formats a value of unit u the way a payload stores it.
func Canonical(u Unit, v decimal.Decimal) string {
	switch u {
	case UnitINR, UnitPct:
		// Two places, unless the value carries more (a purchase RATE of
		// 52.3456 is legal at NUMERIC(19,4)): rounding it to 52.35 here would
		// make the payload disagree with the books by design, and the evidence
		// gate would — rightly — refuse it.
		if r := v.Round(2); r.Equal(v) {
			return r.StringFixed(2)
		}
		return v.Round(4).String()
	case UnitQty:
		return v.Round(4).String()
	case UnitDays, UnitCount:
		return v.Truncate(0).String()
	}
	return v.String()
}

// evidenceRow runs the SQL and returns the one row as JSON-decoded values.
// Wrapping in to_jsonb gives one generic scan for every detector: numerics
// arrive as json.Number with their exact digits, dates as ISO strings, uuid[]
// as an array of strings.
func evidenceRow(ctx context.Context, tx pgx.Tx, sql string, args []string) (map[string]any, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(sql)), "select") &&
		!strings.HasPrefix(strings.ToLower(strings.TrimSpace(sql)), "with") {
		return nil, fmt.Errorf("insights: evidence must be a SELECT")
	}
	if strings.Contains(sql, ";") {
		return nil, fmt.Errorf("insights: evidence must be one statement")
	}
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}
	rows, err := tx.Query(ctx, `SELECT to_jsonb(e)::text FROM (`+sql+`) e`, anyArgs...)
	if err != nil {
		return nil, fmt.Errorf("insights: run evidence: %w", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		got = append(got, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("insights: run evidence: %w", err)
	}
	if len(got) != 1 {
		return nil, fmt.Errorf("%w: evidence returned %d rows, want exactly 1", ErrEvidenceMismatch, len(got))
	}
	dec := json.NewDecoder(strings.NewReader(got[0]))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("insights: decode evidence row: %w", err)
	}
	return m, nil
}

// Verify re-runs a candidate's evidence and reports any disagreement.
func Verify(ctx context.Context, tx pgx.Tx, c Candidate) error {
	td, ok := ByCode(c.Type)
	if !ok {
		return fmt.Errorf("insights: unknown type %q", c.Type)
	}
	row, err := evidenceRow(ctx, tx, c.EvidenceSQL, c.EvidenceArgs)
	if err != nil {
		return err
	}
	var diffs []string
	want := td.EvidenceKeys()
	for _, k := range want {
		f, _ := td.Field(k)
		pv, ok := c.Payload[k]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("payload has no %s", k))
			continue
		}
		ev, ok := row[k]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("evidence returns no column %s", k))
			continue
		}
		if msg := compareValue(f.Unit, pv, ev); msg != "" {
			diffs = append(diffs, k+": "+msg)
		}
	}
	for col := range row {
		if col == "voucher_ids" {
			continue
		}
		if f, ok := td.Field(col); !ok || !f.Unit.Evidenced() {
			diffs = append(diffs, fmt.Sprintf("evidence column %s is not an evidenced payload key", col))
		}
	}
	gotIDs, err := uuidSet(row["voucher_ids"])
	if err != nil {
		diffs = append(diffs, "voucher_ids: "+err.Error())
	} else if a, b := sortedIDs(c.VoucherIDs), gotIDs; !equalStrings(a, b) {
		diffs = append(diffs, fmt.Sprintf("voucher_ids: stored %d %v, evidence %d %v", len(a), a, len(b), b))
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		return fmt.Errorf("%w (%s %s): %s", ErrEvidenceMismatch, c.Type, c.EntityName, strings.Join(diffs, "; "))
	}
	return nil
}

func compareValue(u Unit, payload string, ev any) string {
	if ev == nil {
		return fmt.Sprintf("payload %s, evidence NULL", payload)
	}
	switch u {
	case UnitDate:
		s, ok := ev.(string)
		if !ok || s != payload {
			return fmt.Sprintf("payload %s, evidence %v", payload, ev)
		}
		return ""
	}
	var es string
	switch v := ev.(type) {
	case json.Number:
		es = v.String()
	case string:
		es = v
	default:
		return fmt.Sprintf("evidence %v is not a number", ev)
	}
	pd, err1 := decimal.NewFromString(payload)
	ed, err2 := decimal.NewFromString(es)
	if err1 != nil || err2 != nil {
		return fmt.Sprintf("payload %q / evidence %q not numeric", payload, es)
	}
	if !pd.Equal(ed) {
		return fmt.Sprintf("payload %s, evidence %s", payload, es)
	}
	if u == UnitDays || u == UnitCount {
		if _, err := strconv.ParseInt(payload, 10, 64); err != nil {
			return fmt.Sprintf("payload %s is not a whole number", payload)
		}
	}
	return ""
}

func uuidSet(v any) ([]string, error) {
	if v == nil {
		return []string{}, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("not an array: %v", v)
	}
	seen := map[string]bool{}
	var out []string
	for _, x := range arr {
		s, ok := x.(string)
		if !ok {
			return nil, fmt.Errorf("element %v is not a uuid", x)
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, err
		}
		if !seen[id.String()] {
			seen[id.String()] = true
			out = append(out, id.String())
		}
	}
	sort.Strings(out)
	return out, nil
}

func sortedIDs(ids []uuid.UUID) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		if !seen[id.String()] {
			seen[id.String()] = true
			out = append(out, id.String())
		}
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkShape refuses a candidate whose payload does not match its type's
// declared fields exactly. A detector cannot add a number the registry does
// not declare (the narrator could not name it and the evidence could not check
// it) nor omit one.
func checkShape(c Candidate) error {
	td, ok := ByCode(c.Type)
	if !ok {
		return fmt.Errorf("insights: unknown type %q", c.Type)
	}
	if td.EntityKind != c.EntityKind {
		return fmt.Errorf("insights: %s is about a %s, candidate says %s", c.Type, td.EntityKind, c.EntityKind)
	}
	for _, f := range td.Fields {
		if _, ok := c.Payload[f.Key]; !ok {
			return fmt.Errorf("insights: %s payload lacks %s", c.Type, f.Key)
		}
	}
	for k := range c.Payload {
		if _, ok := td.Field(k); !ok {
			return fmt.Errorf("insights: %s payload has undeclared key %s", c.Type, k)
		}
	}
	if c.Impact.IsNegative() {
		return fmt.Errorf("insights: %s impact is negative", c.Type)
	}
	if strings.TrimSpace(c.Fingerprint) == "" {
		return fmt.Errorf("insights: %s has no fingerprint", c.Type)
	}
	if len(c.VoucherIDs) > MaxVoucherIDs {
		return fmt.Errorf("insights: %s names %d vouchers (cap %d)", c.Type, len(c.VoucherIDs), MaxVoucherIDs)
	}
	return nil
}

// MaxVoucherIDs is 0104's insight_voucher_ids_capped. A list is complete up to
// it — the listed vouchers reproduce the figures — and the detectors whose
// lists are unbounded (a party's whole history) cut at it on BOTH sides, so a
// party beyond it is still raised; that it then cannot be summed from the
// list is d125's stated residual.
const MaxVoucherIDs = 5000
