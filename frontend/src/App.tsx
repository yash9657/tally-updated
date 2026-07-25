import { useMemo } from "react";
import { AppShell } from "./app/AppShell";
import { CompanyProvider, type CompanyInfo } from "./app/CompanyContext";
import { createMockClient, createRestClient } from "./api";
import type { CoredClient } from "./api";

/**
 * Composition root. Wires the {@link CoredClient} and the active company into
 * the {@link CompanyProvider}, then renders the {@link AppShell} (which owns the
 * navigation machine, key capture, chrome, and screen routing).
 *
 * The client is the live REST client when a backend base URL is configured
 * (VITE_CORED_BASE_URL), otherwise an in-memory mock so the UI runs standalone.
 * Tests render <App /> directly and get the mock by default (no live server).
 */
export function App({
  client,
  company,
}: {
  client?: CoredClient;
  company?: CompanyInfo;
} = {}) {
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

/** Pick the client: live (same-origin dev proxy + auto-auth) > explicit base URL
 * > in-memory mock. VITE_LIVE=1 turns on the auto-authenticating live client that
 * the Vite dev proxy forwards to cored/insightd (see vite.config.ts). */
function defaultClient(): CoredClient {
  const env = (import.meta as unknown as { env?: Record<string, string> }).env;
  if (env?.VITE_LIVE) {
    // Same-origin (baseUrl "") so requests go through the dev proxy; a fetch
    // wrapper lazily mints + attaches a dev owner token (auth is fail-closed).
    return createRestClient({ baseUrl: "", fetchImpl: devAuthedFetch() });
  }
  if (env?.VITE_CORED_BASE_URL) {
    return createRestClient({ baseUrl: env.VITE_CORED_BASE_URL });
  }
  return createMockClient();
}

/**
 * DEV ONLY. A fetch that lazily mints a dev owner token from `/auth/token` (once,
 * cached) and attaches it as a Bearer on every subsequent call — cored is
 * fail-closed, so an unauthenticated request 401s. The stable subject makes the
 * caller the Owner of the company the bootstrap creates (create-on-first-login),
 * and `amr:["webauthn"]` satisfies the write step-up so vouchers post. This
 * mirrors the CLI/e2e `auth.mjs` path; it is never used in the built app or tests.
 */
function devAuthedFetch(): typeof fetch {
  let tokenPromise: Promise<string> | null = null;
  const getToken = () => {
    if (!tokenPromise) {
      tokenPromise = fetch("/auth/token", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ subject: "browser-dev-owner", amr: ["webauthn"] }),
      })
        .then((r) => r.json())
        .then((d) => d.token as string);
    }
    return tokenPromise;
  };
  return async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url =
      typeof input === "string"
        ? input
        : input instanceof URL
          ? input.toString()
          : input.url;
    // The token-mint call itself is unauthenticated.
    if (url.includes("/auth/token")) return fetch(input, init);
    const token = await getToken();
    const headers = new Headers(init.headers ?? {});
    headers.set("Authorization", `Bearer ${token}`);
    return fetch(input, { ...init, headers });
  };
}
