/**
 * Shared auth helper for the live e2e scripts (Task D).
 *
 * With cored's auth middleware on, every /v1/companies call needs an
 * Authorization: Bearer <jwt>. These scripts run BEFORE any user exists, so they
 * first POST /auth/token (the in-process dev issuer, mounted ONLY when cored is
 * booted with AUTH_DEV_TOKEN_ENABLED=true; it does not exist in production, where
 * tokens come from the external IdP) to mint an Owner token, then pass it as a
 * header into the REST client. The same
 * Owner subject that creates a company becomes its Owner member (create-on-first-
 * login + the Owner-seed in handleCreateCompany), so all subsequent calls
 * authorize. AMR ["webauthn"] satisfies the Owner level's step-up on writes.
 */

/** Mint a dev Owner token from cored's POST /auth/token. */
export async function mintOwnerToken(baseUrl: string): Promise<string> {
  const subject = `e2e-owner-${crypto.randomUUID()}`;
  const res = await fetch(`${baseUrl.replace(/\/$/, "")}/auth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject, amr: ["webauthn"] }),
  });
  if (!res.ok) {
    throw new Error(`mint owner token failed: ${res.status} ${await res.text()}`);
  }
  const body = (await res.json()) as { token?: string };
  if (!body.token) throw new Error("mint owner token: no token in response");
  return body.token;
}

/** Build the headers object the REST client merges into every request. */
export async function authHeaders(
  baseUrl: string,
): Promise<Record<string, string>> {
  const token = await mintOwnerToken(baseUrl);
  return { Authorization: `Bearer ${token}` };
}

// ===========================================================================
// FIRST-PARTY PASSWORD LOGIN (WS-A3)
// ===========================================================================
//
// Everything above mints a token for an arbitrary subject through the dev-gated
// POST /auth/token — it asserts an identity, it does not AUTHENTICATE one.
// Below is the real thing: POST /auth/login exchanges a subject + password for
// the same kind of Bearer, and POST /auth/password is the only route that ever
// writes a password.
//
// !! THE ONE THING THAT IS DIFFERENT ABOUT A LOGIN TOKEN !!
// A token minted by POST /auth/login carries NO `amr` claim (that is the pinned
// backend contract: WebAuthn is a separate step-up ceremony). Migration 0011's
// seed_company_auth_defaults() gives EVERY company an Owner level with
// requires_webauthn = true, so a login token can READ a company and CREATE a
// company but CANNOT write a voucher or a master inside one — it gets
// 403 {"code":"STEPUP_REQUIRED"}. Measured live, not inferred:
//
//   POST /auth/login              -> 200, token without amr
//   GET  /v1/companies/{id}/trial-balance  -> 200
//   POST /v1/companies                     -> 201
//   POST /v1/companies/{id}/ledgers        -> 403 STEPUP_REQUIRED
//
// So the seeding scripts keep using mintOwnerToken()/token() for the WRITES
// (they need the webauthn AMR) and use the password routes only to ESTABLISH
// the credential a human will later log in with. Do not "simplify" a seeder by
// switching its writes to a login token; it will 403.

/**
 * The demo password every seeded identity gets. >= 10 characters, which is the
 * backend policy floor (authn.MinPasswordRunes). Override with DEMO_PASSWORD to
 * seed a different one — the verifiers and the browser e2e read the same env
 * var, so they stay in agreement.
 */
export const DEMO_PASSWORD = process.env.DEMO_PASSWORD ?? "tally-demo-2026";

/** The identity block POST /auth/login returns alongside the token. */
export interface LoginUser {
  id: string;
  subject: string;
  display_name: string;
  email: string;
}

export interface LoginResult {
  token: string;
  token_type: string;
  expires_in: number;
  user: LoginUser;
}

/** Raised by {@link loginWithPassword} on a non-200, carrying the status. */
export class LoginFailed extends Error {
  readonly status: number;
  readonly body: string;
  constructor(subject: string, status: number, body: string) {
    super(`login as "${subject}" failed: ${status} ${body}`);
    this.name = "LoginFailed";
    this.status = status;
    this.body = body;
  }
}

/**
 * Authenticate a subject with a password — the REAL login, not a dev mint.
 *
 * Returns the token AND the user block, so a caller can prove "who am I" the
 * same way the browser does. Throws {@link LoginFailed} on 401 (invalid
 * credentials — deliberately identical for an unknown subject, a user with no
 * password, a wrong password and an inactive user) and on 423 (locked out).
 */
export async function loginWithPassword(
  baseUrl: string,
  subject: string,
  password: string = DEMO_PASSWORD,
): Promise<LoginResult> {
  const res = await fetch(`${baseUrl.replace(/\/$/, "")}/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject, password }),
  });
  const text = await res.text();
  if (!res.ok) throw new LoginFailed(subject, res.status, text);
  const body = JSON.parse(text) as Partial<LoginResult>;
  if (!body.token) throw new LoginFailed(subject, res.status, "200 without a token");
  if (!body.user?.subject) throw new LoginFailed(subject, res.status, "200 without a user");
  return body as LoginResult;
}

/**
 * Set or change the password of the user the token belongs to.
 *
 * The acting identity comes from the BEARER, never from the body — there is no
 * subject field, deliberately, so an administrator cannot set another person's
 * password. On the first-set path (no credential exists yet) the server ignores
 * `currentPassword`.
 */
export async function setPassword(
  baseUrl: string,
  token: string,
  currentPassword: string,
  newPassword: string,
): Promise<{ status: number; body: string }> {
  const res = await fetch(`${baseUrl.replace(/\/$/, "")}/auth/password`, {
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
  return { status: res.status, body: await res.text() };
}

/**
 * Give `subject` a password, IDEMPOTENTLY, and prove it by logging in.
 *
 * This is what the seeders call. It is the only production-shaped way to create
 * a first password today, and it is worth understanding why it needs a dev
 * token to work at all:
 *
 *   POST /auth/password requires a valid Bearer, and the only in-process minter
 *   of one is the dev-gated POST /auth/token. So a FIRST password can only be
 *   set by somebody who already holds a token from the dev issuer (or from an
 *   external IdP). A production deployment with signer == nil and no IdP has NO
 *   path to a first password — a disclosed gap that needs an admin-issued
 *   one-time set-password token before this is a real production login.
 *
 * Idempotence trick: we send `current_password = password` on every run.
 *   - First run:  no credential exists, the server IGNORES current_password and
 *                 sets it. 204.
 *   - Later runs: the credential exists and current_password matches, so it is a
 *                 no-op change to the same value. 204.
 *   - The account already has a DIFFERENT password: 401, and we say so loudly
 *     rather than silently leaving the seed's advertised password wrong. There
 *     is no admin reset by design; clear it with
 *     `DELETE FROM user_credential WHERE user_id = (SELECT id FROM app_user WHERE subject = '...')`.
 *
 * The final loginWithPassword() is not ceremony — it is the seed verifying its
 * own claim, so `pnpm seed:demo` can never print "password: tally-demo-2026"
 * for an identity that cannot actually log in with it.
 */
export async function ensurePassword(
  baseUrl: string,
  subject: string,
  password: string = DEMO_PASSWORD,
): Promise<LoginResult> {
  const mintRes = await fetch(`${baseUrl.replace(/\/$/, "")}/auth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject, amr: ["webauthn"] }),
  });
  if (!mintRes.ok) {
    throw new Error(
      `ensurePassword: mint token for "${subject}" failed: ${mintRes.status} ` +
        `${await mintRes.text()} (is cored running with AUTH_DEV_TOKEN_ENABLED=true?)`,
    );
  }
  const { token } = (await mintRes.json()) as { token: string };

  // The set below is ALSO what provisions the app_user row for a
  // never-seen-before subject: POST /auth/password runs behind
  // authnOnlyMiddleware, whose CreateUserOnFirstLogin materialises the identity.
  // POST /auth/login deliberately never does that.
  const set = await setPassword(baseUrl, token, password, password);
  if (set.status === 401) {
    throw new Error(
      `ensurePassword: "${subject}" already has a DIFFERENT password. There is no ` +
        "admin reset by design (POST /auth/password is self-service only). Either " +
        "re-run with DEMO_PASSWORD set to the existing one, or clear the credential:\n" +
        `  DELETE FROM user_credential WHERE user_id = (SELECT id FROM app_user WHERE subject = '${subject}');`,
    );
  }
  if (set.status !== 204) {
    throw new Error(
      `ensurePassword: set password for "${subject}" -> ${set.status} ${set.body}`,
    );
  }
  return loginWithPassword(baseUrl, subject, password);
}
