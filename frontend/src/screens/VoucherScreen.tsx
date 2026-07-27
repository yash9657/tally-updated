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
import { useLoadedDraft } from "../app/LoadedDraft";
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
import { acceptEditedPurchaseDraft, canRebuildDraft } from "./vouchers/draftAccept";
import { acceptPurchaseDraft, ApiError } from "../api";
import type {
  DayBookEntry,
  FieldConfidence,
  PostingRequest,
  PurchaseVoucherDraft,
  VoucherDraftBody,
  VoucherRequest,
  VoucherResponse,
  VoucherView,
} from "../api";

/**
 * The account groups a PARTY field offers. A voucher's party is a debtor, a
 * creditor, or the cash/bank account being moved — never an income or tax
 * ledger, and offering those would invite an unbalanced or nonsensical entry.
 */
const PARTY_GROUPS: readonly string[] = [
  "Sundry Debtors",
  "Sundry Creditors",
  "Cash-in-Hand",
  "Bank Accounts",
  "Bank OD A/c",
];

/**
 * One selectable party, resolved from the company's REAL chart of accounts.
 *
 * This replaced a hardcoded sample list ("Acme Traders", "Bharat Stores",
 * "Chandra & Co") that existed in no seeded company — so the picker offered
 * names that were not accounts, and accepting one CREATED a ledger as a side
 * effect of keying a voucher. Two consequences, both bad: an operator could
 * silently mint a near-duplicate of a real party by picking the sample with the
 * similar name, and a Data Entry operator — who may key vouchers but may NOT
 * create masters — got a 403 from the ledger POST rather than from anything
 * they meant to do.
 *
 * Carrying the real `id` also means accepting a voucher for an existing party
 * performs NO write to create it.
 */
interface PartyOption {
  id: string;
  name: string;
  stateCode: string;
}

/** The income/expense ledger a GST class credits/debits, by class. */
const GST_INCOME_LEDGER: Record<string, { name: string; group: string }> = {
  sales: { name: "Domestic Sales", group: "Sales Accounts" },
  purchase: { name: "Purchases", group: "Purchase Accounts" },
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
 * Seed the form from an OCR-derived {@link VoucherDraftBody} (the F9 draft-mode
 * prefill path). DISPLAY ONLY: the accept POSTs the ORIGINAL balanced body, not
 * a form rebuild, so this only populates the visible fields. Money/qty/rate stay
 * exact fixed-4 strings — never Number()-coerced.
 */
function formFromDraft(draft: PurchaseVoucherDraft): VoucherForm {
  const body = draft.body;
  const line = body.gst?.lines[0];
  return {
    date: body.voucher_date ?? today(),
    party: body.party ?? "",
    partyLedgerId: body.party_ledger_id ?? "",
    partyStateCode: "",
    narration: body.narration ?? "",
    postings: [],
    voucherNo: body.voucher_no ?? "",
    gst: {
      placeOfSupply: body.gst?.place_of_supply_state_code ?? "",
      gstRatePct: line?.gst_rate_pct ?? "",
      reverseCharge: body.gst?.reverse_charge ?? false,
      hsn: line?.hsn ?? "",
    },
    taxableValue: line?.taxable_value ?? "0.0000",
  };
}

/** The editable draft fields, and their OCR-confidence keys (for the amber-drop). */
const DRAFT_EDITABLE_FIELDS = ["date", "narration", "taxableValue", "gstRatePct", "placeOfSupply"] as const;
type DraftEditableField = (typeof DRAFT_EDITABLE_FIELDS)[number];

/** confidence key an edited form field invalidates (narration has none). */
const DRAFT_FIELD_CONF_KEY: Partial<Record<DraftEditableField, string>> = {
  date: "invoice_date",
  taxableValue: "totals",
  gstRatePct: "totals",
  placeOfSupply: "place_of_supply",
};

/** The set of editable draft fields whose live value differs from the seed. */
function draftDirtyFields(seed: VoucherForm | null, form: VoucherForm): Set<DraftEditableField> {
  const dirty = new Set<DraftEditableField>();
  if (!seed) return dirty;
  if (form.date !== seed.date) dirty.add("date");
  if (form.narration !== seed.narration) dirty.add("narration");
  if (form.taxableValue !== seed.taxableValue) dirty.add("taxableValue");
  if (form.gst.gstRatePct !== seed.gst.gstRatePct) dirty.add("gstRatePct");
  if (form.gst.placeOfSupply !== seed.gst.placeOfSupply) dirty.add("placeOfSupply");
  return dirty;
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
  const { loaded: loadedDraft, setLoaded: setLoadedDraft } = useLoadedDraft();

  const type = (snapshot.voucherType ?? "sales") as VoucherType;
  const mode = snapshot.mode ?? "create";
  const isAlter = mode === "alter";
  const cfg = configFor(type);

  // DRAFT MODE: the F9 Purchase create screen hydrated from an OCR draft. Active
  // only for a fresh (non-alter) purchase with a loaded draft. The POST on
  // accept sends the ORIGINAL balanced body (acceptPurchaseDraft), NOT a form
  // rebuild — the form prefill below is display-only.
  const isDraftMode =
    type === "purchase" && !isAlter && loadedDraft != null;

  // On an alter frame, the loaded report row gives us the logical voucher_id;
  // the FORM is hydrated from GET /vouchers/{id} (real posting uuids + GST
  // attrs), NOT the day-book names. The loaded voucher_id becomes ctx.voucherID
  // so Ctrl+A routes to the per-class ALTER (PUT), not a create.
  // THE PARTY PICKER'S OPTIONS, from the company's real chart of accounts.
  // Read once per company: the list is a master, not a transaction, and a
  // refetch on every keystroke would make the picker feel broken.
  const [parties, setParties] = useState<PartyOption[]>([]);
  const [partiesError, setPartiesError] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    client
      .listLedgers(companyID)
      .then((res) => {
        if (!live) return;
        setParties(
          res.ledgers
            .filter((l) => PARTY_GROUPS.includes(l.group_name))
            .map((l) => ({ id: l.id, name: l.name, stateCode: l.state_code }))
            .sort((a, b) => a.name.localeCompare(b.name)),
        );
        setPartiesError(null);
      })
      .catch((e: unknown) => {
        if (!live) return;
        // NOT silent. An empty picker with no explanation reads as "this
        // company has no customers", which is a different and much more
        // alarming statement than "the list could not be loaded".
        setPartiesError(
          e instanceof ApiError
            ? `Chart of accounts unavailable (${e.status}). ${e.message}`
            : e instanceof Error
              ? e.message
              : String(e),
        );
      });
    return () => {
      live = false;
    };
  }, [client, companyID]);
  const partyNames = parties.map((p) => p.name);

  const loadedEntry = isAlter ? (loaded?.entry ?? null) : null;
  const loadedVoucherID = loadedEntry?.voucher_id ?? null;
  const seedRef = useRef<string | null>(null);
  const draftSeedRef = useRef<string | null>(null);
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
  //
  // The "already seeded" guard is stamped only AFTER the response is applied,
  // never before the fetch. Under React StrictMode every effect runs twice: the
  // first run's cleanup sets cancelled=true (so its response is dropped) and, if
  // the guard had already been stamped, the second run would return early — the
  // drilled voucher would then load into an EMPTY form, silently. (Ctrl+A on it
  // would then alter a real voucher into blank lines.) Stamping after the apply
  // costs one extra idempotent GET under StrictMode and makes the seed correct.
  useEffect(() => {
    if (!loadedVoucherID || seedRef.current === loadedVoucherID) return;
    let cancelled = false;
    client
      .getVoucher(companyID, loadedVoucherID)
      .then((view) => {
        if (cancelled) return;
        seedRef.current = loadedVoucherID;
        setForm(formFromView(view));
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

  // Seed the form from an OCR draft when entering draft mode (keyed by draft_id,
  // mirroring the alter seedRef guard so a re-render doesn't clobber edits and a
  // fresh draft re-seeds). Prefill is display-only; accept POSTs the orig body.
  // draftSeedFormRef holds the pristine form the draft seeded, so an edit is
  // detected by comparing the live form to it (no parallel dirty flags to keep
  // in sync). Cleared/re-set whenever a fresh draft seeds.
  const draftSeedFormRef = useRef<VoucherForm | null>(null);
  const draftID = isDraftMode ? loadedDraft.draft.draft_id : null;
  useEffect(() => {
    if (!draftID || draftSeedRef.current === draftID) return;
    draftSeedRef.current = draftID;
    if (loadedDraft) {
      const seeded = formFromDraft(loadedDraft.draft);
      draftSeedFormRef.current = seeded;
      setForm(seeded);
    }
  }, [draftID, loadedDraft]);

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
  // Keep the active draft body in a ref so the stable accept handler reads the
  // current draft without re-registering. null when not in draft mode.
  const draftBodyRef = useRef<VoucherDraftBody | null>(null);
  draftBodyRef.current = isDraftMode ? loadedDraft.draft.body : null;

  const onAccept = useCallback(() => {
    if (inFlight.current) return;
    inFlight.current = true;
    // DRAFT MODE: the ONLY write path is acceptPurchaseDraft, which resolves any
    // null ledger ids (Alt+C create-on-the-fly) then POSTs the ORIGINAL balanced
    // body verbatim to /vouchers/purchase — NOT a form rebuild. Accept is the
    // sole human-in-the-loop write; there is no insightd ledger-write path.
    const draftBody = draftBodyRef.current;
    if (draftBody) {
      // Pristine accept POSTs the balanced draft body byte-identical (the proven
      // path). A DIRTY accept (any editable field changed) rebuilds a balanced,
      // orientation-preserving body from the edits via acceptEditedPurchaseDraft
      // — NOT buildGstVoucherRequest (which is sales-shaped). Nothing posts before
      // this Ctrl+A; HITL is untouched.
      const seed = draftSeedFormRef.current;
      const f = formRef.current;
      const dirty = draftDirtyFields(seed, f);
      // Only rebuild when the shape is editable; a non-rebuildable draft always
      // posts pristine (the disclosed edit-deferred path), never silently errors.
      const accept =
        dirty.size > 0 && canRebuildDraft(draftBody)
          ? acceptEditedPurchaseDraft(client, companyID, draftBody, {
              voucherDate: f.date,
              narration: f.narration,
              taxableValue: f.taxableValue,
              gstRatePct: f.gst.gstRatePct,
              placeOfSupply: f.gst.placeOfSupply,
            })
          : acceptPurchaseDraft(client, companyID, draftBody);
      accept
        .then((res: VoucherResponse) => {
          setSavedNo(res.voucher_no);
          setTaxResult({ cgst: res.cgst, sgst: res.sgst, igst: res.igst });
          setStatus("Saved");
          setLoadedDraft(null);
          draftSeedFormRef.current = null;
        })
        .catch((err: unknown) => {
          setStatus(
            `Error: ${err instanceof Error ? err.message : String(err)}`,
          );
        })
        .finally(() => {
          inFlight.current = false;
        });
      return;
    }
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
  }, [
    buildGstRequest,
    buildPlainRequest,
    cfg.showGst,
    client,
    companyID,
    setLoadedDraft,
  ]);

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

  /**
   * Alt+X Cancel. In CREATE mode it clears the form (the machine resets the
   * frame off the same key). A SAVED voucher cannot be "cancelled" here: the
   * ledger is append-only, so the honest answer is Alt+D, which writes a delete
   * tombstone as a new version rather than mutating history.
   */
  const onCancelVoucher = useCallback((): string => {
    if (ctxRef.current.voucherID) {
      return "A saved voucher cannot be cancelled in this build — Alt+D deletes it (appends a delete version; nothing is erased).";
    }
    setForm(emptyForm());
    setStatus(null);
    setSavedNo(null);
    setTaxResult(null);
    return "Voucher cancelled — the form is cleared.";
  }, []);

  /**
   * Alt+2 Duplicate. Keeps the loaded voucher's lines but drops its identity
   * (number + loaded slot) so Ctrl+A POSTS a NEW voucher instead of altering the
   * original. The machine flips the frame to create mode off the same key.
   */
  const onDuplicate = useCallback((): string => {
    if (!ctxRef.current.voucherID) {
      return "Nothing to duplicate — open a saved voucher (e.g. from the Day Book) first.";
    }
    setForm((f) => ({ ...f, voucherNo: "" }));
    setSavedNo(null);
    setLoaded(null);
    seedRef.current = null;
    return "Duplicated — same lines, new voucher. Ctrl+A to save it.";
  }, [setLoaded]);

  /**
   * Page Up / Page Down — browse the SAVED vouchers around the loaded one, in
   * Day Book order (the same ordering the user drilled from). The Day Book is
   * fetched once and cached in a ref; moving loads the neighbour into the
   * loaded-voucher slot, which re-hydrates this form from GET /vouchers/{id}.
   */
  const dayBookRef = useRef<DayBookEntry[] | null>(null);
  const stepVoucher = useCallback(
    (delta: number): string => {
      const currentID = ctxRef.current.voucherID;
      if (!currentID) {
        return "Page Up / Page Down browse saved vouchers — open one from the Day Book first.";
      }
      const apply = (entries: DayBookEntry[]): string => {
        const idx = entries.findIndex((e) => e.voucher_id === currentID);
        if (idx < 0) return "This voucher is not in the current Day Book range.";
        const next = entries[idx + delta];
        if (!next) {
          return delta < 0
            ? "Already at the first voucher."
            : "Already at the last voucher.";
        }
        setLoaded({ entry: next });
        return `Voucher ${next.voucher_no} (${next.date})`;
      };
      if (dayBookRef.current) return apply(dayBookRef.current);
      client
        .getDayBook(companyID, {})
        .then((res) => {
          dayBookRef.current = res.entries;
          apply(res.entries);
        })
        .catch(() => {
          /* the status line already told the user we are loading */
        });
      return "Loading the Day Book to browse vouchers…";
    },
    [client, companyID, setLoaded],
  );

  useRegisterScreenAction("accept", onAccept);
  useRegisterScreenAction("delete", isAlter ? onDelete : undefined);
  useRegisterScreenAction("cancelVoucher", onCancelVoucher);
  useRegisterScreenAction("duplicate", onDuplicate);
  useRegisterScreenAction("prevVoucher", () => stepVoucher(-1));
  useRegisterScreenAction("nextVoucher", () => stepVoucher(1));

  // Title comes from the voucher CONFIG, which is the single source of truth for
  // per-type presentation. A second local map here silently fell behind when
  // credit-note/debit-note were added and rendered the raw slug
  // ("credit-note Voucher") in the header.
  const title = cfg.title;
  const displayNo = savedNo ?? form.voucherNo;

  // Draft-mode edit affordances: whether the draft's shape is rebuildable
  // (single-line, non-RCM, non-exempt), and which OCR-confidence chips to drop
  // because the human has overridden the underlying field.
  const draftRebuildable = isDraftMode ? canRebuildDraft(loadedDraft.draft.body) : false;
  const draftDirtyConfKeys = new Set<string>();
  if (isDraftMode) {
    for (const field of draftDirtyFields(draftSeedFormRef.current, form)) {
      const k = DRAFT_FIELD_CONF_KEY[field];
      if (k) draftDirtyConfKeys.add(k);
    }
  }

  // Selecting a party fills the form name, carries its REAL ledger id, and seeds
  // the place of supply from that ledger's own state code (Tally defaults POS
  // from the party, and allows an override).
  //
  // Carrying `partyLedgerId` is what stops the accept path creating a ledger for
  // a party that already exists. A name typed FREE-HAND still falls through to
  // the create-or-select on accept, which is how a new party is added inline —
  // and which a Data Entry level is correctly refused.
  function handleSelectParty(item: string) {
    const party = parties.find((p) => p.name === item);
    setForm((f) => ({
      ...f,
      party: item,
      partyLedgerId: party?.id ?? "",
      partyStateCode: party?.stateCode ?? "",
      gst: {
        ...f.gst,
        // Seed POS from the party's state code (only if not already overridden).
        placeOfSupply: party?.stateCode || f.gst.placeOfSupply,
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
        <FieldRow
          name="Date"
          active={snapshot.field === "date"}
          value={form.date}
          editable={draftRebuildable}
          onChange={(v) => setForm((f) => ({ ...f, date: v }))}
          testId={isDraftMode ? "draft-date" : undefined}
        />
        <FieldRow
          name="No."
          active={false}
          value={displayNo || "auto"}
          testId="voucher-no-field"
        />
        <div className={snapshot.field === "party-ledger" ? "" : "opacity-70"}>
          <SelectionList
            label={cfg.partyLabel}
            options={partyNames}
            onSelect={handleSelectParty}
            autoFocus={snapshot.field === "party-ledger"}
            // A party the company does not have yet is created on accept — the
            // only way to key the FIRST voucher of a brand-new company, and
            // Tally's own behaviour for a master field.
            allowCreate
          />
          {partiesError ? (
            <p data-testid="party-list-error" className="text-xs text-red-400">
              {partiesError} — type a name to create the party on accept
              (Alt+C also creates one inline).
            </p>
          ) : parties.length === 0 ? (
            <p data-testid="party-list-empty" className="text-xs">
              No debtor, creditor, cash or bank accounts in this company yet —
              type a name to create one on accept.
            </p>
          ) : null}
        </div>
        <FieldRow
          name="Narration"
          active={snapshot.field === "narration"}
          value={form.narration}
          editable={draftRebuildable}
          onChange={(v) => setForm((f) => ({ ...f, narration: v }))}
          testId={isDraftMode ? "draft-narration" : undefined}
        />
      </div>

      {cfg.showGst ? (
        <GstPanel
          form={form}
          setForm={setForm}
          tax={taxResult}
        />
      ) : null}

      {isDraftMode ? (
        <DraftPrefillPanel
          draft={loadedDraft.draft}
          dirtyConfKeys={draftDirtyConfKeys}
          editable={draftRebuildable}
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
  editable,
  onChange,
}: {
  name: string;
  active: boolean;
  value: string;
  testId?: string;
  /** Draft mode: render an editable input while keeping the active highlight. */
  editable?: boolean;
  onChange?: (v: string) => void;
}) {
  return (
    <div className="flex items-center gap-2 py-0.5">
      <span className="w-32">{name}</span>
      {editable ? (
        <input
          data-testid={testId}
          className={
            active
              ? "bg-tally-active px-2 text-white outline-none"
              : "bg-tally-field px-2 text-tally-fieldText outline-none"
          }
          value={value}
          onChange={(e) => onChange?.(e.target.value)}
        />
      ) : (
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
      )}
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

/**
 * A header field is "amber" (needs review) when its confidence is below 0.80.
 * Missing confidence (undefined) is NOT amber: absent fields are surfaced via
 * the unresolved list instead, not flagged on present rows.
 */
function amber(conf?: FieldConfidence): boolean {
  return conf != null && conf.confidence < 0.8;
}

/** Inline amber color (tailwind warn class absent); applied to low-confidence rows. */
const AMBER_STYLE: React.CSSProperties = { color: "#d97706" };

/**
 * The OCR draft prefill panel (DRAFT MODE only). Surfaces, read-only:
 *  - a status banner (ready vs low_confidence_manual),
 *  - per-field amber confidence flags on the header fields,
 *  - the unresolved-field focus hints,
 *  - the redaction proof (PII counts),
 *  - and the derived GST split PREVIEW (server re-derives on submit).
 * Money is rendered as exact fixed-4 strings, never Number()-coerced.
 */
function DraftPrefillPanel({
  draft,
  dirtyConfKeys,
  editable,
}: {
  draft: PurchaseVoucherDraft;
  /** OCR-confidence keys whose field the human has edited (drop the amber chip). */
  dirtyConfKeys: Set<string>;
  /** Whether the draft is rebuildable; false => a disclosed edit-deferred note. */
  editable: boolean;
}) {
  const header = draft.confidence.header;
  const ready = draft.status === "ready";

  // The header confidence rows we surface, keyed to the contract's header map.
  const headerRows: readonly { key: string; label: string }[] = [
    { key: "invoice_no", label: "Invoice No." },
    { key: "invoice_date", label: "Invoice Date" },
    { key: "supplier", label: "Supplier" },
    { key: "place_of_supply", label: "Place of Supply" },
    { key: "totals", label: "Totals" },
  ];

  const red = draft.redaction;
  const redCounts = Object.entries(red.counts_by_type)
    .map(([k, n]) => `${k} ${n}`)
    .join(", ");
  const dg = draft.derived_gst;

  return (
    <div className="mt-3 bg-tally-panel p-3" data-testid="draft-panel">
      <div
        data-testid="draft-status"
        className="mb-2 font-bold"
        style={ready ? undefined : AMBER_STYLE}
      >
        {ready
          ? "Ready — press Ctrl+A to accept"
          : "Low confidence — review every field before accepting"}
      </div>

      {!editable ? (
        <div data-testid="draft-edit-disabled" className="mb-2 text-xs" style={AMBER_STYLE}>
          Editing is not available for this draft (multi-line, reverse-charge or
          exempt); accept as-is or fix it after posting.
        </div>
      ) : null}

      <div className="mb-2">
        {headerRows.map((row) => {
          const conf = header[row.key];
          // An EDITED field drops its amber confidence chip: the OCR confidence
          // described the machine's read, not the human's override.
          const edited = dirtyConfKeys.has(row.key);
          const isAmber = amber(conf) && !edited;
          return (
            <div
              key={row.key}
              data-testid={`conf-${row.key}`}
              className={
                "flex items-center gap-2 py-0.5" +
                (isAmber ? " text-tally-warn" : "")
              }
              style={isAmber ? AMBER_STYLE : undefined}
            >
              <span className="w-32">{row.label}</span>
              <span>
                {edited
                  ? "edited"
                  : conf
                    ? `${(conf.confidence * 100).toFixed(0)}% (${conf.source})`
                    : "—"}
              </span>
            </div>
          );
        })}
      </div>

      {draft.unresolved.length > 0 ? (
        <div data-testid="draft-unresolved" className="mb-2" style={AMBER_STYLE}>
          <span className="w-32">Review: </span>
          <span>{draft.unresolved.join(", ")}</span>
        </div>
      ) : null}

      <div data-testid="draft-redaction" className="mb-2 text-xs">
        PII redacted: {red.total}
        {redCounts ? ` (${redCounts})` : ""}
      </div>

      {dg ? (
        <div
          data-testid="draft-derived-gst"
          className="mt-2 border-t border-tally-field pt-2 text-xs"
        >
          <div className="text-tally-title">
            Derived GST (preview — server re-derives on submit)
          </div>
          <div>CGST {dg.cgst}</div>
          <div>SGST {dg.sgst}</div>
          <div>IGST {dg.igst}</div>
          <div>Total Tax {dg.total_tax}</div>
          <div>Invoice Total {dg.invoice_total}</div>
        </div>
      ) : null}
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
