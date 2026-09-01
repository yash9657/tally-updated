import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
  act,
} from "@testing-library/react";
import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api";
import type { CoredClient, VoucherResponse } from "../api";
import {
  _resetOutputTaxLedgerCache,
  _resetLedgerCache,
} from "./vouchers/gstVoucher";

afterEach(cleanup);
beforeEach(() => {
  _resetOutputTaxLedgerCache();
  _resetLedgerCache();
});

/**
 * ===========================================================================
 * ⛔⛔ D107 — THE WORD "Saved" WAS A FACT ABOUT A KEYPRESS, NOT ABOUT THE BOOKS.
 * ===========================================================================
 *
 * The header rendered `status ? status : snapshot.saved ? "Saved" : null`, and
 * `snapshot.saved` is the MACHINE's flag. Its own declaration says what it
 * means — *"True once this voucher frame has been accepted at least once
 * (Ctrl+A)"* (`navMachine.ts:632`) — and it is set by `fresh.saved = true` in
 * the accept reducer, which is PURE and therefore can never learn whether the
 * write succeeded. It cannot even learn whether one was ATTEMPTED: a refusal
 * this screen makes client-side sends nothing at all.
 *
 * ⛔ THE DANGEROUS CASE IS THE ONE THAT DOES NOT SETTLE. Keying a voucher that
 * needs WebAuthn step-up leaves `status` null for as long as the passkey prompt
 * is pending, so the screen sat reading **`Saved`** beside *"Confirm the
 * passkey prompt"*. Measured 2026-08-31: POST /vouchers/receipt -> 403, nothing
 * written, and the word `Saved` on screen the whole time. An operator who keys
 * the day's receipts and confirms none of the prompts is told every one of them
 * saved. That is the shape `hangs` reproduces below, deterministically.
 *
 * ⭐ THE FIX IS NOT TO INVENT A FACT — IT IS TO RENDER THE ONE THAT ALREADY
 * EXISTS. `savedNo` / `savedVoucherID` are set ONLY from a `VoucherResponse`,
 * so a header built on them cannot say "Saved" about a voucher the server does
 * not have. The fallback branch existed so a frame reset after a REAL save
 * still read "Saved" once `status` cleared; `savedNo` carries that same fact
 * honestly, which is why the branch is redundant rather than load-bearing.
 *
 * ⚠️ `ScreenFrame.saved` ITSELF IS PINNED AND STAYS — `keystroke.test.ts:213`
 * and `corpora/keystroke/voucher-ctrl-a-accepts.json` both want it. It is a
 * legitimate MACHINE fact. What changed is that the SCREEN stopped rendering it
 * as a claim about the books.
 */

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

async function seedParty(base: CoredClient) {
  await base.createLedger("co-1", {
    name: "Acme Traders",
    group_name: "Sundry Debtors",
    state_code: "29",
  });
}

/** Key an accounting-mode GST sale, up to but not including Ctrl+A. */
async function keySale(app: HTMLElement) {
  fireEvent.keyDown(app, { key: "F8" });
  fireEvent.mouseDown(await screen.findByText("Acme Traders"));
  fireEvent.change(screen.getByTestId("gst-taxable"), {
    target: { value: "10000" },
  });
  fireEvent.change(screen.getByTestId("gst-rate"), { target: { value: "18" } });
  fireEvent.change(screen.getByTestId("gst-hsn"), { target: { value: "7326" } });
}

/** Drain the accept chain — see the note in VoucherScreen.gstAlter.test.tsx. */
async function settle() {
  for (let i = 0; i < 3; i++) {
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

describe("⛔ D107: the header may only say Saved about a voucher the server has", () => {
  it("a Ctrl+A the screen is about to REFUSE never claims Saved", async () => {
    // ⭐ THE REGISTER'S MINIMAL REPRO, and it needs no network at all: an EMPTY
    // Purchase voucher. `gstRefusal` answers "Party A/c Name is required" and
    // not one byte leaves the browser — but the machine's reducer has already
    // run, synchronously, off the same keypress.
    const base = createMockClient({ seed: false });
    const { client } = spyOn(base);
    renderShell(client);
    const app = screen.getByRole("application");

    fireEvent.keyDown(app, { key: "F9" }); // purchase
    await screen.findByTestId("gst-taxable");
    expect(screen.queryByText("Saved")).toBeNull();

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    // ⛔ SYNCHRONOUSLY, BEFORE THE REFUSAL ARRIVES. This is the render the
    // operator sees: `status` is still null and `snapshot.saved` is already
    // true. The header went `Purchase Voucher` -> **Saved** -> `Error: …`, and
    // the middle frame is the lie.
    expect(screen.queryByText("Saved")).toBeNull();

    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/is required/),
    );
    expect(screen.queryByTestId("voucher-no")).toBeNull();
  });

  it("⛔⛔ a write still IN FLIGHT never claims Saved — the step-up shape", async () => {
    // The durable case. A POST that never resolves stands in for a pending
    // WebAuthn ceremony: `status` stays null for as long as the operator has
    // not confirmed the passkey, and that is precisely when the old header had
    // nothing left to render but the machine's flag.
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const hangs = vi.fn(() => new Promise<VoucherResponse>(() => {}));
    const client = {
      ...base,
      postSalesVoucher: hangs,
    } as unknown as CoredClient;
    renderShell(client);
    const app = screen.getByRole("application");

    await keySale(app);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await settle();

    // The request really is out and really has not answered.
    expect(hangs).toHaveBeenCalledTimes(1);
    // ⛔ AND THE SCREEN SAYS NOTHING ABOUT THE BOOKS.
    expect(screen.queryByText("Saved")).toBeNull();
    expect(screen.queryByTestId("voucher-no")).toBeNull();
    // ⚠️ AND THE KEYING IS STILL THERE, which is the other half of being
    // honest: nothing was saved, so nothing is cleared. D81 established that
    // keeping the operator's keying through a refusal is correct.
    expect(
      (screen.getByTestId("gst-taxable") as HTMLInputElement).value,
    ).toBe("10000");
  });

  it("a SAVED voucher still confirms itself — Saved and the server's number, over a blank form", async () => {
    // ⛔ THE CONTROL FOR THE FIX. Deleting the fallback branch must not leave an
    // operator with LESS confirmation than the defect gave them: the header has
    // to keep saying so, from `status` and `savedNo`, which are set only from a
    // VoucherResponse. Together with the blank form below this is the whole of
    // §5.2 — a true confirmation over a form that is ready for the next one.
    const base = createMockClient({ seed: false });
    await seedParty(base);
    const { client, spies } = spyOn(base);
    renderShell(client);
    const app = screen.getByRole("application");

    await keySale(app);
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await waitFor(() =>
      expect(spies.postSalesVoucher).toHaveBeenCalledTimes(1),
    );

    await waitFor(() => expect(screen.getByText("Saved")).toBeTruthy());
    expect(screen.getByTestId("voucher-no").textContent).toMatch(/No: \S+/);
    expect(
      (screen.getByTestId("gst-taxable") as HTMLInputElement).value,
    ).toBe("0.0000");
    // ⚠️ AND THE "No." FIELD ON THE FORM READS `auto`, NOT THE NUMBER JUST
    // ASSIGNED. It names the voucher BEING KEYED, and that one has no number
    // yet; the number of the one just saved is the header's job. It used to
    // fall back to `savedNo`, which over a cleared form would have shown the
    // operator the previous invoice's number while they keyed the next.
    expect(screen.getByTestId("voucher-no-field").textContent).toBe("auto");
  });
});

/** Spy every method so calls can be counted without changing behaviour. */
function spyOn(base: CoredClient): {
  client: CoredClient;
  spies: Record<string, ReturnType<typeof vi.fn>>;
} {
  const spies: Record<string, ReturnType<typeof vi.fn>> = {};
  const client = {} as Record<string, unknown>;
  for (const key of Object.keys(base) as (keyof CoredClient)[]) {
    const orig = base[key] as unknown as (...a: unknown[]) => unknown;
    const fn = vi.fn((...args: unknown[]) => orig.apply(base, args));
    spies[key] = fn;
    client[key] = fn;
  }
  return { client: client as unknown as CoredClient, spies };
}
