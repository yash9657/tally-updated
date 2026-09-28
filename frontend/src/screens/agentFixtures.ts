import type { AgentApproval, AgentEvent, AgentRun } from "../api";

/**
 * AGENT-PLANE FIXTURES for the inbox, panel and proposal-mode tests (W2).
 *
 * Shaped exactly as internal/agent serialises an Approval and a Run (D121 §8),
 * including the fields a screen must NOT trust: `args.body` is what the model
 * proposed, `preview.effect` is what the server's handler derived — and the two
 * are deliberately made to carry different ledger NAMES in one fixture below, so
 * a screen that read names from the wrong one shows the wrong word.
 *
 * ⚠️ NOT A TEST FILE (no `.test.` in the name), so vitest does not collect it;
 * it holds data only and names no client method, so the UI-command census's
 * mention scan finds nothing in it to count.
 */

export const RUN_ID = "run-7d3c";

/** A pending PAYMENT proposal: Rs 25,000 office rent from HDFC Bank. */
export function paymentApproval(
  opts: { id?: string; officeRentID: string; hdfcID?: string; deadline?: string } = {
    officeRentID: "ledger-rent",
  },
): AgentApproval {
  const rent = opts.officeRentID;
  const hdfc = opts.hdfcID ?? "ledger-hdfc";
  return {
    id: opts.id ?? "appr-pay-1",
    run_id: RUN_ID,
    step_key: "gate:3",
    state: "pending",
    action_id: "post.vouchers.payment",
    acl_key: "create payment",
    risk: "money",
    instruction: "Pay the May office rent of Rs 25,000 from HDFC Bank, dated 10 May 2026.",
    args: {
      action_id: "post.vouchers.payment",
      path_params: {},
      body: {
        created_by: "agent",
        voucher_date: "2026-05-10",
        narration: "May office rent",
        postings: [
          { ledger_id: rent, dr_cr: "D", amount: "25000.00" },
          { ledger_id: hdfc, dr_cr: "C", amount: "25000.00" },
        ],
      },
    },
    preview: {
      status: 201,
      effect: {
        postings: [
          { ledger_id: rent, ledger: "Office Rent", dr_cr: "D", amount: "25000.0000", amount_paise: 2500000 },
          { ledger_id: hdfc, ledger: "HDFC Bank", dr_cr: "C", amount: "25000.0000", amount_paise: 2500000 },
        ],
        debit_paise: 2500000,
        credit_paise: 2500000,
        vouchers: 1,
      },
    },
    amount_paise: 2500000,
    reasons: ["autonomy for create payment is L1: every commit waits for a human"],
    provenance: { context_sources: ["user", "bank"], fields: { narration: "bank" } },
    on_behalf_of: "user-1",
    requested_at: "2026-05-10T09:00:00Z",
    deadline_at: opts.deadline ?? "2999-01-01T00:00:00Z",
  };
}

/** A pending SALES proposal — which the native GST form cannot restate. */
export function salesApproval(): AgentApproval {
  return {
    id: "appr-sale-1",
    run_id: RUN_ID,
    step_key: "gate:5",
    state: "pending",
    action_id: "post.vouchers.sales",
    acl_key: "create sales",
    risk: "money",
    instruction: "Invoice Acme Retail Rs 10,000.",
    args: {
      action_id: "post.vouchers.sales",
      body: {
        created_by: "agent",
        voucher_date: "2026-05-12",
        narration: "Sale to Acme Retail",
        party: "Acme Retail",
        postings: [
          { ledger_id: "ledger-acme", dr_cr: "D", amount: "10000.00" },
          { ledger_id: "ledger-sales", dr_cr: "C", amount: "10000.00" },
        ],
      },
    },
    preview: {
      status: 201,
      effect: {
        postings: [
          { ledger_id: "ledger-acme", ledger: "Acme Traders", dr_cr: "D", amount: "10000.0000", amount_paise: 1000000 },
          { ledger_id: "ledger-sales", ledger: "Sales", dr_cr: "C", amount: "10000.0000", amount_paise: 1000000 },
        ],
        debit_paise: 1000000,
        credit_paise: 1000000,
        vouchers: 1,
      },
    },
    amount_paise: 1000000,
    reasons: ["autonomy for create sales is L1: every commit waits for a human"],
    provenance: { context_sources: ["user"] },
    on_behalf_of: "user-1",
    requested_at: "2026-05-12T09:00:00Z",
    deadline_at: "2999-01-01T00:00:00Z",
  };
}

/** A proposal the server sent NO preview for — the inbox must refuse to approve it. */
export function blindApproval(): AgentApproval {
  return { ...paymentApproval({ id: "appr-blind-1", officeRentID: "ledger-rent" }), preview: null };
}

export function parkedRun(status: AgentRun["status"] = "parked"): AgentRun {
  return {
    id: RUN_ID,
    created_by: "user-1",
    agent_id: "khata-agent",
    channel: "panel",
    instruction: "Pay the May office rent of Rs 25,000 from HDFC Bank, dated 10 May 2026.",
    inputs: [],
    delegation: { acl_keys: ["create payment"], expires_at: "2999-01-01T00:00:00Z", step_up: false },
    status,
    attempts: 0,
    created_at: "2026-05-10T09:00:00Z",
    updated_at: "2026-05-10T09:00:00Z",
  };
}

export function parkedRunEvents(): AgentEvent[] {
  const base = {
    at: "2026-05-10T09:00:00Z",
    actor_id: "khata-agent",
    hash: "a".repeat(64),
  };
  return [
    { ...base, seq: 1, type: "RUN_STARTED", kind: "run_started", actor_kind: "user", data: { channel: "panel" } },
    { ...base, seq: 2, type: "STEP_FINISHED", name: "decide", kind: "decision_step", actor_kind: "agent", data: { k: 1, tool: "actions.preview" } },
    {
      ...base, seq: 3, type: "TOOL_CALL_RESULT", kind: "preview", actor_kind: "agent",
      action_id: "post.vouchers.payment",
      data: { k: 1, ok: true, status: 201, debit_paise: 2500000, credit_paise: 2500000 },
    },
    {
      ...base, seq: 4, type: "CUSTOM", name: "approval_requested", kind: "approval_requested", actor_kind: "system",
      action_id: "post.vouchers.payment",
      data: { approval_id: "appr-pay-1", amount_paise: 2500000, body_hash: "b".repeat(64) },
    },
  ];
}
