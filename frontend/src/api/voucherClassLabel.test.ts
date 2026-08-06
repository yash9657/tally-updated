import { describe, it, expect } from "vitest";
import { createMockClient } from "./mock";
import { VOUCHER_CLASS_LABELS } from "./types";
import type { CoredClient, VoucherClass, VoucherRequest } from "./index";
import { voucherTypeForClass } from "../screens/vouchers/voucherClass";

/**
 * ⛔⛔ THE `class` LABEL IS A WIRE CONTRACT, AND THE MOCK USED TO GET IT WRONG.
 *
 * This is §9.4's SECOND kind — a shape the mock got wrong — and it survived the
 * entire life of the field because **no screen rendered `class`**. Two separate
 * divergences, in one file:
 *
 *  - `DayBookEntry.class` was `cls.toUpperCase()`, i.e. `"SALES"`, where the
 *    server writes `"Sales"`;
 *  - `VoucherView.class` was a naive title-case, i.e. `"Credit-note"` /
 *    `"Delivery-note"` / `"Physical-stock"`, where the server writes
 *    `"Credit Note"` / `"Delivery Note"` / `"Physical Stock"`.
 *
 * The moment the Day Book DRILL began routing on this field, both became
 * load-bearing: an unrecognised label maps to no voucher screen, so the drill
 * silently declines to open the voucher and the operator gets "Drill down does not
 * apply on this screen" on a row that plainly is one. No error, no stack, nothing
 * to search for.
 *
 * ⛔ AND THIS FILE EXISTS BECAUSE THE FIRST ROUND OF TESTS DID NOT CATCH IT.
 * Reverting `classLabel` to the naive title-case left the whole 771-test suite
 * GREEN — every drill test used Sales / Receipt / Payment / Contra, and for
 * single-word classes the naive transform is accidentally right. That is §9.2 in
 * its purest form: the coverage looked complete and protected nothing. **The
 * multiword classes are the whole point, so this file drives all twelve.**
 */

async function seedStock(base: CoredClient): Promise<string> {
  const item = await base.createStockItem("co-1", {
    name: "Widget",
    unit_symbol: "Nos",
    hsn: "8481",
    gst_rate_pct: "18.0000",
  });
  return item.stock_item_id;
}

/**
 * A minimal, VALID body per class — the mock refuses the same shapes the server
 * refuses (a non-accounting class states no postings; Physical Stock states a
 * COUNT, not a movement), so these bodies are the real ones and not stubs.
 */
async function postOneOfEach(
  base: CoredClient,
): Promise<Map<VoucherClass, string>> {
  const bank = (
    await base.createLedger("co-1", {
      name: "HDFC Bank",
      group_name: "Bank Accounts",
    })
  ).ledger_id;
  const cash = (
    await base.createLedger("co-1", {
      name: "Cash",
      group_name: "Cash-in-Hand",
    })
  ).ledger_id;
  const party = (
    await base.createLedger("co-1", {
      name: "Acme Traders",
      group_name: "Sundry Debtors",
      state_code: "29",
    })
  ).ledger_id;
  const item = await seedStock(base);
  // Godowns are a seeded master and this company is unseeded, so every line is
  // untracked — which is a legal position ("" when the line is not tracked to a
  // location) and keeps this file about the CLASS LABEL and nothing else. The
  // stock journal is the exception: a transfer needs two DIFFERENT locations, so
  // it names the two the seeded fixture provides.
  const { godowns } = await base.listGodowns("co-1");
  const main = godowns[0]?.id ?? "";
  const yard = godowns[1]?.id ?? "";

  const money = (drID: string, crID: string): VoucherRequest => ({
    voucher_date: "2026-04-10",
    narration: "class label probe",
    postings: [
      { ledger_id: drID, dr_cr: "DR", amount: "1000.0000" },
      { ledger_id: crID, dr_cr: "CR", amount: "1000.0000" },
    ],
  });
  const goods = (direction: "IN" | "OUT"): VoucherRequest => ({
    voucher_date: "2026-04-10",
    inventory: [
      {
        stock_item_id: item,
        direction,
        qty: "1.0000",
        rate: "100.0000",
      },
    ],
  });

  const out = new Map<VoucherClass, string>();
  const record = async (
    cls: VoucherClass,
    post: Promise<{ voucher_id: string }>,
  ): Promise<void> => {
    out.set(cls, (await post).voucher_id);
  };

  await record("sales", base.postSalesVoucher("co-1", money(party, cash)));
  await record("purchase", base.postPurchaseVoucher("co-1", money(cash, party)));
  await record("receipt", base.postReceiptVoucher("co-1", money(bank, party)));
  await record("payment", base.postPaymentVoucher("co-1", money(party, bank)));
  await record("contra", base.postContraVoucher("co-1", money(cash, bank)));
  await record("journal", base.postJournalVoucher("co-1", money(party, cash)));
  await record(
    "credit-note",
    base.postCreditNoteVoucher("co-1", money(cash, party)),
  );
  await record(
    "debit-note",
    base.postDebitNoteVoucher("co-1", money(party, cash)),
  );
  // ⚠️ GOODS IN BEFORE GOODS OUT. The mock refuses a movement that would take a
  // position negative, exactly as the server does, so a delivery note posted
  // first fails with a stock error and this file would report a class-label
  // failure it does not have.
  await record("receipt-note", base.postReceiptNoteVoucher("co-1", goods("IN")));
  await record(
    "delivery-note",
    base.postDeliveryNoteVoucher("co-1", goods("OUT")),
  );
  await record(
    "stock-journal",
    base.postStockJournalVoucher("co-1", {
      voucher_date: "2026-04-10",
      // The PAIR, stated explicitly: a transfer is one movement OUT and one IN,
      // and it is the one class carrying both directions on the same voucher.
      inventory: [
        {
          stock_item_id: item,
          godown_id: main,
          direction: "OUT",
          qty: "1.0000",
          rate: "100.0000",
        },
        {
          stock_item_id: item,
          godown_id: yard,
          direction: "IN",
          qty: "1.0000",
          rate: "100.0000",
        },
      ],
    }),
  );
  await record(
    "physical-stock",
    base.postPhysicalStockVoucher("co-1", {
      voucher_date: "2026-04-10",
      physical_count: [
        { stock_item_id: item, qty: "5.0000", rate: "100.0000" },
      ],
    }),
  );
  return out;
}

describe("the voucher `class` label the mock stamps matches the server's", () => {
  it("⛔ every class, in the DAY BOOK and in the GET view, and it round-trips to a screen", async () => {
    const base = createMockClient({ seed: false });
    const ids = await postOneOfEach(base);

    // Completeness first: a class missing from `ids` would make every assertion
    // below vacuous for it, which is how the multiword classes went unprotected.
    const all = Object.keys(VOUCHER_CLASS_LABELS) as VoucherClass[];
    expect([...ids.keys()].sort()).toEqual([...all].sort());

    const day = await base.getDayBook("co-1");
    const byID = new Map(day.entries.map((e) => [e.voucher_id, e]));

    for (const cls of all) {
      const want = VOUCHER_CLASS_LABELS[cls];
      const id = ids.get(cls)!;

      // 1. THE DAY BOOK — the field the drill reads.
      expect(byID.get(id)?.class, `Day Book class for ${cls}`).toBe(want);

      // 2. THE GET VIEW — the field an alter round-trip reads.
      const view = await base.getVoucher("co-1", id);
      expect(view.class, `GET view class for ${cls}`).toBe(want);

      // 3. ⛔ AND IT ROUND-TRIPS BACK TO A SCREEN. This is the assertion that
      // makes the two above matter: a label nothing maps back is a voucher the
      // drill silently refuses to open. Asserting the label alone would pass on a
      // pair of tables that agree with each other and with nothing else.
      expect(voucherTypeForClass(want), `${want} maps to a screen`).toBe(cls);
    }
  });

  it("⛔ CONTROL: the labels the mock used to write map to NOTHING", () => {
    // The exact strings the OLD `classLabel` produced for the multiword classes.
    // If any resolved, the round-trip assertion above would pass on the broken
    // mock too and this whole file would be decoration.
    //
    // ⚠️ THE SINGLE-WORD CLASSES ARE ABSENT FROM THIS LIST ON PURPOSE, and their
    // absence is the finding this file was written for: the old naive title-case
    // was RIGHT for "Sales", "Receipt", "Payment" and "Contra", which is why
    // reverting it left a 771-test suite entirely green. Only the multiword
    // classes can distinguish the two implementations.
    for (const wrong of [
      "Credit-note",
      "Debit-note",
      "Delivery-note",
      "Receipt-note",
      "Stock-journal",
      "Physical-stock",
    ]) {
      expect(voucherTypeForClass(wrong), `${wrong} must not resolve`).toBeUndefined();
    }
    // ⚠️ CASE, ON THE OTHER HAND, IS TOLERATED, and deliberately — see
    // voucherTypeForClass's note. The words and the spaces are load-bearing; the
    // case is not, and hand-written fixtures in this repo carried "SALES".
    expect(voucherTypeForClass("SALES")).toBe("sales");
    expect(voucherTypeForClass("CREDIT NOTE")).toBe("credit-note");
    // And a class this build has NO screen for stays unmapped, which is what
    // makes its Day Book row un-drillable instead of corruptible.
    for (const noScreen of ["Payroll", "Sales Order", "Purchase Order"]) {
      expect(voucherTypeForClass(noScreen), noScreen).toBeUndefined();
    }
  });
});
