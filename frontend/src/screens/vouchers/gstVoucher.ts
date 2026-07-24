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
 * Per-(company, ledger name) memoized create-or-select. createLedger is
 * idempotent server-side (by name within a company), so re-posting reuses the
 * same id; we memoize the PROMISE per (company,name) so the form resolves a
 * stable ledger uuid for a name without firing duplicate creates (and so the
 * mock, which mints a fresh id per call, stays single-valued per name too).
 */
const LEDGER_CACHE = new Map<string, Promise<string>>();

/** Test-only: clear the per-(company,name) ledger memo. */
export function _resetLedgerCache(): void {
  LEDGER_CACHE.clear();
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
  const p = client
    .createLedger(companyID, {
      name: spec.name,
      group_name: spec.groupName,
      ...(spec.stateCode ? { state_code: spec.stateCode } : {}),
    })
    .then((r) => r.ledger_id)
    .catch((err) => {
      LEDGER_CACHE.delete(key);
      throw err;
    });
  LEDGER_CACHE.set(key, p);
  return p;
}

/**
 * Ensure (create-or-select) the three output-tax ledgers for a company and
 * cache their ids. Idempotent and memoized: the first GST post on a fresh
 * company triggers three createLedger calls; subsequent posts reuse the cached
 * ids (no new calls).
 */
export function ensureOutputTaxLedgers(
  client: CoredClient,
  companyID: string,
): Promise<OutputTaxLedgers> {
  const cached = OUTPUT_TAX_CACHE.get(companyID);
  if (cached) return cached;
  const p = (async (): Promise<OutputTaxLedgers> => {
    const [cgst, sgst, igst] = await Promise.all([
      client.createLedger(companyID, {
        name: "Output CGST",
        group_name: OUTPUT_TAX_GROUP,
      }),
      client.createLedger(companyID, {
        name: "Output SGST",
        group_name: OUTPUT_TAX_GROUP,
      }),
      client.createLedger(companyID, {
        name: "Output IGST",
        group_name: OUTPUT_TAX_GROUP,
      }),
    ]);
    return {
      cgstId: cgst.ledger_id,
      sgstId: sgst.ledger_id,
      igstId: igst.ledger_id,
    };
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
  let total = taxableValue;
  if (split.intra) {
    taxPostings.push({ ledger_id: outputTax.cgstId, dr_cr: "CR", amount: split.cgst });
    taxPostings.push({ ledger_id: outputTax.sgstId, dr_cr: "CR", amount: split.sgst });
    total = addMoney(total, split.cgst);
    total = addMoney(total, split.sgst);
  } else {
    taxPostings.push({ ledger_id: outputTax.igstId, dr_cr: "CR", amount: split.igst });
    total = addMoney(total, split.igst);
  }

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
  return req;
}
