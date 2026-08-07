import { describe, it, expect } from "vitest";
import { createMockClient } from "./mock";
import type { CoredClient, GSTLineRequest, VoucherRequest } from ".";

/**
 * gstLineReadback.test.ts — THE MOCK'S HALF OF §4.3's READ-BACK AUDIT for
 * `voucher_gst_line`.
 *
 * ===========================================================================
 * ⛔⛔ THE DEFECT THIS PINS, AND WHY THE MOCK HAS TO PIN IT TOO
 * ===========================================================================
 * `voucher_gst_line` (migration 0016) is keyed on voucher_version_id, so AN ALTER
 * RE-STATES: whatever the body omits is gone. It was never on §4.3's list of
 * tables to check, and until now the voucher GET carried no trace of it. That
 * mattered more than for any of its five siblings, because the missing field is
 * an INPUT and not a record: `taxable_value`/`cgst`/`sgst`/`igst` on the view are
 * what the derivation PRODUCED, and the RATE it was derived at is not recoverable
 * from them.
 *
 * So a form rebuilt from the response had no rate to send back, and the ordinary
 * round trip on an accounting-mode GST invoice — open it, fix the narration, save
 * — re-derived the tax at 0%: an 11,800 invoice became 10,000, and the supply left
 * both the GSTR-1 b2b section and the table-12 HSN summary with nothing refusing
 * it.
 *
 * THE MOCK IS A REAL DELIVERABLE (§7.4). Its `gst_lines` is derived from the
 * request on EVERY write and never carried forward from the prior version,
 * because the server cannot carry it forward — and a mock that did would be more
 * forgiving than the server about exactly the property this projection exists to
 * establish (§9.4's first kind: a refusal, or here a deletion, the mock did not
 * make). The CONTROL below is what proves the mock is not being kind.
 */

const INTRA = { supplier: "29", pos: "29" };

async function seedGstSale(): Promise<{
  client: CoredClient;
  companyID: string;
  voucherID: string;
  body: (lines?: GSTLineRequest[]) => VoucherRequest;
}> {
  const client = createMockClient({ seed: false });
  const { company_id: companyID } = await client.createCompany({
    name: "Readback Co",
    fy_start_date: "2026-04-01",
    base_currency: "INR",
    state_code: INTRA.supplier,
  });
  const debtor = await client.createLedger(companyID, {
    name: "Acme Corp",
    group_name: "Sundry Debtors",
    state_code: INTRA.pos,
  });
  const sales = await client.createLedger(companyID, {
    name: "Domestic Sales",
    group_name: "Sales Accounts",
  });
  const [cgst, sgst, igst] = await Promise.all([
    client.createLedger(companyID, { name: "Output CGST", group_name: "Duties & Taxes" }),
    client.createLedger(companyID, { name: "Output SGST", group_name: "Duties & Taxes" }),
    client.createLedger(companyID, { name: "Output IGST", group_name: "Duties & Taxes" }),
  ]);

  // 10,000 at 18% intra-state = 900 + 900. The oracle is chosen so a wrong answer
  // cannot look right: a rate-less reading posts no tax at all and bills 10,000.
  const body = (lines?: GSTLineRequest[]): VoucherRequest => ({
    voucher_date: "2026-06-15",
    narration: "consultancy",
    party: "Acme Corp",
    party_ledger_id: debtor.ledger_id,
    postings: [
      { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "11800.0000" },
      { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "10000.0000" },
      { ledger_id: cgst.ledger_id, dr_cr: "CR", amount: "900.0000" },
      { ledger_id: sgst.ledger_id, dr_cr: "CR", amount: "900.0000" },
    ],
    ...(lines
      ? {
          gst: {
            supplier_state_code: INTRA.supplier,
            place_of_supply_state_code: INTRA.pos,
            lines,
            cgst_ledger_id: cgst.ledger_id,
            sgst_ledger_id: sgst.ledger_id,
            igst_ledger_id: igst.ledger_id,
          },
        }
      : {}),
  });

  const posted = await client.postSalesVoucher(
    companyID,
    body([{ taxable_value: "10000.0000", gst_rate_pct: "18", hsn: "998311" }]),
  );
  return { client, companyID, voucherID: posted.voucher_id, body };
}

describe("voucher_gst_line read-back (§4.3, the sixth table)", () => {
  it("returns the RATE and the HSN the tax was derived from, not just the amounts", async () => {
    const { client, companyID, voucherID } = await seedGstSale();
    const view = await client.getVoucher(companyID, voucherID);

    expect(view.gst_lines).toBeDefined();
    expect(view.gst_lines).toHaveLength(1);
    const line = view.gst_lines![0]!;
    // ⛔ THE DISTINGUISHING FIELD. `taxable_value` is already on the view, so a
    // projection carrying only that would look like a fix and change nothing —
    // 10,000 at no rate is 10,000 at 0%. The rate is what tells a form that can
    // restate the tax apart from one that silently zeroes it.
    expect(line.gst_rate_pct).toBe("18.0000");
    expect(line.hsn).toBe("998311");
    expect(line.taxable_value).toBe("10000.0000");
    // The derived side, so a form can show the split it read without re-deriving.
    expect(line.cgst).toBe("900.0000");
    expect(line.sgst).toBe("900.0000");
    expect(line.igst).toBe("0.0000");
    // 1-BASED and stored, like fulfilments[].line_no and unlike a cost split's
    // 0-based, re-derived posting_seq.
    expect(line.line_no).toBe(1);
  });

  it("an alter rebuilt FROM THE RESPONSE keeps the tax", async () => {
    const { client, companyID, voucherID, body } = await seedGstSale();
    const view = await client.getVoucher(companyID, voucherID);

    // ⛔ THE WHOLE POINT: `gst.lines` is the response's own array, lifted straight
    // back. The field names overlap GSTLineRequest exactly, and the extra keys the
    // projection adds (line_no, cgst/sgst/igst) are ignored by the server's
    // decoder — so this is an echo, not a translation the author had to remember.
    const req = body(view.gst_lines as unknown as GSTLineRequest[]);
    req.narration = "consultancy (typo fixed)";
    const altered = await client.alterSalesVoucher(companyID, voucherID, req);
    expect(altered.cgst).toBe("900.0000");
    expect(altered.sgst).toBe("900.0000");

    const after = await client.getVoucher(companyID, voucherID);
    expect(after.taxable_value).toBe("10000.0000");
    expect(after.cgst).toBe("900.0000");
    expect(after.gst_lines).toHaveLength(1);
    expect(after.gst_lines![0]!.gst_rate_pct).toBe("18.0000");
  });

  it("CONTROL: an alter that omits `gst` strips the tax and leaves the money", async () => {
    const { client, companyID, voucherID, body } = await seedGstSale();

    // The only body a client could build from a response that did not carry the
    // rate. It must be ACCEPTED — no refusal exists, because a supply with no tax
    // is a legal state — and it must clear the lines. If the mock kept them, the
    // assertion in the test above would pass no matter what the projection did.
    const stripped = body();
    stripped.narration = "consultancy (typo fixed)";
    await client.alterSalesVoucher(companyID, voucherID, stripped);

    const after = await client.getVoucher(companyID, voucherID);
    expect(after.gst_lines).toEqual([]);
    expect(after.cgst).toBe("0.0000");
    expect(after.taxable_value).toBe("0.0000");
    // ⚠️ AND THE MONEY IS UNTOUCHED, which is the whole shape of this defect
    // class: the debtor is still owed 11,800 and Output CGST still carries its
    // 900. Nothing is out of balance and a reportable supply has quietly gone.
    expect(after.postings).toHaveLength(4);
    expect(
      after.postings.find((p) => p.dr_cr === "D")?.amount,
    ).toBe("11800.0000");
  });
});
