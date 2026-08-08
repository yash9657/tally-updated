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
 *        allow_credentials is NEWEST FIRST and at most 64 long — see
 *        {@link MAX_ALLOW_CREDENTIALS} and the server's maxAllowCredentials.
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

/**
 * The HARD ceiling Chromium puts on `allowCredentials`. Hand it more and
 * `navigator.credentials.get()` throws, before it ever consults an
 * authenticator:
 *
 *   RangeError: The 'allowCredentials' attribute exceeds the maximum allowed
 *   size (64).
 *
 * WHY THIS CONSTANT IS HERE AND NOT ONLY ON THE SERVER. This was measured live:
 * one dev user had accumulated 65 credentials, assert/begin returned all 65, and
 * so the FIRST WRITE OF EVERY SESSION died with "PASSKEY STEP-UP FAILED — this
 * change was not saved", on every device, permanently. The server now caps the
 * list (backend internal/server/auth_webauthn.go, maxAllowCredentials), which is
 * the real fix; this is the client refusing to be the thing that explodes when a
 * server — an older build, a future regression — hands it a list it cannot use.
 *
 * The slice keeps the FIRST 64 because the server's contract is NEWEST FIRST
 * (pinned in the wire contract at the top of this file). If a server ever
 * violated that ordering the effect is bounded and recoverable: this device
 * proves none of the offered ids, so stepUp falls through to ENROLMENT, and the
 * credential it enrols is by definition the newest.
 */
export const MAX_ALLOW_CREDENTIALS = 64;

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

/** The `name` of a thrown DOMException / Error, or "" for anything else. */
function errName(err: unknown): string {
  const n =
    typeof err === "object" && err !== null
      ? (err as { name?: unknown }).name
      : undefined;
  return typeof n === "string" ? n : "";
}

/**
 * Does this `navigator.credentials.get()` rejection mean "THIS DEVICE CANNOT
 * PROVE ANY OF THE CREDENTIALS IT WAS OFFERED"? Those are recoverable — the
 * remedy is to ENROL a credential for this device — as opposed to a genuine
 * transport/programming fault, which must surface.
 *
 * Two names qualify, for two different reasons:
 *
 *   NotAllowedError — the spec deliberately collapses several outcomes into this
 *     ONE error so a site cannot enumerate which passkeys a device holds: the
 *     user refused, the prompt timed out, or — the case that matters here — the
 *     authenticator holds none of the credentials in allowCredentials. Callers
 *     therefore cannot tell "wrong device" from "user said no", and must pick a
 *     remedy that is safe for both.
 *
 *   RangeError — the browser refused to even LOOK at the list because it was
 *     longer than {@link MAX_ALLOW_CREDENTIALS}. This is emphatically not "your
 *     passkey was refused": it is "the server sent a list this browser cannot
 *     evaluate", and it used to be turned into a hard StepUpFailedError, which
 *     made EVERY WRITE IN THE PRODUCT impossible for a user with 65 credentials
 *     — the defect this branch exists to survive. Enrolling is the correct
 *     recovery, because the newly-enrolled credential is the newest and a capped
 *     newest-first list is therefore guaranteed to contain it.
 *
 * Note this is name-based, not `instanceof DOMException`-based, on purpose:
 * RangeError is a plain ECMAScript error, not a DOMException, and jsdom's
 * DOMException is not the same class as the one a real browser throws.
 */
function isDeviceCannotProve(err: unknown): boolean {
  const name = errName(err);
  return name === "NotAllowedError" || name === "RangeError";
}

function ceremonyFailure(what: string, err: unknown): StepUpFailedError {
  const msg = err instanceof Error ? `${err.name}: ${err.message}` : String(err);
  return new StepUpFailedError(`${what} was not completed (${msg})`);
}

/**
 * Describe a rejection that was deliberately SWALLOWED so the ceremony could
 * fall through to enrolment, for use in the terminal failure message.
 *
 * This exists because the previous message — a bare "the passkey confirmation
 * returned no assertion" — threw away the one fact that distinguishes a refused
 * prompt (NotAllowedError) from an unusable allow list (RangeError). Those need
 * completely different remedies, and discarding the name cost this project about
 * 25 minutes and five people re-deriving it from the browser console. NAME THE
 * ERROR.
 */
function describeSwallowed(err: unknown, offered: number): string {
  const parts = [`${offered} credential(s) were offered`];
  if (err !== null && err !== undefined) {
    const name = errName(err) || "unknown";
    const message = err instanceof Error ? err.message : String(err);
    parts.push(`the browser's last error was ${name}: ${message}`);
  }
  return ` — ${parts.join("; ")}`;
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

  // The rejection we deliberately swallowed in order to fall through to
  // enrolment, plus how many ids we offered when it happened. Kept so the
  // TERMINAL failure can name them — see describeSwallowed.
  let swallowed: unknown = null;
  let offered = 0;

  // Ask the authenticator to prove one of the credentials the server lists.
  // Returns null when this device holds NONE of them (see enrolment below).
  const tryAssert = async (
    begin: AssertBegin,
  ): Promise<PublicKeyCredential | null> => {
    // TRUNCATE BEFORE WE ASK. Chromium throws RangeError rather than evaluate
    // more than MAX_ALLOW_CREDENTIALS entries, so a longer list is not "a big
    // list", it is a list that cannot be used at all. First 64 == newest 64 per
    // the server's newest-first contract.
    const all = begin.allow_credentials ?? [];
    const allow = all.slice(0, MAX_ALLOW_CREDENTIALS);
    offered = allow.length;
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
      // Two recoverable shapes, one branch: "this authenticator holds none of
      // these" (NotAllowedError, which also covers a refusal — the browser
      // refuses to tell us which) and "this list is too long for me to even
      // look at" (RangeError). Both mean THIS DEVICE CANNOT PROVE ANY OF THESE,
      // and for both the remedy is to enrol a credential for this device. Only
      // a RangeError used to reach the caller as a hard failure, and that is
      // precisely how a 65-credential user lost the ability to write anything.
      if (isDeviceCannotProve(err)) {
        swallowed = err;
        return null;
      }
      throw ceremonyFailure("the passkey confirmation", err);
    }
  };

  let begin = await assertBegin(deps);
  offered = (begin.allow_credentials ?? []).length;
  let got = offered === 0 ? null : await tryAssert(begin);

  if (!got) {
    // ENROL THIS DEVICE. Two different situations arrive here and the remedy is
    // the same, which is why they share a branch:
    //
    //   (a) the user has no passkey at all (allow_credentials was empty);
    //   (b) the user HAS passkeys, but none of them live on THIS device —
    //       they enrolled on their laptop and are now signing in on their phone;
    //   (c) the browser refused to evaluate the offered list AT ALL (RangeError
    //       above MAX_ALLOW_CREDENTIALS). Same remedy for the same reason: the
    //       credential we enrol is the newest, and a capped newest-first list is
    //       therefore guaranteed to contain it.
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
    // NAME THE BROWSER'S ERROR. This is the last line of the only diagnosis the
    // next person gets, and "returned no assertion" on its own is indistinguish-
    // able between a refused prompt and an allow list the browser would not even
    // read (see describeSwallowed).
    throw new StepUpFailedError(
      `the passkey confirmation returned no assertion${describeSwallowed(swallowed, offered)}`,
    );
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
