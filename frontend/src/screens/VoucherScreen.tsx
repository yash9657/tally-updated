import { SelectionList } from "./SelectionList";
import type { NavSnapshot } from "../keystroke/navMachine";

/** Sample party ledgers for the auto-completing selection list. */
const PARTY_LEDGERS = [
  "Cash",
  "Bank",
  "Acme Traders",
  "Bharat Stores",
  "Chandra & Co",
  "Sales",
  "Purchase",
] as const;

const VOUCHER_TITLES: Record<string, string> = {
  contra: "Contra",
  payment: "Payment",
  receipt: "Receipt",
  journal: "Journal",
  sales: "Sales",
  purchase: "Purchase",
};

/**
 * The voucher entry screen (F8 Sales by default). Shows the date field, the
 * auto-completing party-ledger selection list, and reflects the field cursor
 * driven by the navigation machine. The fixed right button bar is rendered by
 * the shell.
 */
export function VoucherScreen({
  snapshot,
  onSelect,
}: {
  snapshot: NavSnapshot;
  onSelect: (item: string) => void;
}) {
  const title = snapshot.voucherType
    ? (VOUCHER_TITLES[snapshot.voucherType] ?? snapshot.voucherType)
    : "Voucher";

  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <header className="mb-3 flex items-center justify-between">
        <h1 className="text-lg font-bold text-tally-title">
          {title} Voucher
          {snapshot.mode === "alter" ? " (Alteration)" : ""}
        </h1>
        {snapshot.saved ? (
          <span className="text-tally-active" role="status">
            Saved
          </span>
        ) : null}
      </header>

      <div className="bg-tally-panel p-3">
        <FieldRow
          name="Date"
          active={snapshot.field === "date"}
          value="13-Jun-2026"
        />
        <div className={snapshot.field === "party-ledger" ? "" : "opacity-70"}>
          <SelectionList
            label="Party A/c Name"
            options={PARTY_LEDGERS}
            onSelect={onSelect}
            autoFocus={snapshot.field === "party-ledger"}
          />
        </div>
        <FieldRow name="Narration" active={snapshot.field === "narration"} value="" />
      </div>

      {snapshot.overlay === "change-mode" ? (
        <ChangeModeOverlay onSelect={onSelect} />
      ) : null}
    </section>
  );
}

function FieldRow({
  name,
  active,
  value,
}: {
  name: string;
  active: boolean;
  value: string;
}) {
  return (
    <div className="flex items-center gap-2 py-0.5">
      <span className="w-32">{name}</span>
      <span
        className={
          active
            ? "bg-tally-active px-2 text-white"
            : "bg-tally-field px-2 text-tally-fieldText"
        }
      >
        {value || " "}
      </span>
    </div>
  );
}

/** The Ctrl+H Change Mode picker (Item Invoice / Accounting Invoice / As Voucher). */
function ChangeModeOverlay({ onSelect }: { onSelect: (item: string) => void }) {
  return (
    <div
      role="dialog"
      aria-label="Change Mode"
      className="mt-3 mx-auto w-72 bg-tally-panel p-3"
    >
      <h2 className="mb-2 text-tally-title">Change Mode</h2>
      <SelectionList
        label="Mode"
        options={["Item Invoice", "Accounting Invoice", "As Voucher"]}
        onSelect={onSelect}
        autoFocus
      />
    </div>
  );
}
