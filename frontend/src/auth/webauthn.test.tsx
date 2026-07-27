import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  waitFor,
  fireEvent,
  act,
} from "@testing-library/react";
import { App, bearerFetch, type AuthGateConfig } from "../App";
import {
  AuthProvider,
  useAuth,
  AUTH_STORAGE_KEY,
  type AuthContextValue,
} from "./AuthContext";
import {
  stepUp,
  StepUpUnsupportedError,
  StepUpFailedError,
  toB64URL,
  fromB64URL,
} from "./webauthn";

/**
 * THE WEBAUTHN STEP-UP, end to end in jsdom.
 *
 * WHAT THIS PROTECTS. A `POST /auth/login` token has no `amr` claim and every
 * company's Owner level is seeded requires_webauthn, so the first WRITE of a
 * browser session is answered 403 STEPUP_REQUIRED. App's transport is supposed
 * to run a passkey ceremony and REPLAY the request with the new token, exactly
 * once, joining concurrent 403s into ONE ceremony (cored's challenge store is
 * single-use per user, so two overlapping ceremonies really do break).
 *
 * NON-VACUITY. Every case below states, in situ, the regression it fails on.
 * The shared guards are: the ceremony counter (a second ceremony fails the
 * concurrency case), the recorded Authorization headers (a retry that reuses
 * the stale token fails the retry case) and the fact that the stub transport
 * 403s EVERY write for the old token (so a test that silently skipped the
 * step-up could never reach the shell).
 */

afterEach(() => {
  cleanup();
  restoreCredentials();
});

beforeEach(() => {
  globalThis.localStorage?.clear();
  globalThis.sessionStorage?.clear();
});

// ---------------------------------------------------------------------------
// A Response stub that supports clone() — the interceptor reads the 403 body
// through a clone so the caller's stream stays consumable.
// ---------------------------------------------------------------------------

function respond(status: number, body?: unknown): Response {
  const text = body === undefined ? "" : JSON.stringify(body);
  const make = (): Response =>
    ({
      ok: status >= 200 && status < 300,
      status,
      statusText: `status ${status}`,
      text: async () => text,
      json: async () => JSON.parse(text),
      clone: () => make(),
    }) as unknown as Response;
  return make();
}

interface Call {
  url: string;
  method: string;
  auth: string | undefined;
  body: unknown;
}

interface Transport {
  fetchImpl: typeof fetch;
  calls: Call[];
  urls: string[];
  of(fragment: string): Call[];
}

function transport(route: (call: Call) => Response): Transport {
  const calls: Call[] = [];
  const fetchImpl = (async (
    input: RequestInfo | URL,
    init: RequestInit = {},
  ) => {
    const url =
      typeof input === "string"
        ? input
        : input instanceof URL
          ? input.toString()
          : (input as Request).url;
    const h = init.headers;
    const auth =
      h instanceof Headers
        ? (h.get("Authorization") ?? undefined)
        : (h as Record<string, string> | undefined)?.["Authorization"];
    let body: unknown = undefined;
    if (typeof init.body === "string") {
      try {
        body = JSON.parse(init.body);
      } catch {
        body = init.body;
      }
    }
    const call: Call = { url, method: init.method ?? "GET", auth, body };
    calls.push(call);
    return route(call);
  }) as unknown as typeof fetch;
  return {
    fetchImpl,
    calls,
    get urls() {
      return calls.map((c) => c.url);
    },
    of: (fragment: string) => calls.filter((c) => c.url.includes(fragment)),
  };
}

// ---------------------------------------------------------------------------
// A stubbed navigator.credentials. It counts ceremonies, so "exactly one
// ceremony" is an assertion and not a hope.
// ---------------------------------------------------------------------------

const CRED_ID = new Uint8Array([1, 2, 3, 4]);

interface CredStub {
  create: ReturnType<typeof vi.fn>;
  get: ReturnType<typeof vi.fn>;
}

function bytes(...v: number[]): Uint8Array {
  return new Uint8Array(v);
}

function fakeAssertion(): PublicKeyCredential {
  return {
    rawId: CRED_ID,
    type: "public-key",
    response: {
      clientDataJSON: bytes(9, 9),
      authenticatorData: bytes(7, 7, 7),
      signature: bytes(5, 5),
    },
  } as unknown as PublicKeyCredential;
}

function fakeAttestation(): PublicKeyCredential {
  return {
    rawId: CRED_ID,
    type: "public-key",
    response: {
      clientDataJSON: bytes(8, 8),
      getAuthenticatorData: () => bytes(6, 6, 6),
    },
  } as unknown as PublicKeyCredential;
}

let hadCredentials = false;

/** Install a stub `navigator.credentials`; returns the two spies. */
function installCredentials(
  opts: { getFails?: boolean } = {},
): CredStub {
  const stub: CredStub = {
    create: vi.fn(async () => fakeAttestation()),
    get: vi.fn(async () => {
      if (opts.getFails) {
        // What Chromium throws when the operator dismisses the prompt.
        const err = new Error("The operation either timed out or was not allowed.");
        err.name = "NotAllowedError";
        throw err;
      }
      return fakeAssertion();
    }),
  };
  hadCredentials = "credentials" in navigator;
  Object.defineProperty(navigator, "credentials", {
    value: stub,
    configurable: true,
    writable: true,
  });
  return stub;
}

function restoreCredentials(): void {
  if (hadCredentials) return;
  if ("credentials" in navigator) {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (navigator as any).credentials;
  }
}

// ---------------------------------------------------------------------------
// The routed stack: login -> 403 STEPUP_REQUIRED on every write -> ceremony ->
// the SAME write succeeds with the stepped-up token.
// ---------------------------------------------------------------------------

const OWNER = { id: "user-a", subject: "browser-dev-owner", display_name: "", email: "" };
const LOGIN_TOKEN = "token-login-no-amr";
const STEPPED_TOKEN = "token-with-amr-webauthn";

interface StackOpts {
  /** No passkey on file yet: assert/begin lists nothing until registration. */
  needsRegistration?: boolean;
  /** assert/finish answers 501 (no dev signer) instead of minting a token. */
  assertFinish501?: boolean;
}

function stack(opts: StackOpts = {}) {
  let registered = !opts.needsRegistration;
  const t = transport((call) => {
    const { url } = call;
    if (url.endsWith("/auth/login")) {
      return respond(200, {
        token: LOGIN_TOKEN,
        token_type: "Bearer",
        expires_in: 3600,
        user: OWNER,
      });
    }
    if (url.endsWith("/auth/webauthn/assert/begin")) {
      return respond(200, {
        challenge: toB64URL(bytes(1, 2, 3)),
        rp_id: "localhost",
        allow_credentials: registered ? [toB64URL(CRED_ID)] : [],
      });
    }
    if (url.endsWith("/auth/webauthn/register/begin")) {
      return respond(200, {
        challenge: toB64URL(bytes(4, 5, 6)),
        rp_id: "localhost",
        user_id: "3f1d0a2e-0000-4000-8000-000000000001",
      });
    }
    if (url.endsWith("/auth/webauthn/register/finish")) {
      registered = true;
      return respond(200, { credential_id: toB64URL(CRED_ID), registered: true });
    }
    if (url.endsWith("/auth/webauthn/assert/finish")) {
      if (opts.assertFinish501) {
        return respond(501, { error: "not available without the dev signer" });
      }
      return respond(200, {
        token: STEPPED_TOKEN,
        token_type: "Bearer",
        expires_in: 3600,
      });
    }
    // Everything cored-side: WRITES need amr, reads do not.
    const write = call.method !== "GET";
    if (write && call.auth !== `Bearer ${STEPPED_TOKEN}`) {
      return respond(403, {
        error: "webauthn step-up required",
        code: "STEPUP_REQUIRED",
      });
    }
    if (url.includes("/v1/companies") && call.method === "POST") {
      return respond(201, { id: "co-1", name: "My Company" });
    }
    if (url.includes("/v1/companies/")) return respond(200, { id: "co-1", name: "My Company" });
    return respond(200, {});
  });
  return t;
}

function gate(t: Transport): AuthGateConfig {
  return { baseUrl: "", fetchImpl: t.fetchImpl };
}

function signIn(password = "pw") {
  fireEvent.change(screen.getByLabelText("User name"), {
    target: { value: "browser-dev-owner" },
  });
  fireEvent.change(screen.getByLabelText("Password"), {
    target: { value: password },
  });
  fireEvent.click(screen.getByRole("button", { name: "Sign In" }));
}

const deps = (t: Transport) => ({
  baseUrl: "",
  token: LOGIN_TOKEN,
  fetchImpl: t.fetchImpl,
});

/**
 * A window onto the live auth context, for the concurrency case: it needs to
 * fire two writes through the REAL provider + interceptor at the same instant,
 * which no rendered screen exposes.
 */
let ctx: AuthContextValue | null = null;
function Probe() {
  ctx = useAuth();
  return null;
}

// ---------------------------------------------------------------------------
// The ceremony client
// ---------------------------------------------------------------------------

describe("auth/webauthn — the ceremony client", () => {
  it("encodes base64url WITHOUT padding and round-trips (cored 400s on '=')", () => {
    // NON-VACUOUS: Go's RawURLEncoding rejects padded/standard base64 with
    // "invalid base64url in <field>". A regression to btoa() alone puts '='
    // and '+'/'/' on the wire and this fails.
    const raw = bytes(251, 255, 190, 0, 1);
    const enc = toB64URL(raw);
    expect(enc).not.toContain("=");
    expect(enc).not.toContain("+");
    expect(enc).not.toContain("/");
    expect(Array.from(new Uint8Array(fromB64URL(enc)))).toEqual(Array.from(raw));
  });

  it("throws StepUpUnsupportedError when the browser has no navigator.credentials", async () => {
    // NON-VACUOUS: the default jsdom navigator has no `credentials`, so this is
    // the real path. If stepUp() ever dereferenced it blindly it would reject
    // with a TypeError instead — and the caller could not tell "this browser
    // cannot" from "your passkey was refused".
    restoreCredentials();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (navigator as any).credentials;
    const t = stack();
    await expect(stepUp(deps(t))).rejects.toBeInstanceOf(StepUpUnsupportedError);
    // …and it did so WITHOUT burning a server challenge.
    expect(t.calls).toHaveLength(0);
  });

  it("registers FIRST when assert/begin lists no credentials, then asserts with a FRESH challenge", async () => {
    const creds = installCredentials();
    const t = stack({ needsRegistration: true });

    await expect(stepUp(deps(t))).resolves.toBe(STEPPED_TOKEN);

    // NON-VACUOUS: the exact call ORDER is the assertion. cored's challenge is
    // single-use (challenges.Take), so re-using the first assert/begin
    // challenge after registering — i.e. dropping the SECOND assert/begin —
    // would 400 against the real server. That regression changes this list.
    expect(t.urls).toEqual([
      "/auth/webauthn/assert/begin",
      "/auth/webauthn/register/begin",
      "/auth/webauthn/register/finish",
      "/auth/webauthn/assert/begin",
      "/auth/webauthn/assert/finish",
    ]);
    expect(creds.create).toHaveBeenCalledTimes(1);
    expect(creds.get).toHaveBeenCalledTimes(1);

    // The registration offered BOTH algorithms the Go verifier supports.
    const created = creds.create.mock.calls[0]![0] as CredentialCreationOptions;
    expect(
      created.publicKey!.pubKeyCredParams!.map((p) => p.alg).sort((a, b) => a - b),
    ).toEqual([-8, -7]); // EdDSA + ES256 — the two internal/webauthn verifies
    expect(created.publicKey!.rp.id).toBe("localhost");
    expect(created.publicKey!.attestation).toBe("none");

    // register/finish sent the AUTHENTICATOR DATA (not an attestation object)
    // and the credential id, base64url-encoded.
    const reg = t.of("register/finish")[0]!.body as Record<string, string>;
    expect(reg.credential_id_b64).toBe(toB64URL(CRED_ID));
    expect(reg.authenticator_data_b64).toBe(toB64URL(bytes(6, 6, 6)));
    expect(reg.client_data_json_b64).toBe(toB64URL(bytes(8, 8)));

    // assert/finish sent all four fields, including the signature.
    const fin = t.of("assert/finish")[0]!.body as Record<string, string>;
    expect(fin).toEqual({
      credential_id_b64: toB64URL(CRED_ID),
      client_data_json_b64: toB64URL(bytes(9, 9)),
      authenticator_data_b64: toB64URL(bytes(7, 7, 7)),
      signature_b64: toB64URL(bytes(5, 5)),
    });
  });

  it("skips registration when a credential already exists", async () => {
    const creds = installCredentials();
    const t = stack();
    await expect(stepUp(deps(t))).resolves.toBe(STEPPED_TOKEN);
    // NON-VACUOUS: enrolling a SECOND passkey on every write would be the
    // regression; it shows up here as a create() call and two extra routes.
    expect(creds.create).not.toHaveBeenCalled();
    expect(t.urls).toEqual([
      "/auth/webauthn/assert/begin",
      "/auth/webauthn/assert/finish",
    ]);
  });

  it("ENROLS THIS DEVICE when the user's existing passkeys live elsewhere", async () => {
    // The bug this pins, found by running the e2e against a database that
    // already held a credential: assert/begin returns a NON-EMPTY
    // allow_credentials (the user enrolled on another device), so the old code
    // skipped registration, called get(), and got NotAllowedError because THIS
    // authenticator holds none of those keys. Step-up then failed forever, so
    // the user could never write again from the second device — with no way out
    // from inside the app. Passkeys are per-authenticator: "this user has a
    // credential somewhere" is not the same question as "this device can prove
    // one".
    //
    // NON-VACUOUS: revert stepUp to throw on NotAllowedError instead of falling
    // back, and this rejects instead of resolving; drop the SECOND assert/begin
    // and the url sequence below fails (the first challenge is single-use, so
    // reusing it would 400 against a real server).
    let getCalls = 0;
    const creds = installCredentials();
    creds.get = vi.fn(async () => {
      getCalls += 1;
      if (getCalls === 1) {
        // Chromium reports "you hold none of these" and "the user refused" with
        // the SAME error, on purpose, so a site cannot enumerate your passkeys.
        const err = new Error("The operation either timed out or was not allowed.");
        err.name = "NotAllowedError";
        throw err;
      }
      return fakeAssertion();
    });

    const t = stack();
    await expect(stepUp(deps(t))).resolves.toBe(STEPPED_TOKEN);
    expect(creds.create).toHaveBeenCalledTimes(1);
    expect(t.urls).toEqual([
      "/auth/webauthn/assert/begin",
      "/auth/webauthn/register/begin",
      "/auth/webauthn/register/finish",
      "/auth/webauthn/assert/begin",
      "/auth/webauthn/assert/finish",
    ]);
  });

  it("fails cleanly when even the enrolment prompt is refused, and explains a 501", async () => {
    // Refusing BOTH prompts must still terminate with a clear error rather than
    // looping: get() -> NotAllowedError -> enrol -> create() also refused.
    const creds = installCredentials({ getFails: true });
    creds.create = vi.fn(async () => {
      const err = new Error("The operation either timed out or was not allowed.");
      err.name = "NotAllowedError";
      throw err;
    });
    const cancelled = stack();
    const err = await stepUp(deps(cancelled)).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(StepUpFailedError);
    expect((err as Error).message).toMatch(/NotAllowedError/);

    // 501: cored has no dev signer, production step-up is the IdP's job. The
    // message must SAY so — a bare "failed" would send someone hunting a bug.
    installCredentials();
    const noSigner = stack({ assertFinish501: true });
    const err2 = await stepUp(deps(noSigner)).catch((e: unknown) => e);
    expect(err2).toBeInstanceOf(StepUpFailedError);
    expect((err2 as Error).message).toMatch(/501|identity provider/);
  });
});

// ---------------------------------------------------------------------------
// The transport interceptor (App) — the part that makes writes work
// ---------------------------------------------------------------------------

describe("the 403 STEPUP_REQUIRED retry", () => {
  it("runs ONE ceremony, retries ONCE, and the retry carries the NEW token", async () => {
    const creds = installCredentials();
    const t = stack();
    // No `client` prop: this drives the REAL REST client over the stub
    // transport, so the company bootstrap's POST /v1/companies is the write
    // that trips the 403 — the same shape as the F8 screen's ledger POST.
    render(<App auth={gate(t)} />);
    signIn();

    await waitFor(() =>
      expect(t.of("/auth/webauthn/assert/finish")).toHaveLength(1),
    );
    await waitFor(() => expect(screen.getByText("Gateway of Tally")).toBeTruthy());

    const writes = t.calls.filter(
      (c) => c.url.includes("/v1/companies") && c.method === "POST",
    );
    // NON-VACUOUS, three ways:
    //  - exactly TWO attempts: the 403 and ONE replay (a loop makes it >2);
    //  - the first carried the amr-LESS login token and the second the STEPPED
    //    one, so a retry that reused the stale token (reading React state that
    //    has not re-rendered yet) fails right here;
    //  - the stub 403s every write for the login token, so the shell could not
    //    have bootstrapped at all without a real step-up.
    expect(writes).toHaveLength(2);
    expect(writes[0]!.auth).toBe(`Bearer ${LOGIN_TOKEN}`);
    expect(writes[1]!.auth).toBe(`Bearer ${STEPPED_TOKEN}`);
    expect(creds.get).toHaveBeenCalledTimes(1);

    // The stepped-up token REPLACED the session token and was re-persisted, so
    // a reload keeps the ability to write.
    const raw = globalThis.sessionStorage?.getItem(AUTH_STORAGE_KEY);
    expect(JSON.parse(raw!).token).toBe(STEPPED_TOKEN);
    expect(JSON.parse(raw!).user.subject).toBe("browser-dev-owner");

    // The ceremony itself did NOT go through the interceptor (no recursion):
    // every /auth/webauthn/* call was made exactly once.
    expect(t.of("/auth/webauthn/assert/begin")).toHaveLength(1);
  });

  it("joins TWO concurrent 403s into exactly ONE ceremony", async () => {
    const creds = installCredentials();
    const t = stack();
    // Two writes fired SIMULTANEOUSLY — the shape the F8 screen produces when
    // it creates the party ledger and the tax ledger before posting. <App/>
    // only issues ONE write during bootstrap, so this drives the real provider
    // and the real interceptor directly.
    render(
      <AuthProvider baseUrl="" fetchImpl={t.fetchImpl}>
        <Probe />
      </AuthProvider>,
    );
    await act(async () => {
      await ctx!.signIn("browser-dev-owner", "pw");
    });
    const authed = bearerFetch(
      ctx!.session!.token,
      () => {},
      () => ctx!.stepUp(),
      () => ctx!.currentToken(),
      t.fetchImpl,
    );
    const write = () =>
      authed("/v1/companies/co-1/ledgers", {
        method: "POST",
        body: JSON.stringify({ name: "Acme Traders" }),
      });

    let both: Response[] = [];
    await act(async () => {
      both = await Promise.all([write(), write()]);
    });

    // Both writes ultimately SUCCEEDED (the stub 403s every write that does not
    // carry the stepped-up token), so neither was abandoned by the join…
    expect(both.map((r) => r.status)).toEqual([201, 201]);
    // …and there was exactly ONE ceremony. NON-VACUOUS: cored's challenge store
    // is single-use PER USER (challenges.Take), so two overlapping begins make
    // one finish 400 against the real server — a bug that a sequential test
    // cannot see. Drop the in-flight memo in AuthContext and this reads 2/2.
    expect(t.of("/auth/webauthn/assert/begin")).toHaveLength(1);
    expect(t.of("/auth/webauthn/assert/finish")).toHaveLength(1);
    expect(creds.get).toHaveBeenCalledTimes(1);
    // Four cored writes in total: two 403s and two replays.
    expect(t.of("/ledgers")).toHaveLength(4);
    expect(t.of("/ledgers").map((c) => c.auth)).toEqual([
      `Bearer ${LOGIN_TOKEN}`,
      `Bearer ${LOGIN_TOKEN}`,
      `Bearer ${STEPPED_TOKEN}`,
      `Bearer ${STEPPED_TOKEN}`,
    ]);
  });

  it("uses a REAL Response: the body survives the STEPUP_REQUIRED sniff (clone)", async () => {
    // WHY A REAL Response. The `respond()` helper above returns a hand-rolled
    // object whose text() is idempotent and whose clone() is a fresh independent
    // object — so it CANNOT model a consumed stream, and every clone() assertion
    // written against it is vacuous. Adversarial review proved exactly that:
    // mutating the interceptor from `res.clone().text()` to `res.text()` left the
    // whole suite green. A genuine `new Response(...)` has real single-use body
    // semantics, so this case actually bites.
    //
    // NON-VACUOUS: drop the .clone() in App.tsx's isStepUpRequired and the
    // caller receives a Response whose body is already drained — bodyUsed true,
    // and json() rejects with "body stream already read".
    const calls: string[] = [];
    const realFetch = (async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : String(input);
      calls.push(url);
      // A plain 403 with NO step-up code: the interceptor must sniff the body,
      // decide it is not a step-up, and hand the UNTOUCHED response back.
      return new Response(JSON.stringify({ error: "not permitted" }), {
        status: 403,
        headers: { "Content-Type": "application/json" },
      });
    }) as typeof fetch;

    let ceremonies = 0;
    const authed = bearerFetch(
      LOGIN_TOKEN,
      () => {},
      async () => {
        ceremonies += 1;
      },
      () => LOGIN_TOKEN,
      realFetch,
    );

    const res = await authed("/v1/companies/co-1/ledgers", { method: "POST" });

    expect(res.status).toBe(403);
    // The caller can STILL read the body — this is the whole point of clone().
    expect(res.bodyUsed).toBe(false);
    await expect(res.json()).resolves.toEqual({ error: "not permitted" });
    // A plain 403 is the PDP saying no; it must never provoke a passkey prompt.
    expect(ceremonies).toBe(0);
    expect(calls).toHaveLength(1);
  });

  it("retries AT MOST once even when the RETRY is also refused", async () => {
    // The gap adversarial review found: the existing failure case exercises a
    // ceremony that never SUCCEEDS. Nothing covered a ceremony that succeeds and
    // whose replay is still 403 — the shape that would loop forever if the
    // one-shot guard regressed (step up, retry, 403, step up, retry, …).
    //
    // NON-VACUOUS: make the interceptor recurse instead of returning the retry's
    // response and this never terminates (vitest times the case out) — and the
    // two exact counts below fail long before that.
    let writes = 0;
    let ceremonies = 0;
    const alwaysRefuses = (async () =>
      new Response(
        JSON.stringify({ code: "STEPUP_REQUIRED", error: "webauthn step-up required" }),
        { status: 403, headers: { "Content-Type": "application/json" } },
      )) as typeof fetch;

    const counting = (async (input: RequestInfo | URL, init?: RequestInit) => {
      writes += 1;
      return alwaysRefuses(input, init);
    }) as typeof fetch;

    const authed = bearerFetch(
      LOGIN_TOKEN,
      () => {},
      async () => {
        // The ceremony SUCCEEDS every time; only the write stays refused.
        ceremonies += 1;
      },
      () => STEPPED_TOKEN,
      counting,
    );

    const res = await authed("/v1/companies/co-1/ledgers", { method: "POST" });

    expect(writes).toBe(2); // the original attempt + exactly ONE replay
    expect(ceremonies).toBe(1); // and exactly ONE ceremony, not one per attempt
    expect(res.status).toBe(403); // the retry's 403 is returned unchanged
    await expect(res.json()).resolves.toMatchObject({ code: "STEPUP_REQUIRED" });
  });

  it("a FAILED ceremony surfaces the original 403 and does not loop", async () => {
    installCredentials({ getFails: true });
    const t = stack();
    render(<App auth={gate(t)} />);
    signIn();

    await waitFor(() =>
      expect(t.of("/auth/webauthn/assert/begin")).toHaveLength(1),
    );
    // Give any (wrong) retry loop time to show itself.
    await new Promise((r) => setTimeout(r, 50));

    const writes = t.calls.filter(
      (c) => c.url.includes("/v1/companies") && c.method === "POST",
    );
    // NON-VACUOUS: exactly ONE attempt — the 403 was returned to the caller
    // unmasked and unretried. A retry-anyway would make this 2; a loop, many.
    expect(writes).toHaveLength(1);
    expect(t.of("/auth/webauthn/assert/finish")).toHaveLength(0);
    // The session token was NOT replaced by a failed ceremony.
    const raw = globalThis.sessionStorage?.getItem(AUTH_STORAGE_KEY);
    expect(JSON.parse(raw!).token).toBe(LOGIN_TOKEN);

    // …and the failure is VISIBLE in the chrome, not a silently dead keystroke.
    await waitFor(() => expect(screen.getByTestId("stepup-failed")).toBeTruthy());
    expect(screen.getByTestId("stepup-failed").textContent).toMatch(
      /PASSKEY STEP-UP FAILED/,
    );
  });

  it("a plain 403 (no STEPUP_REQUIRED code) never triggers a passkey prompt", async () => {
    const creds = installCredentials();
    const t = transport((call) => {
      if (call.url.endsWith("/auth/login")) {
        return respond(200, {
          token: LOGIN_TOKEN,
          token_type: "Bearer",
          expires_in: 3600,
          user: OWNER,
        });
      }
      return respond(403, { error: "forbidden: level denies Create" });
    });
    render(<App auth={gate(t)} />);
    signIn();
    await waitFor(() =>
      expect(t.calls.some((c) => c.url.includes("/v1/companies"))).toBe(true),
    );
    await new Promise((r) => setTimeout(r, 50));

    // NON-VACUOUS: an interceptor that keyed on the STATUS alone would prompt
    // for a passkey on every authorization denial — user-hostile and useless.
    expect(creds.get).not.toHaveBeenCalled();
    expect(t.of("/auth/webauthn")).toHaveLength(0);
    expect(screen.queryByTestId("stepup-failed")).toBeNull();
  });

  it("a 401 still bounces to the login screen (unchanged by the step-up path)", async () => {
    const creds = installCredentials();
    const t = transport((call) => {
      if (call.url.endsWith("/auth/login")) {
        return respond(200, {
          token: LOGIN_TOKEN,
          token_type: "Bearer",
          expires_in: 3600,
          user: OWNER,
        });
      }
      return respond(401, { error: "unauthorized" });
    });
    render(<App auth={gate(t)} />);
    signIn();
    await waitFor(() => expect(screen.getByText("Gateway of Tally")).toBeTruthy());

    // NON-VACUOUS: the 401 handling sits in the SAME wrapper the step-up retry
    // was added to. If the new early-return ordering swallowed it, the shell
    // would keep rendering against a dead session and this never resolves.
    await waitFor(() => expect(screen.getByTestId("login-screen")).toBeTruthy());
    expect(globalThis.sessionStorage?.getItem(AUTH_STORAGE_KEY)).toBeNull();
    expect(creds.get).not.toHaveBeenCalled();
  });
});
