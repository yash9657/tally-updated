/**
 * First-party PASSWORD authentication against cored's `/auth` surface.
 *
 * Deliberately dependency-free (plain `fetch`, no {@link HttpClient}): the auth
 * calls happen BEFORE a {@link CoredClient} exists, and their failure modes are
 * not the generic `ApiError` ones — a 401 and a 423 are the two states the login
 * screen must render differently, so they get their own error classes.
 *
 * PINNED HTTP CONTRACT (backend WS-A1; this module is the client half):
 *   POST /auth/login     {"subject","password"}
 *     200 {"token","token_type":"Bearer","expires_in":3600,
 *          "user":{"id","subject","display_name","email"}}
 *     400 {"error":"subject is required"} / {"error":"password is required"}
 *     401 {"error":"invalid credentials"}   -- IDENTICAL for unknown subject,
 *         no credential set, wrong password and inactive user (no enumeration)
 *     423 {"error":"account locked","code":"LOCKED","until":RFC3339}
 *   POST /auth/password  Bearer required; {"current_password","new_password"}
 *     204 No Content · 400 {"error":"<policy message>"} · 401 invalid credentials
 *
 * The acting user of `/auth/password` is taken by the SERVER from the bearer
 * token, never from the body — this client cannot address another person.
 */

/** The identity behind a session, as the server reports it. */
export interface AuthUser {
  id: string;
  subject: string;
  display_name: string;
  email: string;
}

/** The 200 body of POST /auth/login. */
export interface LoginResponse {
  token: string;
  token_type: string;
  expires_in: number;
  user: AuthUser;
}

/**
 * 401 — the credentials were not accepted. Carries NO detail about which half
 * was wrong (the server deliberately returns a byte-identical body for an
 * unknown subject, a missing credential, a wrong password and an inactive
 * user), so neither may this class.
 */
export class AuthInvalidError extends Error {
  constructor(message = "invalid credentials") {
    super(message);
    this.name = "AuthInvalidError";
  }
}

/** 423 — too many failed attempts; `until` is the RFC3339 instant it lifts. */
export class AuthLockedError extends Error {
  readonly until: string;
  constructor(until: string, message = "account locked") {
    super(message);
    this.name = "AuthLockedError";
    this.until = until;
  }
}

/** Injected fetch (tests / SSR); defaults to the global one. */
function resolveFetch(fetchImpl?: typeof fetch): typeof fetch {
  return fetchImpl ?? globalThis.fetch.bind(globalThis);
}

function joinUrl(baseUrl: string, path: string): string {
  return `${baseUrl.replace(/\/$/, "")}${path}`;
}

/** Read the body once, as JSON when it parses and as raw text otherwise. */
async function readBody(res: Response): Promise<unknown> {
  const text = await res.text();
  if (text.length === 0) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function fieldOf(body: unknown, key: string): string | undefined {
  if (body && typeof body === "object" && key in (body as Record<string, unknown>)) {
    const v = (body as Record<string, unknown>)[key];
    if (typeof v === "string") return v;
  }
  return undefined;
}

/** Non-2xx that is neither 401 nor 423: surface the status AND the body. */
function transportError(res: Response, body: unknown): Error {
  const detail =
    fieldOf(body, "error") ??
    (body === undefined ? "" : typeof body === "string" ? body : JSON.stringify(body));
  return new Error(
    `auth request failed: ${res.status} ${res.statusText}${detail ? `: ${detail}` : ""}`,
  );
}

/**
 * Sign in with a subject + password.
 *
 * @throws {@link AuthInvalidError} on 401, {@link AuthLockedError} on 423, and a
 * plain `Error` carrying the status and body on any other non-2xx (including the
 * 400 validation replies) or on a malformed 200.
 */
export async function login(
  baseUrl: string,
  subject: string,
  password: string,
  fetchImpl?: typeof fetch,
): Promise<LoginResponse> {
  const res = await resolveFetch(fetchImpl)(joinUrl(baseUrl, "/auth/login"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject, password }),
  });
  const body = await readBody(res);
  if (res.status === 401) throw new AuthInvalidError(fieldOf(body, "error"));
  if (res.status === 423) {
    throw new AuthLockedError(fieldOf(body, "until") ?? "", fieldOf(body, "error"));
  }
  if (!res.ok) throw transportError(res, body);

  const parsed = body as Partial<LoginResponse> | undefined;
  if (!parsed || typeof parsed.token !== "string" || !parsed.token) {
    throw new Error("login response did not carry a token");
  }
  const user = parsed.user;
  if (!user || typeof user.subject !== "string") {
    throw new Error("login response did not carry a user");
  }
  return {
    token: parsed.token,
    token_type: parsed.token_type ?? "Bearer",
    expires_in: parsed.expires_in ?? 0,
    user: {
      id: user.id ?? "",
      subject: user.subject,
      display_name: user.display_name ?? "",
      email: user.email ?? "",
    },
  };
}

/**
 * Set or change the SIGNED-IN user's own password. `currentPassword` is ignored
 * by the server on the first-set path (no credential exists yet).
 *
 * @throws {@link AuthInvalidError} on 401 (a credential exists and
 * `currentPassword` did not verify) and a plain `Error` carrying status + body
 * on any other non-2xx — notably the 400 whose message is the policy failure.
 */
export async function setPassword(
  baseUrl: string,
  token: string,
  currentPassword: string,
  newPassword: string,
  fetchImpl?: typeof fetch,
): Promise<void> {
  const res = await resolveFetch(fetchImpl)(joinUrl(baseUrl, "/auth/password"), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({
      current_password: currentPassword,
      new_password: newPassword,
    }),
  });
  if (res.ok) return;
  const body = await readBody(res);
  if (res.status === 401) throw new AuthInvalidError(fieldOf(body, "error"));
  if (res.status === 423) {
    throw new AuthLockedError(fieldOf(body, "until") ?? "", fieldOf(body, "error"));
  }
  throw transportError(res, body);
}
