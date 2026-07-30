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
  getBlob(
    path: string,
    query?: QueryParams,
  ): Promise<{ blob: Blob; filename: string | null }>;
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

/** Construct an {@link HttpClient} bound to a base URL + fetch implementation. */
export function createHttpClient(opts: HttpClientOptions = {}): HttpClient {
  const baseUrl = (opts.baseUrl ?? envBaseUrl()).replace(/\/$/, "");
  const doFetch = opts.fetchImpl ?? globalThis.fetch.bind(globalThis);
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

    const res = await doFetch(url, {
      method,
      headers,
      body: init.body,
    });

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
      const res = await doFetch(url, { method: "GET", headers: { ...baseHeaders } });

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
      };
    },
  };
  return client;
}
