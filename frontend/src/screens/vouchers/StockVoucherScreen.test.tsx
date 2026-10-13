import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor, act } from "@testing-library/react";

import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import { createMockClient, ApiError } from "../../api";
import type { CoredClient } from "../../api";

/**
 * THE FOUR STOCK VOUCHER SCREENS, DRIVEN THROUGH THE REAL SHELL — the two keys
 * that were answering the wrong thing on them.
 *
 * ⛔ Ctrl+P. VOUCHER_BAR advertises "Ctrl+P Print" over every voucher screen and
 * printTarget declares the DELIVERY NOTE one of the six classes that produce a
 * document — but this component registered only `accept`, so the key fell
 * through to the shell's generic "does not apply on this screen" and made no
 * request at all. An operator despatching goods had a challan the server could
 * render and no way to ask for it.
 *
 * ⛔ THE 403. The PDP refuses a write for two unrelated reasons and both arrive
 * as 403: the level may not perform the act, or the level's BACK-DATED CUTOFF
 * closes the period the voucher is dated in. The second was being rendered as
 * the first — "ask an Owner to allow create physical-stock on your level" — so
 * an operator who already held that permission was sent to ask for it again and
 * was never told that a period is locked. The cure is the date field they are
 * looking at.
 *
 * Everything here is pressed on the DOCUMENT through AppShell, never on a
 * located element: two shipped bugs in this project hid behind a key delivered
 * straight to the node that handles it.
 */

afterEach(cleanup);

beforeEach(() => {
  // The default PDF sink calls window.open + createObjectURL; jsdom implements
  // neither usefully. Same stubbing VoucherScreen.print.test.tsx does.
  vi.stubGlobal("open", vi.fn(() => ({}) as Window));
  if (!URL.createObjectURL) {
    Object.defineProperty(URL, "createObjectURL", {
      value: vi.fn(() => "blob:x"),
      writable: true,
    });
    Object.defineProperty(URL, "revokeObjectURL", { value: vi.fn(), writable: true });
  } else {
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:x");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  }
});

/** The combos that open each stock voucher, per keymaps/tallyprime.ts. */
const OPEN_KEY = {
  "delivery-note": { key: "F8", altKey: true as const },
  "receipt-note": { key: "F9", altKey: true as const },
  "stock-journal": { key: "F7", altKey: true as const },
  "physical-stock": { key: "F10", altKey: true as const },
};

async function seedStock(base: CoredClient): Promise<void> {
  await base.createStockItem("co-1", {
    name: "Widget",
    unit_symbol: "Nos",
    hsn: "8481",
    gst_rate_pct: "18.0000",
  });
}

function renderShell(client: CoredClient) {
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <AppShell />
    </CompanyProvider>,
  );
}

const app = () => screen.getByRole("application");
const pressCtrlP = () => fireEvent.keyDown(app(), { key: "p", ctrlKey: true });

async function openStock(client: CoredClient, type: keyof typeof OPEN_KEY) {
  renderShell(client);
  fireEvent.keyDown(app(), OPEN_KEY[type]);
  await screen.findByTestId("stock-voucher-screen");
  await waitFor(() => expect(screen.getByTestId("stock-item:0")).toBeTruthy());
  // ⛔ A DELIVERY NOTE NOW REQUIRES A CONSIGNEE — see buildStockVoucherRequest.
  // Keyed in the HELPER because it is a precondition of keying this class at
  // all, and is the subject of no test in this file (they are about the print route and how a 403 is rendered).
  // Found by driving D30: printing refuses any printable voucher with no other
  // party, so a delivery note saved without one could never be printed.
  if (type === "delivery-note") {
    fireEvent.change(screen.getByTestId("stock-party"), {
      target: { value: "Acme Traders" },
    });
  }
}

/** Key row 0 and save it with Ctrl+A. */
async function keyAndSave(base: CoredClient): Promise<void> {
  const { stock_items } = await base.listStockItems("co-1");
  fireEvent.change(screen.getByTestId("stock-item:0"), {
    target: { value: stock_items[0]!.id },
  });
  fireEvent.change(screen.getByTestId("stock-qty:0"), { target: { value: "3" } });
  fireEvent.change(screen.getByTestId("stock-rate:0"), { target: { value: "100" } });
  fireEvent.keyDown(app(), { key: "a", ctrlKey: true });
}

function printSpy() {
  return vi.fn(
    async (_companyID: string, _voucherClass: string, _voucherID: string) => ({
      blob: new Blob(["%PDF-1.4"], { type: "application/pdf" }),
      filename: "Delivery-Note-DN-1.pdf",
    }),
  );
}

// ---------------------------------------------------------------------------
// 1. Ctrl+P on the Delivery Note — the class printTarget says prints
// ---------------------------------------------------------------------------

describe("Ctrl+P on a Delivery Note (the challan the goods travel on)", () => {
  it("prints the voucher it just saved, on the DELIVERY-NOTE route", async () => {
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const printVoucher = printSpy();
    const client = { ...base, printVoucher } as unknown as CoredClient;

    await openStock(client, "delivery-note");
    await keyAndSave(base);
    await waitFor(() => expect(screen.getByTestId("voucher-no")).toBeTruthy());

    pressCtrlP();
    await waitFor(() => expect(printVoucher).toHaveBeenCalled());
    // ⛔ THE CLASS AND THE ID, NOT MERELY "a call was made". The path segment is
    // also the ACL resource token and the server 404s a class mismatch with a
    // class-neutral body, so a request on the wrong route is indistinguishable
    // from a missing voucher — and would be reported to the operator as one.
    expect(printVoucher.mock.calls[0]![1]).toBe("delivery-note");
    expect(printVoucher.mock.calls[0]![2]).toBeTruthy();
  });

  it("refuses BEFORE saving, and says which key saves it — no request", async () => {
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const printVoucher = printSpy();
    const client = { ...base, printVoucher } as unknown as CoredClient;

    await openStock(client, "delivery-note");
    pressCtrlP();
    expect(await screen.findByText(/has not been saved/i)).toBeTruthy();
    // I-5: a refusal that still hits the network is a 404 in somebody's log.
    expect(printVoucher).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// 2. The other three classes ANSWER — a reason, not "the key does nothing here"
// ---------------------------------------------------------------------------

describe("Ctrl+P on the three classes that issue nothing", () => {
  const CASES = [
    ["receipt-note", /supplier/i],
    ["stock-journal", /Nothing leaves the business/i],
    ["physical-stock", /restates your own records/i],
  ] as const;

  for (const [type, reason] of CASES) {
    it(`${type} answers with its own reason and makes no request`, async () => {
      const base = createMockClient({ seed: false });
      await seedStock(base);
      const printVoucher = printSpy();
      const client = { ...base, printVoucher } as unknown as CoredClient;

      await openStock(client, type);
      pressCtrlP();
      // ⛔ READ OFF THE STATUS LINE, NOT OFF THE PAGE. The class hints on these
      // screens contain the same words as the refusals ("a supplier", "godown"),
      // so a findByText over the whole document passes on the SCREEN'S OWN
      // PROSE whether or not the key answered at all — which is exactly what
      // this test exists to detect. The status line is where the shell puts a
      // key's answer, so that is the only element that proves the key ran.
      const line = await screen.findByTestId("status-line");
      await waitFor(() => expect(line.textContent ?? "").toMatch(reason));
      expect(printVoucher).not.toHaveBeenCalled();
    });
  }

  it("never answers the shell's generic refusal on any of the four", async () => {
    for (const type of Object.keys(OPEN_KEY) as (keyof typeof OPEN_KEY)[]) {
      const base = createMockClient({ seed: false });
      await seedStock(base);
      await openStock(base, type);
      pressCtrlP();
      const line = await screen.findByTestId("status-line");
      expect(
        line.textContent ?? "",
        `${type} still falls through to the shell's generic answer`,
      ).not.toMatch(/does not apply|nothing on this screen/i);
      cleanup();
    }
  });
});

// ---------------------------------------------------------------------------
// 3. A 403 that is a CLOSED PERIOD is not a missing permission
// ---------------------------------------------------------------------------

describe("a back-dated-cutoff refusal is not rendered as an ACL refusal", () => {
  /**
   * The sentences are the SERVER'S OWN (internal/authz/authz.go). They are
   * matched on "back-dated cutoff", which every cutoff refusal carries and no
   * ACL denial does — a status code alone cannot tell the two apart, which is
   * why this used to be one branch.
   */
  const CUTOFF_SENTENCES = [
    // The DESTINATION end — the date the request itself carries.
    "voucher date is before the back-dated cutoff of 2026-07-01",
    // The STORED end — the date of the voucher being altered.
    "this voucher is dated 2026-05-04, inside the period closed by the back-dated cutoff of 2026-07-01",
    // The two FAIL-CLOSED sentinels, where a date was required and could not be
    // read at all. They are 403s carrying no date the operator typed, and they
    // are the shape most easily mistaken for a permission problem.
    "the voucher date could not be read, so the back-dated cutoff of 2026-07-01 cannot be cleared",
    "the stored date of the voucher being changed could not be read, so the back-dated cutoff of 2026-07-01 cannot be cleared",
    // ⛔ AND THE SUBJECTS THIS SCREEN CANNOT ITSELF RAISE, ON PURPOSE. The PDP
    // words the same refusal about a `bank date` or a `deposit date` when the
    // route is a bank-reconciliation tick or a challan allocation
    // (cutoffDateSubject). A stock voucher can produce neither — and that is
    // exactly why they belong here: the client's rule must key on the ONE phrase
    // every cutoff refusal carries and never on a sentence shape, or the next
    // route wired to a screen re-opens this defect with a wording nobody
    // enumerated. Rounds 1-4 of the cutoff fix each asserted a LIST and the next
    // bypass was always off-list.
    "bank date is before the back-dated cutoff of 2026-07-01",
    "this bank date of 2026-05-04 is inside the period closed by the back-dated cutoff of 2026-07-01",
    "deposit date is before the back-dated cutoff of 2026-07-01",
  ];

  for (const sentence of CUTOFF_SENTENCES) {
    it(`says the PERIOD is closed for: "${sentence.slice(0, 40)}…"`, async () => {
      const base = createMockClient({ seed: false });
      await seedStock(base);
      const client = {
        ...base,
        postDeliveryNoteVoucher: vi.fn(async () => {
          throw new ApiError(403, sentence);
        }),
      } as unknown as CoredClient;

      await openStock(client, "delivery-note");
      await keyAndSave(base);

      const denied = await screen.findByTestId("stock-denied");
      const text = denied.textContent ?? "";
      // The server's sentence survives verbatim — it names the cutoff DATE,
      // which is the one fact the operator needs to fix the voucher.
      expect(text).toContain(sentence);
      expect(text).toMatch(/CLOSED PERIOD/);
      // ⛔ AND THE ACL ADVICE IS GONE. Sending somebody to ask for a permission
      // they already hold is the whole defect: they act on it, the Owner grants
      // a rule that changes nothing, and the voucher still will not save.
      expect(text).not.toMatch(/allow "create delivery-note"/);
    });
  }

  it("still renders a REAL ACL refusal as one", async () => {
    // The control. Without it, "always say the period is closed" would pass.
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const client = {
      ...base,
      postDeliveryNoteVoucher: vi.fn(async () => {
        throw new ApiError(403, "create delivery-note is disallowed for level Data Entry");
      }),
    } as unknown as CoredClient;

    await openStock(client, "delivery-note");
    await keyAndSave(base);

    const denied = await screen.findByTestId("stock-denied");
    const text = denied.textContent ?? "";
    expect(text).toMatch(/allow "create delivery-note"/);
    expect(text).not.toMatch(/CLOSED PERIOD/);
  });
});

// ---------------------------------------------------------------------------
// 3. ⛔⛔ D108 — A SECOND Ctrl+A ON A SAVED STOCK VOUCHER MOVED THE GOODS AGAIN
// ---------------------------------------------------------------------------

/** Drain the accept chain — see the note in VoucherScreen.gstAlter.test.tsx. */
async function settle() {
  for (let i = 0; i < 3; i++) {
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

describe("⛔ D108: one keyed stock voucher is one stock voucher", () => {
  it("an UNCHANGED second Ctrl+A posts NOTHING, and the grid is ready for the next", async () => {
    // Driven in a browser 2026-08-31: Alt+F9, Steel Suppliers, MS Angle 100 @
    // 45, and a THIRD press on a screen already reading `Saved  No: 7` -> RN
    // No. 8, voucher count 24 -> 25. The mechanism is the accounting screen's:
    // `navMachine`'s accept reducer replaces the create frame with a fresh one,
    // but `lines` / `party` / `narration` / `voucherNo` are five separate
    // useStates inside this component and replacing the frame never remounted
    // it.
    //
    // ⚠️ THIS SCREEN ALREADY PROVED THE ASYMMETRY WAS AN OVERSIGHT: usePrintAction
    // reads `loadedVoucherID ?? savedID` while the accept handler reads only
    // `ctxRef.current.voucherID`, which is `loadedVoucherID` and therefore always
    // null on a create. The same fact was consulted for PRINTING and ignored for
    // SAVING.
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const postReceiptNoteVoucher = vi.fn((...a: unknown[]) =>
      (base.postReceiptNoteVoucher as unknown as (...x: unknown[]) => unknown)(...a),
    );
    const client = { ...base, postReceiptNoteVoucher } as unknown as CoredClient;

    await openStock(client, "receipt-note");
    await keyAndSave(base);
    await waitFor(() => expect(postReceiptNoteVoucher).toHaveBeenCalledTimes(1));
    // The receipt is on screen — the server's number, which is what must survive.
    expect(screen.getByTestId("voucher-no").textContent).toMatch(/No: \S+/);

    // ⛔⛔ THE PRESS. Nothing re-keyed.
    fireEvent.keyDown(app(), { key: "a", ctrlKey: true });
    await settle();
    expect(postReceiptNoteVoucher).toHaveBeenCalledTimes(1);

    // ---- AND HERE IS WHY: the grid is empty and the party is gone.
    expect((screen.getByTestId("stock-item:0") as HTMLSelectElement).value).toBe("");
    expect((screen.getByTestId("stock-qty:0") as HTMLInputElement).value).toBe("");
    expect((screen.getByTestId("stock-rate:0") as HTMLInputElement).value).toBe("");
    expect((screen.getByTestId("stock-party") as HTMLInputElement).value).toBe("");
    // ⚠️ THE "No." FIELD IS AN EDITABLE INPUT HERE, and it read
    // `savedNo ?? voucherNo` — so over a cleared form it would have shown the
    // operator the number of the voucher they had just saved while they keyed
    // the next, and (being controlled on `savedNo`) would have IGNORED anything
    // they typed into it.
    expect((screen.getByTestId("stock-voucher-no") as HTMLInputElement).value).toBe("");
  });

  it("⛔ CONTROL: a re-keyed second voucher still posts — bulk entry is not broken", async () => {
    // The fix must not become "never accept the same frame twice". Alt+F9 is
    // key-goods-Ctrl+A-key-the-next, exactly as F8 is.
    const base = createMockClient({ seed: false });
    await seedStock(base);
    const postReceiptNoteVoucher = vi.fn((...a: unknown[]) =>
      (base.postReceiptNoteVoucher as unknown as (...x: unknown[]) => unknown)(...a),
    );
    const client = { ...base, postReceiptNoteVoucher } as unknown as CoredClient;

    await openStock(client, "receipt-note");
    await keyAndSave(base);
    await waitFor(() => expect(postReceiptNoteVoucher).toHaveBeenCalledTimes(1));
    await keyAndSave(base);
    await waitFor(() => expect(postReceiptNoteVoucher).toHaveBeenCalledTimes(2));
  });
});
