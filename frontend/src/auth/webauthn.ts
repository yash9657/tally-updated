/**
 * The BROWSER HALF of cored's WebAuthn step-up ceremony.
 *
 * WHY THIS EXISTS. A token minted by `POST /auth/login` carries no `amr` claim,
 * and every company's Owner security level is seeded `requires_webauthn = true`
 * (migration 0011's `seed_company_auth_defaults`). So after a password sign-in
 * the session can READ everything and every WRITE answers
 *   403 {"code":"STEPUP_REQUIRED","error":"webauthn step-up required"}
 * The only honest fix is to actually perform the ceremony: assert a passkey and
 * exchange it for a token carrying `amr:["webauthn"]`.
 *
 * NO LIBRARY, NO CBOR, ON PURPOSE. cored's verifier (backend
 * internal/webauthn/webauthn.go) extracts the COSE public key from the
 * AUTHENTICATOR DATA itself, and `RegistrationInput` has no public-key field —
 * so the client never has to parse an attestation object. Chromium hands us
 * exactly the two buffers the server wants:
 *   registration: AuthenticatorAttestationResponse.getAuthenticatorData()
 *   assertion:    AuthenticatorAssertionResponse.authenticatorData
 *
 * PINNED WIRE CONTRACT (backend/internal/server/auth_webauthn.go — do not drift):
 *   POST /auth/webauthn/register/begin   Bearer, no body
 *     -> 200 {"challenge": b64url, "rp_id": "localhost", "user_id": uuid-string}
 *   POST /auth/webauthn/register/finish  Bearer
 *     <- {"credential_id_b64","client_data_json_b64","authenticator_data_b64"}
 *     -> 200 {"credential_id": b64url, "registered": true}
 *   POST /auth/webauthn/assert/begin     Bearer, no body
 *     -> 200 {"challenge": b64url, "rp_id": ..., "allow_credentials": [b64url]}
 *   POST /auth/webauthn/assert/finish    Bearer
 *     <- {"credential_id_b64","client_data_json_b64","authenticator_data_b64",
 *         "signature_b64"}
 *     -> 200 {"token": jwt-with-amr, "token_type":"Bearer","expires_in":3600}
 *
 * EVERY binary field is base64url WITHOUT PADDING (Go's `base64.RawURLEncoding`).
 * Standard base64, or a trailing '=', makes the server answer
 * `400 invalid base64url in <field>` — hence exactly one encoder here
 * ({@link toB64URL}) and one decoder ({@link fromB64URL}), used everywhere.
 *
 * THE CHALLENGE IS SINGLE-USE AND USER-BOUND (`challenges.Take`): a ceremony
 * must `begin` immediately before it `finish`es, and two overlapping ceremonies
 * for one user will make one of them fail. Callers MUST serialise — see
 * AuthContext's in-flight memoisation.
 *
 * This module is deliberately React-free and drives an INJECTED fetch, which is
 * also what keeps it out of App's 403 retry interceptor: these four calls must
 * never themselves trigger a step-up (that would recurse).
 */

/** What a ceremony needs: where cored is, who we are, and how to talk. */
export interface StepUpDeps {
  /** Base URL for `/auth/*` — "" (same-origin) behind the Vite dev proxy. */
  baseUrl: string;
  /** The CURRENT (amr-less) session bearer; the ceremony is authenticated. */
  token: string;
  /**
   * RAW fetch — never the app's intercepted one. The interceptor retries on
   * STEPUP_REQUIRED by calling back into here, so routing these calls through
   * it would recurse.
   */
  fetchImpl: typeof fetch;
}

/**
 * There is no WebAuthn here at all: no `navigator.credentials`. Happens in
 * jsdom, in an insecure context (plain http on a non-localhost origin) and in
 * old browsers. Distinct from {@link StepUpFailedError} because the caller must
 * explain "this browser cannot", not "your passkey was refused".
 */
export class StepUpUnsupportedError extends Error {
  constructor(
    message = "this browser exposes no navigator.credentials, so a passkey step-up is impossible here (WebAuthn needs a secure context: https, or localhost)",
  ) {
    super(message);
    this.name = "StepUpUnsupportedError";
  }
}

/** The ceremony ran and did not produce a token: refused, cancelled, or 501. */
export class StepUpFailedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "StepUpFailedError";
  }
}

/** The RP name shown by the authenticator's own prompt. */
const RP_NAME = "TallyPrime";

/** How long the authenticator prompt may stay up before the browser gives up. */
const CEREMONY_TIMEOUT_MS = 60_000;

/**
 * COSE algorithms cored can verify — internal/webauthn's `AlgES256` (-7) and
 * `AlgEdDSA` (-8). Offering anything else risks an authenticator picking a key
 * type `coseToSPKI` rejects with ErrUnsupportedAlg at register/finish.
 */
const PUB_KEY_CRED_PARAMS: PublicKeyCredentialParameters[] = [
  { type: "public-key", alg: -7 },
  { type: "public-key", alg: -8 },
];

// ---------------------------------------------------------------------------
// base64url (RawURLEncoding) — the ONE encoder/decoder pair
// ---------------------------------------------------------------------------

/** Encode bytes as base64url WITHOUT padding (Go `base64.RawURLEncoding`). */
export function toB64URL(buf: ArrayBuffer | Uint8Array): string {
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]!);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/**
 * Decode base64url (with or without padding) to bytes. The server hands back
 * challenges as base64url TEXT; `navigator.credentials` wants the BYTES.
 *
 * Returns an ArrayBuffer, which is what `BufferSource` fields want without any
 * cast (a plain Uint8Array is `Uint8Array<ArrayBufferLike>` and does not
 * satisfy the DOM's `ArrayBufferView<ArrayBuffer>`).
 */
export function fromB64URL(s: string): ArrayBuffer {
  const padded = s.length % 4 === 0 ? s : s + "=".repeat(4 - (s.length % 4));
  const bin = atob(padded.replace(/-/g, "+").replace(/_/g, "/"));
  const buf = new ArrayBuffer(bin.length);
  const out = new Uint8Array(buf);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return buf;
}

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

function joinUrl(baseUrl: string, path: string): string {
  return `${baseUrl.replace(/\/$/, "")}${path}`;
}

/**
 * POST a ceremony route with the session bearer and decode its JSON.
 *
 * Any non-2xx becomes a {@link StepUpFailedError} carrying the status AND the
 * server's message — a silent failure here would look to the user like a dead
 * keystroke. 501 gets its own sentence because it is not a bug: cored only
 * mints stepped-up tokens when the dev signer is present (production step-up is
 * the IdP's job).
 */
async function post<T>(
  deps: StepUpDeps,
  path: string,
  body?: unknown,
): Promise<T> {
  let res: Response;
  try {
    res = await deps.fetchImpl(joinUrl(deps.baseUrl, path), {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${deps.token}`,
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (err) {
    throw new StepUpFailedError(
      `${path}: ${err instanceof Error ? err.message : String(err)}`,
    );
  }
  const text = await res.text().catch(() => "");
  let parsed: unknown = undefined;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = text;
    }
  }
  if (res.status === 501) {
    throw new StepUpFailedError(
      `${path}: this server cannot mint a stepped-up token (501) — production step-up is performed by the external identity provider`,
    );
  }
  if (!res.ok) {
    const detail =
      parsed && typeof parsed === "object" && "error" in parsed
        ? String((parsed as Record<string, unknown>).error)
        : typeof parsed === "string"
          ? parsed
          : "";
    throw new StepUpFailedError(
      `${path}: ${res.status}${detail ? ` ${detail}` : ""}`,
    );
  }
  return (parsed ?? {}) as T;
}

// ---------------------------------------------------------------------------
// navigator.credentials
// ---------------------------------------------------------------------------

/** The credentials container, or {@link StepUpUnsupportedError}. */
function credentialsContainer(): CredentialsContainer {
  const nav = (globalThis as { navigator?: Navigator }).navigator;
  const container = nav?.credentials as CredentialsContainer | undefined;
  if (
    !container ||
    typeof container.get !== "function" ||
    typeof container.create !== "function"
  ) {
    throw new StepUpUnsupportedError();
  }
  return container;
}

/**
 * A cancelled prompt rejects with a DOMException (NotAllowedError), and a
 * discoverable-credential mismatch can resolve `null`. Both mean "no assertion",
 * and both must reach the caller as a {@link StepUpFailedError} rather than an
 * opaque DOM error.
 */
// isNotAllowed reports whether a WebAuthn ceremony rejection was NotAllowedError.
//
// The spec deliberately collapses several outcomes into this ONE error so a site
// cannot enumerate which passkeys a device holds: the user refused, the prompt
// timed out, or — the case that matters here — the authenticator holds none of
// the credentials in allowCredentials. Callers therefore cannot tell "wrong
// device" from "user said no", and must pick a remedy that is safe for both.
function isNotAllowed(err: unknown): boolean {
  return (
    typeof err === "object" &&
    err !== null &&
    (err as { name?: unknown }).name === "NotAllowedError"
  );
}

function ceremonyFailure(what: string, err: unknown): StepUpFailedError {
  const msg = err instanceof Error ? `${err.name}: ${err.message}` : String(err);
  return new StepUpFailedError(`${what} was not completed (${msg})`);
}

// ---------------------------------------------------------------------------
// The ceremony
// ---------------------------------------------------------------------------

interface AssertBegin {
  challenge: string;
  rp_id: string;
  allow_credentials?: string[] | null;
}

interface RegisterBegin {
  challenge: string;
  rp_id: string;
  user_id: string;
}

async function assertBegin(deps: StepUpDeps): Promise<AssertBegin> {
  const b = await post<AssertBegin>(deps, "/auth/webauthn/assert/begin");
  if (typeof b.challenge !== "string" || !b.challenge) {
    throw new StepUpFailedError("assert/begin returned no challenge");
  }
  return b;
}

/**
 * The FULL registration ceremony. Run only when the user has no passkey yet
 * (assert/begin listed no allow_credentials) — the server's challenge is
 * single-use, so this is followed by a FRESH assert/begin, never by reusing the
 * assertion challenge that discovered the gap.
 */
async function register(
  deps: StepUpDeps,
  container: CredentialsContainer,
): Promise<void> {
  const begin = await post<RegisterBegin>(
    deps,
    "/auth/webauthn/register/begin",
  );
  if (typeof begin.challenge !== "string" || !begin.challenge) {
    throw new StepUpFailedError("register/begin returned no challenge");
  }
  // user.id must be a BufferSource; the server's user_id is an opaque UUID
  // STRING, so its UTF-8 bytes go on the wire. cored never reads it back.
  const userId = new TextEncoder().encode(begin.user_id ?? "");

  let created: Credential | null;
  try {
    created = await container.create({
      publicKey: {
        challenge: fromB64URL(begin.challenge),
        // rp.id MUST be exactly what the server handed us: cored compares
        // SHA-256(rp_id) against authData.rpIdHash byte for byte.
        rp: { id: begin.rp_id, name: RP_NAME },
        user: {
          id: userId,
          name: begin.user_id ?? "user",
          displayName: begin.user_id ?? "user",
        },
        pubKeyCredParams: PUB_KEY_CRED_PARAMS,
        authenticatorSelection: { userVerification: "preferred" },
        attestation: "none",
        timeout: CEREMONY_TIMEOUT_MS,
      },
    });
  } catch (err) {
    throw ceremonyFailure("passkey registration", err);
  }
  if (!created) {
    throw new StepUpFailedError("passkey registration returned no credential");
  }
  const cred = created as PublicKeyCredential;
  const response = cred.response as AuthenticatorAttestationResponse;
  if (typeof response?.getAuthenticatorData !== "function") {
    throw new StepUpUnsupportedError(
      "this browser's AuthenticatorAttestationResponse has no getAuthenticatorData(), which cored's registration route requires",
    );
  }
  await post(deps, "/auth/webauthn/register/finish", {
    credential_id_b64: toB64URL(cred.rawId),
    client_data_json_b64: toB64URL(response.clientDataJSON),
    authenticator_data_b64: toB64URL(response.getAuthenticatorData()),
  });
}

/**
 * Perform a WebAuthn step-up and return the NEW bearer, which carries
 * `amr:["webauthn"]` and therefore satisfies the PDP's `requires_webauthn`
 * gate on writes.
 *
 * Registers a passkey first when the user has none. Throws
 * {@link StepUpUnsupportedError} when the browser has no WebAuthn at all and
 * {@link StepUpFailedError} for every other failure (refused, cancelled, 400,
 * 501, malformed response).
 */
export async function stepUp(deps: StepUpDeps): Promise<string> {
  const container = credentialsContainer();

  // Ask the authenticator to prove one of the credentials the server lists.
  // Returns null when this device holds NONE of them (see enrolment below).
  const tryAssert = async (
    begin: AssertBegin,
  ): Promise<PublicKeyCredential | null> => {
    const allow = begin.allow_credentials ?? [];
    try {
      const got = await container.get({
        publicKey: {
          challenge: fromB64URL(begin.challenge),
          rpId: begin.rp_id,
          allowCredentials: allow.map((id) => ({
            type: "public-key" as const,
            id: fromB64URL(id),
          })),
          userVerification: "preferred",
          timeout: CEREMONY_TIMEOUT_MS,
        },
      });
      return (got as PublicKeyCredential | null) ?? null;
    } catch (err) {
      // A browser does NOT distinguish "you hold none of these credentials" from
      // "the user refused" — both are NotAllowedError, deliberately, so a site
      // cannot probe which passkeys you own. The caller decides what to do next.
      if (isNotAllowed(err)) return null;
      throw ceremonyFailure("the passkey confirmation", err);
    }
  };

  let begin = await assertBegin(deps);
  let got = (begin.allow_credentials ?? []).length === 0 ? null : await tryAssert(begin);

  if (!got) {
    // ENROL THIS DEVICE. Two different situations arrive here and the remedy is
    // the same, which is why they share a branch:
    //
    //   (a) the user has no passkey at all (allow_credentials was empty);
    //   (b) the user HAS passkeys, but none of them live on THIS device —
    //       they enrolled on their laptop and are now signing in on their phone.
    //
    // (b) is the case that made this a real bug rather than a first-run detail.
    // Skipping enrolment whenever the server listed *some* credential left such a
    // user permanently unable to step up, and therefore permanently unable to
    // WRITE, with no way out from inside the app. Passkeys are per-authenticator;
    // "this user has a credential somewhere" is not the same question as "this
    // device can prove one".
    //
    // The trade-off, stated plainly: because NotAllowedError also covers an
    // outright refusal, a user who CANCELS the prompt is offered enrolment rather
    // than an error. That is the safe direction — the alternative strands case
    // (b) forever — and cancelling the enrolment prompt too still fails cleanly.
    await register(deps, container);
    // The begin above was consumed by its begin/finish pairing; take a fresh one.
    begin = await assertBegin(deps);
    if ((begin.allow_credentials ?? []).length === 0) {
      throw new StepUpFailedError(
        "a passkey was registered but the server still lists no credentials for this user",
      );
    }
    got = await tryAssert(begin);
  }

  if (!got) {
    throw new StepUpFailedError("the passkey confirmation returned no assertion");
  }
  const assertion = got as PublicKeyCredential;
  const response = assertion.response as AuthenticatorAssertionResponse;
  if (!response?.signature) {
    throw new StepUpFailedError("the passkey assertion carried no signature");
  }

  const finished = await post<{ token?: unknown }>(
    deps,
    "/auth/webauthn/assert/finish",
    {
      credential_id_b64: toB64URL(assertion.rawId),
      client_data_json_b64: toB64URL(response.clientDataJSON),
      authenticator_data_b64: toB64URL(response.authenticatorData),
      signature_b64: toB64URL(response.signature),
    },
  );
  if (typeof finished.token !== "string" || !finished.token) {
    throw new StepUpFailedError("assert/finish returned no token");
  }
  return finished.token;
}
