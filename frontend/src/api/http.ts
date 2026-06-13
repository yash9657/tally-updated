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
  /** POST a raw body (e.g. a Tally-XML string) with a caller-chosen content type. */
  postRaw<T>(
    path: string,
    body: BodyInit,
    contentType: string,
    query?: QueryParams,
  ): Promise<T>;
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
    postRaw<T>(
      path: string,
      body: BodyInit,
      contentType: string,
      query?: QueryParams,
    ) {
      return request<T>("POST", path, { body, contentType }, query);
    },
  };
  return client;
}
