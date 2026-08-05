import { SelectionList } from "../screens/SelectionList";
import { GatewayScreen } from "../screens/GatewayScreen";
import { VoucherScreen } from "../screens/VoucherScreen";
import { BillInboxScreen } from "../screens/BillInboxScreen";
import { QueryScreen } from "../screens/QueryScreen";
import { CompanySelectScreen } from "../screens/CompanySelectScreen";
import { MembersScreen } from "../screens/MembersScreen";
import { EditLogScreen } from "../screens/EditLogScreen";
import { HelpScreen } from "../screens/HelpScreen";
import { FeaturesScreen } from "../screens/FeaturesScreen";
import { MasterCreateScreen } from "../screens/masters/MasterCreateScreen";
import {
  DayBookScreen,
  TrialBalanceScreen,
  BalanceSheetScreen,
  ProfitLossScreen,
  OutstandingsScreen,
} from "../screens/reports/ReportScreens";
import { ReportDetailScreen } from "../screens/reports/ReportDetailScreen";
import { StockSummaryScreen } from "../screens/reports/StockSummaryScreen";
import { TDSRegisterScreen } from "../screens/reports/TDSRegisterScreen";
import { BankReconciliationScreen } from "../screens/BankReconciliationScreen";
import { TDSChallansScreen } from "../screens/TDSChallansScreen";
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
import { REPORT_NAMES } from "../keystroke/types";
import type { ScreenComponent, ScreenComponentProps } from "./screenRegistry";
import { ScreenRegistry } from "./screenRegistry";

/**
 * Default, framework-owned screen components. These keep the shell renderable
 * before/without the voucher/report/master agents' richer components. Any agent
 * registers over these by calling `registry.register(...)` with the same key.
 *
 * The Gateway and Voucher screens reuse the existing Phase 0 components.
 */

/** The Alt+G / Ctrl+G Go To picker. Lists the Phase 1 reports plus Vouchers. */
const GoToOverlayScreen: ScreenComponent = ({ snapshot, onSelect }) => {
  const verb = "Go To"; // gotoMode is internal to the machine; label stays "Go To"
  return (
    <section className="flex h-full items-start justify-center bg-tally-bg/80 p-8">
      <div className="w-80 bg-tally-panel p-3 font-mono text-tally-field">
        <h2 className="mb-2 text-tally-title">{verb}</h2>
        <SelectionList
          label="Search"
          options={["Vouchers", ...REPORT_NAMES]}
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
    // F1 Help (keystroke reference, generated from the live keymap) and F11
    // Company Features (the honest capability inventory).
    help: HelpAdapter,
    features: FeaturesAdapter,
    // Live, per-name report screens (override the generic fallback).
    "report:Day Book": DayBookScreen,
    "report:Trial Balance": TrialBalanceScreen,
    "report:Balance Sheet": BalanceSheetScreen,
    "report:Profit & Loss A/c": ProfitLossScreen,
    "report:Outstandings": OutstandingsScreen,
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
  });
}
