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
