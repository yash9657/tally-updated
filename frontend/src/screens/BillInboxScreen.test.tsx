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
  CreateDraftRequest,
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

    // ⛔ THIS ASSERTION USED TO READ `tenant: { company_id: "co-1" }` AND IT WAS
    // PART OF THE DEFECT (trap #7 — a test that pins a bug). That stub tenant is
    // exactly what made insightd run against an empty chart of accounts: no
    // supplier match, no supplier state (so an INTRA-state bill came back as
    // IGST), and no input-tax ledger ids (so the draft was unbalanced by exactly
    // the tax and could never be posted). See the tenant-context comment in
    // BillInboxScreen.tsx. The full assertions live in the describe block below.
    await waitFor(() => expect(spies.createDraft).toHaveBeenCalledTimes(1));
    const [co, req] = spies.createDraft.mock.calls[0]!;
    expect(co).toBe("co-1");
    expect(req.fixture).toEqual({ invoice_no: "X-1" });
    expect(req.tenant.company_id).toBe("co-1");
    expect(Array.isArray(req.tenant.ledgers)).toBe(true);
    // The list is refreshed after a successful create.
    await waitFor(() => expect(spies.listDrafts).toHaveBeenCalledTimes(2));
  });
});

/**
 * ===========================================================================
 * THE TENANT CONTEXT — two HIGH defects that shared one line.
 * ===========================================================================
 * Driven in a browser: a taxed bill filed through this screen's own front door
 * came back with NO input-tax posting legs (debits 1000.0000 against a credit of
 * 1180.0000 — unbalanced by exactly the tax, so cored refuses it) AND with an
 * INTRA-state bill derived as IGST. Both because the screen sent
 * `tenant: { company_id }` and nothing else, so insightd's pipeline had no
 * ledgers to match a supplier against, no supplier state to compare with the
 * place of supply, and no tax-ledger ids to debit.
 *
 * ⚠️ The seeded demo draft always looked right, because seed-demo.mts builds a
 * complete tenant block itself. The oracle and the defect never met.
 */
describe("BillInboxScreen — the tenant context sent to insightd", () => {
  /** A cored client whose chart of accounts is this company's real one. */
  async function coredWithChart(): Promise<CoredClient> {
    const client = createMockClient({ seed: false });
    await client.createCompany({
      id: "co-1",
      name: "Test Co",
      fy_start_date: "2026-04-01",
    });
    await client.createLedger("co-1", {
      name: "Pune Fabricators",
      group_name: "Sundry Creditors",
      gstin: "27AABCP1234R1Z5",
      state_code: "27",
    });
    await client.createLedger("co-1", {
      name: "Raw Material Purchases",
      group_name: "Purchase Accounts",
    });
    for (const n of ["Input CGST", "Input SGST", "Input IGST"]) {
      await client.createLedger("co-1", { name: n, group_name: "Duties & Taxes" });
    }
    // ⛔ The OUTPUT set exists too, and must never be picked for a purchase.
    for (const n of ["Output CGST", "Output SGST", "Output IGST"]) {
      await client.createLedger("co-1", { name: n, group_name: "Duties & Taxes" });
    }
    return client;
  }

  async function capturedTenant(): Promise<Record<string, unknown>> {
    const { client, spies } = fakeInsightd();
    renderInbox(client, () => {}, () => {}, await coredWithChart());
    await waitFor(() => expect(spies.listDrafts).toHaveBeenCalledTimes(1));
    await act(async () => {
      fireEvent.change(screen.getByTestId("inbox-fixture"), {
        target: { value: "{}" },
      });
      fireEvent.click(screen.getByTestId("inbox-create"));
    });
    await waitFor(() => expect(spies.createDraft).toHaveBeenCalledTimes(1));
    return spies.createDraft.mock.calls[0]![1].tenant as Record<string, unknown>;
  }

  it("sends the company's REAL chart of accounts, not a bare company id", async () => {
    const tenant = await capturedTenant();
    const ledgers = tenant.ledgers as { name: string; kind: string }[];
    expect(ledgers.length).toBeGreaterThan(0);
    const supplier = ledgers.find((l) => l.name === "Pune Fabricators");
    expect(supplier).toBeTruthy();
    // Without this the classifier can never match a supplier at all.
    expect(supplier!.kind).toBe("party");
  });

  it("carries each party's STATE CODE — the field that decides CGST+SGST vs IGST", async () => {
    const tenant = await capturedTenant();
    const ledgers = tenant.ledgers as { name: string; state_code?: string }[];
    const supplier = ledgers.find((l) => l.name === "Pune Fabricators")!;
    expect(supplier.state_code).toBe("27");
    expect(tenant.home_state_code).toBe("29");
  });

  it("resolves the three INPUT tax ledgers and never the OUTPUT ones", async () => {
    // ⛔ RULE 4. Input and output tax balance identically, so substituting one
    // for the other is invisible to every arithmetic check — and it is the whole
    // difference between tax this company may reclaim and tax it owes. Assert the
    // resolved ids by NAME, not merely that three ids are present.
    const tenant = await capturedTenant();
    const ledgers = tenant.ledgers as { id: string; name: string }[];
    const nameOf = (id: unknown) =>
      ledgers.find((l) => l.id === id)?.name ?? "(unresolved)";
    expect(nameOf(tenant.cgst_ledger_id)).toBe("Input CGST");
    expect(nameOf(tenant.sgst_ledger_id)).toBe("Input SGST");
    expect(nameOf(tenant.igst_ledger_id)).toBe("Input IGST");
  });

  it("NEVER sends a raw GSTIN across the boundary", async () => {
    // insightd's LedgerRef documents gstin_masked as "a masked tag, never the raw
    // GSTIN" — this context can end up in a cross-border classifier prompt.
    const tenant = await capturedTenant();
    const serialised = JSON.stringify(tenant);
    expect(serialised).not.toContain("27AABCP1234R1Z5");
    const ledgers = tenant.ledgers as { name: string; gstin_masked?: string }[];
    const supplier = ledgers.find((l) => l.name === "Pune Fabricators")!;
    expect(supplier.gstin_masked).toBe("27AAB…1Z5");
  });

  it("sends the same context on the UPLOAD path, which has no fixture to fall back on", async () => {
    // The real front door: a photographed bill carries no tenant block at all.
    const createDraft = vi.fn(
      async (_companyID: string, _req: CreateDraftRequest) => FULL_DRAFT_1,
    );
    const insightd: InsightdClient = {
      listDrafts: async () => SUMMARIES,
      getDraft: async () => FULL_DRAFT_1,
      createDraft,
    };
    renderInbox(insightd, () => {}, () => {}, await coredWithChart());
    await waitFor(() => expect(screen.getByTestId("inbox-upload")).toBeTruthy());

    await act(async () => {
      const file = new File([new Uint8Array([0x50, 0x44, 0x46])], "bill.pdf", {
        type: "application/pdf",
      });
      Object.defineProperty(file, "arrayBuffer", {
        value: async () => new Uint8Array([0x50, 0x44, 0x46]).buffer,
      });
      fireEvent.change(screen.getByTestId("inbox-upload") as HTMLInputElement, {
        target: { files: [file] },
      });
    });

    await waitFor(() => expect(createDraft).toHaveBeenCalledTimes(1));
    const tenant = createDraft.mock.calls[0]![1].tenant as {
      ledgers: { name: string }[];
      cgst_ledger_id?: string;
    };
    expect(tenant.ledgers.some((l) => l.name === "Pune Fabricators")).toBe(true);
    expect(tenant.cgst_ledger_id).toBeTruthy();
  });

  it("RESOLVES the tax ledgers and never CREATES them", async () => {
    // Opening an inbox must not write masters. A company that has never keyed a
    // GST voucher simply gets those ids absent, and insightd then proposes the
    // tax leg with a null ledger_id and names it in `unresolved` — the draft
    // still balances. Creating three Duties & Taxes ledgers as a side effect of a
    // READ is the orphan-master anti-pattern this codebase already has a defect
    // for.
    const client = createMockClient({ seed: false });
    await client.createCompany({
      id: "co-1",
      name: "Test Co",
      fy_start_date: "2026-04-01",
    });
    const before = (await client.listLedgers("co-1")).ledgers.length;

    const { client: insightd, spies } = fakeInsightd();
    renderInbox(insightd, () => {}, () => {}, client);
    await waitFor(() => expect(spies.listDrafts).toHaveBeenCalledTimes(1));
    await act(async () => {
      fireEvent.change(screen.getByTestId("inbox-fixture"), {
        target: { value: "{}" },
      });
      fireEvent.click(screen.getByTestId("inbox-create"));
    });
    await waitFor(() => expect(spies.createDraft).toHaveBeenCalledTimes(1));

    expect((await client.listLedgers("co-1")).ledgers.length).toBe(before);
    const tenant = spies.createDraft.mock.calls[0]![1].tenant as {
      cgst_ledger_id?: string;
    };
    expect(tenant.cgst_ledger_id).toBeUndefined();
  });
});

/**
 * UPLOADING A BILL — the front door of the OCR loop.
 *
 * Until now the only way into the inbox was pasting fixture JSON, which is a
 * developer's tool, not a way to file a supplier invoice. These pin the two
 * things that make an upload usable rather than merely present: the request
 * carries the file's real bytes, and a read that takes up to ~45 s (the WS10
 * spike's measurement for a phone photo) is POLLED rather than assumed
 * finished.
 */
describe("BillInboxScreen — uploading a bill", () => {
  function upload(name: string, type: string, bytes: number[]) {
    const file = new File([new Uint8Array(bytes)], name, { type });
    // jsdom's File has no arrayBuffer() — a gap in the TEST ENVIRONMENT, not in
    // the component: it is standard in every browser, and `pnpm e2e:ui` drives
    // the real one in Chromium. Filling it here keeps the component on the
    // modern API instead of contorting it around a jsdom limitation.
    Object.defineProperty(file, "arrayBuffer", {
      value: async () => new Uint8Array(bytes).buffer,
    });
    const input = screen.getByTestId("inbox-upload") as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file] } });
    return file;
  }

  it("posts the file's real bytes as base64, with its name and type", async () => {
    const createDraft = vi.fn(
      async (_companyID: string, _req: CreateDraftRequest) => ({
        ...FULL_DRAFT_1,
        draft_id: "draft-new",
        status: "ready" as const,
      }),
    );
    const insightd: InsightdClient = {
      listDrafts: async () => SUMMARIES,
      getDraft: async () => FULL_DRAFT_1,
      createDraft,
    };
    renderInbox(insightd, () => {}, () => {});
    await waitFor(() => expect(screen.getByTestId("inbox-upload")).toBeTruthy());

    // "PDF" -> the exact base64 the server must receive back.
    await act(async () => {
      upload("bill.pdf", "application/pdf", [0x50, 0x44, 0x46]);
    });

    await waitFor(() => expect(createDraft).toHaveBeenCalledTimes(1));
    const [companyID, req] = createDraft.mock.calls[0]!;
    expect(companyID).toBe("co-1");
    expect(req.filename).toBe("bill.pdf");
    expect(req.content_type).toBe("application/pdf");
    expect(req.content_b64).toBe(btoa("PDF"));
    // A real upload, not the fixture path.
    expect(req.fixture).toBeUndefined();
  });

  it("POLLS while the server is still reading, then says it is ready", async () => {
    // The claim that matters: with real OCR the create returns a draft in
    // "processing" and the client must keep asking. A client that assumed the
    // first response was final would open an empty voucher.
    //
    // Real timers, one poll interval: fake timers fight waitFor here, and the
    // thing under test is that a SECOND request happens at all — not the exact
    // cadence.
    const created = { ...FULL_DRAFT_1, draft_id: "draft-slow", status: "processing" as const };
    const finished = { ...FULL_DRAFT_1, draft_id: "draft-slow", status: "ready" as const };
    const getDraft = vi.fn(async () => finished);
    const insightd: InsightdClient = {
      listDrafts: async () => SUMMARIES,
      getDraft,
      createDraft: async () => created,
    };
    renderInbox(insightd, () => {}, () => {});
    await waitFor(() => expect(screen.getByTestId("inbox-upload")).toBeTruthy());

    await act(async () => {
      upload("photo.jpg", "image/jpeg", [1, 2, 3]);
    });

    // The create said "processing", so the screen MUST have asked again.
    await waitFor(() => expect(getDraft).toHaveBeenCalledWith("co-1", "draft-slow"), {
      timeout: 4000,
    });
    await waitFor(
      () => expect(screen.getByTestId("inbox-notice").textContent).toContain("read"),
      { timeout: 4000 },
    );
  });

  it("explains an upload failure instead of failing silently", async () => {
    const insightd: InsightdClient = {
      listDrafts: async () => SUMMARIES,
      getDraft: async () => FULL_DRAFT_1,
      createDraft: async () => {
        throw new Error("insightd unreachable");
      },
    };
    renderInbox(insightd, () => {}, () => {});
    await waitFor(() => expect(screen.getByTestId("inbox-upload")).toBeTruthy());

    await act(async () => {
      upload("bill.pdf", "application/pdf", [1]);
    });

    await waitFor(() =>
      expect(screen.getByTestId("inbox-error").textContent).toContain(
        "insightd unreachable",
      ),
    );
  });
});
