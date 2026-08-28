package server

// handler_groups_list.go — GET .../groups, the account-group list.
//
//	GET .../groups  -> Display / master
//
// ===========================================================================
// WHY THIS ROUTE DID NOT EXIST UNTIL T11, AND WHY T11 NEEDED IT
// ===========================================================================
// `POST /groups` has existed since Phase 1 and there has never been a GET beside
// it. That was liveable while every screen that cared about groups named them by
// NAME — createLedger takes a group NAME, and the statement screens get group ids
// inside a report tree they already had to fetch.
//
// A BUDGET LINE BROKE IT. Budgeting a GROUP means storing an account_group_id,
// and the one rule every master form in this product obeys is that IT NEVER ASKS
// FOR AN ID: a uuid is a thing no screen displays, so a box for one cannot be
// filled by a human. Without this route the group grain of T11 was reachable over
// HTTP and unreachable from the keyboard — which is the "present and unusable"
// shape masters/cost_centre.go's header describes for an axis nobody can create.
//
// ⛔ IT COSTS NO ACL CHANGE, AND THAT IS A FACT ABOUT routeAction RATHER THAN AN
// ASSUMPTION. `/groups` is already in isMasterPath, which maps a GET to
// (Display, "master") — the same token /ledgers, /units and /godowns read under,
// and one the seeded Data Entry level already holds (migration 0022 seeds the
// reads an operator needs to open the product at all). So this route is
// GOVERNED the day it mounts, by a rule that already exists, and mounting it
// grants nothing that reading the ledger list did not already grant: a group name
// is on every ledger row today.
//
// ⚠️ THE READ AND THE WRITE SHARE ONE TOKEN, deliberately, which is why the GET
// needed no new arm. isMasterPath is shared between routeAction's Display and
// Create arms precisely so one rule governs master maintenance — see its own
// comment on the stock-group case.
//
// IT NAMES NO AMOUNT. A group is a node in the chart of accounts: a name, a
// parent, a nature. What a group is WORTH is the Trial Balance and the Balance
// Sheet, which are separate report names with their own rules.

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// groupListItem is one account group on the wire.
type groupListItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// ParentName is "" for a primary group — never the nil uuid or a null, both of
	// which a naive client sends back as if they were a parent.
	ParentName string `json:"parent_name"`
	// Nature is ASSET / LIABILITY / INCOME / EXPENSE.
	//
	// ⚠️ IT IS SERVED BECAUSE A CALLER CANNOT DERIVE IT. A budget line takes its
	// SIDE from the target's nature (debit for what is spent or owned, credit for
	// what is earned or owed), and a form that could not show the nature would be
	// asking an operator to accept a derived side they cannot check.
	Nature string `json:"nature"`
	// IsPL distinguishes a Profit & Loss group from a Balance Sheet one. Served
	// beside Nature rather than inferred from it: the two agree today for every
	// seeded group, and a client that derived one from the other would be encoding
	// an assumption this schema does not enforce.
	IsPL bool `json:"is_pl"`
}

// handleListGroups serves GET .../groups.
//
// ORDERED BY NAME, flat. NOT a tree: the parent is named on every row, so a
// caller that wants a hierarchy can build one — and a caller that only needs a
// picker (which is why this route exists) is not made to walk one first. That is
// the same call the cost-centre list makes, and for the same reason.
func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}

	out := []groupListItem{}
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			// The self-join is LEFT: a primary group has no parent and must still be
			// listed. An inner join here would silently drop every top-level group —
			// which is most of the seeded chart — and a picker missing "Indirect
			// Expenses" reads as a product that cannot budget one.
			`SELECT g.id, g.name, COALESCE(p.name, ''), g.nature::text, g.is_pl
			   FROM account_group g
			   LEFT JOIN account_group p ON p.id = g.parent_id
			  ORDER BY g.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				it groupListItem
				id uuid.UUID
			)
			if err := rows.Scan(&id, &it.Name, &it.ParentName, &it.Nature, &it.IsPL); err != nil {
				return err
			}
			it.ID = id.String()
			out = append(out, it)
		}
		return rows.Err()
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}
