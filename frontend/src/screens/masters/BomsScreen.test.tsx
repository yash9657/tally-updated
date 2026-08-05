import { describe, it, expect, afterEach } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor,
} from "@testing-library/react";

import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type { CoredClient } from "../../api";

/**
 * BILLS OF MATERIALS (T3) — through the real shell, for the reason
 * OrderRegisterScreen.test.tsx states in full: T3's backend shipped complete and
 * tested with no screen at all, so the only assertion worth making is that an
 * OPERATOR can reach it.
 *
 * ⛔ THE ONE BEHAVIOUR THIS FILE EXISTS TO PIN. `output_qty` is the YIELD and the
 * components are stored AGAINST it, never pre-divided per unit. A UI that
 * "helpfully" normalised "10 kg makes 3" to 3.3333 kg per unit would lose material
 * at fixed-4 forever — exploding for a batch of 3 gives 9.9999 kg, and the
 * hundredth-of-a-gram shortfall accumulates in closing stock as phantom material
 * that reconciles against nothing. So the explosion assertion below checks the exact
 * figure, not a rounded one, and the create test checks that what is SENT is the
 * recipe as written.
 */

const COMPANY = "company-demo";

afterEach(cleanup);

function renderShell(client: CoredClient = createMockClient()) {
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

/** Gateway -> Bills of Materials. The Gateway is this screen's only door. */
async function openBoms(): Promise<HTMLElement> {
  const app = renderShell();
  fireEvent.mouseDown(screen.getByText("Bills of Materials"));
  fireEvent.keyDown(app, { key: "Enter" });
  await screen.findByTestId("boms-empty");
  return app;
}

/** Key a recipe: Widget yields `output`, consuming `qty` Gaskets. */
function keyARecipe(opts: { name: string; output: string; qty: string }) {
  fireEvent.change(screen.getByLabelText("Recipe name"), {
    target: { value: opts.name },
  });
  const good = screen.getByLabelText("Finished good") as HTMLSelectElement;
  fireEvent.change(good, {
    target: {
      value: [...good.options].find((o) => o.textContent === "Widget")?.value,
    },
  });
  fireEvent.change(screen.getByLabelText("Output quantity (the yield)"), {
    target: { value: opts.output },
  });
  const comp = screen.getByLabelText("Component 1") as HTMLSelectElement;
  fireEvent.change(comp, {
    target: {
      value: [...comp.options].find((o) => o.textContent === "Gasket")?.value,
    },
  });
  fireEvent.change(screen.getByLabelText("Quantity of component 1"), {
    target: { value: opts.qty },
  });
}

describe("Alt+C on the BOM screen — the key is ROUTED by the shell", () => {
  it("opens the recipe form, which is ABSENT until it is pressed", async () => {
    const app = await openBoms();
    // THE CONTROL.
    expect(screen.queryByTestId("bom-panel")).toBeNull();
    expect(screen.getByRole("button", { name: /Alt\+C New Recipe/ })).toBeTruthy();

    fireEvent.keyDown(app, { key: "c", altKey: true });

    // Fails if SCREEN_ACTIONS is missing its `createBom` entry — and nothing else
    // in the repo does.
    expect(screen.getByTestId("bom-panel")).toBeTruthy();
  });

  it("picks the finished good and every component from the served item list", async () => {
    const app = await openBoms();
    fireEvent.keyDown(app, { key: "c", altKey: true });
    expect((screen.getByLabelText("Finished good") as HTMLElement).tagName).toBe(
      "SELECT",
    );
    expect((screen.getByLabelText("Component 1") as HTMLElement).tagName).toBe(
      "SELECT",
    );
    // The seeded company's real items are in them; no id is ever typed.
    expect(screen.getAllByRole("option", { name: "Widget" }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole("option", { name: "Gasket" }).length).toBeGreaterThan(0);
  });

  it("F9 does NOT open a Purchase voucher over a half-keyed recipe", async () => {
    const app = await openBoms();
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyARecipe({ name: "Standard", output: "3", qty: "10" });

    fireEvent.keyDown(app, { key: "F9" });

    expect(screen.queryByText(/Purchase Voucher/)).toBeNull();
    expect(screen.getByTestId("bom-panel")).toBeTruthy();
    expect(
      (screen.getByLabelText("Recipe name") as HTMLInputElement).value,
    ).toBe("Standard");
  });

  it("says the yield is a yield, and warns against pre-dividing it", async () => {
    // The wording IS the safeguard here: the mistake is a helpful-looking one, so
    // the form has to argue against it at the point it would be made.
    const app = await openBoms();
    fireEvent.keyDown(app, { key: "c", altKey: true });
    const panel = screen.getByTestId("bom-panel");
    expect(panel.textContent).toMatch(/NOT per\s*unit of it/);
    expect(panel.textContent).toMatch(/3\.3333/);
    expect(panel.textContent).toMatch(/reconciles against nothing/);
  });
});

describe("recording a recipe, and reading it back AGAINST ITS YIELD", () => {
  it("Ctrl+A saves it and the list shows the yield the operator keyed", async () => {
    const app = await openBoms();
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyARecipe({ name: "Standard", output: "3", qty: "10" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const row = await screen.findByTestId("bom-row:Widget:Standard");
    // ⛔ THE YIELD AS KEYED, not divided. "10 kg makes 3" is output 3.
    expect(row.textContent).toMatch(/3\.0000/);

    // And the components read back against that yield, with the server's line_no.
    const comp = await screen.findByTestId("bom-component-row:Standard:1");
    expect(comp.textContent).toMatch(/Gasket/);
    expect(comp.textContent).toMatch(/10\.0000/);
    // The heading states what the quantity is AGAINST, so the figure cannot be
    // misread as a per-unit rate.
    expect(screen.getByTestId("bom-components").textContent).toMatch(
      /Consumed per 3\.0000 made/,
    );
  });

  it("refuses an item as a component of its OWN recipe, before sending", async () => {
    const app = await openBoms();
    fireEvent.keyDown(app, { key: "c", altKey: true });
    const good = screen.getByLabelText("Finished good") as HTMLSelectElement;
    const widget = [...good.options].find((o) => o.textContent === "Widget")
      ?.value;
    fireEvent.change(good, { target: { value: widget } });
    fireEvent.change(screen.getByLabelText("Output quantity (the yield)"), {
      target: { value: "1" },
    });
    // The SAME item as a component — a recipe that consumes what it makes.
    fireEvent.change(screen.getByLabelText("Component 1"), {
      target: { value: widget },
    });
    fireEvent.change(screen.getByLabelText("Quantity of component 1"), {
      target: { value: "1" },
    });

    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    // The form is still open with the operator's work in it, and nothing was sent.
    expect(screen.getByTestId("bom-panel")).toBeTruthy();
    await waitFor(() =>
      expect(screen.getByTestId("status-line").textContent).toMatch(
        /consume the thing it makes/,
      ),
    );
    expect(screen.queryByTestId("boms-table")).toBeNull();
  });

  it("renders the SERVER'S sentence on a duplicate recipe name", async () => {
    const client = createMockClient();
    // Capture the real refusal for the SAME request rather than retyping it.
    const probe = createMockClient();
    const recipe = {
      stock_item_id: "stock-widget",
      name: "Standard",
      output_qty: "3.0000",
      components: [{ stock_item_id: "stock-gasket", qty: "10.0000" }],
      created_by: "ui",
    };
    await probe.createBom(COMPANY, recipe);
    let expected = "";
    try {
      await probe.createBom(COMPANY, recipe);
    } catch (e) {
      expect(e).toBeInstanceOf(ApiError);
      expect((e as ApiError).status).toBe(409);
      expected = (e as Error).message;
    }
    expect(expected.length).toBeGreaterThan(0);

    const app = renderShell(client);
    fireEvent.mouseDown(screen.getByText("Bills of Materials"));
    fireEvent.keyDown(app, { key: "Enter" });
    await screen.findByTestId("boms-empty");

    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyARecipe({ name: "Standard", output: "3", qty: "10" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByTestId("bom-row:Widget:Standard");

    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyARecipe({ name: "Standard", output: "3", qty: "10" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    expect((await screen.findByTestId("bom-refusal")).textContent).toContain(
      expected,
    );
  });
});

describe("Alt+X explodes — a PREVIEW, and the arithmetic must not lose material", () => {
  async function withOneRecipe(): Promise<HTMLElement> {
    const app = await openBoms();
    fireEvent.keyDown(app, { key: "c", altKey: true });
    keyARecipe({ name: "Standard", output: "3", qty: "10" });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    await screen.findByTestId("bom-row:Widget:Standard");
    return app;
  }

  it("Alt+X opens the explosion panel, and is routed by the shell", async () => {
    const app = await withOneRecipe();
    // THE CONTROL.
    expect(screen.queryByTestId("bom-explode-panel")).toBeNull();
    expect(screen.getByRole("button", { name: /Alt\+X Explode/ })).toBeTruthy();

    fireEvent.keyDown(app, { key: "x", altKey: true });

    // Fails if SCREEN_ACTIONS has no `explodeBom` entry.
    expect(screen.getByTestId("bom-explode-panel")).toBeTruthy();
  });

  it("Alt+X with NOTHING highlighted opens no panel and says so", async () => {
    const app = await openBoms();
    fireEvent.keyDown(app, { key: "x", altKey: true });
    expect(screen.queryByTestId("bom-explode-panel")).toBeNull();
    await waitFor(() =>
      expect(screen.getByTestId("status-line").textContent).toMatch(
        /Highlight a recipe first/,
      ),
    );
  });

  it("⛔ scales by MULTIPLYING BEFORE DIVIDING — 10 for 3 makes 30 for 9, exactly", async () => {
    // THE ASSERTION THIS WHOLE FILE IS FOR. A recipe of "10 consumed yields 3",
    // exploded for 9, must consume exactly 30. A client that pre-divided to 3.3333
    // per unit would answer 29.9997 and short every batch forever — and the figure
    // would look right.
    const app = await withOneRecipe();
    fireEvent.keyDown(app, { key: "x", altKey: true });
    fireEvent.change(screen.getByLabelText("Wanted quantity"), {
      target: { value: "9" },
    });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    const row = await screen.findByTestId("bom-explosion-row:Gasket");
    expect(row.textContent).toMatch(/30\.0000/);
    expect(row.textContent).not.toMatch(/29\.99/);
  });

  it("requires a quantity rather than defaulting to one unit", async () => {
    const app = await withOneRecipe();
    fireEvent.keyDown(app, { key: "x", altKey: true });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });
    expect(screen.queryByTestId("bom-explosion")).toBeNull();
    await waitFor(() =>
      expect(screen.getByTestId("status-line").textContent).toMatch(
        /a question about a different batch/,
      ),
    );
  });

  it("says on its face that nothing moved, and renders the SINGLE-LEVEL notes", async () => {
    // ⛔ An operator who believed Alt+X had produced something would go looking for
    // stock that is not there. And the notes are what stop the component list being
    // read as a complete bill down to raw materials.
    const app = await withOneRecipe();
    fireEvent.keyDown(app, { key: "x", altKey: true });
    const panel = screen.getByTestId("bom-explode-panel");
    expect(panel.textContent).toMatch(/WRITES NOTHING AND POSTS NOTHING/);

    fireEvent.change(screen.getByLabelText("Wanted quantity"), {
      target: { value: "9" },
    });
    fireEvent.keyDown(app, { key: "a", ctrlKey: true });

    await screen.findByTestId("bom-explosion");
    const notes = screen.getByTestId("bom-explosion-notes");
    // The SERVER's own words. Not retyped — asserted to be present and to be about
    // the single-level limit.
    expect(notes.textContent?.length ?? 0).toBeGreaterThan(0);
    expect(notes.textContent).toMatch(/single-level|SINGLE-LEVEL/i);
  });
});

describe("a 403 on the BOM screen is a DECISION", () => {
  it("states why a recipe is gated and who can lift it", async () => {
    const client = createMockClient();
    const denying: CoredClient = {
      ...client,
      async listBoms() {
        throw new ApiError(403, "forbidden", "forbidden");
      },
    };
    const app = renderShell(denying);
    fireEvent.mouseDown(screen.getByText("Bills of Materials"));
    fireEvent.keyDown(app, { key: "Enter" });

    const panel = await screen.findByTestId("boms-denied");
    expect(panel.textContent).toMatch(/allow bom/);
    // It says what a WRONG recipe would do, which is the reason it is gated at all.
    expect(panel.textContent).toMatch(/closing stock will follow/);
  });
});
