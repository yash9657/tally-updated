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

// KnownVoucherClasses are the resource tokens naming a voucher class — the
// kebab-case form of ledger.Class, which is also the path segment routeAction
// derives the token from (POST /vouchers/delivery-note -> "delivery-note").
//
// THE LIST IS NOT "THE ACCOUNTING CLASSES". The four stock vouchers below post
// ZERO accounting lines (internal/ledger/nonaccounting.go), and that is exactly
// why they need their own tokens rather than borrowing an existing one: moving
// goods out of a warehouse without an invoice is a distinct authority from
// raising the invoice, and an administrator who cannot NAME it cannot grant or
// withhold it. Migration 0031 uses these four names.
var KnownVoucherClasses = []Resource{
	"sales", "purchase", "receipt", "payment", "contra", "journal",
	"credit-note", "debit-note",
	// The non-accounting stock vouchers (INV-K). Named individually for the
	// same reason each report is: one shared token would make "an operator may
	// receive goods but may not restate the count" impossible to express, and
	// that is precisely the distinction migration 0031 draws.
	"delivery-note", "receipt-note", "stock-journal", "physical-stock",
	// The PAY RUN (migration 0034/0035, internal/ledger/payroll_class.go). It
	// rides /vouchers/payroll exactly as an invoice rides /vouchers/sales, and
	// it posts real balanced double entry — so it is a voucher class and not
	// something else.
	//
	// WHY IT IS NOT FOLDED INTO "journal". A pay run's postings ARE the wage
	// bill: gross to Salaries & Wages, the PF/ESI/TDS liabilities, the net to
	// each payable. Borrowing the `journal` token would mean the one rule a
	// supervisor writes to let an operator key adjustment journals ALSO lets
	// them create, alter and read what the company pays its staff — and there
	// is no `journal:NAME` narrowing form with which to take it back. Its own
	// token is what makes migration 0035's `disallow create|alter|display
	// payroll` a rule anybody can write at all.
	"payroll",
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
	// Price lists (INV-L, migration 0030): named rates for stock items over a
	// date window, optionally narrowed to a party group.
	//
	// WHY IT IS NOT FOLDED INTO "master". Every other master shares the one
	// `master` token so that one rule governs the chart of accounts, the
	// godowns and the items alike — and that is right for them, because the
	// question "may this level define masters?" has one answer. A price list
	// asks a SECOND question the `master` token cannot express: may this level
	// SEE what we charge, and in particular what we charge THIS customer that
	// we do not charge that one. There is no `master:NAME` narrowing form (only
	// `report:NAME` exists), so folding price lists into `master` would make
	// "an operator may pick a godown but must not read the rate card" an
	// unwritable rule. A separate token costs one line here and buys the
	// administrator a lever; see migration 0031 for the judgement it enables.
	"price-list",

	// The EMPLOYEE MASTER (migrations 0034/0035).
	//
	// WHY IT IS NOT `master`, WHICH IS THE MISTAKE THIS LINE EXISTS TO PREVENT.
	// Migration 0022 grants the seeded Data Entry level `allow display master`
	// so the F9 party picker works at all. Folding employees into that token
	// would hand every operator in every tenant the PAN, UAN, ESIC IP number,
	// bank IFSC and account last-4 of every person in the company — by
	// inheritance, without anybody deciding to, and with no `master:NAME` form
	// in which to narrow it back afterwards. Unlike a cost price, a leak here is
	// somebody's identity rather than somebody's margin. Its own token is what
	// makes migration 0035's `disallow create|alter|display employee` a rule an
	// administrator can write at all.
	"employee",

	// The three ITC / IMS WRITES (migrations 0032/0033, handler_itc.go).
	//
	// All three are default-DENIED for the seeded Data Entry level already —
	// that level's default_access is 'none' and no rule names them — which is
	// exactly why migration 0033 seeds nothing for them and needs no successor.
	// They are named HERE because routeAction must emit them, and a token the
	// router emits but the vocabulary rejects is a route no ACL rule can ever
	// govern: only default_access could reach it, so an owner could never
	// delegate it and never take it back.
	//
	// THEY ARE THREE TOKENS AND NOT ONE, because they are three different
	// authorities and a single `itc` token would make each separation
	// unwritable:
	//
	//	inward-statement — uploading the monthly GSTR-2A/2B file. Clerical, and
	//	  the one of the three an owner might plausibly delegate: it asserts
	//	  nothing, it only makes the portal's own document available to match
	//	  against. Named for gst_inward_statement, the row it creates.
	//	ims-action — ACCEPT / REJECT / PENDING on a supplier document. This is
	//	  what decides whether real input tax credit is claimed: rejecting a
	//	  genuine invoice forfeits money, and accepting one that should not be
	//	  accepted claims credit that is not ours, which is the direction that
	//	  draws a notice and interest. Whoever may upload the file must not get
	//	  this by the same rule.
	//	gst-filing — recording that the return was FILED, which stamps the IMS
	//	  freeze for the period (GSTN IMS FAQ 12): after it, no IMS action counts
	//	  at all. It is the irreversible one, and it belongs to whoever signs the
	//	  return.
	"inward-statement", "ims-action", "gst-filing",

	// BANK RECONCILIATION (T4) — the WRITE of a bank date onto one voucher leg.
	//
	// WHY IT IS ITS OWN TOKEN AND NOT `voucher`. Ticking an item off against a
	// bank statement changes NO balance and alters NO voucher: it records that
	// the bank cleared something, alongside the books rather than inside them
	// (posting is hard-immutable; the reconciliation is a separate row). Folding
	// it into `voucher` would tie "may reconcile the bank" to "may alter a
	// voucher", and those are opposite risk profiles — the clerk who ticks off
	// cheques is precisely the person you do NOT want altering vouchers.
	//
	// AND WHY THE *READ* IS NOT THIS TOKEN. The statement itself is a report
	// (report:bank-reconciliation, below), because it is the company's entire
	// bank position — every un-cleared cheque and counterparty. One token for
	// both would force the clerical write and the sensitive read to be granted
	// together, and the only safe resolution of that is to refuse the clerk the
	// write, which deletes the feature. Migration 0046 depends on the split.
	"bank-reconciliation",

	// THE TDS DEDUCTOR STATUS (migration 0049, Item Zero step 2): whether this
	// company is a s.402(11) designated person and/or a s.402(37) specified
	// person, per tax year. Tally keeps it in F11 > Enable TDS > TDS Deductor
	// Details (RESEARCH.md:692).
	//
	// WHY IT IS NOT `company`, WHICH IS THE FOLD A REVIEWER WOULD SUGGEST. The
	// `company` token governs the company master — its name, address, GSTIN,
	// home state. Those are facts an accountant corrects. THIS decides whether
	// tax is withheld out of every supplier payment for a year: flipping the
	// category from INDIVIDUAL to COMPANY starts deducting on payments that
	// attracted nothing, and flipping it the other way stops deductions the
	// company remains liable for, with interest under s.396. "May correct the
	// address, may not touch the deductor status" is a rule an owner should be
	// able to write, and one token for both makes it unwritable.
	//
	// AND NOT `master` either: migration 0022 grants the seeded Data Entry level
	// `allow display master` so the F9 party picker works, and there is no
	// `master:NAME` narrowing form to take it back afterwards — the same trap
	// the `employee` entry above exists to avoid.
	//
	// UNLIKE bank-reconciliation, THE READ AND THE WRITE SHARE THIS TOKEN. It is
	// one small object (a category and a turnover figure), not a position
	// statement, so there is no sensitive read to separate out — and one token
	// keeps one rule governing both, as it does for masters.
	//
	// NO ACL MIGRATION ACCOMPANIES IT, deliberately, and that is a decision
	// rather than an omission: the seeded Data Entry level has
	// default_access = 'none' and no coarse rule reaches this token (0011's
	// `allow display report` only reaches report:NAME; 0022's is `master`), so
	// Data Entry is already refused both halves and the Owner's 'full' already
	// allows them. That is the intended authority — recording the deductor
	// status is the act that decides a year of withholding, so it belongs to
	// whoever signs the return, not to whoever keys the bills.
	// TestDeductorStatusAuthMatrix pins all three answers.
	"deductor-status",

	// THE s.394(2) MANUFACTURING DECLARATION (migration 0049, Item Zero step 3).
	// A resident buyer's written statement that goods are for manufacturing and
	// not for trading, which exempts collection at source on Table Sl. Nos. 1-5.
	//
	// SEPARATE FROM `deductor-status`, though they share a screen. They are
	// opposite sides of the same statute: the deductor status is a
	// SELF-ASSESSMENT that decides a year of our own withholding; a declaration
	// is a document that ARRIVES from a customer and is transcribed, and it
	// zeroes tax on that customer's sales. An owner may reasonably delegate the
	// transcription and not the self-assessment, and one token would make that
	// unwritable.
	//
	// Both are default-DENIED for the seeded Data Entry level today (its
	// default_access is 'none' and no rule names either token), so no ACL
	// migration accompanies them. The separate token is what makes the
	// delegation POSSIBLE, not automatic.
	"tcs-declaration",
}, KnownVoucherClasses...)

// KnownReportNames is every report a rule may name via the "report:NAME" form.
// It must stay in step with routeAction()'s reportName() — the vocabulary test
// in internal/server enforces that.
var KnownReportNames = []string{
	"trial-balance", "balance-sheet", "profit-and-loss", "day-book",
	"outstandings", "cash-flow", "anomalies", "gstr1", "gstr3b",
	"reconciliation-queue",
	// The Bank Reconciliation Statement (T4). A report NAME so migration 0046
	// can write `disallow display report:bank-reconciliation` at specificity 2 —
	// without which 0011's coarse `allow display report` reaches it at
	// specificity 1 and every seeded Data Entry operator can read the company's
	// bank position.
	"bank-reconciliation",
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

	// ITC reconciliation and the IMS inbox (migration 0033, which seeds the
	// matching `disallow display` rows for the seeded Data Entry level).
	//
	// Each is named individually rather than sharing an "itc" report name for
	// the reason every entry above is: the two are different disclosures and an
	// owner should be able to hold one without the other.
	//
	//	itc-reconciliation — every line carries a NAMED SUPPLIER, their GSTIN,
	//	  the invoice number and date they issued and the taxable value we paid,
	//	  for every purchase in the period, with the arithmetic already done. It
	//	  is the whole supplier book and the purchase prices in one export —
	//	  strictly a sharper disclosure than the averaged cost the stock reports
	//	  above leak.
	//	ims-inbox — the filer's working paper: which supplier documents are
	//	  unmatched and what we did about each. Naming it separately is what lets
	//	  an owner grant the worklist to a compliance clerk while withholding the
	//	  priced reconciliation, or the reverse.
	"itc-reconciliation", "ims-inbox",

	// PAYROLL: the register, one payslip, and the three STATUTORY FILES
	// (migration 0035, which seeds `disallow display` for all five).
	//
	// These are the least generous defaults in the product and the entries here
	// are what make that default expressible. Each is named on its own because
	// the five are five different disclosures at five different grains, and one
	// shared name would make "the payroll clerk may produce the monthly ECR but
	// may not open anybody's payslip" an unwritable rule:
	//
	//	payroll-register    — every employee's gross, deductions and net.
	//	payroll-payslip     — ONE employee's, in full, including the components.
	//	payroll-form-138    — PAN + salary + TDS, per person, per quarter.
	//	payroll-pf-ecr      — UAN + wages, per person, per month.
	//	payroll-esi-return  — IP number + wages + last working day, per person.
	//
	// Four of the five are statutory OUTPUTS. Statutory output and payroll are
	// more sensitive than the cost book: salaries and PAN/UAN/IP numbers are not
	// "reports an operator may see by inheritance", which is precisely what
	// 0011's coarse `allow display report` would otherwise make them the day
	// these routes mount.
	"payroll-register", "payroll-payslip", "payroll-form-138",
	"payroll-pf-ecr", "payroll-esi-return",
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
