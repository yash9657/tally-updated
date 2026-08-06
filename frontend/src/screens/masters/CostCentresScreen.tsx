import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { useClient, useCompanyID } from "../../app/CompanyContext";
import { useRegisterScreenAction } from "../../app/ScreenActions";
import { ApiError } from "../../api";
import type { CostCategory, CostCentre, CostCentreListResponse } from "../../api";
import { usePrintAction } from "../usePrint";

/**
 * COST CATEGORIES AND COST CENTRES — the T2 masters, and the screen that makes
 * the whole feature usable at all.
 *
 * Until an axis and a centre exist, nothing can be allocated and both cost
 * reports can only say so. Tally reaches the same masters through
 * `Alt+G > Create Master > Cost Centre` (RESEARCH.md:525); this is that door.
 *
 * ⛔⛔ THE ONE THING TO UNDERSTAND BEFORE READING ANY OF IT.
 *
 * A cost CATEGORY is an AXIS of analysis (Department, Branch, Project). A cost
 * CENTRE is a POSITION on that axis. Several axes exist in order to allocate the
 * SAME money IN PARALLEL: one Rs 1,00,000 salary is allocated in full across
 * Departments (Sales 60,000 / Admin 40,000) AND AGAIN in full across Branches
 * (Mumbai 70,000 / Delhi 30,000), and both allocations are complete and both are
 * correct. So THIS SCREEN NEVER PRESENTS CENTRES AS ONE FLAT LIST: they are
 * grouped by axis, always, because a list of bare centre names spanning axes is
 * ambiguous — "Mumbai" may legitimately be both a Branch and a Project — and an
 * operator picking from it picks the wrong one. That is also why the server puts
 * `cost_category_name` on EVERY centre row.
 *
 * WHAT THIS SCREEN REFUSES TO DO
 *
 *  1. IT RENDERS NO MONEY, and it has none to render: a master is a name and a
 *     place in a hierarchy. What each centre COST is the Cost Centre Summary, and
 *     it is one axis at a time for the reason above. There is not one amount, not
 *     one total and no arithmetic of any kind in this file.
 *
 *  2. IT NEVER INDENTS THE LIST IN THE ORDER IT ARRIVED. The server orders
 *     centres by (axis name, centre name) and NOT parent-before-child, so
 *     indenting the received order by `depth` renders children under the wrong
 *     parent — a tree that is confidently wrong about which centre rolls up into
 *     which. The tree here is built from `parent_id` (see {@link buildForest}),
 *     which is the derivation the api/types.ts note asks for.
 *
 *  3. IT NEVER WALKS `path` AS A TREE WHEN `ancestry_truncated` IS SET. That flag
 *     says the STORED rows contain a parent cycle — which the write path refuses
 *     to create and which no constraint catches beyond a one-hop self-reference.
 *     The axis is then listed FLAT with the flag stated on its face, because a
 *     drawn hierarchy would be a picture of something that cannot exist.
 *
 *  4. IT NEVER ASKS FOR AN ID. The create panel picks the axis, and the optional
 *     parent, from the SERVED lists. A cost_category_id is a UUID that no screen
 *     in this product displays, so a box for one could not be filled by a human —
 *     the defect the TDS challan form shipped and had to be rebuilt for.
 *
 *  5. IT TOLERATES THE TWO LISTS DISAGREEING. `cost_centres` and
 *     `cost_categories` are read in SEPARATE transactions (api/types.ts says so),
 *     so a centre can arrive whose axis is not in the axis list. Those centres are
 *     shown under a stated heading rather than dropped: a master that vanishes
 *     from its own master list is how an operator concludes it was never created
 *     and creates it again.
 *
 *  6. IT RENDERS THE SERVER'S SENTENCE. Every refusal on these routes names the
 *     fact that is wrong — a duplicate name NAMES THE AXIS it clashes on (the
 *     index is per (company, CATEGORY, name), so the same name on a different axis
 *     is legal and a message that did not say so reads as a bug), a parent on
 *     another axis is a 422 explaining that one axis would roll up into the other.
 *     None of them is replaced.
 *
 *  7. IT TREATS A 403 AS A DECISION. These routes sit behind the bare
 *     `cost-centre` token, and the seeded Data Entry level's default_access is
 *     'none' with no ACL migration granting it — deliberately, because creating an
 *     axis decides how every future allocation is GROUPED and a wrong axis is
 *     invisible on every voucher that used it. So a 403 here is the NORMAL answer
 *     for that level and is rendered as the decision it is.
 */

/** A centre plus its children — the shape this screen renders, never the wire's. */
interface CentreNode {
  centre: CostCentre;
  children: CentreNode[];
}

/**
 * Build the forest for ONE axis from `parent_id`.
 *
 * ⚠️ THIS IS THE DERIVATION THE TYPES ASK FOR, and the reason is worth keeping in
 * front of the code: the received order is (axis name, centre name), so
 * `centres.map(c => <Row indent={c.depth}/>)` puts "Andheri" under whatever
 * happened to precede it alphabetically. Sorting by `path` is the other
 * documented option; parent_id is used here because it also surfaces the case
 * `path` hides — a centre whose parent is NOT in the list, which is rendered at
 * the top with its parentage stated rather than silently disappearing.
 */
export function buildForest(centres: CostCentre[]): {
  roots: CentreNode[];
  /** Centres whose parent_id names a centre that is not in this axis's list. */
  orphans: CostCentre[];
} {
  const byID = new Map<string, CentreNode>();
  for (const c of centres) byID.set(c.id, { centre: c, children: [] });

  const roots: CentreNode[] = [];
  const orphans: CostCentre[] = [];
  for (const c of centres) {
    const node = byID.get(c.id)!;
    if (c.parent_id === "") {
      roots.push(node);
      continue;
    }
    const parent = byID.get(c.parent_id);
    if (!parent) {
      // Shown, not dropped. A centre under a parent this list does not contain is
      // still a real centre with real allocations behind it.
      orphans.push(c);
      continue;
    }
    parent.children.push(node);
  }

  // Sort each LEVEL by name. The server's ordering is already by name within an
  // axis, but a tree built by pointer chasing inherits insertion order, and two
  // renderings of one axis in a different order read as two different lists.
  const sortLevel = (nodes: CentreNode[]) => {
    nodes.sort((a, b) => a.centre.name.localeCompare(b.centre.name));
    for (const n of nodes) sortLevel(n.children);
  };
  sortLevel(roots);
  orphans.sort((a, b) => a.path.localeCompare(b.path));
  return { roots, orphans };
}

/** What the create panel is creating. "" is "the operator has not said". */
type CreateKind = "" | "category" | "centre";

export function CostCentresScreen() {
  const client = useClient();
  const companyID = useCompanyID();

  const [data, setData] = useState<CostCentreListResponse | null>(null);
  const [listError, setListError] = useState("");
  const [denied, setDenied] = useState(false);
  const [busy, setBusy] = useState(false);

  // ---- The create panel (Alt+C). Closed until asked for: this list is read far
  // more often than it is added to, and a form standing open over it would make
  // the list — the thing that shows an operator the name is already taken, and on
  // WHICH axis — the smaller half of the screen.
  const [creating, setCreating] = useState(false);
  /** NO DEFAULT. Defaulting to "centre" would prejudge the harder question. */
  const [kind, setKind] = useState<CreateKind>("");
  const [name, setName] = useState("");
  /** Cost category only (Tally's "Allocate Non-revenue Items"). */
  const [allocateNonRevenue, setAllocateNonRevenue] = useState(false);
  /** Cost centre only. PICKED from the served axes — never typed. */
  const [axisID, setAxisID] = useState("");
  /** Cost centre only. "" means "at the top of the axis", never the nil uuid. */
  const [parentID, setParentID] = useState("");
  /** THE SERVER'S SENTENCE, verbatim. */
  const [refusal, setRefusal] = useState("");
  /** A 403 on the WRITE. The sentence above still shows; this adds the framing. */
  const [createDenied, setCreateDenied] = useState(false);
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    setBusy(true);
    try {
      // NO AXIS FILTER: the whole point of this screen is every axis at once, and
      // the response carries the axes themselves so the create panel never has to
      // ask for an id.
      const res = await client.listCostCentres(companyID);
      setData(res);
      setListError("");
      setDenied(false);
    } catch (e) {
      setData(null);
      // A 403 is a DECISION here, not a fault — see (7) in the header.
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

  // Ctrl+P. There is no printed layout for a list of masters, so the target
  // resolver answers with a SENTENCE and makes no request. Registering the handler
  // is what turns a silently dead key into that sentence.
  usePrintAction({ voucher: { voucherID: null } });

  const axes: CostCategory[] = data?.cost_categories ?? [];

  // ---- Alt+C — CREATE. The list must be loaded first, and that is not friction:
  // the panel's axis picker and its parent picker are both built from this
  // response, and a panel opened over no list could only ask for an id.
  useRegisterScreenAction(
    "createCostMaster",
    useCallback((): string => {
      if (denied) {
        return "You are not permitted to create cost masters on this company.";
      }
      // ⚠️ THE PANEL'S RENDER CONDITION, VERBATIM. Anything else here can claim
      // the key worked while nothing appears — the Alt+O signature.
      if (data === null) {
        return (
          "The cost masters could not be read, so there is nothing to add to yet — " +
          "the axis a new centre sits on is picked from that list, never typed. " +
          "If a refusal is showing above, that is why."
        );
      }
      if (creating) {
        return "The create panel is already open. Ctrl+A accepts it.";
      }
      setCreating(true);
      setKind("");
      setName("");
      setAllocateNonRevenue(false);
      setAxisID("");
      setParentID("");
      setRefusal("");
      setCreateDenied(false);
      setNotice("");
      return "Create a cost category (axis) or a cost centre. Ctrl+A accepts it.";
    }, [creating, data, denied]),
  );

  // ---- Ctrl+A — ACCEPT. Async so the shell awaits it and the REFUSAL reaches the
  // status line as well as the panel.
  const submit = useCallback(async (): Promise<string> => {
    if (!creating) {
      return "Nothing to accept here. Alt+C creates a cost category or a cost centre.";
    }
    if (kind === "") {
      // THE SCREEN'S OWN REFUSAL, and one of only two it makes. It is not
      // duplicated validation: there is no request to send until this is known —
      // the two masters are different routes.
      return (
        "Say which you are creating first. A cost CATEGORY is an axis of analysis " +
        "(Department, Branch, Project); a cost CENTRE is a position on one of those " +
        "axes. They are different masters on different routes, so nothing was sent."
      );
    }
    if (kind === "centre" && axisID === "") {
      // The other one, and for the same reason: cost_category_id is REQUIRED and
      // there is no uncategorised centre.
      return (
        "Choose the axis this centre sits on. There is no uncategorised cost " +
        "centre — a centre with no axis cannot be grouped by anything — so nothing " +
        "was sent."
      );
    }
    setRefusal("");
    setCreateDenied(false);
    setNotice("");
    setBusy(true);
    try {
      if (kind === "category") {
        const created = await client.createCostCategory(companyID, {
          name: name.trim(),
          allocate_non_revenue: allocateNonRevenue,
          // ⛔ REQUIRED. Omitting it is a 400 AFTER the step-up has already
          // succeeded, which reads as "the passkey failed" and is not.
          created_by: "ui",
        });
        setCreating(false);
        setName("");
        await load();
        const message =
          `Created the ${created.name} axis. It has no centres yet, so nothing can ` +
          `be allocated on it — Alt+C again, this time for a CENTRE. ` +
          (created.allocate_non_revenue
            ? "It may carry balance-sheet ledgers as well as P&L ones, which also widens the base its coverage is measured against."
            : "It carries revenue and expense ledgers only, which is the ordinary case.");
        setNotice(message);
        return message;
      }
      const created = await client.createCostCentre(companyID, {
        cost_category_id: axisID,
        name: name.trim(),
        // ⛔ EMPTY, NEVER THE ALL-ZEROS UUID. The route refuses the nil uuid with
        // its own sentence precisely because a client that sent it would silently
        // move a centre to the top of its axis and change which totals it rolls
        // into. `null` is sent for "at the top".
        parent_id: parentID === "" ? null : parentID,
        // ⛔ REQUIRED — see CreateCostCategoryRequest.created_by.
        created_by: "ui",
      });
      setCreating(false);
      setName("");
      setParentID("");
      await load();
      // ⛔ THROUGH THE ENVELOPE, and the null branch is not defensive padding.
      // The server replies {id, cost_centre} and sets cost_centre to NULL when the
      // read-back fails — the row IS written either way. Reading the envelope as if
      // it were the view is what produced "Created undefined on the undefined axis,
      // under undefined" on a create that had actually SUCCEEDED, with every test
      // green because the mock returned a flat object.
      const row = created.cost_centre;
      const message = row
        ? `Created ${row.path} on the ${row.cost_category_name} axis` +
          (row.parent_name === ""
            ? ", at the top of it."
            : `, under ${row.parent_name}.`)
        : `Created the centre. It IS saved — the server could not read it back to ` +
          `show you what it became${created.read_back_error ? ` (${created.read_back_error})` : ""}, ` +
          `so re-open this screen to see it in its axis rather than keying it again.`;
      setNotice(message);
      return message;
    } catch (e) {
      // THE SERVER'S OWN SENTENCE, on the screen and in the status line. A
      // duplicate name NAMES THE AXIS it clashed on, which is the part an operator
      // needs: the same name on a different axis is legal.
      const message = e instanceof Error ? e.message : String(e);
      setRefusal(message);
      setCreateDenied(e instanceof ApiError && e.status === 403);
      return message;
    } finally {
      setBusy(false);
    }
  }, [
    allocateNonRevenue,
    axisID,
    client,
    companyID,
    creating,
    kind,
    load,
    name,
    parentID,
  ]);

  useRegisterScreenAction("accept", submit);

  // Autofocus when the panel opens, so Alt+C leaves the operator keying.
  const firstFieldRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (creating) firstFieldRef.current?.focus();
  }, [creating]);

  /**
   * The centres grouped BY AXIS — the only grouping this screen offers.
   *
   * `unplaced` is the two-transactions case from (5): centres whose axis is not in
   * the served axis list. They are grouped by the `cost_category_name` that rode
   * along on each row, which is exactly why the server puts it there.
   */
  const grouped = useMemo(() => {
    const centres = data?.cost_centres ?? [];
    const byAxis = new Map<string, CostCentre[]>();
    for (const c of centres) {
      const list = byAxis.get(c.cost_category_id) ?? [];
      list.push(c);
      byAxis.set(c.cost_category_id, list);
    }
    const known = new Set(axes.map((a) => a.id));
    const unplaced = centres.filter((c) => !known.has(c.cost_category_id));
    return { byAxis, unplaced };
  }, [axes, data]);

  if (denied) {
    return (
      <section
        data-testid="cost-masters-denied"
        className="flex h-full flex-col bg-tally-bg p-4 font-mono text-tally-field"
      >
        <h1 className="text-lg font-bold text-tally-title">
          Cost Categories &amp; Cost Centres
        </h1>
        <p className="mt-2 max-w-3xl text-sm">
          You do not have permission to read or create this company&apos;s cost
          masters. That is a decision, not a fault: creating an axis of analysis
          decides how every future allocation is GROUPED, and a wrong axis is
          invisible on every voucher that used it — the allocation still totals the
          posting, it is just filed under the wrong heading. So the default belongs
          with whoever owns the chart of analysis, and the seeded Data Entry level
          is refused it with no rule written. An owner can grant it by adding an{" "}
          <span className="text-tally-title">allow cost-centre</span> rule in Users
          &amp; Roles — or{" "}
          <span className="text-tally-title">allow create cost-centre</span> for
          the write alone, since displaying and creating are separate actions on
          the one token.
        </p>
      </section>
    );
  }

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="text-lg font-bold text-tally-title">
        Cost Categories &amp; Cost Centres
      </h1>
      <p className="mt-1 max-w-3xl text-xs">
        A cost <span className="text-tally-title">CATEGORY</span> is an AXIS of
        analysis — Department, Branch, Project. A cost{" "}
        <span className="text-tally-title">CENTRE</span> is a POSITION on that
        axis. Several axes describe the SAME money from different directions: one
        salary is allocated in full across Departments AND AGAIN in full across
        Branches, so the two are never added together and centres are never listed
        outside their axis. Alt+C creates either.
        {busy ? " · working…" : ""}
      </p>

      {listError !== "" ? (
        <p
          data-testid="cost-masters-error"
          className="mt-3 max-w-3xl text-sm text-tally-bar"
        >
          {listError}
        </p>
      ) : null}

      {notice !== "" ? (
        <p
          data-testid="cost-masters-notice"
          className="mt-3 max-w-3xl border border-tally-fieldText p-2 text-sm"
        >
          {notice}
        </p>
      ) : null}

      {/* ---- THE CREATE PANEL (Alt+C). ------------------------------------- */}
      {creating && data !== null ? (
        <form
          data-testid="cost-master-panel"
          className="mt-4 max-w-3xl border border-tally-fieldText p-3 text-sm"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <h2 className="font-bold text-tally-title">Create</h2>

          {/* WHICH MASTER — asked, never defaulted. */}
          <fieldset className="mt-2">
            <legend className="text-xs text-tally-title">
              What are you creating? There is no default.
            </legend>
            <label className="mt-1 flex items-start gap-2 text-xs">
              <input
                type="radio"
                name="cost-master-kind"
                aria-label="Create a cost category (an axis)"
                checked={kind === "category"}
                onChange={() => setKind("category")}
              />
              <span>
                <span className="text-tally-title">A cost CATEGORY</span> — a new
                AXIS of analysis. Every posting you later allocate on it must be
                allocated in FULL, and the axis is complete or it is not; it says
                nothing about any other axis.
              </span>
            </label>
            <label className="mt-1 flex items-start gap-2 text-xs">
              <input
                type="radio"
                name="cost-master-kind"
                aria-label="Create a cost centre (a position on an axis)"
                checked={kind === "centre"}
                disabled={axes.length === 0}
                onChange={() => setKind("centre")}
              />
              <span>
                <span className="text-tally-title">A cost CENTRE</span> — a
                position on an existing axis, optionally under another centre.
                {axes.length === 0 ? (
                  <span className="text-tally-bar">
                    {" "}
                    Not available yet: this company has no axis, and there is no
                    uncategorised centre. Create a category first.
                  </span>
                ) : null}
              </span>
            </label>
          </fieldset>

          <label className="mt-3 flex flex-col text-xs">
            <span className="text-tally-title">Name</span>
            <input
              ref={firstFieldRef}
              aria-label="Name"
              className="mt-1 w-64 bg-tally-field px-1 text-tally-fieldText outline-none"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>

          {kind === "category" ? (
            <label className="mt-3 flex items-start gap-2 text-xs">
              <input
                type="checkbox"
                aria-label="Allocate non-revenue items"
                className="mt-0.5"
                checked={allocateNonRevenue}
                onChange={(e) => setAllocateNonRevenue(e.target.checked)}
              />
              <span>
                <span className="text-tally-title">
                  Allocate non-revenue items
                </span>{" "}
                — let this axis be used on a balance-sheet ledger as well as a P&amp;L
                one (Tally&apos;s own wording). Off is the ordinary case:
                departmental analysis is of revenue and expense. ⚠️ It also decides
                the DENOMINATOR the Cost Category Summary measures this axis&apos;s
                coverage against, so turning it on widens the base and the same
                allocations then cover a smaller share of it.
              </span>
            </label>
          ) : null}

          {kind === "centre" ? (
            <>
              {/* THE AXIS — PICKED from the served list. See (4). */}
              <label className="mt-3 flex flex-col text-xs">
                <span className="text-tally-title">
                  On which axis? Required — there is no uncategorised centre.
                </span>
                <select
                  aria-label="Cost category (axis)"
                  className="mt-1 w-64 bg-tally-field px-1 text-tally-fieldText outline-none"
                  value={axisID}
                  onChange={(e) => {
                    setAxisID(e.target.value);
                    // ⛔ THE PARENT IS CLEARED WITH THE AXIS. A parent from the
                    // previous axis is a 422 ("one axis would roll up into the
                    // other and the Category Summary would double-count") — and
                    // keeping it would put a stale name in a picker whose list has
                    // just changed under it.
                    setParentID("");
                  }}
                >
                  <option value="">— choose —</option>
                  {axes.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                      {a.allocate_non_revenue ? " (incl. non-revenue)" : ""}
                    </option>
                  ))}
                </select>
              </label>

              {/* THE PARENT — also picked, and only from THIS axis's centres. */}
              <label className="mt-3 flex flex-col text-xs">
                <span className="text-tally-title">
                  Under which centre? Leave at the top unless it is a sub-unit.
                </span>
                <select
                  aria-label="Parent cost centre"
                  className="mt-1 w-64 bg-tally-field px-1 text-tally-fieldText outline-none"
                  value={parentID}
                  disabled={axisID === ""}
                  onChange={(e) => setParentID(e.target.value)}
                >
                  <option value="">— at the top of the axis —</option>
                  {/* ⛔ ONLY THIS AXIS'S CENTRES ARE OFFERED. A parent on another
                      axis is refused with a 422, and offering one would be
                      offering the operator a mistake to make. */}
                  {(grouped.byAxis.get(axisID) ?? [])
                    .slice()
                    .sort((a, b) => a.path.localeCompare(b.path))
                    .map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.path}
                      </option>
                    ))}
                </select>
              </label>
            </>
          ) : null}

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
                setCreateDenied(false);
              }}
            >
              Close the panel
            </button>
          </div>

          {refusal !== "" ? (
            /* THE SERVER'S SENTENCE, verbatim. A 409 on a name NAMES THE AXIS,
               because the uniqueness index is per (company, CATEGORY, name) and
               the same name on another axis is legal — a message that hid that
               would read as a bug to an operator looking at the other one. */
            <p
              data-testid="cost-master-refusal"
              className="mt-3 max-w-3xl border border-tally-bar p-2 text-sm text-tally-bar"
            >
              {refusal}
            </p>
          ) : null}

          {createDenied ? (
            <p
              data-testid="cost-master-create-denied"
              className="mt-2 max-w-3xl border border-tally-fieldText p-2 text-sm"
            >
              That is a decision, not a fault. Displaying cost masters and creating
              them are SEPARATE actions on one{" "}
              <span className="text-tally-title">cost-centre</span> token, so a
              level that may see the chart of analysis and may not add to it is a
              real configuration — and you are in it. Nothing you keyed has been
              lost or sent. An owner can grant it with an{" "}
              <span className="text-tally-title">allow create cost-centre</span>{" "}
              rule in Users &amp; Roles.
            </p>
          ) : null}
        </form>
      ) : null}

      {/* ---- THE MASTERS, GROUPED BY AXIS. -------------------------------- */}
      {data !== null ? (
        <>
          {!creating ? (
            <p className="mt-4 text-xs">
              Alt+C creates a cost category or a cost centre.
            </p>
          ) : null}

          {axes.length === 0 ? (
            <p data-testid="cost-axes-empty" className="mt-3 max-w-3xl text-sm">
              This company has no cost categories, so nothing can be allocated to
              anything yet. That is a statement about the masters only — it does
              not mean the cost is not being incurred; it means none of it is
              analysed. Create an axis (Department, Branch, Project) with Alt+C,
              then a centre on it.
            </p>
          ) : (
            axes.map((axis) => (
              <AxisSection
                key={axis.id}
                axis={axis}
                centres={grouped.byAxis.get(axis.id) ?? []}
              />
            ))
          )}

          {grouped.unplaced.length > 0 ? (
            /* (5) THE TWO LISTS DISAGREED. Shown rather than dropped: a centre
               missing from its own master list is how an operator concludes it was
               never created and creates it a second time. */
            <div
              data-testid="cost-centres-unplaced"
              className="mt-5 max-w-3xl border border-tally-bar p-2 text-sm"
            >
              <h2 className="font-bold text-tally-bar">
                Centres whose axis is not in the list above
              </h2>
              <p className="mt-1 text-xs">
                The centres and the axes are read in SEPARATE transactions, so an
                axis created between the two reads yields centres it cannot be
                matched to. These are shown rather than hidden — a master dropped
                from its own list invites an operator to create it again. Each one
                names its own axis, which travelled on the row. Re-open this screen
                and they will normally join it.
              </p>
              <ul className="mt-2 list-none">
                {grouped.unplaced.map((c) => (
                  <li key={c.id} data-testid={`cost-centre-unplaced:${c.name}`}>
                    <span className="text-tally-title">
                      {c.cost_category_name}
                    </span>{" "}
                    / {c.path}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}

          <p className="mt-5 max-w-3xl text-xs">
            THERE IS NO RENAME AND NO RE-PARENT ON THIS SCREEN. The routes exist
            and no key reaches them, so a mis-typed name can only be corrected
            over HTTP for now — said here rather than left to be discovered, and
            note that it is not urgent for a reason: an allocation is stored
            against the centre&apos;s ID, so a rename changes what the reports are
            LABELLED and never which money is where.
          </p>
          {/* ⚠️ THIS PARAGRAPH USED TO SAY "NO SCREEN IN THIS BUILD ALLOCATES A
              POSTING", which was true when it was written and is now false. It is
              corrected rather than deleted: the false version is the plausible one and
              would be re-invented by anyone reading the masters screen and wondering
              where allocation happens. */}
          <p className="mt-2 max-w-3xl text-xs">
            Creating these masters does not allocate anything by itself — an axis with
            centres on it is a chart of analysis, not an analysis. COST IS ALLOCATED ON
            THE VOUCHER: key a Payment, Receipt or Contra (F5 / F6 / F4) in single-entry
            mode and each particular carries an{" "}
            <span className="text-tally-title">allocate</span> control that splits that
            posting across the centres of one or more axes. Each axis must total the
            posting EXACTLY, to the paisa — the server refuses a partial split and a
            database trigger refuses behind it — and the form shows the remaining
            balance per axis as you key it.
          </p>
          <p className="mt-2 max-w-3xl text-xs">
            NOT REACHABLE FROM A SCREEN, and said here rather than left to be
            discovered: a JOURNAL cannot be keyed from scratch in this build at all (the
            double-entry form has no posting grid), so a journal&apos;s allocations still
            arrive over HTTP. Allocation on an ITEM INVOICE is refused outright and
            always will be — its postings are generated inside the server&apos;s
            transaction, so a client-supplied posting number would attribute the cost to
            a receivable. Until a period has allocated vouchers in it, the Cost Category
            Summary will report that period&apos;s cost as unallocated, which is the
            truth rather than an error.
          </p>
        </>
      ) : listError === "" ? (
        <p className="mt-3 text-sm">Loading…</p>
      ) : null}
    </section>
  );
}

/**
 * ONE AXIS and the centres on it — the only unit this screen renders.
 *
 * The tree is built from `parent_id` (see {@link buildForest}); `depth` is used
 * only as an INDENT once the parentage is known, never as the ordering.
 */
function AxisSection({
  axis,
  centres,
}: {
  axis: CostCategory;
  centres: CostCentre[];
}) {
  // (3) A stored parent CYCLE. The hierarchy is not drawn at all when any centre
  // on the axis carries the flag: a drawn tree would be a picture of something
  // that cannot exist, and `path` is the field that cannot be trusted.
  const truncated = centres.filter((c) => c.ancestry_truncated);
  const { roots, orphans } = buildForest(centres);

  return (
    <div data-testid={`cost-axis:${axis.name}`} className="mt-4 max-w-3xl">
      <h2 className="font-bold text-tally-title">
        {axis.name}
        <span className="ml-2 text-xs font-normal text-tally-field">
          {/* SERVER-DERIVED. Not centres.length: the two lists are read in
              separate transactions, so a disagreement between this count and the
              rows below is a FACT about the read and is left visible. */}
          {axis.centre_count} centre(s) ·{" "}
          {axis.allocate_non_revenue
            ? "may carry balance-sheet ledgers too"
            : "revenue and expense ledgers only"}
        </span>
      </h2>

      {truncated.length > 0 ? (
        <p
          data-testid={`cost-axis-truncated:${axis.name}`}
          className="mt-1 border border-tally-bar p-2 text-xs text-tally-bar"
        >
          THE STORED PARENTAGE ON THIS AXIS CONTAINS A CYCLE, so the hierarchy is
          NOT drawn and the centres are listed flat. This should be impossible: the
          write path refuses to create a cycle, and no database constraint catches
          one beyond a one-hop self-reference. Nothing here is safe to read as a
          roll-up — the Cost Centre Summary will report these centres as
          unattached, which is where their money will be.
        </p>
      ) : null}

      {centres.length === 0 ? (
        <p
          data-testid={`cost-axis-empty:${axis.name}`}
          className="mt-1 text-sm"
        >
          No centres on this axis yet, so nothing can be allocated on it. An axis
          with no centres allocates nothing at all — it is not a partial analysis,
          it is none.
        </p>
      ) : truncated.length > 0 ? (
        <ul className="mt-1 list-none">
          {centres
            .slice()
            .sort((a, b) => a.name.localeCompare(b.name))
            .map((c) => (
              <li
                key={c.id}
                data-testid={`cost-centre:${axis.name}:${c.name}`}
                className="text-sm"
              >
                {c.name}
              </li>
            ))}
        </ul>
      ) : (
        <ul className="mt-1 list-none">
          {roots.map((n) => (
            <CentreRow key={n.centre.id} node={n} axisName={axis.name} />
          ))}
          {orphans.map((c) => (
            <li
              key={c.id}
              data-testid={`cost-centre-orphan:${axis.name}:${c.name}`}
              className="text-sm text-tally-bar"
            >
              {c.name} — its parent ({c.parent_name || "unnamed"}) is not in this
              list, so it is shown here rather than under a parent that was
              guessed.
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** One centre and its children. Indented by the DERIVED depth, not the wire's. */
function CentreRow({
  node,
  axisName,
  indent = 0,
}: {
  node: CentreNode;
  axisName: string;
  indent?: number;
}) {
  const c = node.centre;
  return (
    <>
      <li
        data-testid={`cost-centre:${axisName}:${c.name}`}
        className="text-sm"
        style={{ paddingLeft: `${indent * 1.25}rem` }}
      >
        {c.name}
      </li>
      {node.children.map((child) => (
        <CentreRow
          key={child.centre.id}
          node={child}
          axisName={axisName}
          indent={indent + 1}
        />
      ))}
    </>
  );
}
