import type { ReactNode } from "react";
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
