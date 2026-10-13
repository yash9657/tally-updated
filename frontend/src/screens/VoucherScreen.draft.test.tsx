import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { useEffect } from "react";
import { render, screen, cleanup, waitFor, act, fireEvent } from "@testing-library/react";
import { VoucherScreen } from "./VoucherScreen";
import { CompanyProvider } from "../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
} from "../app/ScreenActions";
import { LoadedVoucherProvider } from "../app/LoadedVoucher";
import { LoadedDraftProvider, useLoadedDraft } from "../app/LoadedDraft";
import { createMockClient } from "../api";
import type {
  CoredClient,
  PostingRequest,
  PurchaseVoucherDraft,
} from "../api";
import type { NavSnapshot } from "../keystroke/navMachine";
import {
  _resetOutputTaxLedgerCache,
  _resetLedgerCache,
} from "./vouchers/gstVoucher";
import {
  _resetAcceptedDraftCache,
  _resetDurableAcceptRecords,
} from "./vouchers/draftAccept";

afterEach(cleanup);
beforeEach(() => {
  _resetOutputTaxLedgerCache();
  _resetLedgerCache();
  // ⛔ THE ACCEPT-ONCE STATE IS MODULE-LEVEL AND MUST BE RESET BETWEEN CASES.
  // Every case in this file accepts the SAME fixture draft, so without this the
  // second case is refused by the duplicate guard — correctly — with "this draft
  // has already been accepted in this session as voucher STL/2026/044", and the
  // POST spy is never called. The guard exists because one supplier bill became
  // TWO vouchers in a browser; it is deliberately durable across a page reload,
  // which is exactly why a test file that re-accepts one draft has to clear it
  // rather than work around it.
  _resetAcceptedDraftCache();
  _resetDurableAcceptRecords();
});

/** A vi.fn wrapper over every CoredClient method (mirrors the gst test). */
function spyClient(base: CoredClient = createMockClient({ seed: false })): {
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

/** A purchase CREATE snapshot (draft mode = purchase && !alter && draft loaded). */
const PURCHASE_CREATE_SNAPSHOT: NavSnapshot = {
  screen: "voucher",
  voucherType: "purchase",
  mode: "create",
  field: "date",
  saved: false,
  screenStackDepth: 1,
  canReturnToVoucher: false,
};

/** Seeds the LoadedDraftProvider from inside the tree before assertions run. */
function SeedDraft({ draft }: { draft: PurchaseVoucherDraft }) {
  const { setLoaded } = useLoadedDraft();
  useEffect(() => {
    setLoaded({ draft });
  }, [draft, setLoaded]);
  return null;
}

/** Captures the ScreenActions dispatcher so the test can fire "accept". */
function CaptureDispatch({
  onReady,
}: {
  onReady: (
    dispatch: (kind: "accept" | "delete", snap: NavSnapshot) => { ran: boolean; message?: string },
  ) => void;
}) {
  const dispatch = useScreenActionDispatcher();
  useEffect(() => {
    onReady(dispatch);
  }, [dispatch, onReady]);
  return null;
}

/**
 * Build a realistic "ready" purchase draft: intra-state @18, taxable 1000,
 * CGST 90 + SGST 90, party credit 1180. All ledger ids are real mock-minted
 * uuids so resolveDraftLedgers has nothing to create (acceptPurchaseDraft POSTs
 * the balanced body verbatim). One header field is below 0.80 to assert amber.
 */
async function buildReadyDraft(
  base: CoredClient,
): Promise<PurchaseVoucherDraft> {
  const party = await base.createLedger("co-1", {
    name: "Steel Suppliers",
    group_name: "Sundry Creditors",
    state_code: "29",
  });
  const purchases = await base.createLedger("co-1", {
    name: "Purchases",
    group_name: "Purchase Accounts",
  });
  const cgst = await base.createLedger("co-1", {
    name: "Input CGST",
    group_name: "Duties & Taxes",
  });
  const sgst = await base.createLedger("co-1", {
    name: "Input SGST",
    group_name: "Duties & Taxes",
  });
  const igst = await base.createLedger("co-1", {
    name: "Input IGST",
    group_name: "Duties & Taxes",
  });

  return {
    draft_id: "draft-1",
    company_id: "co-1",
    created_at: "2026-06-13T00:00:00Z",
    source: "upload",
    status: "ready",
    body: {
      voucher_class: "purchase",
      party_ledger_id: party.ledger_id,
      party: "Steel Suppliers",
      voucher_no: "STL/2026/044",
      voucher_date: "2026-06-10",
      narration: "Steel rods purchase",
      postings: [
        { ledger_id: purchases.ledger_id, ledger_name: "Purchases", dr_cr: "D", amount: "1000.0000" },
        { ledger_id: cgst.ledger_id, ledger_name: "Input CGST", dr_cr: "D", amount: "90.0000" },
        { ledger_id: sgst.ledger_id, ledger_name: "Input SGST", dr_cr: "D", amount: "90.0000" },
        { ledger_id: party.ledger_id, ledger_name: "Steel Suppliers", dr_cr: "C", amount: "1180.0000" },
      ],
      gst: {
        ruleset_version: "2024.1",
        supplier_state_code: "29",
        place_of_supply_state_code: "29",
        reverse_charge: false,
        exempt: false,
        lines: [
          { taxable_value: "1000.0000", gst_rate_pct: "18", hsn: "7214" },
        ],
        cgst_ledger_id: cgst.ledger_id,
        sgst_ledger_id: sgst.ledger_id,
        igst_ledger_id: igst.ledger_id,
      },
    },
    confidence: {
      header: {
        invoice_no: { value_present: true, confidence: 0.97, source: "ocr" },
        // Below 0.80 -> amber.
        invoice_date: { value_present: true, confidence: 0.62, source: "ocr" },
        supplier: { value_present: true, confidence: 0.95, source: "classifier" },
        place_of_supply: { value_present: true, confidence: 0.9, source: "inferencer" },
        totals: { value_present: true, confidence: 0.99, source: "derived" },
      },
      line_items: [],
      overall_header: 0.86,
      overall_line_items: 1,
      overall: 0.86,
    },
    redaction: {
      any_redacted: true,
      counts_by_type: { GSTIN: 2, PHONE: 1 },
      total: 3,
    },
    unresolved: [],
    derived_gst: {
      cgst: "90.0000",
      sgst: "90.0000",
      igst: "0.0000",
      total_tax: "180.0000",
      invoice_total: "1180.0000",
    },
  };
}

function renderDraftScreen(
  client: CoredClient,
  draft: PurchaseVoucherDraft,
  onReady: (
    dispatch: (kind: "accept" | "delete", snap: NavSnapshot) => { ran: boolean; message?: string },
  ) => void,
) {
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <ScreenActionsProvider>
        <LoadedVoucherProvider>
          <LoadedDraftProvider>
            <SeedDraft draft={draft} />
            <CaptureDispatch onReady={onReady} />
            <VoucherScreen
              snapshot={PURCHASE_CREATE_SNAPSHOT}
              onSelect={() => {}}
            />
          </LoadedDraftProvider>
        </LoadedVoucherProvider>
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
}

describe("Purchase voucher DRAFT mode (OCR prefill + HITL accept)", () => {
  it("prefills the form, flags low-confidence amber, shows redaction + derived GST, and accepts only on Ctrl+A", async () => {
    const base = createMockClient({ seed: false });
    const draft = await buildReadyDraft(base);
    const { client, spies } = spyClient(base);

    let dispatch:
      | ((kind: "accept" | "delete", snap: NavSnapshot) => { ran: boolean; message?: string })
      | null = null;
    renderDraftScreen(client, draft, (d) => {
      dispatch = d;
    });

    // Prefill: party name + taxable render from the draft body.
    await waitFor(() =>
      expect(
        (screen.getByTestId("gst-taxable") as HTMLInputElement).value,
      ).toBe("1000.0000"),
    );
    // Narration is an editable input in (rebuildable) draft mode; read its value.
    await waitFor(() =>
      expect(
        (screen.getByTestId("draft-narration") as HTMLInputElement).value,
      ).toBe("Steel rods purchase"),
    );

    // Status banner: ready.
    expect(screen.getByTestId("draft-status").textContent).toContain(
      "Ready",
    );

    // The low-confidence header field (invoice_date @0.62) carries the amber
    // color (#d97706, normalized to rgb by jsdom's CSSOM).
    const amberRow = screen.getByTestId("conf-invoice_date");
    expect(amberRow.style.color).toBe("rgb(217, 119, 6)");
    // A high-confidence field is NOT amber.
    const okRow = screen.getByTestId("conf-invoice_no");
    expect(okRow.style.color).toBe("");

    // Redaction proof shows the PII counts.
    const redaction = screen.getByTestId("draft-redaction").textContent ?? "";
    expect(redaction).toContain("3");
    expect(redaction).toContain("GSTIN 2");

    // Derived GST preview shows the fixed-4 intra split.
    const dg = screen.getByTestId("draft-derived-gst").textContent ?? "";
    expect(dg).toContain("90.0000");
    expect(dg).toContain("180.0000");
    expect(dg).toContain("1180.0000");

    // HITL: NO post occurred on render.
    expect(spies.postPurchaseVoucher).not.toHaveBeenCalled();

    // Fire accept via the registered handler (no AppShell keydown wrapper here).
    expect(dispatch).not.toBeNull();
    await act(async () => {
      dispatch!("accept", PURCHASE_CREATE_SNAPSHOT);
    });

    await waitFor(() =>
      expect(spies.postPurchaseVoucher).toHaveBeenCalledTimes(1),
    );
    const [, req] = spies.postPurchaseVoucher!.mock.calls[0]!;
    const postings = (req as { postings: PostingRequest[] }).postings;
    // The balanced body posted verbatim: purchase Dr 1000 + CGST 90 + SGST 90 +
    // party Cr 1180 (fixed-4 strings, dr_cr kept as "D"/"C").
    expect(postings).toHaveLength(4);
    const amounts = postings.map((p) => p.amount);
    expect(amounts).toContain("1000.0000");
    expect(amounts).toContain("1180.0000");
    expect(amounts.filter((a) => a === "90.0000")).toHaveLength(2);
    expect(postings.filter((p) => p.dr_cr === "D")).toHaveLength(3);
    expect(postings.filter((p) => p.dr_cr === "C")).toHaveLength(1);

    // The server-resolved number + derived split render after accept.
    await waitFor(() =>
      expect(screen.getByTestId("voucher-no").textContent).toContain(
        "STL/2026/044",
      ),
    );
    expect(screen.getByTestId("tax-cgst").textContent).toBe("90.0000");
    expect(screen.getByTestId("tax-sgst").textContent).toBe("90.0000");
    expect(screen.queryByTestId("tax-igst")).toBeNull();
  });

  it("edits the taxable value -> accept POSTs the rebuilt, balanced purchase body (orientation preserved)", async () => {
    const base = createMockClient({ seed: false });
    const draft = await buildReadyDraft(base);
    const { client, spies } = spyClient(base);

    let dispatch:
      | ((kind: "accept" | "delete", snap: NavSnapshot) => { ran: boolean; message?: string })
      | null = null;
    renderDraftScreen(client, draft, (d) => {
      dispatch = d;
    });

    // Wait for prefill, then edit taxable 1000 -> 2000 (intra @18).
    const taxable = () => screen.getByTestId("gst-taxable") as HTMLInputElement;
    await waitFor(() => expect(taxable().value).toBe("1000.0000"));
    await act(async () => {
      fireEvent.change(taxable(), { target: { value: "2000.0000" } });
    });

    // Still nothing posted before Ctrl+A (HITL holds through an edit).
    expect(spies.postPurchaseVoucher).not.toHaveBeenCalled();

    expect(dispatch).not.toBeNull();
    await act(async () => {
      dispatch!("accept", PURCHASE_CREATE_SNAPSHOT);
    });

    await waitFor(() =>
      expect(spies.postPurchaseVoucher).toHaveBeenCalledTimes(1),
    );
    const [, req] = spies.postPurchaseVoucher!.mock.calls[0]!;
    const postings = (req as { postings: PostingRequest[] }).postings;

    // Independent literals: 2000 @18 intra -> CGST 180 + SGST 180, party Cr 2360.
    const byAmount = postings.map((p) => `${p.dr_cr}:${p.amount}`);
    expect(byAmount).toContain("D:2000.0000"); // expense debit
    expect(byAmount.filter((s) => s === "D:180.0000")).toHaveLength(2); // input CGST+SGST debit
    expect(byAmount).toContain("C:2360.0000"); // party credit
    // Orientation: 3 debits, 1 credit — a purchase, not a reversed sale.
    expect(postings.filter((p) => p.dr_cr === "D")).toHaveLength(3);
    expect(postings.filter((p) => p.dr_cr === "C")).toHaveLength(1);
    // Balanced to the paisa, computed here (not echoed by the mock).
    const paise = (dc: "D" | "C") =>
      postings
        .filter((p) => p.dr_cr === dc)
        .reduce((a, p) => a + Math.round(Number.parseFloat(p.amount) * 10000), 0);
    expect(paise("D")).toBe(paise("C"));
  });

  it("an edited field drops its amber confidence chip", async () => {
    const base = createMockClient({ seed: false });
    const draft = await buildReadyDraft(base);
    const { client } = spyClient(base);
    renderDraftScreen(client, draft, () => {});

    // invoice_date is amber (0.62) until the human overrides the date.
    await waitFor(() =>
      expect(screen.getByTestId("conf-invoice_date").style.color).toBe(
        "rgb(217, 119, 6)",
      ),
    );
    await act(async () => {
      fireEvent.change(screen.getByTestId("draft-date"), {
        target: { value: "2026-06-11" },
      });
    });
    // The chip is no longer amber and reads "edited".
    expect(screen.getByTestId("conf-invoice_date").style.color).toBe("");
    expect(screen.getByTestId("conf-invoice_date").textContent).toContain("edited");
  });
});

describe("⛔ D108: one accepted draft is one voucher", () => {
  it("a second accept posts NOTHING — and it is the ORDINARY path it must not fall into", async () => {
    // ⛔⛔ THE DRAFT PATH'S FOUR ACCEPT-ONCE LAYERS DO NOT COVER THIS PRESS, and
    // that is the whole point of pinning it separately. The success handler sets
    // `setLoadedDraft(null)`, so the screen LEAVES draft mode — while the form
    // still holds every value the OCR prefilled. A second Ctrl+A therefore does
    // not go through `acceptPurchaseDraft` at all: it goes down the ordinary
    // voucher path, with no draft id, past the durable memo, past
    // `assertNotAlreadyOnTheBooks`, past the posting claim, and posts the
    // supplier's bill a second time.
    //
    // ⚠️ The 2026-08-07 audit already recorded "a 2nd Ctrl+A posts a DUPLICATE
    // voucher" on this path (HANDOFF-PARITY-AUDIT.md, the row-30 drive log) and
    // it was never separately registered. It is D108 reached by a third door.
    const base = createMockClient({ seed: false });
    const draft = await buildReadyDraft(base);
    const { client, spies } = spyClient(base);

    let dispatch:
      | ((kind: "accept" | "delete", snap: NavSnapshot) => { ran: boolean; message?: string })
      | null = null;
    renderDraftScreen(client, draft, (d) => {
      dispatch = d;
    });
    await waitFor(() =>
      expect(
        (screen.getByTestId("gst-taxable") as HTMLInputElement).value,
      ).toBe("1000.0000"),
    );

    await act(async () => {
      dispatch!("accept", PURCHASE_CREATE_SNAPSHOT);
    });
    await waitFor(() =>
      expect(spies.postPurchaseVoucher).toHaveBeenCalledTimes(1),
    );

    // ⛔⛔ THE SECOND PRESS.
    await act(async () => {
      dispatch!("accept", PURCHASE_CREATE_SNAPSHOT);
    });
    for (let i = 0; i < 3; i++) {
      // eslint-disable-next-line no-await-in-loop
      await act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });
    }
    expect(spies.postPurchaseVoucher).toHaveBeenCalledTimes(1);

    // ---- AND HERE IS WHY: the prefilled bill is off the form.
    expect(
      (screen.getByTestId("gst-taxable") as HTMLInputElement).value,
    ).toBe("0.0000");
    // The server's number is still on screen — the receipt survives the clear.
    expect(screen.getByTestId("voucher-no").textContent).toContain(
      "STL/2026/044",
    );
  });
});
