package ledger

// payroll_class.go — WHY A PAY RUN IS ITS OWN VOUCHER CLASS AND NOT A JOURNAL.
//
// This file adds no code. It exists because the `Payroll` constant in
// ledger.go and the `Payroll: true` entry in nonaccounting.go's
// accountingClasses look like two trivial lines, and the reasoning behind them
// is the only thing standing between this product and a fleet-wide privilege
// grant that nobody decided to make. The next person who reads
// `Payroll Class = "Payroll"` and thinks "this is a Journal with a different
// name, why the ceremony?" should find the answer here rather than infer one.
//
// ===========================================================================
// 1. POSTING A PAY RUN AS A JOURNAL WOULD HAND EVERY OPERATOR IN THE FLEET
//    THE RIGHT TO CREATE AND ALTER SALARY VOUCHERS, RETROACTIVELY
// ===========================================================================
// Migration 0011's seed_company_auth_defaults gives the seeded `Data Entry`
// level, in EVERY company that has ever been created, this:
//
//	FOREACH cls IN ARRAY ARRAY['sales','purchase','receipt','payment','contra','journal']
//	    allow create  cls
//	    allow alter   cls
//	    allow display cls
//
// `journal` is in that array. The PDP (internal/authz, Evaluate) resolves an
// exact (action, resource) rule at specificity 2, so `allow create journal`
// wins outright for a request whose resource token is `journal`.
//
// So if a pay run posted with Class = Journal, then on the day the payroll
// route mounts, every operator in every tenant would acquire the right to
// CREATE and ALTER salary vouchers — the ones that carry every employee's net
// pay, the TDS deducted from it and the PF and ESI liabilities. No migration
// would run. No rule would change. Nobody would decide it. The grant would be
// retroactive to companies created years ago, and there would be nothing in any
// diff to review, because the widening is in a list somebody else wrote in 0011
// and the change that triggers it is a route registration.
//
// That is the exact failure mode 0031's header calls out for stock vouchers and
// the exact reason 0029 seeded voucher types but NO acl_rule rows: a class must
// not acquire authority the moment a route appears.
//
// ===========================================================================
// 2. A voucher_type NAMED "Payroll" PARENTED TO JOURNAL DOES NOT FIX IT
// ===========================================================================
// The obvious cheaper move — keep Class = Journal, seed a voucher_type row
// named 'Payroll' with parent_class 'JOURNAL', and let the ACL hang off the
// type — does not work, and it fails silently rather than loudly.
//
// internal/server's routeAction derives the ACL resource token from the CLASS
// (the literal path segment naming the voucher class), never from the
// voucher_type row. A voucher_type is a NUMBERING and PRESENTATION concept: it
// decides which series the number comes from and what the screen is called. It
// is not an authorization boundary and nothing in the PDP can see it. Parenting
// a type to JOURNAL therefore leaves the token as `journal`, which is precisely
// the token 0011 already granted.
//
// Only a distinct CLASS separates the authority, because the class is the thing
// the router turns into a resource token and the thing migration 0035 writes an
// explicit `disallow create payroll` against.
//
// A second, smaller benefit falls out of the same decision: voucher_number_counter
// is keyed (company_id, voucher_type_id, fin_year), so a distinct class with its
// own system voucher_type gets its OWN number series. Salary vouchers do not
// interleave with JV numbers, and an accountant reading a Journal register does
// not find every month's payroll sitting in the middle of it.
//
// The whole cost is one const, one accountingClasses entry, one voucher_type
// seed and one ACL migration. The alternative is a silent fleet-wide grant.
//
// ===========================================================================
// 3. IT IS AN ACCOUNTING CLASS, DELIBERATELY — AND THIS ONE IS LOAD-BEARING
// ===========================================================================
// `Payroll: true` goes into accountingClasses, NOT into stockOnlyClasses, and
// not into some third "posts nothing" bucket.
//
// A pay run is the largest single debit most small companies raise in a month.
// If the class were classified as non-accounting, post() would take the
// !postsDoubleEntry branch, validateStockVoucher would REFUSE the postings
// outright (it refuses postings, bill allocations, GST and TDS on principle —
// see nonaccounting.go), and a caller that dropped them to satisfy it would
// commit a voucher with ZERO posting rows.
//
// And nothing in the database would object. posting_balanced (migration 0002)
// is a CONSTRAINT TRIGGER on the posting TABLE. A voucher that inserts no
// posting rows never fires it. The pay run would ship salaries, TDS, PF and ESI
// liabilities with no double-entry guard anywhere, the trial balance would still
// balance — it would simply be missing the payroll — and there is no report in
// this product that shows you a voucher that did not happen.
//
// So: Payroll states balanced double-entry postings, validatePostings runs, and
// the deferred trigger backs it up at commit. TestPayRunPostsThroughTheLedgerAndBalances
// (internal/payroll/payrolldb) asserts on the POSTED ROWS rather than on
// PostVoucher returning nil, for exactly this reason: "post returned nil" is
// also what a voucher with no postings returns.
//
// ===========================================================================
// 4. WHAT THE LATER WIRING WAVE STILL HAS TO ADD (three touches, mechanical)
// ===========================================================================
// This file deliberately stops at the ledger. The class is postable today and
// unreachable over HTTP today, which is the fail-closed order 0029/0031
// established. When the wiring wave mounts the route it must add, in one change:
//
//	1. internal/authz/vocabulary.go — `"payroll"` to KnownVoucherClasses and
//	   `"employee"` to the master/resource vocabulary, plus the five report
//	   names (payroll-register, payroll-payslip, payroll-form-138,
//	   payroll-pf-ecr, payroll-esi-return) to KnownReportNames.
//	2. internal/server/server.go — the routes, and the class list in
//	   auth_middleware.go's routeAction that must stay in step with (1).
//	3. internal/server/auth_middleware.go — reportName() entries for the five
//	   report names.
//
// Migration 0035 has ALREADY written the explicit `disallow` rows for all of
// them, so the day the route mounts the Data Entry level is refused rather than
// admitted by inheritance. That ordering is the point: the refusal lands BEFORE
// the surface it refuses.
