import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { login as loginRequest, type AuthUser } from "../api/auth";
import { clearPersistedCompanyId } from "../app/CompanyContext";
import { stepUp as runStepUp } from "./webauthn";

/**
 * The browser session: who is signed in and the bearer token every subsequent
 * cored call carries.
 *
 * PERSISTENCE IS `sessionStorage`, DELIBERATELY. A closed tab is a logged-out
 * tab; localStorage would leave a bearer token on a shared machine indefinitely
 * and would also silently share one identity across every tab of that browser
 * profile. The token is short-lived server-side anyway (expires_in), so the
 * worst case of a lost sessionStorage entry is one extra sign-in.
 */
export interface AuthSession {
  token: string;
  user: AuthUser;
}

/**
 * Where the WebAuthn step-up ceremony is. `"failed"` is sticky until the next
 * attempt so the chrome can SAY a passkey was refused — a step-up that dies
 * silently looks to the operator like a dead keystroke.
 */
export type StepUpState = "idle" | "pending" | "failed";

export interface AuthContextValue {
  /** null = nobody is signed in; the shell must not render. */
  session: AuthSession | null;
  /** Resolves on success; REJECTS with AuthInvalidError / AuthLockedError / Error. */
  signIn(subject: string, password: string): Promise<void>;
  /** Explicit sign-out: drops the session AND the persisted company id. */
  signOut(): void;
  /** A 401 from any mid-session API call: drop the session, show the login. */
  onUnauthorized(): void;
  /**
   * Perform a WebAuthn step-up and REPLACE the session token with the returned
   * `amr:["webauthn"]` one. Resolves when the session can write; REJECTS with
   * StepUpUnsupportedError / StepUpFailedError.
   *
   * MEMOISED WHILE IN FLIGHT: concurrent callers (several 403s at once — an
   * screen that creates two ledgers before posting does just that) join ONE
   * ceremony. cored's challenge store is single-use per user, so two overlapping
   * `begin`s would make one `finish` fail.
   */
  stepUp(): Promise<void>;
  /** Ceremony state, for the chrome to render. */
  stepUpState: StepUpState;
  /**
   * The LIVE bearer, readable synchronously. `session.token` is React state and
   * therefore still stale in the microtask right after a ceremony resolves,
   * which is exactly when the 403 retry needs the new token — so the retry
   * reads it from here instead.
   */
  currentToken(): string | null;
}

/** sessionStorage key holding the serialized {@link AuthSession}. */
export const AUTH_STORAGE_KEY = "tally.auth";

/**
 * `null` OUTSIDE a provider — never a throw. The chrome renders in mock/test
 * mode with no AuthProvider above it (App only mounts one in live mode), so a
 * throwing context idiom here would break every existing shell test.
 */
const AuthContext = createContext<AuthContextValue | null>(null);

function readPersistedSession(): AuthSession | null {
  try {
    const raw = globalThis.sessionStorage?.getItem(AUTH_STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<AuthSession>;
    // Validate rather than trust: a half-written or stale-shaped entry must
    // land on the login screen, not crash the shell on `session.user.subject`.
    if (
      !parsed ||
      typeof parsed.token !== "string" ||
      !parsed.token ||
      !parsed.user ||
      typeof parsed.user.subject !== "string"
    ) {
      return null;
    }
    return { token: parsed.token, user: parsed.user as AuthUser };
  } catch {
    return null;
  }
}

function persistSession(session: AuthSession): void {
  try {
    globalThis.sessionStorage?.setItem(AUTH_STORAGE_KEY, JSON.stringify(session));
  } catch {
    // Non-fatal: private mode without sessionStorage still works, the session
    // just does not survive a reload.
  }
}

function forgetSession(): void {
  try {
    globalThis.sessionStorage?.removeItem(AUTH_STORAGE_KEY);
  } catch {
    // Non-fatal (see persistSession).
  }
}

/**
 * Owns the browser's identity. Mounted by {@link App} in live mode ONLY; below
 * it the shell renders only once {@link AuthContextValue.session} is non-null.
 */
export function AuthProvider({
  baseUrl,
  fetchImpl,
  children,
}: {
  /** Base URL for `/auth/*` — "" (same-origin) behind the Vite dev proxy. */
  baseUrl: string;
  /** Injected fetch (tests). Defaults to the global one. */
  fetchImpl?: typeof fetch;
  children: ReactNode;
}) {
  const [session, setSession] = useState<AuthSession | null>(() =>
    readPersistedSession(),
  );
  const [stepUpState, setStepUpState] = useState<StepUpState>("idle");

  // Keep the callbacks referentially stable: the REST client's fetch wrapper is
  // memoized on onUnauthorized, and a new identity there would rebuild the
  // client every render and re-trigger the company bootstrap effect.
  const baseUrlRef = useRef(baseUrl);
  baseUrlRef.current = baseUrl;
  const fetchRef = useRef(fetchImpl);
  fetchRef.current = fetchImpl;

  // The session as a REF as well as state: the step-up retry must read the new
  // token in the same microtask the ceremony resolves in, long before React has
  // re-rendered anything.
  const sessionRef = useRef<AuthSession | null>(session);
  const commit = useCallback((next: AuthSession | null) => {
    sessionRef.current = next;
    if (next) persistSession(next);
    else forgetSession();
    setSession(next);
  }, []);

  const signIn = useCallback(
    async (subject: string, password: string) => {
      const res = await loginRequest(
        baseUrlRef.current,
        subject,
        password,
        fetchRef.current,
      );
      // A new identity starts with a clean step-up state — a previous user's
      // failed ceremony must not be reported against this one.
      setStepUpState("idle");
      commit({ token: res.token, user: res.user });
    },
    [commit],
  );

  const onUnauthorized = useCallback(() => {
    // Session only. The persisted COMPANY is intentionally KEPT: a 401 is
    // usually an expired token for the SAME person, and dropping their company
    // id would make the next sign-in mint them a brand-new empty company
    // instead of returning them to their books.
    commit(null);
  }, [commit]);

  const signOut = useCallback(() => {
    // An explicit sign-out means "somebody else may use this browser next", so
    // the company id MUST go too — otherwise the next user hydrates this user's
    // company, gets a 403, and CompanyContext silently mints them a new one.
    clearPersistedCompanyId();
    setStepUpState("idle");
    commit(null);
  }, [commit]);

  // The single in-flight ceremony. A ref, not state: it must be readable and
  // writable synchronously by every concurrent caller.
  const inFlight = useRef<Promise<void> | null>(null);

  const stepUp = useCallback((): Promise<void> => {
    if (inFlight.current) return inFlight.current;
    const current = sessionRef.current;
    if (!current) {
      return Promise.reject(
        new Error("cannot step up: nobody is signed in"),
      );
    }
    setStepUpState("pending");
    const run = runStepUp({
      baseUrl: baseUrlRef.current,
      token: current.token,
      // The RAW fetch on purpose: routing the ceremony through the app's
      // 403-retry interceptor would recurse.
      fetchImpl: fetchRef.current ?? globalThis.fetch.bind(globalThis),
    })
      .then((token) => {
        // REPLACE the token, keep the identity, and re-persist under the same
        // key so a reload keeps the ability to write.
        commit({ ...(sessionRef.current ?? current), token });
        setStepUpState("idle");
      })
      .catch((err: unknown) => {
        setStepUpState("failed");
        throw err;
      })
      .finally(() => {
        // Clear the memo when it SETTLES, so a later 403 can try again (and a
        // refused passkey is retryable) — but never mid-ceremony.
        inFlight.current = null;
      });
    inFlight.current = run;
    return run;
  }, [commit]);

  const currentToken = useCallback(() => sessionRef.current?.token ?? null, []);

  const value = useMemo<AuthContextValue>(
    () => ({
      session,
      signIn,
      signOut,
      onUnauthorized,
      stepUp,
      stepUpState,
      currentToken,
    }),
    [
      session,
      signIn,
      signOut,
      onUnauthorized,
      stepUp,
      stepUpState,
      currentToken,
    ],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

/**
 * The auth context, or `null` when there is no {@link AuthProvider} above —
 * which is the normal state in mock/test mode. Callers MUST handle null (the
 * chrome renders its signed-in indicator only when a session exists).
 */
export function useAuth(): AuthContextValue | null {
  return useContext(AuthContext);
}
