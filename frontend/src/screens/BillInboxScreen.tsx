import { useCallback, useEffect, useMemo, useState } from "react";
import {
  createInsightdClient,
  type DraftSummary,
  type InsightdClient,
} from "../api";
import { useCompanyID } from "../app/CompanyContext";
import { useLoadedDraft } from "../app/LoadedDraft";
import { OPEN_PURCHASE_DRAFT_SENTINEL } from "../keystroke/navMachine";
import type { ScreenComponentProps } from "../app/screenRegistry";
import { SelectionList } from "./SelectionList";

/**
 * The F9 Bill Inbox — the AI-native upload-to-voucher entry point.
 *
 * On mount it LISTs the pending OCR drafts for the active company (via insightd,
 * which has no auth) and renders them as a Tally selection list. Selecting a
 * draft fetches the FULL {@link PurchaseVoucherDraft}, stashes it in the
 * LoadedDraft context, then fires the pinned sentinel through `onSelect` so the
 * navMachine pushes the F9 Purchase voucher in draft-prefill mode (the proven
 * path in VoucherScreen.draft.test.tsx). The operator reviews + presses Ctrl+A;
 * the EXISTING cored voucher API performs the sole human-in-the-loop write.
 *
 * A "Create from fixture" affordance pastes a fixture JSON and calls
 * createDraft, then refreshes the list — the e2e/demo create-then-open path. No
 * tax is ever computed client-side; Money stays a string end to end.
 */

/**
 * Test seam: tests inject a fake {@link InsightdClient} (no network) via
 * {@link _setInsightdClientFactory}. Defaults to the env-configured client.
 * Mirrors the `_reset*Cache` test-seam idiom used elsewhere in screens/.
 */
let insightdClientFactory: () => InsightdClient = () => createInsightdClient();

/** Override the insightd client factory (tests only). */
export function _setInsightdClientFactory(
  factory: () => InsightdClient,
): void {
  insightdClientFactory = factory;
}

/** Restore the default env-configured insightd client factory (tests only). */
export function _resetInsightdClientFactory(): void {
  insightdClientFactory = () => createInsightdClient();
}

/**
 * Format a draft's grand total for display. "0.0000" is the sentinel a
 * low_confidence_manual draft carries when GST was never derived — show it as an
 * explicit "unknown total" rather than a misleading real zero.
 */
function formatTotal(grandTotal: string): string {
  return grandTotal === "0.0000" ? "—" : grandTotal;
}

/**
 * A stable, human-readable row label for a draft summary.
 *
 * ⛔ AN ACCEPTED DRAFT NAMES THE VOUCHER IT BECAME, AND ITS ABSENCE WAS HALF OF A
 * DUPLICATED SUPPLIER BILL. insightd sets `accepted_voucher_no` precisely so this
 * row can say "already posted as …" instead of sitting here looking postable — and
 * a browser re-drive on 2026-08-08 found the server sent it, the wire returned it
 * and the TS type declared it, while this label never rendered it: the row read
 * `Pune Fabricators · 21240.0000 · accepted · 95%`, with the voucher number nowhere
 * on the screen. An operator who cannot see what happened to a bill re-does it,
 * which is exactly the duplicate the whole accept lifecycle exists to stop.
 *
 * ⚠️ `accepted_voucher_no` may legitimately be "" (cored allocates the number and
 * an empty string means "posted, number not echoed"), so fall back to saying it is
 * posted rather than printing an empty gap that reads like "not posted".
 */
function rowLabel(s: DraftSummary): string {
  const party = s.party.trim().length > 0 ? s.party : "(unknown supplier)";
  const pct = Math.round((s.confidence ?? 0) * 100);
  const head = `${party} · ${formatTotal(s.grand_total)}`;
  if (s.status === "accepted") {
    const no = (s.accepted_voucher_no ?? "").trim();
    return `${head} · already posted as ${no.length > 0 ? no : "a voucher"} — do not re-enter`;
  }
  return `${head} · ${s.status} · ${pct}%`;
}

export function BillInboxScreen({ onSelect }: ScreenComponentProps) {
  const companyID = useCompanyID();
  const { setLoaded } = useLoadedDraft();
  const insightd = useMemo(() => insightdClientFactory(), []);

  const [drafts, setDrafts] = useState<DraftSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [fixtureText, setFixtureText] = useState("");
  /** Progress/outcome of an upload, shown so a 45-second read is not silence. */
  const [notice, setNotice] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const list = await insightd.listDrafts(companyID);
      setDrafts(list);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [insightd, companyID]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Build display rows with GUARANTEED-UNIQUE labels: SelectionList keys and
  // selects by the option STRING, so two drafts that render identically
  // (same party · total · status · pct) must be disambiguated or they collide
  // (one becomes unselectable). Keep the clean label in the common case; append
  // the full draft_id only when a label repeats.
  const rows = useMemo(() => {
    const seen = new Map<string, number>();
    return drafts.map((s) => {
      const base = rowLabel(s);
      const n = seen.get(base) ?? 0;
      seen.set(base, n + 1);
      const label = n === 0 ? base : `${base} · #${s.draft_id}`;
      return { label, id: s.draft_id };
    });
  }, [drafts]);

  // Map the (unique) selected row label back to its draft id locally (the
  // navMachine sentinel never carries the id — see OPEN_PURCHASE_DRAFT_SENTINEL).
  const labelToId = useMemo(() => {
    const m = new Map<string, string>();
    for (const r of rows) m.set(r.label, r.id);
    return m;
  }, [rows]);

  const openDraft = useCallback(
    async (label: string) => {
      const draftID = labelToId.get(label);
      if (!draftID) return;
      setError(null);
      try {
        const draft = await insightd.getDraft(companyID, draftID);
        // (1) stash the full draft in the LoadedDraft context BEFORE the voucher
        // frame mounts, then (2) fire the sentinel so the machine pushes the F9
        // Purchase create frame and VoucherScreen hydrates from the draft.
        setLoaded({ draft });
        onSelect(OPEN_PURCHASE_DRAFT_SENTINEL);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [labelToId, insightd, companyID, setLoaded, onSelect],
  );

  const createFromFixture = useCallback(async () => {
    const text = fixtureText.trim();
    if (text.length === 0) return;
    let fixture: Record<string, unknown>;
    try {
      fixture = JSON.parse(text) as Record<string, unknown>;
    } catch (e) {
      setError(`Invalid fixture JSON: ${e instanceof Error ? e.message : e}`);
      return;
    }
    setCreating(true);
    setError(null);
    try {
      await insightd.createDraft(companyID, {
        source: "upload",
        tenant: { company_id: companyID },
        fixture,
      });
      setFixtureText("");
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setCreating(false);
    }
  }, [fixtureText, insightd, companyID, refresh]);

  /**
   * UPLOAD A REAL BILL — the affordance this screen was missing. The "create"
   * button pasted fixture JSON, which is a developer's tool, not a way to file a
   * supplier invoice.
   *
   * ENQUEUE-AND-POLL, because real OCR takes up to ~45 s on a phone photo (the
   * WS10 spike measured it). insightd returns a draft immediately — already
   * terminal when the deterministic fakes are in use, in status "processing"
   * when the real recogniser is — so this always does the same thing: create,
   * then poll while the status says processing. A client that assumed one mode
   * would break the moment the other was switched on.
   */
  const uploadBill = useCallback(
    async (file: File) => {
      setCreating(true);
      setError(null);
      setNotice(`Reading ${file.name}…`);
      try {
        const buf = await file.arrayBuffer();
        // base64 in chunks: a 12 MP photo blows the argument limit of
        // String.fromCharCode(...bytes) and throws RangeError.
        const bytes = new Uint8Array(buf);
        let binary = "";
        for (let i = 0; i < bytes.length; i += 0x8000) {
          binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
        }
        const created = await insightd.createDraft(companyID, {
          source: "upload",
          filename: file.name,
          content_type: file.type || "application/octet-stream",
          content_b64: btoa(binary),
          tenant: { company_id: companyID },
        });

        // Poll while the server is still reading it. Bounded: a spinner that
        // never resolves is indistinguishable from a hang, so this gives up
        // with a message the operator can act on.
        let draft = created;
        for (let i = 0; i < 60 && draft.status === "processing"; i++) {
          await new Promise((r) => setTimeout(r, 1000));
          draft = await insightd.getDraft(companyID, created.draft_id);
        }
        setNotice(
          draft.status === "processing"
            ? `${file.name} is still being read. It stays in the inbox — reopen this screen to check.`
            : `${file.name} read. Select it below to review in F9.`,
        );
        await refresh();
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        setNotice(null);
      } finally {
        setCreating(false);
      }
    },
    [insightd, companyID, refresh],
  );

  const rowLabels = useMemo(() => rows.map((r) => r.label), [rows]);

  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="mb-2 text-lg font-bold text-tally-title">Bill Inbox</h1>
      <p className="mb-3 text-sm">
        Pending OCR drafts. Select a bill to open it in the F9 Purchase voucher
        (review, then Ctrl+A to post). Esc to go back.
      </p>

      {error ? (
        <p data-testid="inbox-error" className="mb-2 text-sm text-red-400">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p data-testid="inbox-notice" className="mb-2 text-sm text-tally-title">
          {notice}
        </p>
      ) : null}

      {/* UPLOAD A BILL. The real front door of the OCR loop: until now the only
          way in was pasting fixture JSON, which is a developer's tool. */}
      <div className="mb-4 max-w-xl bg-tally-panel p-3">
        <h2 className="mb-1 text-sm text-tally-title">Upload a bill</h2>
        <input
          type="file"
          aria-label="Upload a bill"
          data-testid="inbox-upload"
          accept="image/*,application/pdf"
          disabled={creating}
          className="text-sm"
          onChange={(e) => {
            const file = e.target.files?.[0];
            // Clear the input so the SAME file can be re-uploaded after a
            // failure — a file picker fires no change event for an unchanged
            // selection, which would look like the button had stopped working.
            e.target.value = "";
            if (file) void uploadBill(file);
          }}
        />
        <p className="mt-1 text-xs">
          A photo or PDF of a supplier invoice. Reading it can take up to a
          minute; it appears in the list below when done, and you review it in
          F9 before anything is posted.
        </p>
      </div>

      <div className="mb-4 max-w-xl bg-tally-panel p-3">
        {loading ? (
          <p data-testid="inbox-loading" className="text-sm">
            Loading drafts…
          </p>
        ) : drafts.length === 0 ? (
          <p data-testid="inbox-empty" className="text-sm">
            No pending drafts. Create one from a fixture below.
          </p>
        ) : (
          <SelectionList
            label="Pending bills"
            options={rowLabels}
            onSelect={(label) => void openDraft(label)}
            autoFocus
          />
        )}
      </div>

      <div className="max-w-xl bg-tally-panel p-3">
        <h2 className="mb-1 text-sm text-tally-title">Create from fixture</h2>
        <textarea
          aria-label="Fixture JSON"
          data-testid="inbox-fixture"
          className="h-24 w-full bg-tally-field px-1 text-tally-fieldText outline-none"
          value={fixtureText}
          onChange={(e) => setFixtureText(e.target.value)}
          placeholder='{"...": "fixture JSON passed to insightd verbatim"}'
        />
        <button
          type="button"
          data-testid="inbox-create"
          disabled={creating}
          className="mt-2 bg-tally-active px-2 py-1 text-xs text-white disabled:opacity-50"
          onClick={() => void createFromFixture()}
        >
          {creating ? "Creating…" : "Create draft"}
        </button>
      </div>
    </section>
  );
}
