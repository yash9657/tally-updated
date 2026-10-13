import { describe, it, expect, afterEach, vi } from "vitest";
import { useState } from "react";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";

import { CompanyProvider } from "../../app/CompanyContext";
import { ApiError } from "../../api";
import type {
  CoredClient,
  PartyOpenBillsResponse,
  VoucherClass,
} from "../../api";
import { BillAllocationPicker } from "./BillAllocationPicker";
import { emptyBillRef, type BillBearingLeg, type DraftBillRef } from "./billAllocation";

/**
 * NAMING THE BILL A CREDIT NOTE ADJUSTS — the defect that made a whole document
 * class unprintable.
 *
 * ⛔⛔ WHAT WAS WRONG. The "Agst Ref" row was a <select> whose options came from
 * `useOpenBills(..., mode === "settle")`. A credit note is keyed on the invoice
 * screens, so its panel is in mode "raise" — the fetch never ran, the only option
 * was the empty "— bill —", and there was no free-text fallback anywhere. A
 * credit note that names no original invoice is REFUSED BY THE PRINTER (CGST s.34
 * with rule 53(1A): the original number and date are required particulars), so a
 * credit note keyed in this app could never be printed. Driven: 422.
 *
 * ⚠️ AND THE CLOSED LIST COULD NOT HAVE FIXED IT ON ITS OWN. The ageing report
 * drops a bill that nets to zero, so a PAID invoice — which is exactly what a
 * credit note usually adjusts — is never in it. The name is now an input with the
 * open bills as suggestions, and the mismatch is reported rather than prevented.
 *
 * ⛔ EVERY TEST HERE CARRIES ITS CONTROL. "The open bills were fetched" is
 * consistent with a panel that fetches on every sale, which would put the ageing
 * report on the critical path of ordinary invoicing; so the laziness is asserted
 * beside it.
 */

afterEach(cleanup);

const LEG: BillBearingLeg = { ledgerId: "led-acme", drCr: "CR", amount: "1180.0000" };

/**
 * ⛔ THE FIXTURE IS ONE PARTY, WITH NO TOTALS AND NO AGEING, BECAUSE THAT IS NOW
 * WHAT THE WIRE CARRIES (D97). The picker used to fetch the whole ageing report
 * through `report:outstandings` and narrow it to one `ledger_id` in the browser —
 * which is how a level refused `report:bills-payable` still read every figure in
 * the payables book, and which meant rendering one supplier's list shipped every
 * other party's bills to the client. It now reads
 * `/vouchers/{class}/open-bills/{ledgerID}`, authorised as `display <class>`.
 */
const OPEN_BILLS: PartyOpenBillsResponse = {
  kind: "RECEIVABLE",
  ledger_id: "led-acme",
  ledger: "Acme Traders",
  group: "Sundry Debtors",
  pending: "11800.0000",
  found: true,
  bills: [
    {
      ref_name: "Sales/2026-27/7",
      pending: "11800.0000",
      due_date: "2026-07-19",
      overdue_days: 20,
      slab: "0-45 days",
      on_account: false,
      currency_code: "",
      foreign_pending: "0.0000",
      currency_conflict: false,
    },
  ],
};

/**
 * The picker with its refs held in state, exactly as VoucherScreen holds them —
 * the input is controlled, so a harness that dropped onChange would make every
 * keystroke a no-op and prove nothing.
 */
function Harness({
  client,
  mode,
  initial,
  onRefs,
  voucherClass = "receipt",
  forex,
}: {
  client: Partial<CoredClient>;
  mode: "raise" | "settle";
  initial: DraftBillRef[];
  onRefs?: (refs: DraftBillRef[]) => void;
  voucherClass?: VoucherClass;
  /** T8 step 8 — the LEG's currency and rate, which the picker inherits. */
  forex?: { currencyId: string; currencyCode: string; rate: string };
}) {
  function Inner() {
    const [refs, setRefs] = useState<DraftBillRef[]>(initial);
    return (
      <BillAllocationPicker
        // ⛔ THE CLASS IS THE AUTHORITY THE OPEN-BILL LIST IS READ UNDER (D97), and
        // "receipt" is the honest default for this harness: every settle-mode case
        // below is a Receipt. It is a required prop precisely so a screen cannot
        // reach the picker without saying which `display <class>` rule cored should
        // decide the read on.
        voucherClass={voucherClass}
        leg={LEG}
        refs={refs}
        mode={mode}
        kind="RECEIVABLE"
        label="Acme Traders"
        forex={forex}
        onChange={(next) => {
          setRefs(next);
          onRefs?.(next);
        }}
      />
    );
  }
  return (
    <CompanyProvider
      client={client as CoredClient}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <Inner />
    </CompanyProvider>
  );
}

/** An AGAINST reference the operator has begun keying. */
function againstRef(refName = ""): DraftBillRef {
  return { ...emptyBillRef("AGAINST"), refName, amount: "1180.0000" };
}

describe("a settlement keyed on a RAISE-mode voucher — the credit note", () => {
  it("fetches the party's open bills and offers them as suggestions", async () => {
    const getPartyOpenBills = vi.fn(async () => OPEN_BILLS);
    render(
      <Harness
        client={{ getPartyOpenBills }}
        mode="raise"
        initial={[againstRef()]}
      />,
    );

    await waitFor(() =>
      // ⛔ THE CLASS AND THE LEDGER ARE BOTH IN THE CALL, and they are what the
      // row turned on: the class is the AUTHORITY cored decides the read on, and
      // the ledger is what makes the route party-scoped so there is no unscoped
      // form of it to serve.
      expect(getPartyOpenBills).toHaveBeenCalledWith(
        "co-1",
        "receipt",
        "led-acme",
        "RECEIVABLE",
      ),
    );
    // The bill reaches the DOM as a suggestion the operator can pick.
    await waitFor(() =>
      expect(
        screen
          .getByTestId("billrefs-openbills")
          .querySelector('option[value="Sales/2026-27/7"]'),
      ).toBeTruthy(),
    );
    // And the name is KEYABLE, which is the half a <select> could never do.
    expect(
      (screen.getByTestId("billrefs-bill:0") as HTMLInputElement).tagName,
    ).toBe("INPUT");
  });

  it("⛔ CONTROL: an ordinary sale — one NEW reference — never touches the ageing report", async () => {
    const getPartyOpenBills = vi.fn(async () => OPEN_BILLS);
    render(
      <Harness
        client={{ getPartyOpenBills }}
        mode="raise"
        initial={[{ ...emptyBillRef("NEW"), amount: "1180.0000" }]}
      />,
    );

    // Give the effect every chance to fire before asserting an absence.
    await screen.findByTestId("billrefs-autoname:0");
    expect(getPartyOpenBills).not.toHaveBeenCalled();
    // There is nothing to type into either: a NEW reference is named by the server.
    expect(screen.queryByTestId("billrefs-bill:0")).toBeNull();
  });

  it("accepts a bill the report does NOT list — a settled invoice — and says so", async () => {
    const getPartyOpenBills = vi.fn(async () => OPEN_BILLS);
    const seen: DraftBillRef[][] = [];
    render(
      <Harness
        client={{ getPartyOpenBills }}
        mode="raise"
        initial={[againstRef()]}
        onRefs={(r) => seen.push(r)}
      />,
    );
    await waitFor(() => expect(getPartyOpenBills).toHaveBeenCalled());

    fireEvent.change(screen.getByTestId("billrefs-bill:0"), {
      target: { value: "Sales/2025-26/3" },
    });

    // It is CARRIED, not swallowed: this is the name that goes on the wire and
    // that the printed note cites as the original invoice.
    await waitFor(() =>
      expect(seen.at(-1)?.[0]?.refName).toBe("Sales/2025-26/3"),
    );
    // ⛔ AND THE MISMATCH IS STATED. Outstandings matches a settlement by NAME
    // and ignores the type, so a typo does not fail — it opens a second bill.
    const note = await screen.findByTestId("billrefs-unlisted:0");
    expect(note.textContent).toContain("is not among Acme Traders");
    expect(note.textContent).toContain("OPENS a bill instead of closing one");
  });

  it("⛔ CONTROL: a bill that IS listed draws no complaint, and pre-fills capped at the line", async () => {
    const getPartyOpenBills = vi.fn(async () => OPEN_BILLS);
    const seen: DraftBillRef[][] = [];
    render(
      <Harness
        client={{ getPartyOpenBills }}
        mode="raise"
        initial={[{ ...emptyBillRef("AGAINST"), amount: "" }]}
        onRefs={(r) => seen.push(r)}
      />,
    );
    await waitFor(() => expect(getPartyOpenBills).toHaveBeenCalled());

    fireEvent.change(screen.getByTestId("billrefs-bill:0"), {
      target: { value: "Sales/2026-27/7" },
    });

    await waitFor(() => expect(seen.at(-1)?.[0]?.refName).toBe("Sales/2026-27/7"));
    // The bill owes 11,800 and this credit note is for 1,180: the reference is
    // capped at what is LEFT ON THIS LINE, never at the bill's whole pending.
    expect(seen.at(-1)?.[0]?.amount).toBe("1180.0000");
    expect(screen.queryByTestId("billrefs-unlisted:0")).toBeNull();
  });

  it("keeps the name keyable when the level may not read the ageing report", async () => {
    const getPartyOpenBills = vi.fn(async () => {
      throw new ApiError(403, "disallowed");
    });
    const seen: DraftBillRef[][] = [];
    render(
      <Harness
        client={{ getPartyOpenBills }}
        mode="raise"
        initial={[againstRef()]}
        onRefs={(r) => seen.push(r)}
      />,
    );

    // The message has always promised hand-keying; until the input existed that
    // promise was false.
    await screen.findByTestId("billrefs-denied");
    fireEvent.change(screen.getByTestId("billrefs-bill:0"), {
      target: { value: "INV-17" },
    });
    await waitFor(() => expect(seen.at(-1)?.[0]?.refName).toBe("INV-17"));
    // With no list to check against, silence is the honest answer.
    expect(screen.queryByTestId("billrefs-unlisted:0")).toBeNull();
  });
});

describe("settle mode is unchanged by the fix", () => {
  it("still lists the open bills and still fills the amount from the chosen bill", async () => {
    const getPartyOpenBills = vi.fn(async () => OPEN_BILLS);
    const seen: DraftBillRef[][] = [];
    render(
      <Harness
        client={{ getPartyOpenBills }}
        mode="settle"
        initial={[{ ...emptyBillRef("AGAINST"), amount: "" }]}
        onRefs={(r) => seen.push(r)}
      />,
    );

    await waitFor(() =>
      expect(getPartyOpenBills).toHaveBeenCalledWith(
        "co-1",
        "receipt",
        "led-acme",
        "RECEIVABLE",
      ),
    );
    fireEvent.change(screen.getByTestId("billrefs-bill:0"), {
      target: { value: "Sales/2026-27/7" },
    });
    await waitFor(() => expect(seen.at(-1)?.[0]?.amount).toBe("1180.0000"));
  });
});

/**
 * D97 — WHICH DOOR THE PICKER GOES THROUGH, AND WHOSE AUTHORITY IT SPENDS.
 *
 * ⛔ THE ROW. This panel used to call `getOutstandings(companyID, { kind })` — the
 * RECEIVABLES ageing report — and narrow the whole book to one `ledger_id` in the
 * browser. Because cored maps the FLAT PATH to the PDP resource and never sees a
 * query parameter, that read was decided as `report:outstandings`: a level
 * carrying `disallow display report:bills-payable` was refused the payables ageing
 * at `/bills-payable` and could still read every figure in it here. The refusal
 * governed the DOOR and not the bytes, measured on the wire in drive-l24-d94.mjs.
 *
 * ⛔ AND THE FIX WAS NOT "REFUSE THE PICKER TOO". A clerk who may key a Payment but
 * cannot pick the bill it settles posts it ON ACCOUNT — the balance nobody can
 * chase. So the read moved to a party-scoped route authorised as the VOUCHER CLASS
 * being keyed, which is authority they already hold.
 */
describe("D97 — the picker reads its own door, under the class's authority", () => {
  it("⛔⛔ NEVER touches the ageing report, even when one is available to call", async () => {
    const getPartyOpenBills = vi.fn(async () => OPEN_BILLS);
    // ⚠️ THE REPORT DOOR IS STUBBED AND WATCHED RATHER THAN ABSENT. Leaving it off
    // the client would make this arm pass by TypeError — "it threw" is not "it
    // declined to ask", and only one of those is the row.
    const getOutstandings = vi.fn(async () => {
      throw new Error("the picker must not read the ageing report");
    });
    render(
      <Harness
        client={{ getPartyOpenBills, getOutstandings }}
        mode="settle"
        initial={[againstRef()]}
      />,
    );

    await waitFor(() => expect(getPartyOpenBills).toHaveBeenCalled());
    expect(getOutstandings).not.toHaveBeenCalled();
  });

  it("⛔ spends the authority of the class BEING KEYED, not a fixed one", async () => {
    // A Payment settling a supplier bill. Before the split this was the same
    // `?kind=PAYABLE` read the payables REPORT rule was supposed to govern; it is
    // now `display payment` — the rule that already lets this clerk post the
    // voucher at all.
    const getPartyOpenBills = vi.fn(async () => OPEN_BILLS);
    render(
      <Harness
        client={{ getPartyOpenBills }}
        mode="settle"
        initial={[againstRef()]}
        voucherClass="payment"
      />,
    );

    await waitFor(() =>
      expect(getPartyOpenBills).toHaveBeenCalledWith(
        "co-1",
        "payment",
        "led-acme",
        "RECEIVABLE",
      ),
    );
  });

  it("⛔ CONTROL — an ordinary sale still asks NOTHING at all", async () => {
    // Without this, the two arms above would pass just as well for a picker that
    // called the new route on every render. The read stays LAZY: a plain invoice
    // carries one NEW reference and needs no open-bill list.
    const getPartyOpenBills = vi.fn(async () => OPEN_BILLS);
    render(
      <Harness
        client={{ getPartyOpenBills }}
        mode="raise"
        initial={[{ ...emptyBillRef("NEW"), amount: "1180.0000" }]}
        voucherClass="sales"
      />,
    );

    await screen.findByTestId("billrefs-autoname:0");
    expect(getPartyOpenBills).not.toHaveBeenCalled();
  });
});

/**
 * ⭐⭐ T8 STEP 8 — THE PICKER'S FOREIGN SIDE.
 *
 * ⛔ §4.3 OF THE HANDOFF MADE THIS NON-OPTIONAL, AND STEP 5's DESIGN IS WHY: the
 * server DERIVES the realised exchange difference and REFUSES any voucher whose
 * figure it did not derive, so an operator has to be able to SEE what the bill is
 * carried at in both currencies before they can state a settlement the server will
 * accept. Step 6 put `currency_code`, `foreign_pending` and `currency_conflict` on
 * this exact read for this purpose.
 *
 * ⚠️ THE CURRENCY AND THE RATE ARE INHERITED FROM THE LEG AND ARE NOT KEYED HERE.
 * A `bill_allocation` row and the `posting` it belongs to must agree — step 5 reads
 * the AGAINST row's rate and Outstandings nets the allocation — so two rows naming
 * one document at two rates is a voucher that reports two different exposures for
 * itself. Only the AMOUNT is a per-bill split.
 */
describe("T8/8: the bill picker's foreign side", () => {
  const FOREIGN_BILLS = {
    kind: "RECEIVABLE" as const,
    ledger_id: "led-acme",
    ledger: "Acme Traders",
    group: "Sundry Debtors",
    pending: "66920.9840",
    found: true,
    bills: [
      {
        ref_name: "EXP-1",
        pending: "66920.9840",
        due_date: "2026-07-10",
        overdue_days: 0,
        slab: "0-45 days",
        on_account: false,
        currency_code: "USD",
        foreign_pending: "800.0000",
        currency_conflict: false,
      },
    ],
  };
  const USD = {
    currencyId: "cur-usd",
    currencyCode: "USD",
    rate: "85.2512345678",
  };

  it("shows what the bill is carried at in BOTH currencies", async () => {
    render(
      <Harness
        client={{ getPartyOpenBills: vi.fn(async () => FOREIGN_BILLS) }}
        mode="settle"
        initial={[againstRef("EXP-1")]}
        forex={USD}
      />,
    );
    await waitFor(() =>
      expect(screen.getByTestId("billrefs-billforex:0")).toBeTruthy(),
    );
    const line = screen.getByTestId("billrefs-billforex:0").textContent ?? "";
    // ⛔ BOTH FIGURES. The rupee one alone is what the picker already showed and
    // is not enough to state a settlement the server will accept.
    expect(line).toMatch(/USD 800\.0000/);
    expect(line).toMatch(/66920\.9840/);
    // ⭐ AND IT SAYS WHERE THE DIFFERENCE GOES, because the realised leg is a
    // POSTING and no picker can write one — the operator has to add the line.
    expect(line).toMatch(/needs its own line on the grid/);
    expect(line).toMatch(/refuses any other/);
  });

  it("⛔ WARNS when the bill is open under TWO currencies", async () => {
    render(
      <Harness
        client={{
          getPartyOpenBills: vi.fn(async () => ({
            ...FOREIGN_BILLS,
            bills: [{ ...FOREIGN_BILLS.bills[0]!, currency_conflict: true }],
          })),
        }}
        mode="settle"
        initial={[againstRef("EXP-1")]}
        forex={USD}
      />,
    );
    await waitFor(() =>
      expect(screen.getByTestId("billrefs-billforex:0")).toBeTruthy(),
    );
    // Step 6 discloses the conflict; this is the form acting on the disclosure
    // rather than leaving it on a report the operator may never open.
    expect(screen.getByTestId("billrefs-billforex:0").textContent).toMatch(
      /open under TWO currencies/,
    );
  });

  it("⛔ tells an operator settling a FOREIGN bill on a RUPEE line what will happen", async () => {
    render(
      <Harness
        client={{ getPartyOpenBills: vi.fn(async () => FOREIGN_BILLS) }}
        mode="settle"
        initial={[againstRef("EXP-1")]}
        /* NO forex — the grid line is in the company's own currency. This is the
           case step 5 deliberately does NOT refuse (L36 item 2), on the stated
           grounds that the report discloses it; the form can say so FIRST. */
      />,
    );
    await waitFor(() =>
      expect(screen.getByTestId("billrefs-billforex:0")).toBeTruthy(),
    );
    expect(screen.getByTestId("billrefs-billforex:0").textContent).toMatch(
      /the rupee side will clear while the foreign exposure stays open/,
    );
  });

  it("completes the WHOLE triple from the leg when the foreign amount is keyed", async () => {
    const onRefs = vi.fn();
    render(
      <Harness
        client={{ getPartyOpenBills: vi.fn(async () => FOREIGN_BILLS) }}
        mode="settle"
        initial={[againstRef("EXP-1")]}
        onRefs={onRefs}
        forex={USD}
      />,
    );
    const box = await waitFor(() => screen.getByTestId("billrefs-foreign:0"));
    fireEvent.change(box, { target: { value: "800.00" } });
    const last = onRefs.mock.calls.at(-1)?.[0] as DraftBillRef[];
    // ⛔ ALL THREE IN ONE EDIT. 0080's CHECK refuses a two-of-three row, so a
    // reference carrying a foreign amount and no currency would 422 a request
    // that looks complete on this side.
    expect(last[0]?.foreignAmount).toBe("800.00");
    expect(last[0]?.currencyId).toBe("cur-usd");
    expect(last[0]?.rate).toBe("85.2512345678");
  });

  it("CONTROL: a base leg renders no foreign field at all", async () => {
    render(
      <Harness
        client={{ getPartyOpenBills: vi.fn(async () => FOREIGN_BILLS) }}
        mode="settle"
        initial={[againstRef("EXP-1")]}
      />,
    );
    await waitFor(() => expect(screen.getByTestId("billrefs-bill:0")).toBeTruthy());
    // Every voucher in the field today is one of these, and a permanently blank
    // box on all of them is noise on the form an operator uses most.
    expect(screen.queryByTestId("billrefs-foreign:0")).toBeNull();
  });
});
