import { describe, expect, it } from "vitest";

import {
  RATE_DECIMALS,
  foreignToBase,
  formatRate,
  hasRate,
  normaliseRate,
  parseRate,
} from "./rate";
import { formatFixed4, parseFixed4 } from "../screens/vouchers/itemInvoice";

/**
 * rate.test.ts — A RATE IS NOT MONEY, PROVED RATHER THAN ASSERTED.
 *
 * The design memo's Q1 asks for exactly one test by name: "a distinct `Rate`
 * type and a distinct parse/format pair, with a test that asserts a 10-dp rate
 * survives a round trip". This is that test, plus the CONTROL that gives it its
 * meaning — the same value through the MONEY pair, showing what would have
 * happened had the rate reused it.
 */

describe("a rate keeps ten decimals", () => {
  it("round-trips a ten-decimal rate exactly", () => {
    const rate = "83.6512345678";
    expect(formatRate(parseRate(rate))).toBe(rate);
  });

  it("⛔ THE CONTROL: the MONEY pair destroys the same rate, silently", () => {
    // This is not a test of parseFixed4 — parseFixed4 is correct, for money.
    // It is the demonstration that reusing it for a rate is a data-loss bug
    // that produces a perfectly well-formed value, which is why the two pairs
    // are separate files rather than one function with a `decimals` argument.
    expect(formatFixed4(parseFixed4("83.6512345678"))).toBe("83.6512");
    expect(formatFixed4(parseFixed4("83.6512345678"))).not.toBe("83.6512345678");
  });

  it("keeps the low decimals of a WEAK currency's rate, where 4dp is worst", () => {
    // 1 INR ≈ 0.0033333333 of some stronger unit. At four decimals this is
    // 0.0033 — a relative error around 1.5e-4.
    const weak = "0.0033333333";
    expect(formatRate(parseRate(weak))).toBe(weak);
    expect(formatFixed4(parseFixed4(weak))).toBe("0.0033");
  });

  it("survives a rate large enough to break float arithmetic", () => {
    // Scaled by 1e10 this is 9,999,999,999,999,999,999 — far past 2^53, so a
    // `number`-based implementation would lose the tail. bigint does not.
    const big = "999999999.9999999999";
    expect(formatRate(parseRate(big))).toBe(big);
  });

  it("pads a short rate to the scale the server stores", () => {
    expect(normaliseRate("83.65")).toBe("83.6500000000");
    expect(normaliseRate("1")).toBe("1.0000000000");
    expect(RATE_DECIMALS).toBe(10);
  });

  it("treats empty and zero alike, because a rate of zero is not a rate", () => {
    // The server CHECKs rate > 0, so zero can only ever mean absent and the two
    // readings can never conflict.
    expect(parseRate("")).toBe(0n);
    expect(hasRate("")).toBe(false);
    expect(hasRate("0")).toBe(false);
    expect(hasRate("0.0000000000")).toBe(false);
    expect(hasRate("83.65")).toBe(true);
  });
});

describe("foreignToBase is a SUGGESTION, not the source of the base amount", () => {
  it("converts at the full rate, not a truncated one", () => {
    // $1,000.00 at 83.6512345678 = 83,651.2345678 -> 83,651.2346 at fixed-4.
    const base = foreignToBase(parseFixed4("1000.00"), parseRate("83.6512345678"));
    expect(formatFixed4(base)).toBe("83651.2346");

    // ⛔ AND THIS IS WHY THE FIGURE IS ONLY A SUGGESTION. Had the rate come
    // through the money parser, the same conversion would give a different
    // base — and both are plausible invoice totals, which is exactly the
    // failure mode that has no visible symptom.
    const viaMoney = foreignToBase(parseFixed4("1000.00"), parseRate("83.6512"));
    expect(formatFixed4(viaMoney)).toBe("83651.2000");
  });

  it("rounds half-away-from-zero, as shopspring/decimal does on the server", () => {
    // 1 unit at 1.00000000005 = 1.00000000005 -> the 4dp figure is 1.0000; use
    // a case that lands exactly on a half at the fourth decimal instead.
    // 0.5 foreign at 1.0001 = 0.50005 -> 0.5001 (away from zero), not 0.5000.
    const base = foreignToBase(parseFixed4("0.5"), parseRate("1.0001"));
    expect(formatFixed4(base)).toBe("0.5001");
  });
});
