package tau

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/shopspring/decimal"
)

// Ledger is a ledger master in canonical form.
//
// ⚠️ THE FIELD LIST IS A DECISION ABOUT WHAT "THE SAME BOOKS" MEANS. Name,
// group, GSTIN, state and opening are what change a report, a return or a
// balance. Address, PIN and credit days do not, and a task that judged them
// would fail an agent for formatting a city name differently. A future task
// that genuinely needs one of them adds it here and to every fixture, in one
// reviewed change — never as an optional field some fixtures happen to set.
type Ledger struct {
	Name           string `json:"name"`
	Group          string `json:"group"`
	GSTIN          string `json:"gstin"`
	StateCode      string `json:"state_code"`
	OpeningBalance string `json:"opening_balance"`
	OpeningDC      string `json:"opening_dc"`
}

// StockItem is a stock master in canonical form.
type StockItem struct {
	Name       string `json:"name"`
	Unit       string `json:"unit"`
	HSN        string `json:"hsn"`
	GSTRatePct string `json:"gst_rate_pct"`
}

// Posting is one ledger leg. BankDate is set only on a bank ledger's leg that
// has been reconciled, which is how a bank-match task's effect is visible.
type Posting struct {
	Ledger   string `json:"ledger"`
	DrCr     string `json:"dr_cr"`
	Amount   string `json:"amount"`
	BankDate string `json:"bank_date,omitempty"`
}

// StockLine is one line of a voucher's item grid.
type StockLine struct {
	Item      string `json:"item"`
	Direction string `json:"direction"`
	Qty       string `json:"qty"`
	Rate      string `json:"rate"`
	Amount    string `json:"amount"`
}

// Voucher is a voucher in canonical form.
//
// ⛔ VOUCHER NUMBER, NARRATION AND THE FREE-TEXT PARTY ARE NOT PART OF IT, and
// that is the judge's most consequential decision. The number is the engine's
// or the operator's choice (auto-numbering, a supplier's bill number, a
// prefix); the narration is prose; `party` on an accounting-mode voucher is a
// label the request may or may not carry. None of them moves a balance, and a
// judge that compared them would measure how closely an agent imitates the
// fixture author's typing. The PARTY LEDGER is compared — it is a posting.
type Voucher struct {
	Class     string      `json:"class"`
	Date      string      `json:"date"`
	Postings  []Posting   `json:"postings"`
	Inventory []StockLine `json:"inventory,omitempty"`
}

// State is a company's books in canonical form.
type State struct {
	Ledgers    []Ledger    `json:"ledgers"`
	StockItems []StockItem `json:"stock_items"`
	Vouchers   []Voucher   `json:"vouchers"`
}

// snapshotWindow is wide enough to hold every fixture date. The day book is
// windowed, and a window that silently excluded a voucher would let a run that
// wrote to the wrong year pass as "wrote nothing".
const (
	snapshotFrom = "2000-04-01"
	snapshotTo   = "2099-03-31"
)

// Snapshot reads the books through the same HTTP surface an agent uses.
func Snapshot(ctx context.Context, b Books) (State, error) {
	var st State

	var ledgers struct {
		Ledgers []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			GroupName      string `json:"group_name"`
			GSTIN          string `json:"gstin"`
			StateCode      string `json:"state_code"`
			OpeningBalance string `json:"opening_balance"`
			OpeningDC      string `json:"opening_dc"`
			IsBank         bool   `json:"is_bank"`
		} `json:"ledgers"`
	}
	if err := getJSON(ctx, b, "/ledgers", &ledgers); err != nil {
		return st, err
	}
	var bankIDs []string
	for _, l := range ledgers.Ledgers {
		st.Ledgers = append(st.Ledgers, Ledger{
			Name: l.Name, Group: l.GroupName, GSTIN: l.GSTIN, StateCode: l.StateCode,
			OpeningBalance: l.OpeningBalance, OpeningDC: l.OpeningDC,
		})
		if l.IsBank {
			bankIDs = append(bankIDs, l.ID)
		}
	}

	var items struct {
		StockItems []struct {
			Name       string `json:"name"`
			UnitSymbol string `json:"unit_symbol"`
			HSN        string `json:"hsn"`
			GSTRatePct string `json:"gst_rate_pct"`
		} `json:"stock_items"`
	}
	if err := getJSON(ctx, b, "/stock-items", &items); err != nil {
		return st, err
	}
	for _, it := range items.StockItems {
		st.StockItems = append(st.StockItems, StockItem{
			Name: it.Name, Unit: it.UnitSymbol, HSN: it.HSN, GSTRatePct: it.GSTRatePct,
		})
	}

	// Bank dates, keyed by the leg they clear. The key is (voucher, ledger,
	// side, amount) rather than the reconciliation's leg_no, because leg_no is
	// the reconciliation module's ordinal and the day book's postings arrive in
	// its own order; joining two ordinals from two modules is how a date lands
	// on the wrong leg without anything noticing.
	bankDates := map[string][]string{}
	for _, id := range bankIDs {
		var brs struct {
			LedgerName string `json:"ledger_name"`
			Cleared    []struct {
				VoucherID string `json:"voucher_id"`
				Amount    string `json:"amount"`
				DrCr      string `json:"dr_cr"`
				BankDate  string `json:"bank_date"`
			} `json:"cleared"`
			Uncleared []struct {
				VoucherID string `json:"voucher_id"`
				Amount    string `json:"amount"`
				DrCr      string `json:"dr_cr"`
				BankDate  string `json:"bank_date"`
			} `json:"uncleared"`
		}
		if err := getJSON(ctx, b, "/bank-reconciliation/"+id+"?as_on="+snapshotTo, &brs); err != nil {
			return st, err
		}
		for _, l := range append(brs.Cleared, brs.Uncleared...) {
			if l.BankDate == "" {
				continue
			}
			k := legKey(l.VoucherID, brs.LedgerName, l.DrCr, l.Amount)
			bankDates[k] = append(bankDates[k], l.BankDate)
		}
	}

	var dbk struct {
		Entries []struct {
			VoucherID string `json:"voucher_id"`
			Op        string `json:"op"`
			Class     string `json:"class"`
			Date      string `json:"date"`
			Postings  []struct {
				Ledger string `json:"ledger"`
				DrCr   string `json:"dr_cr"`
				Amount string `json:"amount"`
			} `json:"postings"`
		} `json:"entries"`
	}
	q := url.Values{"from": {snapshotFrom}, "to": {snapshotTo}}
	if err := getJSON(ctx, b, "/day-book?"+q.Encode(), &dbk); err != nil {
		return st, err
	}
	for _, e := range dbk.Entries {
		if strings.EqualFold(e.Op, "DELETE") {
			continue
		}
		v := Voucher{Class: strings.ToLower(e.Class), Date: e.Date}
		for _, p := range e.Postings {
			k := legKey(e.VoucherID, p.Ledger, p.DrCr, p.Amount)
			bd := ""
			if ds := bankDates[k]; len(ds) > 0 {
				bd, bankDates[k] = ds[0], ds[1:]
			}
			v.Postings = append(v.Postings, Posting{Ledger: p.Ledger, DrCr: p.DrCr, Amount: p.Amount, BankDate: bd})
		}
		var full struct {
			Inventory []struct {
				Item      string `json:"item"`
				Direction string `json:"direction"`
				Qty       string `json:"qty"`
				Rate      string `json:"rate"`
				Amount    string `json:"amount"`
			} `json:"inventory"`
		}
		if err := getJSON(ctx, b, "/vouchers/"+e.VoucherID, &full); err != nil {
			return st, err
		}
		for _, l := range full.Inventory {
			v.Inventory = append(v.Inventory, StockLine{
				Item: l.Item, Direction: l.Direction, Qty: l.Qty, Rate: l.Rate, Amount: l.Amount,
			})
		}
		st.Vouchers = append(st.Vouchers, v)
	}
	// A bank date the snapshot could not attach to any leg is state the judge
	// would otherwise never see.
	for k, ds := range bankDates {
		if len(ds) > 0 {
			return st, fmt.Errorf("tau: bank date(s) %v for leg %s matched no day-book posting", ds, k)
		}
	}
	return st.Canonical(), nil
}

func legKey(voucherID, ledger, drcr, amount string) string {
	return voucherID + "|" + ledger + "|" + drcr + "|" + money(amount)
}

// money renders a decimal string at fixed-4; an unparseable value is kept
// verbatim so that it can only ever compare unequal, never equal by accident.
func money(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0.0000"
	}
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return "INVALID(" + s + ")"
	}
	return d.StringFixed(4)
}

// Canonical normalises every value (fixed-4 money, sorted legs and lines,
// empty dr/cr on a zero opening) so that two states that mean the same books
// marshal to the same bytes.
func (s State) Canonical() State {
	out := State{
		Ledgers:    make([]Ledger, 0, len(s.Ledgers)),
		StockItems: make([]StockItem, 0, len(s.StockItems)),
		Vouchers:   make([]Voucher, 0, len(s.Vouchers)),
	}
	for _, l := range s.Ledgers {
		l.Name = strings.TrimSpace(l.Name)
		l.OpeningBalance = money(l.OpeningBalance)
		l.GSTIN = strings.ToUpper(strings.TrimSpace(l.GSTIN))
		// A zero opening has no side. The master may store "D" for it, and a
		// fixture has no business stating which side nothing is on.
		if l.OpeningBalance == "0.0000" {
			l.OpeningDC = ""
		}
		out.Ledgers = append(out.Ledgers, l)
	}
	for _, it := range s.StockItems {
		it.Name = strings.TrimSpace(it.Name)
		it.GSTRatePct = money(it.GSTRatePct)
		out.StockItems = append(out.StockItems, it)
	}
	for _, v := range s.Vouchers {
		v.Class = strings.ToLower(strings.ReplaceAll(v.Class, " ", "-"))
		ps := make([]Posting, 0, len(v.Postings))
		for _, p := range v.Postings {
			p.Amount = money(p.Amount)
			p.DrCr = strings.ToUpper(p.DrCr)
			ps = append(ps, p)
		}
		sort.Slice(ps, func(i, j int) bool { return key(ps[i]) < key(ps[j]) })
		v.Postings = ps
		var ls []StockLine
		for _, l := range v.Inventory {
			l.Qty, l.Rate, l.Amount = money(l.Qty), money(l.Rate), money(l.Amount)
			l.Direction = strings.ToUpper(l.Direction)
			ls = append(ls, l)
		}
		sort.Slice(ls, func(i, j int) bool { return key(ls[i]) < key(ls[j]) })
		v.Inventory = ls
		out.Vouchers = append(out.Vouchers, v)
	}
	sort.Slice(out.Ledgers, func(i, j int) bool { return key(out.Ledgers[i]) < key(out.Ledgers[j]) })
	sort.Slice(out.StockItems, func(i, j int) bool { return key(out.StockItems[i]) < key(out.StockItems[j]) })
	sort.Slice(out.Vouchers, func(i, j int) bool { return key(out.Vouchers[i]) < key(out.Vouchers[j]) })
	return out
}

// key is the canonical JSON of a value: the identity the multiset compares on.
func key(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // every canonical type is plain strings and slices
	}
	return string(b)
}
