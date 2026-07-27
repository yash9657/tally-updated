import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { AuthInvalidError, AuthLockedError } from "../api/auth";
import { useAuth } from "./AuthContext";

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

function failureText(f: Failure): string {
  switch (f.kind) {
    case "invalid":
      return "Invalid credentials — check the user name and password.";
    case "locked":
      return f.until
        ? `ACCOUNT LOCKED — too many failed attempts. Try again after ${f.until}.`
        : "ACCOUNT LOCKED — too many failed attempts.";
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
      className="flex h-full w-full items-center justify-center bg-tally-bg p-4 font-mono text-tally-field"
    >
      <form className="w-full max-w-sm bg-tally-panel p-4" onSubmit={onSubmit}>
        <h1 className="text-lg font-bold text-tally-title">
          TallyPrime — Sign In
        </h1>
        <p className="mt-1 text-xs text-tally-field">
          Enter your user name and password. Enter signs in.
        </p>

        <label className="mt-4 block text-sm text-tally-field">
          <span className="inline-block w-24">User name</span>
          <input
            // eslint-disable-next-line jsx-a11y/no-autofocus -- see the header:
            // the e2e types with page.keyboard and never focuses a locator.
            autoFocus
            aria-label="User name"
            name="subject"
            autoComplete="username"
            className="ml-2 w-40 bg-tally-field px-1 text-tally-fieldText outline-none"
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
          />
        </label>

        <label className="mt-2 block text-sm text-tally-field">
          <span className="inline-block w-24">Password</span>
          <input
            type="password"
            aria-label="Password"
            name="password"
            autoComplete="current-password"
            className="ml-2 w-40 bg-tally-field px-1 text-tally-fieldText outline-none"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>

        {failure ? (
          <p
            role="alert"
            data-testid="login-error"
            data-failure={failure.kind}
            className="mt-3 bg-tally-field px-2 py-1 text-sm text-tally-barKey"
          >
            {failureText(failure)}
          </p>
        ) : null}

        <button
          type="submit"
          disabled={busy}
          className="mt-4 bg-tally-bar px-3 py-1 text-sm font-bold text-tally-barText disabled:opacity-60"
        >
          {busy ? "Signing in…" : "Sign In"}
        </button>
      </form>
    </section>
  );
}
