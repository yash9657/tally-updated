/**
 * rate.ts — PARSE AND FORMAT AN EXCHANGE RATE. Ten decimals, not four.
 *
 * ⛔⛔ WHY THIS FILE EXISTS AT ALL, WHEN A PERFECTLY GOOD FIXED-POINT PAIR
 * ALREADY SHIPS IN screens/vouchers/itemInvoice.ts
 * ===========================================================================
 * Because that pair is for MONEY, money is fixed-4 everywhere in this product,
 * and `parseFixed4` TRUNCATES the fifth decimal onward — its own comment says
 * so in those words. A rate is `NUMERIC(19,10)` on the server (migration 0080)
 * and quoted as BASE UNITS PER ONE FOREIGN UNIT.
 *
 * Feed a rate to the money parser and 83.6512345678 becomes 83.6512. Nothing
 * fails: the truncated value is a perfectly well-formed rate, it round-trips
 * through every later layer exactly, the voucher balances in base, and the bill
 * is simply priced wrong for ever. THAT is why the two live in separate files
 * with separate names rather than one function with a `decimals` argument — a
 * shared helper is one default away from the same bug.
 *
 * ⚠️ AND THE ERROR IS NOT SMALL. Base-per-foreign for a weak currency is
 * 0.0033333333; at four decimals that is 0.0033, a relative error around
 * 1.5e-4 — thousands of rupees on a fifty-lakh invoice.
 *
 * ⭐ BIGINT, NEVER `number`. A rate scaled by 1e10 exceeds 2^53 for any rate
 * above about 900, so float arithmetic would start losing the low decimals of
 * ordinary rates (JPY, KRW, IDR are all quoted well above that in some
 * directions). The money pair uses bigint for the same class of reason.
 */

import type { Rate } from "./types";

/** The scale of a rate: migration 0080's NUMERIC(19,10), spelled once. */
export const RATE_DECIMALS = 10;

const RATE_SCALE = 10_000_000_000n; // 10n ** 10n

/**
 * Parse a rate string into integer units of 1e-10.
 *
 * Returns 0n for empty or unparseable input, exactly as `parseFixed4` does —
 * and 0n is ALSO how "no rate" is spelled, which is safe here for a reason
 * worth stating: a rate of zero is not a rate. The server CHECKs `rate > 0`,
 * so zero can only ever mean absent and the two readings can never conflict.
 *
 * ⚠️ Digits beyond the tenth are TRUNCATED, not rounded — the same rule the
 * money parser follows, so the two behave alike where they behave at all. The
 * server stores NUMERIC(19,10) and would round; a client that truncates can
 * therefore differ from the server in the 10th decimal of a rate quoted to more
 * than ten places. That is accepted deliberately: no quoting convention in use
 * publishes more than ten, and the alternative — a client that rounds while
 * `parseFixed4` beside it truncates — is a difference between two helpers on
 * the same screen, which is the harder one to reason about.
 */
export function parseRate(value: string): bigint {
  const raw = (value ?? "").trim();
  if (raw === "") return 0n;
  const m = /^([+-]?)(\d*)(?:\.(\d*))?$/.exec(raw);
  if (!m) return 0n;
  const sign = m[1] === "-" ? -1n : 1n;
  const whole = m[2] ?? "";
  const frac = (m[3] ?? "").slice(0, RATE_DECIMALS).padEnd(RATE_DECIMALS, "0");
  if (whole === "" && (m[3] ?? "") === "") return 0n;
  return sign * BigInt((whole === "" ? "0" : whole) + frac);
}

/** Render integer units of 1e-10 back as a ten-decimal string. */
export function formatRate(units: bigint): Rate {
  const neg = units < 0n;
  const abs = (neg ? -units : units).toString().padStart(RATE_DECIMALS + 1, "0");
  return `${neg ? "-" : ""}${abs.slice(0, -RATE_DECIMALS)}.${abs.slice(-RATE_DECIMALS)}`;
}

/**
 * Normalise an operator-typed rate to the ten-decimal form the wire carries.
 *
 * ⭐ THE OPERATOR TYPES "83.65" AND THE SERVER STORES "83.6500000000". Sending
 * the typed spelling through unchanged works — the server parses it — but then
 * the value the form holds and the value the GET returns differ as STRINGS,
 * and any screen that compares them to decide "has this changed?" reports a
 * spurious edit on every foreign voucher opened.
 */
export function normaliseRate(typed: string): Rate {
  return formatRate(parseRate(typed));
}

/** True when a rate string denotes an actual rate (present and above zero). */
export function hasRate(value: string | undefined): boolean {
  return parseRate(value ?? "") > 0n;
}

/**
 * Convert a foreign amount to its BASE equivalent, as a fixed-4 money string.
 *
 * ⛔⛔ THIS IS A CONVENIENCE FOR THE OPERATOR'S KEYBOARD AND NOTHING MORE. What
 * it returns is a SUGGESTION for the base amount field — it is NOT how the base
 * amount is determined, and the server neither computes nor checks it. The base
 * amount is STORED and authoritative (design memo Q2): three values are held,
 * not two plus arithmetic, because a multi-currency journal balances in BASE
 * and need not balance in any foreign currency, and because every pair of the
 * three can be made to agree with a wrong third.
 *
 * ⚠️ So an operator who overrides the suggested figure is RIGHT and the form
 * must let them — a voucher whose base leg is a negotiated or contracted figure
 * rather than the day's arithmetic is an ordinary voucher, not an error.
 *
 * Rounds HALF-AWAY-FROM-ZERO, which is what shopspring/decimal does on the
 * server; banker's rounding here would disagree on every exact half.
 */
export function foreignToBase(foreignFixed4: bigint, rateUnits: bigint): bigint {
  const n = foreignFixed4 * rateUnits;
  const neg = n < 0n;
  const abs = neg ? -n : n;
  const q = (abs + RATE_SCALE / 2n) / RATE_SCALE;
  return neg ? -q : q;
}
