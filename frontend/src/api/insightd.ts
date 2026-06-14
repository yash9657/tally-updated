/**
 * Thin client for the `insightd` OCR-to-draft surface.
 *
 * insightd returns a {@link PurchaseVoucherDraft} (the human-approval boundary)
 * and never writes a ledger entry itself. This client only READS a draft (or
 * creates one from a fixture/upload in the e2e path); the ONLY write path is the
 * existing cored voucher API, invoked on human accept (see ./../screens/vouchers/draftAccept).
 *
 * insightd routes are NOT under /v1 (cored is). The dev server listens on 8088.
 * Money stays string end to end — this client does not parse any fields.
 */

import { createHttpClient, type HttpClient } from "./http";
import type { CreateDraftRequest, PurchaseVoucherDraft } from "./draft";

export interface InsightdClient {
  /** GET an existing draft by id (the F9 prefill path). */
  getDraft(companyID: string, draftID: string): Promise<PurchaseVoucherDraft>;
  /** Create a draft from an upload / fixture (the e2e path). May return 201. */
  createDraft(
    companyID: string,
    req: CreateDraftRequest,
  ): Promise<PurchaseVoucherDraft>;
}

/**
 * Read the configured insightd base URL from the Vite env, falling back to the
 * dev port 8088. Guarded for non-Vite (node test) runs exactly like http.ts's
 * envBaseUrl — `import.meta.env` is undefined under bare node.
 */
function insightdEnvBaseUrl(): string {
  const env = (import.meta as unknown as { env?: Record<string, string> }).env;
  return env?.VITE_INSIGHTD_BASE_URL ?? "http://localhost:8088";
}

/**
 * Construct an {@link InsightdClient}. Pass `{ http }` to inject a configured
 * HttpClient (tests / shared transport), or `{ baseUrl }` to build one; with no
 * args the base URL is resolved from the Vite env (dev 8088 fallback).
 */
export function createInsightdClient(
  opts?: { baseUrl?: string } | { http: HttpClient },
): InsightdClient {
  const http: HttpClient =
    opts && "http" in opts
      ? opts.http
      : createHttpClient({
          baseUrl: (opts as { baseUrl?: string })?.baseUrl ?? insightdEnvBaseUrl(),
        });

  return {
    getDraft(companyID, draftID) {
      return http.get<PurchaseVoucherDraft>(
        `/companies/${encodeURIComponent(companyID)}/drafts/${encodeURIComponent(draftID)}`,
      );
    },
    createDraft(companyID, req) {
      return http.post<PurchaseVoucherDraft>(
        `/companies/${encodeURIComponent(companyID)}/drafts`,
        req,
      );
    },
  };
}
