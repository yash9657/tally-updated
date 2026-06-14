import { describe, it, expect } from "vitest";
import { createInsightdClient } from "./insightd";
import { createHttpClient } from "./http";
import type { PurchaseVoucherDraft, CreateDraftRequest } from "./draft";

/** A minimal but contract-shaped draft for the fetch stub to echo back. */
function sampleDraft(): PurchaseVoucherDraft {
  return {
    draft_id: "draft-1",
    company_id: "co-1",
    created_at: "2026-06-13T00:00:00Z",
    source: "upload",
    status: "ready",
    body: {
      voucher_class: "purchase",
      party: "Acme Traders",
      party_ledger_id: "ledger-acme",
      postings: [
        {
          ledger_id: "ledger-purchases",
          ledger_name: "Purchases",
          dr_cr: "D",
          amount: "1000.0000",
        },
      ],
      gst: null,
    },
    confidence: {
      header: {},
      line_items: [],
      overall_header: 1,
      overall_line_items: 1,
      overall: 1,
    },
    redaction: { any_redacted: false, counts_by_type: {}, total: 0 },
    unresolved: [],
    derived_gst: null,
  };
}

describe("InsightdClient over a stub fetch", () => {
  it("getDraft hits the company-scoped draft route (NO /v1) with GET", async () => {
    const calls: { url: string; init?: RequestInit }[] = [];
    const draft = sampleDraft();
    const fetchImpl = (async (url: string | URL, init?: RequestInit) => {
      calls.push({ url: String(url), init });
      return new Response(JSON.stringify(draft), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }) as typeof fetch;

    const client = createInsightdClient({
      http: createHttpClient({ baseUrl: "http://insightd.test", fetchImpl }),
    });

    const got = await client.getDraft("co-1", "draft-1");

    expect(calls).toHaveLength(1);
    expect(calls[0]?.url).toBe(
      "http://insightd.test/companies/co-1/drafts/draft-1",
    );
    expect(calls[0]?.init?.method).toBe("GET");
    // Parsed JSON round-trips; money stays a string.
    expect(got.draft_id).toBe("draft-1");
    expect(got.body.postings[0]?.amount).toBe("1000.0000");
    expect(typeof got.body.postings[0]?.amount).toBe("string");
  });

  it("getDraft URL-encodes the company and draft ids", async () => {
    const calls: { url: string }[] = [];
    const fetchImpl = (async (url: string | URL) => {
      calls.push({ url: String(url) });
      return new Response(JSON.stringify(sampleDraft()), { status: 200 });
    }) as typeof fetch;

    const client = createInsightdClient({
      http: createHttpClient({ baseUrl: "http://insightd.test", fetchImpl }),
    });
    await client.getDraft("co/1", "draft 7");
    expect(calls[0]?.url).toBe(
      "http://insightd.test/companies/co%2F1/drafts/draft%207",
    );
  });

  it("createDraft POSTs a JSON body to the drafts collection and tolerates 201", async () => {
    const calls: { url: string; init?: RequestInit }[] = [];
    const draft = sampleDraft();
    const fetchImpl = (async (url: string | URL, init?: RequestInit) => {
      calls.push({ url: String(url), init });
      // 201 Created — createDraft must parse this like a 200.
      return new Response(JSON.stringify(draft), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      });
    }) as typeof fetch;

    const client = createInsightdClient({
      http: createHttpClient({ baseUrl: "http://insightd.test", fetchImpl }),
    });

    const req: CreateDraftRequest = {
      source: "upload",
      content_type: "image/png",
      filename: "invoice.png",
      tenant: { company_id: "co-1" },
      fixture: { kind: "intra-18" },
    };
    const got = await client.createDraft("co-1", req);

    expect(calls).toHaveLength(1);
    expect(calls[0]?.url).toBe("http://insightd.test/companies/co-1/drafts");
    expect(calls[0]?.init?.method).toBe("POST");
    expect(calls[0]?.init?.body).toBe(JSON.stringify(req));
    expect(got.status).toBe("ready");
  });
});
