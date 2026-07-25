import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { ApiError } from "../api";
import type { CoredClient } from "../api";

/**
 * App-wide context binding the active company id and the {@link CoredClient}
 * implementation. Every screen reads the company id from here (it is the tenant
 * scope embedded in every backend route) and calls the backend through the
 * provided client — which is the REST client in production and the in-memory
 * mock in tests. Screens never construct a client themselves.
 */

export interface CompanyInfo {
  id: string;
  name: string;
  /**
   * The company's principal GST registration state (2-digit code), used as the
   * supplier_state_code in the GST split (intra-state CGST+SGST when it equals
   * the place of supply, else IGST).
   *
   * SOURCE OF TRUTH IS THE SERVER: `company.state_code` (migration 0008). It is
   * written at creation (CreateCompanyRequest.state_code) and READ BACK on load
   * via `client.getCompany` — see the bootstrap effect below. It is NOT a
   * hardcoded constant: the create-time DEFAULT (VITE_HOME_STATE_CODE, "29" for
   * the golden corpora / e2e fixtures) applies ONLY when minting a brand-new
   * company, never to a company that already exists.
   *
   * Keeping this in agreement with the server matters: cored fills an empty
   * SupplierStateCode from company.state_code inside the post transaction and
   * then VERIFIES the posted tax legs against its own derivation, so a stale
   * home state here means a 422, not a silently wrong number.
   *
   * `undefined`/"" means "not known yet"; the GST split then falls back to
   * inter-state, which is also what the server derives for a company with no
   * registered state — the two stay consistent.
   */
  homeStateCode?: string;
}

interface CompanyContextValue {
  client: CoredClient;
  company: CompanyInfo | null;
  setCompany: (c: CompanyInfo | null) => void;
}

const CompanyContext = createContext<CompanyContextValue | null>(null);

/** localStorage key under which the bootstrapped company id is persisted. */
const COMPANY_STORAGE_KEY = "tally.companyId";

/** Default bootstrap parameters for the auto-created demo company. */
const BOOTSTRAP_COMPANY_NAME = "My Company";
const BOOTSTRAP_FY_START = "2026-04-01";
const BOOTSTRAP_CURRENCY = "INR";
/**
 * CREATE-TIME default home GST state for a brand-new bootstrapped company, sent
 * as CreateCompanyRequest.state_code and thereby PERSISTED to company.state_code.
 * Overridable per deployment via VITE_HOME_STATE_CODE; "29" (Karnataka) keeps the
 * golden corpora / e2e fixtures stable.
 *
 * This is a default for CREATION ONLY. An existing company's home state is always
 * read back from the server (getCompany) — never assumed from this constant.
 */
const DEFAULT_NEW_COMPANY_STATE_CODE = "29";

function configHomeStateCode(): string {
  // import.meta.env is injected by Vite; guard for non-Vite (node/vitest) runs.
  const env = (import.meta as unknown as { env?: Record<string, string> }).env;
  return env?.VITE_HOME_STATE_CODE ?? DEFAULT_NEW_COMPANY_STATE_CODE;
}

function readPersistedCompanyId(): string | null {
  try {
    return globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY) ?? null;
  } catch {
    return null;
  }
}

function clearPersistedCompanyId(): void {
  try {
    globalThis.localStorage?.removeItem(COMPANY_STORAGE_KEY);
  } catch {
    // Non-fatal (see persistCompanyId).
  }
}

/** True for a 404 from the API (the persisted company no longer exists). */
// A PERSISTED bootstrap id is UNUSABLE — drop it and mint a fresh company —
// when the server rejects it as malformed (400 "invalid company id"), says it
// does not exist (404), or says the caller is not a member of it (403). All three
// happen after switching backends, wiping the DB, or carrying over an id minted
// by the in-memory mock (whose ids are NOT uuids, so cored 400s on them). Only a
// genuine transport/5xx error still fails closed (below): there the id may be fine.
function isUnusablePersistedId(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    (err.status === 400 || err.status === 403 || err.status === 404)
  );
}

function persistCompanyId(id: string): void {
  try {
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, id);
  } catch {
    // Non-fatal: a private-mode / SSR environment without localStorage still
    // works, it just won't reuse the company across reloads.
  }
}

export function CompanyProvider({
  client,
  initialCompany = null,
  bootstrap = false,
  children,
}: {
  client: CoredClient;
  initialCompany?: CompanyInfo | null;
  /**
   * When true and no company is selected, mint (or reuse a persisted) company on
   * first mount against the live client. Off by default so tests that pass an
   * explicit company stay deterministic (no createCompany calls).
   */
  bootstrap?: boolean;
  children: ReactNode;
}) {
  const [company, setCompany] = useState<CompanyInfo | null>(initialCompany);
  // Guard so React 18 StrictMode's double-invoke (and re-renders) don't mint two
  // companies. Bootstrap runs at most once per provider instance.
  const bootstrapped = useRef(false);

  useEffect(() => {
    if (!bootstrap || company || bootstrapped.current) return;
    bootstrapped.current = true;
    let cancelled = false;
    (async () => {
      const persisted = readPersistedCompanyId();
      if (persisted) {
        // HYDRATE the persisted company from the server (GET /v1/companies/{id})
        // instead of assuming anything about it. This is what makes a
        // non-Karnataka company derive the correct intra/inter-state GST split
        // in-browser: homeStateCode comes from company.state_code, the same
        // column cored defaults SupplierStateCode from when posting.
        try {
          const view = await client.getCompany(persisted);
          if (!cancelled) {
            setCompany({
              id: view.id || persisted,
              name: view.name || BOOTSTRAP_COMPANY_NAME,
              // Verbatim — including "". A company with no registered state
              // derives inter-state on BOTH sides, so client and server agree.
              homeStateCode: view.state_code,
            });
          }
          return;
        } catch (err) {
          if (!isUnusablePersistedId(err)) {
            // FAIL CLOSED on a genuine transport/5xx failure. Selecting the
            // company with a GUESSED home state is the dangerous option: the
            // client would build (say) IGST postings while the server derived
            // CGST+SGST from the real company.state_code and rejected the post
            // with a 422. Leaving company null keeps gated screens behind the
            // "no company" notice; a remount retries.
            bootstrapped.current = false;
            return;
          }
          // 400/403/404: the persisted id is malformed, inaccessible, or names a
          // company that no longer exists (fresh backend, wiped DB, different
          // environment, or a non-uuid id carried over from the in-memory mock).
          // Drop it and mint a new one below rather than wedging on a dead id.
          clearPersistedCompanyId();
        }
      }
      try {
        // Brand-new company: the create-time config default is the ONLY place a
        // state code is assumed, and it is PERSISTED server-side by this call,
        // so the value held here is the value the server will derive from.
        const stateCode = configHomeStateCode();
        const res = await client.createCompany({
          name: BOOTSTRAP_COMPANY_NAME,
          fy_start_date: BOOTSTRAP_FY_START,
          base_currency: BOOTSTRAP_CURRENCY,
          state_code: stateCode,
        });
        persistCompanyId(res.company_id);
        if (!cancelled) {
          setCompany({
            id: res.company_id,
            name: BOOTSTRAP_COMPANY_NAME,
            homeStateCode: stateCode,
          });
        }
      } catch {
        // Leave company null; gated screens stay behind the "no company" notice
        // rather than crashing. A retry happens on the next provider remount.
        bootstrapped.current = false;
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [bootstrap, client, company]);

  const value = useMemo<CompanyContextValue>(
    () => ({ client, company, setCompany }),
    [client, company],
  );
  return (
    <CompanyContext.Provider value={value}>{children}</CompanyContext.Provider>
  );
}

/** Access the full company context (throws outside a provider). */
export function useCompanyContext(): CompanyContextValue {
  const ctx = useContext(CompanyContext);
  if (!ctx) {
    throw new Error("useCompanyContext must be used within a CompanyProvider");
  }
  return ctx;
}

/** Access the configured CoredClient. */
export function useClient(): CoredClient {
  return useCompanyContext().client;
}

/**
 * Access the active company id, throwing if none is selected. Screens that
 * require a company scope (every report and voucher screen) use this; the shell
 * guarantees a company is set before such screens render.
 */
export function useCompanyID(): string {
  const { company } = useCompanyContext();
  if (!company) {
    throw new Error("no active company selected");
  }
  return company.id;
}

/**
 * The supplier (home) GST state code for the active company. Used as the
 * supplier_state_code in the server-derived GST split — when it equals the
 * voucher's place of supply the split is intra-state (CGST+SGST), else IGST.
 * "" when no company / no home state is configured (split falls back to IGST,
 * the safe default the server will re-derive). See CompanyInfo.homeStateCode.
 */
export function useHomeStateCode(): string {
  const { company } = useCompanyContext();
  return company?.homeStateCode ?? "";
}
