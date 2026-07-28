package authz

import (
	"fmt"
	"strings"
)

// vocabulary.go pins the CLOSED sets of actions and resources an ACL rule may
// name, and the validation the admin API applies before writing a rule.
//
// WHY THIS IS NOT COSMETIC: a rule whose resource is misspelled ("sale" instead
// of "sales", "trial_balance" instead of "trial-balance") is not rejected by the
// PDP — it simply never matches, so the level silently falls back to
// default_access. For a `disallow` rule that is a silent-wrong-answer bug of the
// worst kind: the administrator believes they forbade something and the system
// permits it. Validating against the same vocabulary the router can emit turns
// that into a 422 at write time.
//
// The router's routeAction() must only ever emit pairs drawn from these sets;
// TestRouteActionVocabularyIsClosed (internal/server) asserts that structurally,
// so adding a route with a new resource fails a test rather than creating a rule
// nobody can write.

// KnownActions is every action token an acl_rule may carry (plus Wildcard).
var KnownActions = []Action{Create, Alter, Delete, Display, Print, Export}

// KnownVoucherClasses are the resource tokens naming an accounting voucher class.
var KnownVoucherClasses = []Resource{
	"sales", "purchase", "receipt", "payment", "contra", "journal",
	"credit-note", "debit-note",
}

// KnownResources is every non-report resource token an acl_rule may carry (plus
// Wildcard and the report:NAME form handled separately).
var KnownResources = append([]Resource{
	"master",   // ledgers/groups/units/stock items
	"voucher",  // an individual voucher (get/delete are class-agnostic)
	"report",   // any report; report:NAME narrows it
	"company",  // the company master
	"import",   // Tally XML import
	"sync",     // offline sync commit
	"query",    // conversational query
	"security", // users, memberships and security levels (this admin surface)
}, KnownVoucherClasses...)

// KnownReportNames is every report a rule may name via the "report:NAME" form.
// It must stay in step with routeAction()'s reportName() — the vocabulary test
// in internal/server enforces that.
var KnownReportNames = []string{
	"trial-balance", "balance-sheet", "profit-and-loss", "day-book",
	"outstandings", "cash-flow", "anomalies", "gstr1", "gstr3b",
	"reconciliation-queue",
	// The Stock Summary is a REPORT, not a master list: its numbers are
	// DERIVED by replaying the movement ledger through a valuer, and nothing
	// it prints is stored anywhere. Naming it here is what lets an
	// administrator write `disallow display report:stock-summary` for a level
	// that may key invoices but must not see what the goods COST — the closing
	// rate on this report is the company's own cost price, which is
	// commercially sensitive in a way the chart of accounts is not.
	"stock-summary",
	// The four inventory reports the SAME replay produces at other grains
	// (waves 1 and 2). Each is named INDIVIDUALLY rather than folded into
	// "stock-summary", because a name is the only thing an administrator can
	// write a rule against: one shared name would make "an operator may see
	// what is expiring but not what it cost" impossible to express, and
	// migration 0028 has to be able to disallow each of them separately.
	//
	// EVERY ONE OF THEM CARRIES VALUE — a per-godown closing valuation, a
	// per-batch one, cost consumed over a period, and (on the item drill) the
	// rate each PURCHASE was made at, which is the cost price stated outright
	// rather than averaged into a closing rate. So they carry the Stock
	// Summary's exposure exactly, and migration 0028 seeds the matching
	// disallows on the Data Entry level.
	"stock-movement", "godown-summary", "batch-summary", "stock-item-vouchers",
}

// ValidAction reports whether a is a legal rule action (Wildcard included).
func ValidAction(a Action) bool {
	if string(a) == Wildcard {
		return true
	}
	for _, k := range KnownActions {
		if a == k {
			return true
		}
	}
	return false
}

// ValidResource reports whether res is a legal rule resource: the wildcard, a
// known bare resource, or "report:NAME" for a known report name.
func ValidResource(res Resource) bool {
	rs := string(res)
	if rs == Wildcard {
		return true
	}
	if base, name, found := strings.Cut(rs, ":"); found {
		if base != "report" {
			return false
		}
		for _, n := range KnownReportNames {
			if n == name {
				return true
			}
		}
		return false
	}
	for _, k := range KnownResources {
		if res == k {
			return true
		}
	}
	return false
}

// ValidEffect reports whether e is allow or disallow.
func ValidEffect(e Effect) bool { return e == Allow || e == Disallow }

// ValidateRule checks one rule against the closed vocabulary, returning a
// human-readable error naming the offending token (the admin API surfaces it
// verbatim as a 422, so an operator can see WHICH token was rejected).
func ValidateRule(r Rule) error {
	if !ValidEffect(r.Effect) {
		return fmt.Errorf("invalid effect %q: want allow or disallow", r.Effect)
	}
	if !ValidAction(r.Action) {
		return fmt.Errorf("unknown action %q", r.Action)
	}
	if !ValidResource(r.Resource) {
		return fmt.Errorf("unknown resource %q", r.Resource)
	}
	return nil
}

// KnownDefaultAccesses is every default_access value a security level may carry.
// The DB CHECK constraint on security_level.default_access pins the same pair;
// both the validator below and the served vocabulary read it, so the two cannot
// drift apart.
var KnownDefaultAccesses = []string{"full", "none"}

// ValidDefaultAccess reports whether da is one of the default_access values the
// DB CHECK constraint permits.
func ValidDefaultAccess(da string) bool {
	for _, k := range KnownDefaultAccesses {
		if da == k {
			return true
		}
	}
	return false
}
