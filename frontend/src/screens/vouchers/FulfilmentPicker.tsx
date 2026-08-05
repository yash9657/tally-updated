import { useCallback, useEffect, useMemo, useState } from "react";

import { useClient, useCompanyID } from "../../app/CompanyContext";
import { ApiError } from "../../api";
import type { OrderDirection, OrderLineView, OrderView } from "../../api";
import {
  emptyFulfilment,
  fulfilmentWarning,
  type DraftFulfilment,
  type FulfillableLine,
} from "./fulfilment";

/**
 * THE FULFILMENT PICKER — how an operator links the goods on a voucher line to the
 * order lines they discharge (T3, migration 0042).
 *
 * Until this existed, `voucherReq.fulfilments` was on the wire, `FulfilmentInput`
 * was in types.ts, over-fulfilment was refused by a deferred trigger and both order
 * books read the links back — and a keyboard-only operator could not create a single
 * one. The order book therefore reported every order as fully outstanding forever,
 * correctly, because nothing could ever discharge one.
 *
 * ⛔⛔ THE TWO RULES THIS COMPONENT EXISTS TO OBEY.
 *
 *  1. IT NEVER ASKS FOR AN IDENTIFIER. Not an `order_line_id`, and not a `line_no`.
 *     Both are identifiers NO SCREEN IN THIS PRODUCT DISPLAYS, so a box for either
 *     could not be filled by a human — the defect the TDS deposit form shipped with
 *     and had to be rebuilt for, and the defect `voucherReq.ItemLedgerID` shipped as
 *     when every item invoice the grid could send came back 422. The operator picks
 *     an ORDER by its number and a LINE by its item; `line_no` is derived from the
 *     row's own position by {@link buildFulfilments}, never keyed.
 *
 *  2. IT NEVER PRE-FILLS THE LINK QUANTITY WITH THE MOVEMENT QUANTITY AND LOCKS IT.
 *     A fulfilment qty is a LINK, not a movement. Shipping 105 against a 100-unit
 *     order records a movement of 105 and a link of 100 — the extra 5 consumed no
 *     order, because nobody ordered it. A locked field would make that legitimate
 *     over-delivery unsaveable. There IS a one-click "link what is outstanding"
 *     affordance, because typing the number by hand every time is the friction that
 *     makes an operator skip the link entirely; it is a BUTTON the operator presses,
 *     not a default, and the field stays free-typed afterwards.
 *
 * ⚠️ WHICH ORDERS ARE OFFERED, AND WHY IT IS NARROW. Only OPEN orders
 * (`open_only=true`), only lines of the SAME STOCK ITEM as the voucher line, and only
 * the family the goods' direction can discharge (see `fulfilsDirection`). Offering an
 * order line for different goods would be offering the operator a mistake to make —
 * the server refuses it, but by then they have keyed a delivery note.
 *
 * ⚠️ AND WHAT IS NOT RE-CHECKED HERE. `outstanding_qty` is DERIVED server-side on
 * every read and is not stored anywhere. It is shown, and it is not enforced against:
 * altering an unrelated delivery note changes it without touching this voucher, so a
 * client-side limit would be stale the moment it was drawn. The server and a deferred
 * database trigger are what refuse an over-fulfilment, and they are the only two
 * things that can.
 */

/** One candidate: an open order line of the right item, with its order's number. */
interface Candidate {
  orderLineId: string;
  orderNo: string;
  itemName: string;
  outstanding: string;
  qty: string;
  dueDate: string;
}

/**
 * Load the open orders of one direction and flatten them to candidate LINES.
 *
 * Two round trips by design: the list carries no lines (loading every line of every
 * order to render a picker is how a list of a thousand orders becomes a request that
 * times out), so the heads come first and each open order's lines follow.
 */
function useCandidates(direction: OrderDirection | null): {
  byItem: Map<string, Candidate[]>;
  error: string;
  loading: boolean;
} {
  const client = useClient();
  const companyID = useCompanyID();
  const [orders, setOrders] = useState<OrderView[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (direction === null) {
      setOrders([]);
      return;
    }
    let live = true;
    setLoading(true);
    setError("");
    const list =
      direction === "SALES"
        ? client.listSalesOrders(companyID, true)
        : client.listPurchaseOrders(companyID, true);
    const get =
      direction === "SALES"
        ? client.getSalesOrder.bind(client)
        : client.getPurchaseOrder.bind(client);
    list
      .then(async (res) =>
        Promise.all(res.orders.map((o) => get(companyID, o.order_id))),
      )
      .then((full) => {
        if (live) setOrders(full);
      })
      .catch((e: unknown) => {
        if (!live) return;
        setOrders([]);
        // ⚠️ A 403 IS NORMAL AND IS NOT AN ERROR TO SHOUT ABOUT. A level may be
        // allowed to key a delivery note and refused sight of the order book — the
        // two are separate authorities. The picker then says the orders cannot be
        // read, and the voucher SAVES WITHOUT LINKS rather than becoming unusable.
        setError(
          e instanceof ApiError && e.status === 403
            ? "You are not permitted to read this company's orders, so no order can " +
                "be linked here. The voucher itself is unaffected: it will save, move " +
                "stock and post normally — it simply will not discharge an order, and " +
                "the order book will go on showing the commitment as outstanding."
            : e instanceof Error
              ? e.message
              : String(e),
        );
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [client, companyID, direction]);

  const byItem = useMemo(() => {
    const m = new Map<string, Candidate[]>();
    for (const o of orders) {
      if (o.is_closed) continue;
      for (const l of o.lines as OrderLineView[]) {
        // Nothing left on this line: it is discharged, and offering it would be
        // offering a link the server refuses.
        if (Number.parseFloat(l.outstanding_qty) <= 0) continue;
        const list = m.get(l.stock_item_id) ?? [];
        list.push({
          orderLineId: l.order_line_id,
          orderNo: o.order_no,
          itemName: l.stock_item,
          outstanding: l.outstanding_qty,
          qty: l.qty,
          dueDate: l.due_date ?? "",
        });
        m.set(l.stock_item_id, list);
      }
    }
    // Oldest commitment first: the order that has been waiting longest is the one an
    // operator shipping today means to discharge.
    for (const list of m.values()) {
      list.sort((a, b) => a.orderNo.localeCompare(b.orderNo));
    }
    return m;
  }, [orders]);

  return { byItem, error, loading };
}

/**
 * The per-row picker. Rendered inside the grid, on the row the operator is already
 * looking at — not in a separate modal, because the link belongs to the goods on
 * that line and a panel that made the operator re-identify the line would be asking
 * for the identifier this component refuses to ask for.
 */
export function FulfilmentPicker({
  line,
  lineIndex,
  direction,
  onChange,
  testIdPrefix,
}: {
  line: FulfillableLine;
  /** The GRID row index — used only for labels and test ids, never sent. */
  lineIndex: number;
  /** Which family this voucher class can discharge, or null (renders nothing). */
  direction: OrderDirection | null;
  onChange: (fulfilments: DraftFulfilment[]) => void;
  /** "item" or "stock", so the two grids' test ids do not collide. */
  testIdPrefix: string;
}) {
  const { byItem, error, loading } = useCandidates(direction);
  const [open, setOpen] = useState(false);

  const candidates = useMemo(
    () => (line.stockItemId === "" ? [] : (byItem.get(line.stockItemId) ?? [])),
    [byItem, line.stockItemId],
  );

  const links = line.fulfilments;
  const warning = fulfilmentWarning(line);

  const patch = useCallback(
    (idx: number, p: Partial<DraftFulfilment>) =>
      onChange(links.map((f, i) => (i === idx ? { ...f, ...p } : f))),
    [links, onChange],
  );

  // Nothing to render for a class that discharges no commitment (see
  // fulfilsDirection: a transfer, a count and a return each for its own reason).
  if (direction === null) return null;

  const id = `${testIdPrefix}-fulfilment:${lineIndex}`;

  // A row with no item chosen cannot have candidates, and saying that is better than
  // an empty picker that reads as "there are no orders".
  if (line.stockItemId === "") {
    return (
      <span data-testid={`${id}-noitem`} className="text-xs">
        —
      </span>
    );
  }

  return (
    <div data-testid={id} className="text-xs">
      {links.length === 0 ? (
        <button
          type="button"
          data-testid={`${id}-add`}
          aria-label={`Link line ${lineIndex + 1} to an order`}
          className="underline"
          onClick={() => {
            setOpen(true);
            onChange([...links, emptyFulfilment()]);
          }}
          disabled={candidates.length === 0}
        >
          {loading
            ? "reading orders…"
            : candidates.length === 0
              ? "no open order"
              : `link (${candidates.length})`}
        </button>
      ) : null}

      {links.length === 0 && candidates.length === 0 && !loading ? (
        <p data-testid={`${id}-none`} className="mt-0.5 text-tally-field">
          {/* NOT an error, and NOT silence. An operator who expected to discharge an
              order needs to know whether there is nothing open for this item or
              whether the orders could not be read at all. */}
          {error !== ""
            ? "orders unreadable"
            : "nothing open for this item"}
        </p>
      ) : null}

      {links.map((f, i) => (
        <div key={i} className="mt-0.5 flex flex-wrap items-center gap-1">
          {/* THE ORDER LINE — PICKED, by order number and item. Never an id. */}
          <select
            aria-label={`Order for line ${lineIndex + 1}, link ${i + 1}`}
            data-testid={`${id}-order:${i}`}
            className="bg-tally-field px-1 text-tally-fieldText"
            value={f.orderLineId}
            onChange={(e) => {
              const c = candidates.find((x) => x.orderLineId === e.target.value);
              patch(i, {
                orderLineId: e.target.value,
                orderNo: c?.orderNo ?? "",
                itemName: c?.itemName ?? "",
                outstandingAtPick: c?.outstanding ?? "",
                // ⛔ THE QTY IS NOT SET HERE. Picking the order does not decide how
                // much of it this delivery discharges — see the header. The operator
                // types it, or presses the button beside it.
              });
            }}
          >
            <option value="">— choose an order —</option>
            {candidates.map((c) => (
              <option key={c.orderLineId} value={c.orderLineId}>
                {c.orderNo} · {c.itemName} · {c.outstanding} outstanding
                {c.dueDate === "" ? "" : ` · due ${c.dueDate}`}
              </option>
            ))}
          </select>

          <input
            aria-label={`Quantity linked on line ${lineIndex + 1}, link ${i + 1}`}
            data-testid={`${id}-qty:${i}`}
            className="w-20 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
            value={f.qty}
            placeholder="qty"
            onChange={(e) => patch(i, { qty: e.target.value })}
          />

          {/* THE AFFORDANCE, NOT A DEFAULT. It fills the field with what the ORDER
              has outstanding — which is the common case — and leaves it editable.
              Note it does NOT fill in the movement quantity: linking is capped by
              what the order still wants, and the two differ precisely in the
              over-delivery case this component must keep saveable. */}
          {f.orderLineId !== "" ? (
            <button
              type="button"
              data-testid={`${id}-fill:${i}`}
              aria-label={`Link the whole outstanding balance on line ${lineIndex + 1}, link ${i + 1}`}
              className="underline"
              onClick={() => patch(i, { qty: f.outstandingAtPick })}
            >
              all {f.outstandingAtPick}
            </button>
          ) : null}

          <button
            type="button"
            data-testid={`${id}-remove:${i}`}
            aria-label={`Remove link ${i + 1} on line ${lineIndex + 1}`}
            className="underline"
            onClick={() => onChange(links.filter((_, idx) => idx !== i))}
          >
            ×
          </button>
        </div>
      ))}

      {/* A SECOND LINK ON ONE LINE IS ORDINARY: one delivery of 30 can discharge a
          20-unit order and 10 of a second. The server checks the SUM against the
          movement, not each link. */}
      {links.length > 0 && candidates.length > links.length ? (
        <button
          type="button"
          data-testid={`${id}-more`}
          aria-label={`Link line ${lineIndex + 1} to another order`}
          className="mt-0.5 underline"
          onClick={() => onChange([...links, emptyFulfilment()])}
        >
          + another order
        </button>
      ) : null}

      {warning !== "" ? (
        /* ⚠️ A WARNING, NEVER A REFUSAL — see fulfilmentWarning. Over-linking says
           the server will refuse it; under-linking EXPLAINS itself and implies
           nothing is wrong, because usually nothing is. */
        <p data-testid={`${id}-warning`} className="mt-0.5 max-w-md text-tally-bar">
          {warning}
        </p>
      ) : null}

      {error !== "" && links.length > 0 ? (
        <p data-testid={`${id}-error`} className="mt-0.5 max-w-md text-tally-bar">
          {error}
        </p>
      ) : null}

      {open && candidates.length > 0 ? (
        <p data-testid={`${id}-hint`} className="mt-0.5 max-w-md text-tally-field">
          Linking records WHICH COMMITMENT these goods discharged. It does not change
          what moves: the quantity in the Qty column is what leaves the godown either
          way. Link less than you moved and the difference simply consumed no order.
        </p>
      ) : null}
    </div>
  );
}
