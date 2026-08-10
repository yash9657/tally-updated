import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  act,
  waitFor,
} from "@testing-library/react";

import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type { CoredClient, CreateLedgerRequest } from "../../api";

/**
 * ⛔⛔ EVERY TEST IN THIS FILE DRIVES THE REAL SHELL AND PRESSES A REAL COMBO.
 *
 * That is not stylistic. The defects this file exists for are audit row 20 and
 * register rows D36 / D47, and ALL THREE are failures of REACHABILITY:
 *
 *  - row 20 — no keystroke anywhere reached `masterKind` "stock-item" or "unit",
 *    so a company could never be given a stock item or a unit from the keyboard.
 *    `MasterCreateScreen`'s two arms were dead code, and every existing test
 *    rendered the component DIRECTLY, which is why 1,074 green tests said
 *    nothing about it.
 *  - D36 — there was no keyboard path to an opening balance, an opening Dr/Cr, a
 *    bill-wise opening breakup or `maintain_bill_by_bill` on ANY ledger, while
 *    `masters.CreateLedger` had accepted all four all along.
 *  - D47 — a creation the server REFUSED was reported into a screen the machine
 *    had already unmounted, so `Ctrl+A` returned the operator to the Gateway with
 *    NO MESSAGE while `POST /ledgers` answered 403.
 *
 * A test that calls the component or the dispatcher directly cannot fail on any
 * of the three.
 *
 * ⛔⛔ AND THE Alt KEY IS PRESSED AS A MAC ACTUALLY SENDS IT.
 * `fireEvent.keyDown(app, { key: "g", altKey: true })` IS A LIE ON macOS: Option
 * COMPOSES, so a real Mac reports `key: "©"` for Option+G and never "g". EVERY
 * Alt+<letter> binding in this product was once found DEAD on macOS while every
 * browser test passed, because the tests pressed keys a Mac cannot send
 * (see `keystroke/normalize.ts`, which resolves the PHYSICAL `code` when Alt is
 * held). So `altMac()` below sends the composed character AND the code — the
 * event a human on this platform produces. Sending `key:"g"` here would certify
 * a door that does not open.
 */

afterEach(cleanup);

const COMPANY = "company-demo";

/** The event a real Mac sends for Option+<letter>: a COMPOSED key + the code. */
const MAC_COMPOSED: Readonly<Record<string, string>> = {
  g: "©",
  c: "ç",
};

function altMac(letter: string) {
  return {
    key: MAC_COMPOSED[letter] ?? letter,
    code: `Key${letter.toUpperCase()}`,
    altKey: true,
  };
}

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

/** Alt+G, pick `label`, Enter — the documented Go To route (RESEARCH.md:568). */
function goTo(app: HTMLElement, label: string) {
  fireEvent.keyDown(app, altMac("g"));
  fireEvent.mouseDown(screen.getByText(label));
  fireEvent.keyDown(app, { key: "Enter" });
}

function type(testId: string, value: string) {
  fireEvent.change(screen.getByTestId(testId), { target: { value } });
}

/** The status line is the SHELL's, so it survives the create screen unmounting. */
function statusLine(): string {
  return screen.getByTestId("status-line").textContent ?? "";
}

// ---------------------------------------------------------------------------
// ROW 20 — the door itself.
// ---------------------------------------------------------------------------
describe("⛔ row 20: Alt+G reaches every kind of master, not just a ledger", () => {
  it("Alt+G > Create Stock Item opens the STOCK ITEM form with a Units field", () => {
    const app = renderShell(createMockClient());
    // THE CONTROL: before the key, no create frame of any kind is up, and the
    // entry has to actually be in the picker — the literal
    // `["Vouchers", ...REPORT_NAMES]` is the whole of row 20.
    expect(screen.queryByTestId("master-create:stock-item")).toBeNull();

    fireEvent.keyDown(app, altMac("g"));
    expect(screen.getByText("Create Stock Item")).toBeTruthy();
    fireEvent.mouseDown(screen.getByText("Create Stock Item"));
    fireEvent.keyDown(app, { key: "Enter" });

    expect(screen.getByTestId("master-create:stock-item")).toBeTruthy();
    // ⛔ AND THE UNIT FIELD IS THE POINT. `PARENT_OPTIONS["stock-item"]` was `[]`,
    // so no second field rendered at all and the call sent `unit_symbol: p ||
    // "Nos"` — a Kg item was impossible.
    expect(screen.getByRole("combobox", { name: "Units" })).toBeTruthy();
  });

  it("Alt+G > Create Unit opens the UNIT form (symbol, not 'Name')", () => {
    const app = renderShell(createMockClient());
    expect(screen.queryByTestId("master-create:unit")).toBeNull();
    goTo(app, "Create Unit");
    expect(screen.getByTestId("master-create:unit")).toBeTruthy();
    expect(screen.getByLabelText("Symbol")).toBeTruthy();
    expect(screen.getByTestId("master-unit-decimals")).toBeTruthy();
  });

  it("⛔ CONTROL: Alt+G still opens a REPORT, and Alt+C still creates a ledger", () => {
    const app = renderShell(createMockClient());
    goTo(app, "Day Book");
    expect(screen.queryByTestId(/^master-create:/)).toBeNull();
    cleanup();

    const app2 = renderShell(createMockClient());
    fireEvent.keyDown(app2, { key: "F8" }); // a Sales voucher
    fireEvent.keyDown(app2, altMac("c")); // Option+C composes "ç" on a Mac
    expect(screen.getByTestId("master-create:ledger")).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// D36 — the opening balance, its side, its breakup and the bill-wise flag.
// ---------------------------------------------------------------------------
describe("⛔ D36: a ledger can be given an opening balance from the keyboard", () => {
  function spyClient(): {
    client: CoredClient;
    calls: CreateLedgerRequest[];
  } {
    const base = createMockClient();
    const calls: CreateLedgerRequest[] = [];
    const client: CoredClient = {
      ...base,
      createLedger: vi.fn(async (c: string, req: CreateLedgerRequest) => {
        calls.push(req);
        return base.createLedger(c, req);
      }),
    };
    return { client, calls };
  }

  it("sends the balance, the SIDE, the bill-wise flag and the breakup", async () => {
    const { client, calls } = spyClient();
    const app = renderShell(client);
    goTo(app, "Create Ledger");

    type("master-name", "Acme Traders");
    fireEvent.mouseDown(screen.getByText("Sundry Debtors"));
    type("master-opening-balance", "15000");
    // ⛔ THE SIDE IS ASSERTED, NOT THE TOTAL. A debtor opened on the CREDIT side
    // balances to the same paisa and reverses the Balance Sheet.
    fireEvent.click(screen.getByTestId("master-opening-dc-D"));
    fireEvent.click(screen.getByTestId("master-billwise"));

    type("master-bill-0-ref", "INV-001");
    type("master-bill-0-amount", "10000");
    fireEvent.click(screen.getByTestId("master-bill-add"));
    type("master-bill-1-ref", "INV-002");
    type("master-bill-1-amount", "5000");
    expect(screen.getByTestId("master-bill-difference").textContent).toContain(
      "it adds up",
    );

    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });

    expect(calls).toHaveLength(1);
    const req = calls[0]!;
    expect(req.name).toBe("Acme Traders");
    expect(req.group_name).toBe("Sundry Debtors");
    expect(req.opening_balance).toBe("15000.0000");
    expect(req.opening_dc).toBe("D");
    expect(req.maintain_bill_by_bill).toBe(true);
    expect(req.opening_bills).toEqual([
      { ref_name: "INV-001", ref_type: "NEW", amount: "10000.0000" },
      { ref_name: "INV-002", ref_type: "NEW", amount: "5000.0000" },
    ]);
    await waitFor(() => {
      expect(statusLine()).toContain("Acme Traders");
    });
  });

  it("the opening Cr side is keyable too, and it is what is sent", async () => {
    const { client, calls } = spyClient();
    const app = renderShell(client);
    goTo(app, "Create Ledger");
    type("master-name", "Bharat Supplies");
    fireEvent.mouseDown(screen.getByText("Sundry Creditors"));
    type("master-opening-balance", "8000.50");
    fireEvent.click(screen.getByTestId("master-opening-dc-C"));

    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });
    expect(calls[0]!.opening_dc).toBe("C");
    expect(calls[0]!.opening_balance).toBe("8000.5000");
    // Bill-wise off means NO breakup is sent, not an empty one.
    expect(calls[0]!.opening_bills).toBeUndefined();
    expect(calls[0]!.maintain_bill_by_bill).toBe(false);
  });

  it("⛔ the 'details' cursor position actually LANDS on a control", () => {
    // D36's second clause verbatim: "the MASTER_FIELDS 'details' cursor position
    // has no control behind it". Two Enters walk name -> group -> details, and
    // the opening balance must be what has focus — a field the cursor can never
    // reach is the same defect as a field that does not exist.
    const app = renderShell(createMockClient());
    goTo(app, "Create Ledger");
    fireEvent.keyDown(app, { key: "Enter" }); // name -> group
    fireEvent.keyDown(app, { key: "Enter" }); // group -> details
    expect(document.activeElement).toBe(
      screen.getByTestId("master-opening-balance"),
    );
  });

  it("⛔ a breakup that does not ADD UP is refused, and NOTHING is sent", async () => {
    // The server refuses this (masters.CreateLedger), and it must: a breakup with
    // a difference makes Outstandings disagree with the Balance Sheet by exactly
    // the shortfall, silently. The form refuses it while the figures are still on
    // screen and names BOTH of them.
    const { client, calls } = spyClient();
    const app = renderShell(client);
    goTo(app, "Create Ledger");
    type("master-name", "Short Breakup Ltd");
    fireEvent.mouseDown(screen.getByText("Sundry Debtors"));
    type("master-opening-balance", "15000");
    fireEvent.click(screen.getByTestId("master-billwise"));
    type("master-bill-0-ref", "INV-001");
    type("master-bill-0-amount", "10000");

    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });

    expect(calls).toHaveLength(0);
    const line = statusLine();
    expect(line).toContain("10000.0000");
    expect(line).toContain("15000.0000");
    expect(line).toContain("5000.0000"); // the difference, named
    expect(line).toContain("Nothing was created");
  });
});

// ---------------------------------------------------------------------------
// ROW 20's other half — "a Kg item is impossible".
// ---------------------------------------------------------------------------
describe("⛔ a stock item can be given a REAL unit", () => {
  it("Kg is keyable and Kg is what goes on the wire", async () => {
    const base = createMockClient();
    const seen: { name: string; unit_symbol: string }[] = [];
    const client: CoredClient = {
      ...base,
      createStockItem: vi.fn(async (c: string, req) => {
        seen.push({ name: req.name, unit_symbol: req.unit_symbol });
        return base.createStockItem(c, req);
      }),
    };
    const app = renderShell(client);
    goTo(app, "Create Stock Item");

    type("master-name", "Basmati Rice");
    const units = screen.getByRole("combobox", { name: "Units" });
    fireEvent.change(units, { target: { value: "Kg" } });
    fireEvent.mouseDown(screen.getByText("Kg"));
    type("master-item-hsn", "10063020");
    type("master-item-gst-rate", "5");
    type("master-item-opening-qty", "120");
    type("master-item-opening-rate", "82.50");

    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });

    expect(seen).toHaveLength(1);
    // ⛔ THE CONTROL IS "not Nos". The old call site was
    // `unit_symbol: p || "Nos"` over an empty picker, so EVERY item in this
    // product was a Nos item and no test could tell.
    expect(seen[0]!.unit_symbol).toBe("Kg");
    expect(seen[0]!.unit_symbol).not.toBe("Nos");
  });

  it("⛔ an item with NO unit is refused here, STRICTER than the server", async () => {
    // masters.CreateStockItem writes `unit_id NULL` for an empty unit_symbol with
    // no CHECK and no refusal, and the ONLY thing that ever notices is the
    // e-invoice builder (einvoice.go:711, ErrNoUnitOfMeasure) — by which time the
    // invoice is already posted and cannot be e-invoiced at all.
    //
    // ⛔ AND THE REFUSAL IS REACHABLE, which is why the field carries no seeded
    // default: a visible "Nos" would have made this branch dead code, and dead
    // code is what row 20 was.
    const base = createMockClient();
    const create = vi.fn(base.createStockItem);
    const app = renderShell({ ...base, createStockItem: create } as CoredClient);
    goTo(app, "Create Stock Item");
    type("master-name", "Unitless Widget");
    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });
    expect(create).not.toHaveBeenCalled();
    expect(statusLine()).toContain("needs a unit of measure");
    expect(statusLine()).toContain("Nothing was created");
  });

  it("⛔ CONTROL: the same item WITH a unit is created", async () => {
    // A path that refuses everything looks identical to a working refusal.
    const base = createMockClient();
    const create = vi.fn(base.createStockItem);
    const app = renderShell({ ...base, createStockItem: create } as CoredClient);
    goTo(app, "Create Stock Item");
    type("master-name", "Unitless Widget");
    fireEvent.mouseDown(screen.getByText("Nos"));
    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });
    expect(create).toHaveBeenCalledTimes(1);
  });
});

// ---------------------------------------------------------------------------
// D47 — a refusal must reach the operator after the frame has popped.
// ---------------------------------------------------------------------------
describe("⛔ D47: a REFUSED master create is reported, not swallowed", () => {
  function refusingClient(err: ApiError): CoredClient {
    const base = createMockClient();
    return {
      ...base,
      createLedger: vi.fn(() => Promise.reject(err)),
    } as CoredClient;
  }

  it("the 403 reaches the STATUS LINE even though the screen is gone", async () => {
    const err = new ApiError(
      403,
      "create ledger is refused to your security level",
      "forbidden",
    );
    const app = renderShell(refusingClient(err));
    goTo(app, "Create Ledger");
    type("master-name", "Refused Ltd");
    fireEvent.mouseDown(screen.getByText("Sundry Debtors"));

    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });

    // The frame HAS popped — that part is the machine doing its job.
    expect(screen.queryByTestId("master-create:ledger")).toBeNull();
    // ⛔ AND THIS IS THE DEFECT. Before the fix the handler was `void` and
    // reported with setStatus into an unmounted component: the operator was
    // returned to the Gateway with the status line saying nothing about a 403.
    await waitFor(() => {
      const line = statusLine();
      expect(line).toContain("Refused Ltd");
      // The SERVER'S sentence, not a retyped copy of it.
      expect(line).toContain(err.message);
      expect(line).toContain("nothing was created");
    });
  });

  it("an INLINE refusal also says the voucher field now names a ghost", async () => {
    const err = new ApiError(403, "create ledger is refused", "forbidden");
    const app = renderShell(refusingClient(err));
    fireEvent.keyDown(app, { key: "F8" });
    fireEvent.keyDown(app, altMac("c"));
    type("master-name", "Ghost Party");
    fireEvent.mouseDown(screen.getByText("Sundry Debtors"));

    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });

    await waitFor(() => {
      // onSelect() runs SYNCHRONOUSLY (the machine's accept reducer runs on the
      // next line), so a refused inline create leaves the field pre-filled with a
      // master that does not exist. The message says so, in those words.
      expect(statusLine()).toContain("clear it before saving the voucher");
    });
  });

  it("⛔ CONTROL: a SUCCESSFUL create reports success, not a refusal", async () => {
    const app = renderShell(createMockClient());
    goTo(app, "Create Ledger");
    type("master-name", "Accepted Ltd");
    fireEvent.mouseDown(screen.getByText("Sundry Debtors"));

    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });

    await waitFor(() => {
      const line = statusLine();
      expect(line).toContain("Created ledger");
      expect(line).toContain("Accepted Ltd");
      expect(line).not.toContain("Could not create");
    });
  });

  it("a nameless master is refused and the refusal reaches the status line", async () => {
    const app = renderShell(createMockClient());
    goTo(app, "Create Unit");
    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });
    expect(statusLine()).toContain("needs a symbol");
  });
});

// ---------------------------------------------------------------------------
// The group arm, which the Go To route makes reachable for the first time.
// ---------------------------------------------------------------------------
describe("⛔ Create Group: 'Primary' means NO PARENT, plus a nature", () => {
  it("sends nature/is_pl and NOT a parent named 'Primary'", async () => {
    const base = createMockClient();
    const seen: unknown[] = [];
    const client = {
      ...base,
      createGroup: vi.fn(async (c: string, req) => {
        seen.push(req);
        return base.createGroup(c, req);
      }),
    } as CoredClient;
    const app = renderShell(client);
    goTo(app, "Create Group");

    type("master-name", "Plant & Machinery");
    fireEvent.mouseDown(screen.getByText("Primary"));
    fireEvent.change(screen.getByTestId("master-group-nature"), {
      target: { value: "Assets" },
    });

    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });

    // ⛔ NO group named "Primary" is seeded, so sending it as a parent_name is
    // answered with `parent group "Primary" not found` for what the operator read
    // as the ordinary top-level choice.
    expect(seen[0]).toEqual({
      name: "Plant & Machinery",
      nature: "ASSET", // the enum, NOT the label "Assets"
      is_pl: false,
    });
  });

  it("a primary group with no nature is refused before anything is sent", async () => {
    const base = createMockClient();
    const create = vi.fn(base.createGroup);
    const app = renderShell({ ...base, createGroup: create } as CoredClient);
    goTo(app, "Create Group");
    type("master-name", "Nature-less");
    fireEvent.mouseDown(screen.getByText("Primary"));

    await act(async () => {
      fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    });
    expect(create).not.toHaveBeenCalled();
    expect(statusLine()).toContain("needs a nature");
  });
});
