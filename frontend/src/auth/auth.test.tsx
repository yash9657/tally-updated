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

function spyClient(): {
  client: CoredClient;
  spies: Record<string, ReturnType<typeof vi.fn>>;
} {
  const base = createMockClient({ seed: false });
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

  it("a 423 shows ACCOUNT LOCKED with the until instant", async () => {
    const t = transport(() => ({
      status: 423,
      body: {
        error: "account locked",
        code: "LOCKED",
        until: "2026-07-26T11:30:00Z",
      },
    }));
    render(<App auth={gate(t)} client={createMockClient({ seed: false })} company={COMPANY} />);
    signIn("browser-dev-owner", "whatever");

    await waitFor(() => expect(screen.getByTestId("login-error")).toBeTruthy());
    const err = screen.getByTestId("login-error");
    expect(err.getAttribute("data-failure")).toBe("locked");
    expect(err.textContent).toMatch(/ACCOUNT LOCKED/);
    expect(err.textContent).toContain("2026-07-26T11:30:00Z");
    expect(screen.queryByText("Gateway of Tally")).toBeNull();
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

    // A remount with that entry in place goes STRAIGHT to the shell: no login
    // screen and — the non-vacuous half — no second /auth/login round trip.
    const t2 = loginTransport("pw");
    render(<App auth={gate(t2)} client={createMockClient({ seed: false })} company={COMPANY} />);
    expect(screen.getByText("Gateway of Tally")).toBeTruthy();
    expect(screen.queryByTestId("login-screen")).toBeNull();
    expect(t2.urls).toEqual([]);
  });

  it("ignores a malformed persisted session instead of crashing the shell", () => {
    globalThis.sessionStorage?.setItem(AUTH_STORAGE_KEY, '{"token":"t"}');
    render(<App auth={gate(loginTransport("pw"))} client={createMockClient({ seed: false })} company={COMPANY} />);
    expect(screen.getByTestId("login-screen")).toBeTruthy();
  });

  it("a 401 from a mid-session API call returns the user to the login screen", async () => {
    // No `client` prop: the REST client over the stub transport is what the
    // live app uses, so this exercises the real bearer wrapper.
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
      // Everything cored-side rejects the (expired) token.
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

  it("signing out forgets the session AND the previous user's company", async () => {
    const { client, spies } = spyClient();
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
});
