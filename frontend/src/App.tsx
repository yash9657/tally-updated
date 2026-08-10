import { useEffect, useMemo, useState } from "react";
import { AppShell } from "./app/AppShell";
import {
  COMPANY_STORAGE_KEY,
  CompanyProvider,
  rememberChosenCompanyId,
  type CompanyInfo,
} from "./app/CompanyContext";
import { createMockClient, createRestClient } from "./api";
import type { CoredClient } from "./api";
import { AuthProvider, useAuth } from "./auth/AuthContext";
import { LoginScreen } from "./auth/LoginScreen";

/**
 * Composition root. Wires the {@link CoredClient} and the active company into
 * the {@link CompanyProvider}, then renders the {@link AppShell} (which owns the
 * navigation machine, key capture, chrome, and screen routing).
 *
 * TWO COMPOSITIONS, chosen by whether an AUTH GATE is configured:
 *
 *  - GATED (live mode, VITE_LIVE=1, or an explicit `auth` prop): an
 *    {@link AuthProvider} wraps everything; with no session the app renders the
 *    {@link LoginScreen} INSTEAD of the shell; with a session it renders the
 *    shell over a REST client whose fetch attaches `Authorization: Bearer
 *    <session.token>` and calls `onUnauthorized()` on any 401.
 *  - UNGATED (mock/test mode): byte-identical to the pre-login behaviour — the
 *    in-memory mock client, no provider, no login. Tests render <App /> and get
 *    the shell straight away.
 *
 * The `auth` prop is the INJECTABLE SEAM the auth tests drive: without it they
 * would have to stub `import.meta.env.VITE_LIVE`, and any test that failed to
 * would pass VACUOUSLY against the ungated composition.
 */
export interface AuthGateConfig {
  /** Base URL for `/auth/*` and `/v1/*`; "" (same-origin) behind the dev proxy. */
  baseUrl: string;
  /** Injected fetch for the auth calls and the REST client (tests). */
  fetchImpl?: typeof fetch;
}

export function App({
  client,
  company,
  auth,
}: {
  client?: CoredClient;
  company?: CompanyInfo;
  auth?: AuthGateConfig;
} = {}) {
  const gate = useMemo<AuthGateConfig | null>(
    () => auth ?? defaultAuthGate(),
    [auth],
  );

  if (gate) {
    return (
      <AuthProvider baseUrl={gate.baseUrl} fetchImpl={gate.fetchImpl}>
        <AuthGatedApp gate={gate} client={client} company={company} />
      </AuthProvider>
    );
  }
  return <UngatedApp client={client} company={company} />;
}

/**
 * Mock/test composition: no identity layer at all. Unchanged from before the
 * login landed, on purpose — the existing shell suites depend on it.
 */
function UngatedApp({
  client,
  company,
}: {
  client?: CoredClient;
  company?: CompanyInfo;
}) {
  const resolvedClient = useMemo<CoredClient>(
    () => client ?? defaultClient(),
    [client],
  );
  // An explicit company prop is the deterministic test/path: bootstrap stays
  // OFF and no company is auto-created. With no prop we bootstrap a real company
  // against the live client (persisted/minted), gating screens until it's ready.
  return (
    <CompanyProvider
      client={resolvedClient}
      initialCompany={company ?? null}
      bootstrap={!company}
    >
      <AppShell />
    </CompanyProvider>
  );
}

/**
 * Live composition. Nothing below the gate renders until somebody has signed
 * in, so "who am I" is a real per-session answer instead of one hardcoded dev
 * subject.
 *
 * The CompanyProvider is KEYED ON THE USER ID: signing in as a different
 * subject must remount the bootstrap rather than reuse the previous user's
 * hydrated company. (Sign-out additionally forgets the persisted company id —
 * see AuthContext.signOut.)
 */
function AuthGatedApp({
  gate,
  client,
  company,
}: {
  gate: AuthGateConfig;
  client?: CoredClient;
  company?: CompanyInfo;
}) {
  const auth = useAuth();
  const session = auth?.session ?? null;
  const onUnauthorized = auth?.onUnauthorized;
  const requestStepUp = auth?.stepUp;
  const currentToken = auth?.currentToken;

  const resolvedClient = useMemo<CoredClient | null>(() => {
    if (!session) return null;
    // An explicit client prop bypasses the transport entirely (test seam).
    if (client) return client;
    return createRestClient({
      baseUrl: gate.baseUrl,
      fetchImpl: bearerFetch(
        session.token,
        () => onUnauthorized?.(),
        () =>
          requestStepUp
            ? requestStepUp()
            : Promise.reject(new Error("no auth provider")),
        () => currentToken?.() ?? null,
        gate.fetchImpl,
      ),
    });
  }, [
    client,
    currentToken,
    gate.baseUrl,
    gate.fetchImpl,
    onUnauthorized,
    requestStepUp,
    session,
  ]);

  // D25 — SIGNING IN MUST CLAIM A PENDING INVITE. Run it BEFORE the company
  // bootstrap, and gate the shell on it. See claimInvitesOnSignIn.
  const claimed = useSessionBootstrap(
    resolvedClient,
    session?.user.id || session?.user.subject || "",
  );

  if (!session || !resolvedClient) return <LoginScreen />;
  if (!claimed) {
    return (
      <section
        data-testid="session-bootstrap"
        aria-label="Opening your companies"
        className="flex h-full w-full items-center justify-center bg-tally-bg p-4 font-mono text-tally-field"
      >
        Opening your companies…
      </section>
    );
  }

  return (
    <CompanyProvider
      key={session.user.id || session.user.subject}
      client={resolvedClient}
      initialCompany={company ?? null}
      bootstrap={!company}
    >
      <AppShell />
    </CompanyProvider>
  );
}

/**
 * SESSION BOOTSTRAP — the call that turns an invite into a membership (D25).
 *
 * ==========================================================================
 * THE DEFECT
 * ==========================================================================
 * `GET /v1/me` is the ONLY seam in the whole product that converts a pending
 * invite into a membership: the server never writes another person's identity
 * row, so `handleMe` claims first and lists second, and the invitee's own next
 * call is what materialises their access. The browser called it from EXACTLY ONE
 * place — the F3 Select Company screen — which the invited user has no reason to
 * open and, worse, would not be helped by if they did.
 *
 * What actually happened to somebody who was invited and then signed in: the
 * invite was never claimed, so `CompanyProvider`'s bootstrap found nothing
 * persisted, fell through its precedence chain to step 4, and MINTED A BRAND-NEW
 * COMPANY called "My Company" with them as its Owner. They were invited to a
 * firm's books and landed in an empty tenant of their own, with no error
 * anywhere — while the administrator's Users & Roles still showed a pending
 * invite, because nothing had claimed it.
 *
 * ==========================================================================
 * WHY IT GATES THE SHELL INSTEAD OF FIRING AND FORGETTING
 * ==========================================================================
 * ORDERING IS THE WHOLE FIX. The claim and the bootstrap race for the same
 * question ("which company does this person open?"), and the bootstrap is the
 * one that MINTS. A fire-and-forget claim would still be in flight when the
 * provider decided there was nothing to open, and the new company would be
 * created anyway — the defect, with a network request added. So nothing below
 * this renders until the claim has settled.
 *
 * ⚠️ SETTLED, NOT SUCCEEDED. A failure here must not lock anybody out of an app
 * they have already authenticated to: on any error we proceed exactly as before,
 * which is no worse than the behaviour this replaces. (A 401 is separately
 * handled by bearerFetch, which ends the session.)
 *
 * ==========================================================================
 * AND WHY IT WRITES THE COMPANY ID RATHER THAN SELECTING ONE
 * ==========================================================================
 * `CompanyContext` owns the selection precedence — F3 choice, then `?company=`,
 * then localStorage, then mint — and that story must not grow a fifth rule
 * here. Instead this fills in the ONE case where the chain had nothing to go on:
 * when NOTHING is persisted and NOTHING is in the URL, the company the server
 * says this user may open is a better answer than minting a new one. When
 * anything IS persisted or asked for, this touches nothing at all: an operator's
 * own most recent deliberate choice still wins.
 */
function useSessionBootstrap(
  client: CoredClient | null,
  sessionKey: string,
): boolean {
  const [doneFor, setDoneFor] = useState<string | null>(null);

  useEffect(() => {
    if (!client || !sessionKey) return;
    let live = true;
    void (async () => {
      try {
        const me = await client.getMe();
        if (!live) return;
        adoptCompanyIfNoneChosen(me.companies, me.claimed_invites);
      } catch {
        // See the doc: settled, not succeeded.
      } finally {
        if (live) setDoneFor(sessionKey);
      }
    })();
    return () => {
      live = false;
    };
  }, [client, sessionKey]);

  return doneFor === sessionKey;
}

/**
 * Fill in the company only when the operator has expressed no preference at
 * all. A just-claimed invite wins over an older membership, because it is the
 * reason this person is signing in today.
 */
function adoptCompanyIfNoneChosen(
  companies: { company_id: string }[],
  claimedInvites: { company_id: string }[],
): void {
  if (companies.length === 0) return;
  if (readPersistedCompanyId() || readCompanyIdInURL()) return;
  const target = claimedInvites[0]?.company_id || companies[0]?.company_id;
  if (target) rememberChosenCompanyId(target);
}

/** The persisted company id, read through CompanyContext's own storage key. */
function readPersistedCompanyId(): string {
  try {
    return globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY) ?? "";
  } catch {
    // Storage can throw (Safari private mode, a disabled origin). Treating that
    // as "nothing persisted" is the same answer CompanyContext gives.
    return "";
  }
}

/** `?company=<id>` in the address bar, which outranks anything stored. */
function readCompanyIdInURL(): string {
  try {
    return new URL(globalThis.location?.href ?? "").searchParams.get("company") ?? "";
  } catch {
    return "";
  }
}

/**
 * Is the identity layer configured? VITE_LIVE=1 turns on the same-origin live
 * client that the Vite dev proxy forwards to cored (see vite.config.ts), and
 * that client is now ONLY reachable through a real sign-in.
 */
function defaultAuthGate(): AuthGateConfig | null {
  const env = (import.meta as unknown as { env?: Record<string, string> }).env;
  return env?.VITE_LIVE ? { baseUrl: "" } : null;
}

/** Pick the UNGATED client: explicit base URL > in-memory mock. */
function defaultClient(): CoredClient {
  const env = (import.meta as unknown as { env?: Record<string, string> }).env;
  if (env?.VITE_CORED_BASE_URL) {
    return createRestClient({ baseUrl: env.VITE_CORED_BASE_URL });
  }
  return createMockClient();
}

/**
 * The authenticated transport: attach the session's bearer to every cored call
 * and treat ANY 401 as "this session is over" — the token expired, was revoked,
 * or the server restarted with new keys. Reporting it to the auth layer is what
 * returns the user to the login screen instead of leaving the shell rendering
 * empty screens against a dead session.
 *
 * IT ALSO CARRIES THE STEP-UP RETRY. A token from `POST /auth/login` has no
 * `amr` claim, and every company's Owner level is seeded requires_webauthn, so
 * the FIRST write of a session answers
 *   403 {"code":"STEPUP_REQUIRED"}
 * — for the F8 Sales screen that is the on-demand `POST .../ledgers`, before the
 * voucher is even attempted. Handling it here, once, is what keeps every screen
 * free of step-up plumbing: the ceremony runs, the session's token is replaced,
 * and the original request is replayed with it.
 *
 * FOUR RULES THAT MATTER:
 *   - the 403 body is read through `response.clone()`, so the ORIGINAL stream is
 *     still consumable by the caller when we hand it back;
 *   - the ceremony's own /auth/webauthn/* calls never come through here (they
 *     use the raw fetch inside auth/webauthn.ts), so this cannot recurse;
 *   - AT MOST ONE retry per request — a retry that 403s again is returned as-is
 *     rather than looping;
 *   - a step-up that FAILS surfaces the ORIGINAL 403 to the caller, so the
 *     screen reports "you may not do this", not a masked transport error.
 *
 * Any OTHER 403 is still passed straight through: that is the PDP saying "you
 * are you, but you may not do this", and the screens explain it in place.
 *
 * EXPORTED for auth/webauthn.test.tsx: the concurrency rule (two simultaneous
 * 403s must join ONE ceremony) cannot be reached through <App/>, which only
 * issues one write during bootstrap.
 */
export function bearerFetch(
  token: string,
  onUnauthorized: () => void,
  stepUp: () => Promise<void>,
  currentToken: () => string | null,
  base?: typeof fetch,
): typeof fetch {
  const inner = base ?? globalThis.fetch.bind(globalThis);
  const send = (input: RequestInfo | URL, init: RequestInit, bearer: string) => {
    const headers = new Headers(init.headers ?? {});
    headers.set("Authorization", `Bearer ${bearer}`);
    return inner(input, { ...init, headers });
  };
  return async (input: RequestInfo | URL, init: RequestInit = {}) => {
    // The LIVE token, not the one this wrapper was built with: a ceremony that
    // just finished has already replaced it, and React may not have re-rendered
    // (and rebuilt this client) yet. Sending the stale one would 403 again and
    // start a SECOND ceremony against a single-use challenge store.
    const res = await send(input, init, currentToken() ?? token);
    if (res.status === 401) {
      onUnauthorized();
      return res;
    }
    if (res.status !== 403 || !(await isStepUpRequired(res))) return res;

    try {
      await stepUp();
    } catch {
      // The user cancelled, has no authenticator, or the server cannot mint a
      // stepped-up token. The honest answer to the caller is the 403 it asked
      // for; the chrome shows WHY separately (auth.stepUpState).
      return res;
    }
    const retry = await send(input, init, currentToken() ?? token);
    if (retry.status === 401) onUnauthorized();
    return retry;
  };
}

/**
 * Is this 403 the PDP demanding a WebAuthn step-up? Read through a CLONE so the
 * caller can still consume the body we return.
 *
 * Anything unreadable (no clone(), not JSON, a different code) answers false —
 * a plain authorization 403 must NOT trigger a passkey prompt.
 */
async function isStepUpRequired(res: Response): Promise<boolean> {
  try {
    if (typeof res.clone !== "function") return false;
    const text = await res.clone().text();
    if (!text) return false;
    const body = JSON.parse(text) as { code?: unknown };
    return body?.code === "STEPUP_REQUIRED";
  } catch {
    return false;
  }
}
