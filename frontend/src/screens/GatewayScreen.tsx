import { SelectionList } from "./SelectionList";

/** Top-level Gateway of Tally menu (RESEARCH.md sec 4). */
const GATEWAY_MENU = [
  "Vouchers",
  "Bill Inbox",
  "Query",
  "Day Book",
  "Balance Sheet",
  "Profit & Loss A/c",
  "Stock Summary",
  // Bank Reconciliation (T4). TallyPrime reaches it through Alt+G > Banking
  // Activities (RESEARCH.md:873); a Gateway entry is the same door. It was built
  // server-side with no key anywhere in this app, which the handoff calls a
  // parity gap rather than a nicety.
  "Bank Reconciliation",

  // TDS Challans (T6) — the deposits made to the Government, and the only place
  // one can be recorded. Built server-side with no key anywhere in this app; a
  // deduction with no challan behind it cannot be filed at all, so the register
  // needs a door rather than a route only cURL can reach.
  "TDS Challans",

  // Import from Tally (T32) — the migration importer. TallyPrime's own path is
  // Gateway of Tally > Import (RESEARCH.md:1221), so this is the documented
  // door and Alt+O is the documented key; the screen has both because Tally has
  // both. It was built server-side with NO surface at all, so migrating a
  // customer's books meant hand-rolling a curl.
  //
  // ⚠️ It is NOT in Alt+G Go To and cannot be: that picker is generated from
  // ["Vouchers", ...REPORT_NAMES] and this is not a report name.
  "Import from Tally",

  // Print Templates (track C) — what this company's own documents say. The
  // override store has been implemented AND TESTED since migration 0037 and the
  // printer has honoured it the whole time; NO ROUTE COULD WRITE ONE, and
  // print_template held zero rows in the entire database. So the Gateway is this
  // screen's door for the same reason it is Bank Reconciliation's and TDS
  // Challans': built server-side with no surface anywhere.
  //
  // ⚠️ It is NOT in Alt+G Go To and cannot be: that picker is generated from
  // ["Vouchers", ...REPORT_NAMES] and this is not a report name.
  //
  // ⚠️ AND IT HAS NO GLOBAL KEY, on purpose. Tally reaches print configuration
  // through F12 Configure and the print preview rather than a documented Alt
  // binding, so there is no key to be faithful to and inventing one risks
  // shadowing a binding a screen already owns.
  "Print Templates",

  // Reconciliation (track D) — every alter or delete these books refused.
  //
  // ⛔ IT IS A DOOR BECAUSE THE ROWS BEHIND IT HAVE NEVER BEEN VISIBLE. The
  // ledger's freeze rule has raised queue rows since migration 0009 and the route
  // has served them the whole time; nothing in this app called it, so an operator
  // whose edit was refused got the explanation written to a table only Postgres
  // could read. Alt+R reaches it too, but a keyboard operator who does not know
  // the key needs it on the Gateway.
  //
  // ⚠️ THE LABEL IS MATCHED IN navMachine BEFORE the canonicalReportName
  // fallback — see RECONCILIATION_LABEL, and note the trap is sharper here than
  // for the other two because a bank-reconciliation REPORT really does exist.
  //
  // ⛔ IT SAYS "QUEUE" BECAUSE THE BARE WORD COLLIDED WITH "Bank Reconciliation"
  // ABOVE. The Gateway is driven by typing into its search box; the short name
  // opened the BANK screen instead, silently, which reads as an empty queue
  // rather than as a wrong turn. Found in a real browser, not in a test.
  "Reconciliation Queue",

  // Offline Outbox (track D) — the vouchers this machine is holding because the
  // server could not be reached to post them.
  //
  // ⛔ IT IS A DOOR BECAUSE THE ALTERNATIVE IS AN OPERATOR WHO CANNOT FIND THEIR
  // OWN UN-POSTED WORK. Alt+Q reaches it and the accept path's own status line
  // names that key, but a keyboard operator who did not read the status line — or
  // who reloaded, or came back the next morning — has no other way in, and what
  // they are looking for is invoices they believe they have already keyed.
  //
  // ⚠️ It is NOT in Alt+G Go To and cannot be: that picker is generated from
  // ["Vouchers", ...REPORT_NAMES] and this is not a report name. It could not be
  // one — there is no route that serves this list; the rows are in this browser.
  //
  // ⚠️ THE LABEL WAS CHECKED AGAINST THE COLLISION THAT COST "Reconciliation"
  // TWO BROWSER RUNS: the Gateway is driven by typing into its search box, so a
  // label that is a SUBSTRING of a shipped one silently opens the shipped one.
  // "Offline Outbox" is a substring of nothing above and contains nothing above
  // — and note "Bill Inbox" is the nearest neighbour and is not a substring of
  // it. See OFFLINE_OUTBOX_LABEL in navMachine.ts.
  "Offline Outbox",

  // ---- THE THREE COST SCREENS (T2).
  //
  // "Cost Centres" is the MASTERS door — the axes of analysis (Department,
  // Branch, Project) and the positions on them. It comes FIRST of the three on
  // purpose: until an axis and a centre exist, nothing can be allocated and both
  // reports below can only say so.
  //
  // Tally files the two reports under Display More Reports > Statements of
  // Accounts > Cost Centres (RESEARCH.md:810) and creates the masters through
  // Alt+G > Create Master > Cost Centre (RESEARCH.md:525). They are Gateway
  // entries here because this build's second-level menu is generated from
  // REPORT_NAMES, and none of these three is a reportName frame — see
  // COST_CENTRES_LABEL in navMachine.ts.
  "Cost Centres",
  "Cost Centre Summary",
  "Cost Category Summary",

  // ---- THE TWO T11 BUDGET SCREENS.
  //
  // "Budgets" is the MASTER door and comes first for the same reason "Cost
  // Centres" does: until a plan with lines exists, the variance report has nothing
  // in its second column and can only say so.
  //
  // ⛔ "Budget Variance" IS A GATEWAY ENTRY AND NOT A REPORT_NAMES ENTRY, even
  // though the server serves it as `report:budget-variance`. This build's
  // second-level menu is generated from REPORT_NAMES and pushes a `report` frame,
  // which renders "not available in this build" — over a report that is built and
  // ACL-gated. It also takes a plan, a grain and (at the cost-centre grain) an
  // axis, none of which a reportName frame can carry. See BUDGET_VARIANCE_LABEL in
  // navMachine.ts.
  "Budgets",
  "Budget Variance",

  // ---- THE T12 ANNUAL RECONCILIATION.
  //
  // ⛔ A GATEWAY ENTRY AND NOT A REPORT_NAMES ENTRY, for the reason the budget
  // variance above records — and the temptation is stronger here, because
  // `report:gstr1` and `report:gstr3b` ARE already report names, so putting their
  // annual sibling beside them looks obviously right. It would render "not
  // available in this build" over a report that is built and ACL-gated, and a
  // reportName frame could carry neither the FINANCIAL YEAR nor the six TAX
  // LEDGERS this screen needs. See GSTR9_LABEL in navMachine.ts.
  //
  // ⚠️ AND NOTE WHAT THIS ENTRY DOES NOT DO: gstr1 and gstr3b still have no screen
  // at all and are still not reachable from here. Adding one report's door does not
  // add theirs.
  "GSTR-9",

  // ---- THE THREE T3 SCREENS (order processing and bills of materials).
  //
  // The two REGISTERS also have documented keys — Ctrl+F8 and Ctrl+F9
  // (RESEARCH.md:400, :614, :616) — so the Gateway is their second door. The BOM
  // screen's only door is here: RESEARCH pins no key for reaching a bill of
  // materials, because in Tally it is not a screen at all but a page of the stock
  // item's own master (RESEARCH.md:626), and inventing a key would be inventing a
  // keystroke.
  //
  // ⚠️ THESE ARE THE REGISTERS AND NOT THE BOOKS. "Sales Orders" is where an order
  // is created; "Sales Order Book" is what is still outstanding as at a date, and
  // it is a REPORT name reached through Alt+G or Display More Reports. Both exist,
  // they are different screens, and the pair is easy to conflate — which is why
  // Tally's own name for the second one ("Sales Order Outstandings",
  // RESEARCH.md:622) is worth remembering as what the book actually reports.
  "Sales Orders",
  "Purchase Orders",
  "Bills of Materials",

  // ---- THE FOUR PAYROLL ENTRIES (Track 3).
  //
  // "Employees" is FIRST of the four, and that ordering is the same argument
  // "Cost Centres" comes before the two cost reports: until an employee exists
  // WITH a salary structure and a statutory election, a pay run refuses outright
  // ("no salary structure is in force", "no statutory election is in force") and
  // both reports below can only say there is nothing to show.
  //
  // Tally files these under Gateway > Payroll Info / Payroll Vouchers / Payroll
  // Reports. This build has no second-level payroll menu, and a Gateway entry is
  // the same door — the door Bank Reconciliation, the TDS challan register and
  // the three cost screens already use. The pay run additionally has its
  // documented key, Ctrl+F4 (RESEARCH.md:903), and both land on ONE frame.
  //
  // ⚠️ "Payroll Register" IS NOT IN "Display More Reports" even though
  // `payroll-register` is a real report name on the server. That menu is
  // generated from REPORT_NAMES, and the register is not reachable as a
  // reportName frame: its dimension is a WAGE MONTH, not the From/To period a
  // report frame carries. See PAYROLL_REGISTER_LABEL in navMachine.ts for the
  // trap that makes this load-bearing rather than tidy.
  "Employees",
  "Pay Run",
  "Payroll Register",
  "Form 138 (Salary TDS)",

  "Users & Roles",
  "Display More Reports",
] as const;

/**
 * The Gateway of Tally screen. Selecting "Vouchers" and pressing Enter drills
 * into the voucher form; F4–F9 jump straight to a voucher type (handled by the
 * navigation machine at the shell level).
 */
export function GatewayScreen({ onSelect }: { onSelect: (item: string) => void }) {
  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 text-tally-field">
      <h1 className="mb-4 text-center font-mono text-lg font-bold text-tally-title">
        Gateway of Tally
      </h1>
      <div className="mx-auto w-72 bg-tally-panel p-3">
        <SelectionList
          label="MASTERS / TRANSACTIONS / REPORTS"
          options={GATEWAY_MENU}
          onSelect={onSelect}
          autoFocus
        />
      </div>
    </section>
  );
}
