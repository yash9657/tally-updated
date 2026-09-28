package actions

import "github.com/khata/cored/internal/authz"

// Risk is the class of consequence an action has, which is what the W2 policy
// layer (internal/policy) keys autonomy and the structural rules on.
//
// ⛔ IT IS A FUNCTION OF THE ACL KEY, NOT OF THE ROUTE, AND THAT IS THE DESIGN.
// The PDP governs (action, resource); an operator writes rules in those terms.
// A risk class derived from anything finer — a path segment, a handler name —
// would let two routes the PDP treats as one door carry two different autonomy
// levels, and the tenant's own `disallow create payment` would then govern a
// different set of routes than the tenant's own autonomy row for `create
// payment`. One key, one class.
//
// ⛔ AND IT IS COMPUTED HERE, BY ONE FUNCTION, CALLED BY THE GENERATOR. The
// generated registry records the class on every row so a reviewer reads it in
// the diff, and registry_test.go re-derives every row from ClassifyRisk so the
// checked-in column cannot drift from the rule. internal/policy reads the
// row's field, never re-derives, so there is exactly one opinion about what
// "money" means.
type Risk string

const (
	// RiskRead: a display. Reads the books, alters nothing.
	RiskRead Risk = "read"
	// RiskExport: a print. authz treats print as its own authority precisely
	// because a PDF leaves the building (auth_middleware.go on service
	// principals), so it is not folded into read.
	RiskExport Risk = "export"
	// RiskMoney: creates or alters a voucher that posts to ledger balances, or
	// a write whose effect is a payment obligation settled against the books
	// (a pay run, a TDS challan). ⛔ THIS IS THE CLASS THE CaMeL RULE GUARDS:
	// a value that reached one of these from untrusted text can never by
	// itself authorise the commit (PLAN-AGENTIC §2.4).
	RiskMoney Risk = "money"
	// RiskOperational: a transactional document that does not post to a
	// ledger balance — orders, the non-accounting stock vouchers, bank
	// statement lines, reconciliation state.
	RiskOperational Risk = "operational"
	// RiskMaster: master data — ledgers and parties, items, cost centres,
	// budgets, BOMs, price lists, employees, statutory profiles.
	RiskMaster Risk = "master"
	// RiskStatutory: an act toward a government system or a tax position —
	// an IRN, an e-way bill, a GST filing record, an IMS accept/reject.
	RiskStatutory Risk = "statutory"
	// RiskDestructive: every delete. ⛔ STRUCTURALLY FORBIDDEN TO AN AGENT
	// (policy.structuralDeny): "no delete actions" is a non-negotiable, not an
	// autonomy level a tenant can raise.
	RiskDestructive Risk = "destructive"
	// RiskGovernance: who may do what, and what the books are — security
	// levels and members (the ACL), company settings and the year-end lock (a
	// period lock), backup, bulk import/sync, and the agent control plane
	// itself (runs, approvals, delegation). ⛔ STRUCTURALLY FORBIDDEN TO AN
	// AGENT, for the same reason: an agent that could change the ACL, a period
	// lock or its own delegation could grant itself anything.
	RiskGovernance Risk = "governance"
	// RiskUnclassified is what ClassifyRisk returns for a write resource it
	// has never been told about. ⛔ registry_test.go FAILS ON IT, so a new
	// resource cannot reach the registry without somebody deciding its class,
	// and internal/policy treats it as a structural deny in case one ever did.
	RiskUnclassified Risk = "unclassified"
)

// resourceRisk is the write-side class of every resource the PDP emits.
//
// ⚠️ A NEW RESOURCE TOKEN IN authz's VOCABULARY IS NOT CLASSIFIED UNTIL IT IS
// ADDED HERE, and that is deliberate: the default is RiskUnclassified, which
// fails registry_test.go, rather than a guess that would quietly give a new
// door the autonomy of whatever it was guessed to resemble.
var resourceRisk = map[authz.Resource]Risk{
	// Vouchers that post to ledger balances.
	"sales": RiskMoney, "purchase": RiskMoney, "receipt": RiskMoney, "payment": RiskMoney,
	"contra": RiskMoney, "journal": RiskMoney, "credit-note": RiskMoney, "debit-note": RiskMoney,
	"payroll": RiskMoney,
	// A TDS challan is a deposit of tax already deducted: a payment to the
	// government recorded against the books.
	"tds-challan": RiskMoney,
	// `alter voucher` is the class-agnostic edit of an existing voucher's
	// allocations (bill-wise / cost-centre), which moves what a posting settles.
	"voucher": RiskMoney,

	// Non-posting transactional documents.
	"delivery-note": RiskOperational, "receipt-note": RiskOperational,
	"stock-journal": RiskOperational, "physical-stock": RiskOperational,
	"sales-order": RiskOperational, "purchase-order": RiskOperational,
	"bank-statement": RiskOperational, "bank-reconciliation": RiskOperational,
	"reconciliation": RiskOperational,

	// Masters.
	"master": RiskMaster, "employee": RiskMaster, "cost-centre": RiskMaster, "budget": RiskMaster,
	"bom": RiskMaster, "price-list": RiskMaster, "tds-profile": RiskMaster,
	"deductor-status": RiskMaster, "tcs-declaration": RiskMaster, "forex-settings": RiskMaster,

	// Acts toward a government system or a tax position.
	"e-invoice": RiskStatutory, "e-way-bill": RiskStatutory, "gst-filing": RiskStatutory,
	"ims-action": RiskStatutory, "inward-statement": RiskStatutory,

	// Governance.
	"company": RiskGovernance, "security": RiskGovernance, "import": RiskGovernance,
	"sync": RiskGovernance, "backup": RiskGovernance,
	"agent-run": RiskGovernance, "agent-approval": RiskGovernance,
	// W2 breadth: the autonomy table and the release of an agent-created
	// ledger. Each is a way for an agent to widen what it may do next — a
	// higher level, or a party it made becoming payable — so each is the
	// human's alone.
	"agent-autonomy": RiskGovernance, "master-unblock": RiskGovernance,
	// W4: a verdict, a suppression or a digest trains and steers what the
	// owner is shown; an agent doing either would be grading its own ranker.
	"insights": RiskGovernance,
}

// governanceReads are DISPLAY actions that are nevertheless governance: a
// read an agent must never make on anyone's behalf.
//
// ⛔ `display backup` IS THE WHOLE BOOK IN ONE FILE — every ledger, every
// party's PAN, every payslip (auth_middleware.go's T14 arm). An agent that can
// read it can exfiltrate the tenant through a single tool call, so it is not a
// read in the sense the autonomy table means.
var governanceReads = map[authz.Resource]bool{
	"backup": true,
	// The agent control plane: an agent reading the approval inbox is an
	// agent that can see what it is waiting on and phrase its next step at
	// the approver. Reads of runs and approvals are for humans.
	"agent-run": true, "agent-approval": true,
	// The autonomy table: an agent that can read which of its commits need no
	// human can aim at exactly those.
	"agent-autonomy": true,
	// W4: the insight inbox and digest. The digest is model-written text and
	// the inbox is the owner's whole picture of the business; the agent's
	// insight TOOLS are W2-breadth's to add, with a provenance class of their
	// own (d125 OWED), not a read inherited from a route.
	"insights": true,
	// The ACL itself.
	"security": true,
}

// ClassifyRisk is the one rule. Callers: the registry generator
// (internal/server's TestActionRegistryGolden) and registry_test.go's re-check.
func ClassifyRisk(act authz.Action, res authz.Resource) Risk {
	switch act {
	case authz.Delete:
		return RiskDestructive
	case authz.Print:
		return RiskExport
	case authz.Display:
		if governanceReads[res] {
			return RiskGovernance
		}
		return RiskRead
	case authz.Create, authz.Alter:
		if r, ok := resourceRisk[res]; ok {
			return r
		}
		return RiskUnclassified
	}
	return RiskUnclassified
}

// MoneyMoving reports whether committing this action moves money in the
// books. It is the predicate the CaMeL provenance rule gates on.
func (s ActionSpec) MoneyMoving() bool { return s.Risk == RiskMoney }
