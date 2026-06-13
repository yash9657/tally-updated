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
  const resolvedCompany = useMemo<CompanyInfo>(
    () => company ?? { id: "company-demo", name: "Demo Traders" },
    [company],
  );

  return (
    <CompanyProvider client={resolvedClient} initialCompany={resolvedCompany}>
      <AppShell />
    </CompanyProvider>
  );
}

/** Pick the REST client when a base URL is configured, else the mock. */
function defaultClient(): CoredClient {
  const env = (import.meta as unknown as { env?: Record<string, string> }).env;
  if (env?.VITE_CORED_BASE_URL) {
    return createRestClient({ baseUrl: env.VITE_CORED_BASE_URL });
  }
  return createMockClient();
}
