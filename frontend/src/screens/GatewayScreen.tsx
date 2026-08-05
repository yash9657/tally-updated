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
