import {
  approvalEffect,
  type AgentApproval,
  type AgentProposalBody,
  type VoucherRequest,
  type VoucherView,
} from "../../api";
import type { ProposalEntryMode, ProposalKey } from "../../keystroke/proposalKey";
import type { VoucherType } from "../../keystroke/types";
import { configFor } from "./voucherConfig";
import { normalizeMoney } from "./money";

/**
 * AN AGENT PROPOSAL, AS THE NATIVE VOUCHER SCREEN SEES IT (W2).
 *
 * The agent never renders a voucher form of its own (PLAN-AGENTIC W2). When a
 * human wants to change what it proposed, the proposal is opened in the SAME
 * F4/F5/F6/F7 screen an operator keys by hand, and this module is the whole of
 * the translation in both directions:
 *
 *   approval  ──viewFromApproval──►  a VoucherView  ──formFromView (VoucherScreen)──►  the form
 *   the form  ──(the screen's ordinary builders)──►  a VoucherRequest ──sameBody──► edited?
 *
 * ⛔⛔ THE PREFILL GOES THROUGH `formFromView`, THE ALTER HYDRATION, AND THAT IS
 * THE LOAD-BEARING DECISION. That function is the product's one audited path
 * from "a set of postings" to "a form that restates them" — it has been fixed
 * seven times for fields an alter silently dropped. Writing a second, agent-only
 * hydration would restart that count at zero for the one caller whose input is
 * least trusted.
 *
 * ⛔ WHICH PROPOSALS CAN BE OPENED, AND WHY THE REST CANNOT. A proposal is
 * opened only where the native form restates its body LINE FOR LINE:
 *
 *   Payment / Receipt / Contra   single entry (one account on the class's own
 *                                side) or double entry (anything else)
 *   Journal                      double entry
 *
 * Sales, Purchase and the two notes are refused, on purpose. Their native form
 * is the GST invoice, which takes a TAXABLE VALUE and a RATE and DERIVES the
 * tax and party legs itself; the agent's body is raw postings. Prefilling one
 * into the other would post something the preview never showed — different
 * legs, different tax — under a screen that looks like the proposal. Tally's
 * own answer is "As Voucher" (raw Dr/Cr on an invoice class), and that mode is
 * not built here (voucherConfig's `entryModesFor` says so on Ctrl+H). So those
 * proposals are approved or rejected from the inbox's read-only diff, and
 * EDITING one is OWED (D121 Frontend §F.4).
 */

/** The four classes whose native form restates a postings body exactly. */
const PREFILLABLE: Readonly<Record<string, VoucherType>> = {
  "post.vouchers.payment": "payment",
  "post.vouchers.receipt": "receipt",
  "post.vouchers.contra": "contra",
  "post.vouchers.journal": "journal",
};

/** Invoice classes the agent can propose and the native form cannot restate. */
const INVOICE_CLASSES: Readonly<Record<string, string>> = {
  "post.vouchers.sales": "Sales",
  "post.vouchers.purchase": "Purchase",
  "post.vouchers.credit-note": "Credit Note",
  "post.vouchers.debit-note": "Debit Note",
};

/**
 * The body keys the prefilled form carries back. Anything else in a proposal —
 * bill references, cost splits, a GST block, stock lines, a deduction — is a
 * field the form would silently DROP on Ctrl+A, which is the alter-restates
 * defect class in its most dangerous form. Such a proposal is refused rather
 * than opened.
 */
const RESTATED_KEYS = new Set([
  "voucher_date",
  "narration",
  "party",
  "created_by",
  "postings",
]);
const RESTATED_POSTING_KEYS = new Set(["ledger_id", "dr_cr", "amount"]);

export type ProposalTarget =
  | { ok: true; key: ProposalKey }
  | { ok: false; reason: string };

function side(drCr: string): "D" | "C" {
  return drCr.trim().toUpperCase().startsWith("D") ? "D" : "C";
}

/**
 * Where (if anywhere) a proposal can be opened for editing, and in which mode.
 * A refusal carries the sentence the inbox and the panel show.
 */
export function proposalTarget(a: AgentApproval): ProposalTarget {
  if (a.state !== "pending") {
    return { ok: false, reason: `This proposal is ${a.state.replace("_", " ")}; there is nothing left to edit.` };
  }
  const type = PREFILLABLE[a.action_id];
  if (!type) {
    const invoice = INVOICE_CLASSES[a.action_id];
    return {
      ok: false,
      reason: invoice
        ? `A ${invoice} proposal cannot be opened in F-key form: that form is the GST invoice, which re-derives the tax from a taxable value and would post something other than the preview. "As Voucher" is not built. Approve or reject it from the diff here.`
        : `${a.action_id} is not a voucher this build can open for editing. Approve or reject it from the diff here.`,
    };
  }
  const effect = approvalEffect(a);
  if (!effect) {
    return { ok: false, reason: "The server sent no preview for this proposal, so there is nothing to show — and nothing that may be approved." };
  }
  const body = a.args?.body;
  if (!body || !Array.isArray(body.postings) || body.postings.length === 0) {
    return { ok: false, reason: "This proposal carries no postings to open." };
  }
  for (const k of Object.keys(body)) {
    if (!RESTATED_KEYS.has(k)) {
      return { ok: false, reason: `The proposal carries "${k}", which the ${configFor(type).title} form would not send back. Approve or reject it from the diff here.` };
    }
  }
  const names = new Map(effect.postings.map((p) => [p.ledger_id, p.ledger]));
  for (const p of body.postings) {
    for (const k of Object.keys(p)) {
      if (!RESTATED_POSTING_KEYS.has(k)) {
        return { ok: false, reason: `A posting carries "${k}", which the ${configFor(type).title} form would not send back.` };
      }
    }
    if (!names.get(p.ledger_id)) {
      return { ok: false, reason: `Posting ledger ${p.ledger_id} is not named in the server's preview, so the form could only show an id.` };
    }
  }
  return { ok: true, key: { voucherType: type, entryMode: entryModeFor(type, body), approvalID: a.id } };
}

/**
 * Single entry when the class has one and exactly one leg sits on its primary
 * side (the bank a payment is paid from); double entry otherwise, so that two
 * bank legs are never merged into one — the refusal `accountLegFromView` exists
 * to make.
 */
function entryModeFor(type: VoucherType, body: AgentProposalBody): ProposalEntryMode {
  const cfg = configFor(type);
  if (cfg.entry !== "single") return "double";
  const primary = cfg.primarySide === "DR" ? "D" : "C";
  const legs = (body.postings ?? []).filter((p) => side(p.dr_cr) === primary);
  return legs.length === 1 ? "single" : "double";
}

/**
 * The proposal as a VoucherView, for `formFromView`.
 *
 * ⛔ EVERY LEDGER NAME COMES FROM THE SERVER'S PREVIEW (`effect.postings`),
 * which read it back inside the handler's own transaction — never from the
 * body, which carries only ids, and never from anything the model wrote.
 * `proposalTarget` has already refused a body naming a ledger the preview does
 * not, so every lookup here succeeds.
 *
 * ⚠️ `voucher_no` is "" and `voucher_id` is "": this is a CREATE. The number is
 * the one the server allocates when the agent commits — the preview showed one,
 * rolled it back, and the real commit takes it again (D121 §5) — so no number
 * is shown that the books have not issued.
 */
export function viewFromApproval(a: AgentApproval, type: VoucherType): VoucherView {
  const body = a.args?.body ?? {};
  const names = new Map((approvalEffect(a)?.postings ?? []).map((p) => [p.ledger_id, p.ledger]));
  return {
    voucher_id: "",
    voucher_no: "",
    voucher_date: body.voucher_date ?? "",
    // `formFromView` resolves the class config from this, lower-cased.
    class: type,
    voucher_type_id: "",
    party: body.party ?? "",
    party_ledger_id: "",
    narration: body.narration ?? "",
    version_no: 0,
    op: "",
    place_of_supply: "",
    reverse_charge: false,
    taxable_value: "0.0000",
    cgst: "0.0000",
    sgst: "0.0000",
    igst: "0.0000",
    postings: (body.postings ?? []).map((p) => ({
      ledger_id: p.ledger_id,
      ledger: names.get(p.ledger_id) ?? "",
      dr_cr: side(p.dr_cr),
      amount: normalizeMoney(p.amount),
    })),
    bill_allocations: [],
    inventory: [],
    cost_allocations: [],
    gst_lines: [],
    tds: null,
    tcs: null,
  };
}

/** A body's money-bearing identity: date, narration and the multiset of postings. */
function signature(b: {
  voucher_date?: string;
  narration?: string;
  postings?: { ledger_id: string; dr_cr: string; amount: string }[];
}): string {
  const lines = (b.postings ?? [])
    .map((p) => `${p.ledger_id}|${side(p.dr_cr)}|${normalizeMoney(p.amount)}`)
    .sort();
  return JSON.stringify([b.voucher_date ?? "", (b.narration ?? "").trim(), lines]);
}

/** Request fields the native builders can add and a proposal never carries. */
const EXTRA_FIELDS: readonly (keyof VoucherRequest)[] = [
  "bill_allocations",
  "cost_allocations",
  "inventory",
  "gst",
  "tds",
  "tcs",
];

/**
 * Did the operator change the proposal? True when the body the native form
 * built differs from the proposed one in date, narration or any posting — or
 * carries anything a proposal does not (a bill reference, a cost split keyed on
 * the form).
 *
 * ⛔ THE COMPARISON DECIDES WHICH BODY IS COMMITTED, so it errs toward "edited".
 * A false "edited" commits the operator's own restatement of the same voucher
 * (the same postings, through the same validators); a false "unchanged" would
 * commit the AGENT's body over an edit the operator made and saw on screen.
 * `party` is deliberately not compared: single entry derives it from the
 * counterparty particular, and it carries no money.
 */
export function editedFrom(built: VoucherRequest, proposed: AgentProposalBody): boolean {
  for (const f of EXTRA_FIELDS) {
    const v = built[f];
    if (v === undefined || v === null) continue;
    if (Array.isArray(v) && v.length === 0) continue;
    return true;
  }
  return signature(built as Parameters<typeof signature>[0]) !== signature(proposed);
}
