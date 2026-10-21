package server

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khata/cored/internal/agent"
	"github.com/khata/cored/internal/insights"
	"github.com/khata/cored/internal/policy"
)

// Every finding is a bundle: deny the entire bundle if any source report is
// inaccessible. Unknown detector types fail closed. Raw SQL, review notes and
// generated digest prose never enter the model context.
var insightReports = map[string][]string{
	insights.TypeCreditLimitBreach:  {"outstandings", "day-book"},
	insights.TypeDuplicateBill:      {"bills-payable", "day-book"},
	insights.TypePlaceOfSupply:      {"gstr1", "day-book"},
	insights.TypeHSNRate:            {"gstr1", "day-book", "stock-summary"},
	insights.TypeEWayBillNeeded:     {"gstr1", "day-book"},
	insights.TypeCustomerSlower:     {"outstandings", "day-book"},
	insights.TypeSupplierNotIn2B:    {"itc-reconciliation", "bills-payable"},
	insights.TypeSupplierPriceCreep: {"stock-item-vouchers", "bills-payable"},
	insights.TypeCustomerReorderDue: {"day-book", "outstandings"},
	insights.TypeReorderNow:         {"stock-summary", "stock-movement"},
	insights.TypeDeadStock:          {"stock-summary", "stock-movement"},
	insights.TypeCashShortfall:      {"cash-flow", "outstandings", "bills-payable"},
	insights.TypeGSTLiability:       {"gstr3b"},
	insights.TypeTDSNotDeposited:    {"tds-register", "day-book"},
	insights.TypeBackdatedEdits:     {"day-book"},
}

func (p agentProfiles) businessInsights(ctx context.Context, asOf string, may, mayResource func(string) bool) (agent.ProfileResult, error) {
	date := insights.BusinessDate(time.Now())
	if asOf != "" {
		var err error
		date, err = time.Parse("2006-01-02", asOf)
		if err != nil {
			return profileJSON(http.StatusUnprocessableEntity, map[string]string{"error": "as_of must be YYYY-MM-DD"}), nil
		}
	}
	rows, err := insights.List(ctx, p.s.pool, insights.ListFilter{Limit: 200})
	if err != nil {
		return agent.ProfileResult{}, err
	}
	items := make([]map[string]any, 0, 5)
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, row := range rows {
		reports, known := insightReports[row.Type]
		allowed := known
		if row.Type == insights.TypeCreditLimitBreach {
			allowed = allowed && mayResource("master")
		}
		if row.Type == insights.TypeHSNRate {
			allowed = allowed && mayResource("master")
		}
		for _, report := range reports {
			allowed = allowed && may(report)
		}
		if !allowed {
			continue
		}
		day, err := time.Parse("2006-01-02", row.AsOf)
		if err != nil || day.After(date) || day.Before(date.AddDate(0, 0, -3)) {
			continue
		}
		key := row.Type + "/" + row.EntityKey
		if seen[key] || counts[row.Type] >= 2 {
			continue
		}
		// Verify exactly the payload we will return, not a second fetched row.
		err = p.s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return insights.Verify(ctx, tx, row.Candidate())
		})
		if err != nil {
			continue
		} // stale or unavailable evidence is not an insight
		action := row.Action
		meaning := "An observed condition requiring review, not guaranteed savings."
		if row.Type == insights.TypeDuplicateBill {
			action = "Compare both source bills and vouchers before paying; if they are duplicates, preview a correction for human approval."
			meaning = "Matching supplier document numbers identify a possible duplicate, not proof that either purchase is invalid."
		}
		if row.Type == insights.TypeSupplierPriceCreep {
			meaning = "extra_cost applies the latest-minus-first rate to the whole historical quantity: a comparison scenario, NOT actual extra spending or achievable savings. Confirm specification, terms and alternative quotations."
		}
		if row.Type == insights.TypeDeadStock {
			meaning = "Stock value uses the latest purchase rate; it is not recoverable cash, profit or a guaranteed sale price. Check physical stock, seasonality and supplier return terms before acting."
		}
		if row.Type == insights.TypeReorderNow {
			meaning = "Cover extrapolates historical sales; verify open orders, supplier lead time and seasonality before ordering."
		}
		if row.Type == insights.TypeCashShortfall {
			meaning = "A projection from recorded due dates, not a probabilistic forecast or a promise that receipts will arrive."
		}
		items = append(items, map[string]any{"id": row.ID, "type": row.Type, "title": row.Title,
			"entity": row.EntityName, "as_of": row.AsOf, "facts": row.Fields,
			"summary": row.Summary, "next_action": action, "interpretation": meaning, "voucher_ids": row.VoucherIDs,
			"source_reports": reports, "evidence_verified": true})
		seen[key], counts[row.Type] = true, counts[row.Type]+1
		if len(items) == 5 {
			break
		}
	}
	out := profileJSON(http.StatusOK, map[string]any{"as_of": date.Format("2006-01-02"), "findings": items,
		"coverage": "Up to five accessible findings from deterministic detectors refreshed within three days, rechecked against the books. Empty findings do not establish that the business is healthy; inspect current reports or request missing history.",
		"usage":    "Figures describe exposure, balances or scenarios, not guaranteed savings. Do not add overlapping amounts. Read current source books before proposing any write; every write still needs preview and authorization."})
	// Cached facts can support advice, never authorize a financial mutation.
	out.Source = policy.SourceStoredText
	return out, nil
}
