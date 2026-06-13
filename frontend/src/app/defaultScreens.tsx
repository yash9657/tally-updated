import { SelectionList } from "../screens/SelectionList";
import { GatewayScreen } from "../screens/GatewayScreen";
import { VoucherScreen } from "../screens/VoucherScreen";
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

/** Generic report screen — the reports agent registers richer per-name views. */
const ReportScreen: ScreenComponent = ({ snapshot, onSelect }) => {
  const name = snapshot.reportName ?? "Report";
  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">{name}</h1>
      <p className="mt-2 text-sm">
        Press Enter on a line to drill down; Esc to go back.
      </p>
      <div className="mt-3 max-w-md bg-tally-panel p-3">
        <SelectionList
          label="Rows"
          options={["Row 1", "Row 2", "Row 3"]}
          onSelect={onSelect}
          autoFocus
        />
      </div>
    </section>
  );
};

/** Generic report-detail screen (a drill level inside a statement report). */
const ReportDetailScreen: ScreenComponent = ({ snapshot, onSelect }) => {
  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">
        {snapshot.reportName ?? "Report"} — {snapshot.detailOf ?? "Detail"}
      </h1>
      <p className="mt-2 text-sm">
        Press Enter on a line to open the underlying voucher; Esc to go back.
      </p>
      <div className="mt-3 max-w-md bg-tally-panel p-3">
        <SelectionList
          label="Rows"
          options={["Voucher 1", "Voucher 2"]}
          onSelect={onSelect}
          autoFocus
        />
      </div>
    </section>
  );
};

/** Generic inline master-create screen — the masters agent registers the form. */
const MasterCreateScreen: ScreenComponent = ({ snapshot, onSelect }) => {
  const kind = snapshot.masterKind ?? "ledger";
  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">
        Create {kind.replace("-", " ")}
      </h1>
      <div className="mt-3 max-w-md bg-tally-panel p-3">
        <p className="mb-1 text-xs">
          Active field: <span className="text-tally-title">{snapshot.field}</span>
        </p>
        <SelectionList
          label="Under (group)"
          options={["Sundry Debtors", "Sundry Creditors", "Cash-in-Hand"]}
          onSelect={onSelect}
          autoFocus
        />
        <p className="mt-2 text-xs">Ctrl+A to accept and return; Esc to cancel.</p>
      </div>
    </section>
  );
};

/** Gateway adapter: the Phase 0 GatewayScreen only needs onSelect. */
const GatewayAdapter: ScreenComponent = ({ onSelect }: ScreenComponentProps) => (
  <GatewayScreen onSelect={onSelect} />
);

/** Voucher adapter: the Phase 0 VoucherScreen needs the snapshot + onSelect. */
const VoucherAdapter: ScreenComponent = ({
  snapshot,
  onSelect,
}: ScreenComponentProps) => (
  <VoucherScreen snapshot={snapshot} onSelect={onSelect} />
);

/**
 * Build a registry pre-populated with the default screens. Agents extend the
 * returned registry; call sites that want a clean slate construct their own
 * {@link ScreenRegistry}.
 */
export function createDefaultRegistry(): ScreenRegistry {
  return new ScreenRegistry().registerAll({
    gateway: GatewayAdapter,
    menu: GatewayAdapter,
    voucher: VoucherAdapter,
    report: ReportScreen,
    "report-detail": ReportDetailScreen,
    "master-create": MasterCreateScreen,
    "goto-overlay": GoToOverlayScreen,
  });
}
