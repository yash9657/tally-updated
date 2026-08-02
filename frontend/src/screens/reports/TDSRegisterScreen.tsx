import { useEffect, useState } from "react";

import type { NavSnapshot } from "../../keystroke/navMachine";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { ApiError } from "../../api";
import type { TDSRegisterResponse } from "../../api";

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
 *  4. It NEVER implies a return can be filed. `return_file.available` is false
 *     and will stay false until the Form 140 layout is read from a primary
 *     source; the screen states that, names the blockers, and says what the
 *     operator can do instead. An absent "file return" button would leave them
 *     hunting for a feature that is deliberately not there.
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
            </div>
          )}
        </>
      )}
    </section>
  );
}
