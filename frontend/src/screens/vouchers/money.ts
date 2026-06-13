/**
 * String-safe fixed-4 decimal money arithmetic for voucher entry.
 *
 * Backend money is transported as fixed-4 decimal STRINGS (e.g. "1234.5600").
 * We never coerce these to a JS `number` for storage or comparison — that would
 * silently lose paisa across large ledgers. All arithmetic here scales to
 * integer "ten-thousandths" before operating and re-formats to a 4-dp string.
 */

const SCALE = 10000;

/** Parse a money/qty/rate string into integer ten-thousandths. */
function toTenThousandths(value: string): number {
  const v = (value ?? "").trim();
  if (v === "") return 0;
  // parseFloat tolerates partially-typed input ("12.", "-", "") -> 0/NaN guard.
  const n = Number.parseFloat(v);
  if (!Number.isFinite(n)) return 0;
  return Math.round(n * SCALE);
}

/** Format integer ten-thousandths back to a fixed-4 decimal string. */
function fromTenThousandths(units: number): string {
  return (units / SCALE).toFixed(4);
}

/** Normalise a free-typed amount to a canonical fixed-4 string ("0.0000" default). */
export function normalizeMoney(value: string): string {
  return fromTenThousandths(toTenThousandths(value));
}

/** Sum a list of money strings, returning a fixed-4 string. */
export function sumMoney(values: readonly string[]): string {
  let acc = 0;
  for (const v of values) acc += toTenThousandths(v);
  return fromTenThousandths(acc);
}

/** Add two money strings. */
export function addMoney(a: string, b: string): string {
  return fromTenThousandths(toTenThousandths(a) + toTenThousandths(b));
}

/** Subtract b from a (fixed-4 string). */
export function subMoney(a: string, b: string): string {
  return fromTenThousandths(toTenThousandths(a) - toTenThousandths(b));
}

/** Multiply a money string by a rate-percent string, returning fixed-4. */
export function pctOf(value: string, ratePct: string): string {
  const units = toTenThousandths(value);
  const rate = Number.parseFloat((ratePct ?? "").trim());
  if (!Number.isFinite(rate)) return "0.0000";
  return fromTenThousandths(Math.round((units * rate) / 100));
}

/** True when two money strings are numerically equal (paisa-exact). */
export function moneyEq(a: string, b: string): boolean {
  return toTenThousandths(a) === toTenThousandths(b);
}

/** True when the money string is exactly zero. */
export function isZeroMoney(a: string): boolean {
  return toTenThousandths(a) === 0;
}

/** True when the money string is strictly greater than zero. */
export function isPositiveMoney(a: string): boolean {
  return toTenThousandths(a) > 0;
}
