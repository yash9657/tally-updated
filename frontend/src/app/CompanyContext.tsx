import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
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

function readPersistedCompanyId(): string | null {
  try {
    return globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY) ?? null;
  } catch {
    return null;
  }
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
        // Reuse the known company id (the server holds its data); we only need
        // the id to scope routes. Name is cosmetic until a report loads.
        if (!cancelled) setCompany({ id: persisted, name: BOOTSTRAP_COMPANY_NAME });
        return;
      }
      try {
        const res = await client.createCompany({
          name: BOOTSTRAP_COMPANY_NAME,
          fy_start_date: BOOTSTRAP_FY_START,
          base_currency: BOOTSTRAP_CURRENCY,
        });
        persistCompanyId(res.company_id);
        if (!cancelled) {
          setCompany({ id: res.company_id, name: BOOTSTRAP_COMPANY_NAME });
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
