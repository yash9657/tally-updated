/**
 * The live REST/JSON implementation of {@link CoredClient}. Each method maps
 * 1:1 to a route in backend/internal/server/server.go. Company-scoped routes
 * embed the (encoded) company id in the path; reports and GSTR-1 pass their
 * filters as query params. Money stays string end to end.
 */

import type { CoredClient } from "./client";
import { createHttpClient, type HttpClient, type HttpClientOptions } from "./http";
import type {
  BalanceSheetResponse,
  CreateCompanyRequest,
  CreateCompanyResponse,
  CreateGroupRequest,
  CreateGroupResponse,
  CreateLedgerRequest,
  CreateLedgerResponse,
  CreateStockItemRequest,
  CreateStockItemResponse,
  CreateUnitRequest,
  CreateUnitResponse,
  CreateVoucherTypeRequest,
  CreateVoucherTypeResponse,
  DayBookQuery,
  DeleteVoucherResponse,
  DayBookResponse,
  GSTR1Query,
  GSTR1Response,
  ImportTallyXMLQuery,
  ImportTallyXMLResponse,
  OutstandingsQuery,
  OutstandingsResponse,
  ProfitLossResponse,
  TrialBalanceResponse,
  VoucherRequest,
  VoucherResponse,
  VoucherView,
} from "./types";

function company(companyID: string): string {
  return `/v1/companies/${encodeURIComponent(companyID)}`;
}

/**
 * The backend ledger accepts dr_cr as the canonical single-letter wire values
 * "D"/"C" only, while the frontend domain model uses the readable "DR"/"CR".
 * This adapter is the single boundary that translates, so screens and scripts
 * keep the legible internal convention and the wire stays contract-exact.
 */
function wireDrCr(v: string): "D" | "C" {
  return v.trim().toUpperCase().startsWith("D") ? "D" : "C";
}

/** Normalize a voucher body's dr_cr fields to the backend wire contract. */
function wireVoucher(req: VoucherRequest): VoucherRequest {
  if (!req.postings && !req.bill_allocations) return req;
  return {
    ...req,
    postings: req.postings?.map((p) => ({ ...p, dr_cr: wireDrCr(p.dr_cr) })),
    bill_allocations: req.bill_allocations?.map((b) => ({
      ...b,
      dr_cr: wireDrCr(b.dr_cr),
    })),
  };
}

/** Build a REST CoredClient over an HttpClient (constructed from opts if absent). */
export function createRestClient(
  opts: HttpClientOptions | { http: HttpClient } = {},
): CoredClient {
  const http: HttpClient = "http" in opts ? opts.http : createHttpClient(opts);

  return {
    createCompany(req: CreateCompanyRequest) {
      return http.post<CreateCompanyResponse>("/v1/companies", req);
    },

    createGroup(companyID, req: CreateGroupRequest) {
      return http.post<CreateGroupResponse>(`${company(companyID)}/groups`, req);
    },
    createLedger(companyID, req: CreateLedgerRequest) {
      return http.post<CreateLedgerResponse>(
        `${company(companyID)}/ledgers`,
        req,
      );
    },
    createVoucherType(companyID, req: CreateVoucherTypeRequest) {
      return http.post<CreateVoucherTypeResponse>(
        `${company(companyID)}/voucher-types`,
        req,
      );
    },
    createUnit(companyID, req: CreateUnitRequest) {
      return http.post<CreateUnitResponse>(`${company(companyID)}/units`, req);
    },
    createStockItem(companyID, req: CreateStockItemRequest) {
      return http.post<CreateStockItemResponse>(
        `${company(companyID)}/stock-items`,
        req,
      );
    },

    postSalesVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/sales`,
        wireVoucher(req),
      );
    },
    postPurchaseVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/purchase`,
        wireVoucher(req),
      );
    },
    postReceiptVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/receipt`,
        wireVoucher(req),
      );
    },
    postPaymentVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/payment`,
        wireVoucher(req),
      );
    },
    postContraVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/contra`,
        wireVoucher(req),
      );
    },
    postJournalVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/journal`,
        wireVoucher(req),
      );
    },

    alterSalesVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/sales/${encodeURIComponent(voucherID)}`,
        wireVoucher(req),
      );
    },
    alterPurchaseVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/purchase/${encodeURIComponent(voucherID)}`,
        wireVoucher(req),
      );
    },
    alterReceiptVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/receipt/${encodeURIComponent(voucherID)}`,
        wireVoucher(req),
      );
    },
    alterPaymentVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/payment/${encodeURIComponent(voucherID)}`,
        wireVoucher(req),
      );
    },
    alterContraVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/contra/${encodeURIComponent(voucherID)}`,
        wireVoucher(req),
      );
    },
    alterJournalVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/journal/${encodeURIComponent(voucherID)}`,
        wireVoucher(req),
      );
    },

    getVoucher(companyID, voucherID) {
      // Incoming GET postings/bill-allocations are already the canonical "D"/"C"
      // wire form; pass them through unchanged (no wireDrCr translation).
      return http.get<VoucherView>(
        `${company(companyID)}/vouchers/${encodeURIComponent(voucherID)}`,
      );
    },

    deleteVoucher(companyID, voucherID, createdBy?: string) {
      return http.del<DeleteVoucherResponse>(
        `${company(companyID)}/vouchers/${encodeURIComponent(voucherID)}`,
        createdBy ? { created_by: createdBy } : undefined,
      );
    },

    getTrialBalance(companyID) {
      return http.get<TrialBalanceResponse>(`${company(companyID)}/trial-balance`);
    },
    getBalanceSheet(companyID) {
      return http.get<BalanceSheetResponse>(`${company(companyID)}/balance-sheet`);
    },
    getProfitLoss(companyID) {
      return http.get<ProfitLossResponse>(
        `${company(companyID)}/profit-and-loss`,
      );
    },
    getDayBook(companyID, query?: DayBookQuery) {
      return http.get<DayBookResponse>(`${company(companyID)}/day-book`, query);
    },
    getOutstandings(companyID, query?: OutstandingsQuery) {
      return http.get<OutstandingsResponse>(
        `${company(companyID)}/outstandings`,
        query,
      );
    },

    generateGSTR1(companyID, query: GSTR1Query) {
      return http.get<GSTR1Response>(`${company(companyID)}/gstr1`, query);
    },

    importTallyXML(companyID, xml, query?: ImportTallyXMLQuery) {
      return http.postRaw<ImportTallyXMLResponse>(
        `${company(companyID)}/import/tally-xml`,
        xml,
        "application/xml",
        query,
      );
    },
  };
}
