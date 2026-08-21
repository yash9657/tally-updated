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
  ListGodownsResponse,
  ListStockBatchesResponse,
  ListStockItemsResponse,
  StockSummaryResponse,
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
  /**
   * Read the chart of accounts (GET .../ledgers). The only way to resolve the
   * id of a ledger this client did not create itself — including the ledgers
   * every company is SEEDED with (Cash, Profit & Loss A/c). Without it callers
   * end up minting duplicates of the predefined ledgers.
   */
  listLedgers(companyID: string): Promise<ListLedgersResponse>;
  /**
   * Set a ledger's OPENING BALANCE + bill-wise opening breakup. Needed for the
   * ledgers seeded with the company, which cannot carry one at creation.
   */
  setLedgerOpening(
    companyID: string,
    ledgerID: string,
    req: SetLedgerOpeningRequest,
  ): Promise<SetLedgerOpeningResponse>;

  // ---- PRINTING (T1) and BANK RECONCILIATION (T4).
  //
  // Both server halves shipped reachable over HTTP only, with no key anywhere in
  // this client — the handoff calls that a parity gap rather than a nicety, since
  // keystroke compatibility is what lets an operator switch without retraining.
  // These five methods are the client half.

  /**
   * Fetch one voucher's document as a PDF (GET .../print/vouchers/{class}/{id}).
   *
   * `voucherClass` is the kebab-case class and is a LITERAL path segment, so it
   * must be one of the six the server produces a document for — ask
   * screens/printTarget.ts, which refuses locally rather than round-tripping to a
   * 404 the operator cannot explain.
   *
   * Rejects with ApiError carrying the SERVER'S message. Do not rewrite it: a
   * class mismatch answers 404 with a deliberately class-neutral body, and
   * "helpfully" turning that into "wrong class" would leak the very fact the
   * refusal protects.
   */
  printVoucher(
    companyID: string,
    voucherClass: string,
    voucherID: string,
  ): Promise<{ blob: Blob; filename: string | null }>;
  /**
   * Fetch a report as a PDF (GET .../print/reports/{slug}). Nine slugs print;
   * every other known report name answers 501 on purpose, which arrives here as
   * an ApiError to be shown verbatim.
   *
   * `query` carries the parameters a report needs to be unambiguous — today
   * only bank-reconciliation, which takes `ledger_id` or `all` and answers 400
   * with neither, because a company with three banks has not said which
   * statement it wants (docs/design/t4-model.md §1.3, decision (c)).
   */
  printReport(
    companyID: string,
    slug: string,
    query?: Record<string, string>,
  ): Promise<{ blob: Blob; filename: string | null }>;

  /**
   * The Bank Reconciliation Statement for one bank ledger, as at an optional
   * date (omitted = today). A non-bank ledger is a 400 that NAMES the problem
   * rather than an empty statement that reads as "nothing to reconcile".
   */
  bankReconciliation(
    companyID: string,
    ledgerID: string,
    asOn?: string,
  ): Promise<BankReconciliationResponse>;
  /** Record that the bank cleared one leg. */
  setBankDate(
    companyID: string,
    ledgerID: string,
    voucherID: string,
    legNo: number,
    req: SetBankDateRequest,
  ): Promise<SetBankDateResponse>;
  /**
   * Un-reconcile one leg. IDEMPOTENT by design — clearing an already-unreconciled
   * leg is a 200, because the caller's intent is satisfied either way.
   */
  clearBankDate(
    companyID: string,
    ledgerID: string,
    voucherID: string,
    legNo: number,
  ): Promise<SetBankDateResponse>;

  /**
   * Propose how a bank CSV should be read, from the file's own header row.
   * WRITES NOTHING.
   *
   * This exists because the build ships no per-bank column layouts: see
   * `PreviewStatementResponse.unresolved`, which a caller must show rather than
   * confirm past. A mapping guessed instead of confirmed does not fail — it
   * produces real money on real dates that was never a transaction.
   */
  previewBankStatement(
    companyID: string,
    ledgerID: string,
    req: PreviewStatementRequest,
  ): Promise<PreviewStatementResponse>;
  /**
   * Import a statement under a CONFIRMED mapping. Re-importing the same file is
   * a success and a no-op (`already_imported`); overlapping files skip the rows
   * already present rather than doubling them.
   */
  importBankStatement(
    companyID: string,
    ledgerID: string,
    req: ImportStatementRequest,
  ): Promise<ImportStatementResponse>;
  /**
   * What the bank said, over an optional period. Says NOTHING about what has
   * cleared — matching is slice 2b, and the response's `disclosures` say so.
   */
  importedStatement(
    companyID: string,
    ledgerID: string,
    from?: string,
    to?: string,
  ): Promise<ImportedStatementResponse>;

  /**
   * Every recorded deductor-status year, plus the category vocabulary to render
   * a picker from. Owner-only: recording it decides a year of withholding, so a
   * Data Entry token gets 403 on both halves.
   */
  listDeductorStatus(companyID: string): Promise<DeductorStatusListResponse>;
  /**
   * Record or CORRECT one tax year's status. Idempotent on the year — there is
   * one answer per year, and re-stating it corrects it.
   *
   * ⚠️ `taxYearStart` is the year the TURNOVER TEST reads, which is the year
   * BEFORE the payments it governs: pass 2025 to make 2026-27 postable.
   */
  setDeductorStatus(
    companyID: string,
    taxYearStart: number,
    req: SetDeductorStatusRequest,
  ): Promise<SetDeductorStatusResponse>;

  /**
   * Every s.394(2) declaration this company holds, plus the sections one may be
   * filed against. Owner-only, like deductor status.
   */
  listTCSDeclarations(companyID: string): Promise<TCSDeclarationListResponse>;
  /**
   * File (or correct) a buyer's s.394(2) declaration. Idempotent on
   * (buyer, section, tax year).
   *
   * Filing one EXEMPTS that buyer's future sales under the section — the post
   * path reads this table, so there is no exemption flag on a voucher.
   */
  createTCSDeclaration(
    companyID: string,
    req: CreateTCSDeclarationRequest,
  ): Promise<CreateTCSDeclarationResponse>;

  /**
   * The TDS/TCS register for a period. BOTH dates are required — the server
   * refuses to default a period, because a correctly rendered register for the
   * wrong quarter is worse than a refusal.
   */
  tdsRegister(
    companyID: string,
    from: string,
    to: string,
  ): Promise<TDSRegisterResponse>;

  /**
   * The challan register for ONE tax year, plus the two things a deposit form
   * cannot be built without: the minor-head vocabulary, and WHAT CAN BE
   * ALLOCATED (T6, migration 0069).
   *
   * ⛔ `allocatable_deductions` IS NOT A CONVENIENCE. An allocation is keyed on a
   * UUID and a 0-based leg ordinal, and NO SCREEN IN THIS PRODUCT DISPLAYS
   * EITHER, so a form that asks a human to type them asks for something they
   * cannot obtain. The identifiers — and the amount, which must equal the
   * deduction's tax exactly — travel from this response into the POST without a
   * human ever seeing them. `allocatable_error` means only that read failed: the
   * register is still served and still worth rendering.
   *
   * ⚠️ `taxYearStart` IS REQUIRED AND THE SERVER DEFAULTS NOTHING. It is the year
   * the DEPOSIT BELONGS TO, not the year its date falls in: a challan paid on
   * 7 April 2026 is normally for March of 2025-26, which is why it is stored on
   * the challan. A correctly rendered list of the wrong year's deposits is worse
   * than a refusal, because it reads exactly like the answer that was asked for.
   *
   * Data Entry is refused this route BY DESIGN (`tds-challan` is a bare resource
   * token, and that level's default_access is 'none'), so a 403 is a normal
   * answer that must be rendered as the decision it is — never swallowed.
   */
  listTDSChallans(
    companyID: string,
    taxYearStart: number,
  ): Promise<TDSChallanListResponse>;
  /**
   * Record ONE deposit and everything it paid for, in one transaction.
   *
   * There is no PUT, no PATCH and no DELETE: a challan is an EVENT that happened
   * at a bank counter, not a row an operator maintains. Re-posting one CIN is a
   * 409 and NEVER an upsert — the second post is either a double entry or a
   * second real deposit whose serial was mistyped, and overwriting either loses
   * money. Re-allocation and correction are not in this slice.
   *
   * The refusals a caller must render as sentences: 400 for the operator's input
   * or an allocation naming a deduction that does not exist / is not that
   * amount, 409 for a duplicate CIN or a deduction another challan already paid
   * (RETRYING WOULD BE THE MISTAKE), 403 when the level is not permitted.
   */
  createTDSChallan(
    companyID: string,
    req: CreateTDSChallanRequest,
  ): Promise<CreateTDSChallanResponse>;

  /**
   * ATTACH FURTHER DEDUCTIONS TO A DEPOSIT THAT IS ALREADY RECORDED (D1).
   *
   * ⛔ ITS ABSENCE WAS A TRAP AN OPERATOR COULD NOT GET OUT OF. A challan may be
   * recorded naming fewer deductions than it actually paid for — nothing
   * requires the allocations to cover the tax — and the create is the only other
   * writer, so the same CIN cannot be entered again. The audit tried seven doors
   * in a browser and six HTTP methods and challan 05130's unallocated balance
   * stayed at exactly 12000.0000 while the return refused the unattached
   * deduction as never deposited. The ROUTE has existed since that pass; this
   * method is the half that makes it reachable from the product.
   *
   * ⛔ THE CALLER MUST RENDER `challan.unallocated_tax_rupees`. It is the whole
   * oracle of this route — the number that could never fall — and a screen that
   * reports success from the 200 alone looks IDENTICAL whether it moved anything
   * or not. The handler re-reads the challan for exactly this reason, so nothing
   * has to be inferred.
   *
   * ⚠️ IT IS AN ADD, NOT AN EDIT: it cannot move an allocation off another
   * challan (409, naming the challan that has it), cannot change an amount and
   * cannot remove anything. The refusals a caller must render as sentences:
   * 400 for an empty list / an amount that is not the deduction's tax / a
   * deduction of another tax year, 404 for a challan this company does not have
   * (RLS makes another tenant's look exactly like a missing one, deliberately),
   * 409 for a deduction another challan already paid or an allocation total that
   * would exceed the deposit, 403 when the level is not permitted.
   *
   * ⚠️ AND IT CAN BE REFUSED BY THE BACK-DATED CUTOFF WITH NO DATE IN THE BODY —
   * the act adopts the challan's own `deposit_date`. The refusal names "deposit
   * date" so the sentence is about a field the operator can see.
   */
  allocateToTDSChallan(
    companyID: string,
    challanID: string,
    req: AllocateToChallanRequest,
  ): Promise<AllocateToChallanResponse>;

  /**
   * The deductor block of the return, plus the sixteen Annexure 4 categories and
   * the conditional rules that hang off them (T6, migration 0070).
   *
   * A company that has not filled the form in answers 200 with
   * `profile: null, profile_recorded: false` — NEVER 404. The vocabulary the
   * empty form is drawn from arrives in the same response, which is why.
   */
  getTDSProfile(companyID: string): Promise<TDSProfileResponse>;
  /**
   * Record or restate the deductor profile. THE WHOLE ROW, every time — the
   * cross-field rules (state code, ministry) are not checkable on a partial
   * body and a stale conditional value would survive one.
   *
   * ⚠️ "Saved" does NOT mean the return can be filed, and a screen must not say
   * so: this route checks the SHAPE of a state code and never the Annexure 1
   * list, which lives in a package it may not import. The register's
   * `return_file` status is what answers "can this be filed".
   */
  setTDSProfile(
    companyID: string,
    req: SetTDSProfileRequest,
  ): Promise<SetTDSProfileResponse>;

  // ---- T2: cost centres and cost categories.
  //
  // ⛔ THE ONE RULE THAT GOVERNS EVERY CALLER OF THESE SIX: cost figures from
  // different AXES describe the SAME money. One Rs 1,00,000 posting is allocated in
  // full across Departments AND AGAIN in full across Branches, so any total that
  // spans axes is a multiple of the cost actually borne. Scope every figure to one
  // axis, or group by axis with no grand total. See {@link CostCategory}.

  /** The axes. Served by the centres call too, so a create form never asks for an id. */
  listCostCategories(companyID: string): Promise<CostCategoryListResponse>;
  createCostCategory(
    companyID: string,
    req: CreateCostCategoryRequest,
  ): Promise<CostCategory>;
  /**
   * Rename an axis. The name is unique per company, case- and whitespace-
   * insensitively, so a 409 here means an axis of that name already exists.
   */
  renameCostCategory(
    companyID: string,
    categoryID: string,
    req: CreateCostCategoryRequest,
  ): Promise<CostCategory>;
  /**
   * The centres, optionally narrowed to one axis.
   *
   * ⚠️ Passing an axis id that does not exist is a 404, which is NOT the same answer
   * as an empty axis — a caller must render the two differently or an operator
   * cannot tell "wrong axis" from "nothing keyed yet".
   */
  listCostCentres(
    companyID: string,
    costCategoryID?: string,
  ): Promise<CostCentreListResponse>;
  /**
   * Create a centre on an axis.
   *
   * cost_category_id is REQUIRED — there is no uncategorised centre. Omit parent_id
   * (or send it empty) for a centre at the top of its axis; sending the all-zeros
   * uuid is a 400, not a synonym for "no parent".
   *
   * A 409 means the name is taken ON THAT AXIS. The same name on a DIFFERENT axis is
   * legal, so the message must name the axis or it reads as a bug.
   */
  createCostCentre(
    companyID: string,
    req: CreateCostCentreRequest,
  ): Promise<CostCentreWriteResponse>;
  /** Rename or RE-PARENT a centre. A parent must sit on the same axis (422 if not). */
  alterCostCentre(
    companyID: string,
    centreID: string,
    req: CreateCostCentreRequest,
  ): Promise<CostCentreWriteResponse>;

  /**
   * THE COST CENTRE SUMMARY, for ONE axis over a period.
   *
   * costCategoryID is REQUIRED and there is no "all axes" mode: see the rule above.
   * A missing axis is a 400 whose message names the axes available; an unknown one
   * is a 422.
   */
  costCentreSummary(
    companyID: string,
    costCategoryID: string,
    from: string,
    to: string,
  ): Promise<CostCentreSummaryResponse>;
  /**
   * THE COST CATEGORY SUMMARY — one row per axis, and the GAP.
   *
   * It takes no axis: this report IS the list of axes. Its point is
   * `unallocated_*` — cost attached to nothing on an axis, which no database
   * constraint can see because the sum trigger fires on allocations and there are
   * none to fire on.
   */
  costCategorySummary(
    companyID: string,
    from: string,
    to: string,
  ): Promise<CostCategorySummaryResponse>;

  // ---- T3: sales/purchase orders, the bill of materials, and the order books.
  //
  // ⛔ THE ONE RULE THAT GOVERNS EVERY CALLER OF THESE: AN ORDER IS A COMMITTED
  // QUANTITY, NOT A MOVEMENT. Nothing here affects closing stock, the Profit & Loss
  // or the Balance Sheet, and an order must never be shown in the Day Book beside
  // vouchers — it is a promise, not an entry.
  //
  // ⚠️ AND THE SECOND RULE, WHICH IS AN AUTHORISATION FACT: sales and purchase
  // orders are SEPARATE authorities with separate routes and separate report names.
  // Do not build one screen with a direction toggle — the toggle would be reaching
  // a resource the operator may be denied, and they would see a 403 they cannot
  // explain.

  /**
   * Create an order. The DIRECTION IS THE METHOD, not a field: `direction` is not
   * on {@link CreateOrderRequest}'s wire body at all, because the server decides
   * the authority from the path.
   *
   * A 409 means the number is taken FOR THAT DIRECTION, matched
   * case-insensitively — a sales and a purchase order may share a number.
   */
  createSalesOrder(
    companyID: string,
    req: CreateOrderRequest,
  ): Promise<{ order_id: string }>;
  createPurchaseOrder(
    companyID: string,
    req: CreateOrderRequest,
  ): Promise<{ order_id: string }>;
  /**
   * List orders in one direction. `openOnly` narrows to orders with something still
   * outstanding, which is what a fulfilment picker wants.
   *
   * ⚠️ The rows carry NO lines: loading every line of every order to render a
   * picker is how a list of a thousand orders becomes a request that times out.
   * Call {@link ClientApi.getSalesOrder} for the lines.
   */
  listSalesOrders(
    companyID: string,
    openOnly?: boolean,
  ): Promise<OrderListResponse>;
  listPurchaseOrders(
    companyID: string,
    openOnly?: boolean,
  ): Promise<OrderListResponse>;
  /**
   * One order with its lines and their DERIVED fulfilment.
   *
   * ⚠️ A 404 here can mean "no such order" OR "that id is an order of the OTHER
   * direction" — both families read one table and the server refuses to serve one
   * through the other's route, because a caller authorised only for sales orders
   * would otherwise read the company's purchase commitments. Do not report a 404
   * as "deleted".
   *
   * ⛔ `fulfilled_qty` and `outstanding_qty` are DERIVED SERVER-SIDE on every read
   * and are not stored anywhere. Do not cache them across a voucher edit: altering
   * a delivery note changes them without touching the order.
   */
  getSalesOrder(companyID: string, orderID: string): Promise<OrderView>;
  getPurchaseOrder(companyID: string, orderID: string): Promise<OrderView>;
  /**
   * ABANDON an order, with a required reason — NOT "mark it fulfilled". Fulfilment
   * is derived and can never be asserted; this says the commitment will not be met
   * and should leave the order book.
   *
   * `is_closed: false` re-opens it and clears the reason, which is the only reading
   * under which "I closed the wrong order" is correctable. Closing with no reason
   * is a 422: an abandoned commitment with no stated cause cannot be told apart
   * from a mistake.
   */
  closeSalesOrder(
    companyID: string,
    orderID: string,
    req: CloseOrderRequest,
  ): Promise<{ order_id: string; is_closed: boolean }>;
  closePurchaseOrder(
    companyID: string,
    orderID: string,
    req: CloseOrderRequest,
  ): Promise<{ order_id: string; is_closed: boolean }>;

  /**
   * Create a recipe. ⛔ Send `output_qty` as the YIELD and the components stated
   * AGAINST it — never pre-divided per unit. A 10kg-makes-3 recipe normalised to
   * 3.3333/unit truncates and shorts every batch forever; see {@link CreateBomRequest}.
   *
   * A 409 means either the recipe NAME is taken on that item, or the item already
   * has a DEFAULT recipe. The two need different sentences: the first is "pick
   * another name", the second is "clear the other default or add this as an
   * alternative".
   */
  createBom(companyID: string, req: CreateBomRequest): Promise<{ bom_id: string }>;
  /**
   * List recipes, optionally for one item.
   *
   * ⚠️ OMIT `stockItemID` to list every recipe — sending the all-zeros uuid is a
   * 400, not a synonym for "no filter". The rows carry no components, for the
   * reason the order list carries no lines.
   */
  listBoms(companyID: string, stockItemID?: string): Promise<BomListResponse>;
  getBom(companyID: string, bomID: string): Promise<BomView>;
  /**
   * Scale a recipe to a batch size. `qty` is REQUIRED — there is no default,
   * because guessing 1 would answer a question about a different batch.
   *
   * ⛔ IT WRITES NOTHING AND POSTS NOTHING. The response is what a Stock Journal's
   * lines WOULD be; the operator submits that voucher themselves, which is what
   * keeps a manufacture an auditable entry rather than a side effect of opening a
   * recipe. And RENDER `notes` — they state that the explosion is SINGLE-LEVEL, so
   * a screen does not imply it reached raw materials.
   */
  explodeBom(
    companyID: string,
    bomID: string,
    qty: string,
  ): Promise<BomExplosionResponse>;

  /**
   * T5 — RAISE THE e-WAY BILL for one voucher.
   *
   * ⛔ WHAT COMES BACK IS NOT A GOVERNMENT-ISSUED e-WAY BILL, and a screen must
   * say so on the artefact's own face. `local_reference` is a LOCAL identifier;
   * an EBN is issued by NIC, which is unreachable from this deployment. Render
   * `disclosures` — see {@link EWayBillResponse}.
   *
   * A 422 is the interesting failure and there are three kinds, each carrying its
   * own reasoning rather than a bare sentence:
   *   - no distance stated → `blockers` + `basis` (the validity is uncomputable)
   *   - the voucher moves no goods → an e-way bill covers a MOVEMENT of goods
   *   - the payload departs from the recorded NIC schema → `violations`
   *
   * ⚠️ RAISING TWICE FOR ONE VOUCHER IS NOT AN ERROR. It replaces the stored bill
   * and increments `generation_count`, which is how an operator corrects a
   * typo'd distance or adds a vehicle to a Part-A slip.
   */
  raiseEWayBill(
    companyID: string,
    req: EWayBillRequest,
  ): Promise<EWayBillResponse>;
  /**
   * Read the raised e-way bill for a voucher. 404 when none has been raised.
   *
   * ⛔ CHECK `stale` ON WHAT COMES BACK. It means the invoice was altered after
   * the bill was raised, so the payload describes goods the voucher no longer
   * states. Nothing else in the system will ever say so.
   */
  getEWayBill(
    companyID: string,
    voucherID: string,
  ): Promise<EWayBillResponse>;

  /**
   * RAISE THE e-INVOICE for one voucher, through cored's proxy into statutoryd.
   *
   * ⛔⛔ THIS IS THE MOST CONSEQUENTIAL WRITE IN THE PRODUCT AND IT HAS NO UNDO.
   * On success the voucher is FROZEN: it cannot be altered and cannot be
   * deleted, ever, by anybody, and there is no reset path anywhere. Everything
   * else here is correctable. A screen calling this must say so BEFORE the key
   * is pressed, not on the result panel.
   *
   * ⛔ WHAT COMES BACK IS NOT A GOVERNMENT-ISSUED IRN in this deployment — see
   * {@link EInvoiceRaised}. No IRP credentials exist (HANDOFF §10 item 4).
   *
   * ⚠️ RE-RAISING IS SAFE AND RETURNS THE SAME IRN. That is the entire reason
   * the workflow is durable: its `generate` step is memoized, so a retry after a
   * transient fault CANNOT mint a second IRN against a real government API.
   * This is what makes "try again" safe advice for a screen to print — and it is
   * the OPPOSITE of {@link raiseEWayBill}, which replaces the stored bill and
   * increments a generation count. Do not copy that screen's re-raise wording.
   *
   * The three failure families are kept apart because three different people fix
   * them, and a screen that collapses them helps none of the three:
   *   - 422 THE VOUCHER IS WRONG — the accountant fixes it. Includes "this is
   *     the WRONG VOUCHER": only a Sales invoice is e-invoiceable, and a
   *     Purchase can be a perfectly good purchase and still be refused. Also a
   *     stock master with no unit of measure, and an arithmetic the books and
   *     NIC's two-decimal item block cannot both satisfy. Retrying unchanged
   *     cannot help.
   *   - 503 SOMETHING IS DOWN OR SLOW — whoever runs the stack fixes it, and the
   *     operator should simply try again. statutoryd not answering, its service
   *     token dead, or a transient IRP fault.
   *   - 404 no such voucher.
   */
  raiseEInvoice(
    companyID: string,
    req: EInvoiceRaiseRequest,
  ): Promise<EInvoiceRaised>;
  /**
   * Read the IRN pinned onto a voucher. 404 when none has been raised, which is
   * the normal answer for almost every voucher and must not surface as an error.
   *
   * ⚠️ CARRIES NO `ack_no` — the schema has no column for it. See
   * {@link EInvoicePinned}.
   */
  getEInvoice(companyID: string, voucherID: string): Promise<EInvoicePinned>;

  /**
   * THE ORDER BOOKS — outstanding commitments, per direction, as at a date.
   *
   * ⛔ TWO METHODS AND NOT ONE WITH A DIRECTION ARGUMENT, mirroring the two report
   * names. The PDP decides on the report name, so a caller may hold one and be
   * denied the other; a single method would hide that behind an argument and
   * produce a 403 nobody can attribute.
   *
   * `asOn` may be omitted, and then the position is from every order ever placed.
   * The response SAYS SO in `notes` rather than leaving a reader to assume a default.
   */
  salesOrderBook(
    companyID: string,
    asOn?: string,
  ): Promise<OrderBookResponse>;
  purchaseOrderBook(
    companyID: string,
    asOn?: string,
  ): Promise<OrderBookResponse>;

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

  // ---- Inventory masters (the READ half) ---------------------------------
  //
  // These exist for the same reason listLedgers does: a caller that did not
  // create a master itself has no way to address it, and a client that cannot
  // enumerate stock items invents duplicates rather than reusing rows it cannot
  // name. The item grid cannot offer a location picker against a collection it
  // cannot list.
  listStockItems(companyID: string): Promise<ListStockItemsResponse>;
  listGodowns(companyID: string): Promise<ListGodownsResponse>;
  listStockBatches(companyID: string): Promise<ListStockBatchesResponse>;

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
  /**
   * Credit Note — a sales return or downward price correction. The CORRECT way
   * to reverse part of an invoice: a separately-dated document with its own
   * number, reported in GSTR-1's cdnr section. Altering the original invoice
   * instead rewrites history and back-dates the reversal into a filed period.
   */
  postCreditNoteVoucher(
    companyID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  /** Debit Note — a purchase return / upward price correction on a supplier. */
  postDebitNoteVoucher(
    companyID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;

  // ---- The NON-ACCOUNTING stock vouchers -----------------------------------
  //
  // These four move goods and post ZERO accounting lines. They are not a
  // variant of the eight above: a body carrying `postings`, `bill_allocations`,
  // `gst` or `tds` is REFUSED (422), because skipping the double-entry check is
  // only safe when paired with a hard refusal of anything that looks like a
  // money side. Their VoucherResponse carries "0.0000" for every tax head.
  //
  // They exist for the reason a warehouse operator needs them: goods leave
  // hours or weeks before the billing department raises the invoice, and a
  // system that can only record a movement by also recording revenue forces the
  // operator to either invent an invoice or key nothing at all.

  /** Delivery Note (Alt+F8) — goods OUT against a customer. Every line Outward. */
  postDeliveryNoteVoucher(
    companyID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  /** Receipt Note (Alt+F9) — goods IN from a supplier. Every line Inward. */
  postReceiptNoteVoucher(
    companyID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  /**
   * Stock Journal (Alt+F7) — a transfer: OUT of one godown and IN to another on
   * the SAME voucher. The only class that carries both directions at once.
   */
  postStockJournalVoucher(
    companyID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  /**
   * Physical Stock (Alt+F10) — the COUNTED positions (`physical_count`), never
   * the movements. The server differences the count against the book position
   * inside the post transaction and writes the correction.
   *
   * DENIED to the seeded Data Entry level by migration 0031, so a 403 here is a
   * normal, expected answer that callers must render as a stated reason rather
   * than as a blank screen: it is the only document that makes the books agree
   * with a number a human asserts, so it is a supervisor's key.
   */
  postPhysicalStockVoucher(
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
  alterCreditNoteVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  alterDebitNoteVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;

  // The stock classes alter the same way: a FULL restatement of the goods.
  alterDeliveryNoteVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  alterReceiptNoteVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  alterStockJournalVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;
  /**
   * Restate a Physical Stock voucher. Bound for completeness of the class
   * table, but the SCREEN refuses to drive it from a fetched voucher: GET
   * projects the DELTA movements, not the count (the count is not stored — the
   * closing position immediately after the voucher IS the count), so a fetched
   * body cannot be re-posted verbatim. See `stockAlterRefusal`.
   */
  alterPhysicalStockVoucher(
    companyID: string,
    voucherID: string,
    req: VoucherRequest,
  ): Promise<VoucherResponse>;

  // Voucher fetch (GET /vouchers/{voucherID}) — the CURRENT head version of one
  // logical voucher. The source of truth for ALTER hydration: postings carry
  // REAL ledger_id uuids (not Day Book names) plus persisted GST attributes, so
  // a full restatement resends valid uuids and the numbering is preserved.
  getVoucher(companyID: string, voucherID: string): Promise<VoucherView>;

  /**
   * The EDIT LOG of one logical voucher (GET .../vouchers/{id}/history): every
   * hash-chained version, oldest first, WITH the server's verification of the
   * chain. Unlike {@link getVoucher} it reads THROUGH a tombstone — a deletion
   * is the most interesting entry an audit log can carry.
   */
  getVoucherHistory(
    companyID: string,
    voucherID: string,
  ): Promise<VoucherHistoryResponse>;

  // Voucher deletion (DELETE /vouchers/{voucherID}) — class-agnostic tombstone.
  deleteVoucher(
    companyID: string,
    voucherID: string,
    createdBy?: string,
  ): Promise<DeleteVoucherResponse>;

  // Reports
  //
  // PERIOD SEMANTICS DIFFER BY STATEMENT, and the signatures say so rather than
  // taking a uniform {from,to} that two of the three would have to reinterpret:
  //
  //  - The Trial Balance and Balance Sheet are POSITIONS AS AT a date. They take
  //    `asOn` only. Applying a lower bound would silently drop the entity's
  //    earlier history and still foot — wrong, and invisibly so.
  //  - The Profit & Loss is a statement FOR A PERIOD, so it takes both bounds
  //    and excludes the opening balance (a position carried from before the
  //    books began is not income of this quarter).
  //
  // Every argument is optional; omitting them is "everything ever posted".
  getTrialBalance(
    companyID: string,
    asOn?: string,
  ): Promise<TrialBalanceResponse>;
  getBalanceSheet(
    companyID: string,
    asOn?: string,
  ): Promise<BalanceSheetResponse>;
  getProfitLoss(
    companyID: string,
    from?: string,
    to?: string,
  ): Promise<ProfitLossResponse>;
  getDayBook(companyID: string, query?: DayBookQuery): Promise<DayBookResponse>;
  getOutstandings(
    companyID: string,
    query?: OutstandingsQuery,
  ): Promise<OutstandingsResponse>;
  /**
   * The STOCK SUMMARY as at a date — a POSITION, so it takes `asOn` only, for
   * the same reason the Trial Balance does: applying a lower bound would drop
   * the stock the company already owned while still rendering a tidy-looking
   * table, which is the worst kind of wrong answer.
   *
   * It is a Display over `report:stock-summary`, and migration 0026 explicitly
   * DISALLOWS it to the seeded Data Entry level (the closing valuation rate is
   * what the business PAID), so a 403 here is a normal, expected answer that
   * callers must render as a stated reason rather than as an empty table.
   */
  getStockSummary(
    companyID: string,
    asOn?: string,
  ): Promise<StockSummaryResponse>;

  // GST
  generateGSTR1(companyID: string, query: GSTR1Query): Promise<GSTR1Response>;

  // "Ask your books" conversational query (POST /query). Body carries an NL
  // question (default path) OR a pre-built typed DSL (power users); the server
  // translates+compiles+executes under a READ-ONLY, tenant-scoped transaction
  // and returns the typed answer (columns + string rows + the echoed DSL +
  // sql_preview). Read-only: it NEVER writes. Money stays string.
  query(companyID: string, body: QueryRequest): Promise<QueryResult>;

  // ---- SaaS admin surface (backend/internal/server/handler_admin.go) -------
  /**
   * Who am I, and which companies may I open (GET /v1/me)? authn-ONLY: it
   * carries no company in the path, because an INVITED user is not yet a member
   * of anything — this call is what CLAIMS their pending invites and creates the
   * membership. It is also the company switcher's (F3) data source.
   */
  getMe(): Promise<MeResponse>;
  /** Members of a company + its PENDING invites (people granted access who have not signed in yet). */
  listMembers(companyID: string): Promise<MembersResponse>;
  /** Invite a SUBJECT at a security level. Creates an invite, never a user. */
  inviteMember(
    companyID: string,
    req: InviteMemberRequest,
  ): Promise<InviteMemberResponse>;
  /** Revoke a PENDING invite. Inert afterwards: it can never be claimed. */
  revokeInvite(
    companyID: string,
    inviteID: string,
  ): Promise<RevokeInviteResponse>;
  /**
   * Change a member's security level. Refused with 409 if it would demote the
   * LAST Owner — a company can never be left unadministrable.
   */
  updateMember(
    companyID: string,
    userID: string,
    req: UpdateMemberRequest,
  ): Promise<UpdateMemberResponse>;
  /** Revoke a member's access. Same last-Owner guard (409). Takes effect at once. */
  removeMember(
    companyID: string,
    userID: string,
  ): Promise<RemoveMemberResponse>;

  /** The company's security levels with their Disallow/Allow rules and member counts. */
  listSecurityLevels(companyID: string): Promise<SecurityLevelsResponse>;
  /**
   * The CLOSED sets a rule may name. Fetched rather than hardcoded so a rule
   * editor cannot compose a rule the server answers 422 — see
   * {@link SecurityVocabulary}.
   */
  getSecurityVocabulary(companyID: string): Promise<SecurityVocabulary>;
  /** Create a custom security level. Rules are validated against the vocabulary (422). */
  createSecurityLevel(
    companyID: string,
    req: SecurityLevelRequest,
  ): Promise<CreateSecurityLevelResponse>;
  /**
   * Patch a level; `rules`, when present, REPLACES the whole rule set. The Owner
   * level is immutable except its back-dated cutoff (422 otherwise).
   */
  updateSecurityLevel(
    companyID: string,
    levelID: string,
    req: SecurityLevelRequest,
  ): Promise<UpdateSecurityLevelResponse>;
  /** Delete an UNUSED custom level. A level that still has members is 409. */
  deleteSecurityLevel(
    companyID: string,
    levelID: string,
  ): Promise<DeleteSecurityLevelResponse>;

  // Migration import (Tally-XML ingress)
  importTallyXML(
    companyID: string,
    xml: string,
    query?: ImportTallyXMLQuery,
  ): Promise<ImportTallyXMLResponse>;
}
