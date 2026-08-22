import { useEffect, useState } from "react";

import type { TaxSectionsResponse } from "../../api";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import {
  isTaxDeductionKeyed,
  sectionSummary,
  taxDeductionRefusal,
  taxLegRefusal,
  unpostedTaxLegNotice,
  type TaxDeductionForm,
} from "./taxDeduction";

/**
 * THE TDS / TCS BLOCK ON A VOUCHER — register row D3's missing form.
 *
 * ⛔ IT HARD-CODES NO SECTION. The list is SERVED (`GET .../tds-sections`), for
 * the reason the TDS challan screen's rule (3) gives about three minor heads: a
 * TypeScript copy of a statutory table is a drift bug, and this table has seven
 * rows with two rate columns and a threshold each, and it changes with every
 * Finance Act. A section this build cannot post would otherwise fail only on
 * save.
 *
 * ⛔ IT DERIVES NO TAX. The rate table is the server's and the deduction is
 * derived and stored there. What is printed under the picker is the SERVED rate
 * and threshold for the chosen section — the server's own figures echoed — never
 * a computed amount. A client-side preview that disagreed with the posting by a
 * rupee would be worse than no preview.
 *
 * ⚠️ IT IS COLLAPSED UNTIL ASKED FOR. Most payments deduct nothing, and a block
 * that is always open invites a half-keyed one on every voucher.
 */
export function TaxDeductionPanel({
  kind,
  value,
  onChange,
  parties,
  ledgerIdByName,
  ledgerNameByID,
  gridLedgers,
}: {
  /** TDS on money paid out; TCS on a sale. The two are never the same voucher. */
  kind: "TDS" | "TCS";
  value: TaxDeductionForm;
  onChange: (next: TaxDeductionForm) => void;
  /** Ledger names offered as the deductee / buyer, with their ids. */
  parties: readonly string[];
  ledgerIdByName: (name: string) => string;
  /**
   * THE REVERSE LOOKUP, AND IT IS FOR HYDRATION ONLY (register row D88).
   *
   * ⛔ A RE-STATED DEDUCTION ARRIVES WITH AN ID AND NO NAME. `taxDeductionFromView`
   * is pure and runs before the chart of accounts has loaded, so it resolves the
   * deductee from the VOUCHER'S OWN POSTINGS — and a deductee is very often not
   * a leg of the entry at all (a rent withholding debits Rent and credits the
   * bank; the landlord appears nowhere). The block then read
   *
   *     This voucher will carry TDS under 194I-B on (nobody) for 200000.0000
   *
   * over a deduction that names a real party, which is a disclosure saying the
   * wrong thing — seen in a browser on 2026-08-21. The effect below repairs the
   * name ONCE the chart is available; the id was right all along.
   */
  ledgerNameByID: (id: string) => string;
  /**
   * The ledgers KEYED ON THIS VOUCHER'S GRID, so the operator names the line
   * that carries the withheld tax from what is actually on the entry rather
   * than from the whole chart of accounts. ⛔ It is also what makes
   * taxLegRefusal possible: a leg the server cannot find is a verification that
   * silently does not happen.
   */
  gridLedgers: readonly string[];
}) {
  const client = useClient();
  const companyID = useCompanyID();
  const [open, setOpen] = useState(false);
  const [cat, setCat] = useState<TaxSectionsResponse | null>(null);
  /**
   * ⚠️ A FAILED VOCABULARY FETCH IS SAID OUT LOUD, never rendered as an empty
   * picker. An empty picker reads as "this build deducts under no section",
   * which is a statement about the law rather than about a network.
   */
  const [catError, setCatError] = useState("");

  /**
   * ⛔ NAME A HYDRATED PARTY ONCE THE CHART ARRIVES. See `ledgerNameByID`.
   *
   * ⚠️ IT ONLY EVER FILLS A BLANK. It never overwrites a name the operator
   * typed — that would fight the party input on every keystroke that does not
   * yet match a ledger, and typing a party who does not exist is a state this
   * form deliberately allows (the refusal names it, rather than minting one).
   * The id is untouched either way: it was correct before this ran.
   */
  useEffect(() => {
    if (value.partyName !== "" || value.partyLedgerId === "") return;
    const name = ledgerNameByID(value.partyLedgerId);
    if (name !== "") onChange({ ...value, partyName: name });
  }, [value, onChange, ledgerNameByID]);

  useEffect(() => {
    if (!open || cat !== null) return;
    let live = true;
    void (async () => {
      try {
        const res = await client.taxSections(companyID);
        if (live) {
          setCat(res);
          setCatError("");
        }
      } catch (e) {
        if (live) setCatError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      live = false;
    };
  }, [cat, client, companyID, open]);

  const patch = (p: Partial<TaxDeductionForm>) => onChange({ ...value, ...p });
  const rows = kind === "TDS" ? (cat?.tds_sections ?? []) : (cat?.tcs_sections ?? []);
  const doc = rows.find((r) => r.section === value.section);
  const noPan =
    kind === "TDS"
      ? ({ kind: "TDS", floorPct: cat?.tds_no_pan_floor_pct ?? "" } as const)
      : ({
          kind: "TCS",
          multiplier: cat?.tcs_no_pan_multiplier ?? "",
          floorPct: cat?.tcs_no_pan_floor_pct ?? "",
          ceilingPct: cat?.tcs_no_pan_ceiling_pct ?? "",
        } as const);
  const who = kind === "TDS" ? "Deductee" : "Buyer";

  const refusal =
    taxDeductionRefusal(value, kind) || taxLegRefusal(value, kind, gridLedgers);
  const keyed = isTaxDeductionKeyed(value);
  const notice = unpostedTaxLegNotice(value, kind);

  // ⚠️ DECLARED ABOVE THE COLLAPSED RETURN, not beside the panel body. The
  // collapsed branch READS `keyed`, and a `const` below it is in the temporal
  // dead zone — the component throws on its first render and the whole block
  // simply does not appear, which is register row D3 wearing a fresh costume.
  if (!open) {
    return (
      <>
        <button
          type="button"
          data-testid={`tax-${kind.toLowerCase()}-open`}
          className="mt-1 px-2 py-0.5 text-xs underline"
          onClick={() => setOpen(true)}
        >
          {kind === "TDS"
            ? "+ tax deducted at source (TDS)"
            : "+ tax collected at source (TCS)"}
        </button>
        {/* ⛔ A COLLAPSED BLOCK THAT IS STILL KEYED SAYS SO. Hiding the panel
            does not clear it — the operator meant what they typed — but a
            voucher that will post a deduction while nothing on screen mentions
            one is the undisclosed-figure defect this repo keeps finding. */}
        {keyed ? (
          <p data-testid={`tax-${kind.toLowerCase()}-collapsed`} className="text-xs">
            This voucher will carry {kind} under {value.section || "(no section)"} on{" "}
            {value.partyName || "(nobody)"} for {value.taxableAmount || "0"}.
          </p>
        ) : null}
      </>
    );
  }

  return (
    <div
      data-testid={`tax-${kind.toLowerCase()}-panel`}
      className="mt-2 border border-tally-fieldText p-2 text-xs"
    >
      <p className="text-tally-title">
        {kind === "TDS"
          ? "Tax DEDUCTED from this payment"
          : "Tax COLLECTED from this buyer"}
      </p>

      {catError !== "" ? (
        <p data-testid={`tax-${kind.toLowerCase()}-catalog-error`} className="mt-1">
          The statutory section list could not be read, so nothing can be chosen
          from it: {catError}. This is a failure to READ the list — it does not
          mean there is no section for this payment.
        </p>
      ) : null}

      <label className="mt-2 block">
        Section{" "}
        <select
          aria-label={`${kind} section`}
          data-testid={`tax-${kind.toLowerCase()}-section`}
          className="bg-tally-field px-1 text-tally-fieldText"
          value={value.section}
          onChange={(e) => patch({ section: e.target.value })}
        >
          {/* ⛔ THE EMPTY OPTION IS FIRST AND IS SELECTED UNTIL THE OPERATOR
              CHOOSES. Defaulting to the first section would assert what KIND of
              payment this is — which decides both the rate and the code the
              return files it under. */}
          <option value="">— choose the nature of payment —</option>
          {rows.map((r) => (
            <option key={r.section} value={r.section}>
              {r.section}
            </option>
          ))}
        </select>
      </label>
      {doc ? (
        <p data-testid={`tax-${kind.toLowerCase()}-rate`} className="mt-1">
          {sectionSummary(doc, value, noPan)}
        </p>
      ) : null}

      <label className="mt-2 block">
        {who}{" "}
        <input
          aria-label={`${kind} ${who.toLowerCase()}`}
          data-testid={`tax-${kind.toLowerCase()}-party`}
          list={`tax-${kind.toLowerCase()}-parties`}
          className="bg-tally-field px-1 text-tally-fieldText outline-none"
          value={value.partyName}
          onChange={(e) => {
            const name = e.target.value;
            // ⛔ THE ID IS RESOLVED FROM THE NAME, AND AN UNKNOWN NAME RESOLVES
            // TO "". The server REQUIRES the ledger id, and a party typed but
            // never created is not a deductee — leaving the id blank makes the
            // refusal above fire while the figures are still on screen, instead
            // of creating a ledger to satisfy a 422.
            patch({ partyName: name, partyLedgerId: ledgerIdByName(name) });
          }}
        />
      </label>
      <datalist id={`tax-${kind.toLowerCase()}-parties`}>
        {parties.map((p) => (
          <option key={p} value={p} />
        ))}
      </datalist>

      <label className="mt-2 block">
        Amount it applies to{" "}
        <input
          aria-label={`${kind} taxable amount`}
          data-testid={`tax-${kind.toLowerCase()}-amount`}
          inputMode="decimal"
          className="w-32 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
          value={value.taxableAmount}
          onChange={(e) => patch({ taxableAmount: e.target.value })}
        />
      </label>

      {kind === "TDS" ? (
        <fieldset className="mt-2">
          <legend>Deductee</legend>
          {(["OTHER", "IND_HUF"] as const).map((t) => (
            <label key={t} className="mr-3">
              <input
                type="radio"
                name={`tax-deductee-type`}
                data-testid={`tax-tds-type-${t}`}
                className="mr-1"
                checked={value.deducteeType === t}
                onChange={() => patch({ deducteeType: t })}
              />
              {t === "IND_HUF" ? "Individual / HUF" : "Company or other"}
            </label>
          ))}
        </fieldset>
      ) : null}

      {/* ⛔ WHICH LINE CARRIES THE WITHHELD TAX. Without it the deduction is
          recorded for the return while the books show the whole sum paid out —
          see TaxDeductionForm.taxLegLedger. It is offered from the LINES ON THIS
          VOUCHER, not from the chart, because a leg that is not posted is a
          verification that never runs. */}
      <label className="mt-2 block">
        Line carrying the {kind === "TDS" ? "withheld" : "collected"} tax{" "}
        <select
          aria-label={`${kind} liability line`}
          data-testid={`tax-${kind.toLowerCase()}-leg`}
          className="bg-tally-field px-1 text-tally-fieldText"
          value={value.taxLegLedger}
          onChange={(e) => patch({ taxLegLedger: e.target.value })}
        >
          <option value="">— none on this voucher —</option>
          {gridLedgers.map((l) => (
            <option key={l} value={l}>
              {l}
            </option>
          ))}
        </select>
      </label>
      {notice !== "" ? (
        <p data-testid={`tax-${kind.toLowerCase()}-unposted`} className="mt-1">
          {notice}
        </p>
      ) : null}

      <label className="mt-2 block">
        <input
          type="checkbox"
          data-testid={`tax-${kind.toLowerCase()}-pan`}
          className="mr-1"
          checked={value.panAvailable}
          onChange={(e) => patch({ panAvailable: e.target.checked })}
        />
        {/* ⚠️ TICKED BY DEFAULT, and unticking RAISES the rate. The label says
            which way it moves, because a checkbox whose effect is invisible is
            how a party gets deducted at 20% by accident. */}
        PAN is on record for this party (unticking it RAISES the rate)
      </label>

      {refusal !== "" ? (
        <p data-testid={`tax-${kind.toLowerCase()}-refusal`} className="mt-2 text-tally-title">
          {refusal}
        </p>
      ) : null}

      <button
        type="button"
        data-testid={`tax-${kind.toLowerCase()}-close`}
        className="mt-2 px-2 py-0.5 underline"
        onClick={() => setOpen(false)}
      >
        hide
      </button>
    </div>
  );
}
