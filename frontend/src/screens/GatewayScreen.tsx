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
