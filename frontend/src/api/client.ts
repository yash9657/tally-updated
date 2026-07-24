/**
 * The CoredClient interface — the single contract every consumer (screens,
 * tests, the app shell) depends on. Two implementations satisfy it:
 *
 *  - {@link createRestClient} (./rest) — the live REST/JSON client.
 *  - {@link createMockClient} (./mock) — an in-memory implementation for
 *    hermetic component tests (no live server).
 *
 * Depending on this interface (not a concrete client) is what lets a screen be
 * unit-tested against the mock and shipped against REST without code changes.
 */

import type {
  BalanceSheetResponse,
  CompanyView,
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
import type { QueryRequest, QueryResult } from "./query";

export interface CoredClient {
  // Company
  createCompany(req: CreateCompanyRequest): Promise<CreateCompanyResponse>;
  /**
   * Read the company master back (GET /v1/companies/{companyID}). The shell
   * calls this on load to hydrate the real `state_code` (home GST state) instead
   * of assuming a bootstrap constant — without it, every non-Karnataka company
   * derived the wrong intra/inter-state split in the browser.
   */
  getCompany(companyID: string): Promise<CompanyView>;

  // Masters (all company-scoped)
  createGroup(
    companyID: string,
    req: CreateGroupRequest,
  ): Promise<CreateGroupResponse>;
  createLedger(
    companyID: string,
    req: CreateLedgerRequest,
  ): Promise<CreateLedgerResponse>;
  createVoucherType(
    companyID: string,
    req: CreateVoucherTypeRequest,
  ): Promise<CreateVoucherTypeResponse>;
  createUnit(
    companyID: string,
    req: CreateUnitRequest,
  ): Promise<CreateUnitResponse>;
  createStockItem(
    companyID: string,
    req: CreateStockItemRequest,
  ): Promise<CreateStockItemResponse>;

  // Vouchers (one per accounting class)
  postSalesVoucher(
    companyID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  postPurchaseVoucher(
    companyID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  postReceiptVoucher(
    companyID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  postPaymentVoucher(
    companyID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  postContraVoucher(
    companyID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  postJournalVoucher(
    companyID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;

  // Voucher alteration (PUT /vouchers/{class}/{voucherID}) — a FULL restatement.
  // The class is fixed by the path (one method per class, mirroring the posts);
  // the logical voucher_id is preserved (no new id is minted) and the client
  // resends the loaded voucher_no/voucher_date so numbering/date are unchanged.
  alterSalesVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  alterPurchaseVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  alterReceiptVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  alterPaymentVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  alterContraVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  alterJournalVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;

  // Voucher fetch (GET /vouchers/{voucherID}) — the CURRENT head version of one
  // logical voucher. The source of truth for ALTER hydration: postings carry
  // REAL ledger_id uuids (not Day Book names) plus persisted GST attributes, so
  // a full restatement resends valid uuids and the numbering is preserved.
  getVoucher(companyID: string, voucherID: string): Promise<VoucherView>;

  // Voucher deletion (DELETE /vouchers/{voucherID}) — class-agnostic tombstone.
  deleteVoucher(
    companyID: string,
    voucherID: string,
    createdBy?: string,
  ): Promise<DeleteVoucherResponse>;

  // Reports
  getTrialBalance(companyID: string): Promise<TrialBalanceResponse>;
  getBalanceSheet(companyID: string): Promise<BalanceSheetResponse>;
  getProfitLoss(companyID: string): Promise<ProfitLossResponse>;
  getDayBook(
    companyID: string,
    query?: DayBookQuery,
  ): Promise<DayBookResponse>;
  getOutstandings(
    companyID: string,
    query?: OutstandingsQuery,
  ): Promise<OutstandingsResponse>;

  // GST
  generateGSTR1(companyID: string, query: GSTR1Query): Promise<GSTR1Response>;

  // "Ask your books" conversational query (POST /query). Body carries an NL
  // question (default path) OR a pre-built typed DSL (power users); the server
  // translates+compiles+executes under a READ-ONLY, tenant-scoped transaction
  // and returns the typed answer (columns + string rows + the echoed DSL +
  // sql_preview). Read-only: it NEVER writes. Money stays string.
  query(companyID: string, body: QueryRequest): Promise<QueryResult>;

  // Migration import (Tally-XML ingress)
  importTallyXML(
    companyID: string,
    xml: string,
    query?: ImportTallyXMLQuery,
  ): Promise<ImportTallyXMLResponse>;
}
