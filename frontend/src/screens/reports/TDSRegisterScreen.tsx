import { useCallback, useEffect, useState } from "react";

import type { NavSnapshot } from "../../keystroke/navMachine";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { ApiError } from "../../api";
import type { TDSRegisterResponse } from "../../api";
import { useExportAction } from "../../export/useExportAction";
import { usePrintAction } from "../usePrint";
import {
  exactCell,
  moneyCell,
  textCell,
  type ExportRow,
  type ExportSheet,
} from "../../export/sheet";

/**
 * THE TDS / TCS REGISTER — Item Zero step 4.
 *
 * Everything Item Zero built before this was invisible: deductions and
 * collections derived correctly, refused correctly, stored correctly, and
 * impossible to look at. This is the screen that ends that.
 *
 * FOUR THINGS THIS SCREEN REFUSES TO DO
 *
 *  1. It NEVER invents a row. An empty period renders as a stated "nothing in
 *     this period" line. Sample rows on a tax screen are how a number nobody
 *     computed ends up in a return.
 *
 *  2. It NEVER hides the disclosures. They arrive in the payload — the server
 *     decides WHAT must be said, this screen decides only how it looks. The
 *     first of them is the one that matters: none of these figures is tax PAID,
 *     and it is the deposit, not the deduction, that stops interest running
 *     under s.396.
 *
 *  3. It NEVER shows a blank on a 403. Migration 0068 explicitly DISALLOWS this
 *     register to the seeded Data Entry level — it is every supplier this
 *     company pays and every customer it collects from, with amounts. A denial
 *     is a NORMAL answer here, so it is rendered as the reason it is.
 *
 *  4. It NEVER implies a return can be filed. `return_file.available` is false;
 *     the screen states that, names the blockers, and says what the operator can
 *     do instead. An absent "file return" button would leave them hunting for a
 *     feature that is deliberately not there.
 *
 *     ⚠️ BRANCH ON `available` AND ON NOTHING ELSE. `format_version` used to be
 *     empty and used to be a second, equivalent signal; the Form 140 file format
 *     has since been read and implemented, so it is now "1.0" while `available`
 *     is still false — the remaining blockers are a missing challan register and
 *     a missing TAN. A screen that keyed off the version would now show a file
 *     button for a file that cannot be built.
 *
 * ⚠️ A ZERO IN THE TAX COLUMN IS NOT AN ERROR, and the screen says which kind
 * it is. On the deduction side it means a payment below the threshold — kept on
 * purpose, because the annual aggregate is of amounts PAID. On the collection
 * side it can mean three different things, which is why the Declaration column
 * exists.
 */
export function TDSRegisterScreen({ snapshot }: { snapshot: NavSnapshot }) {
  const client = useClient();
  const companyID = useCompanyID();
  // F2 sets the report period. A register is a PERIOD report — unlike the Stock
  // Summary, both bounds are meaningful, and the server refuses to default
  // either.
  const from = snapshot.config?.periodFrom;
  const to = snapshot.config?.periodTo;

  const [data, setData] = useState<TDSRegisterResponse | null>(null);
  const [error, setError] = useState("");
  const [denied, setDenied] = useState(false);

  useEffect(() => {
    let live = true;
    if (!companyID || !from || !to) {
      setData(null);
      setError("");
      setDenied(false);
      return;
    }
    void (async () => {
      try {
        const res = await client.tdsRegister(companyID, from, to);
        if (!live) return;
        setData(res);
        setError("");
        setDenied(false);
      } catch (e) {
        if (!live) return;
        setData(null);
        // A 403 here is a DECISION, not a fault — see (3) above.
        if (e instanceof ApiError && e.status === 403) {
          setDenied(true);
          setError("");
        } else {
          setDenied(false);
          setError(e instanceof Error ? e.message : String(e));
        }
      }
    })();
    return () => {
      live = false;
    };
  }, [client, companyID, from, to]);

  // Alt+E. This screen renders its own two tables rather than going through
  // useReportView, so — like the Stock Summary — its Export key was DEAD while
  // the report button bar over it advertised "Alt+E Export".
  //
  // ⚠️ THE PERIOD IS PART OF THE ANSWER. Without F2 there is no register at all
  // (the server refuses to default either bound, because a defaulted period
  // answers a question nobody asked while looking like the one they did), so
  // Alt+E says that rather than writing an empty file that reads as "no tax".
  useExportAction(
    useCallback(() => {
      if (denied) {
        return "You do not have permission to read the TDS / TCS Register, so there is nothing to export.";
      }
      if (!from || !to) {
        return "Set the period with F2 first — the TDS / TCS Register has no rows until it is told which quarter to cover.";
      }
      if (error !== "") return `TDS / TCS Register unavailable, so there is nothing to export. ${error}`;
      if (data === null) return "TDS / TCS Register is still loading — nothing to export yet.";
      if (data.deductions.length === 0 && data.collections.length === 0) {
        return "Nothing was deducted or collected in this period, so the register has nothing to export.";
      }
      return tdsRegisterSheet(data);
    }, [data, denied, error, from, to]),
  );

  // Ctrl+P. THE REPORT BAR OVER THIS SCREEN ADVERTISES "Ctrl+P Print" — it is
  // REPORT_BAR, shared by every `report` frame — and this screen registered no
  // print handler, so the key fell through to the shell's generic "does not
  // apply on this screen". That is the worst of the three possible answers: the
  // register HAS a mapped slug (printTarget's REPORT_SLUGS), the server answers
  // 501 for it with a sentence saying the ACL rule is honoured and no layout has
  // been written, and mapping it was done precisely so Ctrl+P could say THAT
  // instead of nothing. An operator who reads "does not apply here" concludes the
  // key is wrong for this screen; one who reads the 501 knows the report is real
  // and the paper is not.
  //
  // Registered like every other report screen — no voucher, so the target
  // resolves from the frame — and, like them, it now carries the F2 period, so
  // the day a layout is written the printed quarter is the quarter on screen.
  //
  // ⚠️ It goes BEFORE the `denied` early return, because a hook may not be
  // called conditionally: a 403 must not change this component's hook count.
  usePrintAction();

  if (denied) {
    return (
      <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
        <h1 className="text-lg font-bold text-tally-title">
          TDS / TCS Register
        </h1>
        <p className="mt-2 max-w-3xl text-sm">
          You do not have permission to read this register. It lists every
          supplier this company pays and every customer it collects from, with
          amounts, so it is refused to the Data Entry level by default. An owner
          can grant it by adding an{" "}
          <span className="text-tally-title">
            allow display report:tds-register
          </span>{" "}
          rule in Users &amp; Roles.
        </p>
      </section>
    );
  }

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">TDS / TCS Register</h1>

      {!from || !to ? (
        <p className="mt-2 max-w-3xl text-sm">
          Press F2 to set the period. Both dates are needed — a register is read
          to answer &quot;what do I owe for THIS quarter&quot;, and a defaulted
          period would answer a question you did not ask while looking exactly
          like the one you did.
        </p>
      ) : error !== "" ? (
        <p className="mt-2 max-w-3xl text-sm text-tally-bar">{error}</p>
      ) : data === null ? (
        <p className="mt-2 text-sm">Loading…</p>
      ) : (
        <>
          <p className="mt-1 text-sm">
            {data.from} to {data.to}
            {data.tax_year_start === 0
              ? " — SPANS TWO TAX YEARS"
              : ` — tax year ${data.tax_year_start}-${String((data.tax_year_start + 1) % 100).padStart(2, "0")}`}
          </p>

          {/* ---- DEDUCTIONS ------------------------------------------------ */}
          <h2 className="mt-4 font-bold text-tally-title">
            Tax deducted at source
          </h2>
          {data.deductions.length === 0 ? (
            <p className="mt-1 text-sm">Nothing deducted in this period.</p>
          ) : (
            <table className="mt-1 w-full max-w-5xl text-sm">
              <thead>
                <tr className="text-left text-tally-title">
                  <th className="pr-3 font-normal">Date</th>
                  <th className="pr-3 font-normal">Voucher</th>
                  <th className="pr-3 font-normal">Deductee</th>
                  <th className="pr-3 font-normal">Section</th>
                  <th className="pr-3 font-normal">Code</th>
                  <th className="pr-3 text-right font-normal">Paid</th>
                  <th className="pr-3 text-right font-normal">Rate %</th>
                  <th className="pr-3 text-right font-normal">TDS</th>
                </tr>
              </thead>
              <tbody>
                {data.deductions.map((d) => (
                  <tr key={`${d.voucher_id}:${d.section}`}>
                    <td className="pr-3">{d.date}</td>
                    <td className="pr-3">{d.voucher_no}</td>
                    <td className="pr-3">
                      {d.deductee}
                      {!d.pan_available && (
                        <span className="text-tally-bar"> · no PAN</span>
                      )}
                    </td>
                    <td className="pr-3">{d.section}</td>
                    <td className="pr-3">{d.return_code}</td>
                    <td className="pr-3 text-right">{d.base}</td>
                    <td className="pr-3 text-right">{d.rate_pct}</td>
                    <td className="pr-3 text-right">
                      {d.tds}
                      {d.tds === "0.0000" && (
                        <span className="text-tally-bar"> · below limit</span>
                      )}
                    </td>
                  </tr>
                ))}
                <tr className="text-tally-title">
                  <td className="pr-3 pt-1" colSpan={5}>
                    Total
                  </td>
                  <td className="pr-3 pt-1 text-right">
                    {data.total_base_deducted}
                  </td>
                  <td />
                  <td className="pr-3 pt-1 text-right">{data.total_tds}</td>
                </tr>
              </tbody>
            </table>
          )}

          {/* ---- COLLECTIONS ----------------------------------------------- */}
          <h2 className="mt-4 font-bold text-tally-title">
            Tax collected at source
          </h2>
          {data.collections.length === 0 ? (
            <p className="mt-1 text-sm">Nothing collected in this period.</p>
          ) : (
            <table className="mt-1 w-full max-w-5xl text-sm">
              <thead>
                <tr className="text-left text-tally-title">
                  <th className="pr-3 font-normal">Date</th>
                  <th className="pr-3 font-normal">Voucher</th>
                  <th className="pr-3 font-normal">Buyer</th>
                  <th className="pr-3 font-normal">Section</th>
                  <th className="pr-3 font-normal">s.394(2)</th>
                  <th className="pr-3 text-right font-normal">Sale</th>
                  <th className="pr-3 text-right font-normal">Rate %</th>
                  <th className="pr-3 text-right font-normal">TCS</th>
                </tr>
              </thead>
              <tbody>
                {data.collections.map((c) => (
                  <tr key={`${c.voucher_id}:${c.section}`}>
                    <td className="pr-3">{c.date}</td>
                    <td className="pr-3">{c.voucher_no}</td>
                    <td className="pr-3">{c.buyer}</td>
                    <td className="pr-3">{c.section}</td>
                    {/* The column that tells three zeroes apart. */}
                    <td className="pr-3">
                      {c.declaration === "NONE" ? (
                        "—"
                      ) : c.declaration === "EXEMPT" ? (
                        <span className="text-tally-title">exempt</span>
                      ) : (
                        <span className="text-tally-bar">
                          invalid, no PAN — penal rate
                        </span>
                      )}
                    </td>
                    <td className="pr-3 text-right">{c.base}</td>
                    <td className="pr-3 text-right">{c.rate_pct}</td>
                    <td className="pr-3 text-right">{c.tcs}</td>
                  </tr>
                ))}
                <tr className="text-tally-title">
                  <td className="pr-3 pt-1" colSpan={5}>
                    Total
                  </td>
                  <td className="pr-3 pt-1 text-right">
                    {data.total_base_collected}
                  </td>
                  <td />
                  <td className="pr-3 pt-1 text-right">{data.total_tcs}</td>
                </tr>
              </tbody>
            </table>
          )}

          {/* ---- WHAT THIS CANNOT TELL YOU --------------------------------- */}
          <h2 className="mt-5 font-bold text-tally-title">
            What this register does not tell you
          </h2>
          <ul className="mt-1 max-w-3xl list-disc pl-5 text-xs">
            {data.disclosures.map((d) => (
              <li key={d} className="mt-1">
                {d}
              </li>
            ))}
          </ul>

          {/* ---- THE RETURN, AND WHY THERE ISN'T ONE ----------------------- */}
          {!data.return_file.available && (
            <div className="mt-4 max-w-3xl border border-tally-fieldText p-2">
              <h2 className="font-bold text-tally-title">
                Filing the return — not available
              </h2>
              <p className="mt-1 text-xs">{data.return_file.summary}</p>
              <p className="mt-2 text-xs text-tally-title">What is missing:</p>
              <ul className="mt-1 list-disc pl-5 text-xs">
                {data.return_file.blockers.map((b) => (
                  <li key={b.what} className="mt-1">
                    <span className="text-tally-title">{b.what}</span> — {b.why}{" "}
                    <span className="text-tally-fieldText">({b.where})</span>
                  </li>
                ))}
              </ul>
              <p className="mt-2 text-xs">
                {data.return_file.what_you_can_do_instead}
              </p>
              <p className="mt-1 text-xs text-tally-bar">
                {data.return_file.validation_note}
              </p>
              {/* The citation, ON THE ARTEFACT'S OWN FACE — the same reason the
                  missing-QR notice is printed on an e-invoice rather than kept
                  in a design note. It is also what stops this panel from being
                  read as "nobody has looked at the format yet". */}
              {data.return_file.format_version !== "" && (
                <p className="mt-2 text-xs text-tally-fieldText">
                  Layout implemented from format version{" "}
                  <span className="text-tally-title">
                    {data.return_file.format_version}
                  </span>{" "}
                  — {data.return_file.format_source}
                </p>
              )}
            </div>
          )}
        </>
      )}
    </section>
  );
}

/**
 * The register as ONE sheet.
 *
 * ⛔ TWO TABLES ON SCREEN, ONE TABLE IN THE FILE, AND A `Type` COLUMN IS WHAT
 * MAKES THAT HONEST. Deductions and collections are different obligations under
 * different sections and they must never be added together; stacking them with
 * no discriminator would produce a file whose "Tax" column an operator could
 * total into a single meaningless figure. The two 5th columns likewise stay
 * SEPARATE (`Return code` for deductions, `Declaration` for collections)
 * because they answer different questions — the declaration is the only field
 * that tells three different zeroes apart, and folding it into the code column
 * would lose that.
 *
 * `rate_pct` is a rate, not prose, and `date` / `voucher_no` are identifiers:
 * all three are exact cells so a spreadsheet cannot turn 10.00 into 10 or a
 * voucher number into arithmetic.
 */
export function tdsRegisterSheet(data: TDSRegisterResponse): ExportSheet {
  const rows: ExportRow[] = [];
  for (const d of data.deductions) {
    rows.push([
      textCell("Deduction"),
      exactCell(d.date),
      exactCell(d.voucher_no),
      textCell(d.deductee + (d.pan_available ? "" : " (no PAN)")),
      exactCell(d.section),
      exactCell(d.return_code),
      textCell(""),
      moneyCell(d.base),
      exactCell(d.rate_pct),
      moneyCell(d.tds),
    ]);
  }
  if (data.deductions.length > 0) {
    rows.push([
      textCell("Deduction total"),
      textCell(""),
      textCell(""),
      textCell(""),
      textCell(""),
      textCell(""),
      textCell(""),
      moneyCell(data.total_base_deducted),
      textCell(""),
      moneyCell(data.total_tds),
    ]);
  }
  for (const c of data.collections) {
    rows.push([
      textCell("Collection"),
      exactCell(c.date),
      exactCell(c.voucher_no),
      textCell(c.buyer + (c.pan_available ? "" : " (no PAN)")),
      exactCell(c.section),
      textCell(""),
      textCell(c.declaration),
      moneyCell(c.base),
      exactCell(c.rate_pct),
      moneyCell(c.tcs),
    ]);
  }
  if (data.collections.length > 0) {
    rows.push([
      textCell("Collection total"),
      textCell(""),
      textCell(""),
      textCell(""),
      textCell(""),
      textCell(""),
      textCell(""),
      moneyCell(data.total_base_collected),
      textCell(""),
      moneyCell(data.total_tcs),
    ]);
  }
  return {
    title: `TDS TCS Register ${data.from} to ${data.to}`,
    headers: [
      "Type",
      "Date",
      "Voucher",
      "Party",
      "Section",
      "Return code",
      "Declaration",
      "Base",
      "Rate %",
      "Tax",
    ],
    rows,
  };
}
