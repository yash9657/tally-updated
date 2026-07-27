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
  ListLedgersResponse,
  SetLedgerOpeningRequest,
  SetLedgerOpeningResponse,
  VoucherHistoryResponse,
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
