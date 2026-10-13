/**
 * Tiny fetch wrapper for the `cored` REST surface.
 *
 * Responsibilities, kept deliberately small:
 *  - Resolve the base URL from the Vite env (`VITE_CORED_BASE_URL`), defaulting
 *    to a same-origin relative base so a dev proxy / prod reverse-proxy works
 *    with no config.
 *  - Serialize JSON bodies and append query params.
 *  - Surface non-2xx responses as a typed {@link ApiError} carrying the HTTP
 *    status and the server's `{ "error": "..." }` message when present.
 *
 * The wrapper does NOT parse money fields — they stay strings end to end.
 */

/**
 * ⛔⛔ THE REQUEST NEVER GOT AN ANSWER. This is the ONLY error in this app that
 * means the books did not see the write, and it exists so that the offline
 * outbox can be certain of that before it promises an operator their voucher is
 * safe.
 *
 * ⛔ WHY IT IS THROWN HERE AND NOWHERE ELSE. The queue-or-refuse decision on the
 * voucher accept path used to be expressible only as "not an {@link ApiError}",
 * and that is a decision by EXCLUSION — every programming error in the response
 * handling (a null deref in a `.then`, a builder that throws a TypeError) also
 * fails that test, and would have been queued as a network problem. The operator
 * would then be told their invoice is safely queued because the CLIENT crashed.
 * Only the transport knows a transport failure happened, so only the transport
 * raises this, and the decision becomes positive: `instanceof NetworkError`.
 *
 * ⛔ IT WRAPS THE `fetch` CALL ONLY — NEVER THE BODY READ. Once `fetch` resolves
 * there is a status, so the server answered and the books may have applied the
 * write; a body that dies mid-read is NOT this. That boundary is the whole
 * meaning of the type: "no answer was produced", not "something went wrong".
 *
 * ⚠️ AND THE RESIDUAL AMBIGUITY, STATED RATHER THAN HIDDEN: a connection reset
 * AFTER the server committed but BEFORE the response was read also lands here.
 * The client cannot distinguish that from a request that never arrived — nothing
 * on the wire can. See `shouldQueueOffline` for what is done about it and what
 * is merely disclosed.
 */
export class NetworkError extends Error {
  /** The original `fetch` rejection (a TypeError in every browser). */
  readonly cause: unknown;
  constructor(what: string, cause: unknown) {
    const detail = cause instanceof Error ? cause.message : String(cause);
    super(`could not reach the server (${what}): ${detail}`);
    this.name = "NetworkError";
    this.cause = cause;
  }
}

/** A typed transport / protocol error from the backend. */
export class ApiError extends Error {
  readonly status: number;
  /** The raw decoded body, when one was returned. */
  readonly body: unknown;
  constructor(status: number, message: string, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }
}

export type QueryValue = string | number | boolean | undefined | null;
export type QueryParams = Record<string, QueryValue>;

export interface HttpClientOptions {
  /** Override the base URL (otherwise read from env). */
  baseUrl?: string;
  /** Injected fetch (for tests / SSR). Defaults to global fetch. */
  fetchImpl?: typeof fetch;
  /** Extra headers merged into every request. */
  headers?: Record<string, string>;
}

/** Read the configured base URL from the Vite env, falling back to "". */
function envBaseUrl(): string {
  // import.meta.env is injected by Vite; guard for non-Vite (node test) runs.
  const env = (import.meta as unknown as { env?: Record<string, string> }).env;
  return env?.VITE_CORED_BASE_URL ?? "";
}

function buildQuery(params?: QueryParams): string {
  if (!params) return "";
  const usp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    usp.append(k, String(v));
  }
  const q = usp.toString();
  return q ? `?${q}` : "";
}

export interface HttpClient {
  get<T>(path: string, query?: QueryParams): Promise<T>;
  post<T>(path: string, body?: unknown, query?: QueryParams): Promise<T>;
  /** PUT a JSON body (e.g. a full voucher restatement on alter). Mirrors post. */
  put<T>(path: string, body?: unknown, query?: QueryParams): Promise<T>;
  /** DELETE with no body (e.g. a voucher tombstone). Mirrors get. */
  del<T>(path: string, query?: QueryParams): Promise<T>;
  /** POST a raw body (e.g. a Tally-XML string) with a caller-chosen content type. */
  postRaw<T>(
    path: string,
    body: BodyInit,
    contentType: string,
    query?: QueryParams,
  ): Promise<T>;
  /**
   * GET a BINARY response (the print routes answer `application/pdf`).
   *
   * It exists because `request` reads every response as text and then tries
   * JSON.parse — which for a PDF yields a lossy string, not a document. A PDF
   * must never touch that path.
   *
   * Errors still arrive as JSON (`{"error": …}`) on these routes, so this must
   * distinguish the two: a non-2xx is decoded as text and raised as
   * {@link ApiError} carrying THE SERVER'S OWN MESSAGE. That matters beyond
   * tidiness — the print routes answer 404 with a deliberately class-neutral body
   * for a class mismatch (an escalation refusal: a 403 there would confirm that a
   * voucher with that id exists), and 501 for a report that does not print. A
   * client that replaced either message with a guess of its own would either leak
   * what the 404 protects or hide why nothing printed.
   */
  getBlob(path: string, query?: QueryParams): Promise<BlobResponse>;
}

/**
 * A binary response: the bytes, the download name, and a reader for any
 * RESPONSE HEADER the caller needs.
 *
 * ⛔ `header` EXISTS BECAUSE SOME BODIES ARE NOT SELF-DESCRIBING. The three
 * statutory payroll files are served as a ZIP whose two entries are the file and
 * its disclosure sidecar, and the server states what is inside on the response
 * itself — `X-Statutory-File` names the file the portal will be given, and
 * `X-Statutory-Disclosure` says that nothing in it has been through the FVU, the
 * EPFO portal or the ESIC portal. A client that could not read those would have
 * to either unzip the archive (separating the file from its disclosure, which is
 * the one thing the zip exists to prevent) or invent the sentence itself.
 *
 * It is a LOOKUP and not the `Headers` object so that the shape stays trivially
 * fakeable in a test without constructing a DOM Headers.
 */
export interface BlobResponse {
  blob: Blob;
  filename: string | null;
  /** Case-insensitive response header lookup; null when absent. */
  header(name: string): string | null;
}

/**
 * Pull the download filename out of a Content-Disposition header. The print
 * handlers set one, and it names the document (invoice number, report + period),
 * so honouring it is the difference between saving "Invoice-INV-0042.pdf" and
 * saving "download.pdf".
 *
 * Handles the quoted form and RFC 5987's `filename*=UTF-8''…`, preferring the
 * latter when both are present.
 */
export function filenameFromDisposition(header: string | null): string | null {
  if (!header) return null;
  const ext = /filename\*\s*=\s*UTF-8''([^;]+)/i.exec(header);
  if (ext?.[1]) {
    try {
      return decodeURIComponent(ext[1].trim());
    } catch {
      // A malformed encoding must not lose the download; fall through.
    }
  }
  const quoted = /filename\s*=\s*"([^"]*)"/i.exec(header);
  if (quoted?.[1]) return quoted[1];
  const bare = /filename\s*=\s*([^;]+)/i.exec(header);
  return bare?.[1]?.trim() ?? null;
}

/**
 * THE SESSION TRANSPORT — the one fetch that carries the signed-in bearer.
 *
 * ⛔⛔ WHY THIS EXISTS (register row D49). The Bill Inbox and the draft-accept
 * lifecycle build their transports THEMSELVES, from module scope, nowhere near
 * the auth gate — `createInsightdClient()` and `insightdDraftLifecycle()` both
 * default to a client nobody handed them. That was harmless while those routes
 * went to a service that authenticated nobody; now they are cored routes under
 * `/v1/companies/{id}/drafts`, and a transport with no bearer gets 401 on every
 * one of them.
 *
 * ⛔ AND A 401 THERE IS SILENT. All four insightd-backed layers of the
 * duplicate-bill guard fail OPEN on any non-409 — `claimDraftForPost` warns and
 * returns null, `draftAlreadyAcceptedInsightd` returns null,
 * `releaseClaimAfterFailure` never throws, `recordAccepted` is
 * fire-and-forget — so an unauthenticated draft client does not break the accept.
 * It quietly collapses a five-door guard onto its client-side layers and re-opens
 * a duplicate-supplier-bill defect, with a console.warn as the only evidence.
 *
 * So the auth gate installs its bearer-carrying fetch here, ONCE, and every
 * client built without an explicit `fetchImpl` inherits it.
 *
 * ⚠️ IT IS RESOLVED PER REQUEST, NOT AT CONSTRUCTION. A client built before the
 * gate signs in — which is exactly what a module-scope default does — would
 * otherwise capture `globalThis.fetch` forever and never see the session. Reading
 * it inside `request` makes construction order irrelevant.
 *
 * ⚠️ AN EXPLICIT `fetchImpl` STILL WINS, so every test seam and every script that
 * injects its own transport is unaffected, and mock mode (where nothing installs
 * one) keeps the plain global fetch.
 */
let sessionFetch: typeof fetch | null = null;

/**
 * Install (or clear, with null) the authenticated session transport. Called by
 * the auth gate on sign-in and on sign-out; tests may call it directly.
 */
export function setSessionFetch(impl: typeof fetch | null): void {
  sessionFetch = impl;
}

/** The installed session transport, or null. Exported for assertions. */
export function currentSessionFetch(): typeof fetch | null {
  return sessionFetch;
}

/** Construct an {@link HttpClient} bound to a base URL + fetch implementation. */
export function createHttpClient(opts: HttpClientOptions = {}): HttpClient {
  const baseUrl = (opts.baseUrl ?? envBaseUrl()).replace(/\/$/, "");
  // Resolved per call — see setSessionFetch. Explicit > session > global.
  const doFetch: typeof fetch = (input, init) =>
    (opts.fetchImpl ?? sessionFetch ?? globalThis.fetch.bind(globalThis))(
      input,
      init,
    );
  const baseHeaders = opts.headers ?? {};

  async function request<T>(
    method: string,
    path: string,
    init: { body?: BodyInit; contentType?: string },
    query?: QueryParams,
  ): Promise<T> {
    const url = `${baseUrl}${path}${buildQuery(query)}`;
    const headers: Record<string, string> = { ...baseHeaders };
    if (init.contentType) headers["Content-Type"] = init.contentType;

    // ⛔ THE ONE `await` THAT MEANS "THE BOOKS NEVER SAW THIS". See NetworkError.
    let res: Response;
    try {
      res = await doFetch(url, {
        method,
        headers,
        body: init.body,
      });
    } catch (cause: unknown) {
      throw new NetworkError(`${method} ${path}`, cause);
    }

    const text = await res.text();
    let parsed: unknown = undefined;
    if (text.length > 0) {
      try {
        parsed = JSON.parse(text);
      } catch {
        parsed = text;
      }
    }

    if (!res.ok) {
      const message =
        parsed &&
        typeof parsed === "object" &&
        "error" in (parsed as Record<string, unknown>)
          ? String((parsed as Record<string, unknown>).error)
          : `request failed: ${res.status} ${res.statusText}`;
      throw new ApiError(res.status, message, parsed);
    }

    return parsed as T;
  }

  const client: HttpClient = {
    get<T>(path: string, query?: QueryParams) {
      return request<T>("GET", path, {}, query);
    },
    post<T>(path: string, body?: unknown, query?: QueryParams) {
      return request<T>(
        "POST",
        path,
        {
          body: body === undefined ? undefined : JSON.stringify(body),
          contentType: "application/json",
        },
        query,
      );
    },
    put<T>(path: string, body?: unknown, query?: QueryParams) {
      return request<T>(
        "PUT",
        path,
        {
          body: body === undefined ? undefined : JSON.stringify(body),
          contentType: "application/json",
        },
        query,
      );
    },
    del<T>(path: string, query?: QueryParams) {
      return request<T>("DELETE", path, {}, query);
    },
    postRaw<T>(
      path: string,
      body: BodyInit,
      contentType: string,
      query?: QueryParams,
    ) {
      return request<T>("POST", path, { body, contentType }, query);
    },
    async getBlob(path: string, query?: QueryParams) {
      const url = `${baseUrl}${path}${buildQuery(query)}`;
      // Same boundary as `request` — a download that could not reach the server
      // is a transport failure, not a 0-byte document.
      let res: Response;
      try {
        res = await doFetch(url, { method: "GET", headers: { ...baseHeaders } });
      } catch (cause: unknown) {
        throw new NetworkError(`GET ${path}`, cause);
      }

      if (!res.ok) {
        // The body is JSON here even though the success body is not. Read it as
        // text and surface the server's own sentence — see getBlob's doc comment
        // on why we must not substitute our own.
        const text = await res.text();
        let parsed: unknown = undefined;
        if (text.length > 0) {
          try {
            parsed = JSON.parse(text);
          } catch {
            parsed = text;
          }
        }
        const message =
          parsed &&
          typeof parsed === "object" &&
          "error" in (parsed as Record<string, unknown>)
            ? String((parsed as Record<string, unknown>).error)
            : `request failed: ${res.status} ${res.statusText}`;
        throw new ApiError(res.status, message, parsed);
      }

      return {
        blob: await res.blob(),
        filename: filenameFromDisposition(res.headers.get("Content-Disposition")),
        header: (name: string) => res.headers.get(name),
      };
    },
  };
  return client;
}
