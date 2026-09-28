import { SelectionList } from "../screens/SelectionList";
import { GatewayScreen } from "../screens/GatewayScreen";
import { VoucherScreen } from "../screens/VoucherScreen";
import { BillInboxScreen } from "../screens/BillInboxScreen";
import { QueryScreen } from "../screens/QueryScreen";
import { CompanySelectScreen } from "../screens/CompanySelectScreen";
import { MembersScreen } from "../screens/MembersScreen";
import { EditLogScreen } from "../screens/EditLogScreen";
import { HelpScreen } from "../screens/HelpScreen";
import { DataScreen } from "../screens/DataScreen";
import { ReconciliationScreen } from "../screens/ReconciliationScreen";
import { TallyImportScreen } from "../screens/TallyImportScreen";
import { PrintTemplateScreen } from "../screens/PrintTemplateScreen";
import { FeaturesScreen } from "../screens/FeaturesScreen";
import { MasterCreateScreen } from "../screens/masters/MasterCreateScreen";
import {
  DayBookScreen,
  TrialBalanceScreen,
  BalanceSheetScreen,
  ProfitLossScreen,
  OutstandingsScreen,
  BillsPayableScreen,
  UnadjustedForexScreen,
} from "../screens/reports/ReportScreens";
import { ReportDetailScreen } from "../screens/reports/ReportDetailScreen";
import { StockSummaryScreen } from "../screens/reports/StockSummaryScreen";
import { TDSRegisterScreen } from "../screens/reports/TDSRegisterScreen";
import { BankReconciliationScreen } from "../screens/BankReconciliationScreen";
import { TDSChallansScreen } from "../screens/TDSChallansScreen";
import { EWayBillScreen } from "../screens/EWayBillScreen";
import { EInvoiceScreen } from "../screens/EInvoiceScreen";
import { BudgetsScreen } from "../screens/masters/BudgetsScreen";
import { BudgetVarianceScreen } from "../screens/reports/BudgetVarianceScreen";
import { GSTR9Screen } from "../screens/reports/GSTR9Screen";
import { CostCentresScreen } from "../screens/masters/CostCentresScreen";
import { CostCentreSummaryScreen } from "../screens/reports/CostCentreSummaryScreen";
import { CostCategorySummaryScreen } from "../screens/reports/CostCategorySummaryScreen";
import {
  SalesOrdersScreen,
  PurchaseOrdersScreen,
} from "../screens/OrderRegisterScreen";
import { BomsScreen } from "../screens/masters/BomsScreen";
import {
  SalesOrderBookScreen,
  PurchaseOrderBookScreen,
} from "../screens/reports/OrderBookScreens";
import {
  CashFlowScreen,
  AnomaliesScreen,
} from "../screens/reports/ForecastScreens";
import { EmployeesScreen } from "../screens/payroll/EmployeesScreen";
import { PayRunScreen } from "../screens/payroll/PayRunScreen";
import { PayrollRegisterScreen } from "../screens/payroll/PayrollRegisterScreen";
import { PayslipScreen } from "../screens/payroll/PayslipScreen";
import { Form138Screen } from "../screens/payroll/Form138Screen";
import { REPORT_NAMES } from "../keystroke/types";
import { GOTO_ENTRIES } from "../keystroke/navMachine";
import type { ScreenComponent, ScreenComponentProps } from "./screenRegistry";
import { OfflineOutboxScreen } from "../screens/OfflineOutboxScreen";
import { ApprovalInboxScreen } from "../screens/ApprovalInboxScreen";
import { AgentSettingsScreen } from "../screens/AgentSettingsScreen";
import { ScreenRegistry } from "./screenRegistry";

/**
 * Default, framework-owned screen components. These keep the shell renderable
 * before/without the voucher/report/master agents' richer components. Any agent
 * registers over these by calling `registry.register(...)` with the same key.
 *
 * The Gateway and Voucher screens reuse the existing Phase 0 components.
 */

/**
 * The Alt+G / Ctrl+G Go To picker.
 *
 * ⛔ ITS OPTIONS ARE `GOTO_ENTRIES` AND NOT A LIST WRITTEN HERE. This used to be
 * `["Vouchers", ...REPORT_NAMES]`, and that literal is the whole of row 20: with
 * no masters entry in it and none on the Gateway menu, NO keystroke in the
 * product reached `masterKind` "stock-item" or "unit", so a company could never
 * be given a stock item or a unit from the keyboard at all. The machine owns the
 * list because the machine owns what a selection DOES — see MASTER_GOTO_ENTRIES
 * in navMachine.ts, and `resolveGotoSelection`, which must branch on the same
 * labels this renders or the entry lands on "not available in this build".
 */
const GoToOverlayScreen: ScreenComponent = ({ snapshot, onSelect }) => {
  const verb = "Go To"; // gotoMode is internal to the machine; label stays "Go To"
  return (
    <section className="flex h-full items-start justify-center bg-tally-bg/80 p-8">
      <div className="w-80 bg-tally-panel p-3 font-mono text-tally-field">
        <h2 className="mb-2 text-tally-title">{verb}</h2>
        <SelectionList
          label="Search"
          options={GOTO_ENTRIES}
          onSelect={onSelect}
          autoFocus
        />
        {snapshot.selectedItem ? (
          <p className="mt-2 text-xs">Selected: {snapshot.selectedItem}</p>
        ) : null}
      </div>
    </section>
  );
};

/**
 * Fallback for a report name that has no wired data source. It says so plainly
 * — it must NEVER render placeholder rows, because fabricated numbers in an
 * accounting screen are worse than a missing screen.
 *
 * Stock Summary USED to land here; it now has a movement ledger behind it and
 * is registered by name below. What is left on this fallback is any report a
 * Go To / menu label can name that this build does not serve.
 */
const UnwiredReportScreen: ScreenComponent = ({ snapshot }) => {
  const name = snapshot.reportName ?? "Report";
  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">{name}</h1>
      <p className="mt-3 max-w-xl text-sm">
        {name} is not available in this build — there is no data plane behind it
        yet. Nothing is shown rather than sample rows, because a report that
        invents numbers is worse than no report.
      </p>
      <p className="mt-2 text-sm">
        Working reports: Balance Sheet, Profit &amp; Loss A/c, Trial Balance,
        Day Book, Outstandings, Stock Summary — all reachable with Alt+G. Esc
        goes back.
      </p>
    </section>
  );
};

/** Inline master-create adapter — the wired form lives in MasterCreateScreen. */
const MasterCreateAdapter: ScreenComponent = ({
  snapshot,
  onSelect,
}: ScreenComponentProps) => (
  <MasterCreateScreen snapshot={snapshot} onSelect={onSelect} />
);

/** Gateway adapter: the Phase 0 GatewayScreen only needs onSelect. */
const GatewayAdapter: ScreenComponent = ({
  onSelect,
}: ScreenComponentProps) => <GatewayScreen onSelect={onSelect} />;

/**
 * "Display More Reports" — Tally's second-level report menu, where the Trial
 * Balance and Outstandings live. Selecting an entry and pressing Enter opens it
 * through the same gateway/menu branch of the machine.
 */
const MoreReportsMenu: ScreenComponent = ({ snapshot, onSelect }) => (
  <section className="flex h-full flex-col bg-tally-bg p-4 text-tally-field">
    <h1 className="mb-4 text-center font-mono text-lg font-bold text-tally-title">
      Display More Reports
    </h1>
    <div className="mx-auto w-72 bg-tally-panel p-3">
      <SelectionList
        label="REPORTS"
        options={[...REPORT_NAMES]}
        onSelect={onSelect}
        autoFocus
      />
      {snapshot.selectedItem ? (
        <p className="mt-2 font-mono text-xs text-tally-field">
          Enter to open {snapshot.selectedItem}
        </p>
      ) : null}
    </div>
  </section>
);

/** Voucher adapter: the Phase 0 VoucherScreen needs the snapshot + onSelect. */
const VoucherAdapter: ScreenComponent = ({
  snapshot,
  onSelect,
}: ScreenComponentProps) => (
  <VoucherScreen snapshot={snapshot} onSelect={onSelect} />
);

/** Bill Inbox adapter: lists OCR drafts and opens one into the F9 voucher. */
const BillInboxAdapter: ScreenComponent = ({
  snapshot,
  onSelect,
}: ScreenComponentProps) => (
  <BillInboxScreen snapshot={snapshot} onSelect={onSelect} />
);

/** Query adapter: the "Ask your books" conversational query screen. */
const QueryAdapter: ScreenComponent = ({
  snapshot,
  onSelect,
}: ScreenComponentProps) => (
  <QueryScreen snapshot={snapshot} onSelect={onSelect} />
);

/** F3 Select Company adapter: the companies this USER may open (/v1/me). */
const CompanySelectAdapter: ScreenComponent = ({
  snapshot,
  onSelect,
}: ScreenComponentProps) => (
  <CompanySelectScreen snapshot={snapshot} onSelect={onSelect} />
);

/** Users & Roles adapter: members, pending invites and security levels. */
const MembersAdapter: ScreenComponent = ({
  snapshot,
  onSelect,
}: ScreenComponentProps) => (
  <MembersScreen snapshot={snapshot} onSelect={onSelect} />
);

/**
 * Edit Log adapter (Alt+L on a voucher in alteration mode): the voucher's
 * hash-chained version history plus the server's verification of it.
 */
const EditLogAdapter: ScreenComponent = ({
  snapshot,
  onSelect,
}: ScreenComponentProps) => (
  <EditLogScreen snapshot={snapshot} onSelect={onSelect} />
);

/** F1 Help adapter: the keystroke reference for the frame below it. */
const HelpAdapter: ScreenComponent = ({ snapshot }: ScreenComponentProps) => (
  <HelpScreen snapshot={snapshot} />
);

/** Stock Summary adapter: the inventory position (reads snapshot.config.periodTo). */
const StockSummaryAdapter: ScreenComponent = ({
  snapshot,
}: ScreenComponentProps) => <StockSummaryScreen snapshot={snapshot} />;

/** F11 Company Features adapter. */
const FeaturesAdapter: ScreenComponent = () => <FeaturesScreen />;

/** Alt+Y Data adapter: backup and restore (T14). */
const DataAdapter: ScreenComponent = () => <DataScreen />;

/** Alt+O Migrate-from-Tally adapter: the TallyPrime XML importer (T32). */
const TallyImportAdapter: ScreenComponent = () => <TallyImportScreen />;
const ReconciliationAdapter: ScreenComponent = () => <ReconciliationScreen />;

/** Alt+Q Offline Outbox adapter: the queue this machine holds (track D). */
const OfflineOutboxAdapter: ScreenComponent = () => <OfflineOutboxScreen />;

/** Print Templates adapter: the per-company document layouts (track C). */
const PrintTemplateAdapter: ScreenComponent = () => <PrintTemplateScreen />;

/**
 * Build a registry pre-populated with the default screens. Agents extend the
 * returned registry; call sites that want a clean slate construct their own
 * {@link ScreenRegistry}.
 */
export function createDefaultRegistry(): ScreenRegistry {
  return new ScreenRegistry().registerAll({
    gateway: GatewayAdapter,
    // A "menu" frame is the second-level report menu (Display More Reports).
    menu: MoreReportsMenu,
    voucher: VoucherAdapter,
    // A report NAME with no data plane behind it says so; it never fabricates
    // rows. The five wired reports are registered by name below.
    report: UnwiredReportScreen,
    "report-detail": ReportDetailScreen,
    "master-create": MasterCreateAdapter,
    "goto-overlay": GoToOverlayScreen,
    "bill-inbox": BillInboxAdapter,
    query: QueryAdapter,
    // The SaaS screens: F3 company switching and Users & Roles.
    "company-select": CompanySelectAdapter,
    members: MembersAdapter,
    // Alt+L on a voucher: the MCA audit trail, made visible.
    "edit-log": EditLogAdapter,
    // The Bank Reconciliation Statement (T4). Registered as a SCREEN and not as
    // a report name, even though "bank-reconciliation" is a real name in
    // authz.KnownReportNames: the statement is per bank ledger, so the frame
    // carries a ledger rather than a report period, and the print route (which
    // has no ledger id) answers 501 for it on purpose.
    "bank-reconciliation": BankReconciliationScreen,
    // The TDS CHALLAN register (T6, migration 0069). Registered as a SCREEN and
    // not as a report name: the server serves it as the bare resource token
    // `tds-challan`, so a "report:…" key would be reached by migration 0011's
    // coarse `allow display report` and would need an ACL counterweight. Denied
    // to Data Entry by design; the screen renders that 403 as the decision it is.
    "tds-challans": TDSChallansScreen,
    // ---- THE THREE T2 COST SCREENS.
    //
    // All three are SCREENS and not report names, and the two summaries are the
    // interesting case: `cost-centre-summary` and `cost-category-summary` ARE real
    // names in authz.KnownReportNames (migration 0072 refuses both to the seeded
    // Data Entry level, which needs a name to refuse). They are still not
    // "report:…" keys here, because neither is reachable as a reportName frame —
    // the centre summary needs an AXIS the pure machine cannot know, and both
    // render a shape the generic report screens do not. Registering them by
    // ScreenKind is what keeps the Gateway entry from landing on
    // UnwiredReportScreen, which would announce that a built, ACL-gated report is
    // "not available in this build".
    // T5 — the e-way bill (Rule 138). Registered by ScreenKind and NOT as a
    // "report:…" key, for the reason the three cost screens above are: the
    // server serves it under the BARE `e-way-bill` token, which a coarse
    // `allow display report` can never match, and it carries a FORM.
    "e-way-bill": EWayBillScreen,
    // §1.4 B — the e-invoice / IRN. Registered by ScreenKind and NOT as a
    // "report:…" key, for the same reason the e-way bill above is: the server
    // serves it under the BARE `e-invoice` token, which a coarse `allow display
    // report` can never match, and it carries a FORM.
    "e-invoice": EInvoiceScreen,
    // ---- THE TWO T11 BUDGET SCREENS.
    //
    // ⛔ "budget-variance" IS REGISTERED BY ScreenKind AND NOT AS A "report:…" KEY,
    // and unlike the cost screens above the reason is NOT that the server uses a
    // bare token — it genuinely serves this one as `report:budget-variance`. It is
    // a ScreenKind because a `report` frame cannot carry the PLAN, the GRAIN or the
    // AXIS the screen needs, and because registering it as a report key would put
    // UnwiredReportScreen — "not available in this build" — in front of a report
    // that is built and ACL-gated. The ACL name and the navigation mechanism are
    // different things.
    budgets: BudgetsScreen,
    "budget-variance": BudgetVarianceScreen,
    // ---- THE T12 ANNUAL RECONCILIATION.
    //
    // ⛔ REGISTERED BY ScreenKind FOR EXACTLY THE REASON ABOVE, and it is worth
    // saying twice because the temptation is stronger here: the server serves it
    // as `report:gstr9`, right beside `report:gstr1` and `report:gstr3b`, so a
    // "report:…" key looks obviously correct. It would render UnwiredReportScreen
    // — "not available in this build" — over a report that is built and ACL-gated,
    // and it could not carry the FINANCIAL YEAR or the six TAX LEDGERS the screen
    // needs before it can ask for anything.
    //
    // ⚠️ NOTE WHAT IS *NOT* HERE: gstr1 and gstr3b still have no screen at all.
    // Giving this one a screen does not give them one, and nothing here should be
    // read as implying it.
    gstr9: GSTR9Screen,
    "cost-centres": CostCentresScreen,
    "cost-centre-summary": CostCentreSummaryScreen,
    "cost-category-summary": CostCategorySummaryScreen,
    // ---- THE THREE T3 SCREENS (order processing and bills of materials).
    //
    // ⛔ TWO REGISTER SCREENS AND NOT ONE WITH A DIRECTION TOGGLE. `sales-order` and
    // `purchase-order` are separate authorities on separate routes and the PDP reads
    // the PATH, so one screen serving both would be reaching a resource the operator
    // may be denied. The two entries below are two ADAPTERS over one component whose
    // direction is fixed HERE, at registration — that is what keeps the authority
    // split real while the rendering is shared. See the header of
    // OrderRegisterScreen.tsx.
    //
    // All three are SCREENS and not report names: the server serves them under the
    // bare tokens `sales-order`, `purchase-order` and `bom`, which migration 0011's
    // coarse `allow display report` can never match — and all three carry a create
    // form, which no report screen does.
    "sales-orders": SalesOrdersScreen,
    "purchase-orders": PurchaseOrdersScreen,
    boms: BomsScreen,
    // ---- THE FIVE PAYROLL SCREENS (Track 3).
    //
    // ⛔ NONE OF THEM IS A "report:…" KEY, AND TWO OF THEM LOOK LIKE THEY SHOULD
    // BE. `payroll-register` and `payroll-payslip` are real names in
    // authz.KnownReportNames — migration 0035 refuses all five payroll report
    // names to the seeded Data Entry level, and both genuinely PRINT (unlike the
    // two cost summaries, which are mapped and answer 501). They are still
    // ScreenKinds because neither is REACHABLE as a reportName frame: a report
    // frame carries a From/To period, and these carry a WAGE MONTH — and the
    // payslip additionally carries an EMPLOYEE, which no report frame has a place
    // for. Registering them by ScreenKind is what keeps their Gateway entries
    // from landing on UnwiredReportScreen, which would announce that a built,
    // ACL-gated, printable report is "not available in this build".
    //
    // ⛔ AND THE EMPLOYEE MASTER IS THE ONE THAT MOST NEEDED A SCREEN. Eight
    // payroll routes had been mounted and PDP-mapped since Track 3 with NO
    // payroll screen anywhere in this app — so the biggest module in the product
    // was invisible — and two more routes did not exist at all: the salary
    // structure and the statutory election, without which cored refuses every
    // pay run outright. Both are on the employees screen, behind Alt+S, because
    // an employee without them is not a half-finished record but an unpayable
    // one, and the refusal arrives on the RUN.
    employees: EmployeesScreen,
    "payroll-run": PayRunScreen,
    "payroll-register": PayrollRegisterScreen,
    "payroll-payslip": PayslipScreen,
    "payroll-form-138": Form138Screen,
    // F1 Help (keystroke reference, generated from the live keymap) and F11
    // Company Features (the honest capability inventory).
    help: HelpAdapter,
    features: FeaturesAdapter,
    // Alt+Y — DATA: backup and restore (T14). Registered by ScreenKind and NOT
    // as a "report:…" key, and that is the same argument the cost screens and
    // the e-way bill make one degree sharper: the server serves the archive
    // under the BARE `backup` token precisely so migration 0011's coarse
    // `allow display report` can never reach it, and this screen carries a
    // FORM (a file upload) besides.
    data: DataAdapter,
    // Alt+O — MIGRATE FROM TALLY (T32). Registered by ScreenKind and NOT as a
    // "report:…" key: the server serves the importer under the BARE `import`
    // token, which migration 0011's coarse `allow display report` can never
    // reach, and the screen carries a FORM (a file upload) besides.
    //
    // ⛔ THIS ENTRY IS ONE OF THE THREE THAT SHIP THE KEY DEAD SILENTLY. Without
    // it the navMachine pushes a frame nothing renders, so Alt+O appears to do
    // nothing at all — no error, no status line.
    "tally-import": TallyImportAdapter,
    // RECONCILIATION QUEUE (track D). Registered by ScreenKind and NOT as a
    // "report:…" key: the LIST route is a report token but the two routes this
    // screen needs beyond it are governed by the bare `reconciliation` token,
    // which migration 0011's coarse `allow display report` can never reach — and
    // the screen carries a WRITE.
    //
    // ⛔ THIS ENTRY IS ONE OF THE THREE THAT SHIP THE KEY DEAD SILENTLY. Without
    // it the navMachine pushes a frame nothing renders, so Alt+R appears to do
    // nothing at all — no error, no status line.
    "reconciliation": ReconciliationAdapter,
    // PRINT TEMPLATES (track C). Registered by ScreenKind and NOT as a "report:…"
    // key: the server serves these routes under the `company` token — the same
    // authority as the F11 feature settings — which migration 0011's coarse
    // `allow display report` can never reach, and the screen carries a FORM.
    //
    // ⛔ THIS ENTRY IS ONE OF THE THREE THAT SHIP THE SCREEN DEAD SILENTLY.
    // Without it the navMachine pushes a frame nothing renders, so the Gateway
    // entry appears to do nothing at all — no error, no status line.
    "print-templates": PrintTemplateAdapter,
    // OFFLINE OUTBOX (track D). A ScreenKind and not a "report:…" key for a
    // reason stronger than the three above: THERE IS NO ROUTE THAT COULD SERVE
    // THIS LIST. The rows are in this browser's IndexedDB, not in cored — so a
    // report frame, whose entire shape is "ask the server for rows", could not
    // render it even in principle. It also carries two writes (commit, discard).
    //
    // ⛔ THIS ENTRY IS ONE OF THE ONES THAT SHIP THE KEY DEAD SILENTLY. Without
    // it the navMachine pushes a frame nothing renders, so Alt+Q appears to do
    // nothing at all — no error, no status line.
    "offline-outbox": OfflineOutboxAdapter,
    // THE APPROVAL INBOX (W2). The screen takes `onSelect` because the machine
    // needs the highlighted proposal's key to drill it into its native voucher.
    // ⛔ Without this entry the Gateway and Alt+G doors push a frame nothing
    // renders ("No screen registered"), which is a dead door that looks wired.
    "approval-inbox": ApprovalInboxScreen,
    // AGENT SETTINGS (W2 breadth): autonomy, blocked ledgers, the record.
    "agent-settings": () => <AgentSettingsScreen />,
    // Live, per-name report screens (override the generic fallback).
    "report:Day Book": DayBookScreen,
    "report:Trial Balance": TrialBalanceScreen,
    "report:Balance Sheet": BalanceSheetScreen,
    "report:Profit & Loss A/c": ProfitLossScreen,
    "report:Outstandings": OutstandingsScreen,
    // ---- THE PAYABLES AGEING (D94). Registered BY NAME, like the order books and
    // the forecast plane, because cored genuinely serves it as `report:bills-payable`
    // from its own FLAT LITERAL route — which is the entire row: the PDP decides on
    // the resource that path maps to, so a query parameter could never have carried
    // this. Registering it by name is what buys it the report keymap, F2, the report
    // button bar and a place in Go To and Display More Reports.
    //
    // ⛔ AND IT SHARES ONE BODY WITH "Outstandings" (`OutstandingsLikeScreen`) so the
    // two cannot drift, while calling a DIFFERENT client method so the two rules
    // cannot merge. Never collapse them into one name with a ?kind= — that would make
    // "may see what customers owe us, may not see what we owe suppliers" an unwritable
    // rule, which is what it was until this row.
    //
    // ⚠️ UNLIKE the order books and the forecast plane it has NO counterweight
    // migration: the data is not new (a seeded Data Entry level already reads it as
    // ?kind=PAYABLE, and needs to, for the bill-wise picker on a Payment). The rule is
    // WRITABLE at specificity 2 for a tenant that wants the split; it is not seeded.
    "report:Bills Payable": BillsPayableScreen,
    // ---- THE UNADJUSTED-FOREX REPORT (T8 step 7). Registered BY NAME, which is
    // what buys it the report keymap, F2, the report button bar and a place in Go To
    // and Display More Reports. ⛔ NOTHING TYPE-CHECKS THIS LINE — a `ScreenKind`
    // registration is not an exhaustive Record the way `REPORT_SLUGS` and
    // `REPORT_PERIOD_SHAPE` are, so a name added to REPORT_NAMES with no entry here
    // renders "not available in this build" over a report that is built and served.
    //
    // ⚠️ ITS OWN COMPONENT, NOT `OutstandingsLikeScreen`. The ageings share one body
    // because they are the same report on two sides; this one has two RATE columns
    // (which are not money), no ageing slabs, no On Account row and a caption making
    // a different promise — that the difference is REPORTED AND NOT POSTED.
    //
    // Denied to the seeded Data Entry level by migration 0090, `disallow display` AND
    // `disallow print`; the screen renders that refusal as the reason it is.
    "report:Unadjusted Forex Gain/Loss": UnadjustedForexScreen,
    // Inventory: the position as at a date, replayed from the movement ledger.
    // Denied to Data Entry by migration 0026 (it prints cost prices); the
    // screen states that refusal rather than rendering an empty table.
    "report:Stock Summary": StockSummaryAdapter,
    // The TDS/TCS register (Item Zero step 4). Denied to Data Entry by
    // migration 0068; the screen renders that refusal as the reason it is.
    "report:TDS / TCS Register": TDSRegisterScreen,
    // ---- THE TWO ORDER BOOKS (T3). Registered BY NAME, unlike the three screens
    // above, because both genuinely ARE report names on the server
    // (`report:sales-order-book`, `report:purchase-order-book`) and both are pure
    // readers with an as-at date and nothing else — exactly what a `report` frame
    // is. Registering them by name is what gives them the report keymap, the report
    // button bar, F2 and a place in Go To and Display More Reports for free.
    //
    // ⛔ AND THEY ARE TWO NAMES BECAUSE A QUERY PARAMETER IS INVISIBLE TO THE PDP.
    // Migration 0073 refuses both to the seeded Data Entry level with `disallow
    // display` AND `disallow print` for each. Never collapse them into one name with
    // a ?direction= — that would make "may see the sales pipeline, may not see what
    // we owe suppliers" an unwritable rule.
    "report:Sales Order Book": SalesOrderBookScreen,
    "report:Purchase Order Book": PurchaseOrderBookScreen,
    // ---- THE FORECAST PLANE (wave-2 WS5a). Registered BY NAME for the same
    // reason as the order books: cored genuinely serves them as `report:cash-flow`
    // and `report:anomalies`, and both are pure readers with a date dimension and
    // no form — which is what a `report` frame is.
    //
    // ⛔ THESE TWO LINES ARE THE DOOR. Both routes have been live on the server
    // since wave 2 with no Gateway entry, no Go To entry and no screen, while F11
    // advertised the capability as `on` — register defects D22 and D52. A report
    // NAME without a component here does not fail loudly: it falls through to
    // `UnwiredReportScreen`, so the name and the screen must land together or
    // Alt+G leads somewhere worse than nowhere.
    //
    // ⚠️ NEITHER HAS AN ACL COUNTERWEIGHT MIGRATION. Unlike the order books (0073)
    // and the Stock Summary (0026), nothing narrows `report:cash-flow` or
    // `report:anomalies`, so 0011's coarse `allow display report` grants both to
    // the seeded Data Entry level. For the anomaly list — every backdated,
    // after-hours and round-amount voucher, by number — that is a policy question,
    // it predates this door, and it is registered rather than decided here.
    "report:Cash Flow": CashFlowScreen,
    "report:Anomalies": AnomaliesScreen,
  });
}
