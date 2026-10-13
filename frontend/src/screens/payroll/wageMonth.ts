/**
 * THE WAGE MONTH — one validator, shared by every payroll screen.
 *
 * ⛔ ONE COPY AND NOT ONE PER SCREEN. Four screens ask for a wage month and each
 * refuses locally before making a request, and the repository's own lesson (see
 * `useExportAction`'s header) is that the fifth copy is where copies drift. A
 * screen whose validator is a shade looser than the server's sends a request that
 * comes back 400; one that is a shade tighter refuses a month the server would
 * have accepted, and the operator has no way to tell which of them is wrong.
 *
 * ⛔ AND IT IS STRICT FOR THE SERVER'S REASON, NOT FOR TIDINESS. cored parses the
 * month with `time.Parse("2006-01", …)` — a strict layout, deliberately not a
 * split on '-' — so "2026-13" and "2026-1" are both REFUSED rather than coerced.
 * Every social-security scheme keys off the wage month: a month silently moved to
 * a neighbouring one computes somebody's contributions under the wrong window,
 * and the payslip that results looks entirely ordinary.
 *
 * ⚠️ IT VALIDATES SHAPE AND NOTHING ELSE. Whether a month may be RUN is the
 * server's answer and depends on facts no client holds — the back-dated cutoff,
 * who was employed, what has already been posted. This never guesses at any of
 * them.
 */

/** True when `s` is a wage month the server will accept: YYYY-MM, months 01-12. */
export function isWageMonth(s: string): boolean {
  return /^\d{4}-(0[1-9]|1[0-2])$/.test(s.trim());
}

/**
 * The sentence every payroll screen shows beside a wage-month field.
 *
 * It says WHY the format is strict rather than only what it is. "Must be YYYY-MM"
 * reads as a fussy input mask; the reason it is not one is that the alternative
 * is somebody's contributions computed under the wrong statutory window.
 */
export const WAGE_MONTH_HINT =
  "A wage month is YYYY-MM — 2026-07, never 2026-7 and never 2026-13. " +
  "The server refuses both rather than coercing them: every social-security " +
  "scheme keys off this month, and one quietly moved to its neighbour computes " +
  "somebody's contributions under the wrong window.";
