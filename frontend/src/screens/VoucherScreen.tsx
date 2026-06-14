import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { SelectionList } from "./SelectionList";
import type { NavSnapshot } from "../keystroke/navMachine";
import type { VoucherType } from "../keystroke/types";
import {
  useClient,
  useCompanyID,
  useHomeStateCode,
} from "../app/CompanyContext";
import { useRegisterScreenAction } from "../app/ScreenActions";
import { useLoadedVoucher } from "../app/LoadedVoucher";
import {
  acceptVoucher,
  deleteLoadedVoucher,
  type VoucherEditContext,
} from "./vouchers/editActions";
import { configFor } from "./vouchers/voucherConfig";
import {
  emptyGstFormState,
  ensureLedger,
  ensureOutputTaxLedgers,
  buildGstVoucherRequest,
  type GstFormState,
} from "./vouchers/gstVoucher";
import { normalizeMoney } from "./vouchers/money";
import type {
  PostingRequest,
  VoucherRequest,
  VoucherResponse,
  VoucherView,
} from "../api";

/**
 * Sample party ledgers offered in the auto-completing selection list. These are
 * the NAMES shown to the user; the real ledger uuid is resolved on accept via
 * {@link ensureLedger} (create-or-select, idempotent) so postings carry valid
 * uuids — never the name. A couple carry a default GST state code used to seed
 * the place of supply.
 */
const PARTY_LEDGERS: readonly { name: string; group: string; stateCode?: string }[] = [
  { name: "Acme Traders", group: "Sundry Debtors", stateCode: "29" },
  { name: "Bharat Stores", group: "Sundry Debtors", stateCode: "27" },
  { name: "Chandra & Co", group: "Sundry Debtors", stateCode: "29" },
  { name: "Cash", group: "Cash-in-Hand" },
  { name: "Bank", group: "Bank Accounts" },
] as const;

const PARTY_LEDGER_NAMES = PARTY_LEDGERS.map((p) => p.name);

/** The income/expense ledger a GST class credits/debits, by class. */
const GST_INCOME_LEDGER: Record<string, { name: string; group: string }> = {
  sales: { name: "Domestic Sales", group: "Sales Accounts" },
  purchase: { name: "Purchases", group: "Purchase Accounts" },
};

const VOUCHER_TITLES: Record<string, string> = {
  contra: "Contra",
  payment: "Payment",
  receipt: "Receipt",
  journal: "Journal",
  sales: "Sales",
  purchase: "Purchase",
};

/** Today's date as YYYY-MM-DD (server default if the user leaves date empty). */
function today(): string {
  return new Date().toISOString().slice(0, 10);
}

/**
 * Map a fetched VoucherView's postings into request postings carrying the REAL
 * ledger_id uuids. This REPLACES the retired day-book `postingsFromEntry`, which
 * stuffed the ledger NAME into ledger_id (the documented name-as-uuid bug). The
 * GET endpoint returns canonical "D"/"C" dr_cr which we pass through verbatim.
 */
function postingsFromView(view: VoucherView): PostingRequest[] {
  return view.postings.map((p) => ({
    ledger_id: p.ledger_id,
    dr_cr: p.dr_cr,
    amount: p.amount,
  }));
}

/** The form state a voucher frame edits. */
interface VoucherForm {
  date: string;
  party: string;
  partyLedgerId: string;
  /** The chosen party's GST state code (seeds place of supply). */
  partyStateCode: string;
  narration: string;
  /**
   * The full restated postings (real ledger_id uuids). For GST vouchers these
   * are rebuilt from the GST slice on accept; for hydrated alters they seed the
   * non-GST send path verbatim.
   */
  postings: PostingRequest[];
  /** Display number: empty (=> "auto") on a fresh create, loaded on alter. */
  voucherNo: string;
  /** GST inputs (used only when cfg.showGst). */
  gst: GstFormState;
  /** The taxable base (income posting amount) for a GST voucher, fixed-4 string. */
  taxableValue: string;
}

function emptyForm(): VoucherForm {
  return {
    date: today(),
    party: "",
    partyLedgerId: "",
    partyStateCode: "",
    narration: "",
    postings: [],
    voucherNo: "",
    gst: emptyGstFormState(),
    taxableValue: "0.0000",
  };
}

/**
 * Seed the form from a fetched VoucherView (the GET hydration path). All alter
 * seeding flows through here so buildGstVoucherRequest / the verbatim send path
 * carry REAL posting uuids and persisted GST attrs (place_of_supply, taxable,
 * reverse_charge), and the loaded voucher_no/date preserve numbering.
 */
function formFromView(view: VoucherView): VoucherForm {
  return {
    date: view.voucher_date,
    party: view.party,
    partyLedgerId: view.party_ledger_id,
    partyStateCode: "",
    narration: view.narration,
    postings: postingsFromView(view),
    voucherNo: view.voucher_no,
    gst: {
      placeOfSupply: view.place_of_supply,
      gstRatePct: "",
      reverseCharge: view.reverse_charge,
      hsn: "",
    },
    taxableValue: view.taxable_value,
  };
}

/**
 * The voucher entry/alteration screen. Holds the real form state, drives the
 * LIVE backend on Ctrl+A (Accept) and Alt+D (Delete) via {@link editActions},
 * and surfaces the server-resolved voucher_no + the server-DERIVED GST split.
 * The XState navMachine stays pure (it only moves the field cursor / screen
 * stack); ALL form state + side effects live here.
 */
export function VoucherScreen({
  snapshot,
  onSelect,
}: {
  snapshot: NavSnapshot;
  onSelect: (item: string) => void;
}) {
  const client = useClient();
  const companyID = useCompanyID();
  const homeStateCode = useHomeStateCode();
  const { loaded, setLoaded } = useLoadedVoucher();

  const type = (snapshot.voucherType ?? "sales") as VoucherType;
  const mode = snapshot.mode ?? "create";
  const isAlter = mode === "alter";
  const cfg = configFor(type);

  // On an alter frame, the loaded report row gives us the logical voucher_id;
  // the FORM is hydrated from GET /vouchers/{id} (real posting uuids + GST
  // attrs), NOT the day-book names. The loaded voucher_id becomes ctx.voucherID
  // so Ctrl+A routes to the per-class ALTER (PUT), not a create.
  const loadedEntry = isAlter ? (loaded?.entry ?? null) : null;
  const loadedVoucherID = loadedEntry?.voucher_id ?? null;
  const seedRef = useRef<string | null>(null);
  const [form, setForm] = useState<VoucherForm>(emptyForm);
  const [status, setStatus] = useState<string | null>(null);
  const [savedNo, setSavedNo] = useState<string | null>(null);
  // The server-derived GST split from the last accept (display-only).
  const [taxResult, setTaxResult] = useState<{
    cgst: string;
    sgst: string;
    igst: string;
  } | null>(null);
  // Guard against a double-submit while an accept is in flight.
  const inFlight = useRef(false);

  // Hydrate the alter form from the GET endpoint when a new voucher is drilled.
  useEffect(() => {
    if (!loadedVoucherID || seedRef.current === loadedVoucherID) return;
    seedRef.current = loadedVoucherID;
    let cancelled = false;
    client
      .getVoucher(companyID, loadedVoucherID)
      .then((view) => {
        if (!cancelled) setForm(formFromView(view));
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setStatus(
            `Error loading voucher: ${err instanceof Error ? err.message : String(err)}`,
          );
        }
      });
    return () => {
      cancelled = true;
    };
  }, [client, companyID, loadedVoucherID]);

  // Keep the latest form/ctx in refs so the registered action handlers (stable
  // across renders) always read current state without re-registering.
  const formRef = useRef(form);
  formRef.current = form;

  const editCtx = useMemo<VoucherEditContext>(() => {
    const ctx: VoucherEditContext = { type, mode };
    if (isAlter && loadedVoucherID) {
      return { ...ctx, voucherID: loadedVoucherID };
    }
    return ctx;
  }, [type, mode, isAlter, loadedVoucherID]);
  const ctxRef = useRef(editCtx);
  ctxRef.current = editCtx;

  /** Build a plain (non-GST) request: forward the loaded/seeded postings verbatim. */
  const buildPlainRequest = useCallback((f: VoucherForm): VoucherRequest => {
    const req: VoucherRequest = {
      voucher_date: f.date,
      narration: f.narration,
      party: f.party,
      postings: f.postings,
    };
    if (f.partyLedgerId) req.party_ledger_id = f.partyLedgerId;
    if (f.voucherNo) req.voucher_no = f.voucherNo;
    return req;
  }, []);

  /**
   * Build the GST request: (1) ensure the three output-tax ledgers, (2) resolve
   * the real party + income ledger uuids, (3) assemble the balanced postings +
   * GSTRequest via the shared derive helper. The server re-derives and 422s on
   * any mismatch — the safety net keeping it authoritative.
   */
  const buildGstRequest = useCallback(
    async (f: VoucherForm): Promise<VoucherRequest> => {
      const outputTax = await ensureOutputTaxLedgers(client, companyID);
      // Resolve the party uuid: use the hydrated id if present, else ensure by name.
      const partyLedgerId =
        f.partyLedgerId ||
        (await ensureLedger(client, companyID, {
          name: f.party,
          groupName: "Sundry Debtors",
          stateCode: f.partyStateCode || undefined,
        }));
      const incomeSpec = GST_INCOME_LEDGER[type] ?? {
        name: "Domestic Sales",
        group: "Sales Accounts",
      };
      const incomeLedgerId = await ensureLedger(client, companyID, {
        name: incomeSpec.name,
        groupName: incomeSpec.group,
      });
      return buildGstVoucherRequest({
        partyLedgerId,
        incomeLedgerId,
        taxableValue: normalizeMoney(f.taxableValue),
        supplierStateCode: homeStateCode,
        gst: f.gst,
        outputTax,
        voucherNo: f.voucherNo || undefined,
        voucherDate: f.date,
        narration: f.narration,
        party: f.party,
      });
    },
    [client, companyID, homeStateCode, type],
  );

  // Ctrl+A Accept: POST (create) or PUT (alter) via editActions, then surface
  // the server-resolved voucher_no AND the server-derived GST split. Runs BEFORE
  // the machine resets the frame.
  const onAccept = useCallback(() => {
    if (inFlight.current) return;
    inFlight.current = true;
    const f = formRef.current;
    // Capture the edit context SYNCHRONOUSLY, before any async ledger-ensure /
    // GST-derive work: the machine may pop/reset the frame (create<->alter)
    // before the request promise resolves, so we must not re-read ctxRef late or
    // an alter would mis-route to a create (POST instead of PUT).
    const ctx = ctxRef.current;
    const reqPromise = cfg.showGst
      ? buildGstRequest(f)
      : Promise.resolve(buildPlainRequest(f));
    reqPromise
      .then((req) => acceptVoucher(client, companyID, ctx, req))
      .then((res: VoucherResponse) => {
        setSavedNo(res.voucher_no);
        // Display the server-derived split (never the JS-computed one).
        setTaxResult({ cgst: res.cgst, sgst: res.sgst, igst: res.igst });
        setStatus("Saved");
      })
      .catch((err: unknown) => {
        setStatus(`Error: ${err instanceof Error ? err.message : String(err)}`);
      })
      .finally(() => {
        inFlight.current = false;
      });
  }, [buildGstRequest, buildPlainRequest, cfg.showGst, client, companyID]);

  // Alt+D Delete (alter mode only): tombstone the loaded voucher, then clear the
  // loaded slot. The machine pops back to the originating report.
  const onDelete = useCallback(() => {
    if (!ctxRef.current.voucherID) return; // create-mode: nothing to delete
    deleteLoadedVoucher(client, companyID, ctxRef.current)
      .then(() => {
        setLoaded(null);
        setStatus("Deleted");
      })
      .catch((err: unknown) => {
        setStatus(`Error: ${err instanceof Error ? err.message : String(err)}`);
      });
  }, [client, companyID, setLoaded]);

  useRegisterScreenAction("accept", onAccept);
  useRegisterScreenAction("delete", isAlter ? onDelete : undefined);

  const title = VOUCHER_TITLES[type] ?? type;
  const displayNo = savedNo ?? form.voucherNo;

  // Selecting a party from the list fills the form name AND seeds the place of
  // supply from the party's state code (Tally defaults POS from the party, but
  // allows an override). The real uuid is resolved on accept.
  function handleSelectParty(item: string) {
    const spec = PARTY_LEDGERS.find((p) => p.name === item);
    setForm((f) => ({
      ...f,
      party: item,
      partyLedgerId: "",
      partyStateCode: spec?.stateCode ?? "",
      gst: {
        ...f.gst,
        // Seed POS from the party's state code (only if not already overridden).
        placeOfSupply: spec?.stateCode ?? f.gst.placeOfSupply,
      },
    }));
    onSelect(item);
  }

  return (
    <section className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field">
      <header className="mb-3 flex items-center justify-between">
        <h1 className="text-lg font-bold text-tally-title">
          {title} Voucher{isAlter ? " (Alteration)" : ""}
        </h1>
        <div className="flex items-center gap-3">
          {status ? (
            <span className="text-tally-active" role="status">
              {status}
            </span>
          ) : snapshot.saved ? (
            <span className="text-tally-active" role="status">
              Saved
            </span>
          ) : null}
          {savedNo ? (
            <span className="text-tally-title" data-testid="voucher-no">
              No: {savedNo}
            </span>
          ) : null}
        </div>
      </header>

      <p className="mb-2 text-xs text-tally-field">{cfg.hint}</p>

      <div className="bg-tally-panel p-3">
        <FieldRow name="Date" active={snapshot.field === "date"} value={form.date} />
        <FieldRow
          name="No."
          active={false}
          value={displayNo || "auto"}
          testId="voucher-no-field"
        />
        <div className={snapshot.field === "party-ledger" ? "" : "opacity-70"}>
          <SelectionList
            label={cfg.partyLabel}
            options={PARTY_LEDGER_NAMES}
            onSelect={handleSelectParty}
            autoFocus={snapshot.field === "party-ledger"}
          />
        </div>
        <FieldRow
          name="Narration"
          active={snapshot.field === "narration"}
          value={form.narration}
        />
      </div>

      {cfg.showGst ? (
        <GstPanel
          form={form}
          setForm={setForm}
          tax={taxResult}
        />
      ) : null}

      {snapshot.overlay === "change-mode" ? (
        <ChangeModeOverlay onSelect={onSelect} />
      ) : null}
    </section>
  );
}

function FieldRow({
  name,
  active,
  value,
  testId,
}: {
  name: string;
  active: boolean;
  value: string;
  testId?: string;
}) {
  return (
    <div className="flex items-center gap-2 py-0.5">
      <span className="w-32">{name}</span>
      <span
        data-testid={testId}
        className={
          active
            ? "bg-tally-active px-2 text-white"
            : "bg-tally-field px-2 text-tally-fieldText"
        }
      >
        {value || " "}
      </span>
    </div>
  );
}

/**
 * The GST sub-form (F8/F9, cfg.showGst only): taxable base + rate + place of
 * supply + reverse charge, and a Tax panel showing the SERVER-derived split
 * (read back from the POST response — never computed in JS). Money is rendered
 * as exact fixed-4 strings.
 */
function GstPanel({
  form,
  setForm,
  tax,
}: {
  form: VoucherForm;
  setForm: React.Dispatch<React.SetStateAction<VoucherForm>>;
  tax: { cgst: string; sgst: string; igst: string } | null;
}) {
  return (
    <div className="mt-3 bg-tally-panel p-3" data-testid="gst-panel">
      <h2 className="mb-2 text-tally-title">GST</h2>
      <label className="flex items-center gap-2 py-0.5">
        <span className="w-32">Taxable Value</span>
        <input
          data-testid="gst-taxable"
          className="bg-tally-field px-2 text-tally-fieldText"
          value={form.taxableValue}
          onChange={(e) =>
            setForm((f) => ({ ...f, taxableValue: e.target.value }))
          }
        />
      </label>
      <label className="flex items-center gap-2 py-0.5">
        <span className="w-32">GST Rate %</span>
        <input
          data-testid="gst-rate"
          className="bg-tally-field px-2 text-tally-fieldText"
          value={form.gst.gstRatePct}
          onChange={(e) =>
            setForm((f) => ({ ...f, gst: { ...f.gst, gstRatePct: e.target.value } }))
          }
        />
      </label>
      <label className="flex items-center gap-2 py-0.5">
        <span className="w-32">Place of Supply</span>
        <input
          data-testid="gst-pos"
          className="bg-tally-field px-2 text-tally-fieldText"
          value={form.gst.placeOfSupply}
          onChange={(e) =>
            setForm((f) => ({ ...f, gst: { ...f.gst, placeOfSupply: e.target.value } }))
          }
        />
      </label>
      {tax ? <TaxPanel tax={tax} /> : null}
    </div>
  );
}

/**
 * Render the server-returned tax split: CGST/SGST rows for intra-state, the
 * IGST row for inter-state (non-zero heads only), plus the invoice total. Proves
 * the user sees Tally's auto-derived split — money rendered as exact fixed-4
 * strings, never number-coerced.
 */
function TaxPanel({
  tax,
}: {
  tax: { cgst: string; sgst: string; igst: string };
}) {
  const isNonZero = (m: string) => Number.parseFloat(m || "0") !== 0;
  return (
    <div className="mt-2 border-t border-tally-field pt-2" data-testid="tax-panel">
      <h3 className="text-tally-title">Tax</h3>
      {isNonZero(tax.cgst) ? (
        <TaxRow label="CGST" amount={tax.cgst} testId="tax-cgst" />
      ) : null}
      {isNonZero(tax.sgst) ? (
        <TaxRow label="SGST" amount={tax.sgst} testId="tax-sgst" />
      ) : null}
      {isNonZero(tax.igst) ? (
        <TaxRow label="IGST" amount={tax.igst} testId="tax-igst" />
      ) : null}
    </div>
  );
}

function TaxRow({
  label,
  amount,
  testId,
}: {
  label: string;
  amount: string;
  testId: string;
}) {
  return (
    <div className="flex items-center gap-2 py-0.5">
      <span className="w-32">{label}</span>
      <span data-testid={testId} className="bg-tally-field px-2 text-tally-fieldText">
        {amount}
      </span>
    </div>
  );
}

/** The Ctrl+H Change Mode picker (Item Invoice / Accounting Invoice / As Voucher). */
function ChangeModeOverlay({ onSelect }: { onSelect: (item: string) => void }) {
  return (
    <div
      role="dialog"
      aria-label="Change Mode"
      className="mt-3 mx-auto w-72 bg-tally-panel p-3"
    >
      <h2 className="mb-2 text-tally-title">Change Mode</h2>
      <SelectionList
        label="Mode"
        options={["Item Invoice", "Accounting Invoice", "As Voucher"]}
        onSelect={onSelect}
        autoFocus
      />
    </div>
  );
}
