/**
 * Presentation / round-trip mirror of the canonical Go tax engine
 * (backend compliance.TaxDerive, embedded in PostVoucher's deriveAndVerifyGST).
 *
 * IMPORTANT: this is NOT a second source of truth. The Go engine is
 * authoritative — the server re-derives the CGST/SGST/IGST split on every POST/PUT
 * and 422s if the client's posted tax lines diverge. We compute the split here
 * ONLY to (a) build the balanced + verifiable postings the form must send, and
 * (b) decide which non-zero tax lines to emit (intra: CGST+SGST; inter: IGST).
 * The DISPLAYED tax always comes from VoucherResponse.cgst/sgst/igst, never from
 * this helper.
 *
 * To avoid 422s the rounding MUST match the Go engine exactly: round-half-up at
 * 4 decimal places, applied to EACH half independently for the intra-state
 * case (CGST = SGST = round4(taxable * rate / 2 / 100)), and once for IGST
 * (round4(taxable * rate / 100)). Money stays a fixed-4 string end to end.
 */

const SCALE = 10000; // 4 dp

/** Parse a fixed-4 (or free-typed) decimal string into integer ten-thousandths. */
function toUnits(value: string): number {
  const v = (value ?? "").trim();
  if (v === "") return 0;
  const n = Number.parseFloat(v);
  if (!Number.isFinite(n)) return 0;
  return Math.round(n * SCALE);
}

/** Format integer ten-thousandths back to a fixed-4 decimal string. */
function fromUnits(units: number): string {
  return (units / SCALE).toFixed(4);
}

/**
 * Round-half-up at 4 dp of (taxableUnits * ratePercent / divisor / 100), where
 * taxableUnits is already scaled by SCALE. We compute in a higher-precision
 * domain (taxableUnits is ten-thousandths; multiply by rate, divide by
 * 100*divisor) and round half-up to whole ten-thousandths.
 *
 * round-half-up on the magnitude (Tally's commercial rounding); tax bases are
 * non-negative here so this is equivalent to Math.floor(x + 0.5).
 */
function round4HalfUp(taxableUnits: number, ratePct: number, divisor: number): number {
  // taxableUnits = taxable * SCALE. We want round4(taxable * rate / 100 / divisor)
  // in ten-thousandths = round( taxableUnits * rate / 100 / divisor ).
  const raw = (taxableUnits * ratePct) / (100 * divisor);
  return Math.floor(raw + 0.5);
}

export interface GstSplit {
  /** True when supplier state === place of supply (CGST+SGST), else IGST. */
  readonly intra: boolean;
  readonly cgst: string;
  readonly sgst: string;
  readonly igst: string;
}

/**
 * Derive the CGST/SGST/IGST split for a single-rate voucher. Intra-state
 * (supplier === place of supply, both 2-digit codes) posts CGST = SGST =
 * round4(taxable*rate/2/100) and zero IGST; inter-state posts IGST =
 * round4(taxable*rate/100) and zero CGST/SGST.
 *
 * All inputs and outputs are fixed-4 decimal strings (taxableValue, gstRatePct).
 */
export function deriveGstSplit(
  taxableValue: string,
  gstRatePct: string,
  supplierStateCode: string,
  placeOfSupplyStateCode: string,
): GstSplit {
  const taxableUnits = toUnits(taxableValue);
  const rate = Number.parseFloat((gstRatePct ?? "").trim());
  const safeRate = Number.isFinite(rate) ? rate : 0;
  const intra =
    supplierStateCode.trim() !== "" &&
    supplierStateCode.trim() === placeOfSupplyStateCode.trim();

  if (intra) {
    const half = round4HalfUp(taxableUnits, safeRate, 2);
    return {
      intra: true,
      cgst: fromUnits(half),
      sgst: fromUnits(half),
      igst: "0.0000",
    };
  }
  const full = round4HalfUp(taxableUnits, safeRate, 1);
  return {
    intra: false,
    cgst: "0.0000",
    sgst: "0.0000",
    igst: fromUnits(full),
  };
}

export { toUnits as gstToUnits, fromUnits as gstFromUnits };
