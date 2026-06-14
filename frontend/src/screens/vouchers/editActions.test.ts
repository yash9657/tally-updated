import { describe, it, expect } from "vitest";
import { createActor } from "xstate";
import { navMachine, projectSnapshot } from "../../keystroke/navMachine";
import type { NavSnapshot } from "../../keystroke/navMachine";
import { createMockClient } from "../../api";
import { acceptVoucher, deleteLoadedVoucher } from "./editActions";

type Ev =
  | { type: "KEY"; combo: string }
  | { type: "SELECT"; menuItem: string };

function drive(events: Ev[]): NavSnapshot {
  const actor = createActor(navMachine, {
    input: { startScreen: "gateway", keymapProfile: "tallyprime" },
  });
  actor.start();
  for (const ev of events) actor.send(ev);
  const snap = projectSnapshot(actor.getSnapshot().context);
  actor.stop();
  return snap;
}

describe("Alt+D delete navigation", () => {
  it("pops an alteration-mode voucher back to its originating report", () => {
    // Drill Day Book -> voucher (alter), then Alt+D returns to the report.
    const snap = drive([
      { type: "KEY", combo: "Alt+G" },
      { type: "SELECT", menuItem: "Day Book" },
      { type: "KEY", combo: "Enter" }, // report
      { type: "KEY", combo: "Enter" }, // -> voucher (alter)
      { type: "KEY", combo: "Alt+D" }, // delete -> pop
    ]);
    expect(snap.screen).toBe("report");
    expect(snap.reportName).toBe("Day Book");
  });

  it("is a no-op in a create-mode voucher (nothing persisted to delete)", () => {
    const snap = drive([
      { type: "KEY", combo: "F8" }, // fresh sales voucher (create)
      { type: "KEY", combo: "Alt+D" },
    ]);
    expect(snap.screen).toBe("voucher");
    expect(snap.mode).toBe("create");
  });
});

describe("acceptVoucher / deleteLoadedVoucher dispatch", () => {
  async function seed() {
    const client = createMockClient({ seed: false });
    const { company_id } = await client.createCompany({
      name: "Acme",
      fy_start_date: "2025-04-01",
      base_currency: "INR",
    });
    const debtor = await client.createLedger(company_id, {
      name: "Acme Corp",
      group_name: "Sundry Debtors",
    });
    const sales = await client.createLedger(company_id, {
      name: "Domestic Sales",
      group_name: "Sales Accounts",
    });
    return { client, company_id, debtor, sales };
  }

  it("acceptVoucher POSTs in create mode and ALTERs in alter mode (restating)", async () => {
    const { client, company_id, debtor, sales } = await seed();
    const created = await acceptVoucher(
      client,
      company_id,
      { type: "sales", mode: "create" },
      {
        voucher_no: "INV-1",
        voucher_date: "2025-04-10",
        postings: [
          { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "400.0000" },
          { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "400.0000" },
        ],
      },
    );
    expect(created.version_no).toBe(1);

    const altered = await acceptVoucher(
      client,
      company_id,
      { type: "sales", mode: "alter", voucherID: created.voucher_id },
      {
        voucher_no: "INV-1",
        voucher_date: "2025-04-10",
        postings: [
          { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "650.0000" },
          { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "650.0000" },
        ],
      },
    );
    expect(altered.voucher_id).toBe(created.voucher_id);
    expect(altered.version_no).toBe(2);

    const tb = await client.getTrialBalance(company_id);
    expect(tb.total_debit).toBe("650.0000");
  });

  it("deleteLoadedVoucher rejects without a loaded voucher id", async () => {
    const { client, company_id } = await seed();
    await expect(
      deleteLoadedVoucher(client, company_id, {
        type: "sales",
        mode: "alter",
      }),
    ).rejects.toThrow();
  });
});
