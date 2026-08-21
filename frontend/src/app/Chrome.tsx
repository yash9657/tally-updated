import { useCallback, useState, type ReactNode } from "react";
import type { ButtonBarItem } from "../keystroke/buttonBar";
import { ButtonBar } from "../screens/ButtonBar";
import { useAuth } from "../auth/AuthContext";

/**
 * The persistent Tally chrome: a top product/company bar, the main screen area,
 * the fixed right-side button bar, a status line, and the bottom keyboard-hint
 * strip. The chrome is screen-agnostic — it lays out whatever the router renders
 * into `children` and shows the button bar the active screen supplies.
 */
export function Chrome({
  companyName,
  screenTitle,
  buttonBar,
  hints,
  status,
  onButton,
  children,
}: {
  companyName: string;
  /** Surfaced to assistive tech via the top bar's aria-label, not duplicated as
   * visible text (the active screen body already renders its own heading). */
  screenTitle: string;
  buttonBar: readonly ButtonBarItem[];
  hints: readonly string[];
  /** What the last key press did (or why it did nothing). Null hides the line. */
  status?: string | null;
  /** Fire a button-bar item as if its combo had been typed. */
  onButton?: (combo: string) => void;
  children: ReactNode;
}) {
  return (
    <div className="flex h-full w-full flex-col bg-tally-bg font-mono text-tally-field">
      <TopBar companyName={companyName} screenTitle={screenTitle} />
      <div className="flex min-h-0 flex-1">
        <main className="min-w-0 flex-1 overflow-auto" aria-label="Screen">
          {children}
        </main>
        <ButtonBar items={buttonBar} onPress={onButton} />
      </div>
      <StatusLine status={status ?? null} />
      <BottomHints hints={hints} />
    </div>
  );
}

/**
 * SELF-SERVICE PASSWORD CHANGE (D55's second half).
 *
 * `POST /auth/password` shipped with a client function and NO CALLER, so nobody
 * in this product could change their own password. That is not merely a missing
 * convenience: D55's other half is an ADMINISTRATIVE UNLOCK, and an unlock
 * without a password change means the remedy for a compromised credential is to
 * hand the account back with the same compromised password.
 *
 * WHY IT LIVES IN THE CHROME, AS A BUTTON, WITH NO KEYSTROKE: exactly TopBar's
 * own documented reasoning for Sign Out. RESEARCH.md §21's master shortcut table
 * binds no key to changing a password, and §15 describes "Change User" as a
 * Gateway FEATURE with no key — so inventing a combo is precisely the fake
 * binding `keystroke/buttons.test.ts` exists to catch. A <button> is natively
 * Tab-reachable, so a keyboard-only drive can still reach it.
 *
 * ⛔ IT NAMES THE LOCKOUT CONSEQUENCE. cored routes a wrong `current_password`
 * through the SAME lockout budget as POST /auth/login (a password guess is a
 * password guess), so getting it wrong five times locks the account this form is
 * being used from. A form that did not say so would be a trap: the operator
 * would read the 423 as the product breaking.
 */
function ChangePasswordPanel() {
  const auth = useAuth();
  const [open, setOpen] = useState(false);
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  const reset = useCallback(() => {
    setCurrent("");
    setNext("");
    setConfirm("");
    setError(null);
    setDone(false);
    setBusy(false);
  }, []);

  const submit = useCallback(async () => {
    // The confirmation is checked HERE and not sent: the server has no second
    // field to compare against, so a mistyped new password would otherwise be
    // saved silently and lock the operator out at the next sign-in.
    if (next !== confirm) {
      setError("The new password and its confirmation do not match.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await auth?.changePassword(current, next);
      setDone(true);
      setCurrent("");
      setNext("");
      setConfirm("");
    } catch (e) {
      // The server's own words: the 400 carries the password POLICY failure and
      // the 423 carries the lock. Paraphrasing either would hide which rule was
      // broken, and the policy is the server's to state.
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [auth, current, next, confirm]);

  if (!open) {
    return (
      <button
        type="button"
        data-testid="change-password-open"
        onClick={() => {
          reset();
          setOpen(true);
        }}
        className="bg-tally-bar px-2 text-tally-barText"
      >
        Change Password
      </button>
    );
  }

  return (
    <form
      data-testid="change-password-form"
      className="flex items-center gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <label className="flex items-center gap-1">
        <span className="text-tally-field">Current</span>
        <input
          type="password"
          data-testid="change-password-current"
          value={current}
          autoComplete="current-password"
          onChange={(e) => setCurrent(e.target.value)}
          className="w-28 bg-tally-field px-1 text-tally-fieldText"
        />
      </label>
      <label className="flex items-center gap-1">
        <span className="text-tally-field">New</span>
        <input
          type="password"
          data-testid="change-password-new"
          value={next}
          autoComplete="new-password"
          onChange={(e) => setNext(e.target.value)}
          className="w-28 bg-tally-field px-1 text-tally-fieldText"
        />
      </label>
      <label className="flex items-center gap-1">
        <span className="text-tally-field">Confirm</span>
        <input
          type="password"
          data-testid="change-password-confirm"
          value={confirm}
          autoComplete="new-password"
          onChange={(e) => setConfirm(e.target.value)}
          className="w-28 bg-tally-field px-1 text-tally-fieldText"
        />
      </label>
      <button
        type="submit"
        data-testid="change-password-submit"
        disabled={busy}
        className="bg-tally-bar px-2 text-tally-barText"
      >
        {busy ? "Saving…" : "Save"}
      </button>
      <button
        type="button"
        data-testid="change-password-cancel"
        onClick={() => {
          reset();
          setOpen(false);
        }}
        className="bg-tally-panel px-2 text-tally-field"
      >
        Cancel
      </button>
      {done ? (
        <span
          role="status"
          data-testid="change-password-done"
          className="bg-tally-field px-2 text-tally-fieldText"
        >
          Password changed. This session stays signed in.
        </span>
      ) : null}
      {error ? (
        <span
          role="alert"
          data-testid="change-password-error"
          className="bg-tally-bar px-2 font-bold text-tally-barKey"
        >
          {error}
        </span>
      ) : null}
      {!done && !error ? (
        <span className="text-tally-field">
          A wrong current password counts toward the sign-in lockout.
        </span>
      ) : null}
    </form>
  );
}

/**
 * The product/company top bar. Tally's bar shows the product on the left and
 * the active company on the right; the current screen's name lives in the
 * screen body (so the chrome never duplicates the body heading text). The
 * screen title is exposed to assistive tech via the bar's aria-label.
 *
 * IT ALSO ANSWERS "WHO AM I". The signed-in subject and a Sign Out control
 * render here — and ONLY when a session exists, so the mock/test top bar is
 * byte-identical to what it was before the login landed (useAuth() returns null
 * outside an AuthProvider; see AuthContext).
 *
 * AND IT REPORTS THE WEBAUTHN STEP-UP. The first WRITE of a password-only
 * session is answered 403 STEPUP_REQUIRED and App's transport transparently
 * runs a passkey ceremony (see App.bearerFetch). That ceremony puts a browser
 * prompt on screen and can be REFUSED, so both states are surfaced here: while
 * it is pending the bar says the browser is asking for a passkey, and a failure
 * stays visible until the next attempt. Without this a cancelled prompt would
 * look exactly like a dead keystroke.
 *
 * WHY A PLAIN BUTTON AND NOT A KEYSTROKE (and why the step-up has no key
 * either): there is no TallyPrime-documented sign-out / log-out / change-user /
 * step-up combo. RESEARCH.md §21's master shortcut table has no such binding,
 * and §15's "Change User" is described as a Gateway FEATURE with no key.
 * Inventing one (e.g. Ctrl+Shift+L) is exactly the fake combo
 * `keystroke/buttons.test.ts` exists to catch. A <button> in the chrome is
 * natively keyboard-reachable (Tab, then Enter/Space), so the e2e's
 * page.keyboard-only rule is still satisfiable without a keymap entry — and the
 * step-up needs no key at all: it is triggered by the write the operator
 * already asked for.
 */
function TopBar({
  companyName,
  screenTitle,
}: {
  companyName: string;
  screenTitle: string;
}) {
  const auth = useAuth();
  const session = auth?.session ?? null;
  const stepUpState = auth?.stepUpState ?? "idle";
  return (
    <header
      className="flex items-center justify-between bg-tally-panel px-3 py-1 text-sm"
      aria-label={`TallyPrime — ${screenTitle}`}
    >
      <span className="font-bold text-tally-title">TallyPrime</span>
      <span className="flex items-center gap-3">
        <span className="text-tally-title">{companyName}</span>
        {session && stepUpState === "pending" ? (
          <span
            role="status"
            data-testid="stepup-pending"
            className="bg-tally-field px-2 text-tally-fieldText"
          >
            Confirm the passkey prompt — this change needs a security key.
          </span>
        ) : null}
        {session && stepUpState === "failed" ? (
          <span
            role="alert"
            data-testid="stepup-failed"
            className="bg-tally-bar px-2 font-bold text-tally-barKey"
          >
            PASSKEY STEP-UP FAILED — this change was not saved. Try again to
            re-run the passkey prompt.
          </span>
        ) : null}
        {session ? (
          <span
            className="flex items-center gap-2"
            data-testid="signed-in-as"
          >
            {/* display_name is "" for every identity cored provisions on first
                login (there is no route through which a user can set one), so
                the subject is the fallback — never a blank top bar. */}
            <span className="text-tally-field">
              {session.user.display_name || session.user.subject}
            </span>
            <ChangePasswordPanel />
            <button
              type="button"
              onClick={() => auth?.signOut()}
              className="bg-tally-bar px-2 text-tally-barText"
            >
              Sign Out
            </button>
          </span>
        ) : null}
      </span>
    </header>
  );
}

/**
 * The status line: the shell's answer to "what did that key just do?". Every
 * bound key writes here — including keys whose feature is not built and keys
 * that do not apply on the active screen — so no keystroke is ever silently
 * swallowed. It is a live region so assistive tech announces it too.
 */
function StatusLine({ status }: { status: string | null }) {
  return (
    <div
      aria-live="polite"
      data-testid="status-line"
      className="min-h-[1.25rem] bg-tally-bg px-3 py-0.5 text-xs text-tally-title"
    >
      {status}
    </div>
  );
}

function BottomHints({ hints }: { hints: readonly string[] }) {
  return (
    <footer
      className="flex flex-wrap gap-x-4 gap-y-0.5 bg-tally-panel px-3 py-1 text-xs text-tally-field"
      aria-label="Shortcut hints"
    >
      {hints.map((h) => (
        <span key={h}>{h}</span>
      ))}
    </footer>
  );
}
