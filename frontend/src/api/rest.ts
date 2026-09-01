/**
 * The live REST/JSON implementation of {@link CoredClient}. Each method maps
 * 1:1 to a route in backend/internal/server/server.go. Company-scoped routes
 * embed the (encoded) company id in the path; reports and GSTR-1 pass their
 * filters as query params. Money stays string end to end.
 */

import type { CoredClient, RestoreResult } from "./client";
import {
  createHttpClient,
  type HttpClient,
  type HttpClientOptions,
} from "./http";
import type {
  BooksClosure,
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
  PrintTemplateRequest,
  PrintTemplateResponse,
  ListGodownsResponse,
  ListStockBatchesResponse,
  CreateStockGroupRequest,
  CreateCurrencyRequest,
  CreateCurrencyResponse,
  ListCurrenciesResponse,
  SetCurrencyRateRequest,
  CreateStockGroupResponse,
  InventoryVocabularyResponse,
  ListStockGroupsResponse,
  ListUnitsResponse,
  SetValuationMethodRequest,
  SetValuationMethodResponse,
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
  ListGroupsResponse,
  ListLedgersResponse,
  SetLedgerOpeningRequest,
  SetLedgerOpeningResponse,
  VoucherHistoryResponse,
  BankMatchesResponse,
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
  SetAsideRequest,
  SetAsideResponse,
  SetBankDateRequest,
  SetBankDateResponse,
  ListEmployeesResponse,
  CreateEmployeeRequest,
  CreateEmployeeResponse,
  PayRunRequest,
  PayRunResponse,
  PayrollRegisterResponse,
  PayslipResponse,
  Form138Request,
  StatutoryFileDownload,
  ListSalaryComponentsResponse,
  SetSalaryComponentRequest,
  SetSalaryComponentResponse,
  ListStatutoryElectionsResponse,
  SetStatutoryElectionRequest,
  SetStatutoryElectionResponse,
  PayrollVocabularyResponse,
  ReconciliationPayloadResponse,
  ReconciliationQueueResponse,
  ResolveReconciliationResponse,
  SyncCommitResponse,
  AlterBudgetResponse,
  BudgetDimension,
  BudgetLineListResponse,
  BudgetListResponse,
  BudgetVarianceResponse,
  GSTR9Envelope,
  GSTR9TaxLedgers,
  CreateBudgetLineRequest,
  CreateBudgetLineResponse,
  CreateBudgetRequest,
  CreateBudgetResponse,
  DeleteBudgetResponse,
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
 * The print-template path for one document kind.
 *
 * ⚠️ THE KIND IS ENCODED. Every kind the server knows is a plain kebab-case slug
 * that needs no escaping, so this looks like ceremony — but the value reaching
 * here comes from a screen's picker, and an unencoded segment is how a stray
 * slash turns one path into another route entirely. The server refuses an unknown
 * kind with a 400 that names the vocabulary; it should never see a mangled one.
 */
function printTemplate(companyID: string, docKind: string): string {
  return `${company(companyID)}/print-templates/${encodeURIComponent(docKind)}`;
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
/**
 * The server's restore response, snake_case on the wire. Mapped rather than
 * spread so a field the server renames fails HERE, in one place, instead of
 * arriving as undefined on a screen that then renders "undefined memberships
 * were not restored".
 */
interface RestoreWire {
  company_id: string;
  company_name: string;
  row_count: number;
  rows: Record<string, number>;
  skipped_tables: string[] | null;
  skipped_rows: number;
  memberships_not_restored: string[] | null;
  source_schema_version: string;
  schema_version: string;
}

function fromRestoreWire(w: RestoreWire): RestoreResult {
  return {
    companyID: w.company_id,
    companyName: w.company_name,
    rowCount: w.row_count,
    rows: w.rows ?? {},
    skippedTables: w.skipped_tables ?? [],
    skippedRows: w.skipped_rows,
    membershipsNotRestored: w.memberships_not_restored ?? [],
    sourceSchemaVersion: w.source_schema_version,
    schemaVersion: w.schema_version,
  };
}

/** The wire shape of the year-end lock; see BooksClosure for the camelCase one. */
interface BooksClosureWire {
  closed: boolean;
  closed_through?: string;
  closed_at?: string;
  closed_by?: string;
}

/**
 * ⚠️ ABSENT KEYS BECOME null, NOT undefined. The server omits closed_through
 * entirely when nothing is closed (omitempty), and a screen that switched on
 * `undefined` vs `null` would read "never closed" and "closed but unknown" as
 * the same thing.
 */
function fromClosureWire(w: BooksClosureWire): BooksClosure {
  return {
    closed: w.closed,
    closedThrough: w.closed_through ?? null,
    closedAt: w.closed_at ?? null,
    closedBy: w.closed_by ?? null,
  };
}

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
    listGroups(companyID) {
      // A flat list with each row's parent NAMED, ordered by name. Not a tree: the
      // caller that needs one can build it, and the caller that only needs a picker
      // is not made to walk one first.
      return http.get<ListGroupsResponse>(`${company(companyID)}/groups`);
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

    // ---- T14: DATA (Alt+Y) — backup and restore.
    backupCompany(companyID) {
      // getBlob and NOT get: the body is gzip, and `request` reads every
      // response as text and then tries JSON.parse, which for a compressed
      // archive yields a lossy string rather than a file.
      return http.getBlob(`${company(companyID)}/backup`);
    },
    restoreCompany(archive) {
      // NO company() PREFIX, deliberately: the route is outside the
      // {companyID} subtree because the company does not exist yet, so the
      // subtree's membership check would 403 every caller before the company
      // it names could be created.
      return http.postRaw<RestoreWire>(
        "/v1/companies/restore",
        archive,
        "application/gzip",
      ).then(fromRestoreWire);
    },

    // ---- YEAR-END CLOSURE (0089): the third item on the Alt+Y Data menu.
    //
    // ⛔ THE WIRE IS snake_case AND THE CLIENT IS camelCase, converted HERE and
    // nowhere else, so a screen never sees `closed_through`. The three routes
    // answer the SAME body shape on purpose — a client must not have to
    // reconcile two spellings of one fact — so one mapper serves all three.
    booksClosure(companyID) {
      return http
        .get<BooksClosureWire>(`${company(companyID)}/books-closure`)
        .then(fromClosureWire);
    },
    closeBooks(companyID, closedThrough) {
      return http
        .post<BooksClosureWire>(`${company(companyID)}/books-closure`, {
          closed_through: closedThrough,
        })
        .then(fromClosureWire);
    },
    reopenBooks(companyID) {
      return http
        .del<BooksClosureWire>(`${company(companyID)}/books-closure`)
        .then(fromClosureWire);
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
    // ---- Bank statement MATCHING (T4 slice 2b).
    //
    // ⭐ NOTE WHAT IS NOT HERE: there is no `resolveBankMatch`. Accepting a
    // proposal is `setBankDate` above with a statement_line_id in the body,
    // which is the whole point — matching borrows the tick's authority, its
    // back-dated cutoff and its step-up rather than minting its own.
    bankStatementMatches(companyID, ledgerID, opts) {
      const q: Record<string, string> = {};
      if (opts?.from) q.from = opts.from;
      if (opts?.to) q.to = opts.to;
      // Sent only when the caller chose one. An omitted window means "use the
      // build's search width", and the response states which was used.
      if (opts?.windowDays) q.window_days = String(opts.windowDays);
      return http.get<BankMatchesResponse>(
        `${company(companyID)}/bank-statement/${ledgerID}/matches`,
        Object.keys(q).length > 0 ? q : undefined,
      );
    },
    setAsideStatementLine(companyID, ledgerID, lineID, req: SetAsideRequest) {
      return http.post<SetAsideResponse>(
        `${company(companyID)}/bank-statement/${ledgerID}/lines/${lineID}/set-aside`,
        req,
      );
    },
    restoreStatementLine(companyID, ledgerID, lineID) {
      // A POST, not a DELETE: routeAction maps no DELETE on the /bank-statement/
      // prefix, and widening it would give the prefix an authority nobody chose.
      return http.post<SetAsideResponse>(
        `${company(companyID)}/bank-statement/${ledgerID}/lines/${lineID}/restore`,
        {},
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
      return http.get<TaxSectionsResponse>(
        `${company(companyID)}/tds-sections`,
      );
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
    // ---- T11: budgets and budget vs actual (migrations 0085/0086).
    listBudgets(companyID) {
      return http.get<BudgetListResponse>(`${company(companyID)}/budgets`);
    },
    createBudget(companyID, req: CreateBudgetRequest) {
      return http.post<CreateBudgetResponse>(
        `${company(companyID)}/budgets`,
        req,
      );
    },
    alterBudget(companyID, budgetID: string, req: CreateBudgetRequest) {
      // The server nests the read-back under `budget` and may return it null when
      // the read-back failed AFTER a successful write. Handed over AS THE ENVELOPE
      // rather than unwrapped here, because unwrapping would have to invent
      // something for the null case — and the caller is the only one who can decide
      // what to say about "it saved, but we could not read it back".
      return http.put<AlterBudgetResponse>(
        `${company(companyID)}/budgets/${encodeURIComponent(budgetID)}`,
        req,
      );
    },
    deleteBudget(companyID, budgetID: string, deletedBy: string) {
      // ⚠️ THE ACTOR RIDES IN THE QUERY STRING, not a body: a DELETE carries no body
      // by convention and the server refuses an absent actor rather than defaulting
      // it — a plan deleted by nobody in particular is a plan nobody can be asked
      // about.
      return http.del<DeleteBudgetResponse>(
        `${company(companyID)}/budgets/${encodeURIComponent(budgetID)}`,
        { deleted_by: deletedBy },
      );
    },
    listBudgetLines(companyID, budgetID: string) {
      return http.get<BudgetLineListResponse>(
        `${company(companyID)}/budgets/${encodeURIComponent(budgetID)}/lines`,
      );
    },
    addBudgetLine(companyID, budgetID: string, req: CreateBudgetLineRequest) {
      return http.post<CreateBudgetLineResponse>(
        `${company(companyID)}/budgets/${encodeURIComponent(budgetID)}/lines`,
        req,
      );
    },
    alterBudgetLine(
      companyID,
      budgetID: string,
      lineID: string,
      req: CreateBudgetLineRequest,
    ) {
      return http.put<{ id: string; altered: boolean }>(
        `${company(companyID)}/budgets/${encodeURIComponent(budgetID)}/lines/${encodeURIComponent(lineID)}`,
        req,
      );
    },
    deleteBudgetLine(
      companyID,
      budgetID: string,
      lineID: string,
      deletedBy: string,
    ) {
      return http.del<{ id: string; deleted: boolean }>(
        `${company(companyID)}/budgets/${encodeURIComponent(budgetID)}/lines/${encodeURIComponent(lineID)}`,
        { deleted_by: deletedBy },
      );
    },
    budgetVariance(
      companyID,
      budgetID: string,
      dimension: BudgetDimension,
      costCategoryID?: string,
    ) {
      // A FLAT LITERAL PATH with every selector as a query param, not
      // /budgets/{id}/variance — a report's route must spell its own name so
      // routeAction can map it to `report:budget-variance`, exactly as
      // cost-centre-summary takes cost_category_id this way.
      //
      // ⛔ THE PLAN AND THE GRAIN ARE ALWAYS SENT. The server has no default for
      // either, deliberately: defaulting the grain would render a real, correct
      // ledger-level report under a heading the operator chose for groups.
      //
      // The axis is OMITTED rather than sent empty when absent — the server reads a
      // present-but-blank id as a 400 about a malformed uuid rather than the
      // "which axis did you mean" refusal that names the alternatives.
      return http.get<BudgetVarianceResponse>(
        `${company(companyID)}/budget-variance`,
        costCategoryID
          ? { budget_id: budgetID, dimension, cost_category_id: costCategoryID }
          : { budget_id: budgetID, dimension },
      );
    },

    gstr9(
      companyID,
      from: string,
      to: string,
      ledgers: GSTR9TaxLedgers,
      gstin?: string,
    ) {
      // A FLAT LITERAL PATH beside /gstr1 and /gstr3b, with the year and the six
      // ledgers as query params. A report's route must spell its own name so
      // routeAction can map it to `report:gstr9` — the same reason
      // /budget-variance is not /budgets/{id}/variance.
      //
      // ⛔ ALL SIX LEDGER IDS ARE ALWAYS SENT, and the caller cannot omit one:
      // the type requires all six. The server refuses a missing one with a 400
      // naming it, and this client does not paper over that by sending a blank —
      // a present-but-empty id reads as a malformed uuid rather than as the
      // "there is nothing to reconcile against" refusal, and the operator would
      // be told the wrong thing.
      const query: Record<string, string> = {
        from,
        to,
        cgst_ledger_id: ledgers.cgst,
        sgst_ledger_id: ledgers.sgst,
        igst_ledger_id: ledgers.igst,
        input_cgst_ledger_id: ledgers.inputCGST,
        input_sgst_ledger_id: ledgers.inputSGST,
        input_igst_ledger_id: ledgers.inputIGST,
      };
      if (gstin) query.gstin = gstin;
      return http.get<GSTR9Envelope>(`${company(companyID)}/gstr9`, query);
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
      return http.post<EInvoiceRaised>(`${company(companyID)}/e-invoices`, req);
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
    listUnits(companyID) {
      return http.get<ListUnitsResponse>(`${company(companyID)}/units`);
    },
    listStockGroups(companyID) {
      return http.get<ListStockGroupsResponse>(`${company(companyID)}/stock-groups`);
    },
    createStockGroup(companyID, req: CreateStockGroupRequest) {
      return http.post<CreateStockGroupResponse>(
        `${company(companyID)}/stock-groups`,
        req,
      );
    },
    listCurrencies(companyID) {
      return http.get<ListCurrenciesResponse>(`${company(companyID)}/currencies`);
    },
    createCurrency(companyID, req: CreateCurrencyRequest) {
      return http.post<CreateCurrencyResponse>(
        `${company(companyID)}/currencies`,
        req,
      );
    },
    async setCurrencyRate(companyID, req: SetCurrencyRateRequest) {
      await http.post(`${company(companyID)}/currencies/rates`, req);
    },
    inventoryVocabulary(companyID) {
      return http.get<InventoryVocabularyResponse>(
        `${company(companyID)}/inventory-vocabulary`,
      );
    },
    setStockItemValuationMethod(
      companyID,
      stockItemID,
      req: SetValuationMethodRequest,
    ) {
      // A PUT, and a narrow one: it is an ALTER of a master (the server maps it
      // to `alter master`), so it inherits the back-dated cutoff and the
      // passkey step-up like every other write. It answers with the STORED row.
      return http.put<SetValuationMethodResponse>(
        `${company(companyID)}/stock-items/${encodeURIComponent(stockItemID)}/valuation-method`,
        req,
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

    // ---- PER-COMPANY PRINT TEMPLATES (track C). Three verbs, ONE path.
    //
    // The GET and the two writes share the path deliberately, and routeAction
    // maps them onto the SAME `company` resource token — a Display for the read
    // and an Alter for both writes. One resource, one token, so no ACL rule can
    // govern the read and miss the write, which is the rule the company
    // master's own read/write pair is built on.
    getPrintTemplate(companyID, docKind: string) {
      return http.get<PrintTemplateResponse>(printTemplate(companyID, docKind));
    },
    savePrintTemplate(companyID, docKind: string, req: PrintTemplateRequest) {
      return http.put<PrintTemplateResponse>(
        printTemplate(companyID, docKind),
        req,
      );
    },
    clearPrintTemplate(companyID, docKind: string) {
      // DELETE and not "PUT everything blank": the row must GO, so the company
      // reports `customised: false` and is genuinely back on the built-in
      // layout. See the CoredClient interface for why the two are not the same.
      return http.del<PrintTemplateResponse>(printTemplate(companyID, docKind));
    },

    // ---- PAYROLL (Track 3). Eight routes; see the CoredClient interface for
    // why they are eight methods and not one facade.
    listEmployees(companyID, includeInactive?: boolean) {
      // The flag is sent ONLY when true. The server reads
      // `include_inactive == "true"` literally, and sending "false" would be a
      // client asserting a default it does not own.
      return http.get<ListEmployeesResponse>(
        `${company(companyID)}/employees`,
        includeInactive ? { include_inactive: "true" } : undefined,
      );
    },
    createEmployee(companyID, req: CreateEmployeeRequest) {
      // NOTHING IS DEFAULTED HERE, and `is_active` least of all: the server's
      // field is a *bool whose absence means TRUE, so a client that helpfully
      // sent `false` for an unticked box would create employees who are silently
      // left out of the pay run. The form omits the key unless it was chosen.
      return http.post<CreateEmployeeResponse>(
        `${company(companyID)}/employees`,
        req,
      );
    },
    runPayroll(companyID, req: PayRunRequest) {
      // It rides the ordinary /vouchers/{class} shape on purpose — that is what
      // puts the pay run under the SAME PDP mapping, back-dated cutoff and
      // WebAuthn step-up as an invoice, through the same code.
      return http.post<PayRunResponse>(
        `${company(companyID)}/vouchers/payroll`,
        req,
      );
    },
    payrollRegister(companyID, month: string) {
      return http.get<PayrollRegisterResponse>(
        `${company(companyID)}/payroll-register`,
        { month },
      );
    },
    payslip(companyID, month: string, employeeID: string) {
      return http.get<PayslipResponse>(
        `${company(companyID)}/payroll-payslip`,
        { month, employee_id: employeeID },
      );
    },

    // ---- The three statutory FILES. `application/zip`, so getBlob and never the
    // JSON path — which reads every response as text and would hand back a lossy
    // string instead of an archive.
    payrollPFECR(companyID, month: string) {
      return statutory(
        http.getBlob(`${company(companyID)}/payroll-pf-ecr`, { month }),
      );
    },
    payrollESIReturn(
      companyID,
      month: string,
      leavingReasons?: Readonly<Record<string, string>>,
    ) {
      // `leaving_reason` is a REPEATABLE param of the form CODE=Reason, which is
      // why the query is assembled here rather than passed as an object:
      // buildQuery takes flat pairs, and one key repeated is the server's own
      // contract. URLSearchParams encodes the whole `CODE=Reason` pair as ONE
      // value, which is what keeps a reason containing '&' or '=' — "Left &
      // settled", "Retired = superannuation" — from splitting into a second
      // param or a second key. The server cuts on the FIRST '=' after decoding,
      // so the code may not contain one and a reason may.
      const q = new URLSearchParams({ month });
      for (const [code, reason] of Object.entries(leavingReasons ?? {})) {
        q.append("leaving_reason", `${code}=${reason}`);
      }
      return statutory(
        http.getBlob(
          `${company(companyID)}/payroll-esi-return?${q.toString()}`,
        ),
      );
    },
    payrollForm138(companyID, req: Form138Request) {
      // Every particular is a QUERY PARAM because the route is a GET that
      // produces a file, and not one of them is in the database — see
      // Form138Request. `challan` repeats, in the server's own positional form.
      const q = new URLSearchParams();
      q.set("tax_year", req.tax_year);
      q.set("quarter", req.quarter);
      q.set("tan", req.tan);
      q.set("deductor_pan", req.deductor_pan);
      q.set("deductor_name", req.deductor_name);
      q.set("form_number", req.form_number);
      q.set("file_creation_date", req.file_creation_date);
      if (req.file_sequence !== undefined) {
        q.set("file_sequence", String(req.file_sequence));
      }
      for (const ch of req.challans) {
        // MONTH,BSR,DEPOSIT_DATE,SERIAL[,AMOUNT[,book]] — positional, and the
        // AMOUNT is omitted when the operator did not state one so the server
        // DERIVES it from the month's payslips. Sending an empty field instead
        // would be a stated amount of nothing, which the reconciliation refuses.
        const parts = [
          ch.month,
          ch.bsr_code,
          ch.date_of_deposit,
          String(ch.challan_serial_number),
        ];
        if (ch.total_tds_deposited && ch.total_tds_deposited.trim() !== "") {
          parts.push(ch.total_tds_deposited.trim());
          if (ch.book_entry) parts.push("book");
        } else if (ch.book_entry) {
          // The `book` flag is the SIXTH field, so it needs the fifth present.
          // An empty amount here is the one shape the parser reads as "derive".
          parts.push("");
          parts.push("book");
        }
        q.append("challan", parts.join(","));
      }
      return statutory(
        http.getBlob(`${company(companyID)}/payroll-form-138?${q.toString()}`),
      );
    },

    // ---- The employee master's two effective-dated children. The employee id
    // goes in the PATH and never in a body: it is not the operator's to type —
    // it comes from the master they are looking at — and a body that could
    // disagree with the URL is a question about which one wins.
    listSalaryComponents(companyID, employeeID: string) {
      return http.get<ListSalaryComponentsResponse>(
        `${company(companyID)}/employees/${encodeURIComponent(employeeID)}/salary-components`,
      );
    },
    setSalaryComponent(
      companyID,
      employeeID: string,
      req: SetSalaryComponentRequest,
    ) {
      return http.post<SetSalaryComponentResponse>(
        `${company(companyID)}/employees/${encodeURIComponent(employeeID)}/salary-components`,
        req,
      );
    },
    listStatutoryElections(companyID, employeeID: string) {
      return http.get<ListStatutoryElectionsResponse>(
        `${company(companyID)}/employees/${encodeURIComponent(employeeID)}/statutory-elections`,
      );
    },
    setStatutoryElection(
      companyID,
      employeeID: string,
      req: SetStatutoryElectionRequest,
    ) {
      return http.post<SetStatutoryElectionResponse>(
        `${company(companyID)}/employees/${encodeURIComponent(employeeID)}/statutory-elections`,
        req,
      );
    },
    payrollVocabulary(companyID) {
      return http.get<PayrollVocabularyResponse>(
        `${company(companyID)}/payroll-vocabulary`,
      );
    },

    // ---- OFFLINE SYNC + THE RECONCILIATION QUEUE (track D).
    //
    // Four routes, three of which were mounted long before anything called them.
    // The commit path has existed since Task C; the queue listing since the
    // freeze rule shipped; only the payload read and the resolve are new.
    syncCommit(companyID, intents) {
      // ONE call for the whole outbox. See CoredClient.syncCommit: the cutoff
      // judges the batch as a unit and a step-up is charged once per write, so
      // draining row by row is not a smaller version of this — it is a different
      // and worse behaviour.
      //
      // ⛔⛔ `wireVoucher` ON EVERY PAYLOAD, AND ITS ABSENCE HERE WAS A REAL DEFECT
      // FOUND IN A BROWSER. `SyncIntent.payload` IS a `VoucherRequest` — the same
      // type the online path posts, deliberately, so the server can decode both
      // with the single `buildVoucherInput` translator. That equivalence cuts both
      // ways: it also needs the SAME transformation on the way out. Every
      // `post*Voucher` above sends `wireVoucher(req)`, which maps the frontend's
      // legible `dr_cr` ("DR"/"CR") to the backend's canonical "D"/"C"; this route
      // sent the intents untouched.
      //
      // ⛔ AND IT FAILED IN THE WORST AVAILABLE WAY: not at queue time, when an
      // operator could react, but at COMMIT time. `ledger.Debit` is "D", so a "DR"
      // posting matched NEITHER side, was summed as a credit, and the batch was
      // refused 422 "debit total must equal credit total". An operator could key
      // ten invoices offline, see each one safely queued, and then find that NONE
      // of them can ever be committed — with the outbox correctly refusing to
      // clear, so the queue would simply never drain.
      //
      // ⚠️ NO UNIT TEST COULD HAVE SEEN IT. The mock client is not an HTTP client,
      // so it never runs this adapter, and it accepts either spelling; the vitest
      // suite proved the whole queue-and-commit flow against it and passed. The
      // 422 appeared on the first real commit from a browser. Verifying that the
      // pieces exist is not verifying that they compose.
      return http.post<SyncCommitResponse>(`${company(companyID)}/sync/commit`, {
        intents: intents.map((i) => ({ ...i, payload: wireVoucher(i.payload) })),
      });
    },
    reconciliationQueue(companyID) {
      return http.get<ReconciliationQueueResponse>(`${company(companyID)}/reconciliation-queue`);
    },
    reconciliationPayload(companyID, reconID) {
      return http.get<ReconciliationPayloadResponse>(
        `${company(companyID)}/reconciliation-queue/${encodeURIComponent(reconID)}/payload`,
      );
    },
    resolveReconciliation(companyID, reconID) {
      // No body: the route asserts nothing but "a human dealt with this", and
      // resolved_at is stamped server-side with now() so there is no date a
      // caller could supply.
      return http.post<ResolveReconciliationResponse>(
        `${company(companyID)}/reconciliation-queue/${encodeURIComponent(reconID)}/resolve`,
      );
    },
  };
}

/**
 * Attach the two statutory headers to a blob download.
 *
 * ⛔ THE DISCLOSURE IS READ OFF THE RESPONSE AND NEVER WRITTEN HERE. The server
 * sets `X-Statutory-Disclosure` because no emitted file in this product has been
 * through the FVU, the EPFO portal or the ESIC portal — none of which can run in
 * this environment — and a client that composed that sentence itself would be one
 * refactor away from composing a friendlier one.
 */
function statutory(
  pending: Promise<{
    blob: Blob;
    filename: string | null;
    header(name: string): string | null;
  }>,
): Promise<StatutoryFileDownload> {
  return pending.then((res) => ({
    blob: res.blob,
    filename: res.filename,
    statutoryFile: res.header("X-Statutory-File"),
    disclosure: res.header("X-Statutory-Disclosure"),
  }));
}
