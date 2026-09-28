/**
 * TS mirror of the insightd DRAFT contract — the human-approval boundary.
 *
 * insightd returns a {@link PurchaseVoucherDraft} and STOPS. `body` is a
 * ready-to-POST cored payload (mirrors VoucherRequest), but insightd NEVER
 * POSTs it. The frontend hydrates the F9 Purchase screen from `body` +
 * `confidence` (low-confidence fields amber, focus order from `unresolved`);
 * the operator presses Ctrl+A and the EXISTING cored voucher API submits, at
 * which point cored re-derives GST authoritatively and writes the immutable
 * ledger entry attributed to the human.
 *
 * These types are 1:1 with insightd/insightd/contracts/{draft.py,cored_map.py}.
 *
 * Money / quantities / rates are fixed-4 decimal STRINGS end to end ({@link Money}
 * from "./types") — never JS numbers, which silently lose paisa.
 */

import type { Money } from "./types";

/**
 * 'ready'              -> every field cleared its confidence floor; Ctrl+A is one keystroke.
 * 'low_confidence_manual' -> open the form blank-but-prefilled for full manual review.
 */
/**
 * "processing" is the ENQUEUE-AND-POLL state (WS10). Real OCR takes up to ~45 s
 * on a phone photo, which cannot sit in an HTTP request, so an upload returns
 * immediately in this state and the client polls until it leaves. With the
 * deterministic fakes the pipeline finishes inside the request and the draft is
 * already terminal on return — ONE response shape either way, so a client never
 * has to know which mode the server is in.
 */
/**
 * ⛔ `"accepted"` IS THE TERMINAL, ONCE-ONLY STATE AND ITS ABSENCE WAS A MONEY BUG.
 * Added 2026-08-07 to mirror `insightd/insightd/contracts/draft.py`'s `DraftStatus`
 * after a browser re-drive posted the SAME supplier bill twice: a successfully
 * accepted draft stayed `"ready"`, stayed in the F9 Bill Inbox, and could be
 * re-opened and accepted again. A draft whose invoice number the OCR never read has
 * no number and no date for the client's day-book duplicate check to match on, so
 * the reload case was wide open — two real vouchers for one bill.
 *
 * ⚠️ THIS TYPE IS A MIRROR, NOT A SOURCE. insightd owns the vocabulary; a value
 * missing here does not narrow what the server can send, it only hides it from the
 * type checker — which is exactly how this one went stale.
 */
export type DraftStatus =
  | "processing"
  | "ready"
  | "low_confidence_manual"
  | "accepted";

/** Where a draft field's value came from in the insightd pipeline. */
export type FieldSource =
  | "ocr"
  | "classifier"
  | "inferencer"
  | "derived"
  | "default";

/** Confidence for one draft field. */
export interface FieldConfidence {
  value_present: boolean;
  /** 0.0 .. 1.0 */
  confidence: number;
  source: FieldSource;
}

/**
 * W5 — one deterministic check's verdict on the draft (insightd/validators).
 * `fail` lowered the named field to unresolved; `pass` confirms and NEVER
 * raises; `unverified` ran against incomplete reference data (an HSN this
 * company has not used, with no directory loaded); `not_run` had nothing to
 * check. `detail` is counts and arithmetic, never a raw identifier.
 */
export interface DraftValidation {
  check: string;
  outcome: "pass" | "fail" | "unverified" | "not_run";
  /** The same field token `unresolved` uses: "supplier", "totals", "line[0].hsn", "duplicate". */
  field: string;
  detail: string;
  /** duplicate_bill only: the other drafts this one duplicates. */
  related_draft_ids: string[];
}

/**
 * W5 — WHERE ON THE DOCUMENT a draft field was found. `grounded` is true only
 * when insightd found the value in the OCR text itself; a `source: "model"`
 * location is the model's claim and is not trusted. `value` is the persisted
 * draft value, never OCR text. `bbox` is `[x0, y0, x1, y1]` in the page's pixel
 * space; `page` is 0-based.
 */
export interface FieldProvenance {
  value: string | null;
  page: number | null;
  bbox: number[] | null;
  confidence: number;
  grounded: boolean;
  source: "ocr" | "model" | "none";
}

/**
 * Per-field confidence, bucketed header vs line-items, with rolled-up overalls.
 * `overall = min(overall_header, overall_line_items)`.
 */
export interface ConfidenceMap {
  /** header keys: invoice_no, invoice_date, supplier, place_of_supply, reverse_charge, totals */
  header: Record<string, FieldConfidence>;
  /** per line: description, hsn, qty, rate, taxable_value, gst_rate_pct, ledger */
  line_items: Array<Record<string, FieldConfidence>>;
  overall_header: number;
  overall_line_items: number;
  overall: number;
}

/** Counts only — proves PII was scrubbed; carries NO raw values. */
export interface RedactionReportSummary {
  any_redacted: boolean;
  counts_by_type: Record<string, number>;
  total: number;
}

/**
 * cgst/sgst/igst RE-derived by insightd against cored's ruleset for DISPLAY
 * ONLY. The human's POST re-derives server-side authoritatively; these are
 * never trusted as the source of truth.
 */
export interface GSTAmounts {
  cgst: Money;
  sgst: Money;
  igst: Money;
  total_tax: Money;
  invoice_total: Money;
}

/**
 * One leg of the double-entry voucher (mirrors PostingRequest). `ledger_id` is
 * null when the ledger is a *proposed* new master (Alt+C on-the-fly create);
 * `ledger_name` then carries the human-facing name to create.
 */
export interface PostingDraft {
  ledger_id: string | null;
  ledger_name: string;
  dr_cr: "D" | "C";
  amount: Money;
}

/**
 * One GST line (mirrors GSTLineRequest). The LLM supplies taxable_value + rate
 * + hsn ONLY; cored re-derives the tax server-side.
 */
export interface GSTLineDraft {
  taxable_value: Money;
  gst_rate_pct: Money;
  hsn?: string | null;
}

/** The GST block (mirrors GSTRequest). */
export interface GSTDraft {
  ruleset_version: string;
  supplier_state_code?: string | null;
  place_of_supply_state_code?: string | null;
  reverse_charge: boolean;
  exempt: boolean;
  lines: GSTLineDraft[];
  cgst_ledger_id?: string | null;
  sgst_ledger_id?: string | null;
  igst_ledger_id?: string | null;
}

/**
 * Exactly what a human would POST to /companies/{id}/vouchers/purchase
 * (mirrors VoucherRequest, class fixed = purchase).
 */
export interface VoucherDraftBody {
  voucher_class: "purchase";
  voucher_type_id?: string | null;
  party_ledger_id?: string | null;
  /** supplier display name (for Alt+C if party_ledger_id is null) */
  party?: string | null;
  /** supplier's invoice no (Purchase: supplier ref, not auto-numbered) */
  voucher_no?: string | null;
  voucher_date?: string | null;
  narration?: string | null;
  postings: PostingDraft[];
  gst?: GSTDraft | null;
}

/** The proposal. Returned by insightd; never POSTed by insightd. */
export interface PurchaseVoucherDraft {
  draft_id: string;
  /** tenant scope — drafts are per-company like every cored write */
  company_id: string;
  created_at: string;
  source: "upload" | "whatsapp" | "email";
  status: DraftStatus;
  /** ready-to-POST payload */
  body: VoucherDraftBody;
  confidence: ConfidenceMap;
  /** counts only; proves PII scrubbed */
  redaction: RedactionReportSummary;
  /** fields needing human attention (drives F9 focus order) */
  unresolved: string[];
  /** display-only re-derivation */
  derived_gst?: GSTAmounts | null;
  /**
   * W5 — the evidence behind the numbers. Optional because drafts stored
   * before W5 (and the mock) carry neither; absent means "no evidence
   * recorded", never "every check passed".
   */
  validations?: DraftValidation[];
  /** Field path ("invoice_no", "totals", "line[0].taxable_value") → location. */
  provenance?: Record<string, FieldProvenance>;
  /**
   * The acceptance link, set together with `status: "accepted"` — see
   * {@link DraftStatus}. All three are null/absent for every non-accepted draft.
   * ⚠️ `accepted_voucher_no` may legitimately be `""`: cored allocates the number,
   * and an empty string means "posted, number not echoed", NOT "not posted" — the
   * id is the load-bearing field.
   */
  accepted_voucher_id?: string | null;
  accepted_voucher_no?: string | null;
  /** ISO-8601 UTC, when the acceptance was recorded. */
  accepted_at?: string | null;

  /**
   * ⛔ THE POSTING CLAIM — A SHORT LEASE TAKEN *BEFORE* THE VOUCHER POST, and the
   * fifth door closed on one duplicated supplier bill. Everything above records an
   * acceptance AFTER the fact, and four layers all asked the same question ("has this
   * draft been accepted?") at the same wrong MOMENT — before either racer had posted,
   * when the honest answer was still "no". Two separate browsers were raced on one
   * numberless draft and both posted (vouchers 11 and 12, 27000.0000 + 4860.0000 IGST
   * each); only the second MARKING was refused, after the money had moved. So a poster
   * now RESERVES first, posts second, confirms third.
   *
   * ⚠️ A CLAIM IS A LEASE, NOT A STATUS. `claim_expires_at` in the past means NO live
   * claim, and a claimed draft deliberately stays `"ready"` — the bill really is still
   * unposted and must stay postable, because a permanent claim would be a bill nobody
   * can ever post (worse than the duplicate). All four are null when unclaimed.
   * `claim_token` is a concurrency value and NOT a credential: it authorises nothing
   * beyond releasing or renewing the claim it already holds.
   * Mirrors `insightd/insightd/contracts/draft.py`.
   */
  claim_token?: string | null;
  /** Display-only label ("who/what is posting it"). Never trusted. */
  claimed_by?: string | null;
  claimed_at?: string | null;
  claim_expires_at?: string | null;
}

/**
 * A SLIM, inbox-facing projection of a {@link PurchaseVoucherDraft}. Mirrors the
 * insightd `DraftSummary` response model field-for-field (the LIST route). All
 * fields are DERIVED server-side from the full draft — there is no top-level
 * party/total/confidence on the draft itself:
 *  - party        <- body.party (may be None server-side) || "" fallback.
 *  - grand_total  <- derived_gst.invoice_total when derived_gst is present, else
 *                    "0.0000" (derived_gst is Optional). MUST stay a Money STRING;
 *                    "0.0000" means "unknown total", not a real zero, for a
 *                    low_confidence_manual draft that never derived GST.
 *  - confidence   <- confidence.overall (a float / number).
 * draft_id / company_id / status / source / created_at map directly.
 */
export interface DraftSummary {
  draft_id: string;
  company_id: string;
  created_at: string;
  source: "upload" | "whatsapp" | "email";
  status: DraftStatus;
  /** DERIVED: body.party or "" (supplier display name; may be empty). */
  party: string;
  /**
   * DERIVED: derived_gst.invoice_total when present, else "0.0000" (Money string).
   * "0.0000" should be shown as "unknown total", not a real zero.
   */
  grand_total: Money;
  /** DERIVED: confidence.overall (0.0 .. 1.0). */
  confidence: number;
  /**
   * Set together with `status: "accepted"`, so the inbox can render
   * **"already posted as Purchase 3"** rather than silently dropping the row —
   * an operator who cannot see what happened to a bill re-does it, which is the
   * duplicate this lifecycle exists to stop. Null for every unaccepted draft.
   */
  accepted_voucher_no?: string | null;
  accepted_voucher_id?: string | null;
}

/**
 * Request body for createDraft (the e2e / fixture path). `tenant` and `fixture`
 * are opaque to the frontend — they pass through to insightd verbatim.
 */
export interface CreateDraftRequest {
  source?: string;
  content_b64?: string | null;
  content_type?: string;
  filename?: string;
  tenant: Record<string, unknown>;
  fixture?: Record<string, unknown>;
}
