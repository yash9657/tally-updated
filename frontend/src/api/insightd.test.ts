import { describe, it, expect } from "vitest";
import { createInsightdClient } from "./insightd";
import { createHttpClient, setSessionFetch } from "./http";
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
  it("getDraft hits cored's company-scoped draft route UNDER /v1, with GET", async () => {
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
      "http://insightd.test/v1/companies/co-1/drafts/draft-1",
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
      "http://insightd.test/v1/companies/co%2F1/drafts/draft%207",
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
    expect(calls[0]?.url).toBe("http://insightd.test/v1/companies/co-1/drafts");
    expect(calls[0]?.init?.method).toBe("POST");
    expect(calls[0]?.init?.body).toBe(JSON.stringify(req));
    expect(got.status).toBe("ready");
  });
});

describe("⛔ THE DRAFT CLIENT INHERITS THE SESSION BEARER (register row D49)", () => {
  /**
   * ⛔⛔ WHY THIS ARM EXISTS AND WHY "THE ACCEPT STILL WORKS" IS NOT AN ORACLE.
   *
   * The Bill Inbox and the draft-accept lifecycle both build their transport from
   * MODULE SCOPE — `createInsightdClient()` with no arguments — nowhere near the
   * auth gate. Their routes are cored routes now, so a transport with no bearer
   * gets 401 on every one of them.
   *
   * And a 401 there is SILENT. Every insightd-backed layer of the duplicate-bill
   * guard fails OPEN on a non-409: claimDraftForPost warns and returns null,
   * draftAlreadyAcceptedInsightd returns null, releaseClaimAfterFailure never
   * throws, recordAccepted is fire-and-forget. So an unauthenticated draft client
   * does not break the accept — it quietly collapses a five-door duplicate guard
   * onto its client-side layers and re-opens a money defect, with a console.warn
   * as the only evidence. A test that asserted "the accept succeeded" would be
   * GREEN through exactly that.
   *
   * So this asserts the CREDENTIAL REACHED THE WIRE.
   */
  it("a client built with NO transport sends the session bearer", async () => {
    const seen: { url: string; auth: string | null }[] = [];
    // Stands in for App.tsx's bearerFetch: a fetch that attaches the session's
    // bearer to whatever it is handed.
    const sessionImpl = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const headers = new Headers(init?.headers ?? {});
      headers.set("Authorization", "Bearer session-token");
      seen.push({ url: String(input), auth: headers.get("Authorization") });
      return new Response(JSON.stringify([]), { status: 200 });
    }) as typeof fetch;

    setSessionFetch(sessionImpl);
    try {
      // No baseUrl, no http, no fetchImpl — the module-scope default.
      await createInsightdClient().listDrafts("co-1");
    } finally {
      setSessionFetch(null);
    }

    expect(seen).toHaveLength(1);
    expect(seen[0]?.url).toBe("/v1/companies/co-1/drafts");
    expect(seen[0]?.auth).toBe("Bearer session-token");
  });

  it("⛔ a client built BEFORE the session is installed still picks it up", async () => {
    /**
     * ⛔ THE ORDERING IS THE PROPERTY, AND A MUTATION PROOF IS WHY THIS ARM
     * EXISTS. Resolving the transport ONCE at construction — the version a reader
     * would naturally write — passes every other arm in this file, because they
     * all install the session first. The app does the opposite: the Bill Inbox's
     * factory and the draft lifecycle's memoised port are built from module scope,
     * and the auth gate REBUILDS its bearer fetch whenever the session changes
     * (sign-out, sign-in as a different subject, a step-up replacing the token).
     * A client that captured whichever fetch existed when it happened to be
     * constructed would keep the previous session's closure, and the symptom would
     * be a 401 that every draft layer swallows.
     */
    const seen: string[] = [];
    // Constructed with NO session installed at all.
    const client = createInsightdClient();
    setSessionFetch((async (input: RequestInfo | URL) => {
      seen.push(String(input));
      return new Response("[]", { status: 200 });
    }) as typeof fetch);
    try {
      await client.listDrafts("co-1");
    } finally {
      setSessionFetch(null);
    }
    expect(seen).toEqual(["/v1/companies/co-1/drafts"]);
  });

  it("an explicit transport still wins over the session one", async () => {
    const sessionCalls: string[] = [];
    setSessionFetch((async () => {
      sessionCalls.push("session");
      return new Response("[]", { status: 200 });
    }) as typeof fetch);
    const explicitCalls: string[] = [];
    try {
      const client = createInsightdClient({
        http: createHttpClient({
          baseUrl: "http://explicit.test",
          fetchImpl: (async (url: string | URL) => {
            explicitCalls.push(String(url));
            return new Response("[]", { status: 200 });
          }) as typeof fetch,
        }),
      });
      await client.listDrafts("co-1");
    } finally {
      setSessionFetch(null);
    }
    expect(explicitCalls).toEqual(["http://explicit.test/v1/companies/co-1/drafts"]);
    expect(sessionCalls).toEqual([]);
  });

  it("with NO session installed it falls back to the global fetch", async () => {
    // ⚠️ THE CONTROL. Without this the arm above would pass on a client that
    // simply never worked at all, and mock mode (which installs no session) would
    // be broken with nothing noticing.
    const original = globalThis.fetch;
    const globalCalls: string[] = [];
    globalThis.fetch = (async (url: string | URL) => {
      globalCalls.push(String(url));
      return new Response("[]", { status: 200 });
    }) as typeof fetch;
    try {
      await createInsightdClient({ baseUrl: "http://fallback.test" }).listDrafts("co-1");
    } finally {
      globalThis.fetch = original;
    }
    expect(globalCalls).toEqual(["http://fallback.test/v1/companies/co-1/drafts"]);
  });
});
