import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api/mock";
import { ApiError } from "../api";
import type { CoredClient } from "../api";

/**
 * THE e-INVOICE / IRN (§1.4 B) — and specifically, whether an OPERATOR can
 * raise one.
 *
 * ⛔⛔ WHY EVERY TEST IN THIS FILE DRIVES THE REAL SHELL AND PRESSES THE REAL
 * KEY. This is the sixth track in this repository to ship a complete, tested,
 * adversarially-reviewed BACKEND before anyone noticed no human could reach it —
 * the statutoryd workflow, the cored proxy, the line-item builder and the freeze
 * hook were all green and browser-unreachable, and the F11 row read `server`.
 * So this file asserts the thing every one of those was missing:
 *
 *   A FEATURE IS NOT SHIPPED WHEN ITS TESTS PASS. IT IS SHIPPED WHEN A HUMAN CAN
 *   REACH IT.
 *
 * ⛔ THE MUTATION PROOFS, WHICH WERE RUN AND ARE WHAT MAKE THIS FILE A GUARD
 * RATHER THAN A DESCRIPTION (HANDOFF §9.2):
 *
 *   - delete `"Alt+I": { kind: "eInvoice" }` from keymaps/tallyprime.ts and all
 *     8 tests below fail;
 *   - delete `case "eInvoice"` from navMachine.ts and all 8 fail;
 *   - delete `"e-invoice": EInvoiceScreen` from defaultScreens.tsx and 7 fail.
 *
 *   - delete `"e-invoice"` from `COMPANY_SCOPED_SCREENS` in AppShell.tsx and
 *     exactly 1 fails — "with NO company open", which was written FOR it.
 *
 * ⚠️ AND THAT LAST ONE IS RECORDED BECAUSE IT FIRST FAILED TO FAIL. The claim
 * was in this header before the mutation was run; when it was run, all tests
 * stayed GREEN, because every other test supplies an `initialCompany` and the
 * guard sits only on the no-company path. The claim was false. Rather than
 * dropping it, the test that actually walks that path was written, and the
 * mutation re-run against it. A mutation proof that is asserted rather than
 * executed is worth less than none, because it is read as evidence.
 *
 * ⚠️ The 2 tests that survive the first three mutations are the pure-client
 * ones (the freeze, and the purchase refusal). They SHOULD survive: they assert
 * the domain's behaviour, not the shell's routing, and a key deletion has no
 * business breaking them.
 *
 * Ctrl+A rides on the PRE-EXISTING `accept` entry in SCREEN_ACTIONS, already
 * mutation-proved elsewhere, which is why it is not mutated here.
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

/** A company holding ONE GST sales invoice with an item line. */
async function bookedClient() {
  const c = createMockClient({ seed: false });
  const { company_id } = await c.createCompany({
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
        qty: "10.0000",
        rate: "500.0000",
      },
    ],
  });
  return { c, company_id, party, item };
}

/**
 * The one voucher in the fixture. A helper rather than `entries[0].voucher_id`
 * because `noUncheckedIndexedAccess` types that as possibly-undefined, and a
 * `!` there would silently pass a literal "undefined" to the client if the
 * fixture ever stopped posting.
 */
function firstVoucherID(book: { entries: { voucher_id: string }[] }): string {
  const first = book.entries[0];
  if (!first) throw new Error("fixture posted no voucher");
  return first.voucher_id;
}

/** Press Alt+I and wait for the screen to be up. */
async function openEInvoice(app: HTMLElement) {
  fireEvent.keyDown(app, { key: "i", altKey: true });
  await screen.findByRole("region", { name: "e-Invoice" });
}

/** Select the only listed invoice and press Ctrl+A. */
async function report(app: HTMLElement) {
  fireEvent.click(screen.getByRole("radio"));
  fireEvent.keyDown(app, { key: "a", ctrlKey: true });
  return screen.findByRole("region", { name: "The reported e-invoice" });
}

describe("a screen-owned key is actually ROUTED by the shell", () => {
  it("⛔ Alt+I opens the e-invoice screen — the panel is ABSENT as a control", async () => {
    const { c } = await bookedClient();
    const app = renderShell(c);

    // THE CONTROL. Without it, a screen that was somehow always mounted would
    // make the assertion below pass while Alt+I did nothing at all.
    expect(screen.queryByRole("region", { name: "e-Invoice" })).toBeNull();

    await openEInvoice(app);
    expect(screen.getByRole("region", { name: "e-Invoice" })).toBeTruthy();
  });

  it("⛔ with NO company open, Alt+I degrades instead of THROWING", async () => {
    // ⛔⛔ THIS TEST EXISTS BECAUSE ITS MUTATION PROOF FAILED TO FAIL.
    //
    // The first draft of this file claimed that deleting `"e-invoice"` from
    // COMPANY_SCOPED_SCREENS broke the suite. It was RUN, and it did not: every
    // other test here supplies an `initialCompany`, so the guard was never on
    // the path they take. The claim was removed and this test written, because a
    // mutation proof that was asserted rather than executed is exactly the kind
    // of certification HANDOFF §9.2 exists to forbid.
    //
    // The guard is real: `EInvoiceScreen` calls `useCompanyID()`, which THROWS
    // when no company is open. Without the registration the screen mounts
    // anyway and the throw escapes — the operator gets a blank crash instead of
    // the "select a company" state. `edit-log` is missing from that set today
    // for the same reason nobody noticed: its only door happens to be
    // company-scoped already.
    render(
      <CompanyProvider client={createMockClient({ seed: false })}>
        <AppShell />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "i", altKey: true });

    // It must NOT have thrown, and it must NOT have rendered the screen.
    expect(screen.queryByRole("region", { name: "e-Invoice" })).toBeNull();
    // ⛔ NON-VACUITY: the key genuinely ROUTED — the chrome swapped to this
    // screen's button bar and hint strip, both of which name a key no other
    // screen advertises. Without this the assertion above would also pass on a
    // completely dead Alt+I, which is the failure it is meant to exclude.
    expect(document.body.textContent ?? "").toMatch(/Report the invoice \(FREEZES it\)/);
    // And what the operator gets instead of a crash.
    expect(document.body.textContent ?? "").toMatch(/Preparing company/i);
  });

  it("⛔⛔ the FREEZE is stated BEFORE the key is pressed, not on the result", async () => {
    const { c } = await bookedClient();
    const app = renderShell(c);
    await openEInvoice(app);

    // The whole argument of this screen. An operator who learns that Ctrl+A is
    // irreversible from the RESULT panel has already frozen the invoice, and
    // there is no undo anywhere in the product.
    const notes = screen
      .getAllByRole("note")
      .map((n) => n.textContent ?? "")
      .join(" ");
    expect(notes).toMatch(/FREEZES this voucher/i);
    expect(notes).toMatch(/cannot be altered/i);
    expect(notes).toMatch(/no undo/i);
    // And the standing disclosure, also before anything is raised.
    expect(notes).toMatch(/nothing here is a government-issued IRN/i);

    // ⛔ NON-VACUITY: the result panel is NOT up, so the sentences above are
    // genuinely on the pre-raise form and not leaking from a rendered result.
    expect(
      screen.queryByRole("region", { name: "The reported e-invoice" }),
    ).toBeNull();
  });
});

describe("reporting an invoice, over the real shell", () => {
  it("Ctrl+A reports it and the reference is NOT labelled an IRN", async () => {
    const { c } = await bookedClient();
    const app = renderShell(c);
    await openEInvoice(app);
    const panel = await report(app);

    expect(panel.textContent ?? "").toMatch(/NOT a government-issued IRN/i);
    // The <dt> says "Reference". A label promising an IRN is how a locally
    // minted string gets filed as a government one.
    expect(panel.textContent ?? "").toMatch(/Reference/);
  });

  it("⛔ the FREEZE and the PINNED NUMBER are on the result, as an alert", async () => {
    const { c } = await bookedClient();
    const app = renderShell(c);
    await openEInvoice(app);
    await report(app);

    // The handler returns pinned_voucher_no precisely so this can be shown: a
    // screen that omits it leaves the operator to discover the freeze by being
    // refused an edit later, with nothing connecting the refusal to this action.
    const alert = screen.getByRole("alert").textContent ?? "";
    expect(alert).toMatch(/FROZEN/i);
    expect(alert).toMatch(/credit note/i);
  });

  it("⛔ the ACK NUMBER is shown, and is disclosed as UNSTORED", async () => {
    const { c } = await bookedClient();
    const app = renderShell(c);
    await openEInvoice(app);
    const panel = await report(app);

    // It exists on the raise and NOWHERE ELSE — voucher_external_reference has
    // no column for it. A screen that does not say so lets an operator navigate
    // away from the only copy.
    expect(panel.textContent ?? "").toMatch(/Acknowledgement number/i);
    expect(panel.textContent ?? "").toMatch(/is not stored/i);
  });
});

describe("⛔ the step-up client swap must not wipe the screen", () => {
  /**
   * ⛔⛔ FOUND IN A BROWSER AGAINST REAL CORED, NOT BY REVIEW, AND NOT BY ANY
   * TEST IN THIS FILE — ALL TEN WERE GREEN WHILE IT WAS LIVE.
   *
   * `App.tsx`'s `resolvedClient` memo depends on `session`, and a WebAuthn
   * step-up REPLACES the session's token. So the first write of every session
   * hands every screen a brand-new `CoredClient` object the instant the passkey
   * ceremony completes. Observed sequence:
   *
   *   POST .../e-invoices          -> 403 STEPUP_REQUIRED
   *   webauthn assert begin/finish -> 200   (client identity changes HERE)
   *   POST .../e-invoices (retry)  -> 422   the refusal is set
   *   ...the load effect re-runs on the new client...
   *   -> and its unconditional reset WIPED the refusal
   *
   * The operator pressed Ctrl+A and saw nothing at all happen. It reproduces on
   * the FIRST raise of a session — the only one most operators ever perform.
   *
   * These two tests re-render with a NEW client object carrying the SAME data,
   * which is exactly what the step-up does. Both FAIL without the
   * `shownForRef`/`selectionChanged` guard in EInvoiceScreen.tsx — verified by
   * reverting it.
   */
  function swapClient(c: CoredClient): CoredClient {
    // A new object identity, same behaviour — what App.tsx's memo produces when
    // the session token is replaced.
    return { ...c };
  }

  it("a REFUSAL survives the client being swapped underneath it", async () => {
    // ⚠️ AN ACCOUNTING-MODE SALE — a GST invoice with NO item lines. This is
    // precisely the document the browser drive hit: the demo company's Sales 1
    // is accounting-mode, and real cored refused it with ErrLineNotDescribable
    // ("the books record an HSN/SAC, a value and a rate for each line but no
    // description, quantity or unit, and every NIC item needs one"). So the
    // refusal this test puts on screen is the one an operator actually meets.
    const c = createMockClient({ seed: false });
    const { company_id } = await c.createCompany({
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
    await c.postSalesVoucher(company_id, {
      voucher_date: "2026-06-10",
      party_ledger_id: party,
      item_ledger_id: party,
      gst: {
        place_of_supply_state_code: "29",
        lines: [{ taxable_value: "10000.0000", gst_rate_pct: "18", hsn: "7216" }],
      },
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
    fireEvent.keyDown(app, { key: "i", altKey: true });
    await screen.findByRole("region", { name: "e-Invoice" });

    fireEvent.click(screen.getByRole("radio"));
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const alert = await screen.findByRole("alert");
    expect(alert.textContent ?? "").toMatch(/no item lines/i);
    // The non-retryable advice, which is half the value of showing it at all.
    expect(alert.textContent ?? "").toMatch(/has NOT been frozen/i);

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

    // ⛔ STILL THERE. Without the guard this is where the operator's screen went
    // blank and the keypress looked like it had done nothing at all.
    expect(screen.getByRole("alert").textContent ?? "").toMatch(
      /no item lines/i,
    );
  });

  it("⛔⛔ the ACK NUMBER survives the client being swapped underneath it", async () => {
    // The success-path version, and the one that loses data that exists NOWHERE
    // ELSE: `voucher_external_reference` has no column for the ack number, so a
    // wipe here is unrecoverable rather than merely confusing.
    const { c } = await bookedClient();
    const { rerender } = render(
      <CompanyProvider
        client={c}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "i", altKey: true });
    await screen.findByRole("region", { name: "e-Invoice" });
    fireEvent.click(screen.getByRole("radio"));
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    const panel = await screen.findByRole("region", {
      name: "The reported e-invoice",
    });
    expect(panel.textContent ?? "").toMatch(/Acknowledgement number/i);

    rerender(
      <CompanyProvider
        client={swapClient(c)}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    await new Promise((r) => setTimeout(r, 0));

    // ⛔ STILL THERE. Without the guard the load effect re-runs, clears
    // `raised`, the GET repopulates `pinned` — and the panel silently downgrades
    // to "reported earlier, no acknowledgement number is shown".
    const after = screen.getByRole("region", {
      name: "The reported e-invoice",
    });
    expect(after.textContent ?? "").toMatch(/Acknowledgement number/i);
    expect(after.textContent ?? "").not.toMatch(/reported earlier/i);
  });
});

describe("the two properties a demo cannot show", () => {
  it("⛔⛔ a RE-POST returns the SAME IRN — it never mints a second", async () => {
    const { c, company_id } = await bookedClient();
    const app = renderShell(c);
    await openEInvoice(app);
    const panel = await report(app);
    const voucherID = firstVoucherID(await c.getDayBook(company_id));

    // ⚠️ THE REFERENCE IS READ OFF THE STORE AND THEN LOCATED IN THE PANEL, not
    // scraped out of the panel with a regex. The first draft of this test did
    // scrape it — `/[0-9a-f]{32}/` over `panel.textContent` — and matched two
    // characters early, because the <dt> immediately before it reads
    // "Referen-CE" and `ce` is valid hex. It failed against a CORRECT
    // implementation. An oracle that can misread the value it is checking is
    // exactly the shape HANDOFF §1.10 records twice; the fix is to compare
    // against the source of truth and use the DOM only to prove it is displayed.
    const first = (await c.getEInvoice(company_id, voucherID)).irn;
    expect(first).toMatch(/^[0-9a-f]{32}$/);
    expect(panel.textContent ?? "").toContain(first);

    // This is the one property whose failure mints a DUPLICATE against a real
    // government API, and it is the entire reason statutoryd's workflow is
    // durable and its `generate` step memoized.
    const again = await c.raiseEInvoice(company_id, { voucher_id: voucherID });
    expect(again.irn).toBe(first);
  });

  it("⛔⛔ the reported voucher is FROZEN: alter and delete are both refused", async () => {
    const { c, company_id, party } = await bookedClient();
    const voucherID = firstVoucherID(await c.getDayBook(company_id));
    await c.raiseEInvoice(company_id, { voucher_id: voucherID });

    // ⛔ THE CONSEQUENCE, NOT THE ARTEFACT. This is what makes Ctrl+A the only
    // irreversible key in the product, and it is why the screen warns above the
    // key rather than below it.
    await expect(
      c.alterSalesVoucher(company_id, voucherID, {
        voucher_date: "2026-06-10",
        party_ledger_id: party,
        narration: "a narration edit, which is the most innocent alter there is",
      }),
    ).rejects.toMatchObject({ status: 409 });

    await expect(c.deleteVoucher(company_id, voucherID)).rejects.toMatchObject({
      status: 409,
    });
  });
});

describe("the refusals, which the mock must make because the server does", () => {
  it("⛔ a PURCHASE is refused BY NAME — it is not an outward document", async () => {
    const { c, company_id, party, item } = await bookedClient();
    await c.postPurchaseVoucher(company_id, {
      voucher_date: "2026-06-11",
      party_ledger_id: party,
      item_ledger_id: party,
      gst: { place_of_supply_state_code: "29" },
      inventory: [
        {
          stock_item_id: item,
          direction: "IN",
          qty: "10.0000",
          rate: "400.0000",
        },
      ],
    });
    const purchase = (await c.getDayBook(company_id)).entries.find(
      (e) => e.class === "Purchase",
    );
    expect(purchase).toBeTruthy();

    // This shipped as a 201 and FROZE the purchase forever: the document
    // asserted the company SOLD the goods to its own supplier, and the freeze
    // then made a legitimate purchase record un-alterable with no undo.
    const err = await c
      .raiseEInvoice(company_id, { voucher_id: purchase!.voucher_id })
      .then(
        () => null,
        (e: unknown) => e,
      );
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(422);
    // ⛔ ASSERT THE WORDING, not just the status. A bare 422 passes on a message
    // that reads "your purchase is malformed", which sends the accountant
    // editing a perfectly good purchase.
    expect((err as ApiError).message).toMatch(/SOLD the goods to its own supplier/i);
    expect((err as ApiError).message).toMatch(/CRN\/DBN/);

    // ⛔ AND THE PURCHASE IS STILL DELETABLE — the refusal froze nothing.
    await expect(
      c.deleteVoucher(company_id, purchase!.voucher_id),
    ).resolves.toBeTruthy();
  });

  it("the picker offers ONLY the sales invoice, never the purchase", async () => {
    const { c, company_id, party, item } = await bookedClient();
    await c.postPurchaseVoucher(company_id, {
      voucher_date: "2026-06-11",
      party_ledger_id: party,
      item_ledger_id: party,
      gst: { place_of_supply_state_code: "29" },
      inventory: [
        { stock_item_id: item, direction: "IN", qty: "1.0000", rate: "1.0000" },
      ],
    });
    const app = renderShell(c);
    await openEInvoice(app);

    // A CONVENIENCE filter, not the rule — the server's class gate is the
    // authority. But an operator should not be scrolling past documents that
    // can only ever be refused.
    const radios = screen.getAllByRole("radio");
    expect(radios).toHaveLength(1);
  });
});
