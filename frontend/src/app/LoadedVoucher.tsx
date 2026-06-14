import {
  createContext,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { DayBookEntry } from "../api";

/**
 * A tiny store for the "loaded voucher" — the real voucher a Day Book (or other
 * voucher-bearing report) row drills into. When the user presses Enter on a Day
 * Book row, the navMachine pushes a fresh alteration voucher frame (pure, no
 * data). The report screen, which already holds the selected {@link DayBookEntry}
 * (voucher_id, voucher_no, date, party, postings), stashes it here BEFORE the
 * drill so the alter VoucherScreen can read it and pre-fill its form state.
 *
 * Pre-filling matters for numbering preservation: the alter form resends the
 * loaded voucher_no/date verbatim, and the loaded voucher_id becomes
 * ctx.voucherID so Ctrl+A routes to alter{Class}Voucher (PUT), not a create.
 *
 * The store is intentionally a single slot (Tally drills one voucher at a time).
 * The alter screen clears it on Esc/accept via the consumer's discretion; we
 * keep it simple and let a fresh drill overwrite the slot.
 */

export interface LoadedVoucher {
  readonly entry: DayBookEntry;
}

interface LoadedVoucherValue {
  loaded: LoadedVoucher | null;
  setLoaded: (v: LoadedVoucher | null) => void;
}

const LoadedVoucherContext = createContext<LoadedVoucherValue | null>(null);

export function LoadedVoucherProvider({ children }: { children: ReactNode }) {
  const [loaded, setLoaded] = useState<LoadedVoucher | null>(null);
  const value = useMemo<LoadedVoucherValue>(
    () => ({ loaded, setLoaded }),
    [loaded],
  );
  return (
    <LoadedVoucherContext.Provider value={value}>
      {children}
    </LoadedVoucherContext.Provider>
  );
}

export function useLoadedVoucher(): LoadedVoucherValue {
  const ctx = useContext(LoadedVoucherContext);
  if (!ctx) {
    throw new Error(
      "useLoadedVoucher must be used within a LoadedVoucherProvider",
    );
  }
  return ctx;
}
