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
 *      ⛔ **A `?company=` that is present but NOT uuid-shaped stops here — it
 *      does NOT fall through to 3.** Falling through was a defect, not a
 *      convenience: see {@link URLCompanyId}.
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
 * What the address bar asks for. THREE states, and the difference between the
 * last two is a defect this file shipped with:
 *
 *  - `absent`    — no `?company=` at all (or one carrying nothing; see below).
 *                  Fall through to localStorage, which is the documented rule.
 *  - `malformed` — `?company=` IS present, carries something, and that something
 *                  is not a plausible uuid.
 *  - `ok`        — a plausible uuid, which WINS over whatever is persisted and is
 *                  itself persisted, so a link opens a specific set of books and
 *                  subsequent reloads stay on them (the seeded demo company —
 *                  see `scripts/seed-demo.mts` — is shared exactly this way).
 *
 * ⚠️ **`malformed` USED TO BE FOLDED INTO `absent`, AND THAT WAS THE DEFECT.**
 * This function returned `null` for both, so `?company=demo-co` (a typo, a
 * half-substituted template, an id copied out of the in-memory mock) was
 * SILENTLY DISCARDED and the bootstrap fell through to the localStorage id —
 * opening REAL, POPULATED books that nobody asked for, with nothing on screen
 * saying so. Same "work lands in the wrong company" class as the 403 that is
 * refused below, and the same rule now applies: refuse legibly, open nothing,
 * mint nothing. It cannot be answered by "send it to the server and let cored
 * judge it", because the danger is not what the server says about the bad id —
 * it is that the bad id is dropped and a GOOD id is used in its place.
 *
 * An EMPTY `?company=` (present with no value, or whitespace) is deliberately
 * `absent`, not `malformed`: it names nothing, so there is nothing to be wrong
 * about, and `App.tsx`'s `readCompanyIdInURL` — the other reader of this same
 * parameter, which decides whether D25 may adopt an invited company — treats it
 * as "no preference" too. The two readers must not disagree about one URL.
 */
type URLCompanyId =
  | { kind: "absent" }
  | { kind: "malformed"; raw: string }
  | { kind: "ok"; id: string };

function readCompanyIdFromURL(): URLCompanyId {
  try {
    const raw = new URLSearchParams(globalThis.location?.search ?? "").get(
      "company",
    );
    if (raw === null) return { kind: "absent" };
    const id = raw.trim();
    if (!id) return { kind: "absent" };
    if (!/^[0-9a-fA-F-]{36}$/.test(id)) return { kind: "malformed", raw: id };
    return { kind: "ok", id };
  } catch {
    return { kind: "absent" };
  }
}

/**
 * The id the bootstrap should try to open, AND WHICH DOOR IT CAME THROUGH —
 * resolved ONCE, before any await, so the rest of the effect never re-reads the
 * address bar to work out what the operator asked for.
 *
 * `malformed-url` is a terminal answer, not a step on the way to another one:
 * see {@link URLCompanyId}.
 */
type CompanyIdToOpen =
  | { kind: "url"; id: string }
  | { kind: "persisted"; id: string }
  | { kind: "malformed-url"; raw: string }
  | { kind: "none" };

function resolveCompanyIdToOpen(): CompanyIdToOpen {
  const fromURL = readCompanyIdFromURL();
  if (fromURL.kind === "malformed") {
    return { kind: "malformed-url", raw: fromURL.raw };
  }
  if (fromURL.kind === "ok") return { kind: "url", id: fromURL.id };
  try {
    const stored = globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY) ?? "";
    return stored ? { kind: "persisted", id: stored } : { kind: "none" };
  } catch {
    return { kind: "none" };
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
 * ⛔ **THE 404 ARM IS DEAD CODE AGAINST CORED, AND NOTHING HERE MAY ASSUME
 * OTHERWISE.** cored checks MEMBERSHIP in the auth middleware, before any
 * handler can look for the row (`auth_middleware.go`: `if !member { 403 "not a
 * member of this company" }`), so a well-formed uuid naming a company that has
 * never existed answers **403, not 404** — deliberately, so a non-member cannot
 * enumerate which tenants exist. The 404 arm is kept because the in-memory mock
 * and any future backend that answers it are still correct to; it is simply not
 * the recovery valve for a typo'd uuid. That is why {@link isMembershipRefusal}
 * has to carry its OWN way out (the notice's "forget it and continue" action) —
 * without one, `?company=<uuid that does not exist>` refuses on every load and
 * a reload can never change the answer.
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
 * ⛔ WHAT A 403 ON A COMPANY ID DOES **NOT** TELL YOU, AND WHY THE NOTICE SAYS SO.
 *
 * cored answers 403 for BOTH of these, and there is no way here to tell them
 * apart (see {@link isDeadCompanyId}):
 *
 *   - the company exists and this session's user is not a member of it, and
 *   - the company DOES NOT EXIST AT ALL — a mistyped or stale uuid.
 *
 * That is a correct server: distinguishing them would let a stranger enumerate
 * tenants. But it means the "just wait for the invite" advice is a guess, and —
 * worse — the second case can never resolve on its own. A URL id is not
 * forgotten (it lives in the address bar, and storage may hold an unrelated good
 * id), so a reload asks the same dead question and gets the same refusal, for
 * ever. Hence the notice's action: see `RefusedCompanyNotice` and
 * `forgetRefusalAndRetry`.
 */

/**
 * Which door the refused id came in through: `?company=` in the address bar, or
 * localStorage left behind by an earlier session on this browser. It changes the
 * RECOVERY and the wording of the notice — never whether the refusal is honoured.
 */
type RefusedIdSource = "url" | "persisted";

/**
 * Everything the operator is owed an explanation for. TWO shapes, because one of
 * them never reached the server at all.
 */
type CompanyRefusal =
  /** The server said 403 for an id we actually sent it (ambiguously — above). */
  | { kind: "forbidden"; id: string; source: RefusedIdSource }
  /**
   * `?company=` was present and not uuid-shaped, so NOTHING was sent and nothing
   * was opened. The refusal is OURS, not the server's — which is exactly why it
   * has to be visible: the alternative, and the defect, was opening the
   * localStorage company instead and saying nothing.
   */
  | { kind: "malformed-url"; raw: string };

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
  // WHY NO BOOKS ARE OPEN. Non-null means: nothing was opened, nothing was
  // minted, and the operator is owed an explanation naming what was reached for.
  // Rendered below; it disappears by itself once a company IS selected (F3),
  // because the notice is gated on `!company`.
  //
  // Two kinds (see {@link CompanyRefusal}): a 403 the server gave us, and a
  // `?company=` that never got that far because it is not uuid-shaped. For the
  // 403, `source` is carried because the two doors need DIFFERENT advice: a URL
  // id is still in the address bar, whereas a persisted id has already been
  // forgotten by the time this is set, so the honest thing to say is "continue
  // and you will get a fresh company".
  const [refusedCompany, setRefusedCompany] = useState<CompanyRefusal | null>(
    null,
  );
  // Bumped by {@link forgetRefusalAndRetry} to re-run the bootstrap effect
  // WITHOUT a page reload.
  //
  // ⚠️ IT HAS TO BE AN EFFECT DEPENDENCY, NOT JUST A STATE SET. Clearing
  // `refusedCompany` re-renders but does not re-run the effect (its deps —
  // bootstrap/client/company — are all unchanged), so the retry would clear the
  // notice and then do nothing at all: worse than no button, because the screen
  // would look recovered.
  const [retryTick, setRetryTick] = useState(0);
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
      const target = resolveCompanyIdToOpen();
      if (target.kind === "malformed-url") {
        // ── A `?company=` THAT IS NOT A UUID. REFUSE; DO NOT FALL THROUGH. ──
        // This used to return `null` from the URL reader, indistinguishable from
        // "no ?company= at all", so the bootstrap read localStorage next and
        // OPENED THE PREVIOUS SESSION'S BOOKS — real, populated, and nothing on
        // screen said the id in the address bar had been thrown away. Every
        // screen then answered 200 about the wrong company, which is the same
        // invisible substitution the 403 branch below exists to prevent.
        //
        // Nothing is sent to the server (there is no id to send), nothing is
        // opened, nothing is minted, and localStorage is left ALONE — the stored
        // id is very likely fine and this refusal says nothing about it. The way
        // out is the notice's action, which drops the parameter and re-runs this
        // effect; the stored id is then reached deliberately rather than by
        // accident.
        if (mounted.current) {
          setRefusedCompany({ kind: "malformed-url", raw: target.raw });
        }
        return;
      }
      const persisted = target.kind === "none" ? null : target.id;
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
          //
          // Taken from the door resolved BEFORE the await, not by re-reading the
          // address bar after it: an F3 switch or the retry action can rewrite
          // `?company=` while `getCompany` is in flight, and a second read would
          // then attribute this refusal to the wrong door.
          const askedForInURL = target.kind === "url";
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
                kind: "forbidden",
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
  }, [bootstrap, client, company, retryTick]);

  /**
   * ────────────────────────────────────────────────────────────────────────────
   * THE WAY OUT OF A REFUSAL, AND WHY THE URL DOOR NEEDED ONE AT ALL.
   * ────────────────────────────────────────────────────────────────────────────
   * The refusal rules above are right and are not being softened here: a 403 or a
   * malformed id still opens nothing and mints nothing. But two of them had NO
   * EXIT, and a control with no exit is a wedge:
   *
   *  - `?company=<uuid that does not exist>` — cored answers 403, not 404 (it
   *    checks membership before existence, on purpose), so the documented
   *    "400/404 → drop it and mint" valve NEVER FIRES for a typo. The id is not
   *    forgotten either, because it lives in the address bar. Every reload asks
   *    the same dead question and gets the same refusal, for ever.
   *  - `?company=<not a uuid>` — same shape: the parameter stays in the URL, so
   *    the refusal repeats on every load.
   *
   * "Press F3" is not an exit for either, because F3 selects among companies the
   * session IS a member of — it does not remove the parameter that is refusing,
   * and the next reload lands back on the notice.
   *
   * So: DROP `?company=` and re-run the bootstrap. That is a deliberate operator
   * act on a notice that has already named the id and stated that nothing was
   * opened or created — not a silent substitution, which is what this whole file
   * exists to prevent. Afterwards the ordinary precedence applies: the stored id
   * (reached knowingly now, not by accident), else a mint.
   *
   * ⚠️ `replaceState`, NOT a reload, and not `pushState`: a reload would throw
   * away an unsaved session for no reason, and a Back press must not walk the
   * operator back onto the refusing URL.
   */
  function forgetRefusalAndRetry(): void {
    try {
      const href = globalThis.location?.href;
      if (href) {
        const url = new URL(href);
        url.searchParams.delete("company");
        globalThis.history?.replaceState?.(
          globalThis.history?.state ?? null,
          "",
          `${url.pathname}${url.search}${url.hash}`,
        );
      }
    } catch {
      // Non-fatal (same reasoning as persistCompanyId). The retry below still
      // runs; on the persisted-403 door there is no parameter to drop anyway.
    }
    setRefusedCompany(null);
    // Let the effect run again (it guards on this ref), and give it a dependency
    // change to run FROM. Both are required — see `retryTick`.
    bootstrapped.current = false;
    setRetryTick((n) => n + 1);
  }

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
          refusal={refusedCompany}
          onForgetAndRetry={forgetRefusalAndRetry}
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
 * app used to mint a new "My Company" owned by this user and open that (403), or
 * quietly open the localStorage company (a malformed `?company=`), so every
 * screen answered 200 and nothing on screen distinguished the requested tenant
 * from a different one. It also states what did NOT happen (no company opened,
 * none created), since that is the fact an auditor needs, and — this is the part
 * that was missing — gives an action that actually ENDS the refusal.
 *
 * THREE WORDINGS, ONE RULE. The rule never varies (nothing opened, nothing
 * minted); the wording and the exit do:
 *
 *  - `url` — a `?company=<uuid>` the server refused. ⛔ It must NOT assert that
 *    the company exists and the user merely lacks membership: cored answers 403
 *    for a company that does not exist AT ALL, so "claim your invite and reload"
 *    on its own is a guess that can never come true. Both possibilities are
 *    stated, and the exit drops the parameter.
 *  - `persisted` — found in this browser's storage. Telling that operator "you
 *    asked for those books with ?company=…" would be a lie about their own
 *    address bar and would hide the thing they need: the stale id has been
 *    forgotten, so continuing starts them clean.
 *  - `malformed-url` — never sent to the server at all. This one must say what
 *    was IGNORED and what would have been opened in its place, because that
 *    substitution is the defect and it is otherwise undetectable.
 */
function RefusedCompanyNotice({
  refusal,
  onForgetAndRetry,
}: {
  refusal: CompanyRefusal;
  onForgetAndRetry: () => void;
}) {
  const source = refusal.kind === "malformed-url" ? "url-malformed" : refusal.source;
  return (
    <div
      role="alert"
      data-testid="company-refused-notice"
      data-refused-source={source}
      className="fixed inset-x-0 top-0 z-50 border-b-2 border-red-500 bg-tally-panel p-3 font-mono text-sm text-tally-field"
    >
      {refusal.kind === "malformed-url" ? (
        <>
          <p className="font-bold text-red-400">
            Not a company id — <code>?company={refusal.raw}</code> was ignored.
          </p>
          <p>
            That is not a valid company id, so it was never sent to the server.
            No company has been opened and none was created. In particular this
            browser has NOT been quietly switched to whichever company it had
            stored from an earlier session — those would be real books, keyed
            with real vouchers, that nobody asked for.
          </p>
          <p>
            Fix the <code>?company=</code> in the address bar, or drop it and
            carry on with the company this browser already knows:
          </p>
        </>
      ) : (
        <>
          <p className="font-bold text-red-400">
            Not your books — company {refusal.id} was refused (HTTP 403).
          </p>
          {refusal.source === "url" ? (
            <p>
              You asked for those books with <code>?company={refusal.id}</code>,
              but the server refused. That answer means one of two things and does
              not say which: either this session&apos;s user is not a member of
              that company, or <strong>no such company exists</strong> — cored
              checks membership before existence on purpose, so a mistyped or
              deleted id looks exactly like someone else&apos;s books. No company
              has been opened and none was created — nothing you key now would
              land anywhere near the company you asked for.
            </p>
          ) : (
            <p>
              This browser still had company <code>{refusal.id}</code> stored from
              an earlier session, but the server refused it. No company has been
              opened and none was created — in particular you have NOT been made
              Owner of a new, empty company that would answer every screen with a
              cheerful 200. The stale id has been forgotten, so continuing starts
              clean.
            </p>
          )}
          <p>
            If you were just invited, claim the invite and reload. Otherwise press
            <strong> F3 (Select Company)</strong> to open a company you are a
            member of. (For a seeded demo company, a 403 usually means it was
            seeded under a different subject than the app authenticates as — see
            scripts/seed-demo.mts.)
          </p>
          {refusal.source === "url" ? (
            <p>
              ⛔ A reload will NOT change this answer: the id is still in the
              address bar, so the same refusal repeats for ever. If the id is
              wrong or names a company that is gone, drop it and carry on:
            </p>
          ) : (
            <p>Or start over without it:</p>
          )}
        </>
      )}
      {/*
        The one control on this notice. It is a real <button> so it is reachable
        by keyboard, but it is NOT autofocused: the shell's key capture must keep
        seeing F9/F3 (the notice's own advice names F3), and stealing focus from
        the screen underneath would be a second defect on top of the first.
      */}
      <button
        type="button"
        data-testid="company-refused-forget"
        onClick={onForgetAndRetry}
        className="mt-1 border border-tally-field px-2 py-0.5 underline"
      >
        {refusal.kind === "forbidden" && refusal.source === "persisted"
          ? "Forget it and start a fresh company"
          : "Drop ?company= and continue"}
      </button>
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
