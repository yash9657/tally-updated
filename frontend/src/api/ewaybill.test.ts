import { describe, it, expect } from "vitest";
import { createMockClient } from "./mock";
import { ApiError } from "./http";

/**
 * ewaybill.test.ts — DOES THE MOCK REFUSE WHAT THE SERVER REFUSES?
 *
 * The sibling of orders.test.ts, written for the same reason and against the
 * same three failure modes HANDOFF §9.4 names:
 *
 *   1. A REFUSAL the mock does not make. If the mock accepted a consignment with
 *      no distance, a screen would be built and tested against a raise that
 *      always succeeds and would 422 the first time a browser touched it.
 *   2. A SHAPE the mock gets wrong. `disclosures` is the one that matters here:
 *      a mock returning an empty array (or omitting it) would let a screen ship
 *      with nothing rendering it, and the whole honesty of the artefact rests on
 *      that block reaching paper. `tsc` cannot catch an empty array.
 *   3. A mock MORE PERMISSIVE than the server — the ensureLedger class, which no
 *      test written against the mock can catch by construction. The defence is
 *      that each expectation below was diffed against the GO HANDLER
 *      (internal/server/handler_ewaybill.go) and the DOMAIN
 *      (internal/ewaybill/ewaybill.go), not against the type I would like.
 *
 * ⛔ AND ONE REFUSAL IS DELIBERATELY ABSENT, WHICH IS THE POINT OF THIS FILE.
 * A missing vehicle on a Part B movement is a BLOCKER but NOT a refusal: an
 * operator may raise Part A while the lorry is still being arranged. A mock that
 * refused it would teach a screen author the opposite of the server's rule. That
 * case has its own test below, and it is the one that would catch the naive
 * implementation.
 *
 * ⚠️ THE STATUS CODES ARE PART OF THE CONTRACT. 404 means "no such voucher" and
 * 422 means "the rules refuse this consignment"; a screen rendering both as
 * "something went wrong" would make the difference invisible.
 */

async function bookedCompany(client: ReturnType<typeof createMockClient>) {
  const { company_id } = await client.createCompany({
    name: "EWB Co",
    fy_start_date: "2026-04-01",
    base_currency: "INR",
    state_code: "29",
  });
  const { ledger_id: party } = await client.createLedger(company_id, {
    name: "Acme Buyer",
    group_name: "Sundry Debtors",
    created_by: "owner",
  });
  const { stock_item_id: item } = await client.createStockItem(company_id, {
    name: "MS Angle",
    unit_symbol: "Nos",
    created_by: "owner",
  });
  return { cid: company_id, party, item };
}

/** An item invoice that MOVES GOODS — the precondition for any e-way bill. */
async function goodsInvoice(
  client: ReturnType<typeof createMockClient>,
  cid: string,
  party: string,
  item: string,
) {
  const res = await client.postSalesVoucher(cid, {
    voucher_date: "2026-06-10",
    party_ledger_id: party,
    item_ledger_id: party,
    gst: { place_of_supply_state_code: "29" },
    inventory: [
      { stock_item_id: item, direction: "OUT", qty: "200.0000", rate: "500.0000" },
    ],
  });
  return res.voucher_id;
}

const raise = (voucherID: string, over: Record<string, unknown> = {}) => ({
  voucher_id: voucherID,
  transporter_id: "29AACCT9999K1Z1",
  transporter_name: "Speedy Roadways",
  vehicle_number: "KA01AB1234",
  transport_mode: "ROAD" as const,
  distance_km: 120,
  created_by: "owner",
  ...over,
});

describe("the mock e-way bill API refuses what the server refuses", () => {
  it("raises a well-formed consignment — THE CONTROL for every refusal below", async () => {
    const c = createMockClient({ seed: false });
    const { cid, party, item } = await bookedCompany(c);
    const v = await goodsInvoice(c, cid, party, item);

    const bill = await c.raiseEWayBill(cid, raise(v));
    expect(bill.local_reference).not.toBe("");
    expect(bill.generation_count).toBe(1);
    expect(bill.blockers).toEqual([]);
  });

  it("⛔ the identifier is a LOCAL reference and is not shaped like an EBN", async () => {
    const c = createMockClient({ seed: false });
    const { cid, party, item } = await bookedCompany(c);
    const v = await goodsInvoice(c, cid, party, item);

    const bill = await c.raiseEWayBill(cid, raise(v));
    // A real EBN is 12 digits. Anything that could pass for one on a printed
    // sheet is the forgery this whole design avoids, so the mock must not mint
    // one either — a screen developed against a 12-digit mock value would
    // reasonably label it "e-Way Bill Number".
    expect(bill.local_reference).toMatch(/^LOCAL-EWB-/);
    expect(bill.local_reference).not.toMatch(/^\d{12}$/);
  });

  it("⛔ the disclosure block is present and says what it must", async () => {
    const c = createMockClient({ seed: false });
    const { cid, party, item } = await bookedCompany(c);
    const v = await goodsInvoice(c, cid, party, item);

    const bill = await c.raiseEWayBill(cid, raise(v));
    // NOT just "is an array": an empty array is the shape defect that ships.
    expect(bill.disclosures.length).toBeGreaterThan(0);
    const joined = bill.disclosures.join("\n");
    expect(joined).toContain("NOT A GOVERNMENT-ISSUED");
    expect(joined).toContain("SECONDARY");
    expect(bill.provider).toBe("fake-nic-ewaybill");
  });

  it("refuses a voucher that moves no goods, with 422", async () => {
    const c = createMockClient({ seed: false });
    const { cid, party } = await bookedCompany(c);
    // A receipt: money, no goods.
    const res = await c.postReceiptVoucher(cid, {
      voucher_date: "2026-06-11",
      party_ledger_id: party,
      postings: [
        { ledger_id: party, dr_cr: "DR", amount: "1000.0000" },
        { ledger_id: party, dr_cr: "CR", amount: "1000.0000" },
      ],
    });
    await expect(c.raiseEWayBill(cid, raise(res.voucher_id))).rejects.toThrow(
      /moves no goods/,
    );
    await c
      .raiseEWayBill(cid, raise(res.voucher_id))
      .catch((e: ApiError) => expect(e.status).toBe(422));
  });

  it("refuses an unstated distance, with 422 and the reasoning", async () => {
    const c = createMockClient({ seed: false });
    const { cid, party, item } = await bookedCompany(c);
    const v = await goodsInvoice(c, cid, party, item);

    await expect(
      c.raiseEWayBill(cid, raise(v, { distance_km: 0 })),
    ).rejects.toThrow(/validity period cannot be computed/);
    await c
      .raiseEWayBill(cid, raise(v, { distance_km: 0 }))
      .catch((e: ApiError) => expect(e.status).toBe(422));

    // ⛔ THE CONTROL IS THE INVERSE: the same consignment WITH a distance is
    // accepted. Without it the refusal is consistent with the mock being broken
    // for every input.
    await expect(c.raiseEWayBill(cid, raise(v))).resolves.toBeTruthy();
  });

  it("404s an unknown voucher rather than inventing a consignment", async () => {
    const c = createMockClient({ seed: false });
    const { cid } = await bookedCompany(c);
    await c
      .raiseEWayBill(cid, raise("no-such-voucher"))
      .catch((e: ApiError) => expect(e.status).toBe(404));
  });

  it("404s a GET for a voucher with no bill raised", async () => {
    const c = createMockClient({ seed: false });
    const { cid, party, item } = await bookedCompany(c);
    const v = await goodsInvoice(c, cid, party, item);
    await c.getEWayBill(cid, v).catch((e: ApiError) => expect(e.status).toBe(404));
  });
});

describe("the mock reproduces the server's Rule 138 conclusions", () => {
  it("⛔ a missing vehicle BLOCKS but does not REFUSE — the Part A slip", async () => {
    const c = createMockClient({ seed: false });
    const { cid, party, item } = await bookedCompany(c);
    const v = await goodsInvoice(c, cid, party, item);

    // This is the test that catches the naive implementation. Assess returns the
    // missing vehicle as a Blocker alongside the missing distance, and refusing
    // on "any blocker" is the obvious and WRONG reading.
    const bill = await c.raiseEWayBill(cid, raise(v, { vehicle_number: "" }));
    expect(bill.part_b_required).toBe(true);
    expect(bill.blockers).toHaveLength(1);
    expect(bill.blockers[0]).toMatch(/may not move/);
    // And it must reach the DISCLOSURES, which is what reaches paper. A blocker
    // living only in a field a screen may not render is a consignment that moves
    // on a bill it may not move on.
    expect(bill.disclosures.join("\n")).toContain("BLOCKER:");
  });

  it("validity rounds UP, and over-dimensional cargo is a tenth of the rate", async () => {
    const c = createMockClient({ seed: false });
    const { cid, party, item } = await bookedCompany(c);
    const v = await goodsInvoice(c, cid, party, item);

    // 250 km of ordinary cargo is TWO days, not 1.25 rounded to one: rounding
    // down would expire the bill mid-journey.
    const ordinary = await c.raiseEWayBill(cid, raise(v, { distance_km: 250 }));
    expect(ordinary.validity_days).toBe(2);

    // The SAME 250 km as over-dimensional cargo is thirteen days at 20 km/day.
    // ⛔ This is the asymmetry that makes the flag operator-asserted: guessing
    // "ordinary" would have granted a tenth of the days actually needed.
    const odc = await c.raiseEWayBill(
      cid,
      raise(v, { distance_km: 250, over_dimensional_cargo: true }),
    );
    expect(odc.validity_days).toBe(13);
  });

  it("a regeneration is COUNTED, not refused and not silent", async () => {
    const c = createMockClient({ seed: false });
    const { cid, party, item } = await bookedCompany(c);
    const v = await goodsInvoice(c, cid, party, item);

    const first = await c.raiseEWayBill(cid, raise(v));
    expect(first.generation_count).toBe(1);
    // Correcting a typo'd distance must not be refused.
    const second = await c.raiseEWayBill(cid, raise(v, { distance_km: 450 }));
    expect(second.generation_count).toBe(2);

    // And the second generation's figures are the ones that survive.
    const back = await c.getEWayBill(cid, v);
    expect(back.distance_km).toBe(450);
    expect(back.validity_days).toBe(3);
  });

  it("⛔ an alter marks the stored bill STALE, and the mock derives it on read", async () => {
    const c = createMockClient({ seed: false });
    const { cid, party, item } = await bookedCompany(c);
    const v = await goodsInvoice(c, cid, party, item);
    await c.raiseEWayBill(cid, raise(v));

    // CONTROL: not stale before the alter. Without this a hard-coded `true`
    // would pass.
    expect((await c.getEWayBill(cid, v)).stale).toBe(false);

    await c.alterSalesVoucher(cid, v, {
      voucher_date: "2026-06-10",
      party_ledger_id: party,
      item_ledger_id: party,
      gst: { place_of_supply_state_code: "29" },
      inventory: [
        { stock_item_id: item, direction: "OUT", qty: "150.0000", rate: "500.0000" },
      ],
    });

    const after = await c.getEWayBill(cid, v);
    expect(after.stale).toBe(true);
    // Stale means "no longer describes it", never "was quietly rewritten".
    expect(after.distance_km).toBe(120);
  });
});
