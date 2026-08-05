import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import { ApiError } from "../../api";
import type {
  BomComponentInput,
  BomExplosionResponse,
  BomListResponse,
  BomView,
  StockItemListItem,
} from "../../api";
import { normalizeMoney } from "../vouchers/money";
import { usePrintAction } from "../usePrint";

/**
 * BILLS OF MATERIALS (T3, migration 0043) — the recipes, and the explosion that
 * shows what a Stock Journal's lines would be.
 *
 * A BOM is a MASTER. It moves nothing, commits nothing and balances nothing: it
 * becomes real only when it is EXPLODED into the inventory lines of a Stock Journal
 * that an operator submits themselves. In Tally the same thing is a page of the
 * finished good's own item master — "Set Components (BOM) = Yes" opens the Stock
 * Item Multi Components screen (RESEARCH.md:626) — which is why RESEARCH pins no
 * key for reaching it, and why the Gateway is this screen's only door.
 *
 * ⛔⛔ THE ONE THING THIS SCREEN MUST NOT DO, AND IT IS A ONE-LINE MISTAKE.
 *
 * `output_qty` IS THE YIELD, AND THE COMPONENTS ARE STORED AGAINST IT — NEVER
 * PRE-DIVIDED PER UNIT. Recipes are stated the way a workshop states them: "10 kg of
 * resin makes 3 mouldings". A UI that helpfully normalises that to 3.3333 kg per
 * moulding loses material at fixed-4 — exploding it for a batch of 3 gives 9.9999
 * kg, the consumption is short by a tenth of a gram every batch forever, and the
 * shortfall accumulates in closing stock as phantom material that reconciles against
 * nothing. So this screen SHOWS a per-unit figure where it helps an operator read
 * the recipe, and SENDS the recipe exactly as written, letting the server scale by
 * (wanted / output_qty). The scaling is done there, once, at full precision, and
 * multiplying BEFORE dividing — which is what orders.ScaleComponent does and what
 * api/orders.test.ts pins.
 *
 * WHAT THIS SCREEN REFUSES TO DO
 *
 *  1. IT RENDERS NO MONEY, and it has none to render. A recipe is quantities. What
 *     those quantities COST is the valuation plane, and it is deliberately not here:
 *     Tally's own effective-cost apportionment (co-products, by-products, scrap,
 *     additional cost ledgers — RESEARCH.md:626) is not built, and a screen that put
 *     a value column on a recipe would imply it was.
 *
 *  2. IT NEVER IMPLIES THE EXPLOSION IS COMPLETE TO RAW MATERIALS. It is
 *     SINGLE-LEVEL: a Table explodes to 4 Legs, and if a Leg has its own recipe the
 *     operator issues a second Stock Journal. That is Tally's own default, and it is
 *     what keeps work-in-progress visible as stock instead of vanishing inside one
 *     entry. The server says so in `notes` and this screen renders them.
 *
 *  3. IT NEVER ASKS FOR AN ID. The finished good and every component are picked from
 *     the served stock list.
 *
 *  4. IT NEVER TREATS AN EXPLOSION AS A PRODUCTION. Alt+X writes nothing and posts
 *     nothing — the response is a PREVIEW of a voucher the operator still has to
 *     key. That is what keeps a manufacture an auditable entry rather than a side
 *     effect of opening a recipe, and the panel says so on its face.
 */

/** One component being keyed. No `line_no`: the server assigns it from position. */
interface DraftComponent {
  stock_item_id: string;
  qty: string;
}

const EMPTY_COMPONENT: DraftComponent = { stock_item_id: "", qty: "" };

export function BomsScreen() {
  const client = useClient();
  const companyID = useCompanyID();

  const [data, setData] = useState<BomListResponse | null>(null);
  const [listError, setListError] = useState("");
  const [denied, setDenied] = useState(false);
  const [busy, setBusy] = useState(false);

  const [items, setItems] = useState<StockItemListItem[]>([]);
  const [pickerError, setPickerError] = useState("");

  /** The HIGHLIGHTED recipe, and its components. */
  const [selectedID, setSelectedID] = useState("");
  const [detail, setDetail] = useState<BomView | null>(null);
  const [detailError, setDetailError] = useState("");

  // ---- The create panel (Alt+C).
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [outputItemID, setOutputItemID] = useState("");
  const [outputQty, setOutputQty] = useState("");
  const [isDefault, setIsDefault] = useState(false);
  const [components, setComponents] = useState<DraftComponent[]>([
    { ...EMPTY_COMPONENT },
  ]);
  const [refusal, setRefusal] = useState("");
  const [writeDenied, setWriteDenied] = useState(false);
  const [notice, setNotice] = useState("");

  // ---- The explosion panel (Alt+X).
  const [exploding, setExploding] = useState(false);
  const [wantedQty, setWantedQty] = useState("");
  const [explosion, setExplosion] = useState<BomExplosionResponse | null>(null);
  const [explodeError, setExplodeError] = useState("");

  const load = useCallback(async () => {
    setBusy(true);
    try {
      // ⚠️ NO ITEM FILTER. Omitting `stockItemID` lists every recipe; sending the
      // all-zeros uuid is a 400 and NOT a synonym for "no filter" — the server
      // refuses an explicit zero precisely because `uuid.Parse` accepts it, and two
      // handlers once treated it as "absent" and silently returned every row.
      const res = await client.listBoms(companyID);
      setData(res);
      setListError("");
      setDenied(false);
    } catch (e) {
      setData(null);
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
  }, [client, companyID]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    let live = true;
    client
      .listStockItems(companyID)
      .then((s) => {
        if (!live) return;
        setItems(s.stock_items);
        setPickerError("");
      })
      .catch((e: unknown) => {
        if (live) setPickerError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      live = false;
    };
  }, [client, companyID]);

  const loadDetail = useCallback(
    async (bomID: string) => {
      if (bomID === "") {
        setDetail(null);
        setDetailError("");
        return;
      }
      try {
        setDetail(await client.getBom(companyID, bomID));
        setDetailError("");
      } catch (e) {
        setDetail(null);
        setDetailError(e instanceof Error ? e.message : String(e));
      }
    },
    [client, companyID],
  );

  useEffect(() => {
    void loadDetail(selectedID);
  }, [loadDetail, selectedID]);

  usePrintAction({ voucher: { voucherID: null } });

  const boms = data?.boms ?? [];
  const selected = boms.find((b) => b.bom_id === selectedID);

  // ---- Alt+C — DEFINE A RECIPE.
  useRegisterScreenAction(
    "createBom",
    useCallback((): string => {
      if (denied) {
        return "You are not permitted to create bills of materials on this company.";
      }
      // THE PANEL'S RENDER CONDITION, VERBATIM.
      if (items.length === 0) {
        return (
          "A recipe is made of stock items, and both the finished good and every " +
          "component are PICKED from this company's item list rather than typed as " +
          "ids — so the form cannot open until that list has been read. " +
          (pickerError !== ""
            ? `It could not be: ${pickerError}`
            : "This company has no stock items yet; create the finished good and its components first.")
        );
      }
      if (creating) {
        return "The recipe form is already open. Ctrl+A accepts it.";
      }
      setCreating(true);
      setExploding(false);
      setName("");
      setOutputItemID("");
      setOutputQty("");
      setIsDefault(false);
      setComponents([{ ...EMPTY_COMPONENT }]);
      setRefusal("");
      setWriteDenied(false);
      setNotice("");
      return "Define the recipe as it is stated on the shop floor — the quantity it YIELDS, and the components consumed to yield THAT. Ctrl+A accepts it.";
    }, [creating, denied, items.length, pickerError]),
  );

  // ---- Alt+X — EXPLODE the highlighted recipe.
  useRegisterScreenAction(
    "explodeBom",
    useCallback((): string => {
      if (creating) {
        return "The recipe form is open. Finish it with Ctrl+A or close the panel — a recipe cannot be exploded until it exists.";
      }
      if (selected === undefined) {
        return "Highlight a recipe first — Alt+X explodes the one selected in the list.";
      }
      setExploding(true);
      setWantedQty("");
      setExplosion(null);
      setExplodeError("");
      return `Explode ${selected.name} — say how much of ${selected.stock_item} you want to make. Nothing will move: the answer is what a Stock Journal's lines WOULD be.`;
    }, [creating, selected]),
  );

  const patchComponent = useCallback(
    (idx: number, patch: Partial<DraftComponent>) =>
      setComponents((cs) =>
        cs.map((c, i) => (i === idx ? { ...c, ...patch } : c)),
      ),
    [],
  );
  const addComponent = useCallback(
    () => setComponents((cs) => [...cs, { ...EMPTY_COMPONENT }]),
    [],
  );
  const removeComponent = useCallback(
    (idx: number) =>
      setComponents((cs) =>
        cs.length === 1 ? cs : cs.filter((_, i) => i !== idx),
      ),
    [],
  );

  const filledComponents = useMemo(
    () => components.filter((c) => c.stock_item_id !== ""),
    [components],
  );

  /** Run the explosion. Its own submit: Alt+X's panel is not the accept path. */
  const runExplosion = useCallback(async () => {
    if (selected === undefined) return;
    setExplodeError("");
    setExplosion(null);
    setBusy(true);
    try {
      // ⛔ `qty` IS REQUIRED and there is no default. Guessing 1 would answer a
      // question about a different batch — the server 400s an empty qty and 422s a
      // zero one, and both are right.
      const res = await client.explodeBom(
        companyID,
        selected.bom_id,
        normalizeMoney(wantedQty),
      );
      setExplosion(res);
    } catch (e) {
      setExplodeError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [client, companyID, selected, wantedQty]);

  // ---- Ctrl+A — ACCEPT the recipe form.
  const submit = useCallback(async (): Promise<string> => {
    if (exploding) {
      // Ctrl+A on the explosion panel RUNS the explosion. It is not a save, and the
      // sentence says so: an operator who thought Ctrl+A had produced something
      // would go looking for stock that is not there.
      if (wantedQty.trim() === "") {
        return "Say how much you want to make. There is no default — guessing one unit would answer a question about a different batch, so nothing was sent.";
      }
      await runExplosion();
      return "Exploded — this is a PREVIEW and nothing has been saved. Key a Stock Journal (Esc, then Alt+F7) to actually consume the components and produce the finished good.";
    }
    if (!creating) {
      return "Nothing to accept here. Alt+C defines a recipe; Alt+X explodes the highlighted one.";
    }
    if (outputItemID === "") {
      return "Choose the finished good this recipe makes. A recipe with no output is a list of components with nothing to yield, so nothing was sent.";
    }
    if (filledComponents.length === 0) {
      return "Add at least one component with an item chosen. A recipe that consumes nothing is not a recipe, so nothing was sent.";
    }
    if (filledComponents.some((c) => c.stock_item_id === outputItemID)) {
      // THE SCREEN'S OWN REFUSAL, and it is worth making here rather than letting
      // the 422 come back: an item that is a component of itself is a recipe that
      // consumes what it makes, and an operator who did it by mis-picking one row
      // wants to be told which rule they hit, not to lose the form.
      return "An item cannot be a component of its own recipe — that would consume the thing it makes. Change either the finished good or that component; nothing was sent.";
    }
    setRefusal("");
    setWriteDenied(false);
    setNotice("");
    setBusy(true);
    try {
      const created = await client.createBom(companyID, {
        stock_item_id: outputItemID,
        name: name.trim(),
        // ⛔ THE YIELD, AS WRITTEN. Never divided into a per-unit figure — see the
        // header. The server scales by (wanted / output_qty) at explosion time.
        output_qty: normalizeMoney(outputQty),
        is_default: isDefault,
        created_by: "ui",
        components: filledComponents.map(
          (c): BomComponentInput => ({
            stock_item_id: c.stock_item_id,
            // Consumed to yield output_qty of the finished good — NOT per unit of it.
            qty: normalizeMoney(c.qty),
          }),
        ),
      });
      setCreating(false);
      await load();
      setSelectedID(created.bom_id);
      const outputName =
        items.find((i) => i.id === outputItemID)?.name ?? "the finished good";
      const message =
        `Defined ${name.trim()} on ${outputName}: ${normalizeMoney(outputQty)} ` +
        `yielded from ${filledComponents.length} component(s). Nothing has moved — ` +
        `a recipe is a master, and it consumes and produces nothing until it is ` +
        `exploded into a Stock Journal. Alt+X previews that.`;
      setNotice(message);
      return message;
    } catch (e) {
      // THE SERVER'S OWN SENTENCE. A 409 here is two different facts needing two
      // different responses — the NAME is taken on this item ("pick another"), or
      // the item already has a DEFAULT recipe ("clear that one or add this as an
      // alternative") — and the server's message distinguishes them, so it is not
      // replaced.
      const message = e instanceof Error ? e.message : String(e);
      setRefusal(message);
      setWriteDenied(e instanceof ApiError && e.status === 403);
      return message;
    } finally {
      setBusy(false);
    }
  }, [
    client,
    companyID,
    creating,
    exploding,
    filledComponents,
    isDefault,
    items,
    load,
    name,
    outputItemID,
    outputQty,
    runExplosion,
    wantedQty,
  ]);

  useRegisterScreenAction("accept", submit);

  const firstFieldRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (creating) firstFieldRef.current?.focus();
  }, [creating]);

  if (denied) {
    return (
      <section
        data-testid="boms-denied"
        className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field"
      >
        <h1 className="text-lg font-bold text-tally-title">
          Bills of Materials
        </h1>
        <p className="mt-2 max-w-3xl text-sm">
          You do not have permission to read or define this company&apos;s bills of
          materials. That is a decision, not a fault: a recipe decides what every
          future production run CONSUMES, and a wrong one is invisible on each Stock
          Journal that used it — the components will simply be issued in the wrong
          quantities, and closing stock will follow. An owner can grant it by adding
          an <span className="text-tally-title">allow bom</span> rule in Users &amp;
          Roles, or <span className="text-tally-title">allow create bom</span> for
          the write alone.
        </p>
      </section>
    );
  }

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">Bills of Materials</h1>
      <p className="mt-1 max-w-3xl text-xs">
        A recipe: the quantity a build YIELDS, and the components consumed to yield
        that quantity. It is a MASTER — it moves nothing and costs nothing until it
        is exploded into a Stock Journal. Alt+C defines one; Alt+X explodes the
        highlighted one for a batch size.
        {busy ? " · working…" : ""}
      </p>

      {listError !== "" ? (
        <p
          data-testid="boms-error"
          className="mt-3 max-w-3xl text-sm text-tally-bar"
        >
          {listError}
        </p>
      ) : null}

      {notice !== "" ? (
        <p
          data-testid="boms-notice"
          className="mt-3 max-w-3xl border border-tally-fieldText p-2 text-sm"
        >
          {notice}
        </p>
      ) : null}

      {/* ---- THE CREATE PANEL (Alt+C). ------------------------------------- */}
      {creating ? (
        <form
          data-testid="bom-panel"
          className="mt-4 max-w-4xl border border-tally-fieldText p-3 text-sm"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <h2 className="font-bold text-tally-title">Define a recipe</h2>

          <div className="mt-2 flex flex-wrap gap-4">
            <label className="flex flex-col text-xs">
              <span className="text-tally-title">Recipe name</span>
              <input
                ref={firstFieldRef}
                aria-label="Recipe name"
                className="mt-1 w-48 bg-tally-field px-1 text-tally-fieldText outline-none"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <span className="mt-0.5 text-tally-field">
                An item may have several — a standard build and a substitute build —
                and the name is what an operator picks at explosion time. Unique
                within its item.
              </span>
            </label>
            <label className="flex flex-col text-xs">
              <span className="text-tally-title">Finished good</span>
              <select
                aria-label="Finished good"
                className="mt-1 w-56 bg-tally-field px-1 text-tally-fieldText outline-none"
                value={outputItemID}
                onChange={(e) => setOutputItemID(e.target.value)}
              >
                <option value="">— choose —</option>
                {items.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col text-xs">
              <span className="text-tally-title">Yields this quantity</span>
              <input
                aria-label="Output quantity (the yield)"
                className="mt-1 w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                value={outputQty}
                onChange={(e) => setOutputQty(e.target.value)}
              />
            </label>
          </div>

          <label className="mt-3 flex items-start gap-2 text-xs">
            <input
              type="checkbox"
              aria-label="Make this the default recipe for the item"
              className="mt-0.5"
              checked={isDefault}
              onChange={(e) => setIsDefault(e.target.checked)}
            />
            <span>
              <span className="text-tally-title">The default recipe</span> for this
              item. At most one per item — the server refuses a second, and the
              refusal is a different fact from a duplicate NAME, so read the sentence
              it sends.
            </span>
          </label>

          {/* ---- THE COMPONENTS. ------------------------------------------- */}
          <p className="mt-4 text-xs text-tally-title">
            Components — the quantities consumed to yield the yield above, NOT per
            unit of it
          </p>
          <p className="max-w-3xl text-xs">
            State the recipe as the shop floor states it: &ldquo;10 kg of resin makes
            3 mouldings&rdquo; is output 3, component 10. Do NOT divide it to 3.3333
            per moulding — at four decimal places that shorts every batch by a
            fraction forever, and the shortfall accumulates in closing stock as
            material that reconciles against nothing. The server scales by (wanted ÷
            yield) when you explode it.
          </p>
          <table className="mt-2 w-full text-xs">
            <thead>
              <tr className="text-left text-tally-title">
                <th className="pr-2">#</th>
                <th className="pr-2">Component</th>
                <th className="pr-2">Quantity consumed</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {components.map((c, idx) => (
                <tr key={idx} data-testid={`bom-component:${idx}`}>
                  <td className="pr-2">{idx + 1}</td>
                  <td className="pr-2">
                    <select
                      aria-label={`Component ${idx + 1}`}
                      className="w-56 bg-tally-field px-1 text-tally-fieldText outline-none"
                      value={c.stock_item_id}
                      onChange={(e) =>
                        patchComponent(idx, { stock_item_id: e.target.value })
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
                      aria-label={`Quantity of component ${idx + 1}`}
                      className="w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
                      value={c.qty}
                      onChange={(e) =>
                        patchComponent(idx, { qty: e.target.value })
                      }
                    />
                  </td>
                  <td>
                    <button
                      type="button"
                      aria-label={`Remove component ${idx + 1}`}
                      className="border border-tally-fieldText px-1"
                      onClick={() => removeComponent(idx)}
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
            onClick={addComponent}
          >
            + Add a component
          </button>

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

      {/* ---- THE EXPLOSION PANEL (Alt+X). --------------------------------- */}
      {exploding && selected ? (
        <form
          data-testid="bom-explode-panel"
          className="mt-4 max-w-4xl border border-tally-fieldText p-3 text-sm"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <h2 className="font-bold text-tally-title">
            Explode {selected.name} — {selected.stock_item}
          </h2>
          <p className="mt-1 max-w-3xl text-xs">
            ⛔ THIS WRITES NOTHING AND POSTS NOTHING. What comes back is what a Stock
            Journal&apos;s lines WOULD be for the batch you name. You still key that
            voucher yourself, which is what keeps a manufacture an auditable entry
            rather than a side effect of opening a recipe.
          </p>
          <label className="mt-3 flex flex-col text-xs">
            <span className="text-tally-title">
              How much {selected.stock_item} do you want to make? Required — there is
              no default.
            </span>
            <input
              aria-label="Wanted quantity"
              className="mt-1 w-24 bg-tally-field px-1 text-right text-tally-fieldText outline-none"
              value={wantedQty}
              onChange={(e) => setWantedQty(e.target.value)}
            />
          </label>
          <div className="mt-3 flex items-center gap-3">
            <button
              type="submit"
              className="border border-tally-fieldText px-2 py-0.5 text-xs"
            >
              Ctrl+A — Explode
            </button>
            <button
              type="button"
              className="border border-tally-fieldText px-2 py-0.5 text-xs"
              onClick={() => {
                setExploding(false);
                setExplosion(null);
                setExplodeError("");
              }}
            >
              Close the panel
            </button>
          </div>

          {explodeError !== "" ? (
            <p
              data-testid="bom-explode-error"
              className="mt-3 max-w-3xl border border-tally-bar p-2 text-sm text-tally-bar"
            >
              {explodeError}
            </p>
          ) : null}

          {explosion ? (
            <div className="mt-3" data-testid="bom-explosion">
              <p className="text-xs">
                To make{" "}
                <span className="text-tally-title">{explosion.wanted_qty}</span> of{" "}
                {explosion.stock_item}, from a recipe that yields{" "}
                <span className="text-tally-title">{explosion.output_qty}</span>:
              </p>
              <table className="mt-1 w-full max-w-2xl text-sm">
                <thead>
                  <tr className="text-left text-xs text-tally-title">
                    <th className="pr-3">Component</th>
                    <th className="pr-3 text-right">Consume</th>
                  </tr>
                </thead>
                <tbody>
                  {explosion.components.map((c) => (
                    <tr
                      key={c.stock_item_id}
                      data-testid={`bom-explosion-row:${c.stock_item}`}
                    >
                      <td className="pr-3">{c.stock_item}</td>
                      {/* THE SERVER'S FIGURE. Computed once at full precision,
                          multiplying before dividing. Nothing is re-derived here —
                          re-scaling it in the client is exactly the arithmetic the
                          header forbids. */}
                      <td className="pr-3 text-right">{c.qty}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {/* ⛔ RENDERED, NOT OPTIONAL. These notes state that the explosion is
                  SINGLE-LEVEL — a component with its own recipe is not exploded
                  recursively — and a screen that dropped them would imply the list
                  above reaches raw materials. */}
              {explosion.notes.length > 0 ? (
                <ul
                  data-testid="bom-explosion-notes"
                  className="mt-2 max-w-3xl list-none text-xs"
                >
                  {explosion.notes.map((n) => (
                    <li key={n} className="mt-1">
                      {n}
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          ) : null}
        </form>
      ) : null}

      {refusal !== "" ? (
        <p
          data-testid="bom-refusal"
          className="mt-3 max-w-3xl border border-tally-bar p-2 text-sm text-tally-bar"
        >
          {refusal}
        </p>
      ) : null}

      {writeDenied ? (
        <p
          data-testid="bom-write-denied"
          className="mt-2 max-w-3xl border border-tally-fieldText p-2 text-sm"
        >
          That is a decision, not a fault. Reading recipes and defining them are
          SEPARATE actions on one <span className="text-tally-title">bom</span>{" "}
          token, so a level that may look up what a product is made of and may not
          change it is a real configuration — and you are in it. Nothing you keyed has
          been lost or sent.
        </p>
      ) : null}

      {/* ---- THE RECIPES. -------------------------------------------------- */}
      {data !== null ? (
        <>
          {boms.length === 0 ? (
            <p data-testid="boms-empty" className="mt-4 max-w-3xl text-sm">
              This company has no bills of materials. That is a statement about the
              masters only: it does not mean nothing is manufactured, it means no
              recipe is recorded, so every production Stock Journal is keyed by hand
              and nothing checks the component quantities against a standard. Alt+C
              defines the first one.
            </p>
          ) : (
            <table
              className="mt-4 w-full max-w-4xl text-sm"
              data-testid="boms-table"
            >
              <thead>
                <tr className="text-left text-xs text-tally-title">
                  <th className="pr-3">Recipe</th>
                  <th className="pr-3">Finished good</th>
                  <th className="pr-3 text-right">Yields</th>
                  <th className="pr-3">Default</th>
                </tr>
              </thead>
              <tbody>
                {boms.map((b) => (
                  <tr
                    key={b.bom_id}
                    data-testid={`bom-row:${b.stock_item}:${b.name}`}
                    aria-selected={b.bom_id === selectedID}
                    className={
                      b.bom_id === selectedID
                        ? "bg-tally-panel text-tally-title"
                        : "cursor-pointer"
                    }
                    onClick={() => {
                      setSelectedID(b.bom_id);
                      setExploding(false);
                    }}
                  >
                    <td className="pr-3">
                      <button
                        type="button"
                        aria-label={`Select recipe ${b.name} on ${b.stock_item}`}
                        className="underline"
                        onClick={(e) => {
                          e.stopPropagation();
                          setSelectedID(b.bom_id);
                          setExploding(false);
                        }}
                      >
                        {b.name}
                      </button>
                    </td>
                    <td className="pr-3">{b.stock_item}</td>
                    <td className="pr-3 text-right">{b.output_qty}</td>
                    <td className="pr-3">{b.is_default ? "yes" : ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          {detailError !== "" ? (
            <p
              data-testid="bom-detail-error"
              className="mt-3 max-w-3xl border border-tally-bar p-2 text-sm text-tally-bar"
            >
              {detailError}
            </p>
          ) : null}

          {detail ? (
            <div className="mt-4 max-w-4xl">
              <h2 className="font-bold text-tally-title">
                {detail.name} · {detail.stock_item} · yields {detail.output_qty}
              </h2>
              <table className="mt-1 w-full text-sm" data-testid="bom-components">
                <thead>
                  <tr className="text-left text-xs text-tally-title">
                    <th className="pr-3">#</th>
                    <th className="pr-3">Component</th>
                    <th className="pr-3 text-right">
                      Consumed per {detail.output_qty} made
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {detail.components.map((c) => (
                    <tr
                      key={`${c.line_no}:${c.stock_item_id}`}
                      data-testid={`bom-component-row:${detail.name}:${c.line_no}`}
                    >
                      <td className="pr-3">{c.line_no}</td>
                      <td className="pr-3">{c.stock_item}</td>
                      {/* ⛔ AS STORED, AGAINST THE YIELD. There is deliberately no
                          per-unit column: a divided figure is the one number this
                          feature must never persist or send, and putting it beside
                          the real one invites it being keyed back in. The heading
                          states what the quantity is against. */}
                      <td className="pr-3 text-right">{c.qty}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="mt-2 max-w-3xl text-xs">
                These quantities are against the YIELD of {detail.output_qty}, not per
                unit. Alt+X scales them for a batch — server-side, once, at full
                precision.
              </p>
            </div>
          ) : selectedID !== "" && detailError === "" ? (
            <p className="mt-3 text-sm">Reading the recipe&apos;s components…</p>
          ) : boms.length > 0 ? (
            <p className="mt-3 text-xs">
              Highlight a recipe to see its components. Alt+X explodes the
              highlighted one for a batch size.
            </p>
          ) : null}

          <p className="mt-5 max-w-3xl text-xs">
            NOT BUILT, AND SAID HERE RATHER THAN LEFT TO BE DISCOVERED: there is no
            rename and no alter on this screen, and no component TYPE — Tally
            distinguishes Component / Co-Product / By-Product / Scrap with a Rate %
            for cost apportionment (RESEARCH.md:626), and none of that exists here, so
            every line above is a plain component. The explosion is also SINGLE-LEVEL:
            a component that has its own recipe is not exploded through, which is
            Tally&apos;s own default and is what keeps work-in-progress visible as
            stock rather than vanishing inside one entry.
          </p>
        </>
      ) : listError === "" ? (
        <p className="mt-3 text-sm">Loading…</p>
      ) : null}
    </section>
  );
}
