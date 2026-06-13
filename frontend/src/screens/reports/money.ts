/**
 * Money formatting helpers for the report screens.
 *
 * Money arrives from the backend as fixed-4 decimal STRINGS (e.g. "1234.5600").
 * It MUST NEVER be parsed into a JS `number` — that silently loses paisa on
 * large values and is the exact class of bug a chartered accountant catches.
 * These helpers operate purely on the string representation: they re-group the
 * integer part with the Indian digit-grouping convention (lakh/crore) and keep
 * two visible decimals, all without floating-point arithmetic.
 */

import type { Money } from "../../api";

/** Split a fixed-decimal money string into sign, integer and fractional parts. */
function parseMoney(value: Money | undefined): {
  negative: boolean;
  intPart: string;
  fracPart: string;
} {
  const raw = (value ?? "").trim();
  const negative = raw.startsWith("-");
  const unsigned = negative ? raw.slice(1) : raw;
  const dot = unsigned.indexOf(".");
  const intPart = dot >= 0 ? unsigned.slice(0, dot) : unsigned;
  const fracPart = dot >= 0 ? unsigned.slice(dot + 1) : "";
  return {
    negative,
    intPart: intPart.replace(/^0+(?=\d)/, "") || "0",
    fracPart,
  };
}

/**
 * Group an integer-part string with the Indian numbering system: the last three
 * digits are grouped together, then every two digits before that (e.g.
 * "1234567" -> "12,34,567"). Pure string work — no Number coercion.
 */
function groupIndian(intPart: string): string {
  if (intPart.length <= 3) return intPart;
  const head = intPart.slice(0, intPart.length - 3);
  const tail = intPart.slice(intPart.length - 3);
  const grouped = head.replace(/\B(?=(\d{2})+(?!\d))/g, ",");
  return `${grouped},${tail}`;
}

/**
 * Render a money string for display: Indian-grouped integer part with exactly
 * two decimals, parentheses-free (the sign is preserved). A blank/undefined
 * value renders as "0.00".
 */
export function formatMoney(value: Money | undefined): string {
  const { negative, intPart, fracPart } = parseMoney(value);
  const twoDp = (fracPart + "00").slice(0, 2);
  const body = `${groupIndian(intPart)}.${twoDp}`;
  return negative ? `-${body}` : body;
}

/**
 * True when a money string is exactly zero in value (any number of trailing
 * zeros, optional sign). Used to optionally hide zero-balance rows.
 */
export function isZeroMoney(value: Money | undefined): boolean {
  const { intPart, fracPart } = parseMoney(value);
  return intPart === "0" && /^0*$/.test(fracPart);
}
