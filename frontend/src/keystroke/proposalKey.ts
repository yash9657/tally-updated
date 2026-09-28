import { isVoucherType, type VoucherType } from "./types";

/**
 * THE PROPOSAL KEY — how the approval inbox and the agent side panel tell the
 * navigation machine to open an agent's proposal in its NATIVE voucher screen.
 *
 * `navMachine` is pure and has no database, so it cannot look an approval up.
 * What it needs to push the right frame is two facts only — which voucher CLASS
 * the proposal is, and which ENTRY MODE the native form must be in to restate
 * its postings exactly — and those travel on the key, the same way a Day Book
 * row carries its class on `voucher:<type>:<id>` (keystroke/voucherClass.ts).
 * The approval itself NEVER travels through the machine: the screen puts it in
 * the LoadedProposal slot (app/LoadedProposal.tsx) BEFORE it fires the key, and
 * VoucherScreen reads it from there — exactly the OPEN_PURCHASE_DRAFT_SENTINEL
 * contract the Bill Inbox has used since Phase 2.
 *
 * Two spellings, one grammar, `<prefix><voucherType>:<entryMode>:<approvalID>`:
 *
 *   `proposal:`                  a HIGHLIGHT on the approval inbox; Enter drills.
 *   `__open-agent-proposal__:`   an OPEN from the side panel, from any screen
 *                                that is not a form being keyed (see navMachine).
 *
 * ⛔ THE ENTRY MODE IS ONLY EVER "single" OR "double". Those are the two forms
 * that restate a postings body line for line; an invoice mode would RE-DERIVE
 * the tax from a taxable value, i.e. post something other than what the agent
 * previewed and the human approved. A key naming any other mode is refused.
 */
export const PROPOSAL_ROW_PREFIX = "proposal:";
export const OPEN_AGENT_PROPOSAL_PREFIX = "__open-agent-proposal__:";

export type ProposalEntryMode = "single" | "double";

export interface ProposalKey {
  readonly voucherType: VoucherType;
  readonly entryMode: ProposalEntryMode;
  readonly approvalID: string;
}

function build(prefix: string, k: ProposalKey): string {
  return `${prefix}${k.voucherType}:${k.entryMode}:${k.approvalID}`;
}

/** The inbox's row key for a prefillable proposal. */
export function proposalRowKey(k: ProposalKey): string {
  return build(PROPOSAL_ROW_PREFIX, k);
}

/** The side panel's open sentinel for a prefillable proposal. */
export function openProposalSentinel(k: ProposalKey): string {
  return build(OPEN_AGENT_PROPOSAL_PREFIX, k);
}

/**
 * Parse either spelling under a given prefix. `undefined` for anything else —
 * including an EMPTY approval id, which would otherwise open a prefilled-looking
 * frame for no proposal (the create-in-disguise hazard voucherClass.ts names).
 */
export function parseProposalKey(
  key: string | undefined,
  prefix: string,
): ProposalKey | undefined {
  if (key === undefined || !key.startsWith(prefix)) return undefined;
  const parts = key.slice(prefix.length).split(":");
  if (parts.length !== 3) return undefined;
  const [type, mode, id] = parts as [string, string, string];
  if (!isVoucherType(type)) return undefined;
  if (mode !== "single" && mode !== "double") return undefined;
  if (id === "") return undefined;
  return { voucherType: type, entryMode: mode, approvalID: id };
}
