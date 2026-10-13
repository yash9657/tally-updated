import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { AuthInvalidError, AuthLockedError } from "../api/auth";
import { useAuth } from "./AuthContext";
import { ArrowIcon, Brand, Button, Keycap } from "../design-system/components";
import "./auth.css";

/**
 * The sign-in screen. Rendered INSTEAD of the shell when there is no session,
 * so it deliberately depends on neither the navigation machine nor
 * CompanyContext — at this point there is no company and no client.
 *
 * KEYBOARD-FIRST, and that is a contract, not a nicety: the live e2e drives it
 * with `page.keyboard` only (HANDOFF §6 forbids `locator.press`, which focuses
 * first and manufactures the condition the app must not depend on). So focus
 * MUST start in the user-name field (autoFocus), Tab MUST reach the password
 * field, and Enter in either field MUST submit — which it does natively,
 * because the fields live in a <form> with a submit button.
 *
 * Every failure is rendered DISTINCTLY and the form is never replaced by a
 * blank screen: an operator who mistypes a password must see why, and an
 * operator who is locked out must see until when.
 */

type Failure =
  | { kind: "invalid" }
  | { kind: "locked"; until: string }
  | { kind: "error"; message: string };

function classify(err: unknown): Failure {
  if (err instanceof AuthLockedError) return { kind: "locked", until: err.until };
  if (err instanceof AuthInvalidError) return { kind: "invalid" };
  return {
    kind: "error",
    message: err instanceof Error ? err.message : String(err),
  };
}

/**
 * When the lock lifts, IN THE OPERATOR'S OWN CLOCK (D58).
 *
 * The server answers 423 with a raw UTC RFC3339 instant —
 * `"until":"2026-08-07T23:09:07Z"` — and this screen used to interpolate it
 * verbatim: "Try again after 2026-08-07T23:09:07Z". For this product's audience,
 * accountants in India on IST (UTC+5:30), that string is wrong twice over. It is
 * not a time they recognise (it is five and a half hours behind their wall
 * clock, and after 18:30 local it names YESTERDAY'S DATE, which reads as a lock
 * that expired long ago), and it does not answer the only question being asked,
 * which is "how long am I locked out for?".
 *
 * So both are given: the local instant with its zone named, and the wait.
 *
 * ⚠️ THE ZONE IS THE BROWSER'S, NOT A HARDCODED "IST". A CA working from London
 * must be told London time, and hardcoding +5:30 would put them out by five and
 * a half hours with a confident-looking label. Intl resolves the machine's zone
 * and names it, so the sentence is true wherever it is read.
 */
function lockedUntilText(untilISO: string): string {
  const ms = Date.parse(untilISO);
  if (!Number.isFinite(ms)) {
    // An `until` we cannot parse is still worth showing verbatim: an
    // unrecognised instant is more use to whoever is being telephoned about
    // this than silence, and it is the server's own words.
    return `Try again after ${untilISO}.`;
  }
  const local = new Intl.DateTimeFormat(undefined, {
    day: "numeric",
    month: "short",
    year: "numeric",
    hour: "numeric",
    minute: "2-digit",
    timeZoneName: "short",
  }).format(new Date(ms));

  const remainingMs = ms - Date.now();
  if (remainingMs <= 0) {
    // The lock has already lapsed while this message was on screen. Saying "try
    // again in 0 minutes" would be absurd; saying nothing would leave an
    // operator waiting for a lock that is over.
    return `The lock has now lifted (it ran to ${local}) — try again.`;
  }
  const minutes = Math.ceil(remainingMs / 60000);
  const wait =
    minutes >= 120
      ? `about ${Math.round(minutes / 60)} hours`
      : minutes >= 60
        ? `about an hour`
        : minutes === 1
          ? `about a minute`
          : `about ${minutes} minutes`;
  return `Try again after ${local} — that is ${wait} from now.`;
}

function failureText(f: Failure): string {
  switch (f.kind) {
    case "invalid":
      return "Invalid credentials — check the user name and password.";
    case "locked":
      return f.until
        ? `ACCOUNT LOCKED — too many failed attempts. ${lockedUntilText(f.until)} If you need in sooner, an Owner of your company can unlock you from Users & Roles.`
        : // No `until` at all: still name the remedy, because "locked" with no
          // way out is the state D55 was about.
          "ACCOUNT LOCKED — too many failed attempts. An Owner of your company can unlock you from Users & Roles.";
    case "error":
      return `Could not sign in — ${f.message}`;
  }
}

export function LoginScreen() {
  const auth = useAuth();
  const [subject, setSubject] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<Failure | null>(null);

  // A successful sign-in unmounts this screen (App swaps in the shell), so the
  // post-await state writes must be guarded.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const onSubmit = useCallback(
    (ev: FormEvent<HTMLFormElement>) => {
      ev.preventDefault();
      if (!auth || busy) return;
      setFailure(null);
      setBusy(true);
      auth
        .signIn(subject.trim(), password)
        .catch((err: unknown) => {
          if (mounted.current) setFailure(classify(err));
        })
        .finally(() => {
          if (mounted.current) setBusy(false);
        });
    },
    [auth, busy, password, subject],
  );

  return (
    <section
      data-testid="login-screen"
      aria-label="Sign In"
      className="auth-page"
    >
      <header className="auth-header"><Brand /><a href="/">Back to home</a></header>
      <main className="auth-main">
      <div className="auth-intro"><h1>Back to the books.</h1><p>Sign in to your Tallyweb workspace.</p></div>
      <form className="auth-form" onSubmit={onSubmit} aria-label="Sign in to Tallyweb">

        <label className="tw-field">
          <span>User name</span>
          <input
            // eslint-disable-next-line jsx-a11y/no-autofocus -- see the header:
            // the e2e types with page.keyboard and never focuses a locator.
            autoFocus
            aria-label="User name"
            name="subject"
            autoComplete="username"
            className="tw-input"
            aria-invalid={failure?.kind === "invalid" || undefined}
            aria-describedby={failure ? "login-error" : undefined}
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
          />
        </label>

        <label className="tw-field">
          <span>Password</span>
          <input
            type="password"
            aria-label="Password"
            name="password"
            autoComplete="current-password"
            className="tw-input"
            aria-invalid={failure?.kind === "invalid" || undefined}
            aria-describedby={failure ? "login-error" : undefined}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>

        {failure ? (
          <p
            role="alert"
            id="login-error"
            data-testid="login-error"
            data-failure={failure.kind}
            className="tw-alert"
          >
            {failureText(failure)}
          </p>
        ) : null}

        <Button
          type="submit"
          disabled={busy}
          className="auth-submit"
          aria-busy={busy}
        >
          {busy ? "Signing in…" : "Sign In"}
          <ArrowIcon />
        </Button>
        <p className="auth-key-hint">Press <Keycap>Enter</Keycap> to sign in.</p>
      </form>
      <p className="auth-help">Need access? Ask your company administrator<br />to arrange your Tallyweb account.</p>
      </main>
      <footer className="auth-footer">Your books. Your shortcuts. Your workspace.</footer>
    </section>
  );
}
