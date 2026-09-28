/**
 * THE AGENT PLANE's wire contract (W2, D121 §8 "Frontend contract").
 *
 * Mirrors backend/internal/server/agent_http.go and internal/agent's JSON tags
 * field for field. Three things about this surface are worth knowing before a
 * screen is built on it:
 *
 *  - **Nothing here writes to the books.** Every route is either a READ of the
 *    agent's own tables or a DECISION about a proposal. The only write into the
 *    ledger is the agent's commit step, which the server runs on its next poll
 *    AFTER a human approved — replaying the preview (or the approver's edit),
 *    never regenerating it (PLAN-AGENTIC §2.1). A screen that "approves" has not
 *    posted a voucher, and must not say it has.
 *
 *  - **Money is paise wherever a field ends `_paise`** (integers, computed by
 *    Go), and decimal STRINGS inside a voucher body. Neither is ever arithmetic
 *    on this side: the ledger-effect diff shows what the SERVER's real handler
 *    derived during the preview, which is the whole point of showing it.
 *
 *  - **`reasons` and `provenance` are the reason a human is being asked.** They
 *    are rendered verbatim, and a body value that appears word for word in an
 *    untrusted input (`provenance.fields`) is rendered loudly — that is the
 *    injection defence's human half (PLAN-AGENTIC §2.4).
 */

/** Where an untrusted input came from (policy.Source). `user`/`books` are refused on input. */
export type AgentProvenanceSource =
  | "user"
  | "books"
  | "document"
  | "bank"
  | "whatsapp"
  | "tally_xml"
  | "email"
  | "stored_text";

/** One piece of UNTRUSTED material a run is handed (agent.Input). */
export interface AgentInput {
  label: string;
  provenance: AgentProvenanceSource;
  text: string;
}

/** What the human delegated to the run (policy.Delegation). */
export interface AgentDelegation {
  acl_keys: string[];
  max_amount_paise?: number;
  expires_at: string;
  step_up: boolean;
}

export type AgentRunStatus =
  | "running"
  | "parked"
  | "completed"
  | "failed"
  | "expired"
  | "cancelled";

/** A run as the wire shows it (agent.Run, plus `approvals` on the detail read). */
export interface AgentRun {
  id: string;
  created_by: string;
  agent_id: string;
  channel: string;
  instruction: string;
  inputs: AgentInput[] | null;
  delegation: AgentDelegation;
  status: AgentRunStatus;
  /** `{status, final, reason}` once finished; absent while running. */
  outcome?: { status?: string; final?: string; reason?: string } | null;
  last_error?: string;
  attempts: number;
  next_poll_at?: string;
  created_at: string;
  updated_at: string;
  finished_at?: string;
  /** Present on GET /agent/runs/{id} only. */
  approvals?: AgentApproval[];
}

/** A run is finished when it will never be polled again (agent.Status.Finished). */
export function runFinished(status: AgentRunStatus): boolean {
  return (
    status === "completed" ||
    status === "failed" ||
    status === "expired" ||
    status === "cancelled"
  );
}

/**
 * One intent-log row, as the AG-UI-shaped event stream serves it.
 *
 * `type` is the AG-UI vocabulary; `kind` is the intent log's own and is the one
 * a screen should branch on — the mapping from kind to type is lossy on purpose
 * (five kinds ride `CUSTOM`), and `kind` is what makes it lossless.
 */
export interface AgentEvent {
  seq: number;
  type:
    | "RUN_STARTED"
    | "RUN_FINISHED"
    | "RUN_ERROR"
    | "STEP_FINISHED"
    | "TOOL_CALL_RESULT"
    | "CUSTOM";
  name?: string;
  kind: string;
  at: string;
  actor_kind: "user" | "agent" | "system" | string;
  actor_id: string;
  on_behalf_of?: string;
  action_id?: string;
  acl_key?: string;
  /** Ids, verdicts, paise and HASHES of text — never a prompt (D121 §8.2). */
  data: Record<string, unknown> | null;
  hash: string;
}

export interface AgentEventsResponse {
  run_id: string;
  status: AgentRunStatus;
  finished: boolean;
  /** Pass back as `after` on the next poll. */
  next_after: number;
  events: AgentEvent[];
}

/** One line of a ledger effect: what the real handler would post. */
export interface AgentPosting {
  ledger_id: string;
  /** The ledger's name, read back inside the preview's own transaction. */
  ledger: string;
  dr_cr: "D" | "C" | string;
  amount: string;
  amount_paise: number;
}

/** The ledger-effect diff (agent.Effect). Totals are paise, computed by Go. */
export interface AgentEffect {
  postings: AgentPosting[];
  debit_paise: number;
  credit_paise: number;
  vouchers: number;
}

/** The voucher body the agent proposed (the preview's recorded args). */
export interface AgentProposalBody {
  voucher_date?: string;
  narration?: string;
  party?: string;
  created_by?: string;
  postings?: { ledger_id: string; dr_cr: string; amount: string }[];
  [key: string]: unknown;
}

export type AgentApprovalState =
  | "pending"
  | "approved"
  | "rejected"
  | "ask_back"
  | "expired";

export interface AgentDecisionView {
  id: string;
  decision: "approve" | "reject" | "ask_back" | "expired" | string;
  edited_args?: Record<string, unknown>;
  note?: string;
  decided_by?: string;
  decided_webauthn: boolean;
  decided_at: string;
}

/** An approval-inbox row (agent.Approval). */
export interface AgentApproval {
  id: string;
  run_id: string;
  step_key: string;
  state: AgentApprovalState;
  action_id: string;
  acl_key: string;
  risk: string;
  instruction: string;
  args: {
    action_id?: string;
    path_params?: Record<string, string>;
    body?: AgentProposalBody;
  } | null;
  /** `{status, effect}` — the preview the real handler produced, then rolled back. */
  preview: { status?: number; effect?: AgentEffect | null } | null;
  amount_paise?: number;
  /** WHY a human is needed. Rendered verbatim, never paraphrased. */
  reasons: string[] | null;
  provenance: {
    context_sources: AgentProvenanceSource[] | null;
    /** Body path -> the untrusted source its value was found in verbatim. */
    fields?: Record<string, AgentProvenanceSource>;
  } | null;
  on_behalf_of: string;
  requested_at: string;
  /** 72 h after requested_at. A decision after it is recorded as `expired`. */
  deadline_at: string;
  decision?: AgentDecisionView;
}

export interface StartAgentRunRequest {
  instruction: string;
  channel?: "panel";
  inputs?: AgentInput[];
  /** PDP ACL keys ("create payment"). No wildcards exist. */
  delegate?: string[];
  max_amount_paise?: number;
}

/**
 * One decision about one approval.
 *
 * ⛔ `edited_body` IS A FULL VOUCHER BODY AND ONLY MEANINGFUL WITH `approve` —
 * the server refuses it beside reject/ask_back (422). It is what the approver's
 * NATIVE voucher screen built from what they keyed, and it goes through the
 * same validators as any other voucher; a refused edit comes back as a
 * `commit_refused` event, not as a voucher.
 */
export interface AgentDecisionRequest {
  decision: "approve" | "reject" | "ask_back";
  edited_body?: Record<string, unknown>;
  /** Required for ask_back; optional for reject. */
  note?: string;
}

export interface AgentRunResponse {
  run: AgentRun;
}
export interface AgentRunsResponse {
  runs: AgentRun[];
}
export interface AgentApprovalsResponse {
  approvals: AgentApproval[];
}
export interface AgentApprovalResponse {
  approval: AgentApproval;
}

/** The 503 code the server answers when no model is configured (the shipped default). */
export const AGENT_UNCONFIGURED = "AGENT_UNCONFIGURED";

/**
 * The previewed ledger effect of an approval, or null when there is none to show.
 *
 * ⛔ NULL IS A REFUSAL INPUT, NOT A RENDERING DETAIL. The inbox will not approve
 * a proposal whose diff it cannot draw (see ApprovalInboxScreen), so this
 * function must never invent an effect from the proposed body: the body is what
 * the MODEL said, and the effect is what the SERVER's handler derived from it.
 * Showing the first under the heading of the second would be the exact
 * "LLM-computed money" PLAN-AGENTIC §2.2 forbids, one layer up.
 */
export function approvalEffect(a: AgentApproval): AgentEffect | null {
  const e = a.preview?.effect;
  if (!e || !Array.isArray(e.postings) || e.postings.length === 0) return null;
  return e;
}

/** Format integer paise as a rupee string with two decimals. Display only. */
export function formatPaise(paise: number | undefined | null): string {
  if (paise === undefined || paise === null || !Number.isFinite(paise)) return "—";
  const neg = paise < 0;
  const abs = Math.abs(Math.trunc(paise));
  const rupees = Math.floor(abs / 100);
  const p = String(abs % 100).padStart(2, "0");
  return `${neg ? "-" : ""}${rupees}.${p}`;
}

/**
 * A one-line, human description of one event for the side panel.
 *
 * Reads only the `data` fields D121 §8.2 documents, and never renders anything
 * the server did not send: an unknown kind is shown by its kind, not guessed at.
 */
export function describeAgentEvent(ev: AgentEvent): string {
  const d = (ev.data ?? {}) as Record<string, unknown>;
  const s = (k: string) => (typeof d[k] === "string" ? (d[k] as string) : "");
  const n = (k: string) => (typeof d[k] === "number" ? (d[k] as number) : undefined);
  switch (ev.kind) {
    case "run_started":
      return "Run started";
    case "decision_step":
      return `Decided: ${s("tool") || ev.action_id || "next step"}`;
    case "tool_call":
      return `Tool ${s("tool") || ev.action_id || ""}${n("status") ? ` → ${n("status")}` : ""}`.trim();
    case "preview": {
      const ok = d["ok"] === true;
      return ok
        ? `Previewed ${ev.action_id ?? ""}: Dr ${formatPaise(n("debit_paise"))} / Cr ${formatPaise(n("credit_paise"))}`
        : `Preview of ${ev.action_id ?? ""} refused (${n("status") ?? "?"})`;
    }
    case "verifier_error":
      return `Validator refused ${ev.action_id ?? ""} (${n("status") ?? "?"})`;
    case "policy": {
      const reasons = Array.isArray(d["reasons"]) ? (d["reasons"] as unknown[]).join("; ") : "";
      return `Policy: ${s("mode")}${s("level") ? ` at ${s("level")}` : ""}${reasons ? ` — ${reasons}` : ""}`;
    }
    case "approval_requested":
      return `Waiting for a human: ${formatPaise(n("amount_paise"))} on ${ev.action_id ?? ""}`;
    case "approval_decided":
      return `Human decision: ${s("decision")}${d["edited"] === true ? " (edited)" : ""}`;
    case "commit": {
      const replayed = d["replayed"] === true ? " (replayed)" : "";
      return `Committed ${ev.action_id ?? ""} → ${n("status") ?? "?"}${replayed}`;
    }
    case "commit_refused":
      return `Commit REFUSED for ${ev.action_id ?? ""} (${n("status") ?? s("reason") ?? "?"})`;
    case "run_finished":
      return `Run finished: ${s("status")}${s("reason") ? ` — ${s("reason")}` : ""}`;
    case "run_failed":
      return `Run failed${s("reason") ? `: ${s("reason")}` : ""}`;
    default:
      return ev.kind;
  }
}
