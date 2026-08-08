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

/**
 * localStorage key under which the bootstrapped company id is persisted.
 *
 * EXPORTED for the auth layer: signing out must forget this key. It is
 * localStorage (not sessionStorage), so without the clear the NEXT user to sign
 * in on the same browser hydrates the PREVIOUS user's company id and cored
 * answers 403 (not a member). That 403 is now reported rather than recovered
 * from by minting (see {@link isMembershipRefusal} and the bootstrap effect) —
 * but a notice the second user has to read and act on is still a worse experience
 * than never reaching for the first user's books at all. See
 * `src/auth/AuthContext.tsx` signOut().
 */
export const COMPANY_STORAGE_KEY = "tally.companyId";

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

/**
 * ────────────────────────────────────────────────────────────────────────────
 * COMPANY SELECTION PRECEDENCE — ONE STORY. Read this before changing either
 * this file or `screens/CompanySelectScreen.tsx`; getting it wrong means the
 * operator's next voucher is written to the WRONG COMPANY.
 *
 *   1. An explicit F3 switch, recorded through {@link rememberChosenCompanyId}.
 *      This is the operator's most recent deliberate act, so it outranks the
 *      link that happened to open the tab: it persists the chosen id AND
 *      rewrites a `?company=` that is still sitting in the address bar.
 *   2. `?company=<uuid>` in the URL (below). It beats localStorage, and on a
 *      successful hydrate it BECOMES the persisted id.
 *   3. The persisted id in localStorage (the previous session's company).
 *   4. Nothing at all -> mint "My Company" (only when `bootstrap` is on).
 *
 * 1 and 2 never fight, because a switch makes the URL AGREE with the choice
 * instead of adding an exception to "the URL wins".
 *
 * A REFUSAL IS NOT A FALL-THROUGH, AND THE DOOR THE ID CAME THROUGH DOES NOT
 * CHANGE THAT. Whether the id came from `?company=` (step 2) or from
 * localStorage (step 3), a 403 leaves the company UNSELECTED with a notice and
 * mints NOTHING. **Only a junk/dead id — 400 or 404 — reaches step 4.** The two
 * 403 cases differ in ONE respect, the recovery: a URL id is left alone (the
 * address bar still shows what was asked for, and storage may hold an unrelated
 * good id), while a persisted id is FORGOTTEN so the operator is not wedged on
 * it forever — see the bootstrap effect for why that is the way out and not a
 * loophole.
 *
 * Step 4 is also the ONLY minting path, and it needs `bootstrap`. An explicit
 * `initialCompany` prop short-circuits the whole effect (`company` is already
 * truthy), so it neither hydrates, nor persists, nor mints — see the effect.
 * ────────────────────────────────────────────────────────────────────────────
 */

/**
 * A company id supplied in the URL (`?company=<uuid>`). It WINS over whatever is
 * persisted and is itself persisted, so a link opens a specific set of books and
 * subsequent reloads stay on them — the seeded demo company (see
 * `scripts/seed-demo.mts`) is shared exactly this way. Anything that is not a
 * plausible uuid is ignored rather than sent to the server.
 */
function readCompanyIdFromURL(): string | null {
  try {
    const raw = new URLSearchParams(globalThis.location?.search ?? "").get(
      "company",
    );
    if (!raw) return null;
    const id = raw.trim();
    return /^[0-9a-fA-F-]{36}$/.test(id) ? id : null;
  } catch {
    return null;
  }
}

function readPersistedCompanyId(): string | null {
  const fromURL = readCompanyIdFromURL();
  if (fromURL) return fromURL;
  try {
    return globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY) ?? null;
  } catch {
    return null;
  }
}

/** Forget the persisted company id (used on a dead id, and on sign-out). */
export function clearPersistedCompanyId(): void {
  try {
    globalThis.localStorage?.removeItem(COMPANY_STORAGE_KEY);
  } catch {
    // Non-fatal (see persistCompanyId).
  }
}

/**
 * A bootstrap company id is DEAD — safe to drop and replace with a freshly
 * minted company — when the server says it is malformed (400 "invalid company
 * id") or that it does not exist (404). Both happen routinely after switching
 * backends, wiping the DB, or carrying over an id minted by the in-memory mock
 * (whose ids are NOT uuids, so cored 400s on them). A dead id names no books, so
 * there is nothing to be wrong ABOUT: opening a new company cannot be mistaken
 * for opening someone else's.
 *
 * ⚠️ **400 AND 404 ARE THE ONLY TWO STATUSES THAT MAY STILL MINT.** This
 * predicate used to include 403 as well, and that third arm was the defect:
 *   - 403 (not a member) is NOT "the id is junk" — the company exists and the id
 *     is well formed, so minting a different company silently substitutes real
 *     books with empty ones. It is handled before this predicate is consulted;
 *     see {@link isMembershipRefusal}.
 *   - anything else (5xx, a transport failure, 401) leaves the id's validity
 *     UNKNOWN, so the bootstrap fails closed rather than guessing (below).
 */
function isDeadCompanyId(err: unknown): boolean {
  return (
    err instanceof ApiError && (err.status === 400 || err.status === 404)
  );
}

/**
 * True when the server refused the id because THIS SESSION IS NOT A MEMBER of
 * that company (403). Unlike 400/404 this is not "the id is junk": the company
 * exists, the id is well formed, and the state is possibly TRANSIENT — an
 * unclaimed invite, or a seed run under a different SEED_SUBJECT, resolves it.
 * So it is REPORTED, never recovered from by opening something else.
 *
 * ⚠️ THIS IS DOOR-BLIND ON PURPOSE, AND THAT WAS ROUND 2 OF THE SAME BUG. The
 * first fix only honoured a 403 when `readCompanyIdFromURL() === persisted`; a
 * browser re-drive then planted a foreign company id directly into localStorage
 * (which is exactly what an operator has after any earlier visit), opened `/`
 * with no `?company=` at all, and watched the app mint them a brand-new tenant
 * and make them its Owner without a word. "You are not a member of this
 * company" means the same thing whether the id came from a link or from this
 * browser's own storage, so the refusal must too.
 */
function isMembershipRefusal(err: unknown): boolean {
  return err instanceof ApiError && err.status === 403;
}

/**
 * Which door the refused id came in through: `?company=` in the address bar, or
 * localStorage left behind by an earlier session on this browser. It changes the
 * RECOVERY and the wording of the notice — never whether the refusal is honoured.
 */
type RefusedIdSource = "url" | "persisted";

function persistCompanyId(id: string): void {
  try {
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, id);
  } catch {
    // Non-fatal: a private-mode / SSR environment without localStorage still
    // works, it just won't reuse the company across reloads.
  }
}

/**
 * Record the company the OPERATOR JUST CHOSE at F3 (see
 * `screens/CompanySelectScreen.tsx`). Step 1 of the precedence block above.
 *
 * It does TWO things, and both are load-bearing:
 *
 *  - PERSISTS the id. Without this the switch lived only in React state, so any
 *    page load — a reload, a fresh tab, a crash recovery — silently returned the
 *    operator to the previous company AND THE NEXT VOUCHER THEY KEYED WAS
 *    WRITTEN TO IT. That is the whole defect this function exists to close.
 *  - REWRITES a `?company=` that is still in the address bar. Persisting alone
 *    is not enough: {@link readPersistedCompanyId} prefers the URL on purpose,
 *    so a stale `?company=<old company>` would out-rank the freshly persisted
 *    choice on that same reload and the switch STILL would not survive. Making
 *    the URL agree with the choice keeps "the URL wins" true rather than
 *    carving an exception into it, and keeps the link shareable/copyable as what
 *    is actually on screen.
 *
 * Deliberate limits:
 *  - `replaceState`, not `pushState`: switching company is not a navigation, and
 *    a Back press must not walk the operator into the other company's books.
 *  - a URL with NO `?company=` does not get one added — a plain `/` stays a
 *    plain `/`, and every audit probe / demo-seed link keeps its exact shape.
 */
export function rememberChosenCompanyId(id: string): void {
  persistCompanyId(id);
  try {
    const href = globalThis.location?.href;
    if (!href) return;
    const url = new URL(href);
    if (!url.searchParams.has("company")) return;
    if (url.searchParams.get("company") === id) return;
    url.searchParams.set("company", id);
    globalThis.history?.replaceState?.(
      globalThis.history?.state ?? null,
      "",
      `${url.pathname}${url.search}${url.hash}`,
    );
  } catch {
    // Non-fatal (same reasoning as persistCompanyId): the live session is
    // already re-scoped, this only protects the NEXT load.
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
  // The company id the server REFUSED for this session (403, "not a member"),
  // plus WHICH DOOR it came in through. Non-null means: nothing was opened,
  // nothing was minted, and the operator is owed an explanation naming the books
  // that were reached for. Rendered below; it disappears by itself once a company
  // IS selected (F3), because the notice is gated on `!company`.
  //
  // `source` is carried because the two cases need DIFFERENT advice: a URL id is
  // still in the address bar and can simply be dropped, whereas a persisted id
  // has already been forgotten by the time this is set, so the honest thing to
  // say is "reload and you will get a fresh company".
  const [refusedCompany, setRefusedCompany] = useState<{
    id: string;
    source: RefusedIdSource;
  } | null>(null);
  // Guard so React 18 StrictMode's double-invoke (and re-renders) don't mint two
  // companies. Bootstrap runs at most once per provider instance.
  const bootstrapped = useRef(false);
  // Whether this provider is still mounted, so the async bootstrap only commits
  // state when it is. CRITICAL for StrictMode: a per-effect `cancelled` flag does
  // NOT work here — StrictMode mounts, cleans up (which would set cancelled), then
  // REMOUNTS, but `bootstrapped` blocks the remount from re-running, so the sole
  // in-flight bootstrap would see cancelled=true and silently drop its setCompany
  // (company stays null -> "Preparing company…" forever). A ref that is reset to
  // true on the remount fixes it: by the time the async network call resolves, the
  // remount has set mounted back to true, so the commit lands.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  useEffect(() => {
    if (!bootstrap || company || bootstrapped.current) return;
    bootstrapped.current = true;
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
          // A URL-supplied company becomes the persisted one, so a shared link
          // (e.g. the seeded demo company) survives the next plain reload.
          persistCompanyId(view.id || persisted);
          if (mounted.current) {
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
          // Did the OPERATOR ask for this id, or did we merely find it in
          // localStorage? This does NOT decide whether a 403 is honoured (it
          // always is, below) — only how we recover and what the notice says.
          const askedForInURL = readCompanyIdFromURL() === persisted;
          if (isMembershipRefusal(err)) {
            // ── 403 FROM EITHER DOOR: REPORT, NEVER MINT. ─────────────────
            // "When the id came from the URL the user ASKED for those books, so
            // say why they are not getting them instead of silently opening
            // different ones" — that intent used to be implemented as a
            // console.warn followed by minting a brand-new company with this
            // user as its Owner. A console.warn is not a UI: every screen then
            // returned 200 against a different, EMPTY tenant, which reads as
            // "no refusal, everything permitted" to anyone (human or agent)
            // looking at the app.
            //
            // ⚠️ AND THAT ARGUMENT NEVER DEPENDED ON THE URL. Round 1 gated
            // this on `askedForInURL`; a browser re-drive then planted a foreign
            // company id in localStorage — the state any operator is in after an
            // earlier visit, and the state a signed-out-then-signed-in second
            // user is in — loaded a bare `/`, and the identical defect fired:
            // a DIFFERENT, brand-new id in storage afterwards and the user made
            // Owner of an empty tenant, silently. So the 403 is honoured
            // whichever door the id arrived through.
            if (!askedForInURL) {
              // ── THE WAY OUT, and the reason this is not a wedge. ──────────
              // A persisted id genuinely goes stale (backend switch, wiped DB,
              // a non-uuid id from the in-memory mock), and F3 can only OPEN a
              // company you are already a member of — it cannot create one (see
              // screens/CompanySelectScreen.tsx). If a 403 both refused to mint
              // AND kept the id, a developer whose DB was wiped would be stuck
              // on a dead id with no door at all. So FORGET the id: this load
              // still opens nothing and creates nothing (the operator gets the
              // notice and an accurate account of what was reached for), and the
              // NEXT load is the ordinary "no id at all" case, which mints.
              //
              // That keeps the dangerous half closed — the id that named someone
              // else's books never silently becomes a new tenant while it is
              // still on screen — without leaving anyone trapped.
              //
              // A URL id is deliberately NOT cleared: it lives in the address
              // bar, not in storage, and storage may hold an unrelated good id
              // that this refusal says nothing about. (When the URL id and the
              // stored id are the SAME foreign id, dropping `?company=` moves
              // the next load onto this branch, which then clears it — so the
              // two rules chain to an exit rather than fighting.)
              clearPersistedCompanyId();
            }
            if (mounted.current) {
              setRefusedCompany({
                id: persisted,
                source: askedForInURL ? "url" : "persisted",
              });
            }
            return;
          }
          if (!isDeadCompanyId(err)) {
            // FAIL CLOSED on a genuine transport/5xx failure. Selecting the
            // company with a GUESSED home state is the dangerous option: the
            // client would build (say) IGST postings while the server derived
            // CGST+SGST from the real company.state_code and rejected the post
            // with a 422. Leaving company null keeps gated screens behind the
            // "no company" notice; a remount retries.
            bootstrapped.current = false;
            return;
          }
          // A DEAD id, AND THE ONLY REMAINING PATH TO THE MINT BELOW:
          // malformed (400) or naming a company that no longer exists (404) — a
          // fresh backend, a wiped DB, a different environment, or a non-uuid id
          // carried over from the in-memory mock (cored 400s on those). Drop it
          // and mint a new one rather than wedging on it: a dead id names no
          // books, so nothing can be substituted for anything.
          //
          // NO 403 REACHES HERE ANY MORE, from either door — both returned above
          // with a notice. Minting over one is precisely the "silently opening
          // different ones" the comment there warns about, and doing it for the
          // localStorage door only is how this bug survived its first fix.
          if (askedForInURL) {
            const status = err instanceof ApiError ? err.status : "?";
            console.warn(
              `[company] ?company=${persisted} was rejected by the server (${status}) ` +
                "as a malformed or unknown company id. Opening a new company instead.",
            );
          }
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
        if (mounted.current) {
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
  }, [bootstrap, client, company]);

  const value = useMemo<CompanyContextValue>(
    () => ({ client, company, setCompany }),
    [client, company],
  );
  return (
    <CompanyContext.Provider value={value}>
      {/*
        The refusal notice sits ABOVE the shell, not INSTEAD of it: the notice's
        own advice is "press F3", and F3 (Select Company) is a screen inside the
        shell. Replacing the children would name a door and then remove it.
        `position: fixed` keeps it out of the shell's full-height layout, and it
        holds no focus, so the shell's key capture still sees F3.
      */}
      {refusedCompany && !company ? (
        <RefusedCompanyNotice
          id={refusedCompany.id}
          source={refusedCompany.source}
        />
      ) : null}
      {children}
    </CompanyContext.Provider>
  );
}

/**
 * Blocking, legible answer to "why am I not looking at the books that were
 * reached for".
 *
 * It NAMES THE REFUSED ID, because the failure it replaces was invisible: the
 * app used to mint a new "My Company" owned by this user and open that, so every
 * screen answered 200 and nothing on screen distinguished the requested tenant
 * from an empty new one. It also states what did NOT happen (no company opened,
 * none created), since that is the fact an auditor needs, and points at the one
 * door that fixes it.
 *
 * TWO WORDINGS, ONE REFUSAL. `source` exists because the second half of this bug
 * was found by a browser re-drive that planted the id in localStorage instead of
 * the URL: telling that operator "you asked for those books with ?company=…"
 * would be a lie about their own address bar, and — worse — would hide the one
 * thing they need to know, that the stale id has been forgotten and a reload
 * therefore starts them clean instead of wedging them.
 */
function RefusedCompanyNotice({
  id,
  source,
}: {
  id: string;
  source: RefusedIdSource;
}) {
  return (
    <div
      role="alert"
      data-testid="company-refused-notice"
      data-refused-source={source}
      className="fixed inset-x-0 top-0 z-50 border-b-2 border-red-500 bg-tally-panel p-3 font-mono text-sm text-tally-field"
    >
      <p className="font-bold text-red-400">
        Not your books — company {id} was refused (HTTP 403).
      </p>
      {source === "url" ? (
        <p>
          You asked for those books with <code>?company={id}</code>, but this
          session&apos;s user is not a member of that company. No company has
          been opened and none was created — nothing you key now would land
          anywhere near the company you asked for.
        </p>
      ) : (
        <p>
          This browser still had company <code>{id}</code> stored from an earlier
          session, but this session&apos;s user is not a member of it. No company
          has been opened and none was created — in particular you have NOT been
          made Owner of a new, empty company that would answer every screen with
          a cheerful 200. The stale id has been forgotten, so a reload starts
          clean.
        </p>
      )}
      <p>
        If you were just invited, claim the invite and reload. Otherwise press
        <strong> F3 (Select Company)</strong> to open a company you are a member
        of{source === "persisted"
          ? ", or reload to start a fresh company"
          : ""}
        . (For a seeded demo company, a 403 usually means it was seeded under a
        different subject than the app authenticates as — see
        scripts/seed-demo.mts.)
      </p>
    </div>
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
