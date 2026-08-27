import { describe, it, expect, afterEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
} from "@testing-library/react";
import { createActor } from "xstate";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createDefaultRegistry } from "../app/defaultScreens";
import { createMockClient, ApiError } from "../api";
import type { CoredClient, PrintTemplateResponse } from "../api";
import {
  navMachine,
  projectSnapshot,
  PRINT_TEMPLATES_LABEL,
} from "../keystroke/navMachine";
import { requestFrom, splitTemplateLines } from "./PrintTemplateScreen";
import { printTemplateSurfaceFor } from "./printTemplateSurface";

afterEach(cleanup);

const COMPANY = "company-demo";

function renderShell(client: CoredClient = createMockClient()) {
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <AppShell registry={createDefaultRegistry()} />
    </CompanyProvider>,
  );
  return screen.getByRole("application");
}

/**
 * Open the screen from the GATEWAY menu.
 *
 * ⛔ NOT `Alt+G` Go To, which is the mistake the B1 drive made and paid three
 * failed probes for. That picker is generated from `["Vouchers",
 * ...REPORT_NAMES]` and this is a ScreenKind, not a report name — so it is not in
 * the Go To list and cannot be.
 */
function openFromGateway(app: HTMLElement, label: string): void {
  fireEvent.mouseDown(screen.getByText(label));
  fireEvent.keyDown(app, { key: "Enter" });
}

async function openScreen(client?: CoredClient): Promise<HTMLElement> {
  const app = renderShell(client);
  openFromGateway(app, PRINT_TEMPLATES_LABEL);
  // The screen loads its template on mount; every assertion below depends on
  // that having landed, and pressing a control on a screen that has not finished
  // loading is rule 5's "refusal for the wrong reason".
  await waitFor(() =>
    expect(screen.getByTestId("print-template-state")).toBeTruthy(),
  );
  return app;
}

function input(field: string): HTMLInputElement | HTMLTextAreaElement {
  return screen.getByTestId(`print-template-input-${field}`) as
    | HTMLInputElement
    | HTMLTextAreaElement;
}

function type(field: string, value: string): void {
  fireEvent.change(input(field), { target: { value } });
}

// ===========================================================================
// THE DOOR — three of the registration points ship the screen completely dead
// ===========================================================================

/**
 * ⛔ THE NAVMACHINE DISPATCH IS ONE OF THE THREE POINTS THAT SHIP A SCREEN DEAD
 * WITH NO ERROR AND NO STATUS LINE. `canonicalReportName` FALLS BACK TO THE RAW
 * LABEL, so without the match this label pushes a `report` frame named "Print
 * Templates" — which renders "not available in this build" over a screen that is
 * built and works. That is not an obscure failure: it is indistinguishable, from
 * the operator's chair, from the feature not existing.
 */
describe("the Gateway door lands on the screen and never on a report frame", () => {
  it("opens the print-templates screen", () => {
    const actor = createActor(navMachine, {
      input: { startScreen: "gateway", keymapProfile: "tallyprime" },
    }).start();
    actor.send({ type: "SELECT", menuItem: PRINT_TEMPLATES_LABEL });
    actor.send({ type: "KEY", combo: "Enter" });
    const snap = projectSnapshot(actor.getSnapshot().context);
    expect(snap.screen).toBe("print-templates");
    // ⛔ THE ASSERTION THAT MATTERS, asserted as a property rather than by
    // checking the screen name alone: a `report` frame would carry a reportName,
    // and this frame must carry none.
    expect(snap.reportName ?? "").toBe("");
  });

  it("renders through the real shell, with the chrome titled", async () => {
    await openScreen();
    expect(screen.getByTestId("print-template-screen")).toBeTruthy();
    // ⛔ WITHOUT AppShell's TITLE CASE the chrome reads "Tally" — the `default` —
    // over the screen that decides what every invoice this company issues says.
    expect(screen.getByRole("banner").getAttribute("aria-label")).toContain(
      "Print Templates",
    );
  });

  it("advertises no Enter: Open, because there is nothing to open", async () => {
    await openScreen();
    // ⛔ WITHOUT AppShell's HINT CASE the strip falls to `default` and offers
    // "Enter: Open" on a form.
    const hints =
      screen.getByLabelText("Shortcut hints").textContent ?? "";
    expect(hints).not.toMatch(/Enter: Open/);
    expect(hints).toMatch(/Esc: Back/);
  });
});

/**
 * ⛔⛔ THIS TEST EXISTS BECAUSE A MUTATION PROOF FOUND T32'S SUITE BLIND, AND THE
 * HOLE IS STILL OPEN FOR THIRTEEN OTHER SCREENS.
 *
 * Deleting a screen from AppShell's `COMPANY_SCOPED_SCREENS` breaks NOTHING in an
 * ordinary suite: that set is consulted only when `company === null`
 * (`needsCompany && !companyReady` chooses the "Preparing company…" placeholder),
 * and every test above hands the provider an `initialCompany`. Without the entry
 * the screen renders company-less and `useCompanyID()` THROWS during render,
 * taking the whole shell down.
 *
 * `CompanyProvider` does not bootstrap unless asked, so a provider with no
 * `initialCompany` is a deterministic company-less shell. This is the SECOND
 * screen in that set of fifteen to have such a test; the other thirteen still
 * have none.
 */
describe("the screen is COMPANY-SCOPED, and the company-less shell survives it", () => {
  it("shows the placeholder rather than throwing when no company is open", () => {
    render(
      <CompanyProvider client={createMockClient()}>
        <AppShell registry={createDefaultRegistry()} />
      </CompanyProvider>,
    );
    const app = screen.getByRole("application");
    openFromGateway(app, PRINT_TEMPLATES_LABEL);
    // The placeholder, not the screen — and above all not a thrown "no active
    // company selected" that unmounts the application.
    expect(screen.getByRole("status").textContent).toMatch(/preparing company/i);
    expect(screen.queryByTestId("print-template-screen")).toBeNull();
    // Asserted on the chrome's own aria-label rather than on page text, because
    // the screen deliberately has NOT rendered — its <h1> is exactly what is
    // missing, so a text match would be asserting the thing this test proves
    // does not happen.
    expect(screen.getByRole("banner").getAttribute("aria-label")).toContain(
      "Print Templates",
    );
  });
});

// ===========================================================================
// THE FORM IS BUILT FROM THE ARTEFACT — the ragged grid, on screen
// ===========================================================================

/**
 * ⛔ THE ASSERTION THIS TRACK TURNS ON. A control offered for a field its
 * renderer ignores is the worst outcome available: the operator types a
 * declaration for a report, the screen accepts it, the row stores it, and no
 * printed statement ever carries it. Nothing errors and nothing logs.
 */
describe("the form offers exactly the fields the document actually prints", () => {
  it("offers all seven for a tax invoice", async () => {
    await openScreen();
    const surface = printTemplateSurfaceFor("tax-invoice");
    expect(surface, "the artefact must carry tax-invoice").toBeTruthy();
    for (const field of surface!.honours) {
      expect(
        screen.queryByTestId(`print-template-field-${field}`),
        `the tax invoice honours ${field} and the form does not offer it`,
      ).toBeTruthy();
    }
  });

  it("⛔ hides the four a REPORT cannot print, and says which and why", async () => {
    await openScreen();
    fireEvent.change(screen.getByTestId("print-template-kind"), {
      target: { value: "report" },
    });
    await waitFor(() =>
      expect(screen.queryByTestId("print-template-field-declaration")).toBeNull(),
    );

    const surface = printTemplateSurfaceFor("report");
    expect(surface!.ignores.length, "the artefact says a report ignores fields")
      .toBeGreaterThan(0);
    for (const field of surface!.ignores) {
      expect(
        screen.queryByTestId(`print-template-field-${field}`),
        `a report ignores ${field}; offering it stores a value that never prints`,
      ).toBeNull();
    }
    // And the ones it DOES honour are still there — a screen that hid
    // everything would pass the assertions above and be useless.
    for (const field of surface!.honours) {
      expect(
        screen.queryByTestId(`print-template-field-${field}`),
        `a report honours ${field} and the form does not offer it`,
      ).toBeTruthy();
    }

    // ⛔ NAMED RATHER THAN SILENTLY ABSENT. An operator who set a declaration on
    // the invoice and then picked Report would otherwise find the box simply
    // gone and conclude the screen was broken.
    const said = screen.getByTestId("print-template-ignored").textContent ?? "";
    expect(said).toMatch(/declaration/i);
    expect(said).toMatch(/does not print/i);
  });
});

// ===========================================================================
// WHAT WILL PRINT — the defaults, shown rather than left blank
// ===========================================================================

describe("an empty box shows what will actually print", () => {
  it("carries the built-in declaration as the placeholder, not a blank", async () => {
    await openScreen();
    const expected = printTemplateSurfaceFor("tax-invoice")!.defaults
      .declaration;
    expect(expected, "the artefact must carry a default declaration").not.toBe(
      "",
    );
    // ⛔ A FORM OF EMPTY BOXES READS AS "THIS COMPANY HAS NO DECLARATION". The
    // built-in is real text a company may be perfectly happy with, and an
    // operator has to be able to see it before deciding to replace it.
    expect(input("declaration").getAttribute("placeholder")).toBe(expected);
    expect(
      screen.getByTestId("print-template-default-declaration").textContent ?? "",
    ).toContain(expected);
  });

  it("says a report's title default is the report's own name, rather than showing a blank", async () => {
    await openScreen();
    fireEvent.change(screen.getByTestId("print-template-kind"), {
      target: { value: "report" },
    });
    await waitFor(() =>
      expect(screen.queryByTestId("print-template-field-declaration")).toBeNull(),
    );
    // ⚠️ THE ONE KIND WHOSE DEFAULT IS NOT A FIXED STRING. There are thirty
    // statements and each supplies its own title, so there is nothing to show —
    // and a blank placeholder here would read as "this company has no title".
    expect(
      printTemplateSurfaceFor("report")!.default_title_is_the_documents_own_name,
    ).toBe(true);
    expect(input("title").getAttribute("placeholder") ?? "").toMatch(
      /report's own name/i,
    );
  });

  it("distinguishes 'no default' from 'not offered' for a payment's declaration", async () => {
    await openScreen();
    fireEvent.change(screen.getByTestId("print-template-kind"), {
      target: { value: "payment" },
    });
    await waitFor(() =>
      expect(
        screen.getByTestId("print-template-field-declaration"),
      ).toBeTruthy(),
    );
    // ⚠️ THREE KINDS PRINT NO DECLARATION UNLESS GIVEN ONE, which is a different
    // fact from a kind that IGNORES the field. The first is an empty box worth
    // filling; the second should not be on the form at all. The screen must not
    // collapse them.
    expect(printTemplateSurfaceFor("payment")!.defaults.declaration).toBe("");
    expect(
      screen.getByTestId("print-template-field-declaration").textContent ?? "",
    ).toMatch(/prints no declaration unless you give it one/i);
  });
});

// ===========================================================================
// SAVE AND CLEAR — the two-way door
// ===========================================================================

describe("saving renders the STORED row and never the request", () => {
  it("shows the row the server answered with", async () => {
    await openScreen();
    type("declaration", "Goods once sold are not returnable.");
    fireEvent.click(screen.getByTestId("print-template-save"));
    await waitFor(() =>
      expect(screen.getByTestId("print-template-saved")).toBeTruthy(),
    );
    expect(screen.getByTestId("print-template-state").textContent ?? "").toMatch(
      /has customised/i,
    );
    expect(input("declaration").value).toBe(
      "Goods once sold are not returnable.",
    );
  });

  /**
   * ⛔ THE ORACLE THAT CAN SEE A DROPPED COLUMN. A screen that re-rendered its
   * own form state after a save would be UNABLE to detect a server that stored
   * nothing — a resolved promise looks identical either way. That is the trap
   * register row D43 set for every caller of POST /v1/companies, and the
   * NegativeStockPanel next door carries the same argument.
   */
  it("shows what the server stored even when that is NOT what was typed", async () => {
    const stub = createMockClient();
    const client: CoredClient = {
      ...stub,
      async savePrintTemplate(companyID, docKind) {
        const base = await stub.getPrintTemplate(companyID, docKind);
        // A server that silently dropped the declaration column.
        return {
          ...base,
          customised: true,
          overrides: { ...base.overrides, declaration: "" },
        } satisfies PrintTemplateResponse;
      },
    };
    await openScreen(client);
    type("declaration", "This text is going to be dropped.");
    fireEvent.click(screen.getByTestId("print-template-save"));
    await waitFor(() =>
      expect(screen.getByTestId("print-template-saved")).toBeTruthy(),
    );
    // The box is EMPTY, because the row is. A screen echoing its own state would
    // show the typed text here and report success over a lost value.
    expect(input("declaration").value).toBe("");
  });
});

describe("clearing is reachable, and only when there is something to clear", () => {
  it("offers no clear button on a company that has customised nothing", async () => {
    await openScreen();
    expect(screen.getByTestId("print-template-state").textContent ?? "").toMatch(
      /has not customised/i,
    );
    // ⛔ A BUTTON THAT WOULD DO NOTHING MUST NOT BE OFFERED. Otherwise "Clear this
    // customisation" appears on a company that has none, and pressing it reports
    // success for a state it was already in.
    expect(screen.queryByTestId("print-template-clear")).toBeNull();
  });

  it("⛔ puts the company back on the built-in layout — a one-way door is not a setting", async () => {
    await openScreen();
    type("declaration", "Something this company will regret.");
    fireEvent.click(screen.getByTestId("print-template-save"));
    await waitFor(() =>
      expect(screen.getByTestId("print-template-clear")).toBeTruthy(),
    );

    fireEvent.click(screen.getByTestId("print-template-clear"));
    await waitFor(() =>
      expect(screen.getByTestId("print-template-state").textContent ?? "").toMatch(
        /has not customised/i,
      ),
    );
    // The stored value is gone, and the box shows the built-in default as its
    // placeholder again rather than the text that was cleared.
    expect(input("declaration").value).toBe("");
    expect(screen.queryByTestId("print-template-clear")).toBeNull();
    expect(screen.getByTestId("print-template-saved").textContent ?? "").toMatch(
      /back to the built-in layout/i,
    );
  });
});

// ===========================================================================
// THE REFUSALS ARE THE SERVER'S SENTENCE
// ===========================================================================

describe("a refusal is shown in the server's own words", () => {
  it("renders the server's message rather than a generic failure", async () => {
    const stub = createMockClient();
    const client: CoredClient = {
      ...stub,
      async savePrintTemplate() {
        throw new ApiError(
          403,
          "alter company: this security level may not change the company's print templates",
        );
      },
    };
    await openScreen(client);
    fireEvent.click(screen.getByTestId("print-template-save"));
    await waitFor(() =>
      expect(screen.getByTestId("print-template-error")).toBeTruthy(),
    );
    // ⚠️ A 403 HERE IS CORRECT BEHAVIOUR, NOT A BROKEN SCREEN. The write maps to
    // (alter, company) and migration 0022 grants a Data Entry level `display
    // company` and nothing wider, so a non-Owner can READ this screen and cannot
    // save from it. The operator has to be told which it was.
    expect(screen.getByTestId("print-template-error").textContent ?? "").toContain(
      "may not change the company's print templates",
    );
  });

  it("clears the form on a failed LOAD rather than leaving another document's values on screen", async () => {
    const stub = createMockClient();
    let calls = 0;
    const client: CoredClient = {
      ...stub,
      async getPrintTemplate(companyID, docKind) {
        calls += 1;
        if (calls > 1) throw new ApiError(500, "the database went away");
        return stub.getPrintTemplate(companyID, docKind);
      },
    };
    await openScreen(client);
    fireEvent.change(screen.getByTestId("print-template-kind"), {
      target: { value: "receipt" },
    });
    await waitFor(() =>
      expect(screen.getByTestId("print-template-error")).toBeTruthy(),
    );
    // ⛔ NO FORM AT ALL, rather than the invoice's values under the receipt's
    // heading. Leaving them would invite the operator to press Save and copy one
    // document's declaration onto another.
    expect(screen.queryByTestId("print-template-field-declaration")).toBeNull();
    expect(screen.queryByTestId("print-template-save")).toBeNull();
  });
});

// ===========================================================================
// THE PURE PARTS — testable without mounting anything
// ===========================================================================

describe("splitTemplateLines", () => {
  it("drops the blank lines a textarea accumulates", () => {
    // ⚠️ A TRAILING NEWLINE IS WHAT A TEXTAREA HAS AFTER ANYBODY PRESSES ENTER,
    // and storing it would print an empty line on every document this company
    // issues. A stray blank line on every invoice is a defect; a missing one is
    // a preference.
    expect(splitTemplateLines("A\n\nB\n")).toEqual(["A", "B"]);
    expect(splitTemplateLines("  spaced  \n")).toEqual(["spaced"]);
    expect(splitTemplateLines("")).toEqual([]);
    expect(splitTemplateLines("\n\n")).toEqual([]);
  });
});

describe("requestFrom", () => {
  const filled = {
    title: "T",
    header_lines: "H",
    footer_lines: "F",
    declaration: "D",
    terms: "M",
    signature_label: "S",
    show_bank_details: true,
    bank_lines: "B",
  };

  it("⛔ sends nothing for a field the document does not honour", () => {
    // Storing a value the renderer never reads is the silent lie this whole
    // track is built around avoiding; the server refuses it, and a correct
    // screen never asks.
    const req = requestFrom(filled, ["title", "header_lines", "footer_lines"]);
    expect(req.title).toBe("T");
    expect(req.header_lines).toEqual(["H"]);
    expect(req.footer_lines).toEqual(["F"]);
    expect(req.declaration).toBeUndefined();
    expect(req.terms).toBeUndefined();
    expect(req.signature_label).toBeUndefined();
    expect(req.bank_lines).toBeUndefined();
    expect(req.show_bank_details).toBeUndefined();
  });

  it("sends the bank FLAG and LINES together, because they are one control", () => {
    // ⛔ EVERY RENDERER TESTS `show_bank_details && bank_lines non-empty`. Sending
    // the lines without the flag stores details that print nothing, having asked
    // the operator to type their account number.
    const req = requestFrom(filled, ["bank_details"]);
    expect(req.show_bank_details).toBe(true);
    expect(req.bank_lines).toEqual(["B"]);
  });

  it("sends an EMPTY value for an honoured field the operator cleared", () => {
    // ⚠️ THE PUT IS A FULL RESTATEMENT, so an emptied box has to travel as empty.
    // Omitting it would be merge semantics, and an emptied declaration would come
    // back on the next load — the operator deletes the text, saves, and it
    // reappears.
    const req = requestFrom({ ...filled, declaration: "" }, ["declaration"]);
    expect(req.declaration).toBe("");
    expect("declaration" in req).toBe(true);
  });
});
