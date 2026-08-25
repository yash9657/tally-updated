import { describe, it, expect, afterEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  waitFor,
} from "@testing-library/react";

import { FeaturesScreen } from "./FeaturesScreen";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient } from "../api";

/**
 * F11 > TDS Deductor Details — Item Zero step 2, layer 5.
 *
 * The rest of this screen is a static statement of fact and needs no test. The
 * panel does: it is the ONLY way an operator can record the s.402(11)/(37)
 * status, and without that row the TDS post path refuses every voucher.
 *
 * WHAT THESE TESTS CANNOT PROVE, said plainly: the mock client does not derive
 * TDS, so nothing here exercises the refusal the panel exists to clear. That is
 * covered server-side by TestRecordingTheStatusUnblocksTDSPosting, which drives
 * the real post path over HTTP.
 */

afterEach(cleanup);

function renderFeatures() {
  return render(
    <CompanyProvider
      client={createMockClient()}
      initialCompany={{ id: "company-demo", name: "Demo Traders" }}
    >
      <FeaturesScreen />
    </CompanyProvider>,
  );
}

describe("F11 TDS Deductor Details", () => {
  it("says nothing is recorded, and warns that TDS will be refused", async () => {
    renderFeatures();
    expect(
      await screen.findByText(/Nothing recorded yet .* will be refused/),
    ).toBeTruthy();
  });

  it("records a status and states which year it GOVERNS, not the year keyed", async () => {
    renderFeatures();
    await screen.findByLabelText("Deductor type");

    fireEvent.change(screen.getByLabelText("Tax year beginning"), {
      target: { value: "2025" },
    });
    fireEvent.change(screen.getByLabelText("Deductor type"), {
      target: { value: "COMPANY" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Record" }));

    // THE ASSERTION THAT MATTERS. A 2025-26 row governs payments made in
    // 2026-27 (s.402(37)(b), "the tax year immediately preceding"). An operator
    // who is not told this keys the payment's own year and gets a refusal that
    // looks like a bug.
    await waitFor(() =>
      expect(
        screen.getByText(
          /Recorded for 2025-26 .* governs payments made in 2026-27/,
        ),
      ).toBeTruthy(),
    );
    // And it appears in the list with the same statement.
    expect(screen.getByText(/governs payments in 2026-27/)).toBeTruthy();
  });

  it("asks for turnover ONLY for the categories the test can reach", async () => {
    renderFeatures();
    const picker = await screen.findByLabelText("Deductor type");

    // A COMPANY is a specified and designated person whatever it turned over,
    // so the fields must be ABSENT — not merely optional. A figure typed
    // against a COMPANY is a 400, so offering the box would invite the error.
    fireEvent.change(picker, { target: { value: "COMPANY" } });
    expect(screen.queryByLabelText("Prior-year turnover")).toBeNull();

    // An INDIVIDUAL takes the turnover test, so both operands appear —
    // BUSINESS and PROFESSION carry different limits, so the activity is asked
    // for rather than inferred.
    fireEvent.change(picker, { target: { value: "INDIVIDUAL" } });
    expect(screen.getByLabelText("Prior-year turnover")).toBeTruthy();
    expect(screen.getByLabelText("Business or profession")).toBeTruthy();
  });

  it("surfaces the server's refusal instead of failing silently", async () => {
    renderFeatures();
    const picker = await screen.findByLabelText("Deductor type");

    // An INDIVIDUAL with no turnover: the row cannot be evaluated, and the
    // server says which fact is missing. Reaching the form past the conditional
    // fields is done by picking the category and then clearing the box.
    fireEvent.change(picker, { target: { value: "INDIVIDUAL" } });
    fireEvent.change(screen.getByLabelText("Prior-year turnover"), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Record" }));

    await waitFor(() =>
      expect(screen.getByText(/subject to the turnover test/)).toBeTruthy(),
    );
  });

  it("still renders the honest feature inventory around the panel", async () => {
    renderFeatures();
    // ⚠️ THIS COMMENT WAS STALE AND THE ASSERTION COULD NOT CATCH IT.
    //
    // It said 'The TDS row stays "Not built": there is no register and no Form
    // 140' long after both shipped — and the assertion below is
    // getAllByText("Not built"), which is not row-specific, so it kept passing
    // while the comment described a product that no longer existed. It would have
    // gone on passing after the TDS row flipped to "On", which is the whole
    // problem: an assertion that cannot fail is a comment with a cost.
    //
    // So it now names its row. TDS / TCS is "On" (reachable by key, driven in a
    // browser), and the row that still carries "Not built" is asserted BY NAME
    // rather than by counting labels.
    expect(await screen.findByText("TDS / TCS")).toBeTruthy();
    const tdsRow = screen.getByText("TDS / TCS").closest("tr, li, div");
    expect(tdsRow?.textContent).toContain("On");
    expect(tdsRow?.textContent).not.toContain("Not built");
    // Some row must still be honestly unbuilt, or this screen has stopped being
    // an inventory and become an advertisement.
    expect(screen.getAllByText("Not built").length).toBeGreaterThan(0);
  });
});

/**
 * F11 > s.394(2) manufacturing declarations — Item Zero step 3, layer 5.
 *
 * Same caveat as above, and it matters more here: the mock does not derive TCS,
 * so nothing in this file proves a filed declaration actually exempts a sale.
 * TestFilingADeclarationExemptsTheNextSale does that server-side, over HTTP.
 * What these tests DO cover is the part that lives only in the client — whether
 * the operator is told what the missing-PAN case costs their buyer.
 */
describe("F11 s.394(2) declarations", () => {
  it("says none are held, and that every sale therefore collects", async () => {
    renderFeatures();
    expect(
      await screen.findByText(/None held .* collects at the normal rate/),
    ).toBeTruthy();
  });

  it("WARNS BEFORE FILING that an empty PAN triggers the penal rate", async () => {
    renderFeatures();
    await screen.findByLabelText("Buyer PAN");

    // ⚠️ THE ASSERTION THAT MATTERS ON THIS SCREEN. An operator transcribing a
    // document has no way to know that leaving the PAN blank charges their
    // buyer 5% instead of 2% — worse than filing nothing. The warning must be
    // visible BEFORE the mistake, so it renders on the empty field rather than
    // in a response.
    expect(
      screen.getByText(/s.397\(2\)\(f\)\(i\) makes it INVALID/),
    ).toBeTruthy();
    expect(screen.getByText(/worse for them than filing nothing/)).toBeTruthy();

    // ...and it goes away once a PAN is entered.
    fireEvent.change(screen.getByLabelText("Buyer PAN"), {
      target: { value: "AAAPZ1234C" },
    });
    await waitFor(() =>
      expect(
        screen.queryByText(/worse for them than filing nothing/),
      ).toBeNull(),
    );
  });

  it("files a declaration and shows the s.394(3) forwarding deadline", async () => {
    renderFeatures();
    const buyerPicker = await screen.findByLabelText("Buyer");
    // The mock company seeds ledgers; pick whichever debtor it offers.
    const options = Array.from(
      buyerPicker.querySelectorAll("option"),
    ) as HTMLOptionElement[];
    const buyer = options.find((o) => o.value !== "");
    if (!buyer) throw new Error("mock company offered no ledgers to pick");

    fireEvent.change(buyerPicker, { target: { value: buyer.value } });
    fireEvent.change(screen.getByLabelText("Declaration date"), {
      target: { value: "2026-04-01" },
    });
    fireEvent.change(screen.getByLabelText("Received date"), {
      target: { value: "2026-04-28" },
    });
    fireEvent.change(screen.getByLabelText("Buyer PAN"), {
      target: { value: "AAAPZ1234C" },
    });
    fireEvent.click(screen.getByRole("button", { name: "File" }));

    // Received 28 April -> a copy is due to the Commissioner by 7 MAY. Storing
    // the document without surfacing this would build a compliance trap.
    await waitFor(() => expect(screen.getByText(/by 2026-05-07/)).toBeTruthy());
    expect(screen.getByText(/now exempt/)).toBeTruthy();
  });

  it("refuses a declaration against motor vehicles the way the statute does", async () => {
    renderFeatures();
    await screen.findByLabelText("Section");
    // s.394(2) reaches Sl. Nos. 1-5; motor vehicles are Sl. No. 6, so the
    // picker must not even offer them.
    const sectionPicker = screen.getByLabelText("Section");
    const values = Array.from(sectionPicker.querySelectorAll("option")).map(
      (o) => (o as HTMLOptionElement).value,
    );
    expect(values).not.toContain("206C(1F)-MV");
    expect(values).toContain("206C(1)-SCRAP");
  });
});

/**
 * T14 — THE ROW THAT WAS SPLIT IN TWO.
 *
 * It read "Backup / restore, split company data — Not built". Backup and restore
 * now exist; split does not and is not going to. Flipping the single row to "On"
 * would have absorbed a missing feature into a green badge, which is the exact
 * failure this whole screen exists to prevent — so the row was SPLIT, and the
 * half that did not ship still says so with its reason.
 *
 * ⛔ THE MUTATION THIS PINS is the tempting one-line change: flip the old row to
 * "On" and move on. These assertions go red on it.
 */
describe("F11 — backup / restore shipped, split company data did not", () => {
  function badgeFor(namePattern: RegExp): string {
    const cell = screen.getByText(namePattern);
    const row = cell.closest("tr");
    return row?.firstElementChild?.textContent?.trim() ?? "";
  }
  function noteFor(namePattern: RegExp): string {
    return screen.getByText(namePattern).closest("tr")?.textContent ?? "";
  }

  it("the old combined row is GONE — a half-true row is the failure mode", () => {
    renderFeatures();
    expect(screen.queryByText(/^Backup \/ restore, split company data$/)).toBeNull();
  });

  it("backup / restore reads On, and names the key that reaches it", () => {
    renderFeatures();
    expect(badgeFor(/^Backup \/ restore \(Alt\+Y\)/)).toBe("On");
    // The rule this screen holds itself to: a row flips in the change that gives
    // the operator a SCREEN, not the change that gave them a route.
    expect(noteFor(/^Backup \/ restore \(Alt\+Y\)/)).toMatch(/Alt\+Y/);
  });

  it("...and its note states the two consequences an operator must know first", () => {
    renderFeatures();
    const note = noteFor(/^Backup \/ restore \(Alt\+Y\)/);
    expect(note, "a restore creates a company rather than replacing one").toMatch(
      /CREATES A NEW COMPANY AND NEVER REPLACES ONE/,
    );
    expect(note, "an archive does not re-grant access").toMatch(/privilege-escalation/);
  });

  it("⛔ split company data STILL reads Not built, with the reason", () => {
    renderFeatures();
    expect(badgeFor(/^Split company data/)).toBe("Not built");
    const note = noteFor(/^Split company data/);
    // "Not built" alone reads as a gap. The claim is that the limit split works
    // around is Tally's and not this product's — so the row has to say it.
    expect(note).toMatch(/DELIBERATELY/);
    expect(note).toMatch(/memory|RAM/i);
    expect(note).toMatch(/multi-year/i);
  });
});
