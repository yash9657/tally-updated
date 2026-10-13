import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { App, type AuthGateConfig } from "../App";
import { createMockClient } from "../api";
import type { CoredClient } from "../api";
import { AUTH_STORAGE_KEY, type AuthSession } from "./AuthContext";
import { login, setPassword, AuthInvalidError, AuthLockedError } from "../api/auth";

afterEach(cleanup);

beforeEach(() => {
  globalThis.localStorage?.clear();
  globalThis.sessionStorage?.clear();
});

// ---------------------------------------------------------------------------
// A stub transport. Only the bits api/auth.ts + api/http.ts read are modelled
// (status / ok / statusText / text), so the tests do not depend on whether the
// jsdom environment ships a real Response.
// ---------------------------------------------------------------------------

interface Reply {
  status: number;
  body?: unknown;
}

function respond(r: Reply): Response {
  const text = r.body === undefined ? "" : JSON.stringify(r.body);
  return {
    ok: r.status >= 200 && r.status < 300,
    status: r.status,
    statusText: `status ${r.status}`,
    text: async () => text,
  } as unknown as Response;
}

interface Transport {
  fetchImpl: typeof fetch;
  /** Every URL the app asked for, in order. */
  urls: string[];
  /** Every Authorization header sent, in order (undefined when absent). */
  auths: (string | undefined)[];
}

function transport(
  route: (url: string, init: RequestInit) => Reply,
): Transport {
  const urls: string[] = [];
  const auths: (string | undefined)[] = [];
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
    urls.push(url);
    const h = init.headers;
    auths.push(
      h instanceof Headers
        ? (h.get("Authorization") ?? undefined)
        : ((h as Record<string, string> | undefined)?.["Authorization"]),
    );
    return respond(route(url, init));
  }) as unknown as typeof fetch;
  return { fetchImpl, urls, auths };
}

const OWNER = {
  id: "user-a",
  subject: "browser-dev-owner",
  display_name: "",
  email: "",
};

/** A transport that accepts exactly one password and 401s everything else. */
function loginTransport(
  password: string,
  user: typeof OWNER = OWNER,
  token = "token-a",
): Transport {
  return transport((url, init) => {
    if (url.endsWith("/auth/login")) {
      const body = JSON.parse(String(init.body)) as { password: string };
      if (body.password !== password) {
        return { status: 401, body: { error: "invalid credentials" } };
      }
      return {
        status: 200,
        body: { token, token_type: "Bearer", expires_in: 3600, user },
      };
    }
    return { status: 500, body: { error: `unrouted ${url}` } };
  });
}

function gate(t: Transport): AuthGateConfig {
  return { baseUrl: "", fetchImpl: t.fetchImpl };
}

function signIn(subject: string, password: string) {
  fireEvent.change(screen.getByLabelText("User name"), {
    target: { value: subject },
  });
  fireEvent.change(screen.getByLabelText("Password"), {
    target: { value: password },
  });
  fireEvent.click(screen.getByRole("button", { name: "Sign In" }));
}

const COMPANY = { id: "co-x", name: "Explicit Co" };

/**
 * Wrap a client in per-method spies.
 *
 * `base` is injectable so a test can SEED the mock before the spies start
 * counting: the mock's `getCompany` 404s an id it has never issued (faithfully —
 * so a hydrate against a stale id is observable), which means a test naming a
 * company that only exists in its own fixture would watch CompanyContext
 * correctly discard the dead id and mint instead. Seeding through the bare base
 * keeps that setup out of the call counts the assertions read.
 */
function spyClient(base: CoredClient = createMockClient({ seed: false })): {
  client: CoredClient;
  spies: Record<string, ReturnType<typeof vi.fn>>;
} {
  const spies: Record<string, ReturnType<typeof vi.fn>> = {};
  const client = {} as Record<string, unknown>;
  for (const key of Object.keys(base) as (keyof CoredClient)[]) {
    const orig = base[key] as unknown as (...a: unknown[]) => unknown;
    const fn = vi.fn((...args: unknown[]) => orig.apply(base, args));
    spies[key] = fn;
    client[key] = fn;
  }
  return { client: client as unknown as CoredClient, spies };
}

// ---------------------------------------------------------------------------
// api/auth.ts — the wire contract
// ---------------------------------------------------------------------------

describe("api/auth", () => {
  it("maps 401 to AuthInvalidError and 423 to AuthLockedError with its until", async () => {
    const invalid = transport(() => ({
      status: 401,
      body: { error: "invalid credentials" },
    }));
    await expect(
      login("", "someone", "nope", invalid.fetchImpl),
    ).rejects.toBeInstanceOf(AuthInvalidError);

    const locked = transport(() => ({
      status: 423,
      body: {
        error: "account locked",
        code: "LOCKED",
        until: "2026-07-26T10:15:00Z",
      },
    }));
    const err = await login("", "someone", "nope", locked.fetchImpl).catch(
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(AuthLockedError);
    expect((err as AuthLockedError).until).toBe("2026-07-26T10:15:00Z");
  });

  it("surfaces any other non-2xx with its status AND body", async () => {
    const t = transport(() => ({ status: 400, body: { error: "subject is required" } }));
    const err = await login("", "", "x", t.fetchImpl).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(Error);
    expect((err as Error).message).toContain("400");
    expect((err as Error).message).toContain("subject is required");
  });

  it("POSTs the pinned login body and returns the pinned 200 shape", async () => {
    const t = loginTransport("tally-demo-2026");
    const res = await login("", "browser-dev-owner", "tally-demo-2026", t.fetchImpl);
    expect(t.urls).toEqual(["/auth/login"]);
    expect(res.token).toBe("token-a");
    expect(res.token_type).toBe("Bearer");
    expect(res.user.subject).toBe("browser-dev-owner");
  });

  it("setPassword sends the bearer and the snake_case body, and 204 resolves", async () => {
    const t = transport((url) =>
      url.endsWith("/auth/password")
        ? { status: 204 }
        : { status: 500, body: { error: "unrouted" } },
    );
    await expect(
      setPassword("", "token-a", "old-password", "new-password-1", t.fetchImpl),
    ).resolves.toBeUndefined();
    expect(t.urls).toEqual(["/auth/password"]);
    expect(t.auths).toEqual(["Bearer token-a"]);

    const bad = transport(() => ({
      status: 401,
      body: { error: "invalid credentials" },
    }));
    await expect(
      setPassword("", "token-a", "wrong", "new-password-1", bad.fetchImpl),
    ).rejects.toBeInstanceOf(AuthInvalidError);
  });
});

// ---------------------------------------------------------------------------
// The gate itself
// ---------------------------------------------------------------------------

describe("the auth gate", () => {
  it("notifies browser routing when the real session starts and ends", async () => {
    const onSessionChange = vi.fn();
    render(<App auth={gate(loginTransport("pw"))} client={createMockClient({ seed: false })} company={COMPANY} onSessionChange={onSessionChange} />);
    expect(onSessionChange).toHaveBeenLastCalledWith(false);
    signIn("browser-dev-owner", "pw");
    const signOut = await screen.findByRole("button", { name: "Sign Out" });
    expect(onSessionChange).toHaveBeenLastCalledWith(true);
    fireEvent.click(signOut);
    await waitFor(() => expect(onSessionChange).toHaveBeenLastCalledWith(false));
    expect(screen.getByTestId("login-screen")).toBeTruthy();
  });

  // NON-VACUITY GUARD for every case below: without a gate the shell renders
  // immediately, so a test that forgot to pass `auth` would "pass" while
  // asserting nothing about the login. This case pins BOTH sides of that.
  it("mock mode has no login and no Sign Out; the gate has a login and no shell", () => {
    render(<App />);
    expect(screen.getByText("Gateway of Tally")).toBeTruthy();
    expect(screen.queryByTestId("login-screen")).toBeNull();
    expect(screen.queryByRole("button", { name: "Sign Out" })).toBeNull();
    cleanup();

    render(<App auth={gate(loginTransport("pw"))} />);
    expect(screen.getByTestId("login-screen")).toBeTruthy();
    expect(screen.queryByText("Gateway of Tally")).toBeNull();
  });

  it("focus starts in the user name field (the e2e types with page.keyboard only)", () => {
    render(<App auth={gate(loginTransport("pw"))} />);
    expect(document.activeElement).toBe(screen.getByLabelText("User name"));
  });

  it("a correct password renders the shell and shows who is signed in", async () => {
    const t = loginTransport("tally-demo-2026");
    render(
      <App
        auth={gate(t)}
        client={createMockClient({ seed: false })}
        company={COMPANY}
      />,
    );
    signIn("browser-dev-owner", "tally-demo-2026");

    await waitFor(() => expect(screen.getByText("Gateway of Tally")).toBeTruthy());
    expect(screen.queryByTestId("login-screen")).toBeNull();
    // display_name is "" for this identity, so the SUBJECT must be shown —
    // a bare display_name would render an empty top bar.
    expect(screen.getByTestId("signed-in-as").textContent).toContain(
      "browser-dev-owner",
    );
    expect(screen.getByRole("button", { name: "Sign Out" })).toBeTruthy();
  });

  it("prefers the display name when the identity has one", async () => {
    const t = loginTransport("pw", {
      id: "user-de",
      subject: "demo-data-entry",
      display_name: "Demo Data Entry",
      email: "dataentry@demo.local",
    });
    render(
      <App
        auth={gate(t)}
        client={createMockClient({ seed: false })}
        company={COMPANY}
      />,
    );
    signIn("demo-data-entry", "pw");
    await waitFor(() =>
      expect(screen.getByTestId("signed-in-as").textContent).toContain(
        "Demo Data Entry",
      ),
    );
  });

  it("a 401 shows invalid credentials and does NOT render the shell", async () => {
    const t = loginTransport("right-password");
    render(
      <App
        auth={gate(t)}
        client={createMockClient({ seed: false })}
        company={COMPANY}
      />,
    );
    signIn("browser-dev-owner", "wrong-password");

    await waitFor(() => expect(screen.getByTestId("login-error")).toBeTruthy());
    expect(screen.getByTestId("login-error").getAttribute("data-failure")).toBe(
      "invalid",
    );
    expect(screen.getByTestId("login-error").textContent).toMatch(
      /Invalid credentials/,
    );
    // The gate held: no shell, and nothing was persisted.
    expect(screen.queryByText("Gateway of Tally")).toBeNull();
    expect(screen.getByTestId("login-screen")).toBeTruthy();
    expect(globalThis.sessionStorage?.getItem(AUTH_STORAGE_KEY)).toBeNull();
  });

  /**
   * ⛔ THIS CASE USED TO ASSERT THE DEFECT, AND THE ASSERTION WAS DELETED ON
   * PURPOSE (D58, trap #7).
   *
   * It read `expect(err.textContent).toContain("2026-07-26T11:30:00Z")` — i.e.
   * it REQUIRED the screen to hand the operator the server's raw UTC RFC3339
   * instant, which is exactly what D58 files as the defect. For this product's
   * audience, accountants in India on IST, that string is five and a half hours
   * behind their wall clock and after 18:30 local it names YESTERDAY'S DATE, so
   * it reads as a lock that expired long ago. A test that pins the defect is
   * part of the defect; this now pins the contract instead.
   *
   * ⚠️ AND THE FIXTURE'S INSTANT IS RELATIVE, NOT A LITERAL. The old one was a
   * hardcoded `2026-07-26T11:30:00Z`, which is now in the PAST, so the screen
   * quite correctly rendered "the lock has now lifted" and the case would have
   * silently stopped exercising the wait-remaining branch it exists for — a
   * date-shaped fixture that rots into vacuity. Both branches are driven below,
   * each from `Date.now()`.
   */
  it("a 423 shows ACCOUNT LOCKED in the OPERATOR's clock and how long, never a raw UTC instant", async () => {
    const untilISO = new Date(Date.now() + 12 * 60_000).toISOString();
    const t = transport(() => ({
      status: 423,
      body: { error: "account locked", code: "LOCKED", until: untilISO },
    }));
    render(<App auth={gate(t)} client={createMockClient({ seed: false })} company={COMPANY} />);
    signIn("browser-dev-owner", "whatever");

    await waitFor(() => expect(screen.getByTestId("login-error")).toBeTruthy());
    const err = screen.getByTestId("login-error");
    expect(err.getAttribute("data-failure")).toBe("locked");
    expect(err.textContent).toMatch(/ACCOUNT LOCKED/);

    // THE DEFECT, ASSERTED AS ABSENT. Revert lockedUntilText() to interpolating
    // `f.until` and this line fails.
    expect(err.textContent).not.toContain(untilISO);
    // …replaced by the two things the operator actually asked: when, in a clock
    // they recognise (with the zone NAMED so it cannot be misread), and how long.
    expect(err.textContent).toMatch(/about 12 minutes/);
    expect(err.textContent).toMatch(/\b(AM|PM|GMT|UTC)\b/);
    // …and the remedy, because "locked" with no way out is the state D55 was
    // about. Users & Roles is where the Unlock button lives.
    expect(err.textContent).toMatch(/Users & Roles/);
    expect(screen.queryByText("Gateway of Tally")).toBeNull();
  });

  it("a 423 whose lock has ALREADY lapsed says so, rather than 'try again in 0 minutes'", async () => {
    // The window between cored computing `until` and the operator reading the
    // sentence is real, and a lock that lapsed while the message was on screen
    // must not tell somebody to keep waiting.
    const untilISO = new Date(Date.now() - 60_000).toISOString();
    const t = transport(() => ({
      status: 423,
      body: { error: "account locked", code: "LOCKED", until: untilISO },
    }));
    render(<App auth={gate(t)} client={createMockClient({ seed: false })} company={COMPANY} />);
    signIn("browser-dev-owner", "whatever");

    await waitFor(() => expect(screen.getByTestId("login-error")).toBeTruthy());
    const err = screen.getByTestId("login-error");
    expect(err.textContent).toMatch(/lock has now lifted/i);
    expect(err.textContent).not.toMatch(/about 0 minutes/);
  });

  it("a 423 with NO until still names the remedy instead of leaving a dead end", async () => {
    // The server always sends one today, but "locked, and nothing about what to
    // do" is precisely the state D55 describes. NON-VACUOUS: delete the
    // no-`until` arm of failureText and this renders "undefined".
    const t = transport(() => ({
      status: 423,
      body: { error: "account locked", code: "LOCKED" },
    }));
    render(<App auth={gate(t)} client={createMockClient({ seed: false })} company={COMPANY} />);
    signIn("browser-dev-owner", "whatever");

    await waitFor(() => expect(screen.getByTestId("login-error")).toBeTruthy());
    const err = screen.getByTestId("login-error");
    expect(err.getAttribute("data-failure")).toBe("locked");
    expect(err.textContent).toMatch(/ACCOUNT LOCKED/);
    expect(err.textContent).toMatch(/Users & Roles/);
    expect(err.textContent).not.toMatch(/undefined|NaN|Invalid Date/);
  });

  it("a network/server failure is reported, never a blank screen", async () => {
    const t = transport(() => ({ status: 503, body: { error: "cored is down" } }));
    render(<App auth={gate(t)} client={createMockClient({ seed: false })} company={COMPANY} />);
    signIn("browser-dev-owner", "pw");

    await waitFor(() => expect(screen.getByTestId("login-error")).toBeTruthy());
    expect(screen.getByTestId("login-error").getAttribute("data-failure")).toBe(
      "error",
    );
    expect(screen.getByTestId("login-error").textContent).toContain("503");
    // The form is still there to retry with.
    expect(screen.getByLabelText("Password")).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// Session lifecycle
// ---------------------------------------------------------------------------

describe("the session", () => {
  it("round-trips through sessionStorage: written on login, read on remount", async () => {
    const t = loginTransport("pw");
    render(<App auth={gate(t)} client={createMockClient({ seed: false })} company={COMPANY} />);
    signIn("browser-dev-owner", "pw");
    await waitFor(() => expect(screen.getByText("Gateway of Tally")).toBeTruthy());

    const raw = globalThis.sessionStorage?.getItem(AUTH_STORAGE_KEY);
    expect(raw).toBeTruthy();
    const stored = JSON.parse(raw!) as AuthSession;
    expect(stored.token).toBe("token-a");
    expect(stored.user.subject).toBe("browser-dev-owner");
    cleanup();

    // A remount with that entry in place NEVER SHOWS THE LOGIN SCREEN again,
    // and — the non-vacuous half — makes no second /auth/login round trip.
    //
    // ⚠️ THE SHELL ASSERTION IS NOW AWAITED, AND THE await IS THE POINT. It used
    // to be synchronous, which was only true while the gate rendered the shell
    // in the same tick as the session. D25 put ONE await in front of it: the
    // session bootstrap (GET /v1/me, the only seam that claims a pending invite)
    // must SETTLE before anything below it renders, or the company bootstrap
    // races it and mints "My Company" while the invite is still in flight. So
    // the shell arrives a microtask later. It must still arrive, and the login
    // screen must never come back in between — which is what the two
    // assertions below say together.
    const t2 = loginTransport("pw");
    render(<App auth={gate(t2)} client={createMockClient({ seed: false })} company={COMPANY} />);
    expect(screen.queryByTestId("login-screen")).toBeNull();
    await waitFor(() => expect(screen.getByText("Gateway of Tally")).toBeTruthy());
    expect(screen.queryByTestId("login-screen")).toBeNull();
    expect(t2.urls).toEqual([]);
  });

  it("ignores a malformed persisted session instead of crashing the shell", () => {
    globalThis.sessionStorage?.setItem(AUTH_STORAGE_KEY, '{"token":"t"}');
    render(<App auth={gate(loginTransport("pw"))} client={createMockClient({ seed: false })} company={COMPANY} />);
    expect(screen.getByTestId("login-screen")).toBeTruthy();
  });

  /** An empty, well-formed GET /v1/me: a session with no company and no invite. */
  const EMPTY_ME = {
    user_id: OWNER.id,
    subject: OWNER.subject,
    companies: [] as unknown[],
    claimed_invites: [] as unknown[],
  };

  it("a 401 from a mid-session API call returns the user to the login screen", async () => {
    // No `client` prop: the REST client over the stub transport is what the
    // live app uses, so this exercises the real bearer wrapper.
    //
    // ⚠️ /v1/me IS ROUTED 200 AND THAT IS DELIBERATE, NOT A CONVENIENCE. D25 put
    // the invite-claiming GET /v1/me in front of everything else, so with a
    // blanket 401 this case would bounce on the FIRST call of the session and
    // never test a MID-session 401 at all — it would still be green while
    // asserting something weaker than its own name. Answering /v1/me lets the
    // shell render, exactly as it did before D25, and the company bootstrap's
    // POST /v1/companies is once again the call that dies. The first-call case
    // is worth having too, and has its own test below.
    const t = transport((url) => {
      if (url.endsWith("/auth/login")) {
        return {
          status: 200,
          body: {
            token: "token-a",
            token_type: "Bearer",
            expires_in: 3600,
            user: OWNER,
          },
        };
      }
      if (url.endsWith("/v1/me")) return { status: 200, body: EMPTY_ME };
      // Everything else cored-side rejects the (expired) token.
      return { status: 401, body: { error: "unauthorized" } };
    });
    render(<App auth={gate(t)} />);
    signIn("browser-dev-owner", "pw");
    await waitFor(() => expect(screen.getByText("Gateway of Tally")).toBeTruthy());

    // The company bootstrap fires POST /v1/companies with the bearer attached…
    await waitFor(() =>
      expect(t.urls.some((u) => u.includes("/v1/companies"))).toBe(true),
    );
    expect(t.auths.some((a) => a === "Bearer token-a")).toBe(true);

    // …and its 401 ends the session: back to the login screen, storage cleared.
    await waitFor(() => expect(screen.getByTestId("login-screen")).toBeTruthy());
    expect(screen.queryByText("Gateway of Tally")).toBeNull();
    expect(globalThis.sessionStorage?.getItem(AUTH_STORAGE_KEY)).toBeNull();
  });

  /**
   * ⛔ THE DOOR D56 MADE REACHABLE, AND THE DECISION IT FORCED.
   *
   * Session revocation changed what a 401 MEANS. Before it, a mid-session 401
   * was an expired token; now it is also "the account behind this bearer was
   * LOCKED, and every session it held is dead" — and, because a lock revokes
   * retroactively, that verdict arrives on the very FIRST authenticated call the
   * browser makes after signing in, which since D25 is GET /v1/me.
   *
   * The decision, stated so the next reader does not have to re-derive it: a
   * revoked session's 401 must STILL bounce to the login screen, on the first
   * call as much as on the hundredth. The alternative — treating the bootstrap
   * call as special and swallowing its 401 — leaves a locked operator's browser
   * sitting on "Opening your companies…" or on an empty shell with a dead
   * bearer, which is a WORSE outcome than the defect: they are neither signed in
   * nor signed out, and the screen tells them nothing. Signing them out is the
   * honest answer, and the login screen is where the ACCOUNT LOCKED message they
   * need is rendered (see the 423 cases above).
   *
   * NON-VACUOUS: this is the ONE 401 that App.tsx's session bootstrap catches
   * and swallows on its own (`catch {}` — settled, not succeeded). It works only
   * because bearerFetch reports the 401 to the auth layer BEFORE the promise
   * rejects. Move the `onUnauthorized()` call below the rejection, or make the
   * bootstrap use a raw fetch, and this hangs on "Opening your companies…".
   */
  it("a 401 on the session's FIRST call (a revoked session) also returns to the login screen", async () => {
    const t = transport((url) => {
      if (url.endsWith("/auth/login")) {
        return {
          status: 200,
          body: {
            token: "token-a",
            token_type: "Bearer",
            expires_in: 3600,
            user: OWNER,
          },
        };
      }
      // What a LOCKED subject's bearer gets from every authenticated route:
      // 401 "session revoked: this account was locked" (auth_middleware.go).
      return {
        status: 401,
        body: { error: "session revoked: this account was locked" },
      };
    });
    render(<App auth={gate(t)} />);
    signIn("browser-dev-owner", "pw");

    // The claim seam ran and carried the bearer — so the 401 below is a real
    // refusal of a real request, not a call that never happened.
    await waitFor(() => expect(t.urls.some((u) => u.endsWith("/v1/me"))).toBe(true));
    expect(t.auths.some((a) => a === "Bearer token-a")).toBe(true);

    await waitFor(() => expect(screen.getByTestId("login-screen")).toBeTruthy());
    expect(screen.queryByTestId("session-bootstrap")).toBeNull();
    expect(screen.queryByText("Gateway of Tally")).toBeNull();
    expect(globalThis.sessionStorage?.getItem(AUTH_STORAGE_KEY)).toBeNull();
    // And it did NOT go on to mint a company for a session that is dead.
    expect(t.urls.some((u) => u.includes("/v1/companies"))).toBe(false);
  });

  it("signing out forgets the session AND the previous user's company", async () => {
    const { client, spies } = spyClient();

    // ⛔ THE MOCK CLIENT HAS EXACTLY ONE IDENTITY, AND D25 MADE THAT MATTER.
    // createMockClient's getMe answers "every company MOCK_USER_ID belongs to",
    // which after user A's bootstrap is A's company — for EVERY caller, because
    // the mock cannot tell A from B. The session bootstrap would then hand user
    // B user A's company id and this case would fail for a reason that exists
    // only in the fixture: against a real cored, /v1/me is scoped to the bearer
    // and returns B's memberships, which are none. Modelling that here is what
    // keeps the assertions below about SIGN-OUT rather than about the mock.
    let signedIn = OWNER.subject;
    (client as unknown as Record<string, unknown>).getMe = vi.fn(async () => ({
      user_id: signedIn,
      subject: signedIn,
      companies: [],
      claimed_invites: [],
    }));

    const t = loginTransport("pw");
    render(<App auth={gate(t)} client={client} />);

    // User A signs in and the shell bootstraps a company for them.
    signIn("browser-dev-owner", "pw");
    await waitFor(() => expect(spies.createCompany).toHaveBeenCalledTimes(1));
    expect(globalThis.localStorage?.getItem("tally.companyId")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Sign Out" }));
    await waitFor(() => expect(screen.getByTestId("login-screen")).toBeTruthy());
    expect(globalThis.sessionStorage?.getItem(AUTH_STORAGE_KEY)).toBeNull();
    expect(globalThis.localStorage?.getItem("tally.companyId")).toBeNull();

    // User B signs in on the same browser. If sign-out had left the company id
    // behind, the shell would HYDRATE user A's company (getCompany) — against a
    // real cored that is a 403, and CompanyContext then mints B a brand-new
    // empty company. It must never reach for A's books at all.
    cleanup();
    signedIn = "sharma-owner";
    const t2 = loginTransport("pw", {
      id: "user-b",
      subject: "sharma-owner",
      display_name: "",
      email: "",
    });
    render(<App auth={gate(t2)} client={client} />);
    signIn("sharma-owner", "pw");
    await waitFor(() => expect(spies.createCompany).toHaveBeenCalledTimes(2));
    expect(spies.getCompany).not.toHaveBeenCalled();
  });

  /**
   * D25 — SIGNING IN CLAIMS A PENDING INVITE, AND LANDS IN THE COMPANY IT WAS
   * FOR.
   *
   * The defect, driven: `GET /v1/me` is the ONLY seam that converts an invite
   * into a membership (the server never writes another person's identity row),
   * and the browser called it from exactly ONE place — the F3 Select Company
   * screen, which an invited user has no reason to open. So an invitee signed
   * in, nothing claimed the invite, `CompanyProvider`'s precedence chain found
   * nothing persisted, fell through to its last rule and MINTED "My Company":
   * they were invited to a firm's books and landed in an empty tenant of their
   * own, with no error anywhere, while the administrator's Users & Roles still
   * showed the invite as pending.
   *
   * NON-VACUOUS, twice over:
   *  - `createCompany` NOT being called is the whole defect stated as an
   *    assertion — before the fix it was called exactly once, every time;
   *  - the ORDER matters as much as the call. Make the claim fire-and-forget
   *    instead of gating the shell and the bootstrap wins the race, mints, and
   *    `createCompany` is called again — the defect with a network request
   *    added. That is why the shell is gated on it.
   */
  it("signing in CLAIMS a pending invite and opens that company instead of minting one", async () => {
    const INVITED_CO = "co-invited-0001";
    // The firm's books already exist — the invitee is being let INTO them, not
    // given new ones. Seeded on the bare mock so it costs no spy call.
    const base = createMockClient({ seed: false });
    await base.createCompany({
      id: INVITED_CO,
      name: "Sharma & Co",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
    });
    const { client, spies } = spyClient(base);
    const getMe = vi.fn(async () => ({
      user_id: "user-invitee",
      subject: "new-accountant",
      // handleMe claims FIRST and lists SECOND, so the just-claimed company is
      // in both. `claimed_invites` is what says "this is why they signed in".
      companies: [
        {
          company_id: INVITED_CO,
          name: "Sharma & Co",
          security_level_id: "lvl-1",
          security_level_name: "Data Entry",
          is_owner: false,
        },
      ],
      claimed_invites: [{ invite_id: "inv-1", company_id: INVITED_CO }],
    }));
    (client as unknown as Record<string, unknown>).getMe = getMe;

    const t = loginTransport("pw", {
      id: "user-invitee",
      subject: "new-accountant",
      display_name: "",
      email: "",
    });
    render(<App auth={gate(t)} client={client} />);
    signIn("new-accountant", "pw");

    await waitFor(() => expect(getMe).toHaveBeenCalled());
    await waitFor(() =>
      expect(globalThis.localStorage?.getItem("tally.companyId")).toBe(INVITED_CO),
    );
    // The shell opened the company they were invited to…
    await waitFor(() => expect(spies.getCompany).toHaveBeenCalled());
    expect(spies.getCompany!.mock.calls[0]![0]).toBe(INVITED_CO);
    // …and did NOT mint them an empty tenant of their own.
    expect(spies.createCompany).not.toHaveBeenCalled();
  });

  /**
   * …AND THE CONTROL FOR IT. The claim must not become a fifth rule in
   * CompanyContext's selection precedence: an operator's own most recent
   * deliberate choice still wins over whatever /v1/me happens to list first.
   * Without this, D25's fix would silently override the F3 company switcher.
   */
  it("a company already chosen by the operator OUTRANKS the one /v1/me lists", async () => {
    const base = createMockClient({ seed: false });
    for (const id of ["co-chosen-by-me", "co-listed-first"]) {
      await base.createCompany({
        id,
        name: id,
        fy_start_date: "2026-04-01",
        base_currency: "INR",
      });
    }
    const { client, spies } = spyClient(base);
    globalThis.localStorage?.setItem("tally.companyId", "co-chosen-by-me");
    const getMe = vi.fn(async () => ({
      user_id: "user-invitee",
      subject: "new-accountant",
      companies: [
        {
          company_id: "co-listed-first",
          name: "Some Other Books",
          security_level_id: "lvl-1",
          security_level_name: "Data Entry",
          is_owner: false,
        },
      ],
      claimed_invites: [],
    }));
    (client as unknown as Record<string, unknown>).getMe = getMe;

    const t = loginTransport("pw");
    render(<App auth={gate(t)} client={client} />);
    signIn("browser-dev-owner", "pw");

    await waitFor(() => expect(spies.getCompany).toHaveBeenCalled());
    expect(globalThis.localStorage?.getItem("tally.companyId")).toBe("co-chosen-by-me");
    expect(spies.getCompany!.mock.calls[0]![0]).toBe("co-chosen-by-me");
  });
});

// ---------------------------------------------------------------------------
// D55's SECOND HALF — SELF-SERVICE PASSWORD CHANGE.
//
// `POST /auth/password` shipped with a client function (`setPassword`, pinned
// above) and NO CALLER anywhere in the app, so nobody could change their own
// password. The client-level tests above therefore proved nothing about the
// product: they exercised the function the app never reached.
//
// ⛔ THAT IS D7's LESSON IN A THIRD FILE. `byCommitmentOrder` was pinned nine
// ways while nothing pinned the picker CALLING it, and the same shape held here.
// So these tests drive the CHROME — Tab-reachable buttons, a real transport —
// and assert what went on the WIRE, not what a helper would have sent.
// ---------------------------------------------------------------------------

/**
 * A transport that signs one password in and answers /auth/password according
 * to `onChange`, so each test states its own server outcome.
 */
function passwordTransport(
  loginPassword: string,
  onChange: (body: { current_password: string; new_password: string }) => Reply,
): Transport {
  return transport((url, init) => {
    if (url.endsWith("/auth/login")) {
      const body = JSON.parse(String(init.body)) as { password: string };
      if (body.password !== loginPassword) {
        return { status: 401, body: { error: "invalid credentials" } };
      }
      return {
        status: 200,
        body: { token: "token-a", token_type: "Bearer", expires_in: 3600, user: OWNER },
      };
    }
    if (url.endsWith("/auth/password")) {
      return onChange(
        JSON.parse(String(init.body)) as {
          current_password: string;
          new_password: string;
        },
      );
    }
    return { status: 500, body: { error: `unrouted ${url}` } };
  });
}

async function signInAndOpenChangePassword(t: Transport) {
  render(<App auth={gate(t)} client={createMockClient({ seed: false })} company={COMPANY} />);
  signIn("browser-dev-owner", "old-password-1");
  await waitFor(() => expect(screen.getByTestId("change-password-open")).toBeTruthy());
  fireEvent.click(screen.getByTestId("change-password-open"));
  await waitFor(() => expect(screen.getByTestId("change-password-form")).toBeTruthy());
}

function fillChange(current: string, next: string, confirm: string) {
  fireEvent.change(screen.getByTestId("change-password-current"), {
    target: { value: current },
  });
  fireEvent.change(screen.getByTestId("change-password-new"), {
    target: { value: next },
  });
  fireEvent.change(screen.getByTestId("change-password-confirm"), {
    target: { value: confirm },
  });
}

describe("D55 — a signed-in user can change their own password", () => {
  it("⛔ the app REACHES POST /auth/password, with the bearer and the typed values", async () => {
    const seen: { current_password: string; new_password: string }[] = [];
    const t = passwordTransport("old-password-1", (body) => {
      seen.push(body);
      return { status: 204 };
    });
    await signInAndOpenChangePassword(t);
    fillChange("old-password-1", "new-password-2", "new-password-2");
    fireEvent.click(screen.getByTestId("change-password-submit"));

    await waitFor(() => expect(screen.getByTestId("change-password-done")).toBeTruthy());
    // The WIRE is the oracle: the route was hit, once, with the bearer.
    expect(t.urls.filter((u) => u.endsWith("/auth/password"))).toHaveLength(1);
    expect(seen).toEqual([
      { current_password: "old-password-1", new_password: "new-password-2" },
    ]);
    const idx = t.urls.findIndex((u) => u.endsWith("/auth/password"));
    expect(t.auths[idx]).toBe("Bearer token-a");
  });

  it("⛔ the session SURVIVES a successful change — cored does not revoke it", async () => {
    const t = passwordTransport("old-password-1", () => ({ status: 204 }));
    await signInAndOpenChangePassword(t);
    fillChange("old-password-1", "new-password-2", "new-password-2");
    fireEvent.click(screen.getByTestId("change-password-submit"));

    await waitFor(() => expect(screen.getByTestId("change-password-done")).toBeTruthy());
    // Still signed in: the login screen must NOT be back, and the chrome must
    // still name the user. A change that signed the operator out would read as
    // a failure of the very thing that just succeeded.
    expect(screen.queryByTestId("login-screen")).toBeNull();
    expect(screen.getByTestId("signed-in-as")).toBeTruthy();
  });

  it("a WRONG current password shows the refusal and does not report success", async () => {
    const t = passwordTransport("old-password-1", () => ({
      status: 401,
      body: { error: "invalid credentials" },
    }));
    await signInAndOpenChangePassword(t);
    fillChange("wrong-one", "new-password-2", "new-password-2");
    fireEvent.click(screen.getByTestId("change-password-submit"));

    await waitFor(() => expect(screen.getByTestId("change-password-error")).toBeTruthy());
    expect(screen.queryByTestId("change-password-done")).toBeNull();
    // AND the session is untouched — a 401 from THIS route must not be mistaken
    // for an expired session and sign the operator out.
    expect(screen.queryByTestId("login-screen")).toBeNull();
  });

  it("⛔ the server's PASSWORD POLICY refusal is shown verbatim, not paraphrased", async () => {
    const t = passwordTransport("old-password-1", () => ({
      status: 400,
      body: { error: "password must be at least 12 characters" },
    }));
    await signInAndOpenChangePassword(t);
    fillChange("old-password-1", "short", "short");
    fireEvent.click(screen.getByTestId("change-password-submit"));

    await waitFor(() => expect(screen.getByTestId("change-password-error")).toBeTruthy());
    // The policy is the SERVER'S to state. A generic "invalid password" would
    // leave the operator guessing which rule they broke.
    expect(screen.getByTestId("change-password-error").textContent).toContain(
      "at least 12 characters",
    );
  });

  it("⛔ a 423 from THIS route is surfaced — the change route shares the login lockout", async () => {
    const t = passwordTransport("old-password-1", () => ({
      status: 423,
      body: { error: "account locked", code: "LOCKED", until: "2026-08-20T18:30:00Z" },
    }));
    await signInAndOpenChangePassword(t);
    fillChange("wrong-again", "new-password-2", "new-password-2");
    fireEvent.click(screen.getByTestId("change-password-submit"));

    // cored routes a wrong current_password through POST /auth/login's lockout
    // budget on purpose, so this form CAN lock the account it is being used
    // from. Swallowing the 423 would make that look like the product breaking.
    await waitFor(() => expect(screen.getByTestId("change-password-error")).toBeTruthy());
    expect(screen.queryByTestId("change-password-done")).toBeNull();
  });

  it("⛔ a MISTYPED confirmation never reaches the server", async () => {
    const t = passwordTransport("old-password-1", () => ({ status: 204 }));
    await signInAndOpenChangePassword(t);
    fillChange("old-password-1", "new-password-2", "new-password-3");
    fireEvent.click(screen.getByTestId("change-password-submit"));

    await waitFor(() => expect(screen.getByTestId("change-password-error")).toBeTruthy());
    // The server has no second field to compare against, so a mistyped new
    // password would be SAVED and lock the operator out at the next sign-in.
    expect(t.urls.filter((u) => u.endsWith("/auth/password"))).toHaveLength(0);
  });

  it("the form is reachable by keyboard alone (no invented keystroke)", async () => {
    const t = passwordTransport("old-password-1", () => ({ status: 204 }));
    await signInAndOpenChangePassword(t);
    // TopBar's own reasoning: there is no TallyPrime-documented key for this, so
    // it is a <button>. Being natively focusable is what keeps a
    // page.keyboard-only drive able to reach it.
    const submit = screen.getByTestId("change-password-submit");
    submit.focus();
    expect(document.activeElement).toBe(submit);
  });
});
