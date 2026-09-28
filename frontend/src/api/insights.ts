/**
 * THE INSIGHT ENGINE's wire contract (W4, d125). Mirrors
 * backend/internal/server/insights_http.go and internal/insights' JSON tags.
 *
 *  - **Every figure is a server string, rendered verbatim.** `payload` values
 *    are the detector's paisa-exact numbers ("59384.00"); nothing on this side
 *    adds, rounds or reformats them — the evidence gate proved the SERVER's
 *    string, not a number this screen recomputed.
 *  - **`summary` is the deterministic template**, never model text. The
 *    digest's `narrative` may be model text, and says so in `narrator`.
 *  - **Nothing here writes to the books.** A verdict, a suppression or a digest
 *    is a row in the insight tables; acting on an insight is done in the
 *    screen it names (the voucher, the e-way bill, the TDS challan).
 */

export type InsightUnit = "inr" | "pct" | "qty" | "days" | "count" | "date" | "text";
export type InsightState = "open" | "dismissed" | "superseded" | "expired";
export type InsightVerdict = "useful" | "not_useful" | "wrong";

export interface InsightField {
  key: string;
  label: string;
  unit: InsightUnit;
  value: string;
}

export interface Insight {
  id: string;
  type: string;
  title: string;
  action: string;
  cadence: "realtime" | "nightly";
  entity_kind: string;
  entity_key: string;
  entity_name: string;
  fingerprint: string;
  as_of: string;
  detected_at: string;
  impact_inr: string;
  actionability: string;
  payload: Record<string, string>;
  fields: InsightField[];
  summary: string;
  evidence_sql: string;
  evidence_args: string[];
  voucher_ids: string[];
  trigger_voucher_id: string | null;
  state: InsightState;
  verdicts: Partial<Record<InsightVerdict, number>>;
}

export interface InsightTypeInfo {
  code: string;
  title: string;
  cadence: string;
}

export interface InsightsListResponse {
  insights: Insight[];
  types: InsightTypeInfo[];
  /** Types the plan names that this build does NOT detect, with the reason. */
  owed: { type: string; reason: string }[];
}

export interface InsightEvidenceResponse {
  reproduces: boolean;
  detail?: string;
}

export interface DigestItem {
  insight_id: string;
  type: string;
  title: string;
  fingerprint: string;
  entity_name: string;
  rank: number;
  score: number;
  impact_inr: string;
  p_real: number;
  actionability: number;
  novelty: number;
  affinity: number;
  payload: Record<string, string>;
  voucher_ids: string[];
  text: string;
}

export interface InsightDigest {
  id: string;
  digest_date: string;
  recipient: string;
  items: DigestItem[];
  narrative: string;
  narrator: "model" | "template";
  model_id: string;
  prompt_version: string;
  narration_rejected: string;
}

export interface InsightSuppression {
  id: string;
  type: string;
  entity_key: string;
  until: string | null;
  reason: string;
  created_by: string;
  created_at: string;
}

export interface SuppressInsightRequest {
  type: string;
  entity_key?: string;
  /** 0 = until lifted. */
  days?: number;
  reason: string;
}

export interface InsightRunResponse {
  events: { events: number; skipped: number; raised: Record<string, number> };
  nightly: {
    as_of: string;
    already_ran?: boolean;
    profile_facts: number;
    by_type: Record<string, { inserted: number; unchanged: number; superseded: number; refused: number }>;
    evidence_refused?: string[];
    expired: number;
  };
}
