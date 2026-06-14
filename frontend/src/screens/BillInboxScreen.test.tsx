import { describe, it, expect, afterEach, vi } from "vitest";
import { useEffect } from "react";
import {
  render,
  screen,
  cleanup,
  waitFor,
  fireEvent,
  act,
} from "@testing-library/react";
import {
  BillInboxScreen,
  _setInsightdClientFactory,
  _resetInsightdClientFactory,
} from "./BillInboxScreen";
import { CompanyProvider } from "../app/CompanyContext";
import { LoadedDraftProvider, useLoadedDraft } from "../app/LoadedDraft";
import { createMockClient } from "../api";
import type {
  CoredClient,
  DraftSummary,
  InsightdClient,
  PurchaseVoucherDraft,
} from "../api";
import { OPEN_PURCHASE_DRAFT_SENTINEL } from "../keystroke/navMachine";

afterEach(() => {
  cleanup();
  _resetInsightdClientFactory();
});

/** Two slim summaries the fake insightd client lists for co-1. */
const SUMMARIES: DraftSummary[] = [
  {
    draft_id: "draft-1",
    company_id: "co-1",
    created_at: "2026-06-13T00:00:00Z",
    source: "upload",
    status: "ready",
    party: "Steel Suppliers",
    grand_total: "1180.0000",
    confidence: 0.86,
  },
  {
    draft_id: "draft-2",
    company_id: "co-1",
    created_at: "2026-06-14T00:00:00Z",
    source: "whatsapp",
    status: "low_confidence_manual",
    party: "",
    grand_total: "0.0000",
    confidence: 0.41,
  },
];

/** A minimal full draft the fake getDraft returns for draft-1. */
const FULL_DRAFT_1: PurchaseVoucherDraft = {
  draft_id: "draft-1",
  company_id: "co-1",
  created_at: "2026-06-13T00:00:00Z",
  source: "upload",
  status: "ready",
  body: {
    voucher_class: "purchase",
    party_ledger_id: null,
    party: "Steel Suppliers",
    voucher_no: "STL/2026/044",
    voucher_date: "2026-06-10",
    narration: "Steel rods purchase",
    postings: [],
    gst: null,
  },
  confidence: {
    header: {},
    line_items: [],
    overall_header: 0.86,
    overall_line_items: 1,
    overall: 0.86,
  },
  redaction: { any_redacted: false, counts_by_type: {}, total: 0 },
  unresolved: [],
  derived_gst: null,
};

/** Build a fake InsightdClient with vi.fn spies over each method. */
function fakeInsightd(): {
  client: InsightdClient;
  spies: {
    listDrafts: ReturnType<typeof vi.fn>;
    getDraft: ReturnType<typeof vi.fn>;
    createDraft: ReturnType<typeof vi.fn>;
  };
} {
  const listDrafts = vi.fn(async (_companyID: string) => SUMMARIES);
  const getDraft = vi.fn(async (_companyID: string, draftID: string) => {
    if (draftID === "draft-1") return FULL_DRAFT_1;
    return { ...FULL_DRAFT_1, draft_id: draftID } as PurchaseVoucherDraft;
  });
  const createDraft = vi.fn(async () => FULL_DRAFT_1);
  return {
    client: {
      listDrafts,
      getDraft,
      createDraft,
    } as unknown as InsightdClient,
    spies: { listDrafts, getDraft, createDraft },
  };
}

/** Captures the live LoadedDraft value so the test can assert setLoaded. */
function CaptureLoaded({
  onChange,
}: {
  onChange: (loaded: PurchaseVoucherDraft | null) => void;
}) {
  const { loaded } = useLoadedDraft();
  useEffect(() => {
    onChange(loaded?.draft ?? null);
  }, [loaded, onChange]);
  return null;
}

function renderInbox(
  insightd: InsightdClient,
  onSelect: (item: string) => void,
  onLoadedChange: (loaded: PurchaseVoucherDraft | null) => void,
  coredClient: CoredClient = createMockClient({ seed: false }),
) {
  _setInsightdClientFactory(() => insightd);
  return render(
    <CompanyProvider
      client={coredClient}
      initialCompany={{ id: "co-1", name: "Test Co", homeStateCode: "29" }}
    >
      <LoadedDraftProvider>
        <CaptureLoaded onChange={onLoadedChange} />
        <BillInboxScreen
          snapshot={{
            screen: "bill-inbox",
            screenStackDepth: 2,
            canReturnToVoucher: false,
          }}
          onSelect={onSelect}
        />
      </LoadedDraftProvider>
    </CompanyProvider>,
  );
}

describe("BillInboxScreen (F9 Bill Inbox)", () => {
  it("lists pending drafts from insightd for the active company", async () => {
    const { client, spies } = fakeInsightd();
    renderInbox(
      client,
      () => {},
      () => {},
    );

    await waitFor(() =>
      expect(spies.listDrafts).toHaveBeenCalledWith("co-1"),
    );
    // Both summaries render (party + total + confidence). draft-2 has no party
    // and an unknown total ("0.0000" -> shown as a dash).
    await waitFor(() =>
      expect(screen.getByText(/Steel Suppliers/)).toBeTruthy(),
    );
    expect(screen.getByText(/Steel Suppliers/).textContent).toContain(
      "1180.0000",
    );
    expect(screen.getByText(/Steel Suppliers/).textContent).toContain("86%");
    const unknownRow = screen.getByText(/unknown supplier/);
    expect(unknownRow).toBeTruthy();
    // Unknown total renders as a dash, never a misleading real zero.
    expect(unknownRow.textContent).not.toContain("0.0000");
  });

  it("on selecting a draft: fetches it, sets the LoadedDraft context, then fires the open-purchase sentinel", async () => {
    const { client, spies } = fakeInsightd();
    const onSelect = vi.fn();
    let loadedDraft: PurchaseVoucherDraft | null = null;
    renderInbox(
      client,
      onSelect,
      (d) => {
        loadedDraft = d;
      },
    );

    // Wait for the list, then pick the first row by typing into the combobox
    // and pressing Enter (Tally selection-list semantics).
    await waitFor(() => expect(screen.getByText(/Steel Suppliers/)).toBeTruthy());
    const combobox = screen.getByRole("combobox") as HTMLInputElement;
    await act(async () => {
      fireEvent.change(combobox, { target: { value: "Steel" } });
      fireEvent.keyDown(combobox, { key: "Enter" });
    });

    // getDraft fetched the FULL draft for the selected row's id.
    await waitFor(() =>
      expect(spies.getDraft).toHaveBeenCalledWith("co-1", "draft-1"),
    );
    // The full draft was stashed in the LoadedDraft context BEFORE navigating.
    await waitFor(() => expect(loadedDraft).not.toBeNull());
    expect(loadedDraft!.draft_id).toBe("draft-1");
    // And the navMachine sentinel was fired (which pushes the F9 purchase frame).
    await waitFor(() =>
      expect(onSelect).toHaveBeenCalledWith(OPEN_PURCHASE_DRAFT_SENTINEL),
    );
  });

  it("creates a draft from pasted fixture JSON then refreshes the list", async () => {
    const { client, spies } = fakeInsightd();
    renderInbox(
      client,
      () => {},
      () => {},
    );
    await waitFor(() => expect(spies.listDrafts).toHaveBeenCalledTimes(1));

    const textarea = screen.getByTestId("inbox-fixture");
    await act(async () => {
      fireEvent.change(textarea, {
        target: { value: '{"invoice_no":"X-1"}' },
      });
      fireEvent.click(screen.getByTestId("inbox-create"));
    });

    await waitFor(() =>
      expect(spies.createDraft).toHaveBeenCalledWith("co-1", {
        source: "upload",
        tenant: { company_id: "co-1" },
        fixture: { invoice_no: "X-1" },
      }),
    );
    // The list is refreshed after a successful create.
    await waitFor(() => expect(spies.listDrafts).toHaveBeenCalledTimes(2));
  });
});
