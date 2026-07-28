import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { NavSnapshot } from "../../keystroke/navMachine";
import type { VoucherType } from "../../keystroke/types";
import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import { useLoadedVoucher } from "../../app/LoadedVoucher";
import { ApiError } from "../../api";
import type {
  GodownListItem,
  StockItemListItem,
  VoucherRequest,
  VoucherResponse,
} from "../../api";
import { configFor } from "./voucherConfig";
import {
  buildStockVoucherRequest,
  emptyStockLine,
  isKeyedStockLine,
  isStockVoucherType,
  stockAlterRefusal,
  stockShapeFor,
  type StockVoucherLine,
  type StockVoucherType,
} from "./stockVoucher";
import { formatFixed4, mulFixed4, parseFixed4 } from "./itemInvoice";

/**
 * THE STOCK VOUCHER SCREENS (Alt+F8 Delivery Note, Alt+F9 Receipt Note,
 * Alt+F7 Stock Journal, Alt+F10 Physical Stock).
 *
 * A separate component from {@link VoucherScreen} rather than a fifth branch
 * inside it, because these documents share almost nothing with an accounting
 * voucher: NO party ledger field, NO particulars, NO GST panel, NO amount — the
 * server refuses every one of those on these classes. Rendering the accounting
 * form with its money fields hidden would leave the form state that feeds the
 * accounting build paths alive behind them, one Ctrl+H away from being posted.
 *
 * WHAT THE OPERATOR SEES, BY CLASS:
 *  - Delivery / Receipt Note — item, godown, qty, rate. NO direction column:
 *    the class fixes it, and a Receipt Note that could ship goods out is a
 *    goods-inward document that removes stock.
 *  - Stock Journal — item, FROM godown, TO godown, qty, rate. One keyed row
 *    becomes a PAIR of movements, so the transfer cannot come out unbalanced.
 *  - Physical Stock — item, godown, COUNTED qty, rate. The rate column is there
 *    because a surplus has to be valued; a shortage ignores it.
 */
export function StockVoucherScreen({ snapshot }: { snapshot: NavSnapshot }) {
  const client = useClient();
  const companyID = useCompanyID();
  const { loaded } = useLoadedVoucher();

  const type = (snapshot.voucherType ?? "delivery-note") as StockVoucherType;
  const cfg = configFor(type as VoucherType);
  const shape = stockShapeFor(type);
  const mode = snapshot.mode ?? "create";
  const isAlter = mode === "alter";
  const loadedVoucherID = isAlter ? (loaded?.entry?.voucher_id ?? null) : null;

  const [lines, setLines] = useState<StockVoucherLine[]>(() => [emptyStockLine()]);
  const [date, setDate] = useState<string>(() => new Date().toISOString().slice(0, 10));
  const [narration, setNarration] = useState("");
  const [party, setParty] = useState("");
  const [voucherNo, setVoucherNo] = useState("");
  const [savedNo, setSavedNo] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  /**
   * A refusal by the server's SECURITY LEVEL, kept separate from `status`.
   * Migration 0031 explicitly disallows `create physical-stock` to the seeded
   * Data Entry level, so a 403 here is a POLICY ANSWER, not a fault — and an
   * operator shown a blank screen files a bug where an operator told which rule
   * refused them asks their Owner.
   */
  const [denied, setDenied] = useState<string | null>(null);

  // The masters the grid picks from. A failure is SURFACED, never swallowed: an
  // empty item picker with no explanation reads as "this company stocks
  // nothing", which is a different and much more alarming statement than "the
  // list could not be loaded".
  const [stockItems, setStockItems] = useState<StockItemListItem[]>([]);
  const [godowns, setGodowns] = useState<GodownListItem[]>([]);
  const [mastersError, setMastersError] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    Promise.all([client.listStockItems(companyID), client.listGodowns(companyID)])
      .then(([items, gd]) => {
        if (!live) return;
        setStockItems(items.stock_items);
        setGodowns(gd.godowns);
        setMastersError(null);
      })
      .catch((e: unknown) => {
        if (!live) return;
        setMastersError(
          e instanceof ApiError
            ? `Stock masters unavailable (${e.status}). ${e.message}`
            : e instanceof Error
              ? e.message
              : String(e),
        );
      });
    return () => {
      live = false;
    };
  }, [client, companyID]);

  /**
   * THE ALTER PATH, AND THE ONE CLASS THAT CANNOT TAKE IT.
   *
   * GET /vouchers/{id} projects a Physical Stock voucher's DELTA movements —
   * the correction the server derived, not the count the operator keyed, which
   * is not stored anywhere. Seeding this form from those and re-posting would
   * restate the correction AS IF it were the count: a shelf holding 13 that was
   * corrected by +3 would be re-declared as holding 3. So the alter is refused
   * LOUDLY here rather than allowed to silently corrupt the position it exists
   * to correct.
   */
  const alterRefusal = isAlter ? stockAlterRefusal(type) : null;

  const seedRef = useRef<string | null>(null);
  useEffect(() => {
    if (!loadedVoucherID || alterRefusal || seedRef.current === loadedVoucherID) return;
    let cancelled = false;
    client
      .getVoucher(companyID, loadedVoucherID)
      .then((view) => {
        if (cancelled) return;
        seedRef.current = loadedVoucherID;
        setDate(view.voucher_date);
        setVoucherNo(view.voucher_no);
        setNarration(view.narration);
        setParty(view.party);
        setLines(linesFromView(view.inventory ?? [], shape.kind));
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setStatus(
            `Error loading voucher: ${err instanceof Error ? err.message : String(err)}`,
          );
        }
      });
    return () => {
      cancelled = true;
    };
  }, [client, companyID, loadedVoucherID, alterRefusal, shape.kind]);

  // Keep the live form in refs so the registered Ctrl+A handler stays stable
  // across renders and still reads the current grid.
  const formRef = useRef({ lines, date, narration, party, voucherNo });
  formRef.current = { lines, date, narration, party, voucherNo };
  const inFlight = useRef(false);

  const ctx = useMemo(
    () => ({ type, voucherID: loadedVoucherID }),
    [type, loadedVoucherID],
  );
  const ctxRef = useRef(ctx);
  ctxRef.current = ctx;

  const onAccept = useCallback(() => {
    if (inFlight.current) return;
    const refusal = ctxRef.current.voucherID ? stockAlterRefusal(type) : null;
    if (refusal) {
      setStatus(refusal);
      return;
    }
    const f = formRef.current;
    let req: VoucherRequest;
    try {
      req = buildStockVoucherRequest({
        type,
        lines: f.lines,
        voucherDate: f.date,
        narration: f.narration,
        party: f.party,
        ...(f.voucherNo ? { voucherNo: f.voucherNo } : {}),
      });
    } catch (err: unknown) {
      // A body this form knows is wrong is refused HERE, with a domain message,
      // rather than sent for the server to reject: "a Receipt Note moves stock
      // IN, not OUT" tells the operator what they did; a 422 from the wire does
      // not survive the round trip in that shape.
      setStatus(err instanceof Error ? err.message : String(err));
      return;
    }
    inFlight.current = true;
    setDenied(null);
    const voucherID = ctxRef.current.voucherID;
    const send: Promise<VoucherResponse> = voucherID
      ? cfg.alter(client, companyID, voucherID, req)
      : cfg.post(client, companyID, req);
    send
      .then((res) => {
        setSavedNo(res.voucher_no);
        setStatus("Saved");
      })
      .catch((err: unknown) => {
        if (err instanceof ApiError && err.status === 403) {
          setDenied(
            `${cfg.title} is refused to your security level. It is the document that makes the books agree with a number a human asserts, so it is granted to supervisors by default — ask an Owner to allow "create ${type}" on your level (Users & Roles).`,
          );
          setStatus(null);
          return;
        }
        setStatus(`Error: ${err instanceof Error ? err.message : String(err)}`);
      })
      .finally(() => {
        inFlight.current = false;
      });
  }, [client, companyID, cfg, type]);

  useRegisterScreenAction("accept", onAccept);
  // No `delete` handler and no `duplicate`: those belong to the accounting
  // screen's edit actions, and wiring them here without a tested path would be
  // a button that looks live. Alt+D on this screen therefore says nothing ran,
  // which is the honest answer.

  const setRow = (i: number, patch: Partial<StockVoucherLine>) =>
    setLines((rows) => rows.map((l, idx) => (idx === i ? { ...l, ...patch } : l)));

  const keyed = lines.filter((l) => isKeyedStockLine(l, shape));
  const totalQty = keyed.reduce((sum, l) => sum + parseFixed4(l.qty), 0n);

  return (
    <section
      className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field"
      data-testid="stock-voucher-screen"
    >
      <header className="mb-3 flex items-center justify-between">
        <h1 className="text-lg font-bold text-tally-title">
          {cfg.title}
          {isAlter ? " (Alteration)" : ""}
        </h1>
        <div className="flex items-center gap-3">
          {status ? (
            <span className="text-tally-active" role="status" data-testid="stock-status">
              {status}
            </span>
          ) : null}
          {savedNo ? (
            <span className="text-tally-title" data-testid="voucher-no">
              No: {savedNo}
            </span>
          ) : null}
        </div>
      </header>

      <p className="mb-2 max-w-3xl text-xs">{cfg.hint}</p>
      {/* Said on every one of these screens, because it is the property that
          makes them different from every other voucher in the product. */}
      <p className="mb-2 text-xs" data-testid="stock-no-postings">
        No accounting entry is made by this voucher — only stock moves.
      </p>

      {alterRefusal ? (
        <p className="mb-2 max-w-3xl text-xs text-red-400" data-testid="stock-alter-refused">
          {alterRefusal}
        </p>
      ) : null}
      {denied ? (
        <p className="mb-2 max-w-3xl text-xs text-red-400" data-testid="stock-denied">
          {denied}
        </p>
      ) : null}
      {mastersError ? (
        <p className="mb-2 text-xs text-red-400" data-testid="stock-masters-error">
          {mastersError}
        </p>
      ) : stockItems.length === 0 ? (
        <p className="mb-2 text-xs" data-testid="stock-items-empty">
          This company has no stock items yet — create one (Gateway &gt; Masters)
          before keying a {cfg.title.toLowerCase()}.
        </p>
      ) : null}

      <div className="bg-tally-panel p-3">
        <FieldRow name="Date" value={date} testId="stock-date" onChange={setDate} />
        <FieldRow
          name="No."
          value={savedNo ?? voucherNo}
          testId="stock-voucher-no"
          placeholder="auto"
          onChange={setVoucherNo}
        />
        <FieldRow
          name={cfg.partyLabel}
          value={party}
          testId="stock-party"
          onChange={setParty}
        />
        <FieldRow
          name="Narration"
          value={narration}
          testId="stock-narration"
          onChange={setNarration}
        />
      </div>

      <div className="mt-3 bg-tally-panel p-3 text-sm" data-testid="stock-grid">
        <table className="w-full max-w-4xl">
          <thead>
            <tr className="text-left text-xs text-tally-title">
              <th className="pr-2">Item</th>
              <th className="pr-2">{shape.kind === "transfer" ? "From (Godown)" : "Godown"}</th>
              {shape.kind === "transfer" ? <th className="pr-2">To (Godown)</th> : null}
              <th className="pr-2 text-right">
                {shape.kind === "count" ? "Counted Qty" : "Qty"}
              </th>
              <th className="pr-2 text-right">Rate</th>
              <th className="pr-2 text-right">Value</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {lines.map((l, i) => (
              <tr key={i} className="align-top">
                <td className="pr-2">
                  <select
                    aria-label={`Item ${i + 1}`}
                    data-testid={`stock-item:${i}`}
                    className="bg-tally-field px-1 text-tally-fieldText"
                    value={l.stockItemId}
                    onChange={(e) => {
                      const master = stockItems.find((s) => s.id === e.target.value);
                      setRow(i, {
                        stockItemId: e.target.value,
                        itemName: master?.name ?? "",
                        unit: master?.unit_symbol ?? "",
                      });
                    }}
                  >
                    <option value="">— select item —</option>
                    {stockItems.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name}
                      </option>
                    ))}
                  </select>
                  {l.unit ? <span className="ml-1 text-xs">{l.unit}</span> : null}
                </td>
                <td className="pr-2">
                  <GodownSelect
                    label={
                      shape.kind === "transfer" ? `From godown ${i + 1}` : `Godown ${i + 1}`
                    }
                    testId={`stock-godown:${i}`}
                    godowns={godowns}
                    value={l.godownId}
                    onChange={(v) => setRow(i, { godownId: v })}
                  />
                </td>
                {shape.kind === "transfer" ? (
                  <td className="pr-2">
                    <GodownSelect
                      label={`To godown ${i + 1}`}
                      testId={`stock-to-godown:${i}`}
                      godowns={godowns}
                      value={l.toGodownId}
                      onChange={(v) => setRow(i, { toGodownId: v })}
                    />
                  </td>
                ) : null}
                <td className="pr-2">
                  <input
                    aria-label={`Quantity ${i + 1}`}
                    data-testid={`stock-qty:${i}`}
                    className="w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                    value={l.qty}
                    placeholder="0.0000"
                    onChange={(e) => setRow(i, { qty: e.target.value })}
                  />
                </td>
                <td className="pr-2">
                  <input
                    aria-label={`Rate ${i + 1}`}
                    data-testid={`stock-rate:${i}`}
                    className="w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                    value={l.rate}
                    placeholder="0.0000"
                    onChange={(e) => setRow(i, { rate: e.target.value })}
                  />
                </td>
                {/* DERIVED, never typed. On these classes it values the movement
                    and nothing else: there is no invoice for it to disagree with. */}
                <td className="pr-2 text-right" data-testid={`stock-value:${i}`}>
                  {isKeyedStockLine(l, shape) ? mulFixed4(l.qty, l.rate) : "—"}
                </td>
                <td>
                  {lines.length > 1 ? (
                    <button
                      type="button"
                      data-testid={`stock-remove:${i}`}
                      className="px-1 text-xs underline"
                      onClick={() => setLines((rows) => rows.filter((_, idx) => idx !== i))}
                    >
                      remove
                    </button>
                  ) : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>

        <button
          type="button"
          data-testid="stock-add-line"
          className="mt-1 px-2 py-0.5 text-xs underline"
          onClick={() => setLines((rows) => [...rows, emptyStockLine()])}
        >
          + Add line
        </button>

        <p className="mt-2 border-t border-tally-field pt-2 text-xs" data-testid="stock-total">
          {shape.kind === "count"
            ? `${keyed.length} position(s) counted, ${formatFixed4(totalQty)} units in total. The SERVER derives the correction against the books — this form never states a delta.`
            : shape.kind === "transfer"
              ? `${keyed.length} transfer(s), ${formatFixed4(totalQty)} units. Each line posts as a PAIR (out of From, into To), so the company's total holding is unchanged.`
              : `${keyed.length} line(s), ${formatFixed4(totalQty)} units ${shape.direction === "OUT" ? "out" : "in"}.`}
        </p>
      </div>
    </section>
  );
}

/** Seed the grid from a fetched voucher's projected movements. */
function linesFromView(
  inventory: readonly {
    stock_item_id: string;
    item: string;
    godown_id: string;
    batch_id: string;
    direction: string;
    qty: string;
    rate: string;
  }[],
  kind: "fixed-direction" | "transfer" | "count",
): StockVoucherLine[] {
  // A TRANSFER's stored movements are PAIRS, and re-pairing them by position
  // would be a guess: two transfers of the same item on one voucher project as
  // four rows with nothing but order tying each OUT to its IN. Seeding the OUT
  // legs alone would silently halve the voucher on re-post, so the whole class
  // is left to be re-keyed rather than half-restored.
  if (kind !== "fixed-direction") return [emptyStockLine()];
  const rows = inventory.map((l) => ({
    stockItemId: l.stock_item_id,
    itemName: l.item,
    godownId: l.godown_id,
    toGodownId: "",
    batchId: l.batch_id,
    qty: l.qty,
    rate: l.rate,
    unit: "",
  }));
  return rows.length > 0 ? rows : [emptyStockLine()];
}

function GodownSelect({
  label,
  testId,
  godowns,
  value,
  onChange,
}: {
  label: string;
  testId: string;
  godowns: GodownListItem[];
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <select
      aria-label={label}
      data-testid={testId}
      className="bg-tally-field px-1 text-tally-fieldText"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    >
      {/* "" is a REAL choice, not a missing one: an untracked line posts a NULL
          godown, which is what a company that never enabled multi-location
          wants. */}
      <option value="">— not tracked —</option>
      {godowns.map((g) => (
        <option key={g.id} value={g.id}>
          {g.name}
        </option>
      ))}
    </select>
  );
}

function FieldRow({
  name,
  value,
  testId,
  placeholder,
  onChange,
}: {
  name: string;
  value: string;
  testId: string;
  placeholder?: string;
  onChange: (v: string) => void;
}) {
  return (
    <label className="flex items-center gap-2 py-0.5">
      <span className="w-32">{name}</span>
      <input
        data-testid={testId}
        className="bg-tally-field px-2 text-tally-fieldText outline-none"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  );
}

/** Re-exported so the router in VoucherScreen has one import for the branch. */
export { isStockVoucherType };
