import {
  createContext,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { AgentApproval } from "../api";
import type { ProposalKey } from "../keystroke/proposalKey";

/**
 * A single-slot store for the AGENT PROPOSAL being reviewed in a native voucher
 * screen (W2) — the sibling of {@link LoadedDraft}, for the same reason and with
 * the same contract.
 *
 * The approval inbox (or the Ctrl+K side panel) puts the approval here, THEN
 * fires the proposal key through `onSelect`; the machine pushes a create frame of
 * the proposal's class, and VoucherScreen — seeing a loaded proposal whose class
 * and approval match the frame — prefills its ordinary form from the approval's
 * previewed postings and turns Ctrl+A into "approve this", with the edited body
 * when the operator changed anything.
 *
 * ⛔ SEPARATE FROM LoadedDraft AND LoadedVoucher ON PURPOSE. Each slot selects a
 * different WRITE for Ctrl+A — an OCR draft posts a purchase, a loaded voucher is
 * re-PUT, and a proposal posts NOTHING (it records a decision; the agent's poller
 * commits afterwards). Sharing a slot is how one of those writes would be taken
 * for another, and the dangerous confusion is the one where a proposal is POSTED
 * directly: the approval stays pending, and the moment somebody approves it in the
 * inbox the same payment is on the books twice.
 */
export interface LoadedProposal {
  readonly approval: AgentApproval;
  readonly key: ProposalKey;
}

interface LoadedProposalValue {
  loaded: LoadedProposal | null;
  setLoaded: (v: LoadedProposal | null) => void;
}

const LoadedProposalContext = createContext<LoadedProposalValue | null>(null);

export function LoadedProposalProvider({ children }: { children: ReactNode }) {
  const [loaded, setLoaded] = useState<LoadedProposal | null>(null);
  const value = useMemo<LoadedProposalValue>(
    () => ({ loaded, setLoaded }),
    [loaded],
  );
  return (
    <LoadedProposalContext.Provider value={value}>
      {children}
    </LoadedProposalContext.Provider>
  );
}

/**
 * The slot, or an INERT one outside a provider.
 *
 * ⚠️ INERT RATHER THAN THROWING, unlike useLoadedDraft, and deliberately: 60-odd
 * VoucherScreen tests render the screen inside a LoadedDraftProvider and nothing
 * else, and a proposal slot they never use must not make every one of them wrap a
 * second provider. Inert means "no proposal is loaded", which is exactly the
 * truth outside the shell — and it cannot turn Ctrl+A into a decision by
 * accident, because a decision needs a loaded approval.
 */
export function useLoadedProposal(): LoadedProposalValue {
  return useContext(LoadedProposalContext) ?? INERT;
}

const INERT: LoadedProposalValue = { loaded: null, setLoaded: () => {} };
