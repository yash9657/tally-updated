import {
  createContext,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { PurchaseVoucherDraft } from "../api";

/**
 * A tiny single-slot store for the "loaded draft" — the OCR-derived
 * {@link PurchaseVoucherDraft} that insightd returned for an uploaded invoice.
 * When a draft is fetched (e.g. by a drafts inbox or the e2e harness), it is
 * stashed here BEFORE the Purchase (F9) voucher frame opens, so the
 * VoucherScreen can read it, hydrate its form (prefill + per-field confidence +
 * status banner), and on Ctrl+A submit the ORIGINAL balanced body through the
 * existing cored write path.
 *
 * This mirrors {@link LoadedVoucher} exactly: a single slot (Tally edits one
 * voucher at a time), overwritten by a fresh load and cleared on accept. It is
 * deliberately separate from LoadedVoucher so the draft (create) path and the
 * drill-to-alter path never collide.
 */

export interface LoadedDraft {
  readonly draft: PurchaseVoucherDraft;
}

interface LoadedDraftValue {
  loaded: LoadedDraft | null;
  setLoaded: (v: LoadedDraft | null) => void;
}

const LoadedDraftContext = createContext<LoadedDraftValue | null>(null);

export function LoadedDraftProvider({ children }: { children: ReactNode }) {
  const [loaded, setLoaded] = useState<LoadedDraft | null>(null);
  const value = useMemo<LoadedDraftValue>(
    () => ({ loaded, setLoaded }),
    [loaded],
  );
  return (
    <LoadedDraftContext.Provider value={value}>
      {children}
    </LoadedDraftContext.Provider>
  );
}

export function useLoadedDraft(): LoadedDraftValue {
  const ctx = useContext(LoadedDraftContext);
  if (!ctx) {
    throw new Error("useLoadedDraft must be used within a LoadedDraftProvider");
  }
  return ctx;
}
