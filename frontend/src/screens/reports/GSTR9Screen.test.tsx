import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, act, fireEvent } from "@testing-library/react";

import { GSTR9Screen } from "./GSTR9Screen";
import { CompanyProvider } from "../../app/CompanyContext";
import { ScreenActionsProvider } from "../../app/ScreenActions";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type {
  CoredClient,
  GSTR9Divergence,
  GSTR9Response,
  GSTR9Month,
  GSTTax,
} from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";

/**
 * THE GSTR-9 ANNUAL RECONCILIATION (T12) — the screen, and the four ways it could
 * lie while looking completely correct.
 *
 * ⛔ NOT ONE TEST HERE IS ABOUT A CRASH OR A BLANK PAGE. An annual return's
 * failure mode is a page of plausible, well-footed numbers, and this
 * repository's register is full of exactly that. Each test asserts the presence
 * of an honest refusal or an honest label, and each failure message names the
 * CONSEQUENCE rather than the assertion.
 *
 * The four:
 *
 *  1. ⭐ AN OFFSETTING YEAR RENDERED AS AGREEMENT. Twelve months added up CANCEL:
 *     +40,000 in May and −40,000 in September is an annual net of ZERO over two
 *     months that disagree with the books. A screen that showed the net alone
 *     would tell a filer their year is clean.
 *  2. A NOT_DERIVED table rendered as zero. A zero in table 7 ("ITC reversed")
 *     asserts the taxpayer reversed nothing; the truth is nothing was checked.
 *  3. A suppressed total drawn anyway, or its reason dropped — leaving a blank
 *     where a statutory figure belongs, which a reader reads as zero.
 *  4. A disclosure quietly not rendered. Ten of them are unconditional, and a
 *     screen showing the tidy tables while hiding the caveats is the most
 *     dangerous artefact this product could produce.
 */

afterEach(cleanup);

const COMPANY = "company-demo";

function snap(): NavSnapshot {
  return {
    screen: "gstr9",
    screenStackDepth: 2,
    canReturnToVoucher: false,
    config: {},
  } as unknown as NavSnapshot;
}

function tax(c: string, s: string, i: string): GSTTax {
  return { camt: c, samt: s, iamt: i };
}

const ZERO = tax("0.0000", "0.0000", "0.0000");

function agreed(a: string, b: string): GSTR9Divergence {
  return { a, b, net: ZERO, gross: ZERO, offsetting: false, agrees: true };
}

function month(fp: string, over: Partial<GSTR9Month> = {}): GSTR9Month {
  return {
    fp,
    from: "2025-04-01",
    to: "2025-04-30",
    r1_txval: "0.0000",
    r1_tax: ZERO,
    r3b_txval: "0.0000",
    r3b_tax: ZERO,
    books_outward_tax: ZERO,
    r3b_itc_available: ZERO,
    r3b_itc_ineligible: ZERO,
    books_itc_tax: ZERO,
    hsn_txval: "0.0000",
    hsn_tax: ZERO,
    r1_matches_r3b: true,
    r3b_matches_books: true,
    itc_matches_books: true,
    hsn_foots_to_r1: true,
    ...over,
  };
}

const FPS = [
  "042025", "052025", "062025", "072025", "082025", "092025",
  "102025", "112025", "122025", "012026", "022026", "032026",
];

/** The ten unconditional disclosures, as cored emits them. */
const UNCONDITIONAL = [
  "advances-not-modelled",
  "amendments-not-modelled",
  "annual-table-structure-unverified",
  "books-carry-no-taxable-value",
  "exempt-and-zero-rated-not-marked",
  "itc-reversals-not-computed",
  "no-annual-2a",
  "no-cash-or-credit-ledger",
  "not-filing-ready",
  "portal-schema-unverified",
];

function view(over: Partial<GSTR9Response> = {}): GSTR9Response {
  return {
    gstin: "29ZZZZZ0000Z1Z5",
    fy: "2025-26",
    from: "2025-04-01",
    to: "2026-03-31",
    months: FPS.map((fp) => month(fp)),
    outward_r1_vs_r3b: agreed("gstr1", "gstr3b"),
    outward_r3b_vs_books: agreed("gstr3b", "books"),
    outward_r1_vs_books: agreed("gstr1", "books"),
    itc_vs_books: agreed("gstr3b-4a-plus-4d", "books"),
    hsn_vs_r1: agreed("gstr1-table-12", "gstr1-sections"),
    taxable_r1_vs_r3b: {
      a: "gstr1",
      b: "gstr3b",
      net: "0.0000",
      gross: "0.0000",
      offsetting: false,
      agrees: true,
    },
    tables: [
      {
        no: "4",
        title: "Details of advances and outward supplies on which tax is payable",
        status: "PARTIAL",
        reason: "The B2B and B2C rows come from the year's twelve GSTR-1 returns.",
        rows: [
          {
            label: "4A-4B",
            title: "Supplies to unregistered and registered persons",
            txval: "1000000.0000",
            tax: tax("90000.0000", "90000.0000", "0.0000"),
          },
        ],
        omitted: [
          {
            label: "4C",
            title: "Exports on payment of tax",
            reason: "these books carry no export marker on a supply.",
          },
        ],
        total_suppressed: true,
        total_suppressed_reason:
          "4N totals rows 4A to 4L and five of them are not derived here.",
      },
      {
        no: "7",
        title: "ITC reversed and ineligible ITC",
        status: "NOT_DERIVED",
        reason:
          "the twelve GSTR-3Bs behind this year were generated with NO inward statement.",
      },
    ],
    disclosures: [...UNCONDITIONAL],
    ...over,
  };
}

/**
 * A client serving one hand-built annual return, plus the six tax ledgers the
 * screen must resolve before it will ask for anything.
 *
 * ⛔ THE RESPONSE IS HAND-BUILT RATHER THAN TAKEN FROM createMockClient, AND THAT
 * IS FORCED RATHER THAN LAZY. The mock cannot produce a divergence at all: it
 * stores postings and derives no GST return from anything, so its three columns
 * are the same column three times (mock.ts says so at length). A test that drove
 * the mock could therefore never exercise the offsetting rendering — which is the
 * one behaviour on this screen that matters.
 */
function gstr9Client(res: GSTR9Response | Error): CoredClient {
  const base = createMockClient({ seed: false });
  const ledger = (id: string, name: string) => ({
    id,
    name,
    group_name: "Duties & Taxes",
    opening_balance: "0.0000",
    opening_dc: "D",
    maintain_bill_by_bill: false,
    gstin: "",
    state_code: "",
    registration_type: "",
    pan: "",
    credit_days: null,
  });
  return {
    ...base,
    listLedgers: vi.fn(async () => ({
      ledgers: [
        ledger("l-ocgst", "Output CGST"),
        ledger("l-osgst", "Output SGST"),
        ledger("l-oigst", "Output IGST"),
        ledger("l-icgst", "Input CGST"),
        ledger("l-isgst", "Input SGST"),
        ledger("l-iigst", "Input IGST"),
      ],
    })),
    gstr9: vi.fn(async () => {
      if (res instanceof Error) throw res;
      return { gstr9: res, reconciled: false };
    }),
  } as unknown as CoredClient;
}

async function renderScreen(client: CoredClient) {
  await act(async () => {
    render(
      <CompanyProvider
        client={client}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <ScreenActionsProvider>
          <GSTR9Screen snapshot={snap()} onSelect={() => {}} />
        </ScreenActionsProvider>
      </CompanyProvider>,
    );
  });
}

/** Render, then press the load button, which is what the operator does. */
async function renderAndLoad(client: CoredClient) {
  await renderScreen(client);
  await act(async () => {
    fireEvent.click(screen.getByTestId("gstr9-load"));
  });
}

describe("GSTR-9 annual reconciliation", () => {
  it("⭐ says the months CANCEL rather than showing a clean annual net", async () => {
    // May and September disagree by 40,000 of IGST in opposite directions.
    const months = FPS.map((fp) =>
      month(fp, {
        r3b_matches_books: fp !== "052025" && fp !== "092025",
      }),
    );
    await renderAndLoad(
      gstr9Client(
        view({
          months,
          outward_r3b_vs_books: {
            a: "gstr3b",
            b: "books",
            net: ZERO,
            gross: tax("0.0000", "0.0000", "80000.0000"),
            offsetting: true,
            months: ["052025", "092025"],
            agrees: false,
          },
          disclosures: [
            ...UNCONDITIONAL,
            "monthly-differences-offset",
            "returns-and-books-disagree",
          ],
        }),
      ),
    );

    const note = screen.queryByTestId("gstr9-offsetting-gstr3b-books");
    expect(
      note,
      "the screen did not say the months CANCEL. The annual net is ZERO and two " +
        "months disagree with the books by 40,000 each — a reader shown only the " +
        "net is told their year is clean, which is the exact defect this report " +
        "exists to find",
    ).not.toBeNull();
    expect(
      note!.textContent,
      "the offsetting note does not NAME the disagreeing months, so a reader " +
        "cannot go and look at them",
    ).toContain("052025");
    expect(note!.textContent).toContain("092025");

    // ⛔ AND THE GROSS IS ON THE PAGE. Without it the note is an assertion the
    // reader cannot check.
    const leg = screen.getByTestId("gstr9-leg-gstr3b-books");
    expect(
      leg.textContent,
      "the gross divergence (80,000) is not rendered. The net alone is the figure " +
        "that hides the disagreement, and a screen that prints it without the " +
        "gross beside it has lost the only property distinguishing this report " +
        "from adding twelve months up",
    ).toContain("80,000.00");

    // The verdict must not read as agreement.
    expect(
      screen.getByTestId("gstr9-verdict-gstr3b-books").textContent,
      "the verdict reads as agreement over a year with two disagreeing months",
    ).toContain("disagrees");
  });

  it("renders the gross beside the net even when the year is clean", async () => {
    // ⛔ THE CONTROL FOR THE TEST ABOVE, AND IT IS NOT REDUNDANT. If the gross
    // were rendered ONLY on bad years, a reader would learn to read its absence
    // as good news — and its absence is also exactly how a rendering bug looks.
    await renderAndLoad(gstr9Client(view()));
    const leg = screen.getByTestId("gstr9-leg-gstr3b-books");
    expect(
      leg.textContent,
      "the gross row is missing on a clean year. It must always be there, or its " +
        "absence becomes a signal that means two different things",
    ).toContain("Gross divergence");
    expect(
      screen.queryByTestId("gstr9-offsetting-gstr3b-books"),
      "the offsetting note fired on a year where nothing cancels. A warning that " +
        "appears on every year is a warning nobody reads",
    ).toBeNull();
    expect(screen.getByTestId("gstr9-verdict-gstr3b-books").textContent).toContain(
      "agrees in all 12 months",
    );
  });

  it("shows a NOT_DERIVED table as a stated gap and never as zero", async () => {
    await renderAndLoad(gstr9Client(view()));
    const t7 = screen.getByTestId("gstr9-table-7");
    expect(
      t7.textContent,
      "table 7 does not say it is NOT DERIVED. A zero in 'ITC reversed' asserts " +
        "the taxpayer reversed NOTHING, when the truth is that nothing was checked",
    ).toContain("NOT DERIVED");
    expect(
      t7.textContent,
      "table 7 does not name the missing input, so a filer cannot tell what would " +
        "have to change for the figure to exist",
    ).toContain("inward statement");
    expect(
      t7.querySelectorAll("td").length,
      "a NOT_DERIVED table rendered figure cells. NOT_DERIVED means NO rows — a " +
        "zero row is a much stronger claim than 'this build cannot tell'",
    ).toBe(0);
  });

  it("suppresses table 4's total and prints the reason where the figure would be", async () => {
    await renderAndLoad(gstr9Client(view()));
    const note = screen.getByTestId("gstr9-total-suppressed-4");
    expect(
      note.textContent,
      "the suppressed total is not explained. 4N totals rows five of which are " +
        "not derived, so a figure there would be an UNDERSTATEMENT THAT FOOTS — " +
        "and a blank where a statutory figure belongs is read as zero",
    ).toContain("not derived here");
    // The omitted sub-row must be named too, or PARTIAL tells the reader less
    // than DERIVED would.
    expect(
      screen.queryByTestId("gstr9-omitted-4-4C"),
      "table 4 is PARTIAL but does not name row 4C as omitted",
    ).not.toBeNull();
  });

  it("renders every disclosure, including ones it has no description for", async () => {
    await renderAndLoad(
      gstr9Client(view({ disclosures: [...UNCONDITIONAL, "some-future-token"] })),
    );
    for (const d of UNCONDITIONAL) {
      expect(
        screen.queryByTestId(`gstr9-disclosure-${d}`),
        `the disclosure ${d} was not rendered. All of them are unconditional: a ` +
          "screen that shows the tidy tables and hides the caveats is the most " +
          "dangerous artefact this product could emit",
      ).not.toBeNull();
    }
    // ⛔ AN UNKNOWN TOKEN IS SHOWN, NOT SWALLOWED. A disclosure the screen does
    // not recognise is still a limitation of the return, and dropping it is the
    // exact failure the closed vocabulary exists to prevent.
    expect(
      screen.queryByTestId("gstr9-disclosure-some-future-token"),
      "an unrecognised disclosure was DROPPED. That is the closed-vocabulary " +
        "failure moved one layer out: the token exists precisely because a " +
        "limitation must reach the reader",
    ).not.toBeNull();
  });

  it("shows all twelve months, including the ones with nothing in them", async () => {
    await renderAndLoad(gstr9Client(view()));
    for (const fp of FPS) {
      expect(
        screen.queryByTestId(`gstr9-month-${fp}`),
        `period ${fp} is missing from the grid. A month absent from a list of ` +
          "twelve is a month nobody checked, and a reader cannot tell that from " +
          "a month in which nothing happened",
      ).not.toBeNull();
    }
  });

  it("names WHICH comparison broke in a month rather than only colouring it", async () => {
    const months = FPS.map((fp) =>
      month(fp, { itc_matches_books: fp !== "062025" }),
    );
    await renderAndLoad(gstr9Client(view({ months })));
    expect(
      screen.getByTestId("gstr9-month-062025").textContent,
      "the broken month does not say WHICH of the four comparisons failed. " +
        "'Something is wrong with June' sends an accountant through four " +
        "reconciliations to find out which",
    ).toContain("ITC ≠ books");
    expect(screen.getByTestId("gstr9-month-072025").textContent).toContain("ties");
  });

  it("refuses to load until all six tax ledgers are chosen, and says why", async () => {
    // A company whose ledgers are named so that nothing pre-selects.
    const base = createMockClient({ seed: false });
    const client = {
      ...base,
      listLedgers: vi.fn(async () => ({ ledgers: [] })),
      gstr9: vi.fn(async () => {
        throw new Error("gstr9 must not be called with an incomplete selection");
      }),
    } as unknown as CoredClient;

    await renderScreen(client);
    expect(
      screen.queryByTestId("gstr9-incomplete"),
      "the screen does not say why it will not load. Without the six ledgers the " +
        "books column reads zero for all twelve months and every period looks " +
        "wrong by exactly the tax charged — twelve findings that are all " +
        "artefacts of a missing setting",
    ).not.toBeNull();
    expect(
      (screen.getByTestId("gstr9-load") as HTMLButtonElement).disabled,
      "the load button is enabled with no ledgers chosen, so the operator can " +
        "send a request the server can only refuse",
    ).toBe(true);
  });

  it("pre-selects the six tax ledgers by name but still shows every choice", async () => {
    // ⛔ THE CONTROL FOR THE TEST ABOVE. If nothing ever pre-selected, the refusal
    // would be permanent and this screen unusable — and the test above would pass
    // for the wrong reason.
    await renderScreen(gstr9Client(view()));
    expect(
      (screen.getByTestId("gstr9-ledger-cgst") as HTMLSelectElement).value,
      "the output CGST ledger was not pre-selected from a list that contains one " +
        "called exactly 'Output CGST'",
    ).toBe("l-ocgst");
    expect(
      (screen.getByTestId("gstr9-ledger-inputIGST") as HTMLSelectElement).value,
      "the input IGST ledger was not pre-selected, or was matched to an OUTPUT " +
        "ledger — which would reconcile the year against the wrong side of the books",
    ).toBe("l-iigst");
    // ⚠️ AND THE CHOICE IS STILL SHOWN. A pre-selection is a suggestion, never a
    // silent decision: the operator must be able to see and change it.
    expect(
      (screen.getByTestId("gstr9-ledger-cgst") as HTMLSelectElement).tagName,
    ).toBe("SELECT");
    expect(
      (screen.getByTestId("gstr9-load") as HTMLButtonElement).disabled,
      "all six pre-selected and the button is still disabled",
    ).toBe(false);
  });

  it("meets a refused operator on MOUNT, before they fill the form in", async () => {
    // ⛔ THE ARM THAT CAUGHT A REAL DEFECT. Before the on-mount probe existed, a
    // refused operator saw a setup form, chose six ledgers and only met the
    // refusal when they pressed the button — which reads as "the report failed"
    // rather than "you may not read this". The browser drive found it (two arms
    // red at 31/33), not this suite.
    //
    // ⚠️ NOTE THERE IS NO CLICK HERE. renderScreen, not renderAndLoad.
    await renderScreen(gstr9Client(new ApiError(403, "forbidden")));
    expect(
      screen.queryByTestId("gstr9-denied"),
      "the refusal was not shown until the operator asked for the report. A " +
        "permission is not something an operator should have to discover by " +
        "filling a form in and pressing a button",
    ).not.toBeNull();
    expect(
      screen.queryByTestId("gstr9-load"),
      "the setup form is still on screen under a refusal, inviting an operator " +
        "to choose six ledgers for a report they cannot read",
    ).toBeNull();
  });

  it("renders a 403 as migration 0087's decision, not as an error", async () => {
    // renderScreen, not renderAndLoad: the on-mount probe has already met the
    // refusal and the load button is gone by design — see the test above.
    await renderScreen(gstr9Client(new ApiError(403, "forbidden")));
    const denied = screen.getByTestId("gstr9-denied");
    expect(
      denied.textContent,
      "the refusal does not name the rule, so an operator cannot ask for the " +
        "right thing",
    ).toContain("report:gstr9");
    expect(
      denied.textContent,
      "the refusal does not say that reversing it means DELETING the disallow. " +
        "An added allow ties at specificity 3 and a disallow wins a tie, so an " +
        "owner following the obvious advice would change nothing and not know it",
    ).toContain("DELETING");
    expect(
      screen.queryByTestId("gstr9-months"),
      "an empty month grid was drawn under a refusal, which reads as a year in " +
        "which nothing happened",
    ).toBeNull();
  });
});
