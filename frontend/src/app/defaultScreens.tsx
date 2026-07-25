import { SelectionList } from "../screens/SelectionList";
import { GatewayScreen } from "../screens/GatewayScreen";
import { VoucherScreen } from "../screens/VoucherScreen";
import { BillInboxScreen } from "../screens/BillInboxScreen";
import { QueryScreen } from "../screens/QueryScreen";
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
 * Fallback for a report name that has no wired data source (e.g. Stock Summary,
 * which needs inventory vouchers this build does not have). It says so plainly
 * — it must NEVER render placeholder rows, because fabricated numbers in an
 * accounting screen are worse than a missing screen.
 */
const UnwiredReportScreen: ScreenComponent = ({ snapshot }) => {
  const name = snapshot.reportName ?? "Report";
  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">{name}</h1>
      <p className="mt-3 max-w-xl text-sm">
        {name} is not available in this build — there is no data plane behind it
        yet (it needs the inventory module). Nothing is shown rather than sample
        rows, because a report that invents numbers is worse than no report.
      </p>
      <p className="mt-2 text-sm">
        Working reports: Balance Sheet, Profit &amp; Loss A/c, Trial Balance, Day
        Book, Outstandings — all reachable with Alt+G. Esc goes back.
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
const GatewayAdapter: ScreenComponent = ({ onSelect }: ScreenComponentProps) => (
  <GatewayScreen onSelect={onSelect} />
);

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

/** F1 Help adapter: the keystroke reference for the frame below it. */
const HelpAdapter: ScreenComponent = ({ snapshot }: ScreenComponentProps) => (
  <HelpScreen snapshot={snapshot} />
);

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
  });
}
