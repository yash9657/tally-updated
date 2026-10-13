import { describe, expect, it } from "vitest";

import { fyTagFor } from "./fyTag";

describe("the provisional number's FY label", () => {
  it("1 April starts a new year", () => {
    expect(fyTagFor(new Date("2025-04-01T00:00:00"))).toBe("2025-26");
  });

  it("31 March is still the OLD year — the boundary, in both directions", () => {
    expect(fyTagFor(new Date("2026-03-31T00:00:00"))).toBe("2025-26");
    expect(fyTagFor(new Date("2026-04-01T00:00:00"))).toBe("2026-27");
  });

  it("January through March belong to the year that began last April", () => {
    expect(fyTagFor(new Date("2026-01-15T00:00:00"))).toBe("2025-26");
  });

  it("a century roll keeps two digits and does not print '2099-100'", () => {
    expect(fyTagFor(new Date("2099-05-01T00:00:00"))).toBe("2099-00");
  });
});
