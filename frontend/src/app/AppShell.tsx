import { useCallback, useEffect, useRef, useState } from "react";
import { useNavMachine } from "../keystroke/useNavMachine";
import { buttonBarFor } from "../keystroke/buttonBar";
import type { NavSnapshot } from "../keystroke/navMachine";
import {
  actionLabel,
  resolveAction,
  resolveKeymap,
} from "../keystroke/keymaps/tallyprime";
import { LEDGER_WISE_DEFAULT, type KeymapAction } from "../keystroke/types";
import { comboFromEvent } from "../keystroke/normalize";
import { useCompanyContext } from "./CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
  type ScreenActionKind,
} from "./ScreenActions";
import { LoadedVoucherProvider } from "./LoadedVoucher";
import { VOUCHER_CONFIGS } from "../screens/vouchers/voucherConfig";
import { LoadedDraftProvider } from "./LoadedDraft";
import { Chrome } from "./Chrome";
import { ScreenRegistry } from "./screenRegistry";
import { createDefaultRegistry } from "./defaultScreens";
import { FrameOverlayHost } from "../screens/overlays/FrameOverlays";

/** Screens that require an active company scope (useCompanyID would throw). */
const COMPANY_SCOPED_SCREENS = new Set<NavSnapshot["screen"]>([
  "voucher",
  "report",
  "report-detail",
  "master-create",
  // The Bank Reconciliation Statement reads a company's bank ledgers, so it
  // needs a scope. Omitting it here would let the screen mount without one and
  // useCompanyID() would THROW rather than render the "select a company" state.
  "bank-reconciliation",
  // The TDS challan register reads and writes ONE company's deposits, so it
  // needs a scope. Omitting it does not degrade gracefully: useCompanyID()
  // THROWS, and the screen never gets to render its own "no company" state.
  "tds-challans",
  // The three T2 COST screens. All three read (and the masters screen writes)
  // ONE company's cost masters and allocations, so all three need a scope. Same
  // failure mode as the two above: omit one and useCompanyID() THROWS before the
  // screen can render its own state — including its 403 panel, which is a NORMAL
  // answer on every one of them (migration 0072 refuses both reports to the
  // seeded Data Entry level, and the masters sit behind a bare `cost-centre`
  // token against a level whose default_access is 'none').
  "cost-centres",
  "cost-centre-summary",
  "cost-category-summary",
  // The three T3 screens. Same failure mode as every screen above: all three read
  // (and all three write) ONE company's orders or recipes, so omitting one means
  // useCompanyID() THROWS before the screen can render its own state — including
  // its 403 panel, which is a NORMAL answer on the two registers (a level may be
  // allowed to commit us to customers and refused to commit us to suppliers, which
  // is why they are two authorities in the first place).
  "sales-orders",
  "purchase-orders",
  "boms",
  // T5. The e-way bill screen reads ONE company's vouchers and writes that
  // company's bills, so it needs a scope. Same failure mode as every screen
  // above: omit it and useCompanyID() THROWS before the screen can render its
  // own state — including its 403 panel, which is a NORMAL answer here (the
  // bare `e-way-bill` token is denied to a level whose default_access is 'none').
  "e-way-bill",
  // §1.4 B. The e-invoice screen reads ONE company's sales invoices and reports
  // one of them, so it needs a scope. Identical failure mode to every screen
  // above: omit it and useCompanyID() THROWS before the screen can render its
  // own state — including its 403 panel, which is a NORMAL answer here (the bare
  // `e-invoice` token is denied to a level whose default_access is 'none', so
  // Data Entry cannot raise or see one until an administrator grants it).
  "e-invoice",
  // THE FIVE PAYROLL SCREENS. All five read (and three of them write) ONE
  // company's employees, pay runs and statutory files, so all five need a scope.
  // Same failure mode as every screen above: omit one and useCompanyID() THROWS
  // before the screen can render its own state — including its 403 panel, which
  // is a NORMAL answer on every one of them (migration 0035 disallows
  // create/alter/display of the bare `employee` token for the seeded Data Entry
  // level, and refuses all five payroll report names to it as well).
  "employees",
  "payroll-run",
  "payroll-register",
  "payroll-payslip",
  "payroll-form-138",
  // The EDIT LOG reads ONE company's voucher versions, so it needs a scope for
  // the same reason as every screen above: omit it and useCompanyID() THROWS
  // before the screen can render anything, including its own empty state.
  //
  // ⚠️ THIS WAS LATENT, NOT BROKEN: the log is reached by Alt+L FROM a voucher,
  // which is already company-scoped, so the missing entry could not bite while
  // that was the only route in. It would have bitten the moment the screen got a
  // second entry point — a Go To row, or a deep link — which is exactly the kind
  // of change nobody expects to break an unrelated screen.
  "edit-log",
  // T14 — the DATA screen (Alt+Y). It backs up ONE company, so it needs a scope
  // for the same reason every screen above does: omit it and useCompanyID()
  // THROWS before the screen can render anything, including the sentence
  // explaining that a restore creates a new company rather than replacing one.
  "data",
  // T32 — MIGRATE FROM TALLY (Alt+O). It imports INTO one company, so it needs a
  // scope for the same reason every screen above does: omit it and
  // useCompanyID() THROWS before the screen can render anything, including the
  // line naming the company the export is about to be posted into.
  "tally-import",
  // PRINT TEMPLATES (track C). Every route it drives is company-scoped, so it
  // needs a scope for the same reason every screen above does: omit it and
  // useCompanyID() THROWS during render, taking the whole shell down before the
  // screen can show anything — including the line naming which company's
  // documents are being edited.
  //
  // ⛔ NO TEST IN THIS REPOSITORY COULD SEE THIS ENTRY BEFORE ONE WAS WRITTEN FOR
  // IT. This set is consulted ONLY when company === null, and every screen test
  // passes an initialCompany, so deleting an entry here breaks nothing in the
  // suite. PrintTemplateScreen.test.tsx has the company-less case; it is the
  // second screen in the set to have one.
  "print-templates",
]);

/**
 * Keymap actions whose real work belongs to the ACTIVE SCREEN (it owns the
 * client + the form state), not to the pure navigation machine. The shell fires
 * the registered handler with the PRE-transition snapshot, then lets the
 * machine move the stack.
 */
const SCREEN_ACTIONS: Partial<Record<KeymapAction["kind"], ScreenActionKind>> = {
  accept: "accept",
  delete: "delete",
  exportScreen: "export",
  cancelVoucher: "cancelVoucher",
  duplicate: "duplicate",
  prevVoucher: "prevVoucher",
  nextVoucher: "nextVoucher",
  print: "print",
  // Alt+M — E-MAIL THIS DOCUMENT (B2).
  //
  // ⛔⛔ THIS LINE IS THE ONE THAT SHIPS THE KEY DEAD. Everything else can be
  // present and correct — the keymap binding, the KeymapAction, the
  // ScreenActionKind, the button-bar item and the screen's own
  // useEmailDocumentAction — and Alt+M STILL does nothing, silently, because the
  // shell never dispatches it. Read the comment on `importBankStatement` below:
  // that is exactly how Alt+O shipped, with every unit test passing because they
  // call the dispatcher directly and bypass this map.
  emailDocument: "emailDocument",
  bankReconciliation: "bankReconciliation",
  // Alt+O on the reconciliation screen — statement IMPORT (T4 slice 2a).
  //
  // ⚠️ THIS MAP IS A REGISTRATION POINT AND IT IS NOT IN §5.2's LIST OF TWELVE.
  // Everything else can be present — the keymap binding, the ScreenActionKind,
  // the navMachine case, the button-bar item, and the screen's
  // useRegisterScreenAction — and the key STILL does nothing, silently, because
  // the shell never dispatches it. That is exactly how Alt+O shipped: every
  // unit test passed (they call the dispatcher directly and bypass this map),
  // buttons.test.ts passed (it checks the ACTION resolves, not that the shell
  // routes it), and the failure only appeared when the app was driven in a real
  // browser. Add the entry here whenever you add a screen action.
  importBankStatement: "importBankStatement",
  // Alt+C on the TDS challan register — RECORD A DEPOSIT (T6).
  //
  // ⛔ THIS IS THE LINE THE COMMENT ABOVE IS ABOUT. Alt+O shipped completely
  // dead with its keymap binding, its ScreenActionKind, its navMachine case, its
  // bar item and its screen handler ALL present and correct, because this map had
  // no entry: the shell resolved the combo, found no screen action and forwarded
  // the key to a machine that deliberately does nothing. Silently.
  recordTDSChallan: "recordTDSChallan",
  // Alt+A on the same register — ATTACH to a deposit already recorded (D1).
  // Same registration point and the same silent failure mode as the line above:
  // without this entry the key resolves, finds no screen action, and is
  // forwarded to a machine case that deliberately does nothing.
  allocateTDSChallan: "allocateTDSChallan",
  // Alt+C on the COST MASTERS screen — create an axis or a centre (T2). Same
  // registration point, same failure mode: without this line the panel never
  // opens and NOTHING says so, while the keymap binding, the ScreenActionKind,
  // the navMachine case, the bar item and the screen's own handler all look
  // correct in review.
  createCostMaster: "createCostMaster",
  // F4 on the COST CENTRE SUMMARY — choose the axis the report is for (T2).
  // Ditto: a Cost Centre Summary with no axis chosen renders a picker, and
  // without this entry F4 could never reopen it.
  selectCostCategory: "selectCostCategory",
  // ---- THE FOUR T3 SCREEN ACTIONS (Alt+C / Alt+W on the two order registers,
  // Alt+C / Alt+X on the BOM screen).
  //
  // ⛔ THIS IS THE LINE-FOR-LINE REPEAT OF THE Alt+O LESSON ABOVE, four times
  // over. Every other registration for these keys can be present and correct —
  // the keymap binding, the KeymapAction, the ScreenActionKind, the navMachine
  // case, the button-bar item and the screen's own useRegisterScreenAction — and
  // the key still does NOTHING, silently, because the shell resolves the combo,
  // finds no screen action, and forwards it to a machine that deliberately does
  // not move. There is no error and no status line. Unit tests do not catch it
  // because they call the dispatcher directly and bypass this map.
  createOrder: "createOrder",
  closeOrder: "closeOrder",
  createBom: "createBom",
  explodeBom: "explodeBom",
  // ---- THE THREE PAYROLL SCREEN ACTIONS (Alt+C / Alt+S on the employee master,
  // Alt+S on the payroll register).
  //
  // ⛔ THE Alt+O LESSON AGAIN, and one of these three is the worst place yet to
  // repeat it. `setEmployeePay` is the ONLY key that gives an employee a salary
  // structure and a statutory election, and without both cored refuses the entire
  // pay run — on the RUN, days later, naming a person. A silently dead Alt+S here
  // would look exactly like a complete employee master right up to pay day.
  createEmployee: "createEmployee",
  setEmployeePay: "setEmployeePay",
  payrollStatutoryFiles: "payrollStatutoryFiles",
  // Alt+V on the Stock Summary — the COSTING METHOD (T9). Without this line the
  // panel never opens and NOTHING says so: the keymap binding, the
  // ScreenActionKind, the navMachine case and the screen's own handler all look
  // correct in review. That is the Alt+O lesson, and this key is the only way an
  // operator can re-value an item created before T9 — which is every item in
  // every existing company.
  setValuationMethod: "setValuationMethod",
};

/** How long a status-line message stays up before it fades (ms). */
const STATUS_TTL_MS = 6000;

/**
 * Actions that are meaningful even when the machine's stack comes out
 * IDENTICAL. Pressing F8 inside a Sales voucher rebuilds the same frame — a
 * byte-identical stack — but it did the Tally thing (a fresh Sales voucher), so
 * it must not be reported as "does not apply on this screen".
 */
const ALWAYS_MEANINGFUL = new Set<KeymapAction["kind"]>([
  "voucher",
  // Ctrl+F8 / Ctrl+F9 on the register they already open. The machine's pushOnce
  // deliberately does not stack a second identical frame, so the stack comes out
  // byte-identical — but the operator asked for the sales order register and is
  // looking at the sales order register, so "does not apply on this screen" would
  // be a false statement about a key that did exactly what it says.
  "orderRegister",
  // Alt+Z on the e-way bill screen it already opens. Same pushOnce reasoning
  // as orderRegister above: the operator asked for the e-way bill screen and is
  // looking at it, so "does not apply on this screen" would be a false statement.
  "eWayBill",
  // Alt+I on the e-invoice screen it already opens — same pushOnce reasoning.
  "eInvoice",
  // Ctrl+F4 on the pay run screen it already opens — same pushOnce reasoning as
  // orderRegister above.
  "payRun",
  "date",
  "help",
  "features",
  "configure",
  "calculator",
  // ⛔ TAB AND SHIFT+TAB, AND THEY ARE HERE FOR A REASON THAT IS NOT THE ONES
  // ABOVE. The others are keys whose effect the stack signature cannot see. These
  // two are keys whose effect THIS APP DOES NOT PRODUCE AT ALL: the machine's
  // nextField/prevField return {} on every screen that is not a voucher — a
  // report, the Gateway, Users & Roles, the Bill Inbox — while the BROWSER moves
  // focus perfectly well, because useNavMachine no longer preventDefaults them.
  //
  // Without this entry the operator presses Tab, watches the focus ring move, and
  // is told "Tab — Next field does not apply on this screen". A status line that
  // contradicts what the screen just did is worse than an empty one: it teaches
  // the operator to stop believing the status line, which is the only channel a
  // keystroke product has for saying anything at all.
  "nextField",
  "prevField",
]);

/**
 * The application shell. Provides the screen-action bridge and the loaded-voucher
 * store (so it is renderable standalone in tests), then delegates to the inner
 * shell which owns the navigation machine + chrome. The providers wrap the inner
 * shell because the inner shell CONSUMES the screen-action dispatcher.
 */
export function AppShell({
  registry = createDefaultRegistry(),
}: {
  registry?: ScreenRegistry;
}) {
  return (
    <ScreenActionsProvider>
      <LoadedVoucherProvider>
        <LoadedDraftProvider>
          <AppShellInner registry={registry} />
        </LoadedDraftProvider>
      </LoadedVoucherProvider>
    </ScreenActionsProvider>
  );
}

function AppShellInner({
  registry = createDefaultRegistry(),
}: {
  registry?: ScreenRegistry;
}) {
  const { company } = useCompanyContext();
  const { snapshot, onKeyDown, onSelect, onConfig, stackSignature } = useNavMachine({
    startScreen: "gateway",
    keymapProfile: "tallyprime",
  });
  const rootRef = useRef<HTMLDivElement>(null);
  const dispatchScreenAction = useScreenActionDispatcher();
  /** The transient status line: what the last key press actually did. */
  const [status, setStatus] = useState<string | null>(null);

  // Keep the live snapshot in a ref so the DOCUMENT-level key listener (bound
  // once) always reads the current frame without being re-bound per keystroke.
  const snapshotRef = useRef(snapshot);
  snapshotRef.current = snapshot;

  const showStatus = useCallback((message: string | null) => {
    setStatus(message);
  }, []);

  // Auto-expire the status line so it reads as feedback, not as chrome.
  useEffect(() => {
    if (status === null) return;
    const t = window.setTimeout(() => setStatus(null), STATUS_TTL_MS);
    return () => window.clearTimeout(t);
  }, [status]);

  /**
   * The single key handler. It (1) resolves the combo against the keymap FOR
   * THE ACTIVE SCREEN, (2) fires the screen-owned side effect (if any) with the
   * pre-transition snapshot — before the machine's reducer resets the voucher
   * frame and wipes the captured number — (3) forwards the key to the machine,
   * and (4) makes sure the user is TOLD what happened: an unbuilt feature says
   * so, and a key that does nothing on this screen says that too. A silently
   * dead key is the thing a Tally operator cannot forgive.
   */
  const handleKeyDown = useCallback(
    (ev: React.KeyboardEvent | KeyboardEvent) => {
      const snap = snapshotRef.current;
      const combo = comboFromEvent({
        key: ev.key,
        // ⛔ `code` IS NOT OPTIONAL IN PRACTICE — dropping it here is what made
        // every Alt+<letter> dead on macOS, because Option composes the
        // character and `key` becomes "¬"/"©"/"ç". See KeyEventLike.code.
        code: ev.code,
        ctrlKey: ev.ctrlKey,
        altKey: ev.altKey,
        shiftKey: ev.shiftKey,
        metaKey: ev.metaKey,
      });
      const action = resolveAction(
        resolveKeymap("tallyprime"),
        snap.screen,
        combo,
      );

      // Unbound key (ordinary typing, arrows inside a list, …): not ours.
      if (!action) return;

      if (action.kind === "unavailable") {
        ev.preventDefault();
        showStatus(`${action.feature} — ${action.note}`);
        return;
      }

      const screenActionKind = SCREEN_ACTIONS[action.kind];
      const effect = screenActionKind
        ? dispatchScreenAction(screenActionKind, snap)
        : { ran: false as const };

      // ⛔ A REFUSED ACTION DOES NOT REACH THE MACHINE (register row D92).
      // The machine's `accept` reducer pops a master-create frame
      // unconditionally — it was written when the only way to reach that
      // reducer was a successful create — so a refusal used to throw the
      // operator back to the Gateway and DISCARD everything they had keyed,
      // then explain why on a screen they could no longer act on. Measured in
      // a browser before this line existed: name a ledger, leave the group
      // blank, Ctrl+A, and the form is gone along with the name.
      //
      // The key is still CONSUMED (preventDefault below) — this is not "the
      // key did nothing", it is "the screen answered and the answer was no".
      // Only a handler that returns `screenRefusal(...)` takes this path, so
      // every other action behaves exactly as it did.
      if (effect.refused) {
        ev.preventDefault();
        if (effect.message) showStatus(effect.message);
        return;
      }

      // Did the key move the machine? Measured across the send against the
      // machine's LIVE stack (not the rendered snapshot, which is a tick behind
      // when a SelectionList fires SELECT and KEY in the same keypress).
      const before = stackSignature();
      onKeyDown(ev);
      const machineActs =
        stackSignature() !== before || ALWAYS_MEANINGFUL.has(action.kind);

      // ---- Feedback. Priority: what the screen said > what the key toggled >
      // "this key does nothing here".
      if (effect.pending) {
        // An in-flight screen action (a print fetch). Await it so the outcome —
        // and above all a REFUSAL, which is the message that matters most —
        // reaches the status line rather than being dropped.
        effect.pending
          .then((message) => {
            if (typeof message === "string") showStatus(message);
          })
          .catch((err: unknown) => {
            showStatus(err instanceof Error ? err.message : String(err));
          });
      } else if (effect.message) {
        showStatus(effect.message);
      } else if (effect.ran || machineActs) {
        showStatus(toggleMessage(action, snap));
      } else {
        showStatus(
          `${combo} — ${actionLabel(action)} does not apply on this screen.`,
        );
      }
    },
    [dispatchScreenAction, onKeyDown, showStatus, stackSignature],
  );

  // THE key delivery path. It is bound to the DOCUMENT, not to the root div,
  // because focus is not guaranteed to live inside the app: every screen change
  // unmounts the previously focused control (e.g. the Gateway's autofocused
  // combobox), at which point the browser moves focus to <body> — which is NOT
  // a descendant of the root div, so a React onKeyDown there would never fire.
  // That is exactly what made Esc (and every other shortcut) dead after drilling
  // into a report. A document listener is immune to where focus happens to be.
  useEffect(() => {
    const listener = (ev: KeyboardEvent) => handleKeyDown(ev);
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, [handleKeyDown]);

  // Belt-and-braces focus retention: if a screen change left focus on <body>
  // (no autofocused control on the new screen), pull it back into the app root
  // so screen readers follow the navigation and in-screen arrow keys land.
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const active = document.activeElement;
    if (!active || active === document.body || !root.contains(active)) {
      root.focus({ preventScroll: true });
    }
  }, [snapshot.screen, snapshot.reportName, snapshot.overlay]);

  /** Button-bar clicks dispatch the SAME combo the keyboard would send. */
  const pressCombo = useCallback(
    (combo: string) => {
      const synthetic = syntheticEventFor(combo);
      if (synthetic) handleKeyDown(synthetic);
    },
    [handleKeyDown],
  );

  const bar = buttonBarFor(snapshot.screen, snapshot.voucherType);
  const Screen = registry.resolve(snapshot);
  const needsCompany = COMPANY_SCOPED_SCREENS.has(snapshot.screen);
  const companyReady = company !== null;

  return (
    <div
      ref={rootRef}
      tabIndex={0}
      role="application"
      aria-label="Tally-compatible keystroke engine"
      className="h-full w-full outline-none"
    >
      <Chrome
        companyName={company?.name ?? "No Company"}
        screenTitle={screenTitle(snapshot)}
        buttonBar={bar}
        hints={hintsFor(snapshot)}
        status={status}
        onButton={pressCombo}
      >
        {needsCompany && !companyReady ? (
          <CompanyLoadingScreen />
        ) : Screen ? (
          // ⛔ `onConfig` IS PASSED TO THE SCREEN AND NOT ONLY TO THE OVERLAY
          // HOST. A screen whose dimension is typed on its own face — the
          // Payroll Register's wage month is the first — has no other way to put
          // that dimension on the frame, and a drill from it therefore carried
          // NOTHING. That was measured in a browser: Enter on a register row
          // opened a payslip screen with no month, which is the state that says
          // "open this from the register", from the register. See
          // ScreenComponentProps.onConfig.
          <Screen snapshot={snapshot} onSelect={onSelect} onConfig={onConfig} />
        ) : (
          <UnregisteredScreen snapshot={snapshot} />
        )}
        <FrameOverlayHost
          snapshot={snapshot}
          onConfig={onConfig}
          onClose={() => pressCombo("Esc")}
        />
      </Chrome>
    </div>
  );
}

/**
 * A confirmation for keys that toggle a per-frame view setting, so the user can
 * see the state they just flipped (Tally shows the same thing in its button
 * bar). Returns null for actions whose effect is self-evident on screen.
 */
function toggleMessage(action: KeymapAction, before: NavSnapshot): string | null {
  const on = (v: boolean | undefined) => (v ? "off" : "on"); // pre-toggle value
  switch (action.kind) {
    case "detailed":
      return `Detailed: ${on(before.config?.detailed)}`;
    case "ledgerWise":
      return `Ledger-wise: ${on(before.config?.ledgerWise ?? LEDGER_WISE_DEFAULT)}`;
    case "changeView":
      return `View: ${before.config?.vertical ? "horizontal" : "vertical"}`;
    default:
      return null;
  }
}

/**
 * Build a KeyboardEvent-shaped object for a canonical combo ("Alt+G", "F9",
 * "Ctrl+A") so a mouse click on the button bar takes the exact same code path
 * as the keystroke — one implementation, no drift between mouse and keyboard.
 */
function syntheticEventFor(combo: string): KeyboardEvent | null {
  const parts = combo.split("+");
  const base = parts[parts.length - 1] ?? "";
  const key =
    base === "Esc" ? "Escape" : base === "Space" ? " " : base;
  return {
    key,
    ctrlKey: parts.includes("Ctrl"),
    altKey: parts.includes("Alt"),
    shiftKey: parts.includes("Shift"),
    metaKey: false,
    preventDefault: () => {},
    stopPropagation: () => {},
  } as unknown as KeyboardEvent;
}

/** Placeholder shown while the company bootstrap is in flight (gated screens). */
function CompanyLoadingScreen() {
  return (
    <section
      role="status"
      className="flex h-full items-center justify-center bg-tally-bg p-4 font-mono text-tally-field"
    >
      <p>Preparing company…</p>
    </section>
  );
}

/** Human title for the chrome's title bar, derived from the active frame. */
function screenTitle(snap: NavSnapshot): string {
  switch (snap.screen) {
    case "gateway":
    case "menu":
      return "Gateway of Tally";
    case "voucher": {
      // Title from the voucher CONFIG, not by upper-casing the slug: the slug
      // form rendered "Credit-note Voucher" for the hyphenated types.
      const t = snap.voucherType;
      const mode = snap.mode === "alter" ? " (Alteration)" : "";
      const title = t ? VOUCHER_CONFIGS[t].title : "Voucher";
      return `${title} Voucher${mode}`;
    }
    case "report":
      return snap.reportName ?? "Report";
    case "report-detail":
      return `${snap.reportName ?? "Report"} — ${snap.detailOf ?? "Detail"}`;
    case "master-create":
      return `Create ${(snap.masterKind ?? "ledger").replace("-", " ")}`;
    case "goto-overlay":
      return "Go To";
    case "bill-inbox":
      return "Bill Inbox";
    case "query":
      return "Ask your books";
    case "help":
      return "Help — Keyboard";
    case "features":
      return "Company Features";
    case "tds-challans":
      return "TDS Challans";
    // The T2 cost screens. The centre summary's title deliberately does NOT name
    // the axis: the axis is chosen inside the screen (F4) and a stale title bar
    // naming the previous one, over another axis's figures, is precisely the
    // confusion the whole feature has to avoid. The screen prints the axis in its
    // own heading, from the response the numbers came in.
    case "cost-centres":
      return "Cost Categories & Cost Centres";
    case "cost-centre-summary":
      return "Cost Centre Summary";
    // The T3 screens. Each register NAMES ITS DIRECTION, and that is not
    // decoration: the two are separate authorities and look nearly identical on
    // screen, so a title bar reading only "Orders" over a purchase commitment is
    // how an operator reads a supplier obligation as a customer's promise.
    case "sales-orders":
      return "Sales Orders";
    case "purchase-orders":
      return "Purchase Orders";
    case "boms":
      return "Bills of Materials";
    // ⛔ THE TITLE SAYS "e-Way Bill" AND NOT "e-Way Bill Number" or "EBN". A
    // title bar is read as a claim about what the screen produces, and what it
    // produces is a payload and a LOCAL reference — never a government number.
    case "e-way-bill":
      return "e-Way Bill";
    // ⛔ THE TITLE SAYS "e-Invoice" AND NOT "IRN". Same argument as the line
    // above, and it bites harder: a title bar reading "IRN" is a claim that this
    // screen produces a government reference number, which this build cannot.
    case "e-invoice":
      return "e-Invoice";
    // ⛔ THE TITLE SAYS "Data" AND NOT "Backup". The screen is Tally's Data menu
    // — backup, restore, and a deliberate refusal of Split Company Data — and a
    // title bar naming only the first would leave an operator looking for the
    // other two somewhere else.
    case "data":
      return "Data";
    // ⛔ THE TITLE NAMES TALLY. Without this case the chrome reads "Tally" — the
    // `default` below — over a screen that is about to post somebody's whole
    // book into the open company, which is the one screen where a wrong title is
    // more than cosmetic.
    case "tally-import":
      return "Import from Tally";
    // ⛔ WITHOUT THIS CASE THE CHROME READS "Tally" — the `default` below — over
    // the screen that decides what every invoice this company issues will say.
    case "print-templates":
      return "Print Templates";
    // ⛔ WITHOUT THIS CASE THE CHROME READS "Tally" — the `default` below — over a
    // screen showing a voucher's hash-chained version history. The screen itself
    // is real and honest (it renders voucher_version with tri-state chain
    // verification); it was the frame around it that said nothing. A title bar is
    // read as the name of what you are looking at, and an audit trail that will
    // not name itself is the one screen where that matters most.
    // The five PAYROLL screens. ⛔ THE REGISTER AND THE PAYSLIP DELIBERATELY DO
    // NOT NAME THE MONTH IN THE TITLE BAR, for the reason the Cost Centre Summary
    // does not name its axis: the month is chosen INSIDE the screen, and a stale
    // title naming the previous one over another month's figures is precisely the
    // confusion payroll must avoid. Each screen prints the month in its own
    // heading, from the response the figures came in.
    case "employees":
      return "Employees";
    case "payroll-run":
      return "Pay Run";
    case "payroll-register":
      return "Payroll Register";
    // ⛔ AND THE PAYSLIP DOES NOT NAME THE EMPLOYEE EITHER — a title bar carrying
    // one person's name over another person's figures is the single worst
    // rendering error this screen can make, and it is exactly what a title
    // derived from the frame (rather than from the served payslip) would produce
    // when a drill resolves to a different row than the one highlighted.
    case "payroll-payslip":
      return "Payslip";
    case "payroll-form-138":
      return "Form 138 — Salary TDS (Annexure I)";
    case "edit-log":
      return "Edit Log";
    default:
      return "Tally";
  }
}

/** Bottom-strip shortcut hints, scoped to the active screen. */
function hintsFor(snap: NavSnapshot): string[] {
  const common = ["Esc: Back", "Alt+G: Go To", "Ctrl+G: Switch To", "F1: Help"];
  switch (snap.screen) {
    case "voucher":
      return [
        "Enter: Next field",
        "Tab / Shift+Tab: Field",
        "Alt+C: Create Master",
        "Ctrl+H: Change Mode",
        // ⛔ Alt+L IS NAMED HERE BECAUSE THIS IS THE ONLY SCREEN IT EXISTS ON.
        // It opens the voucher's edit log (navMachine's `editLog`), and until
        // 2026-08-07 nothing on any surface mentioned it — not this strip, not
        // the button bar. An audit trail nobody can open is an audit trail the
        // product does not have, whatever the parity ledger says.
        "Alt+L: Edit Log",
        "Ctrl+A: Accept",
        "Ctrl+Q: Quit",
        ...common,
      ];
    case "report":
    case "report-detail":
      return [
        "Enter: Drill down",
        "F2: Period",
        "F12: Configure",
        "Ctrl+F: Filter",
        "Alt+E: Export",
        ...common,
      ];
    case "master-create":
      return ["Enter: Next field", "Ctrl+A: Accept", "Esc: Cancel", "F1: Help"];
    case "cost-centres":
      return ["Alt+C: Create", "Ctrl+A: Accept", ...common];
    case "e-way-bill":
      return ["Ctrl+A: Raise the bill", ...common];
    // ⛔ "Report the invoice (FREEZES it)" AND NOT "Ctrl+A: Raise". The hint
    // strip is the last surface an operator reads before pressing the key, and
    // it is the only one of the three (bar, strip, screen) that sits directly
    // above their hands. The bar has no room for the consequence; this does, and
    // the consequence is that the voucher can never be altered or deleted again.
    case "e-invoice":
      return ["Ctrl+A: Report the invoice (FREEZES it)", ...common];
    // ⛔ THE TWO COST SUMMARIES ADVERTISE Alt+E ON BOTH SURFACES OR NEITHER.
    // buttonBar.ts put "Alt+E Export" on both of these bars the day the screens
    // registered a real handler (T15); this strip did not, so the same key was
    // sold at the top of the screen and unmentioned at the bottom. An operator
    // reads the hint strip as the list of what works here, so a key missing from
    // it reads as a key that does not — which is how a working export goes
    // unused. Both surfaces are hand-maintained lists; the shell test in
    // export/exportKey.test.tsx presses the key on each screen and asserts BOTH
    // of them name it, which is the only thing that keeps them in step.
    case "cost-centre-summary":
      return ["F2: Period", "F4: Cost Category", "Alt+E: Export", ...common];
    case "cost-category-summary":
      // No axis picker on purpose: this report IS the list of axes, and Enter on
      // a row opens that axis's Cost Centre Summary.
      return [
        "F2: Period",
        "Enter: Open this axis",
        "Alt+E: Export",
        ...common,
      ];
    // The two order registers. "Alt+W: Pre-close" is spelled Tally's way
    // (RESEARCH.md:622) and the hint strip has no room to say what it means, so
    // the key's own label carries that: it ABANDONS an order and is not "mark it
    // delivered". Nothing here is a period — the outstanding position as at a date
    // is the order BOOK, which is a report.
    case "sales-orders":
    case "purchase-orders":
      return [
        "Alt+C: New order",
        "Alt+W: Pre-close",
        "Ctrl+A: Accept",
        "Ctrl+F8 / Ctrl+F9: Sales / Purchase",
        ...common,
      ];
    case "boms":
      return [
        "Alt+C: New recipe",
        "Alt+X: Explode",
        "Ctrl+A: Accept",
        ...common,
      ];
    case "goto-overlay":
      return ["Type to search", "Enter: Open", "Esc: Cancel"];
    case "help":
      return ["Esc: Back"];
    case "features":
      return ["Esc: Back", "F1: Help"];
    case "data":
      return ["Esc: Back", "F1: Help"];
    // ⛔ WITHOUT THIS CASE THE STRIP FALLS TO `default` AND ADVERTISES
    // "Enter: Open" ON A SCREEN WITH NOTHING TO OPEN — and worse, next to a
    // button that fires an import that cannot be undone.
    case "tally-import":
      return ["Esc: Back", "F1: Help"];
    // ⛔ WITHOUT THIS CASE THE STRIP FALLS TO `default` AND ADVERTISES
    // "Enter: Open" ON A SCREEN WITH NOTHING TO OPEN. This is a form: its two
    // actions are named buttons, deliberately, because "save a layout" and
    // "clear a customisation" are different consequences and a generic accept
    // key cannot say which it would fire.
    case "print-templates":
      return ["Esc: Back", "F1: Help"];
    // ⛔ WITHOUT THIS CASE THE STRIP FALLS TO `default` AND ADVERTISES
    // "Enter: Open" ON A SCREEN WITH NOTHING TO OPEN. The edit log is a read-only
    // list of versions; there is no drill-down behind a row, so the one hint an
    // operator was given here was the one thing that does not work.
    case "edit-log":
      return ["Esc: Back", "F1: Help"];
    default:
      return ["Enter: Open", ...common];
  }
}

function UnregisteredScreen({ snapshot }: { snapshot: NavSnapshot }) {
  return (
    <section className="flex h-full items-center justify-center bg-tally-bg p-4 font-mono text-tally-field">
      <p>No screen registered for "{snapshot.screen}".</p>
    </section>
  );
}
