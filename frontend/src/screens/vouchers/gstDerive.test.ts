import { describe, it, expect } from "vitest";
import { deriveGstSplit } from "./gstDerive";

/**
 * The TS derive helper is a presentation MIRROR of the canonical Go engine
 * (compliance.TaxDerive). The server re-derives and 422s on mismatch, so these
 * vectors lock the rounding (round-half-up, 4 dp, on each half for intra) to the
 * Go contract. Money stays fixed-4 strings end to end.
 */
describe("deriveGstSplit", () => {
  it("intra-state 10000 @ 18% -> CGST 900 + SGST 900, no IGST", () => {
    const s = deriveGstSplit("10000.0000", "18", "29", "29");
    expect(s.intra).toBe(true);
    expect(s.cgst).toBe("900.0000");
    expect(s.sgst).toBe("900.0000");
    expect(s.igst).toBe("0.0000");
  });

  it("inter-state 10000 @ 18% -> IGST 1800, no CGST/SGST", () => {
    const s = deriveGstSplit("10000.0000", "18", "29", "27");
    expect(s.intra).toBe(false);
    expect(s.igst).toBe("1800.0000");
    expect(s.cgst).toBe("0.0000");
    expect(s.sgst).toBe("0.0000");
  });

  it("re-derives to a new base: intra 20000 @ 18% -> 1800/1800", () => {
    const s = deriveGstSplit("20000.0000", "18", "29", "29");
    expect(s.cgst).toBe("1800.0000");
    expect(s.sgst).toBe("1800.0000");
  });

  it("rounds each half independently, round-half-up at 4dp", () => {
    // 100 @ 3% -> total 3.00, half 1.50 each (exact, no rounding drift).
    const s = deriveGstSplit("100.0000", "3", "29", "29");
    expect(s.cgst).toBe("1.5000");
    expect(s.sgst).toBe("1.5000");
  });

  it("empty supplier state falls back to inter-state (IGST)", () => {
    const s = deriveGstSplit("10000.0000", "18", "", "29");
    expect(s.intra).toBe(false);
    expect(s.igst).toBe("1800.0000");
  });

  it("zero rate yields a zero split", () => {
    const s = deriveGstSplit("10000.0000", "", "29", "29");
    expect(s.cgst).toBe("0.0000");
    expect(s.sgst).toBe("0.0000");
    expect(s.igst).toBe("0.0000");
  });
});
