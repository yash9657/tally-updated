import { describe, it, expect } from "vitest";

import { ApiError } from "./http";
import { createMockClient } from "./mock";

/**
 * `allow_negative_stock` through the MOCK client — register rows D5 + D43.
 *
 * ⛔ READ THE GAP BEFORE READING THE ASSERTIONS. This file proves the SETTING
 * round-trips. It cannot prove the SWITCH works, and nothing in vitest can:
 * this mock has no negative-stock enforcement at all — its `post` never
 * computes a closing position — so a voucher that drives an item below zero is
 * accepted here whether the flag is on or off. The accept/refuse pair that is
 * D5's actual oracle lives on the wire, in `scripts/drive-l15-d5.mjs`, read
 * back out of Postgres. The gap is pinned as `company-features-negative-stock`
 * in `corpora/mock-vs-cored/masters.json` so it cannot be mistaken for
 * coverage.
 *
 * What IS worth pinning here is the D43 shape, because D43 was not a stock bug
 * — it was a client being told a setting had been applied that never was. That
 * failure is reproducible in a mock, and a mock that echoed the request instead
 * of its store would hide it from all 61 test files that build masters through
 * this client.
 */
describe("company features: allow_negative_stock (D5 + D43)", () => {
  async function freshCompany(allow?: boolean) {
    const client = createMockClient({ seed: false });
    const { company_id } = await client.createCompany({
      name: "Negative Stock Co",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
      ...(allow === undefined ? {} : { allow_negative_stock: allow }),
    });
    return { client, company_id };
  }

  it("carries the key from create through to the read-back", async () => {
    const { client, company_id } = await freshCompany(true);
    const view = await client.getCompany(company_id);
    // THE D43 ASSERTION. The create resolving is not evidence: cored answered
    // 201 to this exact body for the whole life of the route while the column
    // stayed false.
    expect(view.allow_negative_stock).toBe(true);
  });

  it("defaults to REFUSE when the key is omitted", async () => {
    const { client, company_id } = await freshCompany();
    const view = await client.getCompany(company_id);
    // The restrictive default is the load-bearing one: an escape hatch nobody
    // asked for must be shut, and it must be shut in the mock too or every
    // screen test would be measured against a permissive tenant no real
    // operator has.
    expect(view.allow_negative_stock).toBe(false);
  });

  it("keeps an explicit false distinguishable from an omitted key", async () => {
    const { client, company_id } = await freshCompany(false);
    expect((await client.getCompany(company_id)).allow_negative_stock).toBe(
      false,
    );
  });

  it("switches the setting in both directions and reports the STORE", async () => {
    const { client, company_id } = await freshCompany();

    const on = await client.setCompanyFeatures(company_id, {
      allow_negative_stock: true,
    });
    expect(on.allow_negative_stock).toBe(true);
    // The independent read. `setCompanyFeatures` answering true while
    // `getCompany` answers false is precisely the divergence an echoed
    // response cannot detect, which is why both are asserted.
    expect((await client.getCompany(company_id)).allow_negative_stock).toBe(
      true,
    );

    // A switch that only goes one way is not a switch.
    const off = await client.setCompanyFeatures(company_id, {
      allow_negative_stock: false,
    });
    expect(off.allow_negative_stock).toBe(false);
    expect((await client.getCompany(company_id)).allow_negative_stock).toBe(
      false,
    );
  });

  it("leaves a setting alone when the field is omitted", async () => {
    const { client, company_id } = await freshCompany(true);
    // An empty body must not be a reset. This matters for the SECOND setting
    // that lands on this route rather than for this one: a PUT carrying only
    // the new setting would silently revert the old one if omission meant
    // false.
    const res = await client.setCompanyFeatures(company_id, {});
    expect(res.allow_negative_stock).toBe(true);
    expect((await client.getCompany(company_id)).allow_negative_stock).toBe(
      true,
    );
  });

  it("404s an unknown company rather than materialising one", async () => {
    const client = createMockClient({ seed: false });
    await expect(
      client.setCompanyFeatures("no-such-company", {
        allow_negative_stock: true,
      }),
    ).rejects.toBeInstanceOf(ApiError);
  });

  it("⚠️ DOES NOT ENFORCE the setting — the gap, pinned so it is not mistaken for coverage", async () => {
    // This is not a test of desired behaviour. It is a LABEL on a known
    // divergence: the mock accepts an outward move it has no stock for,
    // whichever way the flag is set, because it models no stock position at
    // all. If the mock ever grows an enforcer, this assertion fails and
    // whoever wrote it deletes this test and the corpus gap together — which
    // is the only way a known gap leaves the codebase on purpose.
    const { client, company_id } = await freshCompany(false);
    const view = await client.getCompany(company_id);
    expect(view.allow_negative_stock).toBe(false);
    // Nothing in this client can be asked "would this voucher go negative?".
    expect(
      Object.keys(client).some((k) => /negative/i.test(k)),
    ).toBe(false);
  });
});
