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
/**
 * The row readers, at MODULE scope so more than one describe can use them.
 *
 * They were local to the backup/restore block; T32 needed the same two, and a
 * second copy is how two readers of the same table quietly start disagreeing
 * about what "the badge" is.
 */
function badgeFor(namePattern: RegExp): string {
  const cell = screen.getByText(namePattern);
  const row = cell.closest("tr");
  return row?.firstElementChild?.textContent?.trim() ?? "";
}
function noteFor(namePattern: RegExp): string {
  return screen.getByText(namePattern).closest("tr")?.textContent ?? "";
}

describe("F11 — backup / restore shipped, split company data did not", () => {
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

/**
 * T32 — THE MIGRATE-FROM-TALLY ROW.
 *
 * ⛔ THE RULE THIS PINS is FeaturesScreen's own: a row flips in the change that
 * gives the operator a SCREEN, not the change that gave them a route. This row
 * read `state: "server"` for a whole lane and its note SAID SO — "there is NO
 * SCREEN for it — an operator cannot migrate their own books from the keyboard"
 * — which is exactly the honesty that makes flipping it meaningful now that
 * Alt+O exists and has been driven in a browser.
 */
describe("T32 — Migrate from Tally reads On, and only because a key reaches it", () => {
  it("reads On and names Tally's own key", () => {
    renderFeatures();
    expect(badgeFor(/^Migrate from Tally \(Alt\+O\)/)).toBe("On");
  });

  it("the note no longer claims there is NO SCREEN — that clause had to go", () => {
    renderFeatures();
    const note = noteFor(/^Migrate from Tally \(Alt\+O\)/);
    // ⛔ THE ONE ASSERTION THAT CANNOT BE SATISFIED BY FLIPPING THE BADGE ALONE.
    // A row reading "On" above a note saying no screen exists is worse than the
    // honest "Server only" it replaced.
    expect(note).not.toMatch(/NO SCREEN/);
    expect(note).not.toMatch(/cannot migrate their own books/);
    expect(note).toMatch(/Alt\+O/);
    // Tally's own path, so an operator coming from Tally finds the door.
    expect(note).toMatch(/Gateway of Tally > Import/);
  });

  it("...and it still carries the two things that make a migration go wrong", () => {
    renderFeatures();
    const note = noteFor(/^Migrate from Tally \(Alt\+O\)/);
    // The encoding trap: the reason the screen sends bytes and not text.
    expect(note, "the UTF-16 trap is named").toMatch(/UTF-16/);
    // And the report guidance: vouchers_posted alone is the failure mode.
    expect(note, "the stock figures are not optional reading").toMatch(
      /stock movements and opening positions/i,
    );
    expect(note, "a balanced trial balance is not proof").toMatch(
      /trial balance cannot see quantity/i,
    );
  });

  it("⛔ the rest of the hard-won note SURVIVED the edit", () => {
    renderFeatures();
    const note = noteFor(/^Migrate from Tally \(Alt\+O\)/);
    // Only the clause that stopped being true was replaced. Everything the
    // migration lane learned about real exports is still here — a wholesale
    // rewrite would have quietly dropped it, and none of it is recoverable from
    // the code.
    expect(note).toMatch(/IMPORTDATA\/REQUESTDESC\/REQUESTDATA/);
    expect(note).toMatch(/PARTYGSTIN/);
    expect(note).toMatch(/ACTION=Alter/);
    expect(note).toMatch(/Stock Summary/);
  });
});

/**
 * B1 — THE EXPORT ROW WAS SPLIT, NOT FLIPPED.
 *
 * It read "Export: e-mail a document, and export to PDF — Not built". PDF export
 * shipped; e-mail did not, and is blocked on a product decision rather than on
 * work. Flipping the joint row to On would have claimed e-mail works — the exact
 * failure this screen exists to prevent, and the same shape as the backup /
 * split-company row above.
 */
describe("B1 — export to PDF shipped, e-mailing a document did not", () => {
  it("the old JOINT row is GONE — a half-true row is the failure mode", () => {
    renderFeatures();
    expect(
      screen.queryByText(/^Export: e-mail a document, and export to PDF$/),
    ).toBeNull();
  });

  it("PDF export reads On and names the key", () => {
    renderFeatures();
    expect(badgeFor(/^Export to PDF \(Alt\+E\)/)).toBe("On");
  });

  it("⛔ ...and its note says the PDF is NOT the rows on screen", () => {
    renderFeatures();
    const note = noteFor(/^Export to PDF \(Alt\+E\)/);
    // The one thing an operator must know: Alt+E's other formats write what is
    // displayed; this one is the server's whole statement.
    expect(note).toMatch(/NOT the rows on screen/i);
    expect(note).toMatch(/WHOLE statement/i);
    // And that it is offered only where a layout exists, with the 501 named.
    expect(note).toMatch(/ten\s+of the thirty/i);
    expect(note).toMatch(/501/);
    // ⛔ AND THAT THE LIST IS GENERATED. A row claiming a hand-maintained list is
    // accurate is the claim this repository has had to withdraw three times.
    expect(note).toMatch(/generates it by asking the router|cannot drift/i);
  });

  it("⛔ the OLD e-mail row name is gone — it was never an export format", () => {
    renderFeatures();
    // It shipped as Alt+M, its own key, because Tally's own top menu carries
    // `E: Export` and `M: E-mail` as two separate entries. Keeping "Export:" in
    // the name would have re-asserted the framing the implementation rejected.
    expect(screen.queryByText(/^Export: e-mail a document$/)).toBeNull();
  });
});

/**
 * B2 — E-MAIL A DOCUMENT READS ON, AND MUST NOT READ AS SMTP.
 *
 * ⛔ WHAT THIS BLOCK EXISTS TO PREVENT is the tempting version: flip the badge to
 * On and leave a row that any reader takes to mean this product sends mail. It
 * does not — there is no SMTP anywhere and internal/notify is untouched — so the
 * badge is earned by a KEY, and the row's own NAME has to say which mechanism.
 *
 * It also pins the two limits an operator meets in the first minute: the flow is
 * two steps and the second is theirs, and the draft has no recipient.
 */
describe("B2 — e-mail a document reads On, and names the mechanism", () => {
  const ROW = /^E-mail a document \(Alt\+M/;

  it("reads On and names Tally's own key", () => {
    renderFeatures();
    expect(badgeFor(ROW)).toBe("On");
  });

  it("⛔ the row NAME says it opens YOUR mail client, so On cannot read as SMTP", () => {
    renderFeatures();
    const name = screen.getByText(ROW).textContent ?? "";
    // ⛔ THE ASSERTION THAT CANNOT BE SATISFIED BY FLIPPING THE BADGE ALONE. A
    // green badge over a bare "E-mail a document" claims a transport that does
    // not exist anywhere in this product.
    expect(
      name,
      "the row reads On without naming the mechanism, so it claims this product sends mail",
    ).toMatch(/opens your mail client/i);
    expect(name).toMatch(/Alt\+M/);
  });

  it("⛔ ...and the note says the flow is TWO STEPS and the attachment is the operator's", () => {
    renderFeatures();
    const note = noteFor(ROW);
    // The one thing that goes wrong: a draft sent with no invoice on it.
    expect(note).toMatch(/ATTACH THE SAVED PDF BEFORE YOU SEND/i);
    expect(note).toMatch(/cannot put a file on a mail draft/i);
  });

  it("⛔ ...and that the draft has NO RECIPIENT, because no party e-mail is stored", () => {
    renderFeatures();
    const note = noteFor(ROW);
    // ⚠️ THIS CORRECTS THE PLAN, which described a "pre-addressed" draft. Measured
    // over the whole schema: no ledger carries an e-mail address.
    expect(note).toMatch(/NO RECIPIENT/i);
    expect(note).toMatch(/no party e-mail address anywhere/i);
  });

  it("⛔ ...and that this product still does not send mail", () => {
    renderFeatures();
    const note = noteFor(ROW);
    expect(note).toMatch(/STILL DOES NOT SEND MAIL/i);
    // The outbox exists and is good — saying so is what stops the next reader
    // rebuilding it. These clauses were NOT rewritten when the row flipped.
    expect(note).toMatch(/internal\/notify/);
    expect(note).toMatch(/LogSender/);
    // ⛔ And the rule that must not be broken if a transport is ever added.
    expect(note).toMatch(/HALF-SEND/i);
  });
});

/**
 * TRACK C — THE PRINT-TEMPLATES ROW, AND THE WORD THAT HAD TO GO.
 *
 * ⛔ WHAT THIS BLOCK EXISTS TO PREVENT is the tempting version of this change:
 * ship the fields that exist, flip the badge to "On", and leave the row's name
 * promising a LOGO. Three of the four things the old name listed are columns in
 * `print_template`; a logo is not, and there is no image storage anywhere in this
 * product — no column, no object store, no upload route. So a green badge over
 * that name would advertise a feature with nowhere to put its data, which is the
 * precise failure this whole screen exists to prevent.
 *
 * The row's own doctrine is also pinned: it flips in the change that gives the
 * operator a SCREEN, not the change that gave them a route. The route, the
 * override table and the printer's honouring of it all existed for months while
 * this row correctly read "Not built".
 */
describe("track C — Per-company print templates reads On, and promises no logo", () => {
  const ROW = /^Per-company print templates/;

  it("reads On, because a screen reaches it and not merely a route", () => {
    renderFeatures();
    expect(badgeFor(ROW)).toBe("On");
  });

  it("⛔ the row NAME no longer promises a logo", () => {
    renderFeatures();
    const name = screen.getByText(ROW).textContent ?? "";
    // ⛔ THE ONE ASSERTION THAT CANNOT BE SATISFIED BY FLIPPING THE BADGE ALONE,
    // and the mutation §1.2 of the handoff asks for: leave "logo" in the name and
    // this goes red naming it. A row reading "On" while its own name lists a
    // field the product cannot store is worse than the honest "Not built" it
    // replaced.
    expect(
      name,
      "the row name still promises a logo, and there is no image storage anywhere " +
        "in this product to put one in",
    ).not.toMatch(/logo/i);
    // And it names what DOES exist, so the rename is not just a deletion.
    expect(name).toMatch(/letterhead/i);
    expect(name).toMatch(/declaration/i);
  });

  it("the note says a logo is absent and WHY, rather than going quiet about it", () => {
    renderFeatures();
    const note = noteFor(ROW);
    // Dropping the word from the name and saying nothing would leave an operator
    // who remembers the old row wondering whether the logo moved somewhere.
    expect(note).toMatch(/NO LOGO/i);
    expect(note).toMatch(/image storage/i);
  });

  it("the note no longer claims nothing writes an override — that clause had to go", () => {
    renderFeatures();
    const note = noteFor(ROW);
    // The old note's honest core: "Nothing in this application yet writes an
    // override, so every company prints the built-in layout." That is what
    // stopped being true.
    expect(note).not.toMatch(/Not built as a UI/);
    expect(note).not.toMatch(/Nothing in this application yet writes an override/);
    // And it names the door, so an operator can find the screen.
    expect(note).toMatch(/Gateway of Tally > Print Templates/);
  });

  it("...and it keeps the two facts that make the feature usable rather than surprising", () => {
    renderFeatures();
    const note = noteFor(ROW);
    // An empty box prints the built-in default — the reason the form shows the
    // default beneath each field rather than leaving a blank that reads as
    // "this company has no declaration".
    expect(note).toMatch(/built-in default/i);
    // ⛔ AND THE RAGGED GRID. A report honours three of the seven fields, and an
    // operator who set a declaration on the invoice and then picked Report must
    // not conclude the screen is broken.
    expect(note, "the note says a report ignores some fields").toMatch(
      /REPORT reads only the title and the letterhead lines/i,
    );
  });
});

/**
 * ⭐⭐ T8 STEP 8 — MULTI-CURRENCY READS "Part built", AND THE STATE IS NEW.
 *
 * ⛔ THE MUTATION THIS PINS IS THE TEMPTING ONE: flip the row to "On" and move on.
 * Q4 of the design memo asks for `partial` BY NAME, and the reason is the failure
 * this whole screen exists to prevent — an operator who reads "On" believes the
 * unrealised difference is posted, and it is not. These assertions go red on it.
 */
describe("T8 step 8 — Multi-currency reads Part built, not On and not Not built", () => {
  it("carries the NEW badge, and it is not the flattering one", () => {
    renderFeatures();
    expect(badgeFor(/^Multi-currency$/)).toBe("Part built");
  });

  it("names the missing part in the note, which is what makes `partial` honest", () => {
    renderFeatures();
    const note = noteFor(/^Multi-currency$/);
    // Q4's own words, required on the screen.
    expect(note).toMatch(/REPORTED AND NOT POSTED/);
    // ⛔ AND THE NAMED GAP. "Part built" with a vague note is "Not built" with
    // better marketing, which is the doctrine this file's header states.
    expect(note).toMatch(/REVALUATION VOUCHER/);
    // ⭐ AND WHAT THE OPERATOR CAN NOW DO, with the key that reaches it — the rule
    // this screen holds itself to is that a row flips in the change that gives
    // them a SCREEN.
    expect(note).toMatch(/Ctrl\+H/);
    expect(note).toMatch(/Alt\+G > Create Currency/);
    // ⛔ AND THE LIMITATION THAT IS A DECISION RATHER THAN AN OMISSION.
    expect(note).toMatch(/DOUBLE ENTRY ONLY/);
  });

  it("the LEGEND explains the new state, or the badge is a word with no meaning", () => {
    renderFeatures();
    // A fifth badge nobody defined is worse than four: an operator cannot tell
    // "Part built" from "Server only" without being told.
    expect(screen.getByText(/PART BUILT \(usable from the keyboard/)).toBeTruthy();
  });

  it("the note no longer claims the row STAYS OFF — that clause had to go", () => {
    renderFeatures();
    const note = noteFor(/^Multi-currency$/);
    // ⛔ THE ONE ASSERTION THAT CANNOT BE SATISFIED BY FLIPPING THE BADGE ALONE,
    // and it caught a real defect: step 8 flipped the row to `partial` and left
    // the step-4 paragraph BELOW it reading "⛔ NO VOUCHER SCREEN OFFERS A
    // CURRENCY FIELD, so this row stays off". A badge reading "Part built" above
    // a note saying the row is off is the same failure the T32 and print-template
    // rows were each caught in — a note is not a changelog that only grows, and
    // the clause a later wave falsifies has to GO, not sit further down the page.
    expect(note).not.toMatch(/so this row stays off/);
    expect(note).not.toMatch(/NO VOUCHER SCREEN OFFERS A CURRENCY FIELD/);
    // ⭐ AND THE HISTORY SURVIVES THE CORRECTION rather than being deleted: the
    // build order is WHY the field came last, and that reasoning is still worth
    // an operator's time.
    expect(note).toMatch(/step 8 is what closed that/);
    expect(note).toMatch(/worse than no form/);
  });

  it("...and the CURRENCY-MASTERS note no longer says nothing can be keyed", () => {
    renderFeatures();
    const note = noteFor(
      /^Currency masters and exchange rates \(Alt\+G > Create Currency\)/,
    );
    // ⛔ THE SECOND STALE CLAUSE, ON A DIFFERENT ROW. This one read "WHAT IT IS
    // FOR RIGHT NOW: nothing you can key … no voucher screen offers a currency
    // yet" — true when the masters shipped a wave early, false the moment step 8
    // gave the grid its fields. ⚠️ A ROW READING "On" IS NOT ENOUGH: this row read
    // On the whole time, so only the note could carry the lie.
    // ⚠️ THE LIVE CLAIM, NOT THE QUOTATION OF IT. The note QUOTES its own old
    // wording on purpose — the same treatment the double-entry row's 2026-08-06
    // overstatement got, because a correction that deletes the error leaves the
    // next reader unable to tell a fixed claim from one never made. So these match
    // the ASSERTING forms, which is what would actually mislead an operator.
    expect(note).not.toMatch(/WHAT IT IS FOR RIGHT NOW: nothing you can key/);
    expect(note).not.toMatch(/; no voucher screen offers a currency yet/);
    // ⭐ AND THE QUOTED HISTORY IS REQUIRED, not merely tolerated.
    expect(note).toMatch(/THIS SENTENCE READ 'nothing you can key' UNTIL T8 STEP 8/);
    // ⭐ AND IT NOW POINTS AT WHAT THE MASTERS ARE FOR — this screen is the switch
    // that makes the currency fields appear at all, which is the one thing an
    // operator who cannot find them needs to be told.
    expect(note).toMatch(/WHAT IT IS FOR: keying a foreign voucher/);
    expect(note).toMatch(/HIDDEN UNTIL A CURRENCY EXISTS/);
  });

  it("the currency-masters row is still its OWN row and still reads On", () => {
    renderFeatures();
    // ⭐ THE CONTROL FOR THE DECISION NOT TO SPLIT FURTHER. Step 3 split this one
    // off for a real reason and it stays split; step 8 added a STATE rather than
    // four more rows, and this asserts the existing split was not collapsed on
    // the way.
    expect(
      badgeFor(/^Currency masters and exchange rates \(Alt\+G > Create Currency\)/),
    ).toBe("On");
  });
});

/**
 * ⭐⭐ T8 STEP 8 — THE FOREX GAIN/LOSS LEDGER PANEL.
 *
 * ⛔ IT IS THE SETTING STEP 5's OWN REFUSAL NAMES: *"set
 * company_forex_settings.forex_gain_loss_ledger_id (Gateway > F11 >
 * Multi-currency)"*. That path did not exist — an operator following the
 * instruction arrived at a row of prose — and a refusal an operator cannot act on
 * is an outage.
 */
describe("T8 step 8 — the forex gain/loss ledger panel", () => {
  it("says NONE CHOSEN when nothing is set, and what that means", async () => {
    renderFeatures();
    await waitFor(() =>
      expect(screen.getByTestId("forex-ledger-state")).toBeTruthy(),
    );
    // ⛔ "NONE CHOSEN" AND WHAT IT COSTS, not a blank. A settlement that would
    // realise a difference is REFUSED until one is chosen, and a panel that
    // showed an empty field would leave an operator hunting for the reason.
    expect(screen.getByTestId("forex-ledger-state").textContent).toMatch(
      /NONE CHOSEN — a realised difference will be refused/,
    );
  });

  it("offers only revenue and expense ledgers, never a party or a bank", async () => {
    renderFeatures();
    await waitFor(() =>
      expect(screen.getByTestId("forex-ledger-pick")).toBeTruthy(),
    );
    const options = Array.from(
      screen.getByTestId("forex-ledger-pick").querySelectorAll("option"),
    ).map((o) => o.textContent ?? "");
    // ⛔ A GAIN CREDITED TO A CUSTOMER'S OWN LEDGER makes a settled bill show a
    // residue for ever. The server refuses it; the picker does not offer it.
    expect(options.some((o) => /Sundry Debtors|Sundry Creditors/.test(o))).toBe(
      false,
    );
    expect(options.some((o) => /Bank Accounts|Cash-in-Hand/.test(o))).toBe(false);
  });

  it("says the UNREALISED difference is a report and not this setting", async () => {
    renderFeatures();
    await waitFor(() =>
      expect(screen.getByTestId("forex-ledger-state")).toBeTruthy(),
    );
    // ⚠️ THE TWO ARE EASY TO CONFLATE AND THE PANEL SEPARATES THEM. This ledger
    // is where a REALISED difference posts; the unrealised one posts nowhere.
    // ⚠️ SCOPED TO THE PANEL. The phrase legitimately appears in the roster note
    // too, and `getByText` across the whole screen would be an assertion about
    // which of the two happened to render — not about the panel saying it.
    const panel = screen
      .getByTestId("forex-ledger-state")
      .closest("div")?.textContent ?? "";
    expect(panel).toMatch(/REALISED exchange gain or loss/);
    expect(panel).toMatch(/REPORTED and NOT POSTED/);
    expect(panel).toMatch(/Alt\+G > Unadjusted Forex Gain\/Loss/);
  });
});
