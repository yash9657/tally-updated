import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { useClient, useCompanyID } from "../app/CompanyContext";
import { useRegisterScreenAction } from "../app/ScreenActions";
import { ApiError } from "../api";
import type {
  CoredClient,
  LedgerListItem,
  OrderDirection,
  OrderLineInput,
  OrderListResponse,
  OrderView,
  StockItemListItem,
} from "../api";
import { normalizeMoney } from "./vouchers/money";
import { formatMoney } from "./reports/money";
import { usePrintAction } from "./usePrint";

/**
 * THE ORDER REGISTERS (T3, migration 0042) — the screen that makes order
 * processing usable at all, and the last thing standing between a built backend
 * and a keyboard-only operator who could not create an order.
 *
 * ⛔⛔ THE ONE THING TO UNDERSTAND BEFORE READING ANY OF IT.
 *
 * AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT. The stock ledger has exactly
 * one primitive — a positive quantity plus a direction — and an order is neither.
 * Nothing on this screen moves stock, posts to a ledger, appears in the Day Book or
 * touches the Balance Sheet: it records a PROMISE. Pushing an order through
 * `stock_movement` would corrupt closing stock, and closing stock now feeds the
 * Balance Sheet, so the error would compound with every open order and reconcile
 * against nothing.
 *
 * ⛔ AND WHY THERE ARE TWO OF THESE SCREENS RATHER THAN ONE WITH A TOGGLE.
 *
 * `sales-order` and `purchase-order` are SEPARATE AUTHORITIES: the server serves
 * them on two routes under two bare tokens, and the PDP reads the PATH. So "this
 * salesperson may commit us to customers but may not commit us to suppliers" is a
 * real, writable rule — and a single screen with a direction switch would be
 * reaching a resource the operator may be denied, producing a 403 they cannot
 * attribute to anything they did. Tally arrives at the same place from a different
 * direction: it has two KEYS, Ctrl+F8 and Ctrl+F9 (RESEARCH.md:400, :614, :616).
 *
 * ⚠️ SO WHY IS THERE ONE COMPONENT? Because the rule is about AUTHORITY, not about
 * pixels. {@link OrderRegisterScreen} is instantiated twice and each instance's
 * direction is fixed AT REGISTRATION (defaultScreens.tsx maps the two ScreenKinds
 * to two adapters). It is never read from operator input, no control on the screen
 * changes it, and every request an instance makes goes to exactly one route through
 * exactly one client method. What the design forbids is a single reachable surface
 * serving both directions. If a direction control ever appears here, that is the
 * defect all of this exists to prevent.
 *
 * WHAT THIS SCREEN REFUSES TO DO
 *
 *  1. IT NEVER ASKS FOR AN ID. The party is picked from the served chart of
 *     accounts and every item from the served stock list. A `party_ledger_id` or a
 *     `stock_item_id` is a UUID no screen in this product displays, so a box for one
 *     could not be filled by a human — the defect the TDS challan form shipped with
 *     and had to be rebuilt for.
 *
 *  2. IT NEVER ASKS FOR A `line_no`, and never sends one. The SERVER assigns it
 *     from each line's position in the array (the first line is 1). A
 *     client-supplied ordinal would be a second source of truth for the same fact,
 *     and the two part company the first time an operator deletes a middle line —
 *     which this form lets them do.
 *
 *  3. IT NEVER RENDERS A MISSING DUE DATE AS A DATE. An omitted due date means "no
 *     delivery date was agreed", which is NOT overdue however old the order is. A
 *     screen that defaulted it to today, or sorted it as if it were a date, would
 *     invent an overdue commitment out of an ordinary open one.
 *
 *  4. IT NEVER SAYS "CLOSE" WHEN IT MEANS "ABANDON". Alt+W pre-closes an order
 *     (Tally's own word, RESEARCH.md:622) and that is manual ABANDONMENT — "the
 *     customer cancelled". It is not "mark it delivered", and it cannot be:
 *     fulfilment is DERIVED from the delivery notes and can never be asserted.
 *     `is_closed` is the one order field that IS stored, precisely because nothing
 *     can compute it — an order nobody ever delivered and one that was cancelled
 *     have identical fulfilment histories. So the reason is required, and the form
 *     says what the key means before it takes it.
 *
 *  5. IT NEVER CACHES `fulfilled_qty` OR `outstanding_qty` ACROSS AN EDIT. Neither
 *     is stored anywhere: the server sums `order_fulfilment` over CURRENT,
 *     non-deleted voucher versions on every single read. A stored figure would go
 *     stale undetectably the first time a fulfilling voucher was ALTERED, because an
 *     alter writes a NEW version and re-states its links without visiting the order.
 *     This screen re-reads the order after anything that could have changed it.
 *
 *  6. IT RENDERS THE SERVER'S SENTENCE, verbatim. A 409 on an order number says it
 *     is taken FOR THAT DIRECTION, matched case-insensitively — and a sales and a
 *     purchase order may legally share a number, so a message that hid the
 *     direction would read as a bug to anyone looking at the other register.
 *
 *  7. IT TREATS A 403 AS A DECISION. See the two-authorities note above: being
 *     allowed one register and refused the other is the configuration the split
 *     exists to make possible, so a denial names the direction it refused.
 *
 *  8. IT NEVER OFFERS THE WHOLE CHART OF ACCOUNTS AS THE PARTY. A sales order
 *     commits to a Sundry Debtor and a purchase order to a Sundry Creditor; the
 *     picker is narrowed to that group, and the SERVER refuses anything outside
 *     it by ltree ancestry. It shipped unnarrowed, and a sales order could be
 *     committed to a bank ledger. See {@link RegisterCopy.partyGroup}.
 *
 *  9. IT NEVER CALLS AN ABANDONED BALANCE "OUTSTANDING". After Alt+W the order
 *     leaves the open-only list, and the detail panel below it says so and
 *     renames the column — a live-looking commitment under a row that has
 *     vanished is how an operator comes to chase goods nobody is sending.
 */

/** Everything that differs between the two registers, in one place. */
interface RegisterCopy {
  title: string;
  /** "Customer" / "Supplier" — who the party ON THIS DIRECTION is. */
  partyNoun: string;
  /** The sentence under the heading. */
  blurb: string;
  /** What an outstanding line means here — delivered by us, or received by us. */
  fulfilledNoun: string;
  /**
   * The account group this direction's party MUST be kept under.
   *
   * ⛔⛔ THIS IS WHAT MAKES THE PARTY A PARTY, AND ITS ABSENCE WAS THE DEFECT.
   * The picker below offered the ENTIRE chart of accounts, so a sales order could
   * be committed to a BANK ledger and a purchase order to a customer — and
   * nothing on the client, in the server's Validate, or in the database said no.
   * The order books GROUP BY COUNTERPARTY and exist to answer "who is owed what",
   * so such an order names, in a report an operator acts on, an account that can
   * never be invoiced and can never deliver.
   *
   * ⚠️ THE SERVER IS THE AUTHORITY AND THIS IS ONLY THE NARROWING. cored walks
   * the ltree ANCESTRY of the ledger's group (orders.checkPartyGroup) and refuses
   * anything outside it with a sentence. A client-side filter alone would be a
   * suggestion — the route accepts a `party_ledger_id`, not a picked option — and
   * a server refusal alone would be an unexplained 422 on a choice the screen
   * itself offered. Both ends, and the screen's end is deliberately the weaker
   * one: see `offerEveryLedger` for why it can be turned off.
   */
  partyGroup: string;
  /** The key that opens this register, for cross-referencing in prose. */
  key: string;
  /** The order BOOK that reports this direction's outstanding position. */
  bookName: string;
}

const SALES_COPY: RegisterCopy = {
  title: "Sales Orders",
  partyNoun: "Customer",
  blurb:
    "What customers have committed to buy from us. An order is a PROMISE, not an " +
    "entry: nothing here moves stock, posts to a ledger or appears in the Day Book.",
  fulfilledNoun: "Delivered",
  partyGroup: "Sundry Debtors",
  key: "Ctrl+F8",
  bookName: "Sales Order Book",
};

const PURCHASE_COPY: RegisterCopy = {
  title: "Purchase Orders",
  partyNoun: "Supplier",
  blurb:
    "What we have committed to buy from suppliers. An order is a PROMISE, not an " +
    "entry: nothing here moves stock, posts to a ledger or appears in the Day Book " +
    "— and a purchase commitment is not a liability until the goods or the bill " +
    "arrive.",
  fulfilledNoun: "Received",
  partyGroup: "Sundry Creditors",
  key: "Ctrl+F9",
  bookName: "Purchase Order Book",
};

/**
 * The four client methods this screen needs, resolved ONCE from the direction.
 *
 * ⛔ THIS IS THE ONLY PLACE THE DIRECTION IS BRANCHED ON, and it is deliberately a
 * lookup of METHODS rather than a `direction` argument threaded into shared calls.
 * Two authorities means two routes; a single method taking a direction would hide
 * that behind a parameter, and the 403 it produced would be unattributable.
 */
function methodsFor(client: CoredClient, direction: OrderDirection) {
  return direction === "SALES"
    ? {
        list: client.listSalesOrders.bind(client),
        get: client.getSalesOrder.bind(client),
        create: client.createSalesOrder.bind(client),
        close: client.closeSalesOrder.bind(client),
      }
    : {
        list: client.listPurchaseOrders.bind(client),
        get: client.getPurchaseOrder.bind(client),
        create: client.createPurchaseOrder.bind(client),
        close: client.closePurchaseOrder.bind(client),
      };
}

/** One line being keyed. `line_no` is absent on purpose — see refusal (2). */
interface DraftLine {
  stock_item_id: string;
  qty: string;
  rate: string;
  due_date: string;
}

const EMPTY_LINE: DraftLine = {
  stock_item_id: "",
  qty: "",
  rate: "",
  due_date: "",
};

export function OrderRegisterScreen({
  direction,
}: {
  /** ⛔ FIXED AT REGISTRATION. Never operator input — see the header. */
  direction: OrderDirection;
}) {
  const client = useClient();
  const companyID = useCompanyID();
  const copy = direction === "SALES" ? SALES_COPY : PURCHASE_COPY;
  const api = useMemo(() => methodsFor(client, direction), [client, direction]);
  const testid = direction === "SALES" ? "sales-orders" : "purchase-orders";

  const [data, setData] = useState<OrderListResponse | null>(null);
  const [listError, setListError] = useState("");
  const [denied, setDenied] = useState(false);
  const [busy, setBusy] = useState(false);
  /**
   * ⚠️ NOT A "SHOW ALL" DEFAULT. The register opens on OPEN orders, because that is
   * what an operator acts on; the response ECHOES the filter and the heading is
   * rendered from the echo, never from this flag — "no orders" and "no OPEN orders"
   * are different answers and a filtered list under an unfiltered heading is how the
   * second gets read as the first.
   */
  const [openOnly, setOpenOnly] = useState(true);

  /** The HIGHLIGHTED order, and its lines. Local state: the machine stays pure. */
  const [selectedID, setSelectedID] = useState("");
  const [detail, setDetail] = useState<OrderView | null>(null);
  const [detailError, setDetailError] = useState("");

  // ---- The create form (Alt+C).
  const [creating, setCreating] = useState(false);
  const [orderNo, setOrderNo] = useState("");
  const [orderDate, setOrderDate] = useState("");
  const [partyID, setPartyID] = useState("");
  const [narration, setNarration] = useState("");
  const [lines, setLines] = useState<DraftLine[]>([{ ...EMPTY_LINE }]);
  /** THE SERVER'S SENTENCE, verbatim. */
  const [refusal, setRefusal] = useState("");
  const [writeDenied, setWriteDenied] = useState(false);
  const [notice, setNotice] = useState("");

  // ---- The pre-close panel (Alt+W).
  const [closing, setClosing] = useState(false);
  const [reason, setReason] = useState("");

  // ---- The pickers. Both are SERVED lists; nothing here is ever typed as an id.
  const [ledgers, setLedgers] = useState<LedgerListItem[]>([]);
  const [items, setItems] = useState<StockItemListItem[]>([]);
  const [pickerError, setPickerError] = useState("");
  /**
   * ⛔⛔ THE ESCAPE HATCH, AND WHY THERE IS ONE AT ALL.
   *
   * The party list is narrowed to ledgers whose group is exactly
   * {@link RegisterCopy.partyGroup}, and that filter is NARROWER THAN THE SERVER'S
   * RULE. cored walks ltree ANCESTRY, so a customer filed under a user sub-group
   * ("Retail Customers", itself under Sundry Debtors) is a perfectly ordinary
   * customer and cored accepts it — while `LedgerListItem` carries only the
   * ledger's IMMEDIATE `group_name`, so this screen cannot see that it is one.
   *
   * That is a KNOWN, NAMED failure shape in this codebase: `is_bank` is
   * server-derived from exactly this ancestry BECAUSE a client filtering on the
   * two seeded group names silently dropped real bank accounts — silently, because
   * a shorter list is not an error. So the filter is not allowed to be the last
   * word. Ticking this shows the whole chart, and the server still refuses
   * anything outside the group with a sentence this screen renders verbatim.
   *
   * ⚠️ IT DEFAULTS TO false, WHICH IS THE HALF THAT FIXES THE DEFECT. The wrong
   * party must not be a thing an operator can pick by accident; it may only be a
   * thing they reach on purpose, having read why.
   */
  const [offerEveryLedger, setOfferEveryLedger] = useState(false);

  /**
   * The ledgers this register will OFFER as a party.
   *
   * Compared on `group_name` with surrounding space trimmed and case ignored,
   * because the group is a name a human typed when the ledger was created and
   * "sundry debtors " is the same group as "Sundry Debtors".
   */
  const partyLedgers = useMemo(
    () =>
      offerEveryLedger
        ? ledgers
        : ledgers.filter(
            (l) =>
              l.group_name.trim().toLowerCase() ===
              copy.partyGroup.toLowerCase(),
          ),
    [copy.partyGroup, ledgers, offerEveryLedger],
  );

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const res = await api.list(companyID, openOnly);
      setData(res);
      setListError("");
      setDenied(false);
    } catch (e) {
      setData(null);
      // A 403 is a DECISION here — see refusal (7).
      if (e instanceof ApiError && e.status === 403) {
        setDenied(true);
        setListError("");
      } else {
        setDenied(false);
        setListError(e instanceof Error ? e.message : String(e));
      }
    } finally {
      setBusy(false);
    }
  }, [api, companyID, openOnly]);

  useEffect(() => {
    void load();
  }, [load]);

  // The two pickers, loaded once. They are what makes refusal (1) possible: a form
  // that could not offer these lists could only ask for a UUID.
  useEffect(() => {
    let live = true;
    Promise.all([
      client.listLedgers(companyID),
      client.listStockItems(companyID),
    ])
      .then(([l, s]) => {
        if (!live) return;
        setLedgers(l.ledgers);
        setItems(s.stock_items);
        setPickerError("");
      })
      .catch((e: unknown) => {
        if (!live) return;
        // STATED, not swallowed. Without these lists the create form cannot be
        // filled at all, and Alt+C says so rather than opening an unusable panel.
        setPickerError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      live = false;
    };
  }, [client, companyID]);

  /**
   * Re-read the HIGHLIGHTED order's lines.
   *
   * ⛔ ALWAYS FROM THE SERVER, NEVER FROM A CACHE — refusal (5). `fulfilled_qty`
   * and `outstanding_qty` are derived on every read, and altering a delivery note
   * changes them without touching the order at all.
   */
  const loadDetail = useCallback(
    async (orderID: string) => {
      if (orderID === "") {
        setDetail(null);
        setDetailError("");
        return;
      }
      try {
        const res = await api.get(companyID, orderID);
        setDetail(res);
        setDetailError("");
      } catch (e) {
        setDetail(null);
        // ⚠️ A 404 HERE IS NOT "DELETED". It also means "that id is an order of the
        // OTHER direction" — both families read one table and the server refuses to
        // serve one through the other's route. Saying "deleted" would be a claim
        // about a row that exists.
        setDetailError(
          e instanceof ApiError && e.status === 404
            ? "That order could not be read on this register. It has either been " +
                "removed, or it is an order of the other direction — the two share " +
                "one table and the server will not serve one through the other's " +
                "route, because that is the boundary the two authorities rest on."
            : e instanceof Error
              ? e.message
              : String(e),
        );
      }
    },
    [api, companyID],
  );

  useEffect(() => {
    void loadDetail(selectedID);
  }, [loadDetail, selectedID]);

  // Ctrl+P. There is no printed layout for an order register, so the resolver
  // answers with a SENTENCE and makes no request. Registering it is what turns a
  // silently dead key into that sentence.
  usePrintAction({ voucher: { voucherID: null } });

  const orders = data?.orders ?? [];
  const selected = orders.find((o) => o.order_id === selectedID);

  // ---- Alt+C — RECORD AN ORDER.
  useRegisterScreenAction(
    "createOrder",
    useCallback((): string => {
      if (denied) {
        return `You are not permitted to create ${copy.title.toLowerCase()} on this company.`;
      }
      // ⚠️ THE PANEL'S RENDER CONDITION, VERBATIM. Anything else here can claim the
      // key worked while nothing appears — the Alt+O signature.
      if (ledgers.length === 0 || items.length === 0) {
        return (
          `The ${copy.partyNoun.toLowerCase()} and the items are PICKED from this ` +
          `company's own lists, never typed as ids, so the form cannot open until ` +
          `both have been read. ` +
          (pickerError !== ""
            ? `They could not be: ${pickerError}`
            : items.length === 0
              ? "This company has no stock items yet — an order commits a QUANTITY OF SOMETHING, so create an item first."
              : "The chart of accounts came back empty.")
        );
      }
      if (creating) {
        return "The order form is already open. Ctrl+A accepts it.";
      }
      setCreating(true);
      setClosing(false);
      setOrderNo("");
      setOrderDate("");
      setPartyID("");
      // A fresh form starts NARROWED. Carrying the widened list over from a
      // previous order would make "offer every ledger" sticky, and a safety
      // narrowing that a past decision silently disables is not one.
      setOfferEveryLedger(false);
      setNarration("");
      setLines([{ ...EMPTY_LINE }]);
      setRefusal("");
      setWriteDenied(false);
      setNotice("");
      return `Key the order. Ctrl+A accepts it. Nothing is posted and no stock moves — an order is a commitment.`;
    }, [
      copy.partyNoun,
      copy.title,
      creating,
      denied,
      items.length,
      ledgers.length,
      pickerError,
    ]),
  );

  // ---- Alt+W — PRE-CLOSE (abandon) the highlighted order.
  useRegisterScreenAction(
    "closeOrder",
    useCallback((): string => {
      if (denied) {
        return `You are not permitted to alter ${copy.title.toLowerCase()} on this company.`;
      }
      if (creating) {
        return (
          "The order form is open. Finish it with Ctrl+A or close the panel first — " +
          "pre-closing acts on an order in the list, and the one you are keying is " +
          "not in it yet."
        );
      }
      if (selected === undefined) {
        // ⚠️ THE PANEL'S RENDER CONDITION AGAIN. Alt+W acts on the HIGHLIGHTED
        // order, so with nothing highlighted the panel must not open.
        return (
          "Highlight the order first — Alt+W pre-closes the one selected in the " +
          "list. Nothing has been changed."
        );
      }
      setClosing(true);
      setReason("");
      setRefusal("");
      setWriteDenied(false);
      setNotice("");
      return selected.is_closed
        ? `${selected.order_no} is already pre-closed. Ctrl+A RE-OPENS it and clears the reason.`
        : `Pre-close ${selected.order_no} — this ABANDONS the outstanding balance and is not "mark it delivered". A reason is required. Ctrl+A accepts it.`;
    }, [copy.title, creating, denied, selected]),
  );

  const addLine = useCallback(
    () => setLines((ls) => [...ls, { ...EMPTY_LINE }]),
    [],
  );
  const removeLine = useCallback(
    (idx: number) =>
      // ⛔ AND THIS IS WHY NO `line_no` IS SENT (refusal 2): deleting a middle line
      // renumbers everything after it, and a client-supplied ordinal would now
      // disagree with the position the server derives.
      setLines((ls) => (ls.length === 1 ? ls : ls.filter((_, i) => i !== idx))),
    [],
  );
  const patchLine = useCallback(
    (idx: number, patch: Partial<DraftLine>) =>
      setLines((ls) => ls.map((l, i) => (i === idx ? { ...l, ...patch } : l))),
    [],
  );

  /** Lines with an item chosen — the only ones that will be sent. */
  const filledLines = useMemo(
    () => lines.filter((l) => l.stock_item_id !== ""),
    [lines],
  );

  // ---- Ctrl+A — ACCEPT, for whichever panel is open. Async so the shell awaits it
  // and the server's refusal reaches the status line as well as the panel.
  const submit = useCallback(async (): Promise<string> => {
    if (closing && selected) {
      const reopening = selected.is_closed;
      if (!reopening && reason.trim() === "") {
        // THE SCREEN'S OWN REFUSAL, and it is not duplicated validation: the server
        // 422s a missing reason, and stating it here means the operator is told
        // BEFORE a write that will be refused.
        return (
          "Say why this order is being abandoned. A commitment that disappears with " +
          "no stated cause cannot be told apart from a mistake later, which is why " +
          "the reason is required rather than encouraged. Nothing was sent."
        );
      }
      setBusy(true);
      try {
        await api.close(companyID, selected.order_id, {
          is_closed: !reopening,
          // ⛔ OMITTED WHEN RE-OPENING. The server refuses a reason on an open
          // order — a reason attached to something that has not happened is a claim
          // about nothing — so re-opening clears it rather than keeping it around.
          ...(reopening ? {} : { reason: reason.trim() }),
        });
        setClosing(false);
        setReason("");
        await load();
        await loadDetail(selected.order_id);
        const message = reopening
          ? `Re-opened ${selected.order_no}. Its reason has been cleared, and whatever is still outstanding on it is back in the ${copy.bookName}.`
          : `Pre-closed ${selected.order_no}. Its outstanding balance has left the ${copy.bookName} — the order is ABANDONED, not delivered, and every delivery already recorded against it is untouched.`;
        setNotice(message);
        return message;
      } catch (e) {
        const message = e instanceof Error ? e.message : String(e);
        setRefusal(message);
        setWriteDenied(e instanceof ApiError && e.status === 403);
        return message;
      } finally {
        setBusy(false);
      }
    }

    if (!creating) {
      return `Nothing to accept here. Alt+C records an order; Alt+W pre-closes the highlighted one.`;
    }
    if (partyID === "") {
      return (
        `Choose the ${copy.partyNoun.toLowerCase()} this order is with. An order ` +
        `with no party is a commitment to nobody, so nothing was sent.`
      );
    }
    if (filledLines.length === 0) {
      return (
        "Add at least one line with an item chosen. An order with no lines commits " +
        "nothing at all, and the server refuses it — nothing was sent."
      );
    }

    setRefusal("");
    setWriteDenied(false);
    setNotice("");
    setBusy(true);
    try {
      const created = await api.create(companyID, {
        order_no: orderNo.trim(),
        order_date: orderDate,
        party_ledger_id: partyID,
        narration: narration.trim(),
        // ⛔ REQUIRED. Omitting it is a 400 AFTER the step-up has already
        // succeeded, which reads to an operator as "the passkey failed" and is not.
        created_by: "ui",
        lines: filledLines.map(
          (l): OrderLineInput => ({
            stock_item_id: l.stock_item_id,
            // The COMMITTED quantity, always positive — the order's DIRECTION
            // carries the sense, and a negative quantity here would be a different
            // order rather than this one reversed.
            qty: normalizeMoney(l.qty),
            // ⚠️ OMITTED, NOT ZEROED, when the operator did not agree a rate. The
            // server defaults it to zero and always sends a figure back, but "no
            // rate agreed" and "agreed to be free" are different facts and only the
            // absent field says the first.
            ...(l.rate.trim() === "" ? {} : { rate: normalizeMoney(l.rate) }),
            // ⛔ OMITTED MEANS "NO DATE AGREED", WHICH IS NOT OVERDUE — refusal (3).
            ...(l.due_date === "" ? {} : { due_date: l.due_date }),
            // ⛔ NO `line_no`. The server assigns it from this array's order.
          }),
        ),
      });
      setCreating(false);
      await load();
      // Highlight what was just created, and read its lines back — which is also
      // the only way the operator sees the line numbers the SERVER assigned.
      setSelectedID(created.order_id);
      const message =
        `Recorded order ${orderNo.trim()} with ${filledLines.length} line(s). ` +
        `Nothing has moved: no stock, no posting, nothing in the Day Book. It is ` +
        `outstanding in full until a delivery is linked to it, and it will show in ` +
        `the ${copy.bookName} until then.`;
      setNotice(message);
      return message;
    } catch (e) {
      // THE SERVER'S OWN SENTENCE — refusal (6). A 409 names the DIRECTION the
      // number is taken in, which is the part that matters: the other register may
      // legally hold the same number.
      const message = e instanceof Error ? e.message : String(e);
      setRefusal(message);
      setWriteDenied(e instanceof ApiError && e.status === 403);
      return message;
    } finally {
      setBusy(false);
    }
  }, [
    api,
    closing,
    companyID,
    copy.bookName,
    copy.partyNoun,
    creating,
    filledLines,
    load,
    loadDetail,
    narration,
    orderDate,
    orderNo,
    partyID,
    reason,
    selected,
  ]);

  useRegisterScreenAction("accept", submit);

  const firstFieldRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (creating) firstFieldRef.current?.focus();
  }, [creating]);

  if (denied) {
    return (
      <section
        data-testid={`${testid}-denied`}
        className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field"
      >
        <h1 className="text-lg font-bold text-tally-title">{copy.title}</h1>
        <p className="mt-2 max-w-3xl text-sm">
          You do not have permission to read or record this company&apos;s{" "}
          {copy.title.toLowerCase()}. That is a decision, not a fault — and it is
          the exact reason sales and purchase orders are SEPARATE authorities
          rather than one screen with a direction switch: a level can be allowed to
          commit the company to its customers and refused the power to commit it to
          its suppliers, which is a real division of duties. With one screen that
          rule could not be written, because the server&apos;s policy decision reads
          the ROUTE and cannot see a query parameter. An owner can grant it by
          adding an{" "}
          <span className="text-tally-title">
            allow {direction === "SALES" ? "sales-order" : "purchase-order"}
          </span>{" "}
          rule in Users &amp; Roles — or{" "}
          <span className="text-tally-title">
            allow create {direction === "SALES" ? "sales-order" : "purchase-order"}
          </span>{" "}
          for the write alone, since displaying orders and recording them are
          separate actions on the one token.
        </p>
      </section>
    );
  }

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">{copy.title}</h1>
      <p className="mt-1 max-w-3xl text-xs">
        {copy.blurb} Alt+C records one; Alt+W pre-closes the highlighted one.
        {busy ? " · working…" : ""}
      </p>

      {listError !== "" ? (
        <p
          data-testid={`${testid}-error`}
          className="mt-3 max-w-3xl text-sm text-tally-bar"
        >
          {listError}
        </p>
      ) : null}

      {notice !== "" ? (
        <p
          data-testid={`${testid}-notice`}
          className="mt-3 max-w-3xl border border-tally-fieldText p-2 text-sm"
        >
          {notice}
        </p>
      ) : null}

      {pickerError !== "" ? (
        <p
          data-testid={`${testid}-picker-error`}
          className="mt-3 max-w-3xl border border-tally-bar p-2 text-xs text-tally-bar"
        >
          The {copy.partyNoun.toLowerCase()} list or the stock item list could not
          be read, so Alt+C cannot open a form that would have to ask for UUIDs
          instead: {pickerError}
        </p>
      ) : null}

      {/* ---- THE PRE-CLOSE PANEL (Alt+W). ---------------------------------- */}
      {closing && selected ? (
        <form
          data-testid={`${testid}-close-panel`}
          className="mt-4 max-w-3xl border border-tally-bar p-3 text-sm"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <h2 className="font-bold text-tally-bar">
            {selected.is_closed ? "Re-open" : "Pre-close"} {selected.order_no}
          </h2>
          {selected.is_closed ? (
            <p className="mt-1 text-xs">
              This order is pre-closed, for the stated reason{" "}
              <span className="text-tally-title">
                {selected.closed_reason || "(none recorded)"}
              </span>
              . Accepting RE-OPENS it and clears that reason together — the server
              refuses a reason on an open order, because a reason attached to
              something that has not happened is a claim about nothing. Whatever is
              still outstanding returns to the {copy.bookName}.
            </p>
          ) : (
            <>
              <p className="mt-1 text-xs">
                ⛔ THIS ABANDONS THE ORDER. It is Tally&apos;s &ldquo;pre-close&rdquo;
                and it does NOT mean &ldquo;mark it delivered&rdquo; — it cannot,
                because what has been delivered is DERIVED from the delivery notes
                and can never be asserted. Every delivery already recorded against
                this order stays exactly as it is; what leaves is the outstanding
                balance, and with it the order&apos;s place in the {copy.bookName}.
              </p>
              <label className="mt-3 flex flex-col text-xs">
                <span className="text-tally-title">
                  Why? Required — an abandoned commitment with no cause cannot be
                  told apart from a mistake.
                </span>
                <input
                  aria-label="Reason for pre-closing this order"
                  className="mt-1 w-96 bg-tally-field px-1 text-tally-fieldText outline-none"
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                />
              </label>
            </>
          )}
          <div className="mt-3 flex items-center gap-3">
            <button
              type="submit"
              className="border border-tally-fieldText px-2 py-0.5 text-xs"
            >
              Ctrl+A — {selected.is_closed ? "Re-open" : "Pre-close"}
            </button>
            <button
              type="button"
              className="border border-tally-fieldText px-2 py-0.5 text-xs"
              onClick={() => {
                setClosing(false);
                setRefusal("");
                setWriteDenied(false);
              }}
            >
              Close the panel
            </button>
          </div>
        </form>
      ) : null}

      {/* ---- THE CREATE FORM (Alt+C). -------------------------------------- */}
      {creating ? (
        <form
          data-testid={`${testid}-panel`}
          className="mt-4 max-w-5xl border border-tally-fieldText p-3 text-sm"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <h2 className="font-bold text-tally-title">
            Record a {direction === "SALES" ? "sales" : "purchase"} order
          </h2>

          <div className="mt-2 flex flex-wrap gap-4">
            <label className="flex flex-col text-xs">
              <span className="text-tally-title">Order number</span>
              <input
                ref={firstFieldRef}
                aria-label="Order number"
                className="mt-1 w-40 bg-tally-field px-1 text-tally-fieldText outline-none"
                value={orderNo}
                onChange={(e) => setOrderNo(e.target.value)}
              />
            </label>
            <label className="flex flex-col text-xs">
              <span className="text-tally-title">Order date</span>
              <input
                type="date"
                aria-label="Order date"
                className="mt-1 bg-tally-field px-1 text-tally-fieldText outline-none"
                value={orderDate}
                onChange={(e) => setOrderDate(e.target.value)}
              />
            </label>
            {/* THE PARTY — PICKED from the served chart of accounts, and NARROWED
                to this direction's counterparty group. See refusal (1), and
                RegisterCopy.partyGroup for what the unnarrowed list allowed. */}
            <label className="flex flex-col text-xs">
              <span className="text-tally-title">
                {copy.partyNoun}{" "}
                <span className="font-normal text-tally-field">
                  · under {copy.partyGroup}
                </span>
              </span>
              <select
                aria-label={copy.partyNoun}
                className="mt-1 w-64 bg-tally-field px-1 text-tally-fieldText outline-none"
                value={partyID}
                onChange={(e) => setPartyID(e.target.value)}
              >
                <option value="">— choose —</option>
                {partyLedgers.map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.name}
                    {/* The GROUP is shown only on the widened list, where it is the
                        one fact that tells a real party from a bank account. On the
                        narrowed list every row has the same group and printing it
                        would be noise. */}
                    {offerEveryLedger ? ` · ${l.group_name}` : ""}
                  </option>
                ))}
              </select>
            </label>
          </div>

          {/* ---- WHO MAY BE A PARTY, AND THE WAY OUT OF THE NARROWING. ------ */}
          <p
            data-testid={`${testid}-party-group-note`}
            className="mt-2 max-w-3xl text-xs"
          >
            A {direction === "SALES" ? "sales" : "purchase"} order commits this
            company to a{" "}
            <span className="text-tally-title">
              {copy.partyNoun.toLowerCase()}
            </span>
            , so the list above offers ledgers under{" "}
            <span className="text-tally-title">{copy.partyGroup}</span> and
            nothing else. It is not a formatting rule: the {copy.bookName} groups
            by counterparty to answer who is owed what, and an order committed to a
            bank, a tax head or an expense account names a party that can never be
            invoiced and can never deliver.
            {partyLedgers.length === 0 && !offerEveryLedger ? (
              <>
                {" "}
                <span className="text-tally-bar">
                  This company has no ledger under {copy.partyGroup} yet, so there
                  is nobody to commit to. Create one in Gateway &gt; Create Master,
                  or tick the box below if the {copy.partyNoun.toLowerCase()} is
                  kept somewhere else.
                </span>
              </>
            ) : null}
          </p>
          <label className="mt-1 flex items-center gap-2 text-xs">
            <input
              type="checkbox"
              aria-label="Offer every ledger, not only this direction's party group"
              checked={offerEveryLedger}
              onChange={(e) => {
                const wide = e.target.checked;
                setOfferEveryLedger(wide);
                // ⛔ A CHOICE THAT HAS LEFT THE LIST MUST LEAVE THE FIELD. Narrowing
                // back with a bank still selected would send it — the request carries
                // the id, not the option — and the operator would meet a 422 for a
                // party the form no longer shows them.
                if (!wide) {
                  setPartyID((current) =>
                    ledgers.some(
                      (l) =>
                        l.id === current &&
                        l.group_name.trim().toLowerCase() ===
                          copy.partyGroup.toLowerCase(),
                    )
                      ? current
                      : "",
                  );
                }
              }}
            />
            <span>
              Offer every ledger — for a {copy.partyNoun.toLowerCase()} kept in a
              SUB-GROUP of {copy.partyGroup}, which this list cannot see (it
              carries each ledger&apos;s immediate group only). The server still
              refuses anything outside {copy.partyGroup} and says so.
            </span>
          </label>

          <label className="mt-3 flex flex-col text-xs">
            <span className="text-tally-title">Narration</span>
            <input
              aria-label="Narration"
              className="mt-1 w-full max-w-2xl bg-tally-field px-1 text-tally-fieldText outline-none"
              value={narration}
              onChange={(e) => setNarration(e.target.value)}
            />
          </label>

          {/* ---- THE LINE GRID. No line numbers are asked for or sent. ------ */}
          <table className="mt-3 w-full text-xs">
            <thead>
              <tr className="text-left text-tally-title">
                {/* The POSITION is shown so an operator can talk about "line 2",
                    and it is derived from the row's index here exactly as the
                    server derives it from the array — never keyed, never sent. */}
                <th className="pr-2">#</th>
                <th className="pr-2">Item</th>
                <th className="pr-2">Quantity</th>
                <th className="pr-2">Rate (optional)</th>
                <th className="pr-2">Due on (optional)</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {lines.map((line, idx) => (
                <tr key={idx} data-testid={`${testid}-line:${idx}`}>
                  <td className="pr-2">{idx + 1}</td>
                  <td className="pr-2">
                    <select
                      aria-label={`Item on line ${idx + 1}`}
                      className="w-56 bg-tally-field px-1 text-tally-fieldText outline-none"
                      value={line.stock_item_id}
                      onChange={(e) =>
                        patchLine(idx, { stock_item_id: e.target.value })
                      }
                    >
                      <option value="">— choose —</option>
                      {items.map((s) => (
                        <option key={s.id} value={s.id}>
                          {s.name}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td className="pr-2">
                    <input
                      aria-label={`Quantity on line ${idx + 1}`}
                      className="w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                      value={line.qty}
                      onChange={(e) => patchLine(idx, { qty: e.target.value })}
                    />
                  </td>
                  <td className="pr-2">
                    <input
                      aria-label={`Rate on line ${idx + 1}`}
                      className="w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                      value={line.rate}
                      onChange={(e) => patchLine(idx, { rate: e.target.value })}
                    />
                  </td>
                  <td className="pr-2">
                    <input
                      type="date"
                      aria-label={`Due date on line ${idx + 1}`}
                      className="bg-tally-field px-1 text-tally-fieldText outline-none"
                      value={line.due_date}
                      onChange={(e) =>
                        patchLine(idx, { due_date: e.target.value })
                      }
                    />
                  </td>
                  <td>
                    <button
                      type="button"
                      aria-label={`Remove line ${idx + 1}`}
                      className="border border-tally-fieldText px-1"
                      onClick={() => removeLine(idx)}
                    >
                      −
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <button
            type="button"
            className="mt-2 border border-tally-fieldText px-2 py-0.5 text-xs"
            onClick={addLine}
          >
            + Add a line
          </button>

          <p className="mt-2 max-w-3xl text-xs">
            A blank <span className="text-tally-title">Due on</span> means no
            delivery date was agreed, which is NOT overdue however long the order
            stands open — it is left blank rather than defaulted, because a default
            would invent a deadline nobody set. A blank{" "}
            <span className="text-tally-title">Rate</span> means no rate was agreed;
            it is not sent as zero. And the rate agreed here is an INPUT to the order
            book&apos;s value column, never a valuation: the invoice that fulfils
            this line may lawfully bill a different rate, and that difference is a
            finding to show rather than a disagreement to resolve by overwriting
            either figure.
          </p>

          <div className="mt-3 flex items-center gap-3">
            <button
              type="submit"
              className="border border-tally-fieldText px-2 py-0.5 text-xs"
            >
              Ctrl+A — Accept
            </button>
            <button
              type="button"
              className="border border-tally-fieldText px-2 py-0.5 text-xs"
              onClick={() => {
                setCreating(false);
                setRefusal("");
                setWriteDenied(false);
              }}
            >
              Close the panel
            </button>
          </div>
        </form>
      ) : null}

      {refusal !== "" ? (
        /* THE SERVER'S SENTENCE, verbatim — refusal (6). */
        <p
          data-testid={`${testid}-refusal`}
          className="mt-3 max-w-3xl border border-tally-bar p-2 text-sm text-tally-bar"
        >
          {refusal}
        </p>
      ) : null}

      {writeDenied ? (
        <p
          data-testid={`${testid}-write-denied`}
          className="mt-2 max-w-3xl border border-tally-fieldText p-2 text-sm"
        >
          That is a decision, not a fault. Displaying orders and recording them are
          SEPARATE actions on one{" "}
          <span className="text-tally-title">
            {direction === "SALES" ? "sales-order" : "purchase-order"}
          </span>{" "}
          token, so a level that may read this company&apos;s commitments and may not
          make new ones is a real configuration — and you are in it. Nothing you
          keyed has been lost or sent.
        </p>
      ) : null}

      {/* ---- THE REGISTER. ------------------------------------------------- */}
      {data !== null ? (
        <>
          <div className="mt-4 flex items-center gap-3 text-xs">
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                aria-label="Show only orders with something outstanding"
                checked={openOnly}
                onChange={(e) => {
                  setOpenOnly(e.target.checked);
                  // The highlight is dropped with the filter: an order that has
                  // just left the list must not stay selected, or Alt+W would act
                  // on a row nobody can see.
                  setSelectedID("");
                  setClosing(false);
                }}
              />
              <span>Only orders with something outstanding</span>
            </label>
          </div>

          {/* ⛔ THE HEADING IS RENDERED FROM THE RESPONSE'S ECHO, never from the
              checkbox. "No orders" and "no OPEN orders" are different answers, and
              a filtered list under an unfiltered heading is how the second gets
              read as the first — which is why the server echoes the filter at all. */}
          <p className="mt-2 text-xs text-tally-title">
            {data.open_only
              ? "Orders with something still outstanding"
              : "Every order on this register, outstanding or not"}
            {" · "}
            {orders.length} order(s)
          </p>

          {orders.length === 0 ? (
            <p
              data-testid={`${testid}-empty`}
              className="mt-2 max-w-3xl text-sm"
            >
              {data.open_only
                ? "Nothing is outstanding on this register. That is not the same as having no orders — untick the box above to see every order, including those already fulfilled in full and those pre-closed."
                : `This company has no ${copy.title.toLowerCase()} at all. Alt+C records the first one. Nothing will move when you do: an order is a commitment, and it is discharged later by a delivery that names it.`}
            </p>
          ) : (
            <table
              className="mt-2 w-full max-w-5xl text-sm"
              data-testid={`${testid}-table`}
            >
              <thead>
                <tr className="text-left text-xs text-tally-title">
                  <th className="pr-3">Order</th>
                  <th className="pr-3">Date</th>
                  <th className="pr-3">{copy.partyNoun}</th>
                  <th className="pr-3">Narration</th>
                  <th className="pr-3">State</th>
                </tr>
              </thead>
              <tbody>
                {orders.map((o) => (
                  <tr
                    key={o.order_id}
                    data-testid={`${testid}-row:${o.order_no}`}
                    aria-selected={o.order_id === selectedID}
                    className={
                      o.order_id === selectedID
                        ? "bg-tally-panel text-tally-title"
                        : "cursor-pointer"
                    }
                    onClick={() => {
                      setSelectedID(o.order_id);
                      setClosing(false);
                    }}
                  >
                    <td className="pr-3">
                      <button
                        type="button"
                        aria-label={`Select order ${o.order_no}`}
                        className="underline"
                        onClick={(e) => {
                          e.stopPropagation();
                          setSelectedID(o.order_id);
                          setClosing(false);
                        }}
                      >
                        {o.order_no}
                      </button>
                    </td>
                    <td className="pr-3">{o.order_date}</td>
                    <td className="pr-3">{o.party}</td>
                    <td className="pr-3">{o.narration}</td>
                    <td className="pr-3">
                      {/* ⛔ "Pre-closed" AND ITS REASON, never just a flag. A
                          closed order whose reason is not on screen is a
                          commitment that vanished for no stated cause. */}
                      {o.is_closed ? (
                        <span className="text-tally-bar">
                          pre-closed — {o.closed_reason || "(no reason recorded)"}
                        </span>
                      ) : (
                        "open"
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          {/* ---- THE HIGHLIGHTED ORDER'S LINES. ---------------------------- */}
          {detailError !== "" ? (
            <p
              data-testid={`${testid}-detail-error`}
              className="mt-3 max-w-3xl border border-tally-bar p-2 text-sm text-tally-bar"
            >
              {detailError}
            </p>
          ) : null}

          {detail ? (
            <div className="mt-4 max-w-5xl">
              {/* ⛔⛔ THE PRE-CLOSED STATE BELONGS ON THE DETAIL, NOT ONLY ON THE
                  ROW. Alt+W pre-closes the highlighted order, `load()` drops it
                  from the open-only list — and this panel went on showing its
                  balance under a column headed "Outstanding", with no marker
                  anywhere, while the row it came from had vanished from the table
                  above. An operator was left reading a live-looking commitment
                  belonging to an order that no longer appeared to exist.

                  It is rendered from `detail.is_closed` and NOT from `selected`,
                  which is exactly the point: `selected` is looked up in the LIST,
                  and after a pre-close the order is not in it. */}
              <h2 className="font-bold text-tally-title">
                {detail.order_no} · {detail.party}
                {detail.is_closed ? (
                  <span
                    data-testid={`${testid}-detail-preclosed`}
                    className="ml-2 font-normal text-tally-bar"
                  >
                    · PRE-CLOSED —{" "}
                    {detail.closed_reason || "(no reason recorded)"}
                  </span>
                ) : null}
              </h2>
              {detail.is_closed ? (
                <p
                  data-testid={`${testid}-detail-preclosed-note`}
                  className="mt-1 max-w-3xl border border-tally-bar p-2 text-xs"
                >
                  ⛔ THIS ORDER IS ABANDONED, NOT DELIVERED. The balance below is
                  what was still owed when it was pre-closed, and it is NOT
                  outstanding any more: it has left the {copy.bookName} and nobody
                  is waiting for it. Every delivery already recorded against it is
                  untouched, which is why the {copy.fulfilledNoun.toLowerCase()}{" "}
                  column still reads what it reads.
                  {data.open_only ? (
                    <>
                      {" "}
                      The order has also left the list above, because that list is
                      filtered to orders with something outstanding — untick{" "}
                      <span className="text-tally-title">
                        Only orders with something outstanding
                      </span>{" "}
                      to bring it back, where Alt+W RE-OPENS it and clears the
                      reason.
                    </>
                  ) : null}
                </p>
              ) : null}
              <table
                className="mt-1 w-full text-sm"
                data-testid={`${testid}-lines`}
              >
                <thead>
                  <tr className="text-left text-xs text-tally-title">
                    <th className="pr-3">#</th>
                    <th className="pr-3">Item</th>
                    <th className="pr-3 text-right">Ordered</th>
                    <th className="pr-3 text-right">{copy.fulfilledNoun}</th>
                    {/* ⛔ THE COLUMN IS RENAMED, NOT BLANKED, ON A PRE-CLOSED
                        ORDER. The figure is real and must stay on screen — it is
                        what was still owed when the commitment was abandoned, and
                        it is the number an auditor asks about. What must not stay
                        is the WORD "Outstanding", which claims somebody is still
                        waiting for goods nobody is going to send. */}
                    <th className="pr-3 text-right">
                      {detail.is_closed ? "Abandoned" : "Outstanding"}
                    </th>
                    <th className="pr-3 text-right">Rate</th>
                    <th className="pr-3">Due on</th>
                  </tr>
                </thead>
                <tbody>
                  {detail.lines.map((l) => (
                    <tr
                      key={l.order_line_id}
                      data-testid={`${testid}-line-row:${detail.order_no}:${l.line_no}`}
                    >
                      {/* The line number the SERVER assigned, read back. This is
                          the number an operator quotes when they ask which line is
                          short — and the one place it is ever displayed. */}
                      <td className="pr-3">{l.line_no}</td>
                      <td className="pr-3">{l.stock_item}</td>
                      <td className="pr-3 text-right">{l.qty}</td>
                      {/* ⛔ BOTH ARRIVE DERIVED. Neither is subtracted here — see
                          refusal (5). */}
                      <td className="pr-3 text-right">{l.fulfilled_qty}</td>
                      <td
                        className={`pr-3 text-right ${detail.is_closed ? "text-tally-bar" : "text-tally-title"}`}
                        title={
                          detail.is_closed
                            ? "Abandoned when this order was pre-closed. It is not outstanding and nobody is waiting for it."
                            : undefined
                        }
                      >
                        {l.outstanding_qty}
                      </td>
                      <td className="pr-3 text-right">{formatMoney(l.rate)}</td>
                      <td className="pr-3">
                        {l.due_date === undefined || l.due_date === "" ? (
                          <span title="No delivery date was agreed on this line, so it cannot be overdue.">
                            none agreed
                          </span>
                        ) : (
                          l.due_date
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="mt-2 max-w-3xl text-xs">
                {copy.fulfilledNoun} is DERIVED — the server sums the deliveries
                linked to each line across CURRENT, non-deleted voucher versions,
                every time this is read. There is no stored figure to go stale, which
                matters because altering a delivery note changes these numbers
                without touching the order at all. Partial fulfilment is the ordinary
                case and the outstanding balance is the entire point.
              </p>
            </div>
          ) : selectedID !== "" && detailError === "" ? (
            <p className="mt-3 text-sm">Reading the order&apos;s lines…</p>
          ) : orders.length > 0 ? (
            <p className="mt-3 text-xs">
              Highlight an order to see its lines and what is still outstanding on
              each of them. Alt+W pre-closes the highlighted one.
            </p>
          ) : null}

          <p className="mt-5 max-w-3xl text-xs">
            THERE IS NO WAY TO ALTER AN ORDER&apos;S LINES ON THIS SCREEN, and it is
            said here rather than left to be discovered. A committed quantity can be
            revised server-side, but no route calls that service method yet, so a
            mis-keyed quantity is corrected today by pre-closing the order with a
            reason and recording the right one. What is NOT a way to correct it: a
            second order for the difference — that commits the company twice.
          </p>
        </>
      ) : listError === "" ? (
        <p className="mt-3 text-sm">Loading…</p>
      ) : null}
    </section>
  );
}

/** The SALES order register — Ctrl+F8, and the `sales-order` authority. */
export function SalesOrdersScreen() {
  return <OrderRegisterScreen direction="SALES" />;
}

/** The PURCHASE order register — Ctrl+F9, and the `purchase-order` authority. */
export function PurchaseOrdersScreen() {
  return <OrderRegisterScreen direction="PURCHASE" />;
}
