import { useCallback, useEffect, useMemo, useState } from "react";

import type { ScreenComponentProps } from "../../app/screenRegistry";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import { ApiError } from "../../api";
import type {
  GSTR9Divergence,
  GSTR9Response,
  GSTR9TableStatus,
  GSTR9TaxableDivergence,
  GSTR9TaxLedgers,
  GSTTax,
  LedgerListItem,
  Money,
} from "../../api";
import { formatMoney } from "./money";
import { SelectionList } from "../SelectionList";
import { usePrintAction } from "../usePrint";

/**
 * THE GSTR-9 ANNUAL RECONCILIATION (T12) — the only screen in this product whose
 * subject is a DISAGREEMENT rather than a figure.
 *
 * Every other statement here answers "what happened". This one asks whether the
 * year's FILED RETURNS and the BOOKS tell the same story, and its content is the
 * places where they do not. Three independently-computed views of the same twelve
 * months are put side by side — GSTR-1's section classifier, GSTR-3B's own SQL,
 * and the postings on the tax ledgers — plus a fourth over the per-line HSN
 * detail.
 *
 * ⛔⛔ THE SIX THINGS THIS SCREEN REFUSES TO DO. Every one of them is a way an
 * annual return lies while FOOTING PERFECTLY.
 *
 *  1. ⭐ IT NEVER PRINTS AN ANNUAL NET DIFFERENCE ON ITS OWN. Twelve months added
 *     up CANCEL. A company that over-declared 40,000 of IGST in May and
 *     under-declared 40,000 in September has an annual net of ZERO — every figure
 *     foots, and the return is silently wrong about the one thing it exists to
 *     say. So `gross` is rendered beside `net` on every comparison, ALWAYS, and
 *     when `offsetting` is true the screen says the months cancel and NAMES them.
 *     This is the same discipline BudgetVarianceScreen applies with
 *     `totals_are_addable`.
 *
 *  2. IT NEVER READS AGREEMENT OFF A NET. `agrees` is a per-month property the
 *     server computes; this screen renders it and never recomputes it. A zero net
 *     is exactly the case where a recomputed verdict would be wrong.
 *
 *  3. IT NEVER RENDERS A NOT_DERIVED TABLE AS ZERO. Eleven of the fourteen
 *     statutory tables cannot be produced from these books, and a zero in table 7
 *     ("ITC reversed") asserts the taxpayer reversed NOTHING when the truth is
 *     that nothing was checked. NOT_DERIVED tables show their REASON where their
 *     figures would be, and carry no rows.
 *
 *  4. IT NEVER DRAWS A SUPPRESSED TOTAL. When `total_suppressed` is set there is
 *     no total row, and `total_suppressed_reason` is printed in its place. Table
 *     4's 4N is the case: it totals rows five of which are not derived, so a
 *     figure there would be an UNDERSTATEMENT THAT FOOTS.
 *
 *  5. IT NEVER SHOWS A SUBSET OF THE DISCLOSURES. All of them are rendered, every
 *     time, above the figures. Ten are unconditional — this build has never seen
 *     the GSTN annual schema, RESEARCH.md contains no GSTR-9 material at all, and
 *     the portal is geo-fenced from here. A screen that showed the tidy tables
 *     and hid the caveats would be the most dangerous artefact in this product.
 *
 *  6. IT NEVER TOUCHES A NUMBER. Every amount is a fixed-point STRING rendered
 *     through formatMoney. No parseFloat, no Number(), no arithmetic on money —
 *     not even to decide whether something is zero, which is done on the server's
 *     own boolean flags.
 *
 * AND ONE MORE, ABOUT WHAT IT ASKS FOR:
 *
 *  7. IT NEVER GUESSES THE SIX TAX LEDGERS AND NEVER ASKS FOR A UUID. The server
 *     REQUIRES all six — without them the books column reads zero for all twelve
 *     months and every period looks wrong by exactly the tax charged, which is
 *     twelve findings that are all artefacts of a missing parameter. So this
 *     screen resolves them from the served ledger list, PRE-SELECTS by name as a
 *     convenience, and shows every choice for confirmation before it will load.
 *     A pre-selection is a suggestion on screen, never a silent decision.
 *
 * IT TREATS A 403 AS A DECISION. Migration 0087 refuses `report:gstr9` to the
 * seeded Data Entry level — a whole financial year of turnover and input credit,
 * plus the months in which this company may have mis-filed. A 403 is the NORMAL
 * answer for that level and is rendered as the decision it is, not as an error
 * and not as an empty table.
 */

/**
 * What each disclosure token MEANS, in words.
 *
 * ⛔ THE MAP IS EXHAUSTIVE OVER THE SERVER'S CLOSED VOCABULARY, and an unknown
 * token is rendered VERBATIM rather than dropped. A disclosure the screen does
 * not recognise is still a limitation of the return, and silently swallowing it
 * is the exact failure the closed vocabulary exists to prevent.
 */
const DISCLOSURES: Record<string, string> = {
  "not-filing-ready":
    "THIS IS NOT A FILING-READY RETURN. It is a reconciliation workpaper for finding the month where the returns and the books parted company.",
  "annual-table-structure-unverified":
    "The table numbering and titles have NOT been checked against the CBIC notification or the GSTN annual utility. The portal is not reachable from this build.",
  "portal-schema-unverified":
    "The JSON key names are this build's own and are not the portal's. Nothing here can be uploaded.",
  "itc-reversals-not-computed":
    "The twelve GSTR-3Bs behind this year were generated with NO inward statement, so nothing was matched against what suppliers filed. Table 7 is not derived rather than zero.",
  "exempt-and-zero-rated-not-marked":
    "These books carry no export, SEZ, deemed-export, nil-rated, exempt or non-GST marker on a supply, so table 5 and rows 4C-4E cannot be produced.",
  "advances-not-modelled":
    "Advance receipts and their adjustment against later invoices are not modelled, so table 4F cannot be produced.",
  "amendments-not-modelled":
    "Nothing records that a document was AMENDED IN A LATER RETURN. An alteration here appends a version dated in the ORIGINAL period.",
  "no-cash-or-credit-ledger":
    "There is no electronic cash or credit ledger and no GST challan register, so what was actually PAID — as opposed to payable — is not known.",
  "no-annual-2a":
    "The department's own annual GSTR-2A is not held, so table 8 cannot be produced. 8D is 8A minus 8B, and reporting it as zero would assert agreement with a statement never read.",
  "books-carry-no-taxable-value":
    "The books post TAX to ledgers; taxable value is not a posted quantity. The taxable-value comparison is therefore GSTR-1 against GSTR-3B only, with no third opinion.",
  "monthly-differences-offset":
    "⛔ AT LEAST ONE COMPARISON HAS MONTHS THAT CANCEL. Its annual net understates the disagreement — read the gross figure and the named months, not the net.",
  "returns-and-books-disagree":
    "⛔ At least one comparison is non-zero in at least one month: the returns and the books, or the two returns, do not tell the same story.",
};

/** The comparisons, in the order an accountant checks them. */
const LEGS: {
  key: keyof Pick<
    GSTR9Response,
    | "outward_r1_vs_r3b"
    | "outward_r3b_vs_books"
    | "outward_r1_vs_books"
    | "itc_vs_books"
    | "hsn_vs_r1"
  >;
  title: string;
  asks: string;
}[] = [
  {
    key: "outward_r1_vs_r3b",
    title: "GSTR-1 vs GSTR-3B (outward tax)",
    asks: "Do the two returns you filed agree with each other? A break here is a filing inconsistency the department can see without opening your books.",
  },
  {
    key: "outward_r3b_vs_books",
    title: "GSTR-3B vs the books (outward tax)",
    asks: "Does the return agree with what was actually posted to the output-tax ledgers? A break here means the return and the trial balance disagree about what was charged.",
  },
  {
    key: "outward_r1_vs_books",
    title: "GSTR-1 vs the books (outward tax)",
    asks: "The third leg. It is implied by the other two, and it is shown because this is the statement an accountant actually checks.",
  },
  {
    key: "itc_vs_books",
    title: "GSTR-3B table 4(A)+4(D) vs the books (input tax)",
    asks: "Does every rupee of posted input tax appear in the return? The two sides are separate queries over separate tables — a journal crediting an input-tax ledger directly breaks this.",
  },
  {
    key: "hsn_vs_r1",
    title: "Per-line HSN detail vs the invoice aggregates",
    asks: "GSTR-1 table 12 is built from per-line detail; every other figure here comes from the invoice-level GST columns. This is the check that the lines still sum to the invoice.",
  },
];

/**
 * The six ledgers, with the name fragment used to PRE-SELECT one.
 *
 * ⚠️ THE FRAGMENTS ARE A CONVENIENCE AND NEVER A DECISION. A company may call its
 * ledgers anything; matching "Output CGST" is a good guess and a guess is not
 * good enough for a figure a filer will act on. Every pre-selection is shown in a
 * control the operator can change, and nothing loads until all six are set.
 */
const LEDGER_SLOTS: {
  key: keyof GSTR9TaxLedgers;
  label: string;
  hint: string;
  match: (name: string) => boolean;
}[] = [
  {
    key: "cgst",
    label: "Output CGST",
    hint: "what was charged on sales",
    match: (n) => /out/.test(n) && /cgst/.test(n),
  },
  {
    key: "sgst",
    label: "Output SGST",
    hint: "what was charged on sales",
    match: (n) => /out/.test(n) && /sgst/.test(n),
  },
  {
    key: "igst",
    label: "Output IGST",
    hint: "what was charged on sales",
    match: (n) => /out/.test(n) && /igst/.test(n),
  },
  {
    key: "inputCGST",
    label: "Input CGST",
    hint: "the credit claimed on purchases",
    match: (n) => /in(put)?/.test(n) && /cgst/.test(n) && !/out/.test(n),
  },
  {
    key: "inputSGST",
    label: "Input SGST",
    hint: "the credit claimed on purchases",
    match: (n) => /in(put)?/.test(n) && /sgst/.test(n) && !/out/.test(n),
  },
  {
    key: "inputIGST",
    label: "Input IGST",
    hint: "the credit claimed on purchases",
    match: (n) => /in(put)?/.test(n) && /igst/.test(n) && !/out/.test(n),
  },
];

/** The financial years offered, newest first. */
function financialYears(now: Date): { label: string; from: string; to: string }[] {
  // The CURRENT financial year is the one that began in April: before April, that
  // is last April. Getting this wrong offers a year the company has not lived
  // through yet as the default.
  const y = now.getUTCMonth() >= 3 ? now.getUTCFullYear() : now.getUTCFullYear() - 1;
  const out: { label: string; from: string; to: string }[] = [];
  for (let i = 0; i < 6; i++) {
    const s = y - i;
    out.push({
      label: `${s}-${String((s + 1) % 100).padStart(2, "0")}`,
      from: `${s}-04-01`,
      to: `${s + 1}-03-31`,
    });
  }
  return out;
}

/** A tax triple as three labelled cells. Pure string rendering. */
function TaxCells({ tax }: { tax: GSTTax }) {
  return (
    <>
      <td className="px-2 py-0.5 text-right tabular-nums">{formatMoney(tax.camt)}</td>
      <td className="px-2 py-0.5 text-right tabular-nums">{formatMoney(tax.samt)}</td>
      <td className="px-2 py-0.5 text-right tabular-nums">{formatMoney(tax.iamt)}</td>
    </>
  );
}

/**
 * One comparison.
 *
 * ⛔ THE GROSS ROW IS NOT OPTIONAL AND IS NOT CONDITIONAL. It is rendered even
 * when it equals the net, because a reader who only ever sees it on bad years
 * learns to read its absence as good news — and its absence would then also be
 * how a rendering bug looks.
 */
function Leg({
  title,
  asks,
  d,
}: {
  title: string;
  asks: string;
  d: GSTR9Divergence;
}) {
  const months = d.months ?? [];
  return (
    <div
      data-testid={`gstr9-leg-${d.a}-${d.b}`}
      className="mt-3 border border-tally-bar/40 p-2"
    >
      <div className="flex items-baseline justify-between gap-2">
        <h3 className="font-bold">{title}</h3>
        <span
          data-testid={`gstr9-verdict-${d.a}-${d.b}`}
          className={d.agrees ? "text-xs" : "text-xs font-bold text-tally-bar"}
        >
          {d.agrees ? "agrees in all 12 months" : `disagrees in ${months.length} of 12 months`}
        </span>
      </div>
      <p className="mt-0.5 text-xs opacity-80">{asks}</p>

      <table className="mt-1 w-full max-w-2xl">
        <thead>
          <tr className="text-xs opacity-70">
            <th className="px-2 py-0.5 text-left"> </th>
            <th className="px-2 py-0.5 text-right">CGST</th>
            <th className="px-2 py-0.5 text-right">SGST</th>
            <th className="px-2 py-0.5 text-right">IGST</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <th className="px-2 py-0.5 text-left font-normal">
              Net for the year ({d.a} − {d.b})
            </th>
            <TaxCells tax={d.net} />
          </tr>
          <tr className={d.offsetting ? "font-bold text-tally-bar" : ""}>
            <th className="px-2 py-0.5 text-left font-normal">
              Gross divergence (sum of monthly differences)
            </th>
            <TaxCells tax={d.gross} />
          </tr>
        </tbody>
      </table>

      {/* ⭐ THE SUPPRESSION. When the months cancel, the net above is a number
          that HIDES a disagreement, and this is what says so. */}
      {d.offsetting ? (
        <p
          data-testid={`gstr9-offsetting-${d.a}-${d.b}`}
          className="mt-1 max-w-2xl border border-tally-bar p-2 text-xs font-bold text-tally-bar"
        >
          ⛔ THE MONTHS CANCEL. The net above is smaller than the gross because
          this year's differences run in opposite directions — do not read the net
          as the size of the problem. The disagreeing months are{" "}
          {months.join(", ")}.
        </p>
      ) : null}

      {!d.agrees && !d.offsetting ? (
        <p className="mt-1 text-xs">
          Disagreeing months: {months.join(", ")}.
        </p>
      ) : null}
    </div>
  );
}

/** The taxable-value comparison, which has one figure rather than three heads. */
function TaxableLeg({ d }: { d: GSTR9TaxableDivergence }) {
  const months = d.months ?? [];
  return (
    <div data-testid="gstr9-leg-taxable" className="mt-3 border border-tally-bar/40 p-2">
      <div className="flex items-baseline justify-between gap-2">
        <h3 className="font-bold">GSTR-1 vs GSTR-3B (taxable value)</h3>
        <span className={d.agrees ? "text-xs" : "text-xs font-bold text-tally-bar"}>
          {d.agrees ? "agrees in all 12 months" : `disagrees in ${months.length} of 12 months`}
        </span>
      </div>
      <p className="mt-0.5 text-xs opacity-80">
        TWO SOURCES ONLY, and the third column is missing for a reason rather than
        by omission: the books post TAX to ledgers, and taxable value is not a
        posted quantity. There is no books-side opinion on it to compare against.
      </p>
      <dl className="mt-1 flex gap-6 text-xs">
        <div>
          <dt className="opacity-70">Net for the year</dt>
          <dd className="tabular-nums">{formatMoney(d.net)}</dd>
        </div>
        <div className={d.offsetting ? "font-bold text-tally-bar" : ""}>
          <dt className="opacity-70">Gross divergence</dt>
          <dd className="tabular-nums">{formatMoney(d.gross)}</dd>
        </div>
      </dl>
      {d.offsetting ? (
        <p
          data-testid="gstr9-offsetting-taxable"
          className="mt-1 max-w-2xl border border-tally-bar p-2 text-xs font-bold text-tally-bar"
        >
          ⛔ THE MONTHS CANCEL — read the gross, not the net. Disagreeing months:{" "}
          {months.join(", ")}.
        </p>
      ) : null}
    </div>
  );
}

/** How a table's status reads on the page. Never abbreviated to a colour. */
const STATUS_WORDS: Record<GSTR9TableStatus, string> = {
  DERIVED: "derived from these books",
  PARTIAL: "partly derived — some rows cannot be produced",
  NOT_DERIVED: "NOT DERIVED",
};

export function GSTR9Screen({ snapshot }: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();
  void snapshot;

  const years = useMemo(() => financialYears(new Date()), []);
  const [yearIndex, setYearIndex] = useState(0);
  const [pickingYear, setPickingYear] = useState(false);

  const [ledgers, setLedgers] = useState<LedgerListItem[] | null>(null);
  const [chosen, setChosen] = useState<Partial<GSTR9TaxLedgers>>({});

  const [data, setData] = useState<GSTR9Response | null>(null);
  const [error, setError] = useState("");
  const [denied, setDenied] = useState(false);
  const [busy, setBusy] = useState(false);

  const year = years[yearIndex]!;

  // ---- The ledgers this company has. SERVED, so nothing here asks for a uuid.
  useEffect(() => {
    let live = true;
    client
      .listLedgers(companyID)
      .then((res) => {
        if (!live) return;
        setLedgers(res.ledgers);
        // ⚠️ PRE-SELECT, DO NOT DECIDE. Each slot takes the first ledger whose
        // name matches its fragment; the control below still shows the choice and
        // the operator can change any of them before anything is fetched.
        const pick: Partial<GSTR9TaxLedgers> = {};
        for (const slot of LEDGER_SLOTS) {
          const hit = res.ledgers.find((l) => slot.match(l.name.toLowerCase()));
          if (hit) pick[slot.key] = hit.id;
        }
        setChosen(pick);
      })
      .catch((e: unknown) => {
        if (!live) return;
        setLedgers([]);
        setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      live = false;
    };
  }, [client, companyID]);

  const complete = useMemo(
    () => LEDGER_SLOTS.every((s) => Boolean(chosen[s.key])),
    [chosen],
  );

  // ---- ⛔ THE REPORT'S OWN PERMISSION, PROBED ON MOUNT.
  //
  // Without this, a refused operator sees a setup form, chooses six ledgers, and
  // only discovers they are not permitted when they press the button — which
  // reads as "the report failed" rather than "you may not read this". Migration
  // 0087's refusal is a DECISION and an operator should meet it immediately.
  //
  // ⚠️ IT SENDS AN EMPTY WINDOW AND EMPTY LEDGERS DELIBERATELY, and the two
  // answers are the whole point: the PDP runs BEFORE the handler, so a refused
  // level gets 403 while a permitted one gets 400 about the missing parameters.
  // That is exactly the discriminator this screen needs, it costs ONE request,
  // and — this is the part that matters — it NEVER reaches Generate9, which reads
  // twelve months across four queries each. A speculative full report would be
  // forty-eight queries fired at a screen nobody has asked anything of yet.
  //
  // This is the same probe BudgetVarianceScreen sends with an empty budget_id,
  // for the same reason and with the same two answers.
  useEffect(() => {
    let live = true;
    const none = {
      cgst: "",
      sgst: "",
      igst: "",
      inputCGST: "",
      inputSGST: "",
      inputIGST: "",
    };
    client.gstr9(companyID, "", "", none).catch((e: unknown) => {
      if (!live) return;
      if (e instanceof ApiError && e.status === 403) setDenied(true);
      // A 400 means the report is PERMITTED and only the request was empty,
      // which is true by construction here. Nothing is said.
    });
    return () => {
      live = false;
    };
  }, [client, companyID]);

  const load = useCallback(() => {
    if (!complete) return;
    setBusy(true);
    setError("");
    setDenied(false);
    client
      .gstr9(companyID, year.from, year.to, chosen as GSTR9TaxLedgers)
      .then((res) => {
        setData(res.gstr9);
        setBusy(false);
      })
      .catch((e: unknown) => {
        setBusy(false);
        setData(null);
        // ⛔ A 403 IS MIGRATION 0087'S DECISION, NOT A FAULT. Rendered as such:
        // an operator told "something went wrong" retries; one told what the
        // refusal is and who can lift it asks the right person.
        if (e instanceof ApiError && e.status === 403) {
          setDenied(true);
          return;
        }
        setError(e instanceof Error ? e.message : String(e));
      });
  }, [chosen, client, companyID, complete, year.from, year.to]);

  // Ctrl+P. There is no printed layout and there is a REASON rather than a
  // backlog item: on screen the ten unconditional disclosures sit beside the
  // figures; on paper, tidy tables on company stationery with nowhere for ten
  // caveats is a document somebody hands to a tax officer. The server answers
  // 501, and registering the handler is what turns a dead key into that sentence.
  usePrintAction({ voucher: { voucherID: null } });

  // F4 — CHOOSE THE FINANCIAL YEAR. Not F2 and not a date range: GSTR-9 covers a
  // financial year and cored refuses anything else, so a period frame would let
  // an operator compose a request that can only be answered with a 400. F2 says
  // that in words instead — see the `gstr9` block in the TallyPrime keymap.
  useRegisterScreenAction(
    "selectFinancialYear",
    useCallback((): string => {
      if (denied) {
        return (
          "The year cannot be changed while this report is refused: you are not " +
          "permitted to read it at this level (report:gstr9)."
        );
      }
      setPickingYear(true);
      return "Choose the financial year. GSTR-9 is annual — 1 April to 31 March — and no other window exists.";
    }, [denied]),
  );

  return (
    <section data-testid="gstr9" className="p-3 text-sm">
      <h1 className="text-base font-bold text-tally-title">GSTR-9 Annual Return</h1>
      <p className="text-xs opacity-80">
        A RECONCILIATION of the year&apos;s GSTR-1 and GSTR-3B to the books —
        not a fresh derivation, and not a filing.
      </p>

      {/* ---- THE REFUSAL, RENDERED AS A DECISION. */}
      {denied ? (
        <div
          data-testid="gstr9-denied"
          className="mt-3 max-w-3xl border border-tally-bar p-3 text-xs"
        >
          <p className="font-bold text-tally-bar">
            You are not permitted to read this company&apos;s annual return.
          </p>
          <p className="mt-1">
            This is a decision, not a fault. The annual return is a whole financial
            year of turnover and input credit in one document, and — through the
            comparisons below it — a named list of the months in which this company
            may have mis-filed. The seeded Data Entry level is refused it
            (`report:gstr9`, migration 0087).
          </p>
          <p className="mt-1">
            An owner can grant it in Users &amp; Roles. ⚠️ It takes DELETING the
            `disallow display report:gstr9` rule rather than adding an allow: the
            two tie on specificity and a disallow wins a tie, so an added allow
            would silently lose.
          </p>
        </div>
      ) : null}

      {pickingYear ? (
        <div className="mt-3 max-w-2xl" data-testid="gstr9-year-picker">
          <p className="text-xs">
            GSTR-9 covers a FINANCIAL YEAR — 1 April to 31 March. There is no
            annual return for any other window, and the server refuses one, so a
            free date range is not offered here.
          </p>
          <SelectionList
            label="SELECT A FINANCIAL YEAR"
            options={years.map((y) => `${y.label}  (${y.from} to ${y.to})`)}
            onSelect={(label) => {
              const i = years.findIndex((y) => label.startsWith(y.label));
              if (i >= 0) setYearIndex(i);
              setPickingYear(false);
            }}
          />
        </div>
      ) : null}

      {!denied ? (
        <>
          {/* ---- WHAT THIS REPORT NEEDS BEFORE IT CAN SAY ANYTHING. */}
          <div className="mt-3 max-w-4xl border border-tally-bar/40 p-2">
            <h2 className="font-bold">
              Financial year {year.label} ({year.from} to {year.to}) — F2 to change
            </h2>
            <p className="mt-1 text-xs opacity-80">
              THE SIX TAX LEDGERS ARE REQUIRED. Without them the books column would
              be zero for all twelve months and every period would look wrong by
              exactly the tax charged — twelve findings that are all artefacts of a
              missing setting. They are pre-selected by name where a name matched;
              check each one.
            </p>
            <div className="mt-2 grid grid-cols-1 gap-1 md:grid-cols-2">
              {LEDGER_SLOTS.map((slot) => (
                <label key={slot.key} className="flex items-center gap-2 text-xs">
                  <span className="w-28 shrink-0">
                    {slot.label}
                    <span className="block opacity-60">{slot.hint}</span>
                  </span>
                  <select
                    data-testid={`gstr9-ledger-${slot.key}`}
                    className="min-w-0 flex-1 border border-tally-bar/40 bg-transparent px-1 py-0.5"
                    value={chosen[slot.key] ?? ""}
                    onChange={(e) =>
                      setChosen((prev) => ({ ...prev, [slot.key]: e.target.value }))
                    }
                  >
                    <option value="">— not chosen —</option>
                    {(ledgers ?? []).map((l) => (
                      <option key={l.id} value={l.id}>
                        {l.name}
                      </option>
                    ))}
                  </select>
                </label>
              ))}
            </div>
            <div className="mt-2 flex items-center gap-3">
              <button
                type="button"
                data-testid="gstr9-load"
                disabled={!complete || busy}
                onClick={load}
                className="border border-tally-bar px-2 py-0.5 text-xs disabled:opacity-40"
              >
                {busy ? "Reading twelve months…" : "Reconcile the year"}
              </button>
              {!complete ? (
                <span data-testid="gstr9-incomplete" className="text-xs">
                  All six tax ledgers must be chosen. This report is a
                  reconciliation TO the books, and with no books column there is
                  nothing to reconcile against.
                </span>
              ) : null}
            </div>
          </div>

          {error ? (
            <p data-testid="gstr9-error" className="mt-3 text-xs text-tally-bar">
              {error}
            </p>
          ) : null}

          {data ? (
            <>
              {/* ---- ⛔ THE DISCLOSURES COME FIRST, ALWAYS, AND IN FULL. */}
              <div
                data-testid="gstr9-disclosures"
                className="mt-4 max-w-4xl border border-tally-bar p-2"
              >
                <h2 className="font-bold text-tally-bar">
                  What this document is not ({data.disclosures.length})
                </h2>
                <ul className="mt-1 space-y-1 text-xs">
                  {data.disclosures.map((d) => (
                    <li key={d} data-testid={`gstr9-disclosure-${d}`}>
                      <span className="font-mono opacity-70">{d}</span>
                      {" — "}
                      {/* An unrecognised token is shown RAW rather than dropped. */}
                      {DISCLOSURES[d] ?? "(no description for this token in this build)"}
                    </li>
                  ))}
                </ul>
              </div>

              {/* ---- THE COMPARISONS. */}
              <h2 className="mt-4 font-bold">
                Where the returns and the books disagree
              </h2>
              {LEGS.map((leg) => (
                <Leg key={leg.key} title={leg.title} asks={leg.asks} d={data[leg.key]} />
              ))}
              <TaxableLeg d={data.taxable_r1_vs_r3b} />

              {/* ---- THE EVIDENCE. */}
              <h2 className="mt-4 font-bold">The twelve months</h2>
              <p className="text-xs opacity-80">
                Every month of the year, including the ones with no activity. A
                month missing from a list of twelve is a month nobody checked.
              </p>
              <table
                data-testid="gstr9-months"
                className="mt-1 w-full max-w-5xl text-xs"
              >
                <thead>
                  <tr className="opacity-70">
                    <th className="px-2 py-0.5 text-left">Period</th>
                    <th className="px-2 py-0.5 text-right">GSTR-1 tax</th>
                    <th className="px-2 py-0.5 text-right">GSTR-3B tax</th>
                    <th className="px-2 py-0.5 text-right">Books (output)</th>
                    <th className="px-2 py-0.5 text-right">Books (input)</th>
                    <th className="px-2 py-0.5 text-left">Verdict</th>
                  </tr>
                </thead>
                <tbody>
                  {data.months.map((m) => {
                    const ok =
                      m.r1_matches_r3b &&
                      m.r3b_matches_books &&
                      m.itc_matches_books &&
                      m.hsn_foots_to_r1;
                    return (
                      <tr
                        key={m.fp}
                        data-testid={`gstr9-month-${m.fp}`}
                        className={ok ? "" : "font-bold text-tally-bar"}
                      >
                        <td className="px-2 py-0.5">{m.fp}</td>
                        <td className="px-2 py-0.5 text-right tabular-nums">
                          {formatMoney(sumHeads(m.r1_tax))}
                        </td>
                        <td className="px-2 py-0.5 text-right tabular-nums">
                          {formatMoney(sumHeads(m.r3b_tax))}
                        </td>
                        <td className="px-2 py-0.5 text-right tabular-nums">
                          {formatMoney(sumHeads(m.books_outward_tax))}
                        </td>
                        <td className="px-2 py-0.5 text-right tabular-nums">
                          {formatMoney(sumHeads(m.books_itc_tax))}
                        </td>
                        <td className="px-2 py-0.5">
                          {ok ? "ties" : describeBreak(m)}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>

              {/* ---- THE STATUTORY TABLES, WITH WHAT COULD NOT BE PRODUCED. */}
              <h2 className="mt-4 font-bold">The statutory tables</h2>
              <div data-testid="gstr9-tables" className="mt-1 max-w-4xl space-y-2">
                {data.tables.map((t) => (
                  <div
                    key={t.no}
                    data-testid={`gstr9-table-${t.no}`}
                    className="border border-tally-bar/40 p-2 text-xs"
                  >
                    <div className="flex items-baseline justify-between gap-2">
                      <h3 className="font-bold">
                        Table {t.no} — {t.title}
                      </h3>
                      <span
                        className={
                          t.status === "DERIVED" ? "opacity-70" : "font-bold text-tally-bar"
                        }
                      >
                        {STATUS_WORDS[t.status]}
                      </span>
                    </div>
                    {t.reason ? <p className="mt-0.5">{t.reason}</p> : null}

                    {t.rows && t.rows.length > 0 ? (
                      <table className="mt-1 w-full">
                        <tbody>
                          {t.rows.map((r) => (
                            <tr key={r.label}>
                              <th className="px-2 py-0.5 text-left font-normal">
                                {r.label} {r.title}
                              </th>
                              <td className="px-2 py-0.5 text-right tabular-nums">
                                {/* ⛔ A ROW WITH NO TAXABLE VALUE PRINTS A DASH,
                                    NOT A ZERO. The ITC tables are tax-only, and
                                    "no taxable value" is a different fact from
                                    "a taxable value of zero". */}
                                {r.txval === undefined ? "—" : formatMoney(r.txval)}
                              </td>
                              <TaxCells tax={r.tax} />
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    ) : null}

                    {t.omitted && t.omitted.length > 0 ? (
                      <ul className="mt-1 space-y-0.5">
                        {t.omitted.map((o) => (
                          <li key={o.label} data-testid={`gstr9-omitted-${t.no}-${o.label}`}>
                            <span className="font-bold">
                              {o.label} {o.title}
                            </span>
                            {" — not derived: "}
                            {o.reason}
                          </li>
                        ))}
                      </ul>
                    ) : null}

                    {/* ⛔ THE SUPPRESSED TOTAL. The reason goes where the figure
                        would have been — never a blank, which reads as zero. */}
                    {t.total_suppressed ? (
                      <p
                        data-testid={`gstr9-total-suppressed-${t.no}`}
                        className="mt-1 border border-tally-bar p-1 font-bold text-tally-bar"
                      >
                        No total is shown. {t.total_suppressed_reason}
                      </p>
                    ) : null}
                  </div>
                ))}
              </div>
            </>
          ) : null}
        </>
      ) : null}
    </section>
  );
}

/**
 * The three heads as one figure, by STRING addition of the fixed-4 decimals.
 *
 * ⛔ THERE IS NO Number() HERE AND THERE MUST NEVER BE. A tax total through a
 * float loses paisa on large values, which is precisely the class of error this
 * report exists to detect — a reconciliation that introduced its own rounding
 * would manufacture the divergences it reports.
 */
function sumHeads(t: GSTTax): Money {
  return addFixed4(addFixed4(t.camt, t.samt), t.iamt);
}

/**
 * Add two fixed-4 decimal strings without leaving string arithmetic.
 *
 * BigInt on the scaled integers: exact for any value a NUMERIC(19,4) can hold,
 * and no floating point anywhere. `BigInt` is not `Number` — it cannot lose a
 * paisa.
 */
function addFixed4(a: Money, b: Money): Money {
  const scale = (v: Money): bigint => {
    const raw = (v ?? "0").trim();
    const neg = raw.startsWith("-");
    const u = neg ? raw.slice(1) : raw;
    const dot = u.indexOf(".");
    const int = dot >= 0 ? u.slice(0, dot) : u;
    const frac = (dot >= 0 ? u.slice(dot + 1) : "").padEnd(4, "0").slice(0, 4);
    const n = BigInt(int || "0") * 10000n + BigInt(frac || "0");
    return neg ? -n : n;
  };
  const total = scale(a) + scale(b);
  const neg = total < 0n;
  const abs = neg ? -total : total;
  const int = abs / 10000n;
  const frac = (abs % 10000n).toString().padStart(4, "0");
  return `${neg ? "-" : ""}${int.toString()}.${frac}`;
}

/** Which comparison broke in this month, in words rather than a colour. */
function describeBreak(m: {
  r1_matches_r3b: boolean;
  r3b_matches_books: boolean;
  itc_matches_books: boolean;
  hsn_foots_to_r1: boolean;
}): string {
  const broken: string[] = [];
  if (!m.r1_matches_r3b) broken.push("GSTR-1 ≠ GSTR-3B");
  if (!m.r3b_matches_books) broken.push("GSTR-3B ≠ books");
  if (!m.itc_matches_books) broken.push("ITC ≠ books");
  if (!m.hsn_foots_to_r1) broken.push("HSN detail ≠ invoices");
  return broken.join("; ");
}
