/**
 * Thin client for the `insightd` OCR-to-draft surface.
 *
 * insightd returns a {@link PurchaseVoucherDraft} (the human-approval boundary)
 * and never writes a ledger entry itself. This client only READS a draft (or
 * creates one from a fixture/upload in the e2e path); the ONLY write path is the
 * existing cored voucher API, invoked on human accept (see ./../screens/vouchers/draftAccept).
 *
 * ⛔⛔ THESE ARE CORED ROUTES NOW (register row D49, closed 2026-08-24). They used
 * to be insightd's own, on :8088, reached SAME-ORIGIN through a vite rule that
 * routed every `/companies/*` to a service which authenticated nobody and scoped
 * drafts by the company_id in the path — so any caller who could name a tenant's
 * uuid could hydrate its supplier bills in full. cored is the only door now:
 * `/v1/companies/{id}/drafts...` goes through the same verified JWT, membership
 * check and PDP as the day book, and cored forwards to insightd with the company
 * its PDP authorized.
 *
 * ⚠️ SO THIS CLIENT NEEDS NO BASE URL AND NO CREDENTIAL OF ITS OWN, and that is
 * the point rather than a convenience: `createHttpClient()` resolves cored's base
 * (same-origin in live mode) and picks up the SESSION transport installed by the
 * auth gate, so the bearer arrives without any caller passing one.
 * ⚠️ `VITE_INSIGHTD_BASE_URL` is gone with it. Nothing in the browser addresses
 * insightd any more; if you find something reading that variable, it is talking
 * to a process the operator's credentials do not reach.
 *
 * Money stays string end to end — this client does not parse any fields.
 */

import { createHttpClient, type HttpClient } from "./http";
import type {
  CreateDraftRequest,
  DraftSummary,
  PurchaseVoucherDraft,
} from "./draft";

export interface InsightdClient {
  /** GET an existing draft by id (the F9 prefill path). */
  getDraft(companyID: string, draftID: string): Promise<PurchaseVoucherDraft>;
  /** Create a draft from an upload / fixture (the e2e path). May return 201. */
  createDraft(
    companyID: string,
    req: CreateDraftRequest,
  ): Promise<PurchaseVoucherDraft>;
  /**
   * LIST the pending drafts for a company (the Bill Inbox path). Returns a slim
   * {@link DraftSummary} per draft; an empty list for an unknown company.
   */
  listDrafts(companyID: string): Promise<DraftSummary[]>;
}

/**
 * The ONE place a draft URL is built on this side (its sibling is `draftPath` in
 * screens/vouchers/draftAccept.ts, which builds the three lifecycle POSTs and
 * carries the same note). Under cored, so `/v1` and the company segment cored's
 * router already validates.
 */
function draftsPath(companyID: string): string {
  return `/v1/companies/${encodeURIComponent(companyID)}/drafts`;
}

/**
 * Construct an {@link InsightdClient}. Pass `{ http }` to inject a configured
 * HttpClient (tests / shared transport), or `{ baseUrl }` to build one; with no
 * args the base URL is cored's (same-origin in live mode) and the transport is
 * the session one the auth gate installed.
 */
export function createInsightdClient(
  opts?: { baseUrl?: string } | { http: HttpClient },
): InsightdClient {
  const http: HttpClient =
    opts && "http" in opts
      ? opts.http
      : createHttpClient(
          // A caller-supplied baseUrl still wins (the node scripts pass cored's).
          // With none, createHttpClient resolves VITE_CORED_BASE_URL / same-origin.
          (opts as { baseUrl?: string })?.baseUrl
            ? { baseUrl: (opts as { baseUrl?: string }).baseUrl }
            : {},
        );

  return {
    getDraft(companyID, draftID) {
      return http.get<PurchaseVoucherDraft>(
        `${draftsPath(companyID)}/${encodeURIComponent(draftID)}`,
      );
    },
    createDraft(companyID, req) {
      return http.post<PurchaseVoucherDraft>(draftsPath(companyID), req);
    },
    listDrafts(companyID) {
      return http.get<DraftSummary[]>(draftsPath(companyID));
    },
  };
}
