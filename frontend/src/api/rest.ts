/**
 * The live REST/JSON implementation of {@link CoredClient}. Each method maps
 * 1:1 to a route in backend/internal/server/server.go. Company-scoped routes
 * embed the (encoded) company id in the path; reports and GSTR-1 pass their
 * filters as query params. Money stays string end to end.
 */

import type { CoredClient } from "./client";
import {
  createHttpClient,
  type HttpClient,
  type HttpClientOptions,
} from "./http";
import type {
  BalanceSheetResponse,
  CompanyFeaturesRequest,
  CompanyFeaturesResponse,
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
  ListGodownsResponse,
  ListStockBatchesResponse,
  ListStockItemsResponse,
  OutstandingsQuery,
  OutstandingsResponse,
  PartyOpenBillsResponse,
  ProfitLossResponse,
  StockSummaryResponse,
  TrialBalanceResponse,
  VoucherRequest,
  VoucherResponse,
  VoucherView,
  ListLedgersResponse,
  SetLedgerOpeningRequest,
  SetLedgerOpeningResponse,
  VoucherHistoryResponse,
  BankReconciliationResponse,
  ImportedStatementResponse,
  ImportStatementRequest,
  ImportStatementResponse,
  PreviewStatementRequest,
  PreviewStatementResponse,
  DeductorStatusListResponse,
  TDSRegisterResponse,
  TCSDeclarationListResponse,
  CreateTCSDeclarationRequest,
  CreateTCSDeclarationResponse,
  SetDeductorStatusRequest,
  SetDeductorStatusResponse,
  CostCategory,
  CostCategoryListResponse,
  CostCategorySummaryResponse,
  CreateOrderRequest,
  CloseOrderRequest,
  OrderListResponse,
  OrderView,
  CreateBomRequest,
  BomListResponse,
  BomView,
  BomExplosionResponse,
  EWayBillRequest,
  EWayBillResponse,
  EInvoiceRaiseRequest,
  EInvoiceRaised,
  EInvoicePinned,
  OrderBookResponse,
  AnomaliesResponse,
  CashFlowResponse,
  CostCentreListResponse,
  CostCentreWriteResponse,
  CostCentreSummaryResponse,
  CreateCostCategoryRequest,
  CreateCostCentreRequest,
  TDSChallanListResponse,
  CreateTDSChallanRequest,
  CreateTDSChallanResponse,
  AllocateToChallanRequest,
  AllocateToChallanResponse,
  TaxSectionsResponse,
  TDSProfileResponse,
  SetTDSProfileRequest,
  SetTDSProfileResponse,
  SetBankDateRequest,
  SetBankDateResponse,
} from "./types";
import type { QueryRequest, QueryResult } from "./query";
import type {
  CreateSecurityLevelResponse,
  DeleteSecurityLevelResponse,
  InviteMemberRequest,
  InviteMemberResponse,
  MeResponse,
  MembersResponse,
  RemoveMemberResponse,
  RevokeInviteResponse,
  SecurityLevelRequest,
  SecurityLevelsResponse,
  SecurityVocabulary,
  UpdateMemberRequest,
  UpdateMemberResponse,
  UpdateSecurityLevelResponse,
} from "./admin";

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
    getCompany(companyID: string) {
      return http.get<CompanyView>(company(companyID));
    },
    setCompanyFeatures(companyID: string, req: CompanyFeaturesRequest) {
      // PUT on the company ROOT — the same path getCompany reads, which is why
      // routeAction maps both to the `company` resource token: one resource,
      // one token, so no ACL rule can govern the read and miss the write.
      return http.put<CompanyFeaturesResponse>(company(companyID), req);
    },

    createGroup(companyID, req: CreateGroupRequest) {
      return http.post<CreateGroupResponse>(
        `${company(companyID)}/groups`,
        req,
      );
    },
    listLedgers(companyID) {
      return http.get<ListLedgersResponse>(`${company(companyID)}/ledgers`);
    },
    setLedgerOpening(companyID, ledgerID, req: SetLedgerOpeningRequest) {
      return http.put<SetLedgerOpeningResponse>(
        `${company(companyID)}/ledgers/${ledgerID}/opening`,
        req,
      );
    },

    // ---- Printing (T1). Both answer application/pdf, so they go through
    // getBlob rather than the JSON path, which would corrupt the document into a
    // lossy string. Errors on these routes ARE JSON and are surfaced verbatim.
    printVoucher(companyID, voucherClass, voucherID) {
      return http.getBlob(
        `${company(companyID)}/print/vouchers/${voucherClass}/${voucherID}`,
      );
    },
    printReport(companyID, slug, query) {
      return http.getBlob(`${company(companyID)}/print/reports/${slug}`, query);
    },

    // ---- Bank reconciliation (T4).
    bankReconciliation(companyID, ledgerID, asOn) {
      return http.get<BankReconciliationResponse>(
        `${company(companyID)}/bank-reconciliation/${ledgerID}`,
        // Omitted rather than sent empty: buildQuery drops "" anyway, and the
        // server reads a missing as_on as "today".
        asOn ? { as_on: asOn } : undefined,
      );
    },
    setBankDate(
      companyID,
      ledgerID,
      voucherID,
      legNo,
      req: SetBankDateRequest,
    ) {
      return http.put<SetBankDateResponse>(
        `${company(companyID)}/bank-reconciliation/${ledgerID}/${voucherID}/${legNo}`,
        req,
      );
    },
    clearBankDate(companyID, ledgerID, voucherID, legNo) {
      return http.del<SetBankDateResponse>(
        `${company(companyID)}/bank-reconciliation/${ledgerID}/${voucherID}/${legNo}`,
      );
    },
    // ---- Bank statement import (T4 slice 2a).
    previewBankStatement(companyID, ledgerID, req: PreviewStatementRequest) {
      return http.post<PreviewStatementResponse>(
        `${company(companyID)}/bank-statement/${ledgerID}/preview`,
        req,
      );
    },
    importBankStatement(companyID, ledgerID, req: ImportStatementRequest) {
      return http.post<ImportStatementResponse>(
        `${company(companyID)}/bank-statement/${ledgerID}/import`,
        req,
      );
    },
    importedStatement(companyID, ledgerID, from, to) {
      return http.get<ImportedStatementResponse>(
        `${company(companyID)}/bank-statement/${ledgerID}`,
        // Omitted rather than sent empty; the server reads a missing period as
        // "everything imported".
        from || to ? { from: from ?? "", to: to ?? "" } : undefined,
      );
    },
    // ---- TDS deductor status (Item Zero step 2).
    listDeductorStatus(companyID) {
      return http.get<DeductorStatusListResponse>(
        `${company(companyID)}/deductor-status`,
      );
    },
    setDeductorStatus(
      companyID,
      taxYearStart: number,
      req: SetDeductorStatusRequest,
    ) {
      // The year is in the PATH because it is the row's identity. A body that
      // could disagree with the URL is a question about which one wins.
      return http.put<SetDeductorStatusResponse>(
        `${company(companyID)}/deductor-status/${taxYearStart}`,
        req,
      );
    },

    // ---- The TDS/TCS register (Item Zero step 4).
    tdsRegister(companyID, from: string, to: string) {
      return http.get<TDSRegisterResponse>(
        `${company(companyID)}/tds-register`,
        { from, to },
      );
    },

    // ---- TDS challans (T6, migration 0069).
    listTDSChallans(companyID, taxYearStart: number) {
      // The year is a QUERY PARAM and it is always sent. The server has no
      // default for it and refuses the request without one — see the interface.
      //
      // NOTHING IS MAPPED HERE, and that includes `allocatable_deductions` and
      // `allocatable_error`: the response is handed over as it arrived. The
      // allocation identifiers (a voucher UUID and a 0-based leg) and the amount
      // must reach the POST byte-identical, because the write path compares the
      // amount for EQUALITY with the deduction's tax — so a client-side
      // normalisation of any of the three would be a bug, not a tidy-up.
      return http.get<TDSChallanListResponse>(
        `${company(companyID)}/tds-challans`,
        { tax_year_start: taxYearStart },
      );
    },
    createTDSChallan(companyID, req: CreateTDSChallanRequest) {
      // POST and only POST: there is no PUT, PATCH or DELETE on this resource.
      // The CIN is the identity and re-posting it is a 409, never an upsert.
      return http.post<CreateTDSChallanResponse>(
        `${company(companyID)}/tds-challans`,
        req,
      );
    },
    allocateToTDSChallan(
      companyID,
      challanID: string,
      req: AllocateToChallanRequest,
    ) {
      // POST to a SUB-RESOURCE of the challan, not a PUT on the challan: what is
      // created is a link, and the challan existed before the request. That is
      // why the server answers 200 and not 201.
      //
      // NOTHING IS MAPPED, for the reason listTDSChallans states: the three
      // identifier/amount fields must reach the server byte-identical to the way
      // `allocatable_deductions` served them, because the amount is compared for
      // EQUALITY with the deduction's tax.
      //
      // The challan id goes in the PATH, not the body. It is not the operator's
      // to type — it comes from the register they are looking at.
      return http.post<AllocateToChallanResponse>(
        `${company(companyID)}/tds-challans/${encodeURIComponent(challanID)}/allocations`,
        req,
      );
    },

    taxSections(companyID) {
      // No parameters at all: the table is the Finance Act's, not the tenant's,
      // and two companies on one deployment get the same answer. A company id in
      // the path only because every route on this seam is company-scoped by the
      // middleware that authorises it.
      return http.get<TaxSectionsResponse>(`${company(companyID)}/tds-sections`);
    },

    // ---- The TDS deductor return profile (T6, migration 0070).
    getTDSProfile(companyID) {
      return http.get<TDSProfileResponse>(`${company(companyID)}/tds-profile`);
    },
    setTDSProfile(companyID, req: SetTDSProfileRequest) {
      // No key in the path — the company IS the key (company_tds_profile's whole
      // primary key), and it is already in the prefix. PUT with the WHOLE row,
      // because 0070's cross-field CHECKs are not checkable on a partial body.
      return http.put<SetTDSProfileResponse>(
        `${company(companyID)}/tds-profile`,
        req,
      );
    },

    // ---- T2: cost centres and cost categories.
    listCostCategories(companyID) {
      return http.get<CostCategoryListResponse>(
        `${company(companyID)}/cost-categories`,
      );
    },
    createCostCategory(companyID, req: CreateCostCategoryRequest) {
      return http.post<CostCategory>(
        `${company(companyID)}/cost-categories`,
        req,
      );
    },
    renameCostCategory(
      companyID,
      categoryID: string,
      req: CreateCostCategoryRequest,
    ) {
      return http.put<CostCategory>(
        `${company(companyID)}/cost-categories/${encodeURIComponent(categoryID)}`,
        req,
      );
    },
    listCostCentres(companyID, costCategoryID?: string) {
      // The axis is OMITTED rather than sent empty when absent. The server reads a
      // present-but-blank parameter as "every axis", which is the same answer, but
      // an all-zeros or empty id is a 400 — so not sending the key at all is the
      // only unambiguous way to ask for every axis.
      return http.get<CostCentreListResponse>(
        `${company(companyID)}/cost-centres`,
        costCategoryID ? { cost_category_id: costCategoryID } : undefined,
      );
    },
    createCostCentre(companyID, req: CreateCostCentreRequest) {
      // The server nests the view under `cost_centre` and may return it null when the
      // read-back fails. Handed over AS THE ENVELOPE rather than unwrapped here,
      // because unwrapping would have to invent something for the null case and the
      // caller is the only one who can decide what to say about it.
      return http.post<CostCentreWriteResponse>(
        `${company(companyID)}/cost-centres`,
        req,
      );
    },
    alterCostCentre(companyID, centreID: string, req: CreateCostCentreRequest) {
      return http.put<CostCentreWriteResponse>(
        `${company(companyID)}/cost-centres/${encodeURIComponent(centreID)}`,
        req,
      );
    },

    costCentreSummary(
      companyID,
      costCategoryID: string,
      from: string,
      to: string,
    ) {
      // THE AXIS IS ALWAYS SENT. The server has no default and refuses without one,
      // deliberately: defaulting to "the first axis" would render a real, correct
      // Department report under a heading the operator chose for Branches.
      //
      // A FLAT LITERAL PATH with the axis as a QUERY PARAM, not /cost-categories/
      // {id}/summary — a report's route must spell its own name so routeAction can
      // map it, exactly as stock-item-vouchers takes stock_item_id this way.
      return http.get<CostCentreSummaryResponse>(
        `${company(companyID)}/cost-centre-summary`,
        { cost_category_id: costCategoryID, from, to },
      );
    },
    costCategorySummary(companyID, from: string, to: string) {
      // No axis: this report IS the list of axes.
      return http.get<CostCategorySummaryResponse>(
        `${company(companyID)}/cost-category-summary`,
        { from, to },
      );
    },

    // ---- T3: orders, the bill of materials, and the order books.
    //
    // ⛔ TWO METHODS PER OPERATION AND NOT ONE WITH A DIRECTION ARGUMENT. The
    // direction lives in the PATH because routeAction reads the path to decide
    // whether this is `sales-order` or `purchase-order` authority; a body field or a
    // query parameter would let a caller holding one reach the other.
    createSalesOrder(companyID, req: CreateOrderRequest) {
      return http.post<{ order_id: string }>(
        `${company(companyID)}/sales-orders`,
        req,
      );
    },
    createPurchaseOrder(companyID, req: CreateOrderRequest) {
      return http.post<{ order_id: string }>(
        `${company(companyID)}/purchase-orders`,
        req,
      );
    },
    listSalesOrders(companyID, openOnly?: boolean) {
      // The key is OMITTED rather than sent as "false", so the server's own default
      // is what answers. Sending open_only=false works identically today and would
      // pin a default this client does not own.
      return http.get<OrderListResponse>(
        `${company(companyID)}/sales-orders`,
        openOnly ? { open_only: "true" } : undefined,
      );
    },
    listPurchaseOrders(companyID, openOnly?: boolean) {
      return http.get<OrderListResponse>(
        `${company(companyID)}/purchase-orders`,
        openOnly ? { open_only: "true" } : undefined,
      );
    },
    getSalesOrder(companyID, orderID: string) {
      return http.get<OrderView>(
        `${company(companyID)}/sales-orders/${encodeURIComponent(orderID)}`,
      );
    },
    getPurchaseOrder(companyID, orderID: string) {
      return http.get<OrderView>(
        `${company(companyID)}/purchase-orders/${encodeURIComponent(orderID)}`,
      );
    },
    closeSalesOrder(companyID, orderID: string, req: CloseOrderRequest) {
      return http.put<{ order_id: string; is_closed: boolean }>(
        `${company(companyID)}/sales-orders/${encodeURIComponent(orderID)}/close`,
        req,
      );
    },
    closePurchaseOrder(companyID, orderID: string, req: CloseOrderRequest) {
      return http.put<{ order_id: string; is_closed: boolean }>(
        `${company(companyID)}/purchase-orders/${encodeURIComponent(orderID)}/close`,
        req,
      );
    },

    createBom(companyID, req: CreateBomRequest) {
      return http.post<{ bom_id: string }>(`${company(companyID)}/boms`, req);
    },
    listBoms(companyID, stockItemID?: string) {
      // OMITTED rather than sent empty when absent, for the reason listCostCentres
      // records: the server refuses an all-zeros or malformed id with a 400, so not
      // sending the key at all is the only unambiguous way to ask for every recipe.
      return http.get<BomListResponse>(
        `${company(companyID)}/boms`,
        stockItemID ? { stock_item_id: stockItemID } : undefined,
      );
    },
    getBom(companyID, bomID: string) {
      return http.get<BomView>(
        `${company(companyID)}/boms/${encodeURIComponent(bomID)}`,
      );
    },
    raiseEWayBill(companyID, req: EWayBillRequest) {
      // ⛔ FLAT, not /vouchers/{id}/e-way-bill. cored's routeAction reads
      // everything after "/vouchers/" as the voucher CLASS, so the nested path
      // would map to a class nothing knows and default-deny the Owner. The
      // voucher is named in the BODY. See handler_ewaybill.go's header.
      return http.post<EWayBillResponse>(
        `${company(companyID)}/e-way-bills`,
        req,
      );
    },
    getEWayBill(companyID, voucherID: string) {
      return http.get<EWayBillResponse>(
        `${company(companyID)}/e-way-bills/${encodeURIComponent(voucherID)}`,
      );
    },
    raiseEInvoice(companyID, req: EInvoiceRaiseRequest) {
      // ⛔ FLAT, not /vouchers/{id}/e-invoice — the same trap the e-way bill
      // route documents, and it bites identically. cored's routeAction reads
      // everything after "/vouchers/" as the voucher CLASS, so the nested path
      // maps to a request to create a voucher of class "{id}/e-invoice" — a
      // class no vocabulary knows, i.e. default-deny for the OWNER too,
      // presenting as an unexplained 403. The voucher is named in the BODY.
      // See handler_einvoice.go's header.
      //
      // ⚠️ statutoryd's own endpoint IS nested (POST
      // /companies/{c}/vouchers/{v}/e-invoice) and that is fine: it has no
      // routeAction, no PDP and no voucher classes. The two paths differ on
      // purpose, and this is cored's.
      return http.post<EInvoiceRaised>(
        `${company(companyID)}/e-invoices`,
        req,
      );
    },
    getEInvoice(companyID, voucherID: string) {
      return http.get<EInvoicePinned>(
        `${company(companyID)}/e-invoices/${encodeURIComponent(voucherID)}`,
      );
    },
    explodeBom(companyID, bomID: string, qty: string) {
      // A GET, because it writes nothing. qty is ALWAYS sent: the server has no
      // default and refuses without one, deliberately — guessing 1 would answer a
      // question about a different batch size.
      return http.get<BomExplosionResponse>(
        `${company(companyID)}/boms/${encodeURIComponent(bomID)}/explode`,
        { qty },
      );
    },

    salesOrderBook(companyID, asOn?: string) {
      // A FLAT LITERAL PATH spelling the report's own name, with the date as a query
      // param — the rule every report route follows so routeAction can map it. And
      // TWO paths rather than one with ?direction=, because the PDP decides on the
      // report NAME and a query parameter is invisible to it.
      return http.get<OrderBookResponse>(
        `${company(companyID)}/sales-order-book`,
        asOn ? { as_on: asOn } : undefined,
      );
    },
    purchaseOrderBook(companyID, asOn?: string) {
      return http.get<OrderBookResponse>(
        `${company(companyID)}/purchase-order-book`,
        asOn ? { as_on: asOn } : undefined,
      );
    },

    cashFlow(companyID, asOn?: string) {
      // A FLAT LITERAL PATH, like every report route, so routeAction can map it to
      // `report:cash-flow` — the PDP decides on the NAME and cannot see a query
      // parameter. `horizon_days` and `bucket` are deliberately NOT sent: they are
      // the server's defaults (90 days, weekly) and F2 gives the operator a date
      // and nothing else, so a horizon nobody can set would be an argument with no
      // keyboard behind it.
      return http.get<CashFlowResponse>(
        `${company(companyID)}/cash-flow`,
        asOn ? { as_on: asOn } : undefined,
      );
    },
    anomalies(companyID, from?: string, to?: string) {
      // BOTH bounds are optional and BOTH are omitted when absent, rather than
      // sent empty: the server reads "" as unbounded, but a `?from=&to=` on the
      // wire is indistinguishable from a screen that meant to send a window and
      // computed one wrong.
      const q: Record<string, string> = {};
      if (from) q.from = from;
      if (to) q.to = to;
      return http.get<AnomaliesResponse>(
        `${company(companyID)}/anomalies`,
        Object.keys(q).length > 0 ? q : undefined,
      );
    },


    // ---- s.394(2) declarations (Item Zero step 3).
    listTCSDeclarations(companyID) {
      return http.get<TCSDeclarationListResponse>(
        `${company(companyID)}/tcs-declarations`,
      );
    },
    createTCSDeclaration(companyID, req: CreateTCSDeclarationRequest) {
      return http.post<CreateTCSDeclarationResponse>(
        `${company(companyID)}/tcs-declarations`,
        req,
      );
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
    listStockItems(companyID) {
      return http.get<ListStockItemsResponse>(
        `${company(companyID)}/stock-items`,
      );
    },
    listGodowns(companyID) {
      return http.get<ListGodownsResponse>(`${company(companyID)}/godowns`);
    },
    listStockBatches(companyID) {
      return http.get<ListStockBatchesResponse>(
        `${company(companyID)}/batches`,
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
    postCreditNoteVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/credit-note`,
        wireVoucher(req),
      );
    },
    postDebitNoteVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/debit-note`,
        wireVoucher(req),
      );
    },

    // The non-accounting stock vouchers. wireVoucher is still applied for
    // uniformity; it is a no-op here, because a body with no postings and no
    // bill allocations has no dr_cr to translate — and one of these carrying
    // either is refused by the server, not silently normalized.
    postDeliveryNoteVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/delivery-note`,
        wireVoucher(req),
      );
    },
    postReceiptNoteVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/receipt-note`,
        wireVoucher(req),
      );
    },
    postStockJournalVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/stock-journal`,
        wireVoucher(req),
      );
    },
    postPhysicalStockVoucher(companyID, req: VoucherRequest) {
      return http.post<VoucherResponse>(
        `${company(companyID)}/vouchers/physical-stock`,
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
    alterCreditNoteVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/credit-note/${encodeURIComponent(voucherID)}`,
        wireVoucher(req),
      );
    },
    alterDebitNoteVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/debit-note/${encodeURIComponent(voucherID)}`,
        wireVoucher(req),
      );
    },
    alterDeliveryNoteVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/delivery-note/${encodeURIComponent(voucherID)}`,
        wireVoucher(req),
      );
    },
    alterReceiptNoteVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/receipt-note/${encodeURIComponent(voucherID)}`,
        wireVoucher(req),
      );
    },
    alterStockJournalVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/stock-journal/${encodeURIComponent(voucherID)}`,
        wireVoucher(req),
      );
    },
    alterPhysicalStockVoucher(companyID, voucherID, req: VoucherRequest) {
      return http.put<VoucherResponse>(
        `${company(companyID)}/vouchers/physical-stock/${encodeURIComponent(voucherID)}`,
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

    // An EMPTY bound is omitted rather than sent as "": the server 400s an
    // unparseable date on purpose (a report that quietly ignored the period
    // would answer a different question than the one on screen), so sending an
    // empty string would turn "no period" into a bad request.
    getTrialBalance(companyID, asOn?: string) {
      return http.get<TrialBalanceResponse>(
        `${company(companyID)}/trial-balance`,
        asOn ? { as_on: asOn } : undefined,
      );
    },
    getBalanceSheet(companyID, asOn?: string) {
      return http.get<BalanceSheetResponse>(
        `${company(companyID)}/balance-sheet`,
        asOn ? { as_on: asOn } : undefined,
      );
    },
    getProfitLoss(companyID, from?: string, to?: string) {
      const q: Record<string, string> = {};
      if (from) q.from = from;
      if (to) q.to = to;
      return http.get<ProfitLossResponse>(
        `${company(companyID)}/profit-and-loss`,
        Object.keys(q).length > 0 ? q : undefined,
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
    getBillsPayable(companyID, query?: { as_on?: string }) {
      // ⛔ THE FLAT PATH IS THE POINT (D94) — see the interface. No `kind` is sent
      // and none would be honoured: this route IS the payables book.
      return http.get<OutstandingsResponse>(
        `${company(companyID)}/bills-payable`,
        query,
      );
    },
    getPartyOpenBills(companyID, voucherClass, ledgerID, kind) {
      // ⛔ THE CLASS AND THE LEDGER ARE BOTH IN THE PATH, AND NEITHER IS STYLE.
      // The class is what cored's PDP decides on (it reads the path, never the
      // query), and the ledger is in the path so this route has NO unscoped form
      // — it can never answer with the whole ageing book, whatever a caller
      // omits. The kind rides the query because the authority does not depend on
      // it. See the interface for the row this shape closes.
      return http.get<PartyOpenBillsResponse>(
        `${company(companyID)}/vouchers/${voucherClass}/open-bills/${encodeURIComponent(ledgerID)}`,
        { kind },
      );
    },
    getStockSummary(companyID, asOn?: string) {
      // `as_on` is omitted entirely when absent (not sent as ""), so the server
      // replays the WHOLE movement ledger — a position has no lower bound.
      return http.get<StockSummaryResponse>(
        `${company(companyID)}/stock-summary`,
        asOn ? { as_on: asOn } : undefined,
      );
    },

    getVoucherHistory(companyID, voucherID) {
      return http.get<VoucherHistoryResponse>(
        `${company(companyID)}/vouchers/${encodeURIComponent(voucherID)}/history`,
      );
    },

    generateGSTR1(companyID, query: GSTR1Query) {
      return http.get<GSTR1Response>(`${company(companyID)}/gstr1`, query);
    },

    query(companyID, body: QueryRequest) {
      return http.post<QueryResult>(`${company(companyID)}/query`, body);
    },

    // ---- SaaS admin surface -------------------------------------------
    // /v1/me is NOT company-scoped: an invited user has no company yet.
    getMe() {
      return http.get<MeResponse>("/v1/me");
    },
    listMembers(companyID) {
      return http.get<MembersResponse>(`${company(companyID)}/members`);
    },
    inviteMember(companyID, req: InviteMemberRequest) {
      return http.post<InviteMemberResponse>(
        `${company(companyID)}/members/invites`,
        req,
      );
    },
    revokeInvite(companyID, inviteID) {
      return http.del<RevokeInviteResponse>(
        `${company(companyID)}/members/invites/${encodeURIComponent(inviteID)}`,
      );
    },
    updateMember(companyID, userID, req: UpdateMemberRequest) {
      return http.put<UpdateMemberResponse>(
        `${company(companyID)}/members/${encodeURIComponent(userID)}`,
        req,
      );
    },
    removeMember(companyID, userID) {
      return http.del<RemoveMemberResponse>(
        `${company(companyID)}/members/${encodeURIComponent(userID)}`,
      );
    },

    listSecurityLevels(companyID) {
      return http.get<SecurityLevelsResponse>(
        `${company(companyID)}/security-levels`,
      );
    },
    getSecurityVocabulary(companyID) {
      return http.get<SecurityVocabulary>(
        `${company(companyID)}/security-levels/vocabulary`,
      );
    },
    createSecurityLevel(companyID, req: SecurityLevelRequest) {
      return http.post<CreateSecurityLevelResponse>(
        `${company(companyID)}/security-levels`,
        req,
      );
    },
    updateSecurityLevel(companyID, levelID, req: SecurityLevelRequest) {
      return http.put<UpdateSecurityLevelResponse>(
        `${company(companyID)}/security-levels/${encodeURIComponent(levelID)}`,
        req,
      );
    },
    deleteSecurityLevel(companyID, levelID) {
      return http.del<DeleteSecurityLevelResponse>(
        `${company(companyID)}/security-levels/${encodeURIComponent(levelID)}`,
      );
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
