/**
 * forexSettings.test.ts — the client half of T8 step 5's setter.
 *
 * ⛔ THE MOCK IS A CONTRACT, NOT A CONVENIENCE. Every screen in this product can be
 * driven against it, so a mock that accepted a ledger the server refuses would let a
 * settings screen look finished and fail in production on the one refusal the whole
 * step rests on. D84's rule: the mock refuses what the server refuses, IN THE
 * SERVER'S ORDER.
 */

import { describe, expect, it } from "vitest";

import { createMockClient } from "./mock";

async function company() {
  const api = createMockClient({ seed: false });
  const { company_id: id } = await api.createCompany({
    name: "Forex Settings Co",
    fy_start_date: "2025-04-01",
    // ⚠️ An INR-base company: the base currency is what makes a USD posting
    // FOREIGN, and it is a required field precisely so no company is created
    // without one.
    base_currency: "INR",
  });
  return { api, id };
}

describe("forex settings", () => {
  it("reads as NOT CHOSEN before anything is set, with no nil uuid anywhere", async () => {
    const { api, id } = await company();
    const got = await api.forexSettings(id);
    // ⛔ ABSENT OR "", NEVER "00000000-0000-0000-0000-000000000000". A client that
    // round-tripped the nil uuid into the PUT would be sending a choice rather than
    // clearing one, and the setter would have to special-case a well-formed id that
    // names no ledger.
    expect(got.gain_loss_ledger_id ?? "").toBe("");
    expect(got.gain_loss_ledger_name ?? "").toBe("");
  });

  it("refuses a party ledger and names what to choose instead", async () => {
    const { api, id } = await company();
    const { ledger_id: debtor } = await api.createLedger(id, {
      name: "Globex LLC (USA)",
      group_name: "Sundry Debtors",
      created_by: "test",
    });
    // A realised gain credited here lands on a CUSTOMER'S OWN BALANCE, so a fully
    // settled bill would show a permanent residue — the silent wrong number the
    // server's refusal exists to prevent, reached through the setting instead of
    // around it.
    await expect(
      api.setForexSettings(id, { gain_loss_ledger_id: debtor }),
    ).rejects.toThrow(/revenue or expense/);
  });

  it("accepts an expense ledger and reads it back by id AND by name", async () => {
    const { api, id } = await company();
    const { ledger_id: pl } = await api.createLedger(id, {
      name: "Forex Gain/Loss",
      group_name: "Indirect Expenses",
      created_by: "test",
    });
    await api.setForexSettings(id, { gain_loss_ledger_id: pl });

    const got = await api.forexSettings(id);
    expect(got.gain_loss_ledger_id).toBe(pl);
    // ⭐ THE NAME MATTERS: a screen that had to resolve the uuid against the ledger
    // list to render what is configured would show a blank setting whenever that
    // list was paged or filtered.
    expect(got.gain_loss_ledger_name).toBe("Forex Gain/Loss");
  });

  it("accepts an income ledger too — a difference can be a GAIN", async () => {
    const { api, id } = await company();
    const { ledger_id: pl } = await api.createLedger(id, {
      name: "Exchange Gains",
      group_name: "Indirect Incomes",
      created_by: "test",
    });
    await api.setForexSettings(id, { gain_loss_ledger_id: pl });
    expect((await api.forexSettings(id)).gain_loss_ledger_id).toBe(pl);
  });

  it("CLEARS the choice with an empty id, and clearing is not a delete", async () => {
    const { api, id } = await company();
    const { ledger_id: pl } = await api.createLedger(id, {
      name: "Forex Gain/Loss",
      group_name: "Indirect Expenses",
      created_by: "test",
    });
    await api.setForexSettings(id, { gain_loss_ledger_id: pl });
    await api.setForexSettings(id, { gain_loss_ledger_id: "" });

    const got = await api.forexSettings(id);
    expect(got.gain_loss_ledger_id ?? "").toBe("");
    // ⚠️ AND THAT IS NOT A BROKEN STATE, it is "not chosen" — 0080 made the column
    // nullable so an operator who picked the wrong ledger can un-pick it. Every
    // difference-realising settlement starts refusing again, which is correct.
    expect(got.gain_loss_ledger_name ?? "").toBe("");
  });

  it("refuses a ledger that does not exist, before asking whether it is suitable", async () => {
    const { api, id } = await company();
    await expect(
      api.setForexSettings(id, { gain_loss_ledger_id: "led-nope" }),
    ).rejects.toThrow(/no such ledger/);
  });
});
