/**
 * Public API surface for the `cored` client. Consumers import from "@/api"
 * (i.e. ./api) and depend only on the {@link CoredClient} interface plus the
 * request/response types — never on a concrete implementation.
 */
export type { CoredClient } from "./client";
export { createRestClient } from "./rest";
export { createMockClient } from "./mock";
export type { MockClientOptions } from "./mock";
export {
  createHttpClient,
  ApiError,
  type HttpClient,
  type HttpClientOptions,
} from "./http";
export * from "./types";

// insightd DRAFT contract + client (the OCR-to-draft surface) and the
// human-accept conversion helpers (draft body -> existing cored write path).
export type {
  DraftStatus,
  FieldSource,
  FieldConfidence,
  ConfidenceMap,
  RedactionReportSummary,
  GSTAmounts,
  PostingDraft,
  GSTLineDraft,
  GSTDraft,
  VoucherDraftBody,
  PurchaseVoucherDraft,
  DraftSummary,
  CreateDraftRequest,
} from "./draft";
export { createInsightdClient, type InsightdClient } from "./insightd";
export {
  draftBodyToVoucherRequest,
  resolveDraftLedgers,
  acceptPurchaseDraft,
} from "../screens/vouchers/draftAccept";
