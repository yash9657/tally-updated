import { buildBillAllocations, type DraftBillRef } from "./billAllocation";
/**
 * GST wiring for the Sales/Purchase voucher form (used only when
 * cfg.showGst). Three responsibilities, all in the screen layer (the XState
 * machine stays pure):
 *
 *  1. {@link ensureOutputTaxLedgers} — idempotently create/select the three
 *     Duties & Taxes output-tax ledgers (Output CGST/SGST/IGST) per company and
 *     memoize their ids. Mirrors the server's masters.EnsureLedger (idempotent
 *     by name) — the user is never blocked to pre-create them, exactly as in
 *     Tally where GST ledgers pre-exist under Duties & Taxes.
 *
 *  2. {@link GstFormState} — the extra inputs the F8/F9 form collects:
 *     place of supply (defaulted from the party's state code), a single
 *     per-voucher GST rate %, reverse charge, and an optional HSN.
 *
 *  3. {@link buildGstVoucherRequest} — assemble a balanced, server-verifiable
 *     VoucherRequest: party Dr (total), income Cr (taxable), and the NON-ZERO
 *     tax Cr lines the split implies (intra: CGST+SGST; inter: IGST), plus the
 *     GSTRequest block. Tax amounts come from the shared derive helper so they
 *     equal the server's derivation (deriveAndVerifyGST 422s on mismatch).
 */

import type {
  CoredClient,
  GSTRequest,
  PostingRequest,
  VoucherRequest,
} from "../../api";
import { addMoney } from "./money";
import { deriveGstSplit } from "./gstDerive";

/** The three resolved Duties & Taxes output-tax ledger ids for a company. */
export interface OutputTaxLedgers {
  cgstId: string;
  sgstId: string;
  igstId: string;
}

/** The extra GST inputs the Sales/Purchase form collects (cfg.showGst only). */
export interface GstFormState {
  /** 2-digit place-of-supply state code; defaulted from the party's state code. */
  placeOfSupply: string;
  /** A single per-voucher GST rate %, as a free-typed string (e.g. "18"). */
  gstRatePct: string;
  reverseCharge: boolean;
  /** Optional HSN/SAC text. */
  hsn: string;
}

export function emptyGstFormState(): GstFormState {
  return { placeOfSupply: "", gstRatePct: "", reverseCharge: false, hsn: "" };
}

/**
 * Per-company memoized output-tax ledger cache. EnsureLedger is idempotent
 * server-side (by name under Duties & Taxes), but we still memoize/serialize per
 * company so concurrent first-GST posts don't fire duplicate createLedger calls
 * and resolve inconsistent ids. The promise (not just the value) is cached so a
 * second call in flight reuses the first request rather than racing it.
 */
const OUTPUT_TAX_CACHE = new Map<string, Promise<OutputTaxLedgers>>();

/** Test-only: clear the per-company output-tax ledger memo. */
export function _resetOutputTaxLedgerCache(): void {
  OUTPUT_TAX_CACHE.clear();
}

const OUTPUT_TAX_GROUP = "Duties & Taxes";

/**
 * Per-(company, ledger name) memoized RESOLVE-then-create.
 *
 * ⛔⛔ THIS COMMENT USED TO SAY "createLedger is idempotent server-side (by name
 * within a company), so re-posting reuses the same id". THAT WAS FALSE, and the whole
 * helper was built on it.
 *
 * `INSERT INTO ledger` (internal/masters/masters.go) has NO `ON CONFLICT`, and
 * `ledger_company_id_name_key` is a plain UNIQUE index on (company_id, name). So
 * creating a ledger that already exists is not idempotent — it is a raw Postgres
 * unique-violation, surfaced to the operator as
 * `masters: insert ledger: ERROR: duplicate key value violates unique constraint`.
 *
 * The consequence was severe and entirely invisible to the test suite: on a FRESH PAGE
 * LOAD the memo below is empty, so keying a Payment / Receipt / Contra against any
 * ledger the company ALREADY HAS — which is the normal case, and every case after the
 * first day — failed with that SQL error. It only ever appeared to work because the
 * memo was warm from an earlier post in the same page session, or because the ledger
 * genuinely did not exist yet. Found by driving the app.
 *
 * So this now RESOLVES FIRST, against the company's own chart of accounts, and creates
 * only what is genuinely absent. The server stays strict — creating a duplicate
 * SHOULD be an error on a masters form, and making the INSERT idempotent would let a
 * real mistake pass silently. It is this helper that wants find-or-create, so it is
 * this helper that does the finding.
 *
 * ⚠️ THE NAME MATCH IS EXACT, mirroring the index. "electricity" and "Electricity" are
 * two different ledgers to the constraint, so resolving them to one here would make
 * this helper disagree with the database about what is a duplicate.
 */
const LEDGER_CACHE = new Map<string, Promise<string>>();

/**
 * The company's chart of accounts, by exact name, fetched ONCE per company.
 *
 * The PROMISE is cached, not the value, so several particulars resolving at the same
 * time share one request instead of racing it.
 */
const LEDGER_INDEX_CACHE = new Map<string, Promise<Map<string, string>>>();

function ledgerIndex(
  client: CoredClient,
  companyID: string,
): Promise<Map<string, string>> {
  const cached = LEDGER_INDEX_CACHE.get(companyID);
  if (cached) return cached;
  const p = client
    .listLedgers(companyID)
    .then((r) => new Map(r.ledgers.map((l) => [l.name, l.id])))
    .catch((err) => {
      // NOT swallowed into an empty index: an empty one would mean "this company has
      // no ledgers", and the caller would try to CREATE every name it needs and hit
      // the very constraint violation this function exists to avoid.
      LEDGER_INDEX_CACHE.delete(companyID);
      throw err;
    });
  LEDGER_INDEX_CACHE.set(companyID, p);
  return p;
}

/** Test-only: clear the per-(company,name) ledger memo and the chart index. */
export function _resetLedgerCache(): void {
  LEDGER_CACHE.clear();
  LEDGER_INDEX_CACHE.clear();
}

export interface EnsureLedgerSpec {
  name: string;
  groupName: string;
  stateCode?: string;
}

/**
 * Resolve a ledger id by name for a company, creating it under the given group
 * if absent (idempotent + memoized). Used to resolve the real income ledger
 * ("Domestic Sales" / "Purchases") and a real party ledger uuid for the GST
 * form, replacing the sample name-only flow.
 */
export function ensureLedger(
  client: CoredClient,
  companyID: string,
  spec: EnsureLedgerSpec,
): Promise<string> {
  const key = `${companyID}::${spec.name}`;
  const cached = LEDGER_CACHE.get(key);
  if (cached) return cached;
  const p = (async (): Promise<string> => {
    // ---- RESOLVE FIRST. See the note on LEDGER_CACHE: creating a ledger that
    // already exists is a raw unique-violation, not a no-op.
    const index = await ledgerIndex(client, companyID);
    const existing = index.get(spec.name);
    if (existing !== undefined) return existing;
    try {
      const r = await client.createLedger(companyID, {
        name: spec.name,
        group_name: spec.groupName,
        ...(spec.stateCode ? { state_code: spec.stateCode } : {}),
      });
      // Remembered, so a second particular naming this same NEW ledger resolves to
      // the id just minted instead of trying to create it again.
      index.set(spec.name, r.ledger_id);
      return r.ledger_id;
    } catch (err) {
      // ⚠️ THE RACE THE MEMO CANNOT COVER: another tab, another operator, or a
      // create that landed between our list and our insert. Re-read the chart and
      // resolve, because by now the ledger exists and the operator's voucher is
      // perfectly postable — failing here would refuse a valid voucher for a reason
      // that has already ceased to be true.
      LEDGER_INDEX_CACHE.delete(companyID);
      const fresh = await ledgerIndex(client, companyID).catch(() => null);
      const found = fresh?.get(spec.name);
      if (found !== undefined) return found;
      throw err;
    }
  })().catch((err) => {
    LEDGER_CACHE.delete(key);
    throw err;
  });
  LEDGER_CACHE.set(key, p);
  return p;
}

/**
 * Ensure (create-or-select) the three output-tax ledgers for a company and
 * cache their ids. Idempotent and memoized: the first GST post on a fresh
 * company creates the three that are missing; subsequent posts reuse the cached
 * ids (no new calls).
 *
 * ===========================================================================
 * ⛔⛔ THIS FUNCTION CALLED createLedger UNCONDITIONALLY FOR TWO RELEASES, AND
 * IT WAS FOUND BY A BROWSER — AFTER THE "SAME" BUG WAS ALREADY FIXED ABOVE
 * ===========================================================================
 * `ensureLedger` above was corrected to resolve-then-create when the duplicate
 * key defect was found on the Payment / Receipt / Contra path. THIS FUNCTION
 * WAS NOT, and it is the identical bug one function down: `INSERT INTO ledger`
 * has no ON CONFLICT and `ledger_company_id_name_key` is a plain UNIQUE index,
 * so creating an "Output CGST" the company already has is a raw unique
 * violation.
 *
 * The symptom: on a fresh page load the cache is empty, so keying an ITEM
 * INVOICE (F8/F9, Ctrl+H > Item Invoice) against any company that had already
 * posted one GST voucher failed with
 *   `masters: insert ledger: ERROR: duplicate key value violates unique
 *    constraint "ledger_company_id_name_key"`
 * — i.e. every company after its first day.
 *
 * ⚠️ TWO LESSONS, AND THE SECOND IS THE ONE THAT COST THIS:
 *
 *  1. NOT ONE TEST COULD SEE IT. The mock's createLedger mints a fresh id per
 *     call, so it is MORE PERMISSIVE than the server — HANDOFF §9.4's third
 *     kind, invisible by construction. 726 frontend tests were green.
 *
 *  2. ⛔ FIXING "THE BUG" IS NOT FIXING THE CLASS. The correction at a86283b
 *     fixed the call site that had been REPORTED and left its neighbour — in
 *     the same file, forty lines away, with the same name prefix — untouched.
 *     When a defect is found in an `ensure*` / `upsert*` / `getOrCreate*`
 *     helper, grep the file for the others before closing it.
 *
 * The fix is to go THROUGH ensureLedger rather than to re-implement its
 * resolve-then-create dance here. A second copy of that logic is a second copy
 * to correct, which is exactly how this happened.
 */
export function ensureOutputTaxLedgers(
  client: CoredClient,
  companyID: string,
): Promise<OutputTaxLedgers> {
  const cached = OUTPUT_TAX_CACHE.get(companyID);
  if (cached) return cached;
  const p = (async (): Promise<OutputTaxLedgers> => {
    const [cgstId, sgstId, igstId] = await Promise.all([
      ensureLedger(client, companyID, {
        name: "Output CGST",
        groupName: OUTPUT_TAX_GROUP,
      }),
      ensureLedger(client, companyID, {
        name: "Output SGST",
        groupName: OUTPUT_TAX_GROUP,
      }),
      ensureLedger(client, companyID, {
        name: "Output IGST",
        groupName: OUTPUT_TAX_GROUP,
      }),
    ]);
    return { cgstId, sgstId, igstId };
  })().catch((err) => {
    // Don't poison the cache on failure — allow a retry on the next post.
    OUTPUT_TAX_CACHE.delete(companyID);
    throw err;
  });
  OUTPUT_TAX_CACHE.set(companyID, p);
  return p;
}

/** Inputs needed to assemble a GST sales/purchase request. */
export interface GstVoucherInputs {
  /** Party ledger uuid (debited for the gross total on a sale). */
  partyLedgerId: string;
  /** Income/expense ledger uuid (credited for the taxable base on a sale). */
  incomeLedgerId: string;
  /** The taxable base, fixed-4 string (the income posting amount). */
  taxableValue: string;
  /** Supplier (home) state code — from CompanyInfo.homeStateCode. */
  supplierStateCode: string;
  gst: GstFormState;
  outputTax: OutputTaxLedgers;
  /** Carried-forward number/date on alter (empty/today on create). */
  voucherNo?: string;
  voucherDate?: string;
  narration?: string;
  party?: string;
  /**
   * The bill-wise references this invoice opens against the PARTY leg. Normally
   * one unnamed NEW ref for the gross total — the server stamps it with the
   * voucher number it allocates, because that number does not exist yet.
   *
   * Absent/empty for a party that does not maintain bill-by-bill, which is the
   * majority of ledgers and not an omission.
   */
  billRefs?: readonly DraftBillRef[];
}

/**
 * Build the balanced VoucherRequest for a GST SALE (the F8 form). The party is
 * debited the gross (taxable + tax); the income ledger credited the taxable
 * base; the non-zero tax heads credited per the split. The GSTRequest carries
 * the codes + single rate line + all three output-tax ledger ids so the server
 * re-derives and verifies. dr_cr uses the legible "DR"/"CR" (the REST adapter
 * normalizes to the wire "D"/"C").
 *
 * NOTE: tax amounts here come from {@link deriveGstSplit} (the presentation
 * mirror of the Go engine) ONLY to build verifiable postings; the DISPLAYED tax
 * is read back from VoucherResponse.cgst/sgst/igst, never from this function.
 */
/**
 * THE VOUCHER GROSS — taxable plus derived tax, i.e. the PARTY leg's amount.
 *
 * Exported for the same reason {@link itemInvoiceGross} is: the bill-reference
 * panel and the builder must agree on what the invoice comes to, and the only way
 * to guarantee that is for both to call this.
 */
export function gstVoucherGross(
  taxableValue: string,
  gstRatePct: string,
  supplierStateCode: string,
  placeOfSupplyStateCode: string,
): string {
  const split = deriveGstSplit(
    taxableValue,
    gstRatePct,
    supplierStateCode,
    placeOfSupplyStateCode,
  );
  return split.intra
    ? addMoney(addMoney(taxableValue, split.cgst), split.sgst)
    : addMoney(taxableValue, split.igst);
}

export function buildGstVoucherRequest(
  inputs: GstVoucherInputs,
): VoucherRequest {
  const {
    partyLedgerId,
    incomeLedgerId,
    taxableValue,
    supplierStateCode,
    gst,
    outputTax,
  } = inputs;

  const split = deriveGstSplit(
    taxableValue,
    gst.gstRatePct,
    supplierStateCode,
    gst.placeOfSupply,
  );

  const taxPostings: PostingRequest[] = [];
  if (split.intra) {
    taxPostings.push({ ledger_id: outputTax.cgstId, dr_cr: "CR", amount: split.cgst });
    taxPostings.push({ ledger_id: outputTax.sgstId, dr_cr: "CR", amount: split.sgst });
  } else {
    taxPostings.push({ ledger_id: outputTax.igstId, dr_cr: "CR", amount: split.igst });
  }
  // ⚠️ THROUGH THE EXPORTED HELPER, not summed again here. The bill-reference
  // panel shows this same figure, and one definition of "what the invoice comes
  // to" is the only way the form cannot display one total and post another.
  const total = gstVoucherGross(
    taxableValue,
    gst.gstRatePct,
    supplierStateCode,
    gst.placeOfSupply,
  );

  const postings: PostingRequest[] = [
    // Party Dr = gross total (taxable + tax).
    { ledger_id: partyLedgerId, dr_cr: "DR", amount: total },
    // Income Cr = taxable base.
    { ledger_id: incomeLedgerId, dr_cr: "CR", amount: taxableValue },
    ...taxPostings,
  ];

  const gstReq: GSTRequest = {
    place_of_supply_state_code: gst.placeOfSupply,
    reverse_charge: gst.reverseCharge,
    lines: [
      {
        taxable_value: taxableValue,
        gst_rate_pct: gst.gstRatePct,
        ...(gst.hsn ? { hsn: gst.hsn } : {}),
      },
    ],
    cgst_ledger_id: outputTax.cgstId,
    sgst_ledger_id: outputTax.sgstId,
    igst_ledger_id: outputTax.igstId,
  };
  // OMIT supplier_state_code when the home state is unknown, so the server's
  // company.state_code default fires explicitly (ledger.go fills an empty
  // SupplierStateCode from the company master INSIDE the post transaction). Same
  // omit idiom as draftAccept.ts. Note the server treats an absent field and ""
  // identically, so this is wire hygiene, not a behaviour change on its own —
  // what actually makes the split correct is that homeStateCode is hydrated from
  // company.state_code (CompanyContext), so the postings this function derives
  // and the server's re-derivation agree. If it were empty here while the
  // company DID have a state, the client's IGST postings would fail the server's
  // verify with a 422 rather than silently booking the wrong tax.
  if (supplierStateCode) gstReq.supplier_state_code = supplierStateCode;

  const req: VoucherRequest = {
    voucher_date: inputs.voucherDate,
    narration: inputs.narration,
    party: inputs.party,
    party_ledger_id: partyLedgerId,
    postings,
    gst: gstReq,
  };
  if (inputs.voucherNo) req.voucher_no = inputs.voucherNo;

  // ---- THE BILL THIS INVOICE OPENS.
  //
  // ⛔ ATTACHED HERE, NOT AT THE CALL SITE, because `total` is computed in THIS
  // function and the reference must equal the party posting to the paisa — the
  // server refuses any other total. A caller recomputing "taxable plus tax" would
  // be a second implementation of the same arithmetic, and the drifted copy is
  // always the one that reaches production. Single-sourced for the same reason
  // the postings array above is.
  const billAllocations = buildBillAllocations(
    { ledgerId: partyLedgerId, drCr: "DR", amount: total },
    inputs.billRefs ?? [],
  );
  if (billAllocations.length > 0) req.bill_allocations = billAllocations;
  return req;
}
