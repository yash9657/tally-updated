import { useCallback, useState } from "react";

import { ApiError } from "../api";
import type { ImportTallyXMLResponse } from "../api";
import { useClient, useCompanyContext, useCompanyID } from "../app/CompanyContext";

/**
 * MIGRATE FROM TALLY (Alt+O) — import a TallyPrime XML export (T32).
 *
 * Tally's own surface is `Gateway of Tally > Import`, and Alt+O is its
 * documented global for it ("O: Import", RESEARCH.md:361, :415, :1221). The
 * route this screen drives — POST .../import/tally-xml — has existed since the
 * migration lane and NOTHING IN THE PRODUCT COULD REACH IT: migrating meant
 * hand-rolling a `curl` with the right content type. That is HANDOFF.md §1.15's
 * rule, and this screen exists to satisfy it: a route table is not a surface.
 *
 * ⛔⛔ THE FILE IS SENT AS BYTES AND NEVER AS A STRING.
 *
 * Every genuine TallyPrime export is UTF-16LE with a BOM. `File.text()` decodes
 * as UTF-8 unconditionally, so the obvious one-liner destroys the only file
 * shape a real customer will ever send, while every ASCII fixture in the corpus
 * keeps passing. The `File` goes to `postRaw` untouched and the SERVER'S sniffer
 * decodes it — see CoredClient.importTallyXML's comment for the whole argument.
 *
 * ⛔ IT NEVER PARSES OR PRE-VALIDATES THE XML. The encoding sniffing, the
 * IMPORTDATA/REQUESTDESC/REQUESTDATA nesting, the dated GST registrations and
 * the ACTION=Alter|Delete routing by Tally GUID are all server-side and were all
 * hard-won against a real export. A client-side "fail fast" check would be a
 * second implementation of the format, and the first thing it would disagree
 * about is the encoding.
 *
 * ⛔ AND IT DOES NOT REPORT `vouchers_posted` ALONE. That is the failure this
 * screen exists to prevent: quantity is invisible in a trial balance, so an
 * import that read every stock line and dropped it answers 200 with books that
 * balance perfectly and a Stock Summary with nothing in it. `stock_lines` and
 * `opening_positions` sit BESIDE the voucher count, and `opening_positions: 0`
 * against a non-zero `stock_lines` is rendered as a WARNING rather than a clean
 * result.
 *
 * ⚠️ THERE IS NO PROGRESS BAR, ON PURPOSE. The route is a single POST that
 * returns when the whole import has committed; there is no progress channel to
 * read, so a percentage would be invented. A spinner is honest.
 */
export function TallyImportScreen(): JSX.Element {
  const client = useClient();
  const companyID = useCompanyID();
  const { company } = useCompanyContext();

  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<ImportTallyXMLResponse | null>(null);

  const runImport = useCallback(async () => {
    if (!file) return;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      // ⛔ THE `File` ITSELF, NEVER `await file.text()`. See the header comment,
      // and note that `FileReader.readAsText` is NOT the same function — it
      // BOM-sniffs and would have looked fine. CoredClient.importTallyXML
      // carries the measurement.
      setResult(await client.importTallyXML(companyID, file));
    } catch (e) {
      setError(messageOf(e));
    } finally {
      setBusy(false);
    }
  }, [client, companyID, file]);

  return (
    <div className="tally-import-screen" data-testid="tally-import-screen">
      <h1>Import from Tally</h1>

      <section className="tally-import-pick">
        {/*
          THE COMPANY IS NAMED BEFORE THE FILE IS CHOSEN. An import posts into
          the company that is currently open and there is no way to undo one —
          every voucher it writes goes through the immutable ledger path, so a
          file imported into the wrong company leaves that company's books
          permanently carrying somebody else's trade. Naming it here costs one
          line; discovering it from a Trial Balance costs the book.
        */}
        <p data-testid="tally-import-target">
          The export will be imported into <strong>{company?.name ?? companyID}</strong>
          , the company that is open now. There is no way to undo an import: the
          audit trail is immutable, so nothing can remove the vouchers it writes.
        </p>
        {/*
          D100: THE RECIPE, NOT "EXPORT THE WHOLE COMPANY". This screen used to
          say "export the whole company (Masters and Transactions)", and an
          operator who does exactly that in TallyPrime gets an ACCOUNTING
          MASTERS export — ledgers and groups and nothing else. The stock items
          are absent, so the first sale of goods bought before the window drives
          a position negative and the import aborts offering "import a
          Masters.xml carrying the items FIRST", which is the door the operator
          could not find. Type of Master is that door. Keep this in step with
          describeNegativeStock() in backend/internal/migration/inventory.go —
          an operator meets the same problem there, and one recipe drifting from
          the other is how this defect gets re-registered.
        */}
        <p>
          Choose the XML file TallyPrime wrote. In TallyPrime press{" "}
          <strong>Alt+E</strong> to export, and before exporting the masters open{" "}
          <strong>Configure</strong> and set:
        </p>
        <ul data-testid="tally-import-recipe">
          <li>
            <strong>Type of Master = All Masters</strong> — the default is{" "}
            <em>Accounting Masters</em>, which carries your ledgers and groups
            but <strong>no stock items, units or godowns</strong>. An import
            without them aborts on the first sale of goods you bought earlier.
          </li>
          <li>
            <strong>Include dependent masters = Yes</strong> — so a master your
            vouchers refer to comes with them.
          </li>
          <li>
            <strong>Export closing balance as opening balance = Yes</strong> —
            an export of a closed year carries CLOSING balances, and this
            company needs them as OPENINGS.
          </li>
        </ul>
        <p>
          Export the transactions too. Together these read the chart of
          accounts, the vouchers, and the stock.
        </p>
        <input
          type="file"
          accept=".xml,text/xml,application/xml"
          disabled={busy}
          data-testid="tally-import-file"
          onChange={(ev) => {
            const picked = ev.target.files?.[0] ?? null;
            // Clear the input so choosing the SAME file twice fires again. After
            // a refusal, re-picking the file the server just refused is the most
            // natural thing an operator does — the same reason DataScreen's
            // restore input resets.
            ev.target.value = "";
            setFile(picked);
            setError(null);
            setResult(null);
          }}
        />
        {file !== null && (
          <p data-testid="tally-import-chosen">
            {file.name} ({file.size.toLocaleString()} bytes)
          </p>
        )}
        <button
          type="button"
          onClick={() => void runImport()}
          disabled={busy || file === null}
          data-testid="tally-import-button"
        >
          {busy ? "Importing…" : "Import this file"}
        </button>
        {/*
          A SPINNER AND NOT A PERCENTAGE. See the header comment: there is no
          progress channel on this route, so any number would be invented.
        */}
        {busy && (
          <p data-testid="tally-import-busy">
            Importing. A whole-company export takes a while and the page will not
            show progress — the server answers once, when the import has
            committed.
          </p>
        )}
      </section>

      {result !== null && <ImportReport result={result} />}

      {/*
        ⛔ A REFUSAL MUST SAY WHAT IT LEFT BEHIND, AND THE ANSWER IS NOW "NO
        VOUCHERS" (D98). Before the voucher pass became one transaction, an
        import refused on voucher 3 had already committed vouchers 1 and 2, said
        nothing about them, and the operator's natural recovery — fix the file,
        import it again — posted them a SECOND time. This paragraph is where that
        recovery begins, so it is where the guarantee belongs.

        ⚠️ THE SECOND SENTENCE IS NOT DECORATION. Without it the honest operator
        does the cautious thing, which used to be the wrong thing: hand-edit the
        file down to "the ones that did not make it" and import that.
      */}
      {error !== null && (
        <div className="tally-import-error" role="alert" data-testid="tally-import-error">
          <p>{error}</p>
          <p data-testid="tally-import-refusal-atomic">
            <strong>No vouchers were imported.</strong> A refused import leaves
            the books exactly as they were, so fix the file and import the whole
            thing again — do not remove the vouchers before the failure, and do
            not import a trimmed file. Masters the file created (ledgers, stock
            items, units) do remain, and re-importing resolves them rather than
            duplicating them.
          </p>
        </div>
      )}
    </div>
  );
}

/**
 * THE REPORT. Its shape is the argument: the accounting and THE GOODS side by
 * side, the warnings in full, and a closing sentence saying what a clean-looking
 * report still does not prove.
 */
function ImportReport({ result }: { result: ImportTallyXMLResponse }): JSX.Element {
  // ⚠️ ZERO OPENING POSITIONS ALONGSIDE REAL STOCK MOVEMENT IS NOT A CLEAN
  // RESULT. The export carried no STOCKITEM opening balances (a Day Book export
  // carries none), so every position starts at nil and the first issue of goods
  // bought before the export window either was refused or consumed nothing.
  const openingGap = result.stock_lines > 0 && result.opening_positions === 0;

  return (
    <section className="tally-import-report" data-testid="tally-import-report">
      <h2>Imported from {result.source_company}</h2>

      <h3>The accounting</h3>
      <ul>
        <li data-testid="tally-import-posted">
          Vouchers posted: {result.vouchers_posted}
        </li>
        <li>Vouchers altered: {result.vouchers_altered}</li>
        <li>Vouchers deleted: {result.vouchers_deleted}</li>
        <li>
          Vouchers skipped (already imported): {result.vouchers_skipped}
        </li>
      </ul>

      {/*
        ⛔ MONEY THE SOURCE BOOKS EXCLUDE GETS ITS OWN BLOCK, AND ONLY WHEN THERE
        IS SOME (D101). Tally keeps a CANCELLED or OPTIONAL voucher out of the
        books, so this importer does not post them — but an import that quietly
        drops entries is the same class of silence as one that quietly adds them.
        "3 posted" against a file of 5 has to explain the other two.

        ⚠️ IT IS DELIBERATELY NOT IN THE LIST ABOVE, beside "skipped (already
        imported)". Those two numbers mean opposite things and an operator
        scanning a list of four counts will not stop to tell them apart.
      */}
      {result.vouchers_voided > 0 && (
        <>
          <h3>Excluded as void in Tally: {result.vouchers_voided}</h3>
          <p data-testid="tally-import-voided-note">
            These vouchers are marked cancelled, optional or deleted in the
            source company. Tally keeps them out of its own books, so they were
            NOT posted here — they are not missing, and they are not duplicates.
          </p>
          <ul data-testid="tally-import-voided">
            {result.voided_vouchers.map((v) => (
              <li key={v}>{v}</li>
            ))}
          </ul>
        </>
      )}
      {result.ledgers_tombstoned.length > 0 && (
        <>
          <h3>
            Ledgers deleted in Tally, not created:{" "}
            {result.ledgers_tombstoned.length}
          </h3>
          <ul data-testid="tally-import-tombstoned">
            {result.ledgers_tombstoned.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        </>
      )}

      {/*
        ⛔ THE GOODS GET THEIR OWN HEADING, LEVEL WITH THE ACCOUNTING. Folding
        these two figures into the list above is how they get skimmed past, and
        they are the only numbers on the wire that can distinguish "imported the
        accounting" from "imported the trade".
      */}
      <h3>The goods</h3>
      <ul>
        <li data-testid="tally-import-stock-lines">
          Stock movements written: {result.stock_lines}
        </li>
        <li data-testid="tally-import-opening-positions">
          Opening positions written: {result.opening_positions}
        </li>
      </ul>
      {result.stock_lines === 0 && (
        <p className="tally-import-warning" data-testid="tally-import-no-stock">
          NO STOCK MOVEMENTS were written. On a file full of invoices that is the
          tell that the trade did not arrive: the rupee value of a sale is in the
          ledger entries, so the trial balance will look right either way. Check
          the Stock Summary before you trust this import.
        </p>
      )}
      {openingGap && (
        <p
          className="tally-import-warning"
          data-testid="tally-import-opening-gap"
        >
          NO OPENING POSITIONS were written, and stock moved. The export carried
          no opening stock, so every item starts at nil and any sale of goods
          bought before the export window has nothing behind it. This is a
          WARNING, not a clean result.
        </p>
      )}

      {/*
        ⛔ THE WARNINGS IN FULL, NEVER A COUNT. The importer carries on past what
        it cannot represent exactly — an opening bill-wise breakup that does not
        sum to its own balance, a ledger whose Tally group was not in the file, a
        compound unit flattened to a symbol. An operator told only "121 vouchers
        posted" will believe the migration was lossless when parts of it were not.
      */}
      <h3>Warnings ({result.warnings.length})</h3>
      {result.warnings.length === 0 ? (
        <p data-testid="tally-import-no-warnings">
          None. That is necessary and not sufficient — see below.
        </p>
      ) : (
        <ul data-testid="tally-import-warnings">
          {result.warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      )}

      {/*
        WHAT WAS CREATED, not merely referenced. An operator reconciling a
        migration needs to know which masters the import invented — a ledger
        ensured under the wrong group is a real defect that no total reveals.
      */}
      <h3>Masters created</h3>
      <EnsuredList
        label="Account groups"
        testid="tally-import-groups"
        names={result.groups_ensured}
      />
      <EnsuredList
        label="Ledgers touched"
        testid="tally-import-ledgers"
        names={result.ledgers_ensured}
      />
      <EnsuredList
        label="Stock items"
        testid="tally-import-stock-items"
        names={result.stock_items_ensured}
      />
      <EnsuredList
        label="Units"
        testid="tally-import-units"
        names={result.units_ensured}
      />
      <EnsuredList
        label="Locations"
        testid="tally-import-godowns"
        names={result.godowns_ensured}
      />
      <EnsuredList
        label="Batches"
        testid="tally-import-batches"
        names={result.batches_ensured}
      />

      {/*
        ⛔ THE SENTENCE THE FEATURES ROW ALREADY CARRIES, ON THE SCREEN WHERE IT
        IS ACTED ON. It is the whole reason this report is shaped the way it is.
      */}
      <p className="tally-import-caveat" data-testid="tally-import-caveat">
        Never treat an empty warning list as the only thing to check, and never
        treat a balanced trial balance as proof the migration worked: check the
        Stock Summary.
      </p>
    </section>
  );
}

/** One ensured-masters row. Renders the NAMES, and says so when there are none. */
function EnsuredList({
  label,
  testid,
  names,
}: {
  label: string;
  testid: string;
  names: string[];
}): JSX.Element {
  return (
    <div data-testid={testid}>
      <h4>
        {label} ({names.length})
      </h4>
      {names.length === 0 ? <p>None.</p> : <p>{names.join(", ")}</p>}
    </div>
  );
}

/**
 * The SERVER'S sentence, never a substitute for it.
 *
 * Every refusal on this route names the fact that is wrong, and each of them is
 * a REAL ANSWER an operator has to act on rather than an error state:
 *
 *  - the WHOLE-IMPORT ABORT: an export with no STOCKITEM masters carries no
 *    opening stock, so the first sale of goods bought before the export window
 *    has nothing to issue. The import is refused entirely, naming both ways
 *    forward — the importer will not invent an opening position nobody stated
 *    and will not switch this company's negative-stock setting on its behalf;
 *  - 403 — this security level may not import. No seeded level names the
 *    `import` resource and Data Entry's default access is 'none', so the route
 *    is default-DENIED for every seeded non-Owner level. That is a DECISION,
 *    not a bug;
 *  - the BACK-DATED CUTOFF — the file contains a voucher dated before a closed
 *    period. An import is a write, so it inherits the cutoff;
 *  - 413 — the file is over the route's 128 MiB body cap.
 *
 * A client that replaced any of them with "import failed" would take away the
 * one thing the operator needs to know what to do next. DataScreen's messageOf
 * is the same function for the same reason.
 */
function messageOf(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return String(e);
}
