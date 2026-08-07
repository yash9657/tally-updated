import type { NavSnapshot } from "../keystroke/navMachine";
import type { ReportName, VoucherType } from "../keystroke/types";

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
 * `payroll-register` and `payroll-payslip` also print server-side but have NO
 * screen in this build (payroll is server-only), so they are deliberately absent
 * rather than mapped to a screen that does not exist.
 *
 * ⚠️ NOT EVERY MAPPED REPORT ACTUALLY PRINTS. The slug being here means the
 * print route is REACHABLE and the ACL rule `print report:<slug>` can match it;
 * whether the server has a layout is its own answer. The TDS/TCS register is
 * mapped and answers 501 — there is no printed layout for it yet, and mapping it
 * is what makes Ctrl+P say so instead of doing nothing. An absent slug would be
 * indistinguishable from a key that quietly failed.
 */
const REPORT_SLUGS: Record<ReportName, string> = {
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
};

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
    return {
      kind: "report",
      slug,
      label: drilled
        ? `${name} (the whole statement, not this drill-down)`
        : name,
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
      return {
        kind: "refusal",
        reason:
          "Select a bank account first — a Bank Reconciliation Statement is always for one account, and printing one you did not choose would put the wrong account on paper.",
      };
    }
    return {
      kind: "report",
      slug: "bank-reconciliation",
      label: "Bank Reconciliation Statement",
      query: { ledger_id: voucher.bankLedgerID },
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
        "The Cost Centre Summary does not print in this build (the server answers 501 for it). A cost summary is always for ONE cost category, and the print route carries no way to say which — printing the first axis would put a real, correctly rendered Department report on paper for a question that was about Branches, with nothing on the page saying so. Alt+E is not built here either; read it on screen.",
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
