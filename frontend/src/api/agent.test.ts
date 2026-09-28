import { describe, expect, it } from "vitest";

import { createRestClient } from "./rest";
import type { HttpClient } from "./http";
import type { VoucherRequest } from "./types";
import {
  approvalEffect,
  describeAgentEvent,
  formatPaise,
  type AgentEvent,
} from "./agent";
import {
  editedFrom,
  proposalTarget,
  viewFromApproval,
} from "../screens/vouchers/agentProposal";
import {
  OPEN_AGENT_PROPOSAL_PREFIX,
  PROPOSAL_ROW_PREFIX,
  openProposalSentinel,
  parseProposalKey,
  proposalRowKey,
} from "../keystroke/proposalKey";
import {
  blindApproval,
  paymentApproval,
  salesApproval,
} from "../screens/agentFixtures";

/**
 * THE AGENT PLANE's pure half (W2): the eight wire calls, and the translation
 * between an approval and the native voucher form. Every screen test in this
 * lane stands on these, so each has a CONTROL beside it — a helper that refuses
 * nothing, or maps names from the body, would pass half of these and fail the
 * other half.
 */

function recorder() {
  const calls: { verb: string; path: string; body?: unknown; query?: unknown }[] = [];
  const rec =
    (verb: string) =>
    (path: string, a?: unknown, b?: unknown) => {
      if (verb === "GET") calls.push({ verb, path, query: a });
      else calls.push({ verb, path, body: a, query: b });
      return Promise.resolve({} as never);
    };
  const http = {
    get: rec("GET"),
    post: rec("POST"),
    put: rec("PUT"),
    del: rec("DELETE"),
    postRaw: rec("POST"),
    getBlob: rec("GET"),
  } as unknown as HttpClient;
  return { http, calls };
}

describe("the agent client's eight routes (D121 §8)", () => {
  it("each method hits exactly the route the contract names", async () => {
    const { http, calls } = recorder();
    const c = createRestClient({ http });
    await c.startAgentRun("co", { instruction: "x" });
    await c.listAgentRuns("co", 20);
    await c.getAgentRun("co", "r/1");
    await c.agentRunEvents("co", "r1", 7);
    await c.cancelAgentRun("co", "r1");
    await c.listAgentApprovals("co", { state: "pending" });
    await c.getAgentApproval("co", "a1");
    await c.decideAgentApproval("co", "a1", { decision: "reject" });
    expect(calls.map((x) => `${x.verb} ${x.path}`)).toEqual([
      "POST /v1/companies/co/agent/runs",
      "GET /v1/companies/co/agent/runs",
      // ⚠️ An id is a path SEGMENT and is encoded: "r/1" must not become a
      // second segment and reach a different route.
      "GET /v1/companies/co/agent/runs/r%2F1",
      "GET /v1/companies/co/agent/runs/r1/events",
      "POST /v1/companies/co/agent/runs/r1/cancel",
      "GET /v1/companies/co/agent/approvals",
      "GET /v1/companies/co/agent/approvals/a1",
      "POST /v1/companies/co/agent/approvals/a1/decision",
    ]);
    expect(calls[0]?.body).toEqual({ channel: "panel", instruction: "x" });
    expect(calls[1]?.query).toEqual({ limit: 20 });
    expect(calls[3]?.query).toEqual({ after: 7 });
    expect(calls[5]?.query).toEqual({ state: "pending", run_id: undefined });
  });

  it("an EDITED body crosses the D/C boundary every voucher post does", async () => {
    const { http, calls } = recorder();
    const c = createRestClient({ http });
    await c.decideAgentApproval("co", "a1", {
      decision: "approve",
      edited_body: {
        voucher_date: "2026-05-10",
        postings: [
          { ledger_id: "l1", dr_cr: "DR", amount: "1.0000" },
          { ledger_id: "l2", dr_cr: "CR", amount: "1.0000" },
        ],
      },
    });
    const sent = calls[0]?.body as { edited_body: { postings: { dr_cr: string }[] } };
    expect(sent.edited_body.postings.map((p) => p.dr_cr)).toEqual(["D", "C"]);
    // CONTROL: a decision with no edit is sent untouched.
    await c.decideAgentApproval("co", "a1", { decision: "approve" });
    expect(calls[1]?.body).toEqual({ decision: "approve" });
  });
});

describe("display helpers", () => {
  it("formats paise exactly, including the negative and the missing", () => {
    expect(formatPaise(2500000)).toBe("25000.00");
    expect(formatPaise(5)).toBe("0.05");
    expect(formatPaise(-118000)).toBe("-1180.00");
    expect(formatPaise(undefined)).toBe("—");
  });

  it("the effect is the SERVER's preview, and a missing one is null — never the body", () => {
    expect(approvalEffect(paymentApproval())?.postings).toHaveLength(2);
    expect(approvalEffect(blindApproval())).toBeNull();
    expect(
      approvalEffect({
        ...paymentApproval(),
        preview: { status: 201, effect: { postings: [], debit_paise: 0, credit_paise: 0, vouchers: 0 } },
      }),
    ).toBeNull();
  });

  it("describes an event from its documented data and never prints a hash", () => {
    const ev: AgentEvent = {
      seq: 3, type: "TOOL_CALL_RESULT", kind: "preview", at: "", actor_kind: "agent",
      actor_id: "a", action_id: "post.vouchers.payment", hash: "f".repeat(64),
      data: { ok: true, status: 201, debit_paise: 2500000, credit_paise: 2500000, body_hash: "e".repeat(64) },
    };
    const text = describeAgentEvent(ev);
    expect(text).toBe("Previewed post.vouchers.payment: Dr 25000.00 / Cr 25000.00");
    expect(text).not.toContain("eeee");
    expect(describeAgentEvent({ ...ev, kind: "something_new", data: null })).toBe("something_new");
  });
});

describe("proposal keys (keystroke/proposalKey.ts)", () => {
  const k = { voucherType: "payment" as const, entryMode: "single" as const, approvalID: "a1" };
  it("round-trips both spellings, and refuses the other's prefix", () => {
    expect(parseProposalKey(proposalRowKey(k), PROPOSAL_ROW_PREFIX)).toEqual(k);
    expect(parseProposalKey(openProposalSentinel(k), OPEN_AGENT_PROPOSAL_PREFIX)).toEqual(k);
    expect(parseProposalKey(proposalRowKey(k), OPEN_AGENT_PROPOSAL_PREFIX)).toBeUndefined();
  });
  it("refuses an invoice mode, an unknown class and an empty id", () => {
    expect(parseProposalKey("proposal:sales:accounting-invoice:a1", PROPOSAL_ROW_PREFIX)).toBeUndefined();
    expect(parseProposalKey("proposal:banana:single:a1", PROPOSAL_ROW_PREFIX)).toBeUndefined();
    expect(parseProposalKey("proposal:payment:single:", PROPOSAL_ROW_PREFIX)).toBeUndefined();
  });
});

describe("which proposals the native form can restate (agentProposal.ts)", () => {
  it("a payment with one bank leg opens in SINGLE entry", () => {
    const t = proposalTarget(paymentApproval());
    expect(t).toEqual({ ok: true, key: { voucherType: "payment", entryMode: "single", approvalID: "appr-pay-1" } });
  });

  it("a payment split across TWO bank legs opens in DOUBLE entry, never merged", () => {
    const a = paymentApproval();
    a.args!.body!.postings = [
      { ledger_id: "ledger-rent", dr_cr: "D", amount: "25000.00" },
      { ledger_id: "ledger-hdfc", dr_cr: "C", amount: "20000.00" },
      { ledger_id: "ledger-cash", dr_cr: "C", amount: "5000.00" },
    ];
    a.preview!.effect!.postings.push({ ledger_id: "ledger-cash", ledger: "Cash", dr_cr: "C", amount: "5000.0000", amount_paise: 500000 });
    const t = proposalTarget(a);
    expect(t.ok && t.key.entryMode).toBe("double");
  });

  it("a journal is always double entry", () => {
    const a = { ...paymentApproval(), action_id: "post.vouchers.journal" };
    const t = proposalTarget(a);
    expect(t.ok && t.key).toEqual({ voucherType: "journal", entryMode: "double", approvalID: "appr-pay-1" });
  });

  it("⛔ a SALES proposal is refused, and the reason names the missing mode", () => {
    const t = proposalTarget(salesApproval());
    expect(t.ok).toBe(false);
    expect(!t.ok && t.reason).toMatch(/As Voucher" is not built/);
  });

  it("⛔ a body field the form would DROP refuses the prefill", () => {
    const a = paymentApproval();
    (a.args!.body as Record<string, unknown>).bill_allocations = [{ ref_name: "INV-1" }];
    const t = proposalTarget(a);
    expect(!t.ok && t.reason).toMatch(/"bill_allocations"/);
  });

  it("⛔ a ledger the preview does not name refuses the prefill", () => {
    const a = paymentApproval();
    a.preview!.effect!.postings = a.preview!.effect!.postings.slice(0, 1);
    expect(proposalTarget(a).ok).toBe(false);
  });

  it("no preview, no prefill — and a decided proposal is not re-opened", () => {
    expect(proposalTarget(blindApproval()).ok).toBe(false);
    expect(proposalTarget({ ...paymentApproval(), state: "approved" }).ok).toBe(false);
  });
});

describe("approval → form → body", () => {
  it("names every ledger from the SERVER'S PREVIEW, not from anything the model wrote", () => {
    const a = paymentApproval();
    // The body carries a misleading party label; the view must not use it for a
    // ledger name, and the ledger names must be the preview's.
    a.args!.body!.party = "Totally Legit Vendor";
    const v = viewFromApproval(a, "payment");
    expect(v.postings.map((p) => [p.ledger, p.dr_cr, p.amount])).toEqual([
      ["Office Rent", "D", "25000.0000"],
      ["HDFC Bank", "C", "25000.0000"],
    ]);
    expect(v.voucher_no).toBe("");
    expect(v.voucher_id).toBe("");
  });

  it("the same voucher in the form's DR/CR spelling is NOT an edit", () => {
    const proposed = paymentApproval().args!.body!;
    expect(
      editedFrom(
        {
          voucher_date: "2026-05-10",
          narration: "May office rent",
          party: "Office Rent",
          postings: [
            { ledger_id: "ledger-hdfc", dr_cr: "CR", amount: "25000.0000" },
            { ledger_id: "ledger-rent", dr_cr: "DR", amount: "25000.0000" },
          ],
        },
        proposed,
      ),
    ).toBe(false);
  });

  it("a changed amount, date, narration or ledger IS an edit", () => {
    const proposed = paymentApproval().args!.body!;
    const base: VoucherRequest = {
      voucher_date: "2026-05-10",
      narration: "May office rent",
      postings: [
        { ledger_id: "ledger-hdfc", dr_cr: "CR", amount: "25000.0000" },
        { ledger_id: "ledger-rent", dr_cr: "DR", amount: "25000.0000" },
      ],
    };
    expect(editedFrom({ ...base, voucher_date: "2026-05-11" }, proposed)).toBe(true);
    expect(editedFrom({ ...base, narration: "June rent" }, proposed)).toBe(true);
    expect(
      editedFrom(
        { ...base, postings: [base.postings![0]!, { ...base.postings![1]!, amount: "24000.0000" }] },
        proposed,
      ),
    ).toBe(true);
    expect(
      editedFrom(
        { ...base, postings: [base.postings![0]!, { ...base.postings![1]!, ledger_id: "ledger-cash" }] },
        proposed,
      ),
    ).toBe(true);
  });

  it("⛔ anything the form ADDED (a bill reference, a cost split) is an edit", () => {
    const proposed = paymentApproval().args!.body!;
    const same: VoucherRequest = {
      voucher_date: "2026-05-10",
      narration: "May office rent",
      postings: [
        { ledger_id: "ledger-hdfc", dr_cr: "CR", amount: "25000.0000" },
        { ledger_id: "ledger-rent", dr_cr: "DR", amount: "25000.0000" },
      ],
    };
    expect(editedFrom({ ...same, bill_allocations: [] }, proposed)).toBe(false);
    expect(
      editedFrom(
        { ...same, bill_allocations: [{ ledger_id: "ledger-rent" } as never] },
        proposed,
      ),
    ).toBe(true);
  });
});
