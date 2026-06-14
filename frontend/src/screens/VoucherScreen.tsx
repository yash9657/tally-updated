import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { SelectionList } from "./SelectionList";
import type { NavSnapshot } from "../keystroke/navMachine";
import type { VoucherType } from "../keystroke/types";
import { useClient, useCompanyID } from "../app/CompanyContext";
import { useRegisterScreenAction } from "../app/ScreenActions";
import { useLoadedVoucher } from "../app/LoadedVoucher";
import {
  acceptVoucher,
  deleteLoadedVoucher,
  type VoucherEditContext,
} from "./vouchers/editActions";
import { configFor } from "./vouchers/voucherConfig";
import type {
  DrCr,
  PostingRequest,
  VoucherRequest,
  DayBookEntry,
} from "../api";

/** Sample party ledgers for the auto-completing selection list. */
const PARTY_LEDGERS = [
  "Cash",
  "Bank",
  "Acme Traders",
  "Bharat Stores",
  "Chandra & Co",
  "Sales",
  "Purchase",
] as const;

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

/** Reconstruct VoucherRequest postings from a loaded day-book entry. */
function postingsFromEntry(entry: DayBookEntry): PostingRequest[] {
  return entry.postings.map((p) => ({
    // The day book carries ledger NAMEs; we resend them as the ledger reference.
    // (On alter the backend keys off voucher_id, not these; numbering/identity
    // are what must be preserved, which the voucher_no/date below carry.)
    ledger_id: p.ledger,
    dr_cr: p.dr_cr as DrCr,
    amount: p.amount,
  }));
}

/** The form state a voucher frame edits. */
interface VoucherForm {
  date: string;
  party: string;
  partyLedgerId: string;
  narration: string;
  postings: PostingRequest[];
  /** Display number: empty (=> "auto") on a fresh create, loaded on alter. */
  voucherNo: string;
}

function emptyForm(): VoucherForm {
  return {
    date: today(),
    party: "",
    partyLedgerId: "",
    narration: "",
    postings: [],
    voucherNo: "",
  };
}

function formFromEntry(entry: DayBookEntry): VoucherForm {
  return {
    date: entry.date,
    party: entry.party,
    partyLedgerId: "",
    narration: entry.narration,
    postings: postingsFromEntry(entry),
    voucherNo: entry.voucher_no,
  };
}

/**
 * The voucher entry/alteration screen. Holds the real form state, drives the
 * LIVE backend on Ctrl+A (Accept) and Alt+D (Delete) via {@link editActions},
 * and surfaces the server-resolved voucher_no. The XState navMachine stays pure
 * (it only moves the field cursor / screen stack); ALL form state + side effects
 * live here. In create mode the voucher_no field is read-only ("auto" until the
 * server allocates it); in alter mode it shows the loaded, resent number.
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
  const { loaded, setLoaded } = useLoadedVoucher();

  const type = (snapshot.voucherType ?? "sales") as VoucherType;
  const mode = snapshot.mode ?? "create";
  const isAlter = mode === "alter";

  // On an alter frame, consume the loaded voucher (drilled from a report) ONCE
  // to seed the form. The loaded voucher_id becomes the edit ctx.voucherID so
  // Ctrl+A routes to the per-class ALTER (PUT), not a create.
  const loadedEntry = isAlter ? (loaded?.entry ?? null) : null;
  const seedRef = useRef<string | null>(null);
  const [form, setForm] = useState<VoucherForm>(() =>
    loadedEntry ? formFromEntry(loadedEntry) : emptyForm(),
  );

  useEffect(() => {
    if (loadedEntry && seedRef.current !== loadedEntry.voucher_id) {
      seedRef.current = loadedEntry.voucher_id;
      setForm(formFromEntry(loadedEntry));
    }
  }, [loadedEntry]);

  const [status, setStatus] = useState<string | null>(null);
  const [savedNo, setSavedNo] = useState<string | null>(null);
  // Guard against a double-submit while an accept is in flight.
  const inFlight = useRef(false);

  // Keep the latest form/ctx in refs so the registered action handlers (stable
  // across renders) always read current state without re-registering.
  const formRef = useRef(form);
  formRef.current = form;

  const editCtx = useMemo<VoucherEditContext>(() => {
    const ctx: VoucherEditContext = { type, mode };
    if (isAlter && loadedEntry) {
      return { ...ctx, voucherID: loadedEntry.voucher_id };
    }
    return ctx;
  }, [type, mode, isAlter, loadedEntry]);
  const ctxRef = useRef(editCtx);
  ctxRef.current = editCtx;

  const buildRequest = useCallback((f: VoucherForm): VoucherRequest => {
    const req: VoucherRequest = {
      voucher_date: f.date,
      narration: f.narration,
      party: f.party,
      postings: f.postings,
    };
    if (f.partyLedgerId) req.party_ledger_id = f.partyLedgerId;
    // voucher_no: empty on create (server allocates); resent verbatim on alter.
    if (f.voucherNo) req.voucher_no = f.voucherNo;
    return req;
  }, []);

  // Ctrl+A Accept: POST (create) or PUT (alter) via editActions, then surface
  // the server-resolved voucher_no. Runs BEFORE the machine resets the frame.
  const onAccept = useCallback(() => {
    if (inFlight.current) return;
    inFlight.current = true;
    const req = buildRequest(formRef.current);
    acceptVoucher(client, companyID, ctxRef.current, req)
      .then((res) => {
        setSavedNo(res.voucher_no);
        setStatus("Saved");
      })
      .catch((err: unknown) => {
        setStatus(`Error: ${err instanceof Error ? err.message : String(err)}`);
      })
      .finally(() => {
        inFlight.current = false;
      });
  }, [buildRequest, client, companyID]);

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

  const cfg = configFor(type);
  const title = VOUCHER_TITLES[type] ?? type;
  const displayNo = savedNo ?? form.voucherNo;

  // Selecting a party from the list fills both the form and the machine's pick.
  function handleSelectParty(item: string) {
    setForm((f) => ({ ...f, party: item, partyLedgerId: "" }));
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
            options={PARTY_LEDGERS}
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
