import type { ReportAvailability } from "./reportView";

/**
 * A REPORT THAT DID NOT ARRIVE, STATED — the screen half of the same fact
 * `exportUnavailableMessage` puts on the status line when Alt+E is pressed.
 *
 * ⛔ WHY THIS IS A COMPONENT AND NOT FIVE COPIES. Alt+E's whole promise is that
 * it writes what is on screen; the corollary is that when it REFUSES, the screen
 * must be refusing too, in the same words. The Stock Summary already did this
 * (it has rendered a 403 as a refusal since it shipped) and the five statement
 * screens did not — they rendered a refused report as an empty statement, or,
 * on the Trial Balance and the Outstandings, as a total of zero. One component
 * fed by one `ReportAvailability` value is what makes the key and the screen
 * unable to disagree.
 *
 * A 403 says WHO CAN LIFT IT. An operator told only "forbidden" files a bug
 * report; an operator told which rule refused them asks the Owner.
 */
export function ReportUnavailable({
  label,
  availability,
}: {
  /** What the operator calls this report — the screen's own heading. */
  label: string;
  availability: ReportAvailability;
}) {
  if (availability.kind !== "failed") return null;
  if (availability.status === 403) {
    return (
      <div data-testid="report-denied" className="text-sm">
        <p className="text-tally-title">
          Your security level may not open the {label}.
        </p>
        <p className="mt-1 text-xs">
          This is the security level working, not a fault: a report can be
          withheld from a level even where the coarse &ldquo;display
          report&rdquo; rule would otherwise reach it. An Owner can lift it in
          Gateway &gt; Users &amp; Roles by removing the matching
          &ldquo;disallow display&rdquo; rule. Nothing is exported either —
          Alt+E says the same thing this screen does.
        </p>
      </div>
    );
  }
  return (
    <p data-testid="report-error" className="text-sm text-red-400">
      {label} unavailable
      {availability.status ? ` (${availability.status})` : ""}.{" "}
      {availability.message}
    </p>
  );
}
