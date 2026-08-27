import { useCallback, useRef, useState } from "react";

import { ApiError } from "../api";
import type { RestoreResult } from "../api";
import { useClient, useCompanyID } from "../app/CompanyContext";
// ⛔ THE SHARED ANCHOR. This file used to carry its own copy, whose comment said
// it was "deliberately not a third variant" — while being one. There is now
// exactly one, and it is the one that revokes the object URL late.
import { downloadBlob } from "../export/download";

/**
 * THE DATA SCREEN (Alt+Y) — BACKUP AND RESTORE (T14).
 *
 * Tally's own surface is `Alt+Y (Data) > Backup / Restore` (RESEARCH.md:993),
 * and Alt+Y was completely unbound in this build, so the real key was free to
 * take rather than invented.
 *
 * THE THREE THINGS THIS SCREEN SAYS OUT LOUD, BEFORE THE OPERATOR ACTS
 *
 *  1. A RESTORE CREATES A NEW COMPANY AND NEVER REPLACES ONE. That is not a
 *     simplification of a feature, it is what the schema permits: there is no
 *     DELETE /companies route anywhere and every immutability trigger refuses
 *     DELETE for EVERY role, so "restore over this company" is impossible here
 *     — and making it possible would mean putting a tenant-wipe into the
 *     product. An operator who learns this from a 409 AFTER choosing a file has
 *     been told too late, so the screen says it beside the file picker.
 *
 *  2. THE ARCHIVE DOES NOT RE-GRANT ACCESS. company_membership is the only
 *     foreign key from a company's data to a user, so an archive names people
 *     who need not exist in the database it is restored into — and silently
 *     recreating access rights from a file somebody handed you is a
 *     privilege-escalation vector. The restore makes the RESTORING user the
 *     Owner and reports the rest BY NAME, which this screen renders as a list
 *     to act on rather than a count to skim past.
 *
 *  3. SPLIT COMPANY DATA IS ABSENT ON PURPOSE. Tally splits by financial year
 *     because it loads a whole company into RAM (RESEARCH.md:987) and that is
 *     the binding constraint there. It is not one here, so building split would
 *     be building a workaround for a limitation this product does not have. The
 *     Features screen carries the row; this screen says it where an operator
 *     coming from Tally's Data menu will look for the third item and not find
 *     it.
 *
 * ⛔ IT NEVER PARSES THE ARCHIVE. The file goes to the server as bytes and comes
 * back as bytes. Every judgement about what is in it — is it an archive, is this
 * build able to read its format, did the download arrive whole, does its company
 * already exist — is the server's, and the server's sentence is what is shown.
 * A client that decided any of those locally would be a second implementation of
 * the format, and the first thing it would get wrong is the trailer check that
 * distinguishes a complete archive from most of one.
 */
export function DataScreen(): JSX.Element {
  const client = useClient();
  const companyID = useCompanyID();
  const fileInput = useRef<HTMLInputElement | null>(null);

  const [busy, setBusy] = useState<"backup" | "restore" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [savedAs, setSavedAs] = useState<string | null>(null);
  const [restored, setRestored] = useState<RestoreResult | null>(null);

  const takeBackup = useCallback(async () => {
    setBusy("backup");
    setError(null);
    setSavedAs(null);
    setRestored(null);
    try {
      const { blob, filename } = await client.backupCompany(companyID);
      // The SERVER names the file (Content-Disposition), because it is the side
      // that knows the company's name and the date it took the archive. The
      // fallback is only for a server that sent no header at all.
      const name = filename ?? "company.tallybak.jsonl.gz";
      downloadBlob(blob, name);
      setSavedAs(name);
    } catch (e) {
      setError(messageOf(e));
    } finally {
      setBusy(null);
    }
  }, [client, companyID]);

  const restore = useCallback(
    async (file: File) => {
      setBusy("restore");
      setError(null);
      setSavedAs(null);
      setRestored(null);
      try {
        setRestored(await client.restoreCompany(file));
      } catch (e) {
        setError(messageOf(e));
      } finally {
        setBusy(null);
      }
    },
    [client],
  );

  return (
    <div className="data-screen" data-testid="data-screen">
      <h1>Data</h1>

      <section className="data-backup">
        <h2>Backup</h2>
        <p>
          Downloads this company&rsquo;s whole book as one compressed archive:
          every ledger, every voucher and every version of it, the inventory, the
          statutory workflow log and the unposted bills in the inbox.
        </p>
        <p className="data-warning">
          Treat the file as you would the books themselves. It contains every
          party&rsquo;s details and every salary, and anyone who has it has all
          of them.
        </p>
        <button
          type="button"
          onClick={() => void takeBackup()}
          disabled={busy !== null}
          data-testid="data-backup-button"
        >
          {busy === "backup" ? "Preparing the archive…" : "Back up this company"}
        </button>
        {savedAs !== null && (
          <p className="data-ok" data-testid="data-backup-saved">
            Downloaded {savedAs}
          </p>
        )}
      </section>

      <section className="data-restore">
        <h2>Restore</h2>
        {/*
          The consequence, stated BEFORE the file picker rather than after the
          server answers. See the header comment, point 1.
        */}
        <p className="data-warning" data-testid="data-restore-warning">
          Restoring CREATES A NEW COMPANY from the archive. It never replaces
          this one, and there is no way to overwrite a company in this product:
          the audit trail is immutable, so nothing can delete a book once it
          exists. If the archive&rsquo;s company is already here, the restore is
          refused rather than merged.
        </p>
        <p data-testid="data-restore-memberships-warning">
          Who could open the archived company is NOT restored. You become its
          Owner, and anyone else who had access has to be granted it again
          deliberately &mdash; the restore lists them by name so you know who.
        </p>
        <input
          ref={fileInput}
          type="file"
          accept=".gz,application/gzip"
          disabled={busy !== null}
          data-testid="data-restore-file"
          onChange={(ev) => {
            const file = ev.target.files?.[0];
            // Clear the input so choosing the SAME file twice fires again —
            // after a failed restore, re-picking the file it just refused is
            // the most natural thing an operator does.
            ev.target.value = "";
            if (file) void restore(file);
          }}
        />
        {busy === "restore" && <p>Restoring…</p>}
        {restored !== null && (
          <div className="data-ok" data-testid="data-restore-result">
            <p>
              Restored {restored.companyName} ({restored.rowCount} rows) as a new
              company.
            </p>
            {restored.membershipsNotRestored.length > 0 && (
              <>
                <p>
                  These people could open the archived company and have NOT been
                  granted access here:
                </p>
                <ul data-testid="data-restore-memberships">
                  {restored.membershipsNotRestored.map((name: string) => (
                    <li key={name}>{name}</li>
                  ))}
                </ul>
              </>
            )}
          </div>
        )}
      </section>

      <section className="data-split">
        <h2>Split Company Data</h2>
        {/*
          ⛔ NOT "coming soon", and not silence. See the header comment, point 3.
          A Tally operator opens this menu looking for three items; the third is
          missing on purpose and the reason is a claim this product is making.
        */}
        <p data-testid="data-split-absent">
          Not built, deliberately. Tally splits a company by financial year
          because it loads the whole company into memory and cannot hold the
          history. This product keeps every year queryable at once, so splitting
          would be a workaround for a limit it does not have. Closing a year into
          a read-only archive company is a different feature with a real reason,
          and it is not built either.
        </p>
      </section>

      {error !== null && (
        <p className="data-error" role="alert" data-testid="data-error">
          {error}
        </p>
      )}
    </div>
  );
}

/**
 * The SERVER'S sentence, never a substitute for it.
 *
 * Every refusal on these two routes names the fact that is wrong: a file that is
 * not an archive, a format this build cannot read, a download that arrived
 * short, an archive from a newer database, a company that is already here. A
 * client that replaced any of them with "restore failed" would take away the one
 * thing the operator needs to know what to do next.
 */
function messageOf(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return String(e);
}
