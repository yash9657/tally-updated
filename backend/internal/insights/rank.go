package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// rank.go: score = impact × p_real × actionability × novelty × affinity
// (RESEARCH-AGENTIC D8, r6 §5.3), and the ≤5-item selection with per-type
// weekly caps and suppression.
//
// ⛔ WHERE EACH TERM COMES FROM TODAY — stated, because five multiplied terms
// read as a learned model and four of them are not:
//
//   impact        the detector's rupee figure (payload-backed, evidence-checked).
//   p_real        Beta posterior per (tenant, type): the type's DECLARED prior
//                 (TypeDef.PriorReal/PriorFalse — this build's judgement) plus
//                 insight_feedback, where "useful" and "not useful" both count
//                 as REAL (the owner saw a true thing and may not care) and only
//                 "wrong" counts as false. A type whose p_real falls below
//                 DigestMinPReal (0.70 — the plan's "false-positive rate below
//                 30%") leaves the digest; it stays in the inbox.
//   actionability a declared constant per type (TypeDef.Actionability).
//   novelty       0.5^k, k = digests in the last 14 days (same recipient) that
//                 already carried this fingerprint — the recency penalty of
//                 r6 §6 with a 1-digest half-life, not a fitted decay.
//   affinity      2 × (1+u)/(2+u+n) for the recipient, u/n = their "useful" /
//                 "not useful" verdicts on the type: 1.0 with no data, towards 2
//                 for a type they keep marking useful, towards 0 for one they
//                 keep marking not useful. The company digest (recipient "")
//                 has affinity 1.

// MaxDigestItems is the hard cap (also a CHECK in 0102).
const MaxDigestItems = 5

// NoveltyDays and CapDays are the look-back windows.
const (
	NoveltyDays = 14
	CapDays     = 7
)

// DigestItem is one ranked insight with every term of its score.
type DigestItem struct {
	InsightID     uuid.UUID         `json:"insight_id"`
	Type          string            `json:"type"`
	Title         string            `json:"title"`
	Fingerprint   string            `json:"fingerprint"`
	EntityName    string            `json:"entity_name"`
	Rank          int               `json:"rank"`
	Score         float64           `json:"score"`
	Impact        string            `json:"impact_inr"`
	PReal         float64           `json:"p_real"`
	Actionability float64           `json:"actionability"`
	Novelty       float64           `json:"novelty"`
	Affinity      float64           `json:"affinity"`
	Payload       map[string]string `json:"payload"`
	VoucherIDs    []uuid.UUID       `json:"voucher_ids"`
	// Text is the item's narration, filled by the narrator (or the template).
	Text string `json:"text"`
}

// Verdicts are feedback counts.
type Verdicts struct{ Useful, NotUseful, Wrong int }

// PReal is the Beta posterior mean for a type.
func PReal(td TypeDef, v Verdicts) float64 {
	real := td.PriorReal + float64(v.Useful+v.NotUseful)
	return real / (td.PriorReal + td.PriorFalse + float64(v.Useful+v.NotUseful+v.Wrong))
}

// Affinity is the recipient's multiplier for a type.
func Affinity(v Verdicts) float64 {
	return 2 * (1 + float64(v.Useful)) / (2 + float64(v.Useful+v.NotUseful))
}

// Novelty is 0.5^timesShown.
func Novelty(timesShown int) float64 { return math.Pow(0.5, float64(timesShown)) }

// Score multiplies the five terms.
func Score(impact decimal.Decimal, pReal, actionability, novelty, affinity float64) float64 {
	f, _ := impact.Float64()
	return f * pReal * actionability * novelty * affinity
}

type suppKey struct{ typ, entity string }

// SelectDigest ranks the open insights for a recipient on a date and returns
// at most MaxDigestItems, honouring suppressions, the p_real floor and the
// per-type weekly caps. It reads; it writes nothing.
func SelectDigest(ctx context.Context, tx pgx.Tx, date time.Time, recipient string) ([]DigestItem, error) {
	date = dateOnly(date)
	// Feedback, per type (company) and per type for the recipient.
	byType := map[string]Verdicts{}
	byRecipient := map[string]Verdicts{}
	rows, err := tx.Query(ctx, `SELECT insight_type, verdict, given_by, count(*) FROM insight_feedback GROUP BY 1, 2, 3`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var typ, verdict, by string
		var n int
		if err := rows.Scan(&typ, &verdict, &by, &n); err != nil {
			rows.Close()
			return nil, err
		}
		add := func(m map[string]Verdicts) {
			v := m[typ]
			switch verdict {
			case VerdictUseful:
				v.Useful += n
			case VerdictNotUseful:
				v.NotUseful += n
			case VerdictWrong:
				v.Wrong += n
			}
			m[typ] = v
		}
		add(byType)
		if recipient != "" && by == recipient {
			add(byRecipient)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	supp := map[suppKey]bool{}
	srows, err := tx.Query(ctx, `SELECT insight_type, entity_key FROM insight_suppression WHERE until IS NULL OR until > $1`,
		date)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var k suppKey
		if err := srows.Scan(&k.typ, &k.entity); err != nil {
			srows.Close()
			return nil, err
		}
		supp[k] = true
	}
	srows.Close()

	// History: fingerprints shown (novelty) and type counts (caps).
	shown := map[string]int{}
	typeCount := map[string]int{}
	hrows, err := tx.Query(ctx, `
		SELECT digest_date, items::text FROM insight_digest
		 WHERE recipient = $1 AND digest_date < $2 AND digest_date >= $2 - $3::int`, recipient, date, NoveltyDays)
	if err != nil {
		return nil, err
	}
	for hrows.Next() {
		var on time.Time
		var raw string
		if err := hrows.Scan(&on, &raw); err != nil {
			hrows.Close()
			return nil, err
		}
		var items []DigestItem
		_ = json.Unmarshal([]byte(raw), &items)
		recent := !on.Before(date.AddDate(0, 0, -(CapDays - 1)))
		for _, it := range items {
			shown[it.Fingerprint]++
			if recent {
				typeCount[it.Type]++
			}
		}
	}
	hrows.Close()

	irows, err := tx.Query(ctx, selectInsight+` WHERE i.state = 'open'`)
	if err != nil {
		return nil, err
	}
	var cands []DigestItem
	for irows.Next() {
		in, err := scanInsight(irows)
		if err != nil {
			irows.Close()
			return nil, err
		}
		td, ok := ByCode(in.Type)
		if !ok || supp[suppKey{in.Type, ""}] || supp[suppKey{in.Type, in.EntityKey}] {
			continue
		}
		pr := PReal(td, byType[in.Type])
		if pr < DigestMinPReal {
			continue
		}
		aff := 1.0
		if recipient != "" {
			aff = Affinity(byRecipient[in.Type])
		}
		act, _ := in.Actionability.Float64()
		nov := Novelty(shown[in.Fingerprint])
		sc := Score(in.Impact, pr, act, nov, aff)
		if sc <= 0 {
			continue
		}
		cands = append(cands, DigestItem{InsightID: in.ID, Type: in.Type, Title: td.Title,
			Fingerprint: in.Fingerprint, EntityName: in.EntityName, Score: sc,
			Impact: Canonical(UnitINR, in.Impact), PReal: pr, Actionability: act, Novelty: nov, Affinity: aff,
			Payload: in.Payload, VoucherIDs: in.VoucherIDs})
	}
	irows.Close()
	if err := irows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Score != cands[j].Score {
			return cands[i].Score > cands[j].Score
		}
		return cands[i].InsightID.String() < cands[j].InsightID.String()
	})
	var out []DigestItem
	for _, c := range cands {
		td, _ := ByCode(c.Type)
		if typeCount[c.Type] >= td.WeeklyCap {
			continue
		}
		typeCount[c.Type]++
		c.Rank = len(out) + 1
		out = append(out, c)
		if len(out) == MaxDigestItems {
			break
		}
	}
	return out, nil
}

// Digest is a stored digest.
type Digest struct {
	ID                uuid.UUID    `json:"id"`
	Date              string       `json:"digest_date"`
	Recipient         string       `json:"recipient"`
	Items             []DigestItem `json:"items"`
	Narrative         string       `json:"narrative"`
	Narrator          string       `json:"narrator"`
	ModelID           string       `json:"model_id"`
	PromptVersion     string       `json:"prompt_version"`
	NarrationRejected string       `json:"narration_rejected"`
}

// ErrNoDigest is returned when no digest exists for the date.
var ErrNoDigest = errors.New("insights: no digest for that date")

// SaveDigest stores a digest; a second save for the same (date, recipient)
// keeps the first (the UNIQUE), so a retry after a crash cannot send two
// different mornings.
func SaveDigest(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, d Digest) (Digest, error) {
	if len(d.Items) > MaxDigestItems {
		return Digest{}, fmt.Errorf("insights: %d digest items (cap %d)", len(d.Items), MaxDigestItems)
	}
	items, _ := json.Marshal(d.Items)
	if d.Items == nil {
		items = []byte("[]")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO insight_digest (id, company_id, digest_date, recipient, items, narrative, narrator,
		                            model_id, prompt_version, narration_rejected)
		VALUES ($1, $2, $3::date, $4, $5::jsonb, $6, $7, $8, $9, $10)
		ON CONFLICT (company_id, digest_date, recipient) DO NOTHING`,
		uuid.Must(uuid.NewV7()), companyID, d.Date, d.Recipient, string(items), d.Narrative, d.Narrator,
		d.ModelID, d.PromptVersion, d.NarrationRejected); err != nil {
		return Digest{}, fmt.Errorf("insights: save digest: %w", err)
	}
	return readDigest(ctx, tx, d.Date, d.Recipient)
}

func readDigest(ctx context.Context, tx pgx.Tx, date, recipient string) (Digest, error) {
	var (
		d   Digest
		raw string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, to_char(digest_date, 'YYYY-MM-DD'), recipient, items::text, narrative, narrator,
		       model_id, prompt_version, narration_rejected
		  FROM insight_digest WHERE digest_date = $1::date AND recipient = $2`, date, recipient).Scan(
		&d.ID, &d.Date, &d.Recipient, &raw, &d.Narrative, &d.Narrator, &d.ModelID, &d.PromptVersion, &d.NarrationRejected)
	if errors.Is(err, pgx.ErrNoRows) {
		return Digest{}, ErrNoDigest
	}
	if err != nil {
		return Digest{}, err
	}
	_ = json.Unmarshal([]byte(raw), &d.Items)
	return d, nil
}

// GetDigest reads a stored digest.
func GetDigest(ctx context.Context, r Runner, date, recipient string) (Digest, error) {
	var d Digest
	err := r.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		d, err = readDigest(ctx, tx, date, recipient)
		return err
	})
	return d, err
}

// Narrator turns ranked items into text. internal/insights/narrate implements
// it; this package declares only the seam, so the engine never imports a
// model client.
type Narrator interface {
	Narrate(ctx context.Context, companyID uuid.UUID, items []DigestItem) (Narration, error)
}

// Narration is a narrator's answer.
type Narration struct {
	Texts         []string // one per item, same order
	Narrator      string   // "model" | "template"
	ModelID       string
	PromptVersion string
	Rejected      string // why the model's narration was not used, if it was not
}

// TemplateNarration renders each item's declared template.
func TemplateNarration(items []DigestItem) Narration {
	n := Narration{Narrator: "template"}
	for _, it := range items {
		td, _ := ByCode(it.Type)
		n.Texts = append(n.Texts, RenderTemplate(td.Template, it.Payload))
	}
	return n
}

// ComposeDigest selects, narrates and stores the digest for (date, recipient).
// Selection reads in one transaction; narration (which may call a model) runs
// OUTSIDE any transaction, so a slow provider holds no locks and no
// connection; the save is a third, idempotent transaction.
func ComposeDigest(ctx context.Context, r Runner, companyID uuid.UUID, date time.Time, recipient string, nar Narrator) (Digest, error) {
	ds := dateOnly(date).Format(isoDate)
	if d, err := GetDigest(ctx, r, ds, recipient); err == nil {
		return d, nil
	} else if !errors.Is(err, ErrNoDigest) {
		return Digest{}, err
	}
	var items []DigestItem
	if err := r.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// ⛔ NOT BEFORE ITS NIGHT (the wave-2 review). The first digest for a
		// date wins under UNIQUE, so one composed before that night's run (D
		// pressed between midnight and 02:00 IST) froze the day WITHOUT the
		// night's findings. Refused instead; the poller composes it once the
		// night has run (0104's due list).
		var ran bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM insight_run WHERE run_date = $1::date AND kind = 'nightly')`,
			ds).Scan(&ran); err != nil {
			return err
		}
		if !ran {
			return ErrNightlyNotRun
		}
		var err error
		items, err = SelectDigest(ctx, tx, date, recipient)
		return err
	}); err != nil {
		return Digest{}, err
	}
	n := TemplateNarration(items)
	if nar != nil && len(items) > 0 {
		got, err := nar.Narrate(ctx, companyID, items)
		if err == nil && len(got.Texts) == len(items) {
			n = got
		} else if err != nil {
			n.Rejected = err.Error()
		}
	}
	var narrative string
	for i := range items {
		items[i].Text = n.Texts[i]
		narrative += fmt.Sprintf("%d. %s\n", i+1, n.Texts[i])
	}
	var out Digest
	err := r.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = SaveDigest(ctx, tx, companyID, Digest{Date: ds, Recipient: recipient, Items: items,
			Narrative: narrative, Narrator: n.Narrator, ModelID: n.ModelID, PromptVersion: n.PromptVersion,
			NarrationRejected: n.Rejected})
		return err
	})
	return out, err
}

// ErrNightlyNotRun is ComposeDigest's refusal for a date whose nightly run
// has not happened: see ComposeDigest. The HTTP layer answers it 409.
var ErrNightlyNotRun = errors.New("insights: the nightly run for that date has not happened yet")
