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
