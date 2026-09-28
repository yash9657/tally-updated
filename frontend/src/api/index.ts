/**
 * Public API surface for the `cored` client. Consumers import from "@/api"
 * (i.e. ./api) and depend only on the {@link CoredClient} interface plus the
 * request/response types — never on a concrete implementation.
 */
export type { CoredClient, RestoreResult } from "./client";
export { createRestClient } from "./rest";
export { createMockClient } from "./mock";
export type { MockClientOptions } from "./mock";
export {
  createHttpClient,
  // The authenticated session transport (D49). Installed once by the auth gate;
  // every client built without an explicit fetchImpl inherits it.
  setSessionFetch,
  currentSessionFetch,
  ApiError,
  // ⛔ The transport's "the books never saw this" signal — the ONE error the
  // offline outbox is allowed to queue on. See http.ts.
  NetworkError,
  type HttpClient,
  type HttpClientOptions,
} from "./http";
export * from "./types";

// "Ask your books" conversational-query contract (the typed DSL + the
// /query request/response envelope), mirroring the backend WS1/WS2 shapes.
export type {
  QueryEntity,
  AggFunc,
  FilterOp,
  QueryMetric,
  QueryFilter,
  DateRange,
  OrderTerm,
  Query,
  QueryRequest,
  ColumnMeta,
  QueryResult,
} from "./query";

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

// SaaS admin contract (members, invites, security levels, /v1/me).
export type {
  MeCompany,
  ClaimedInvite,
  MeResponse,
  MemberView,
  InviteView,
  MembersResponse,
  InviteMemberRequest,
  InviteMemberResponse,
  SecurityLevelRule,
  SecurityLevelView,
  SecurityLevelsResponse,
  SecurityVocabulary,
  SecurityLevelRequest,
  CreateSecurityLevelResponse,
  UpdateSecurityLevelResponse,
  DeleteSecurityLevelResponse,
  UpdateMemberRequest,
  UpdateMemberResponse,
  RemoveMemberResponse,
  RevokeInviteResponse,
} from "./admin";
export {
  draftBodyToVoucherRequest,
  resolveDraftLedgers,
  acceptPurchaseDraft,
} from "../screens/vouchers/draftAccept";

// The AGENT PLANE (W2, D121 §8): runs, the AG-UI-shaped event stream and the
// approval inbox. Types plus the pure display helpers the panel and inbox share.
export type {
  AgentProvenanceSource,
  AgentInput,
  AgentDelegation,
  AgentRunStatus,
  AgentRun,
  AgentEvent,
  AgentEventsResponse,
  AgentPosting,
  AgentEffect,
  AgentProposalBody,
  AgentApprovalState,
  AgentDecisionView,
  AgentApproval,
  StartAgentRunRequest,
  AgentDecisionRequest,
  AgentRunResponse,
  AgentRunsResponse,
  AgentApprovalsResponse,
  AgentApprovalResponse,
  AgentVersionWritten,
  AgentVersionRef,
  AgentLedgerDelta,
  AgentStockMove,
  AgentMasterChange,
  AgentOrderChange,
  AgentBankDateChange,
  AgentEditPreview,
  AgentEditPreviewResponse,
  AgentAutonomyRow,
  AgentAutonomyResponse,
  SetAgentAutonomyRequest,
  AgentVerifyRunResponse,
  AgentVerifyAnchorsResponse,
  UnblockLedgerResponse,
} from "./agent";
export {
  AGENT_UNCONFIGURED,
  approvalEffect,
  effectOrNull,
  describeAgentEvent,
  formatPaise,
  runFinished,
} from "./agent";
