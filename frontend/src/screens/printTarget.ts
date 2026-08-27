import type { NavSnapshot } from "../keystroke/navMachine";
import type { FrameConfig, ReportName, VoucherType } from "../keystroke/types";
// GENERATED, NOT AUTHORED — see PRINTS_A_LAYOUT below. internal/server's
// TestPrintedReportLayoutsGolden emits this from the router that serves the
// requests, so the frontend cannot hold a stale opinion about what prints.
import printedLayouts from "../api/goldens/printed-report-layouts.json";

/**
 * WHAT DOES Ctrl+P PRINT? — the pure resolution, with no client and no React.
 *
 * Design memo: docs/design/t1t4-ui-model.md §1.1–1.3.
 *
 * T1 shipped a printing pipeline reachable over HTTP only. This module is the
 * decision half of the key that reaches it: given the active frame, it resolves
 * one of the two existing print routes, or a REFUSAL THAT NAMES ITS REASON.
 *
 * It is pure and total on purpose. Print has more ways to be inapplicable than
 * it has ways to work — seven of the thirteen voucher classes print no document
 * at all, an unsaved voucher has no id, and most reports are display-only — and
 * every one of those is a sentence an operator has to be able to read. Keeping
 * the decision here means those sentences are unit-testable without mounting a
 * screen or stubbing a fetch.
 */

/**
 * The six voucher classes that produce a document, from the server's own
 * `printingdb.docKindForClass` (internal/printing/printingdb/document.go:424).
 *
 * THIS TABLE IS A DUPLICATE OF A SERVER DECISION, AND THAT IS DELIBERATE — but
 * it is a duplicate that must not drift, because a class the client offers to
 * print and the server refuses is a 404 the operator cannot explain. Invariant
 * I-3 of the memo pins it from both sides: a test here enumerates these six, and
 * a Go test asserts docKindForClass accepts exactly these and refuses the other
 * seven.
 */
/**
 * ⛔ WHICH REPORTS ACTUALLY PRODUCE A DOCUMENT — READ FROM A GENERATED GOLDEN.
 *
 * GENERATED, NOT AUTHORED. `internal/server`'s `TestPrintedReportLayoutsGolden`
 * requests every name in `authz.KnownReportNames` and records the answer, so this
 * list is whatever the router actually does. Ten of the thirty print; the other
 * twenty answer an honest 501.
 *
 * ⛔ THE SLUG BEING IN `REPORT_SLUGS` BELOW DOES NOT MEAN IT PRINTS, and that
 * distinction is load-bearing rather than pedantic. A mapped slug means the route
 * is REACHABLE and `print report:<slug>` can be governed by an ACL rule — which
 * is exactly why the TDS/TCS register and both order books are mapped and answer
 * 501: so Ctrl+P SAYS the layout does not exist instead of doing nothing.
 *
 * For Ctrl+P that is the right behaviour and this list is not consulted. For
 * Alt+E-as-PDF it is not: an export FORMAT that fails after the operator has
 * chosen it is a menu item that lies, so the option is gated on this.
 */
const PRINTS_A_LAYOUT: ReadonlySet<string> = new Set(
  printedLayouts.prints_a_layout,
);

/**
 * Whether the server has a printed layout for a report slug.
 *
 * ⚠️ Ask this ONLY to decide whether to OFFER something. Never to decide whether
 * to send a request Ctrl+P would otherwise send: a 501 the operator asked for is
 * information, and suppressing it locally would turn a stated gap into a dead key.
 */
export function printsALayout(slug: string): boolean {
  return PRINTS_A_LAYOUT.has(slug);
}

/** Every slug with a printed layout, sorted. Exported for tests and the F12 note. */
export const SLUGS_THAT_PRINT: readonly string[] = printedLayouts.prints_a_layout;

/** Every slug whose print route answers an honest 501. */
export const SLUGS_THAT_DO_NOT_PRINT: readonly string[] = printedLayouts.answers_501;

export const PRINTABLE_VOUCHER_CLASSES: readonly VoucherType[] = [
  "sales",
  "credit-note",
  "debit-note",
  "receipt",
  "payment",
  "delivery-note",
] as const;

/**
 * Why each non-printable class prints nothing. These are REASONS, not apologies:
 * "a purchase bill is the supplier's document" tells an operator they are not
 * missing a feature, whereas "printing failed" sends them to support.
 *
 * The wording follows the server's own explanation in printingdb's doc comment,
 * so the two halves of the product give one answer.
 */
const NO_DOCUMENT_REASON: Partial<Record<VoucherType, string>> = {
  purchase:
    "A purchase bill is the SUPPLIER'S document, not ours — printing our own copy of it would be issuing a document nobody can rely on. File the bill you were given.",
  contra:
    "A contra moves money between your own accounts. Nothing is issued to anybody, so there is no document to print.",
  journal:
    "A journal is an internal adjustment. Nothing is issued to anybody, so there is no document to print.",
  "receipt-note":
    "Goods arrived on the SUPPLIER'S delivery challan — you received paper, you did not issue it. There is no document to print.",
  "stock-journal":
    "A stock journal transfers goods between your own godowns. Nothing leaves the business, so there is no document to print.",
  "physical-stock":
    "A physical stock count restates your own records. Nothing is issued to anybody, so there is no document to print.",
};

/**
 * Client report name -> the server's report slug.
 *
 * ⚠️ `payroll-register` AND `payroll-payslip` ARE NOT IN THIS TABLE, AND THE
 * REASON CHANGED. It used to be "payroll is server-only, so they are deliberately
 * absent rather than mapped to a screen that does not exist" — both now HAVE
 * screens. They are still absent because neither is a report NAME frame: this
 * table is keyed by `ReportName`, and both payroll screens are ScreenKinds whose
 * dimension is a wage MONTH (and, for the payslip, an EMPLOYEE) rather than the
 * From/To period a report frame carries. Both DO print — `printPayrollRegister`
 * and `printPayrollPayslip` are real layouts, not deferred 501s — and they are
 * resolved in the ScreenKind branches at the bottom of `resolvePrintTarget`,
 * where the month and the employee id are available to be sent.
 *
 * ⚠️ NOT EVERY MAPPED REPORT ACTUALLY PRINTS. The slug being here means the
 * print route is REACHABLE and the ACL rule `print report:<slug>` can match it;
 * whether the server has a layout is its own answer. The TDS/TCS register is
 * mapped and answers 501 — there is no printed layout for it yet, and mapping it
 * is what makes Ctrl+P say so instead of doing nothing. An absent slug would be
 * indistinguishable from a key that quietly failed.
 */
const REPORT_SLUGS: Record<ReportName, string> = {
  // ⛔ THE PAYABLES AGEING (D94). It is one of the few names here that DOES print —
  // the layout is `outstandings`' own, with the kind passed rather than parsed, so
  // `/print/reports/bills-payable` cannot be talked into printing the receivables
  // list by a `?kind=` on the URL.
  "Bills Payable": "bills-payable",
  "Balance Sheet": "balance-sheet",
  "Profit & Loss A/c": "profit-and-loss",
  "Trial Balance": "trial-balance",
  "Day Book": "day-book",
  Outstandings: "outstandings",
  "Stock Summary": "stock-summary",
  "TDS / TCS Register": "tds-register",
  // ---- THE TWO ORDER BOOKS (T3). MAPPED AND DEFERRED, exactly like the TDS/TCS
  // register above: the server keeps both names mounted and answers 501, so Ctrl+P
  // SAYS the printed layout does not exist rather than doing nothing.
  //
  // The server's reason (handler_print_reports.go) is worth knowing here, because it
  // is a layout decision and not an oversight: on our own stationery a printed order
  // book reads as a statement of what the company has contracted to do, but its
  // figures are outstanding quantities at the ORDER's agreed rate — which the
  // eventual invoice may lawfully differ from. A page that does not distinguish what
  // is agreed from what is expected is worse than a refusal.
  //
  // ⛔ TWO SLUGS, NEVER ONE WITH A ?direction=. The pair mirrors the two report
  // names, and mapping them separately is what lets `print report:sales-order-book`
  // be granted or refused on its own — migration 0073 refuses both.
  "Sales Order Book": "sales-order-book",
  "Purchase Order Book": "purchase-order-book",
  // ---- THE FORECAST PLANE. MAPPED AND DEFERRED, for the same reason as the two
  // books above: the server mounts both names and answers 501, so Ctrl+P says the
  // printed layout does not exist instead of doing nothing. REPORT_BAR advertises
  // "Ctrl+P Print" over every `report` frame, so an unmapped name here would ship a
  // button that silently fails on a screen that otherwise works.
  //
  // ⛔ AND FOR CASH FLOW THE 501 IS A LAYOUT DECISION WORTH KEEPING. Printed on the
  // company's own stationery, a bucket table of fixed-4 figures reads exactly like a
  // statement of account — while every date in it is INVENTED by the projection. The
  // screen can carry `estimate`, the `method` sentence and the whole basis block
  // beside the numbers; a page that loses them is a guess on letterhead. A printed
  // cash flow needs the disclosure designed INTO the layout, not appended to it.
  "Cash Flow": "cash-flow",
  Anomalies: "anomalies",
};

/**
 * WHICH BOUNDS OF THE F2 PERIOD A REPORT IS PRINTED FOR.
 *
 * ⛔ THE DEFECT THIS TABLE EXISTS TO CLOSE. Ctrl+P used to send the slug and
 * NOTHING ELSE. The server reads `from` / `to` / `as_on` off the query and
 * treats an absent one as "no bound" — so the paper came back subtitled "For all
 * dates in the books" or "As at the latest date in the books" while the screen
 * it was printed from showed one quarter. Every figure on it was real, which is
 * exactly what made it dangerous: a Trial Balance for the life of the company,
 * printed from a screen headed "As on 30-Jun-2026", is a document somebody signs.
 *
 *  - "as-at"       — a POSITION. Only the upper bound applies, and it is sent as
 *                    `as_on`. Bounding a position BELOW would drop the entity's
 *                    earlier history and still foot: a wrong answer that looks
 *                    right.
 *  - "full-period" — a FLOW between two dates. Both bounds apply, as `from`/`to`.
 *
 * ⛔ IT MUST NOT DRIFT FROM THE F2 OVERLAY'S OWN CLASSIFICATION. The period
 * dialog (screens/overlays/FrameOverlays.tsx) tells the operator which bounds a
 * report honours from AS_AT_ONLY_REPORTS / FULL_PERIOD_REPORTS, and each report
 * SCREEN reads the same fields when it fetches. Three lists that can disagree is
 * how a report comes to be told its "From" date does nothing while the printed
 * copy silently applies it — so printTarget.test.ts asserts this table is exactly
 * those two lists, restricted to the reports that print. This one is kept here,
 * rather than imported, because this module is pure and total by construction and
 * must not pull a React component's module graph in behind it.
 */
type ReportPeriodShape = "as-at" | "full-period";

const REPORT_PERIOD_SHAPE: Record<ReportName, ReportPeriodShape> = {
  // As-at, like Outstandings and the two order books: a bill is either still open
  // on the day asked about or it is not, and bounding it BELOW would drop the older
  // half of exactly the debt this report exists to show.
  "Bills Payable": "as-at",
  "Balance Sheet": "as-at",
  "Profit & Loss A/c": "full-period",
  "Trial Balance": "as-at",
  "Day Book": "full-period",
  Outstandings: "as-at",
  "Stock Summary": "as-at",
  "TDS / TCS Register": "full-period",
  "Sales Order Book": "as-at",
  "Purchase Order Book": "as-at",
  // ⛔ THE FORECAST PLANE SPLITS, AND THE SPLIT IS THE POINT.
  //
  // Cash Flow is "as-at": it takes ONE date and projects FORWARD from it. Sending a
  // `from` would ask the server to bound a projection below, which means dropping
  // the bills that are already overdue — the ones folded into the first bucket, and
  // the most certain cash in the whole answer.
  //
  // Anomalies is "full-period": both bounds apply, and they bound `created_at` —
  // WHEN A VOUCHER WAS ENTERED, not the period it accounts for. That is the only
  // axis on which "backdated" or "after hours" means anything, and it is why this
  // one report's F2 period is about the operator's working day rather than the
  // books' accounting period.
  "Cash Flow": "as-at",
  Anomalies: "full-period",
};

/**
 * The date query for one report, from the frame's F2 period.
 *
 * An UNSET bound is omitted rather than sent empty: the server's `reportDate`
 * treats a present-but-unparseable date as a 400 and an absent one as "no
 * bound", so sending `from=` would turn "the operator never set a period" into a
 * refusal on some routes and an open bound on others. Omitting it says the one
 * thing that is true.
 */
function reportPeriodQuery(
  name: ReportName,
  config: FrameConfig | undefined,
): Record<string, string> {
  const from = config?.periodFrom?.trim() ?? "";
  const to = config?.periodTo?.trim() ?? "";
  const query: Record<string, string> = {};
  if (REPORT_PERIOD_SHAPE[name] === "as-at") {
    if (to !== "") query.as_on = to;
    return query;
  }
  if (from !== "") query.from = from;
  if (to !== "") query.to = to;
  return query;
}

/**
 * How the period reads on the STATUS LINE, so the operator can see what went to
 * paper without opening the PDF. "" when the frame carries no period at all —
 * which is itself worth saying, hence the caller's "all dates" wording.
 */
function periodLabel(query: Record<string, string>): string {
  if (query.as_on) return `as on ${query.as_on}`;
  if (query.from && query.to) return `${query.from} to ${query.to}`;
  if (query.from) return `from ${query.from}`;
  if (query.to) return `up to ${query.to}`;
  return "";
}

/** A resolved print destination, or a refusal carrying the reason to display. */
export type PrintTarget =
  | {
      kind: "voucher";
      /** The kebab-case class, which is also the URL segment. */
      voucherClass: VoucherType;
      voucherID: string;
      /** A human label for the status line, e.g. "Sales invoice". */
      label: string;
    }
  | {
      kind: "report";
      slug: string;
      label: string;
      /**
       * Query parameters the report needs to be unambiguous. Only
       * bank-reconciliation uses this today: it identifies WHICH bank account,
       * and the server answers 400 without it rather than picking one.
       */
      query?: Record<string, string>;
    }
  | { kind: "refusal"; reason: string };

/**
 * What the caller knows about the voucher on screen, if any.
 *
 * `voucherID` is present in two situations and the distinction matters for the
 * message, not for the outcome: an ALTERATION (drilled from the Day Book, id
 * from LoadedVoucher) and a voucher JUST SAVED in create mode (id from the
 * create response — memo §2.1, which exists precisely so that "print the invoice
 * from the screen you keyed it on" works).
 */
export interface PrintVoucherContext {
  voucherID: string | null;
  /** True when the id came from a create-accept rather than a drill. */
  justSaved?: boolean;
  /**
   * The bank ledger the reconciliation screen currently has selected, if any.
   *
   * It lives here rather than on NavSnapshot because it is SCREEN STATE, not
   * navigation state: the nav machine has no business knowing which of three
   * bank accounts is on screen, and putting it there would make every screen's
   * snapshot carry a field only one screen sets.
   */
  bankLedgerID?: string | null;
  /**
   * The NAME of that bank ledger, for the status line.
   *
   * It exists because the reconciliation screen AUTO-SELECTS when the company
   * has exactly one bank account, so the operator can print an account they were
   * never asked about. Naming it in the confirmation is what keeps that
   * convenience from being silent — see the bank-reconciliation branch.
   */
  bankLedgerName?: string | null;
  /**
   * How many bank accounts this company has, when the screen knows.
   *
   * ZERO IS NOT THE SAME REFUSAL AS "you have not picked one yet", and only the
   * screen can tell them apart: `undefined` means the list has not arrived.
   */
  bankAccountCount?: number;
  /**
   * The WAGE MONTH ("YYYY-MM") the payroll register or payslip is showing.
   *
   * It lives here rather than on NavSnapshot's config for the same reason
   * `bankLedgerID` does — it is what the SCREEN has actually loaded, which is not
   * always what the frame asked for: a month typed but not yet fetched, or one
   * the server refused, must not reach the print route as though it had produced
   * the page on screen. Printing "2026-13" because it was typed would produce a
   * 400 the operator cannot connect to anything they can see.
   */
  payrollMonth?: string | null;
  /** The employee whose payslip is on screen, and their name for the status line. */
  payrollEmployeeID?: string | null;
  payrollEmployeeName?: string | null;
}

function titleCase(cls: VoucherType): string {
  return cls
    .split("-")
    .map((w) => w[0]?.toUpperCase() + w.slice(1))
    .join(" ");
}

/**
 * Resolve what Ctrl+P prints on the active screen.
 *
 * Total by construction: every branch returns either a route or a sentence. A
 * `refusal` must NEVER be turned into an HTTP call by the caller — invariant I-5
 * asserts zero fetches on the unsaved-voucher path, because a refusal that still
 * hits the network is a 404 in somebody's log.
 */
export function resolvePrintTarget(
  snapshot: NavSnapshot,
  voucher: PrintVoucherContext = { voucherID: null },
): PrintTarget {
  const { screen, voucherType, reportName, parentReportName } = snapshot;

  if (screen === "voucher") {
    if (!voucherType) {
      return {
        kind: "refusal",
        reason: "There is no voucher on this screen to print.",
      };
    }
    // The class check comes FIRST, before the "is it saved" check. A Contra
    // voucher does not become printable by being saved, and telling the
    // operator to save it first would be a lie they act on.
    if (!PRINTABLE_VOUCHER_CLASSES.includes(voucherType)) {
      return {
        kind: "refusal",
        reason:
          NO_DOCUMENT_REASON[voucherType] ??
          `A ${titleCase(voucherType)} voucher has no printable document.`,
      };
    }
    if (!voucher.voucherID) {
      return {
        kind: "refusal",
        reason:
          "Nothing to print yet — this voucher has not been saved. Press Ctrl+A to save it, then Ctrl+P prints it.",
      };
    }
    return {
      kind: "voucher",
      voucherClass: voucherType,
      voucherID: voucher.voucherID,
      label: `${titleCase(voucherType)}${voucher.justSaved ? " (just saved)" : ""}`,
    };
  }

  if (screen === "report" || screen === "report-detail") {
    // A drill-down prints its PARENT report, because that is the only thing the
    // route offers. Saying so is the point — an operator who drilled into one
    // party's bills and got the whole Outstandings statement should know why.
    const name = reportName ?? parentReportName;
    if (!name) {
      return {
        kind: "refusal",
        reason: "There is no report on this screen to print.",
      };
    }
    const slug = REPORT_SLUGS[name as ReportName];
    if (!slug) {
      return {
        kind: "refusal",
        reason: `${name} cannot be printed in this build — it is a display-only report. The server answers 501 for it rather than printing a blank page.`,
      };
    }
    const drilled = screen === "report-detail";
    // THE PERIOD IS PART OF THE REQUEST, NOT DECORATION. See
    // REPORT_PERIOD_SHAPE: without it the server prints the whole book under the
    // screen's heading.
    const query = reportPeriodQuery(name as ReportName, snapshot.config);
    const period = periodLabel(query);
    // ⚠️ A DRILL-DOWN FRAME CARRIES NO PERIOD OF ITS OWN. `reduceKey` pushes
    // report-detail as a bare frame (only the cost-category drill copies the
    // period), and ReportDetailScreen itself fetches with no dates — so the
    // level on screen really is all-dates and printing it that way is honest.
    // The label says which it is either way, because "Printed Day Book" over a
    // page covering a different span than the screen is the whole defect.
    const scope = period !== "" ? period : "all dates in the books";
    return {
      kind: "report",
      slug,
      label: drilled
        ? `${name} (the whole statement, not this drill-down) — ${scope}`
        : `${name} — ${scope}`,
      ...(Object.keys(query).length > 0 ? { query } : {}),
    };
  }

  if (screen === "bank-reconciliation") {
    // Memo §1.5 / invariant I-10, now CLOSED. The route shape was the whole
    // blocker: /print/reports/{name} carried no ledger id, and a BRS is always
    // for ONE bank account. HANDOFF-PARITY-SPRINT.md §3.4 took decision (c) —
    // ?ledger_id= for one account, ?all=true for every account.
    //
    // The refusal survives for the case it was actually about. An operator who
    // has not yet picked an account has not said which statement they want, and
    // printing "the first one" would put a real, correctly rendered statement
    // for the WRONG ACCOUNT on paper with nothing on the page saying so.
    if (!voucher.bankLedgerID) {
      // ⛔ AND IT SAYS WHICH OF THE TWO SILENCES THIS IS. A company with NO bank
      // accounts cannot "select one first", and telling its operator to do that
      // sends them looking for a picker that will never have a row in it — the
      // server's own answer on that route is a 404 naming the group a ledger has
      // to sit under, and the key should not be less informative than the wire.
      if (voucher.bankAccountCount === 0) {
        return {
          kind: "refusal",
          reason:
            "This company has no bank accounts, so there is no Bank Reconciliation Statement to print. A ledger is a bank account when its group sits under Bank Accounts or Bank OD A/c — create one and its postings appear here.",
        };
      }
      return {
        kind: "refusal",
        reason:
          "Select a bank account first (F4) — a Bank Reconciliation Statement is always for one account, and printing one you did not choose would put the wrong account on paper.",
      };
    }
    // ⛔ THE ACCOUNT IS NAMED ON THE STATUS LINE, AND THAT IS THE POINT OF THE
    // NAME BEING HERE. The refusal above was written to stop a statement for the
    // WRONG account reaching paper — but the screen auto-selects when the company
    // has exactly one bank account, so on that (very common) company Ctrl+P as the
    // FIRST key prints an account the operator never chose. That choice is right —
    // making somebody pick from a list of one is friction — so the fix is not to
    // re-arm the refusal but to make the outcome VISIBLE: the operator reads which
    // account went to paper without opening the PDF, and can tell at once that it
    // is not the one they meant.
    //
    // AND THE DATE. `as_on` is the F2 upper bound, exactly as the screen reads it
    // (BankReconciliationScreen: `const asOn = snapshot.config?.periodTo`). Without
    // it the printed statement is "as at the latest date in the books" while the
    // screen says "As at 30-Jun-2026" — the same defect as every other report, and
    // worse here, because a BRS is a document a bank manager is shown.
    const asOn = snapshot.config?.periodTo?.trim() ?? "";
    const account = voucher.bankLedgerName?.trim();
    return {
      kind: "report",
      slug: "bank-reconciliation",
      label:
        `Bank Reconciliation Statement — ${account && account !== "" ? account : "the selected account"}` +
        (asOn !== "" ? ` as on ${asOn}` : ""),
      query: {
        ledger_id: voucher.bankLedgerID,
        ...(asOn !== "" ? { as_on: asOn } : {}),
      },
    };
  }

  // ---- THE THREE T2 COST SCREENS. Each is refused with its OWN reason, and none
  // of them reaches the network: `report:cost-centre-summary` and
  // `report:cost-category-summary` are both mapped in authz.KnownReportNames and
  // both answer 501 from /print/reports/{name} (backend handler_print_reports.go
  // lists them in the deferred set), so a request could only ever come back as
  // that 501 — and the refusals below say the same thing without a round trip that
  // ends in somebody's log.
  //
  // WHY NOT MAP THEM TO SLUGS the way the TDS register is mapped: mapping earns an
  // absent layout the server's own sentence, which is the right trade when the
  // route CAN express the request. It cannot here. /print/reports/{name} is a flat
  // literal carrying no axis, and the Cost Centre Summary is always for ONE axis —
  // the same shape problem the Bank Reconciliation Statement had before
  // ?ledger_id= (invariant I-10), and the same failure if it were fudged: a real,
  // correctly rendered Department report on paper for a company whose question was
  // about Branches, with nothing on the page saying so.
  if (screen === "cost-centre-summary") {
    return {
      kind: "refusal",
      reason:
        // ⛔ THE LAST SENTENCE USED TO READ "Alt+E is not built here either; read
        // it on screen", AND IT WAS FALSE. Alt+E on this very screen writes
        // Cost-Centre-Summary-<axis>-<from>-to-<to>.csv immediately, with the
        // figures at 4dp and the AXIS in the file name — the export was built (T15)
        // and this sentence was never re-read. A refusal that misstates what the
        // OTHER key does is worse than a bare refusal: it sends an operator who
        // needs these figures away from the one way out of the screen that works.
        "The Cost Centre Summary does not print in this build (the server answers 501 for it). A cost summary is always for ONE cost category, and the print route carries no way to say which — printing the first axis would put a real, correctly rendered Department report on paper for a question that was about Branches, with nothing on the page saying so. Alt+E DOES work here: it writes this axis's figures to a spreadsheet, with the axis in the file name so the file cannot be read under the wrong heading.",
    };
  }
  if (screen === "cost-category-summary") {
    return {
      kind: "refusal",
      reason:
        "The Cost Category Summary does not print in this build (the server answers 501 for it — the layout is unwritten, not the report). Note what a printed copy would have to carry that a screen can enforce: the rows must NEVER be added together, because each is a different axis through the same money.",
    };
  }
  if (screen === "cost-centres") {
    return {
      kind: "refusal",
      reason:
        "There is nothing to print here — this is a list of masters, and the print route serves REPORTS. What each of these centres COST is the Cost Centre Summary; how much of the period's cost each axis accounts for is the Cost Category Summary. Neither of those prints yet either.",
    };
  }

  // ---- THE FIVE PAYROLL SCREENS. Two of them PRINT — and they are the only
  // screens added in this wave that do, which is a fact about the server and not a
  // preference: handler_print_reports.go carries a real `printPayrollRegister` and
  // `printPayrollPayslip` layout, with the RULESET VERSIONS laid out on the page,
  // while every other payroll name is in the deferred set.
  //
  // ⛔ THE MONTH IS PART OF THE REQUEST, NOT DECORATION — the same defect the
  // REPORT_PERIOD_SHAPE table exists to close, and worse here. The route reads
  // `month` and refuses without it, so an unsent month is a 400; and a payslip
  // printed for the wrong month is one person's pay, correctly rendered, for a
  // period nobody asked about, on a document that person is handed.
  if (screen === "payroll-register") {
    const month = voucher.payrollMonth?.trim() ?? "";
    if (month === "") {
      // No round trip. The register on screen has no month yet, so there is no
      // document to ask for — and a request with no month is a 400 in somebody's
      // log rather than an answer.
      return {
        kind: "refusal",
        reason:
          "Type the wage month (YYYY-MM) first — a payroll register is always for ONE month, and the print route refuses without it rather than printing the whole year under a monthly heading.",
      };
    }
    return {
      kind: "report",
      slug: "payroll-register",
      label: `Payroll Register — ${month}`,
      query: { month },
    };
  }

  if (screen === "payroll-payslip") {
    const month = voucher.payrollMonth?.trim() ?? "";
    const employeeID = voucher.payrollEmployeeID?.trim() ?? "";
    if (month === "" || employeeID === "") {
      return {
        kind: "refusal",
        reason:
          "There is no payslip on this screen yet. A payslip is ONE person's pay for ONE wage month, so both are needed — open it from the Payroll Register (Enter on a row), which carries both with it.",
      };
    }
    // ⛔ THE NAME IS ON THE STATUS LINE, and it is here for the reason the bank
    // account's name is on the reconciliation's: the operator reads WHO went to
    // paper without opening the PDF. On this screen that matters more than
    // anywhere else in the product — the failure it catches is handing one
    // employee another employee's salary.
    const who = voucher.payrollEmployeeName?.trim();
    return {
      kind: "report",
      slug: "payroll-payslip",
      label:
        `Payslip — ${who && who !== "" ? who : "the selected employee"} for ${month}`,
      query: { month, employee_id: employeeID },
    };
  }

  if (screen === "employees") {
    return {
      kind: "refusal",
      reason:
        "There is nothing to print here — this is a list of PEOPLE, and the print route serves reports. What each of them was paid is the Payroll Register (Alt+G > Payroll Register), which prints; one person's own document is their Payslip, which prints from the register with Enter then Ctrl+P.",
    };
  }

  if (screen === "payroll-run") {
    return {
      kind: "refusal",
      reason:
        "A pay run has no printed document of its own — it is the ACT of computing and posting a wage month, not a statement. What it produced is the Payroll Register for that month, and that prints (Alt+G > Payroll Register, then Ctrl+P). Each person's own copy is their payslip.",
    };
  }

  if (screen === "payroll-form-138") {
    return {
      kind: "refusal",
      reason:
        "Form 138 is not printed, it is DOWNLOADED — press Ctrl+A. It is a FILE the department's utility reads, not a page: the statement must begin with its FH record, and a PDF of it would be a document no portal can accept and no human is meant to read. ⛔ And note what the download says about itself: nothing here has been through the department's FVU, which needs a Java runtime this environment does not have, so the archive carries a README stating that — and the file and that statement cannot be saved apart.",
    };
  }

  if (screen === "e-invoice") {
    return {
      kind: "refusal",
      reason:
        "The e-invoice does not print in this build, and that is a DECISION rather than an unwritten layout — the same one the e-way bill makes, for a sharper reason. A printed tax invoice carrying an IRN is the document a buyer files their input credit against, and what this build can produce is a payload plus a reference minted by a stand-in provider because no IRP credentials exist (HANDOFF §10 item 4). Printing it would hand a buyer a document that looks filed and is not, and the buyer would find out from the department rather than from us. The screen shows the reference and its disclosure instead. ⛔ Note that the INVOICE ITSELF still prints from the voucher — it is only the e-invoice artefact that does not, and that is the right split: the sale is real, the filing is not. The day real IRP credentials exist, the printed form with its Rule 48(4) QR code is the change to make — and the QR code specifically is NOT shippable from here at all, because its payload is signed with the Income Tax Department's private key (HANDOFF §5.3).",
    };
  }

  if (screen === "e-way-bill") {
    return {
      kind: "refusal",
      reason:
        "The e-way bill does not print in this build, and that is a DECISION rather than an unwritten layout. A printed sheet is exactly the artefact a driver hands to a checkpost, and what this build can produce is a payload plus a LOCAL reference that no government issued. Printing it would put a document that looks like an e-way bill into the one situation where being wrong is a detained vehicle. The screen shows the payload and the full disclosure block instead; the day real NIC credentials exist (HANDOFF §10 item 4), the printed form is the change to make.",
    };
  }

  return {
    kind: "refusal",
    reason: "Nothing on this screen can be printed.",
  };
}
