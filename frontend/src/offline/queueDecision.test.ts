import { describe, expect, it } from "vitest";

import { ApiError, NetworkError, createHttpClient } from "../api/http";

import { shouldQueueOffline } from "./queueDecision";

describe("⛔ the queue-or-refuse judgement on the main write path", () => {
  it("QUEUES when the transport says the request never got an answer", () => {
    const v = shouldQueueOffline(new NetworkError("POST /v1/x", new TypeError("Failed to fetch")));
    expect(v.queue).toBe(true);
    // The sentence must say it is NOT in the books. An operator who reads
    // "queued" and nothing else has been told the wrong thing.
    expect(v.why).toMatch(/not in the books/i);
  });

  it("⛔⛔ DOES NOT QUEUE A 422 — the books refused it, and this is the direction that loses money", () => {
    const v = shouldQueueOffline(new ApiError(422, "debit total must equal credit total"));
    expect(v.queue).toBe(false);
    expect(v.why).toMatch(/refused/i);
  });

  it("⛔ DOES NOT QUEUE A 403 — a denial and a closed period are decisions, not outages", () => {
    expect(shouldQueueOffline(new ApiError(403, "voucher date is before the back-dated cutoff of 2026-04-01")).queue).toBe(false);
  });

  it("⛔ DOES NOT QUEUE A 500 — the books were REACHED and may have applied it; a re-commit mints a new idempotency key and cannot be deduped", () => {
    expect(shouldQueueOffline(new ApiError(500, "internal error")).queue).toBe(false);
  });

  it("⛔ DOES NOT QUEUE A 502/503/504 — a gateway that answered is a server something reached", () => {
    for (const status of [502, 503, 504]) {
      expect(shouldQueueOffline(new ApiError(status, "bad gateway")).queue, `HTTP ${status}`).toBe(false);
    }
  });

  it("⛔⛔ DOES NOT QUEUE A BARE TypeError, EVEN ONE WORDED EXACTLY LIKE A FETCH FAILURE", () => {
    // THE CONTROL FOR THE WHOLE DESIGN. This is what a null deref inside the
    // accept chain looks like, and it is indistinguishable by message from a
    // browser's own network rejection. A decision by exclusion ("not an
    // ApiError") queues it and tells the operator their invoice is safe because
    // the CLIENT crashed. Only the transport may declare a transport failure.
    const v = shouldQueueOffline(new TypeError("Failed to fetch"));
    expect(v.queue).toBe(false);
    expect(v.why).toMatch(/application itself/i);
  });

  it("the PROPERTY: over every failure this path can see, exactly one kind queues", () => {
    const failures: ReadonlyArray<[label: string, err: unknown]> = [
      ["network", new NetworkError("POST /v1/x", new TypeError("Failed to fetch"))],
      ["400", new ApiError(400, "bad request")],
      ["401", new ApiError(401, "unauthorized")],
      ["403", new ApiError(403, "denied")],
      ["404", new ApiError(404, "no such company")],
      ["409", new ApiError(409, "duplicate voucher number")],
      ["422", new ApiError(422, "unbalanced")],
      ["500", new ApiError(500, "internal error")],
      ["503", new ApiError(503, "unavailable")],
      ["client TypeError", new TypeError("x is not a function")],
      ["client Error", new Error("boom")],
      ["a thrown string", "boom"],
      ["undefined", undefined],
    ];
    const queued = failures.filter(([, e]) => shouldQueueOffline(e).queue).map(([l]) => l);
    expect(queued).toEqual(["network"]);
  });

  it("every verdict carries a sentence for the operator, never an empty string", () => {
    for (const err of [new NetworkError("x", new Error("y")), new ApiError(422, "z"), new Error("q")]) {
      expect(shouldQueueOffline(err).why.length).toBeGreaterThan(20);
    }
  });
});

describe("⛔ THE ORACLE THAT MAKES THE ABOVE MEAN ANYTHING: the real transport raises NetworkError", () => {
  // Without this, the judgement is tested against a hand-built error and
  // deleting http.ts's try/catch would fail NOTHING — the classifier would be
  // right about a value nothing ever produces.
  it("a fetch that REJECTS becomes a NetworkError, not the raw TypeError", async () => {
    const c = createHttpClient({
      baseUrl: "http://x",
      fetchImpl: () => Promise.reject(new TypeError("Failed to fetch")),
    });
    const err = await c.post("/v1/companies/c/vouchers/sales", {}).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(NetworkError);
    expect(shouldQueueOffline(err).queue, "and it must be the thing the outbox queues on").toBe(true);
  });

  it("a fetch that RESOLVES non-2xx is still an ApiError and still refuses to queue", async () => {
    const c = createHttpClient({
      baseUrl: "http://x",
      fetchImpl: () =>
        Promise.resolve(
          new Response(JSON.stringify({ error: "debit total must equal credit total" }), {
            status: 422,
            headers: { "Content-Type": "application/json" },
          }),
        ),
    });
    const err = await c.post("/v1/companies/c/vouchers/sales", {}).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(422);
    expect(shouldQueueOffline(err).queue).toBe(false);
  });

  it("⛔ a body that dies AFTER a 200 is NOT a NetworkError — the server answered, so the write may have landed", async () => {
    const c = createHttpClient({
      baseUrl: "http://x",
      fetchImpl: () =>
        Promise.resolve({
          ok: true,
          status: 200,
          statusText: "OK",
          text: () => Promise.reject(new TypeError("network error while reading body")),
        } as unknown as Response),
    });
    const err = await c.post("/v1/companies/c/vouchers/sales", {}).catch((e: unknown) => e);
    expect(err).not.toBeInstanceOf(NetworkError);
    expect(shouldQueueOffline(err).queue, "queueing this would risk a duplicate invoice").toBe(false);
  });
});
