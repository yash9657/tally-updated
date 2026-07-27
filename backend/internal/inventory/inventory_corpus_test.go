package inventory

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// The golden inventory corpus is the shared oracle for stock valuation: the Go
// valuer below and the INDEPENDENT Python re-derivation in corpora/validate.py
// must both reproduce every fixture's expected block. Neither is allowed to be
// a checksum of the other — validate.py re-derives the arithmetic from the
// movements, so a fixture whose expected value is wrong fails there even if
// this package were wrong in the same way.

// fixturePosition is the JSON shape of one expected (or opening) position. The
// nullable dimensions are pointers so that a JSON null (SQL NULL) is
// distinguishable from an empty string, exactly as the schema requires.
type fixturePosition struct {
	StockItem string  `json:"stockItem"`
	Godown    *string `json:"godown"`
	Batch     *string `json:"batch"`

	Qty  string `json:"qty"`
	Rate string `json:"rate"`

	OpeningQty   string `json:"openingQty"`
	OpeningValue string `json:"openingValue"`
	InwardQty    string `json:"inwardQty"`
	InwardValue  string `json:"inwardValue"`
	OutwardQty   string `json:"outwardQty"`
	OutwardValue string `json:"outwardValue"`
	ClosingQty   string `json:"closingQty"`
	ClosingValue string `json:"closingValue"`
	ClosingRate  string `json:"closingRate"`
}

// key builds the PositionKey a fixture position names, mapping JSON null to the
// empty string the package uses for a NULL dimension.
func (p fixturePosition) key() PositionKey {
	k := PositionKey{StockItemID: p.StockItem}
	if p.Godown != nil {
		k.GodownID = *p.Godown
	}
	if p.Batch != nil {
		k.BatchID = *p.Batch
	}
	return k
}

type fixtureMovement struct {
	VoucherDate      string  `json:"voucherDate"`
	VoucherNo        string  `json:"voucherNo"`
	VersionCreatedAt string  `json:"versionCreatedAt"`
	VersionID        string  `json:"versionId"`
	LineNo           int     `json:"lineNo"`
	StockItem        string  `json:"stockItem"`
	Godown           *string `json:"godown"`
	Batch            *string `json:"batch"`
	Direction        string  `json:"direction"`
	Qty              string  `json:"qty"`
	Rate             string  `json:"rate"`
	DiscountPct      string  `json:"discountPct"`
}

type fixtureBatch struct {
	ID         string `json:"id"`
	StockItem  string `json:"stockItem"`
	ExpiryDate string `json:"expiryDate"`
}

type inventoryFixture struct {
	ID              string            `json:"id"`
	Description     string            `json:"description"`
	ValuationMethod string            `json:"valuationMethod"`
	Godowns         []string          `json:"godowns"`
	Batches         []fixtureBatch    `json:"batches"`
	Openings        []fixturePosition `json:"openings"`
	Movements       []fixtureMovement `json:"movements"`
	BackdatedInward *struct {
		VoucherNo       string          `json:"voucherNo"`
		ExpectedWithout fixturePosition `json:"expectedWithout"`
	} `json:"backdatedInward"`
	Expected struct {
		Positions []fixturePosition `json:"positions"`
	} `json:"expected"`

	// path is the file the fixture came from, so a failure names it.
	path string
}

func corporaInventoryDir(t *testing.T) string {
	t.Helper()
	// This file lives at backend/internal/inventory; the corpus lives at
	// <repo>/corpora/inventory. Walk up to the repo root.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "corpora", "inventory")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate corpora/inventory above %q", dir)
	return ""
}

func loadFixtures(t *testing.T) []inventoryFixture {
	t.Helper()
	dir := corporaInventoryDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpora dir: %v", err)
	}
	var fixtures []inventoryFixture
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var f inventoryFixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatalf("unmarshal %s: %v", path, err)
		}
		f.path = path
		fixtures = append(fixtures, f)
	}
	if len(fixtures) == 0 {
		t.Fatal("no inventory corpus fixtures were read — the valuation gate is not running")
	}
	return fixtures
}

func mustDec(t *testing.T, field, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("%s: parse decimal %q: %v", field, s, err)
	}
	return d
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse voucherDate %q: %v", s, err)
	}
	return d
}

func mustStamp(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse versionCreatedAt %q: %v", s, err)
	}
	return ts
}

// movements converts a fixture's movement list into the package's type.
func (f inventoryFixture) toMovements(t *testing.T) []Movement {
	t.Helper()
	out := make([]Movement, 0, len(f.Movements))
	for _, m := range f.Movements {
		pos := fixturePosition{StockItem: m.StockItem, Godown: m.Godown, Batch: m.Batch}
		out = append(out, Movement{
			VoucherDate:      mustDate(t, m.VoucherDate),
			VoucherNo:        m.VoucherNo,
			VersionCreatedAt: mustStamp(t, m.VersionCreatedAt),
			LineNo:           m.LineNo,
			VoucherVersionID: m.VersionID,
			Position:         pos.key(),
			Direction:        Direction(m.Direction),
			Qty:              mustDec(t, "qty", m.Qty),
			Rate:             mustDec(t, "rate", m.Rate),
			DiscountPct:      mustDec(t, "discountPct", m.DiscountPct),
		})
	}
	return out
}

func (f inventoryFixture) toOpenings(t *testing.T) []Opening {
	t.Helper()
	out := make([]Opening, 0, len(f.Openings))
	for _, o := range f.Openings {
		out = append(out, Opening{
			Position: o.key(),
			Qty:      mustDec(t, "opening qty", o.Qty),
			Rate:     mustDec(t, "opening rate", o.Rate),
		})
	}
	return out
}

// assertPosition compares one replayed position against the fixture's expected
// block. Failure messages state the money consequence, not "got x want y".
func assertPosition(t *testing.T, fixtureID string, key PositionKey, want fixturePosition, got Position) {
	t.Helper()
	checks := []struct {
		what string
		got  decimal.Decimal
		want string
	}{
		{"opening quantity", got.OpeningQty, want.OpeningQty},
		{"opening value", got.OpeningValue, want.OpeningValue},
		{"inward quantity", got.InwardQty, want.InwardQty},
		{"inward value", got.InwardValue, want.InwardValue},
		{"outward quantity", got.OutwardQty, want.OutwardQty},
		{"CONSUMED (outward) value", got.OutwardValue, want.OutwardValue},
		{"closing quantity", got.ClosingQty, want.ClosingQty},
		{"closing value", got.ClosingValue, want.ClosingValue},
		{"closing rate", got.ClosingRate, want.ClosingRate},
	}
	for _, c := range checks {
		w := mustDec(t, c.what, c.want)
		if !c.got.Equal(w) {
			t.Errorf(
				"%s: position %+v would report a %s of %s, but the books say %s — the stock summary and the balance sheet would disagree by %s",
				fixtureID, key, c.what, c.got.StringFixed(4), w.StringFixed(4), c.got.Sub(w).StringFixed(4),
			)
		}
	}
}

// TestWeightedAverageCorpus replays every golden fixture and demands its exact
// expected position. This is the gate for the whole valuation contract.
func TestWeightedAverageCorpus(t *testing.T) {
	for _, f := range loadFixtures(t) {
		t.Run(f.ID, func(t *testing.T) {
			valuer, err := ValuerFor(f.ValuationMethod)
			if err != nil {
				t.Fatalf("fixture %s names valuation method %q, which no valuer implements: %v", f.path, f.ValuationMethod, err)
			}

			got := ReplayAll(f.toOpenings(t), f.toMovements(t), valuer)
			if len(got) != len(f.Expected.Positions) {
				t.Fatalf("%s: replay produced %d positions but the fixture expects %d — an item/godown/batch bucket was blended away or invented", f.ID, len(got), len(f.Expected.Positions))
			}
			for _, want := range f.Expected.Positions {
				key := want.key()
				pos, ok := got[key]
				if !ok {
					t.Fatalf("%s: no position replayed for %+v — that stock exists in the books but would vanish from the stock summary", f.ID, key)
				}
				assertPosition(t, f.ID, key, want, pos)
			}
		})
	}
}

// TestValueIsConserved pins the identity every valuer must satisfy:
// opening + inward - consumed == closing. A valuer that consumed an outward at
// the SALE rate rather than at cost breaks this, and the difference is exactly
// the gross profit that would silently disappear into (or out of) stock.
func TestValueIsConserved(t *testing.T) {
	for _, f := range loadFixtures(t) {
		t.Run(f.ID, func(t *testing.T) {
			valuer, err := ValuerFor(f.ValuationMethod)
			if err != nil {
				t.Fatalf("ValuerFor(%q): %v", f.ValuationMethod, err)
			}
			for key, pos := range ReplayAll(f.toOpenings(t), f.toMovements(t), valuer) {
				want := pos.OpeningValue.Add(pos.InwardValue).Sub(pos.OutwardValue)
				if !pos.ClosingValue.Equal(want) {
					t.Errorf(
						"%s: position %+v closes at %s but opening+inward-consumed is %s — %s of stock value was created or destroyed by the replay",
						f.ID, key, pos.ClosingValue.StringFixed(4), want.StringFixed(4), pos.ClosingValue.Sub(want).StringFixed(4),
					)
				}
			}
		})
	}
}

// TestReplayIsOrderDeterministic shuffles every fixture's movements twenty ways
// and demands one identical answer. Weighted average is order-dependent, so a
// Replay that trusted its caller's order would return a different, equally
// confident closing value depending on which ORDER BY the report happened to
// use — and nobody would notice, because each answer is internally consistent.
func TestReplayIsOrderDeterministic(t *testing.T) {
	for _, f := range loadFixtures(t) {
		t.Run(f.ID, func(t *testing.T) {
			valuer, err := ValuerFor(f.ValuationMethod)
			if err != nil {
				t.Fatalf("ValuerFor(%q): %v", f.ValuationMethod, err)
			}
			openings := f.toOpenings(t)
			base := f.toMovements(t)
			canonical := ReplayAll(openings, base, valuer)

			// A fixed seed so a failure is reproducible from the log alone.
			rng := rand.New(rand.NewSource(20260727))
			for round := 0; round < 20; round++ {
				shuffled := make([]Movement, len(base))
				copy(shuffled, base)
				rng.Shuffle(len(shuffled), func(i, j int) {
					shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
				})

				got := ReplayAll(openings, shuffled, valuer)
				if len(got) != len(canonical) {
					t.Fatalf("%s: shuffle %d produced %d positions, canonical order produced %d", f.ID, round, len(got), len(canonical))
				}
				for key, want := range canonical {
					have, ok := got[key]
					if !ok {
						t.Fatalf("%s: shuffle %d lost position %+v entirely", f.ID, round, key)
					}
					if !have.ClosingValue.Equal(want.ClosingValue) || !have.ClosingQty.Equal(want.ClosingQty) || !have.OutwardValue.Equal(want.OutwardValue) {
						t.Errorf(
							"%s: shuffle %d valued position %+v at %s over %s units (consuming %s) but the canonical replay says %s over %s units (consuming %s) — the closing stock of this company would depend on the order rows came back from the database",
							f.ID, round, key,
							have.ClosingValue.StringFixed(4), have.ClosingQty.StringFixed(4), have.OutwardValue.StringFixed(4),
							want.ClosingValue.StringFixed(4), want.ClosingQty.StringFixed(4), want.OutwardValue.StringFixed(4),
						)
						break
					}
				}
			}
		})
	}
}

// TestBackdatedInwardRevaluesLaterOutward is the executable statement of why
// stock value is never materialised: inserting a purchase DATED BEFORE an
// already-posted issue changes what that issue cost, with the issue itself
// untouched. A cached or stored per-item rate cannot express this.
func TestBackdatedInwardRevaluesLaterOutward(t *testing.T) {
	found := 0
	for _, f := range loadFixtures(t) {
		if f.BackdatedInward == nil {
			continue
		}
		found++
		t.Run(f.ID, func(t *testing.T) {
			valuer, err := ValuerFor(f.ValuationMethod)
			if err != nil {
				t.Fatalf("ValuerFor(%q): %v", f.ValuationMethod, err)
			}
			openings := f.toOpenings(t)
			all := f.toMovements(t)

			// The world BEFORE the back-dated voucher was entered: same issue,
			// same date, same quantity — only the older purchase is missing.
			var without []Movement
			for i, m := range f.Movements {
				if m.VoucherNo == f.BackdatedInward.VoucherNo {
					continue
				}
				without = append(without, all[i])
			}
			if len(without) == len(all) {
				t.Fatalf("%s: backdatedInward.voucherNo %q matches no movement — the fixture cannot prove anything", f.ID, f.BackdatedInward.VoucherNo)
			}

			key := f.Expected.Positions[0].key()
			before := ReplayAll(openings, without, valuer)[key]
			after := ReplayAll(openings, all, valuer)[key]

			assertPosition(t, f.ID+" (before the back-dated purchase)", key, f.BackdatedInward.ExpectedWithout, before)
			assertPosition(t, f.ID+" (after the back-dated purchase)", key, f.Expected.Positions[0], after)

			if before.OutwardValue.Equal(after.OutwardValue) {
				t.Fatalf(
					"%s: the issue consumed %s both before and after a back-dated purchase was entered — the replay is NOT re-valuing history, so an altered or late-entered April invoice would leave July's closing stock wrong",
					f.ID, before.OutwardValue.StringFixed(4),
				)
			}
			if before.ClosingValue.Equal(after.ClosingValue) {
				t.Errorf("%s: closing value %s is unchanged by a back-dated purchase of real stock", f.ID, before.ClosingValue.StringFixed(4))
			}
		})
	}
	if found == 0 {
		t.Fatal("no fixture declares a backdatedInward block — the corpus no longer proves that valuation re-values history")
	}
}
