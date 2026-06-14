import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { useEffect } from "react";
import { render, screen, cleanup, waitFor, act } from "@testing-library/react";
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

afterEach(cleanup);
beforeEach(() => {
  _resetOutputTaxLedgerCache();
  _resetLedgerCache();
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
    dispatch: (kind: "accept" | "delete", snap: NavSnapshot) => boolean,
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
    dispatch: (kind: "accept" | "delete", snap: NavSnapshot) => boolean,
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
      | ((kind: "accept" | "delete", snap: NavSnapshot) => boolean)
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
    await waitFor(() => expect(screen.getByText("Steel rods purchase")).toBeTruthy());

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
});
