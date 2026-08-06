import { describe, it, expect, afterEach } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor,
} from "@testing-library/react";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api/mock";
import type { CoredClient } from "../api";

/**
 * THE e-WAY BILL (T5) — and specifically, whether an OPERATOR can raise one.
 *
 * ⛔⛔ WHY EVERY TEST IN THIS FILE DRIVES THE REAL SHELL AND PRESSES THE REAL
 * KEY. T5's rules-and-payload core — Assess, BuildEWayBill, ValidateEWayBill,
 * the recorded NIC schema, the disclosure block — was BUILT, TESTED AND GOOD for
 * a whole release while the F11 Features row said `off`, because there was no
 * store, no route and no screen. A keyboard-only operator could not raise an
 * e-way bill at all. That is the fifth occurrence of one shape in this
 * repository's history (HANDOFF §4.1 lists four), so this file asserts the thing
 * every one of them was missing:
 *
 *   A FEATURE IS NOT SHIPPED WHEN ITS TESTS PASS. IT IS SHIPPED WHEN A HUMAN CAN
 *   REACH IT.
 *
 * So there are no tests here that call the screen-action dispatcher directly.
 * Every one renders `<AppShell/>`, presses the combo an operator would press,
 * and asserts what appears.
 *
 * ⛔ THE MUTATION PROOFS, WHICH WERE RUN AND ARE WHAT MAKE THIS FILE A GUARD
 * RATHER THAN A DESCRIPTION (HANDOFF §9.2):
 *
 *   - delete `"Alt+Z": { kind: "eWayBill" }` from keymaps/tallyprime.ts and
 *     every test below fails;
 *   - delete `case "eWayBill"` from navMachine.ts and every test below fails;
 *   - delete `"e-way-bill": EWayBillScreen` from defaultScreens.tsx and every
 *     test below fails.
 *
 * Ctrl+A rides on the PRE-EXISTING `accept` entry in AppShell's SCREEN_ACTIONS
 * rather than a new one, which is why there is no fourth mutation here — the
 * entry this screen depends on is already mutation-proved by other files.
 */

const COMPANY = "company-demo";

afterEach(cleanup);

function renderShell(client: CoredClient) {
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
  return screen.getByRole("application");
}

/**
 * A company with an invoice that MOVES GOODS, so there is something to raise a
 * bill against.
 */
async function bookedClient() {
  const c = createMockClient({ seed: false });
  const { company_id } = await c.createCompany({
    // The mock keys everything off the company id the provider is given, so the
    // fixture must create THAT company rather than a fresh one.
    id: COMPANY,
    name: "Demo Traders",
    fy_start_date: "2026-04-01",
    base_currency: "INR",
    state_code: "29",
  });
  const { ledger_id: party } = await c.createLedger(company_id, {
    name: "Acme Buyer",
    group_name: "Sundry Debtors",
    created_by: "owner",
  });
  const { stock_item_id: item } = await c.createStockItem(company_id, {
    name: "MS Angle",
    unit_symbol: "Nos",
    created_by: "owner",
  });
  await c.postSalesVoucher(company_id, {
    voucher_date: "2026-06-10",
    party_ledger_id: party,
    item_ledger_id: party,
    gst: { place_of_supply_state_code: "29" },
    inventory: [
      {
        stock_item_id: item,
        direction: "OUT",
        qty: "200.0000",
        rate: "500.0000",
      },
    ],
  });
  return c;
}

/** Press Alt+Z and wait for the screen to be up. */
async function openEWayBill(app: HTMLElement) {
  fireEvent.keyDown(app, { key: "z", altKey: true });
  await screen.findByRole("region", { name: "e-Way Bill" });
}

describe("a screen-owned key is actually ROUTED by the shell", () => {
  it("⛔ Alt+Z opens the e-way bill screen — the panel is ABSENT as a control", async () => {
    const app = renderShell(await bookedClient());

    // THE CONTROL. Without it, a screen that was somehow always mounted would
    // make the assertion below pass while Alt+Z did nothing at all.
    expect(screen.queryByRole("region", { name: "e-Way Bill" })).toBeNull();

    await openEWayBill(app);
    expect(screen.getByRole("region", { name: "e-Way Bill" })).toBeTruthy();
  });

  it("the standing notice is up BEFORE anything is raised", async () => {
    const app = renderShell(await bookedClient());
    await openEWayBill(app);
    // An operator who reads "this is not a government e-way bill" only on the
    // result panel has already decided to print by then.
    expect(screen.getByRole("note").textContent ?? "").toMatch(
      /nothing here is an EBN/i,
    );
  });
});

describe("raising a bill, over the real shell", () => {
  it("Ctrl+A raises it and the DISCLOSURES are rendered in full", async () => {
    const c = await bookedClient();
    const app = renderShell(c);
    await openEWayBill(app);

    fireEvent.click(screen.getByRole("radio"));
    fireEvent.change(screen.getByLabelText("Approximate distance (km)"), {
      target: { value: "120" },
    });
    fireEvent.change(screen.getByLabelText("Vehicle number (Part B)"), {
      target: { value: "KA01AB1234" },
    });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const raised = await screen.findByRole("region", {
      name: "The raised e-way bill",
    });
    expect(raised).toBeTruthy();

    // ⛔ THE DISCLOSURE BLOCK IS THE POINT OF THE ARTEFACT. Asserting the LIST
    // is present is not enough — an empty <ul> would satisfy that. Assert the
    // sentences that carry the honesty.
    const block = screen.getByRole("list", { name: "Disclosures" }).textContent ?? "";
    expect(block).toMatch(/NOT A GOVERNMENT-ISSUED/i);
    expect(block).toMatch(/SECONDARY/i);
  });

  it("⛔ the identifier is labelled a LOCAL REFERENCE and never an EBN", async () => {
    const c = await bookedClient();
    const app = renderShell(c);
    await openEWayBill(app);

    fireEvent.click(screen.getByRole("radio"));
    fireEvent.change(screen.getByLabelText("Approximate distance (km)"), {
      target: { value: "120" },
    });
    fireEvent.change(screen.getByLabelText("Vehicle number (Part B)"), {
      target: { value: "KA01AB1234" },
    });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const raised = await screen.findByRole("region", {
      name: "The raised e-way bill",
    });
    const text = raised.textContent ?? "";
    expect(text).toMatch(/Local reference/);
    // ⛔ THE ONLY PLACE THE PHRASE "e-Way Bill Number" MAY APPEAR ON THIS SCREEN
    // IS IN THE SENTENCE DENYING IT. A well-meaning relabel of the identifier
    // would put a government-looking claim on a document no government issued,
    // so the assertion counts occurrences rather than merely looking for the
    // denial: one, and it is the denial.
    const mentions = text.match(/e-Way Bill Number/gi) ?? [];
    expect(mentions).toHaveLength(1);
    expect(text).toMatch(/is NOT an e-Way Bill Number/i);
  });

  it("an unstated distance is REFUSED, and the reasoning is shown", async () => {
    const c = await bookedClient();
    const app = renderShell(c);
    await openEWayBill(app);

    fireEvent.click(screen.getByRole("radio"));
    // Distance deliberately left blank — the field starts empty and stays that
    // way, which is the case an operator hits by simply not knowing it yet.
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await waitFor(() => {
      expect(screen.getByRole("alert").textContent ?? "").toMatch(
        /validity period cannot be computed/i,
      );
    });
    // And nothing was raised.
    expect(
      screen.queryByRole("region", { name: "The raised e-way bill" }),
    ).toBeNull();
  });

  it("⛔ a missing vehicle raises a PART A slip and warns, rather than refusing", async () => {
    const c = await bookedClient();
    const app = renderShell(c);
    await openEWayBill(app);

    fireEvent.click(screen.getByRole("radio"));
    fireEvent.change(screen.getByLabelText("Approximate distance (km)"), {
      target: { value: "120" },
    });
    // No vehicle. This is the case the naive screen refuses.
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const raised = await screen.findByRole("region", {
      name: "The raised e-way bill",
    });
    expect(raised).toBeTruthy();
    // The warning must be present and must say the goods may not move.
    await waitFor(() => {
      const alerts = screen.getAllByRole("alert");
      expect(alerts.some((a) => /may not move/i.test(a.textContent ?? ""))).toBe(
        true,
      );
    });
  });
});
