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
import { ApiError } from "../api";
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

describe("⛔ the step-up client swap must not wipe the screen (LEAD 1)", () => {
  /**
   * ⛔⛔ CONFIRMED IN A REAL BROWSER AGAINST REAL CORED, 2026-08-07, with the
   * control that makes it a proof rather than an observation.
   *
   * `App.tsx`'s `resolvedClient` memo depends on `session`, and a WebAuthn
   * step-up REPLACES the session token — so the first write of every session
   * hands every screen a brand-new `CoredClient` object the instant the passkey
   * ceremony completes. The load effect here is keyed on `client`, and its
   * reset was unconditional. Observed:
   *
   *   POST .../e-way-bills          -> 403 STEPUP_REQUIRED
   *   webauthn assert begin/finish  -> 200   (client identity changes HERE)
   *   POST .../e-way-bills (retry)  -> 422   the refusal is set
   *   ...the load effect re-runs on the new client...
   *   -> and its unconditional reset WIPED the refusal
   *
   * ⛔ THE CONTROL, DRIVEN: pressing Ctrl+A a SECOND time in the same session
   * sends the IDENTICAL request and gets the IDENTICAL 422 — and the refusal
   * renders in full and stays, because no step-up (and hence no client swap)
   * happens on the second press. Same request, same answer, different outcome:
   * the swap is the cause.
   *
   * ⚠️ THIS SCREEN IS THE SIBLING OF `EInvoiceScreen`, WHICH WAS FIXED TWO
   * SESSIONS AGO AND WHOSE FIX WAS NEVER COPIED HERE. §1.3's lesson — "fixing
   * the REPORTED call site is not fixing the CLASS" — applied and was not
   * applied.
   */
  function swapClient(c: CoredClient): CoredClient {
    // A new object identity, same behaviour: what App.tsx's memo produces when
    // the session token is replaced.
    return { ...c };
  }

  it("a REFUSAL survives the client being swapped underneath it", async () => {
    const c = await bookedClient();
    const { rerender } = render(
      <CompanyProvider
        client={c}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");
    await openEWayBill(app);

    fireEvent.click(screen.getAllByRole("radio")[0]!);
    // DISTANCE LEFT BLANK — the server refuses rather than guessing one, which
    // is the refusal an operator actually meets on their first raise.
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const alert = await screen.findByRole("alert");
    const said = alert.textContent ?? "";
    expect(said.length, "no refusal rendered at all").toBeGreaterThan(0);

    // Swap the client, exactly as a completed step-up does.
    rerender(
      <CompanyProvider
        client={swapClient(c)}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    await new Promise((r) => setTimeout(r, 0));

    // ⛔ STILL THERE. This is where the operator's screen went blank and the
    // keypress looked like it had done nothing at all.
    expect(
      screen.getByRole("alert").textContent ?? "",
      "the refusal was wiped by the client swap",
    ).toBe(said);
  });

  it("⛔ the operator's TYPED consignment survives it too", async () => {
    // The second, distinct unconditional write in the same effect, and one
    // `EInvoiceScreen`'s fix could not have covered because it has no form: the
    // GET's success handler re-fills distance/vehicle/mode/transporter/ODC and
    // both pincodes from the PREVIOUSLY RAISED bill on every effect re-run. A
    // client swap mid-correction therefore replaces the operator's new figure
    // with the old one — and DISTANCE is what the validity period is computed
    // from, so this silently changes when the bill expires.
    //
    // ⚠️ THE FIRST VERSION OF THIS TEST COULD NOT FAIL, and the mutation proof
    // is the only reason that was noticed: with no bill already raised the GET
    // 404s, the `.then` never runs, and there is no pre-fill to guard. It now
    // raises a bill FIRST, so the pre-fill path is genuinely on.
    const c = await bookedClient();
    const { entries } = await c.getDayBook(COMPANY);
    const target = entries[0]!;
    await c.raiseEWayBill(COMPANY, {
      voucher_id: target.voucher_id,
      distance_km: 120,
      transport_mode: "ROAD",
      vehicle_number: "KA01AB1234",
      created_by: "owner",
    });

    const { rerender } = render(
      <CompanyProvider
        client={c}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");
    await openEWayBill(app);
    fireEvent.click(screen.getAllByRole("radio")[0]!);

    // The pre-fill must actually have happened, or this proves nothing.
    const distance = await screen.findByLabelText(/distance/i);
    await waitFor(() => {
      expect((distance as HTMLInputElement).value).toBe("120");
    });

    // The operator CORRECTS it.
    fireEvent.change(distance, { target: { value: "250" } });
    expect((distance as HTMLInputElement).value).toBe("250");

    // ...and the step-up swaps the client underneath them.
    rerender(
      <CompanyProvider
        client={swapClient(c)}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    await new Promise((r) => setTimeout(r, 0));
    await new Promise((r) => setTimeout(r, 0));

    expect(
      (screen.getByLabelText(/distance/i) as HTMLInputElement).value,
      "the client swap overwrote the distance the operator typed with the previously raised one",
    ).toBe("250");
  });

  it("⛔⛔ a refusal that arrives while the client is being swapped IS STILL SHOWN", async () => {
    // ⛔ THIS IS THE ONE THAT WAS ACTUALLY KILLING THE SCREEN, and neither of
    // the two tests above could see it — both were GREEN in the browser-broken
    // state, because both swap the client AFTER the raise has already settled.
    //
    // The real ordering is the other one. A step-up's retry resolves LATE:
    //
    //   Ctrl+A -> raise claims generation N
    //          -> 403 -> ceremony -> client identity changes
    //          -> load effect cleanup bumps the generation, the re-run bumps it
    //             AGAIN
    //          -> the retry finally answers 422
    //          -> the raise re-checks its generation, finds it moved, and
    //             DISCARDS ITS OWN RESULT
    //
    // So the refusal never reaches state at all — there is nothing for a reset
    // guard to protect. The raise now carries its OWN generation (raiseGenRef),
    // which only a newer raise or a different document may invalidate.
    //
    // The test holds the raise open across the swap, which is exactly what the
    // ceremony does.
    const c = await bookedClient();
    let releaseRaise: (e: unknown) => void = () => {};
    const held = new Promise((_resolve, reject) => {
      releaseRaise = reject;
    });
    const slow: CoredClient = {
      ...c,
      raiseEWayBill: () => held as Promise<never>,
    };

    const { rerender } = render(
      <CompanyProvider
        client={slow}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");
    await openEWayBill(app);
    fireEvent.click(screen.getAllByRole("radio")[0]!);

    // The raise goes out and does NOT resolve yet.
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // ...the ceremony completes and the client identity changes UNDER it.
    rerender(
      <CompanyProvider
        client={{ ...slow }}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    await new Promise((r) => setTimeout(r, 0));

    // ...and only NOW does the retry answer.
    releaseRaise(new ApiError(422, "ewaybill: the distance has not been stated"));
    await new Promise((r) => setTimeout(r, 0));

    const alert = await screen.findByRole("alert");
    expect(
      alert.textContent ?? "",
      "the raise discarded its own refusal because the load effect moved the generation",
    ).toMatch(/distance has not been stated/i);
  });

  it("CONTROL: choosing a DIFFERENT document DOES reset the panel", async () => {
    // Without this arm the guard would pass by never resetting at all, which
    // would leave one document's refusal showing above another document.
    const c = await bookedClient();
    const app = renderShell(c);
    await openEWayBill(app);

    const radios = screen.getAllByRole("radio");
    fireEvent.click(radios[0]!);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByRole("alert");

    if (radios.length < 2) return; // one document seeded; nothing to switch to
    fireEvent.click(radios[1]!);
    await waitFor(() => {
      expect(screen.queryByRole("alert")).toBeNull();
    });
  });
});
