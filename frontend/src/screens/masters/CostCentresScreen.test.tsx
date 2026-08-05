import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  act,
} from "@testing-library/react";

import { CostCentresScreen, buildForest } from "./CostCentresScreen";
import { AppShell } from "../../app/AppShell";
import { CompanyProvider } from "../../app/CompanyContext";
import {
  ScreenActionsProvider,
  useScreenActionDispatcher,
} from "../../app/ScreenActions";
import { createMockClient } from "../../api/mock";
import { ApiError } from "../../api";
import type {
  CoredClient,
  CostCategory,
  CostCentre,
  CostCentreListResponse,
  CreateCostCentreRequest,
} from "../../api";
import type { NavSnapshot } from "../../keystroke/navMachine";

/**
 * THE COST MASTERS SCREEN (T2).
 *
 * ⚠️ WHICH TESTS PROVE WHAT. The suite at the bottom marked "THROUGH THE REAL
 * SHELL" renders `AppShell` and presses the real combo; it is the ONLY suite here
 * that exercises AppShell's SCREEN_ACTIONS map — the registration point that
 * shipped missing for Alt+O with 459 green tests. Delete
 * `createCostMaster: "createCostMaster"` from that map and those tests fail and
 * nothing else in the repo does. Every other suite calls the dispatcher directly:
 * they prove the SCREEN, and nothing whatsoever about routing.
 *
 * ⚠️ REFUSAL TEXT IS CAPTURED, NEVER RETYPED. The mock's sentences are hand copies
 * of the server's, and a hand copy is what went stale for two days on the TDS
 * register. Each refusal test asks the CLIENT for the message it throws for the
 * same request and looks for THAT in the DOM, so a wording change moves both and
 * the only way to fail is to stop rendering the server's sentence.
 */

afterEach(cleanup);

const COMPANY = "company-demo";

const SNAP = {
  screen: "cost-centres",
  screenStackDepth: 2,
  canReturnToVoucher: false,
} as unknown as NavSnapshot;

function axisView(over: Partial<CostCategory> = {}): CostCategory {
  return {
    id: "cost-cat-department",
    name: "Department",
    allocate_non_revenue: false,
    centre_count: 1,
    created_by: "seed",
    created_at: "2026-04-01T00:00:00Z",
    ...over,
  };
}

function centreView(over: Partial<CostCentre> = {}): CostCentre {
  return {
    id: "cost-ctr-sales",
    name: "Sales",
    cost_category_id: "cost-cat-department",
    cost_category_name: "Department",
    parent_id: "",
    parent_name: "",
    depth: 0,
    path: "Sales",
    ancestry_truncated: false,
    created_by: "seed",
    created_at: "2026-04-01T00:00:00Z",
    ...over,
  };
}

function listView(
  over: Partial<CostCentreListResponse> = {},
): CostCentreListResponse {
  return {
    cost_centres: [centreView()],
    cost_categories: [axisView()],
    ...over,
  };
}

/** A client serving one hand-built list response (and counting the calls). */
function listingClient(res: CostCentreListResponse): CoredClient {
  return {
    ...createMockClient({ seed: false }),
    listCostCentres: vi.fn(async () => res),
  } as unknown as CoredClient;
}

/**
 * Render the screen with a dispatcher that fires its registered actions the way
 * AppShell would — which proves they are registered, and nothing more.
 */
function renderScreen(client: CoredClient) {
  let fire: (kind: "createCostMaster" | "accept") => Promise<string | void> =
    async () => {};
  function Harness() {
    const dispatch = useScreenActionDispatcher();
    fire = async (kind) => {
      const res = dispatch(kind, SNAP);
      if (res.pending) return await res.pending;
      return res.message;
    };
    return <CostCentresScreen />;
  }
  render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: COMPANY, name: "Demo Traders" }}
    >
      <ScreenActionsProvider>
        <Harness />
      </ScreenActionsProvider>
    </CompanyProvider>,
  );
  return {
    create: () => act(async () => void (await fire("createCostMaster"))),
    accept: () => act(async () => void (await fire("accept"))),
    lastMessage: async (kind: "createCostMaster" | "accept") => {
      let out: string | void = undefined;
      await act(async () => {
        out = await fire(kind);
      });
      return out as string | undefined;
    },
  };
}

/** Wait for the list to be on screen (every test needs it loaded first). */
async function loaded() {
  await screen.findByText(/Alt\+C creates a cost category/);
}

// ---------------------------------------------------------------------------
// THE TREE. The pure derivation first, because it is where the wrong answer is
// silent: a tree that is confidently wrong about which centre rolls up into which.
// ---------------------------------------------------------------------------
describe("the hierarchy is built from parent_id, not from the received order", () => {
  /**
   * ⛔ THE ORDER THE SERVER SENDS IS (axis name, centre name) AND NOT
   * parent-before-child. So indenting the received list by `depth` — which is what
   * a flat list plus a depth column invites — puts "Andheri" under whatever
   * happened to precede it alphabetically.
   *
   * These centres are given in the order the server would send them: Andheri
   * (depth 1, child of Mumbai) FIRST, because A sorts before M.
   */
  const mumbai = centreView({
    id: "c-mumbai",
    name: "Mumbai",
    cost_category_id: "cost-cat-branch",
    cost_category_name: "Branch",
    path: "Mumbai",
  });
  const andheri = centreView({
    id: "c-andheri",
    name: "Andheri",
    cost_category_id: "cost-cat-branch",
    cost_category_name: "Branch",
    parent_id: "c-mumbai",
    parent_name: "Mumbai",
    depth: 1,
    path: "Mumbai / Andheri",
  });
  const nashik = centreView({
    id: "c-nashik",
    name: "Nashik",
    cost_category_id: "cost-cat-branch",
    cost_category_name: "Branch",
    path: "Nashik",
  });

  it("puts a child under its OWN parent even when it arrives first", () => {
    const { roots, orphans } = buildForest([andheri, mumbai, nashik]);
    expect(orphans).toEqual([]);
    // Two roots, in name order, and Andheri is NOT one of them — the failure this
    // guards would have made it a third root or a child of nothing.
    expect(roots.map((r) => r.centre.name)).toEqual(["Mumbai", "Nashik"]);
    const mum = roots.find((r) => r.centre.name === "Mumbai")!;
    expect(mum.children.map((c) => c.centre.name)).toEqual(["Andheri"]);
    expect(roots.find((r) => r.centre.name === "Nashik")!.children).toEqual([]);
  });

  it("renders the child AFTER its parent in the DOM, not first", async () => {
    // The control for the assertion above at the rendering layer: the naive
    // implementation renders Andheri first (it arrives first), indented under
    // nothing.
    renderScreen(
      listingClient(
        listView({
          cost_centres: [andheri, mumbai, nashik],
          cost_categories: [
            axisView({ id: "cost-cat-branch", name: "Branch", centre_count: 3 }),
          ],
        }),
      ),
    );
    await loaded();
    const rows = screen
      .getByTestId("cost-axis:Branch")
      .querySelectorAll("li[data-testid^='cost-centre:']");
    expect([...rows].map((r) => r.getAttribute("data-testid"))).toEqual([
      "cost-centre:Branch:Mumbai",
      "cost-centre:Branch:Andheri",
      "cost-centre:Branch:Nashik",
    ]);
    // And the child is INDENTED relative to its parent, which is the only visible
    // difference between "under Mumbai" and "a third branch".
    const child = screen.getByTestId("cost-centre:Branch:Andheri");
    expect(child.getAttribute("style")).toContain("padding-left");
  });

  it("shows a centre whose parent is not in the list, rather than dropping it", () => {
    const stray = centreView({
      id: "c-stray",
      name: "Worli",
      cost_category_id: "cost-cat-branch",
      cost_category_name: "Branch",
      parent_id: "c-not-here",
      parent_name: "Ghost",
      depth: 1,
      path: "Ghost / Worli",
    });
    const { roots, orphans } = buildForest([stray, mumbai]);
    expect(roots.map((r) => r.centre.name)).toEqual(["Mumbai"]);
    // A centre with real allocations behind it does not become unreal because its
    // parent is missing from one read.
    expect(orphans.map((o) => o.name)).toEqual(["Worli"]);
  });
});

describe("centres are grouped BY AXIS, never listed flat", () => {
  it("puts each centre under its own axis, with the axis's own rule beside it", async () => {
    renderScreen(createMockClient({ seed: false }));
    await loaded();
    // The seeded fixture is deliberately TWO axes over one posting.
    const dept = screen.getByTestId("cost-axis:Department");
    const branch = screen.getByTestId("cost-axis:Branch");
    expect(dept.textContent).toContain("Sales");
    expect(dept.textContent).toContain("Admin");
    expect(branch.textContent).toContain("Mumbai");
    expect(branch.textContent).toContain("Delhi");
    // ⛔ AND NOT THE OTHER WAY ROUND. A flat list of centre names is ambiguous —
    // "Mumbai" may be both a Branch and a Project — so this is the assertion that
    // the grouping is real and not decorative.
    expect(dept.textContent).not.toContain("Mumbai");
    expect(branch.textContent).not.toContain("Sales");
    // The axis's own rule travels with it, because it decides the denominator the
    // Cost Category Summary measures coverage against.
    expect(dept.textContent).toMatch(/revenue and expense ledgers only/);
  });

  it("shows the SERVER's centre count, not the number of rows it drew", async () => {
    // The two lists are read in separate transactions, so a disagreement between
    // them is a FACT about the read. Recomputing the count from the rows would hide
    // it — and hiding it is how "where did my centre go" becomes "create it again".
    renderScreen(
      listingClient(
        listView({
          cost_categories: [axisView({ centre_count: 7 })],
          cost_centres: [centreView()],
        }),
      ),
    );
    await loaded();
    expect(screen.getByTestId("cost-axis:Department").textContent).toContain(
      "7 centre(s)",
    );
  });

  it("states an axis with no centres instead of drawing an empty box", async () => {
    renderScreen(
      listingClient(listView({ cost_centres: [], cost_categories: [axisView()] })),
    );
    await loaded();
    const said =
      screen.getByTestId("cost-axis-empty:Department").textContent ?? "";
    // An axis with no centres allocates NOTHING — it is not a partial analysis.
    expect(said).toMatch(/nothing can be allocated on it/);
    expect(said).toMatch(/it is none/);
  });

  it("states a company with no axes at all, and what that means", async () => {
    renderScreen(
      listingClient(listView({ cost_centres: [], cost_categories: [] })),
    );
    await loaded();
    const said = screen.getByTestId("cost-axes-empty").textContent ?? "";
    expect(said).toMatch(/nothing can be allocated/);
    // The distinction that matters: the cost is still being incurred.
    expect(said).toMatch(/does not mean the cost is not being incurred/);
  });
});

describe("the two faults the wire can carry are rendered, not absorbed", () => {
  it("refuses to DRAW a hierarchy when ancestry_truncated is set", async () => {
    // The flag says the STORED rows contain a parent cycle. Drawing a tree from
    // `path` would be a picture of something that cannot exist, so the axis is
    // listed flat and the flag is stated.
    renderScreen(
      listingClient(
        listView({
          cost_centres: [
            centreView({ ancestry_truncated: true }),
            centreView({
              id: "c-2",
              name: "Admin",
              parent_id: "cost-ctr-sales",
              parent_name: "Sales",
              depth: 1,
              path: "Sales / Admin",
              ancestry_truncated: true,
            }),
          ],
          cost_categories: [axisView({ centre_count: 2 })],
        }),
      ),
    );
    await loaded();
    const said =
      screen.getByTestId("cost-axis-truncated:Department").textContent ?? "";
    expect(said).toMatch(/CONTAINS A CYCLE/);
    expect(said).toMatch(/NOT drawn/);
    // It also says where the money will BE, which is the actionable half.
    expect(said).toMatch(/unattached/);
    // FLAT: the child is rendered with no indent, because its parentage is exactly
    // what cannot be trusted.
    const child = screen.getByTestId("cost-centre:Department:Admin");
    expect(child.getAttribute("style")).toBeNull();
  });

  it("shows centres whose axis is missing from the axis list", async () => {
    // api/types.ts: `cost_centres` and `cost_categories` are read in SEPARATE
    // transactions, so this is a documented, expected disagreement — not corruption.
    renderScreen(
      listingClient(
        listView({
          cost_centres: [
            centreView(),
            centreView({
              id: "c-mystery",
              name: "Ahmedabad",
              cost_category_id: "cost-cat-branch",
              cost_category_name: "Branch",
              path: "Ahmedabad",
            }),
          ],
          // Branch is NOT in the axis list.
          cost_categories: [axisView()],
        }),
      ),
    );
    await loaded();
    const panel = screen.getByTestId("cost-centres-unplaced");
    expect(panel.textContent).toMatch(/SEPARATE transactions/);
    // The centre is named WITH its axis, which is why the server puts
    // cost_category_name on every row.
    const row = screen.getByTestId("cost-centre-unplaced:Ahmedabad");
    expect(row.textContent).toContain("Branch");
    expect(row.textContent).toContain("Ahmedabad");
  });
});

describe("the create panel asks for nothing an operator cannot see", () => {
  it("PRE-SELECTS NEITHER master, and offers no box for an id", async () => {
    const h = renderScreen(createMockClient({ seed: false }));
    await loaded();
    await h.create();
    expect(screen.getByTestId("cost-master-panel")).toBeTruthy();

    const radios = screen.getAllByRole("radio") as HTMLInputElement[];
    expect(radios.length).toBe(2);
    for (const r of radios) expect(r.checked).toBe(false);

    // ⛔ THE BOX THAT MUST NOT EXIST. cost_category_id is a UUID that NO screen in
    // this product displays, so a text field for it could not be filled by a human
    // — the defect the TDS challan form shipped and had to be rebuilt for.
    expect(screen.queryByLabelText("Cost category id")).toBeNull();
    expect(screen.queryByLabelText("Parent id")).toBeNull();
  });

  it("will not offer a CENTRE when there is no axis to put it on", async () => {
    const h = renderScreen(
      listingClient(listView({ cost_centres: [], cost_categories: [] })),
    );
    await loaded();
    await h.create();
    const centreOption = screen.getByLabelText(
      "Create a cost centre (a position on an axis)",
    ) as HTMLInputElement;
    // There is no uncategorised centre, so this is not a form to be filled in
    // hopefully — it is unavailable, with the reason on screen.
    expect(centreOption.disabled).toBe(true);
    expect(screen.getByTestId("cost-master-panel").textContent).toMatch(
      /no uncategorised centre/,
    );
    // The CONTROL: the category option is offered.
    expect(
      (
        screen.getByLabelText(
          "Create a cost category (an axis)",
        ) as HTMLInputElement
      ).disabled,
    ).toBe(false);
  });

  it("offers ONLY this axis's centres as a parent", async () => {
    const h = renderScreen(createMockClient({ seed: false }));
    await loaded();
    await h.create();
    fireEvent.click(
      screen.getByLabelText("Create a cost centre (a position on an axis)"),
    );
    fireEvent.change(screen.getByLabelText("Cost category (axis)"), {
      target: { value: "cost-cat-department" },
    });
    const parent = screen.getByLabelText(
      "Parent cost centre",
    ) as HTMLSelectElement;
    const options = [...parent.options].map((o) => o.textContent);
    expect(options).toContain("Sales");
    expect(options).toContain("Admin");
    // ⛔ A PARENT ON ANOTHER AXIS IS A 422 ("one axis would roll up into the other
    // and the Category Summary would double-count"), so offering one would be
    // offering the operator a mistake to make.
    expect(options).not.toContain("Mumbai");
    expect(options).not.toContain("Delhi");
    // And the default is the top of the axis, not the first centre in the list.
    expect(parent.value).toBe("");
  });

  it("clears a chosen parent when the axis changes under it", async () => {
    const h = renderScreen(createMockClient({ seed: false }));
    await loaded();
    await h.create();
    fireEvent.click(
      screen.getByLabelText("Create a cost centre (a position on an axis)"),
    );
    const axis = screen.getByLabelText("Cost category (axis)");
    fireEvent.change(axis, { target: { value: "cost-cat-department" } });
    fireEvent.change(screen.getByLabelText("Parent cost centre"), {
      target: { value: "cost-ctr-sales" },
    });
    expect(
      (screen.getByLabelText("Parent cost centre") as HTMLSelectElement).value,
    ).toBe("cost-ctr-sales");

    fireEvent.change(axis, { target: { value: "cost-cat-branch" } });
    // A parent from the previous axis is a 422, and leaving it selected would put a
    // name in the box that is no longer in the list behind it.
    expect(
      (screen.getByLabelText("Parent cost centre") as HTMLSelectElement).value,
    ).toBe("");
  });
});

describe("the wire carries the served id, and never the nil uuid", () => {
  function spyClient() {
    const base = createMockClient({ seed: false });
    const createCostCentre = vi.fn(base.createCostCentre);
    return {
      client: { ...base, createCostCentre } as unknown as CoredClient,
      createCostCentre,
    };
  }

  it("sends the axis id the operator PICKED and parent_id null for the top", async () => {
    const { client, createCostCentre } = spyClient();
    const h = renderScreen(client);
    await loaded();
    await h.create();
    fireEvent.click(
      screen.getByLabelText("Create a cost centre (a position on an axis)"),
    );
    fireEvent.change(screen.getByLabelText("Cost category (axis)"), {
      target: { value: "cost-cat-branch" },
    });
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Pune" },
    });
    await h.accept();

    expect(createCostCentre).toHaveBeenCalledTimes(1);
    const req = createCostCentre.mock.calls[0]![1] as CreateCostCentreRequest;
    expect(req.cost_category_id).toBe("cost-cat-branch");
    expect(req.name).toBe("Pune");
    // ⛔ null, NEVER "00000000-0000-0000-0000-000000000000". The route refuses the
    // all-zeros uuid with its own sentence precisely because a client sending it
    // would silently move a centre to the top of its axis and change which totals
    // it rolls into.
    expect(req.parent_id).toBeNull();
  });

  it("sends the PICKED parent's id when one was chosen", async () => {
    const { client, createCostCentre } = spyClient();
    const h = renderScreen(client);
    await loaded();
    await h.create();
    fireEvent.click(
      screen.getByLabelText("Create a cost centre (a position on an axis)"),
    );
    fireEvent.change(screen.getByLabelText("Cost category (axis)"), {
      target: { value: "cost-cat-department" },
    });
    fireEvent.change(screen.getByLabelText("Parent cost centre"), {
      target: { value: "cost-ctr-sales" },
    });
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Inside Sales" },
    });
    await h.accept();
    const req = createCostCentre.mock.calls[0]![1] as CreateCostCentreRequest;
    expect(req.parent_id).toBe("cost-ctr-sales");
    // The confirmation names the PATH the server derived, so the operator can see
    // which centre it went under rather than trusting that the picker was right.
    expect(screen.getByTestId("cost-masters-notice").textContent).toMatch(
      /Sales \/ Inside Sales/,
    );
  });

  it("creates an AXIS and says what is still missing", async () => {
    const h = renderScreen(createMockClient({ seed: false }));
    await loaded();
    await h.create();
    fireEvent.click(screen.getByLabelText("Create a cost category (an axis)"));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Project" },
    });
    fireEvent.click(screen.getByLabelText("Allocate non-revenue items"));
    const msg = await h.lastMessage("accept");
    // The panel closes and the operator is told the axis is not yet usable: an axis
    // with no centres allocates nothing.
    expect(screen.queryByTestId("cost-master-panel")).toBeNull();
    expect(msg).toMatch(/no centres yet/);
    // And that the flag they ticked widens the coverage denominator.
    expect(msg).toMatch(/balance-sheet ledgers/);
  });
});

describe("every refusal is the SERVER's sentence, or this screen's own", () => {
  it("renders the 409 that NAMES THE AXIS a centre name clashes on", async () => {
    const client = createMockClient({ seed: false });
    // Captured, not retyped. The mock refuses before storing anything, so asking
    // first cannot change what the screen then meets.
    let expected = "";
    try {
      await client.createCostCentre(COMPANY, {
        cost_category_id: "cost-cat-department",
        name: "Sales",
        created_by: "ui",
      });
    } catch (e) {
      expected = (e as ApiError).message;
    }
    // The message must name the axis: the index is per (company, CATEGORY, name),
    // so the same name on another axis is legal and a message that hid that would
    // read as a bug to an operator looking at the other Mumbai.
    expect(expected).toMatch(/Department/);
    expect(expected).toMatch(/DIFFERENT axis/);

    const h = renderScreen(client);
    await loaded();
    await h.create();
    fireEvent.click(
      screen.getByLabelText("Create a cost centre (a position on an axis)"),
    );
    fireEvent.change(screen.getByLabelText("Cost category (axis)"), {
      target: { value: "cost-cat-department" },
    });
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Sales" },
    });
    await h.accept();

    expect(screen.getByTestId("cost-master-refusal").textContent).toBe(expected);
    // The panel STAYS OPEN with what was keyed: one field has to be corrected, not
    // the whole master re-keyed.
    expect(screen.getByTestId("cost-master-panel")).toBeTruthy();
  });

  it("makes its own two refusals locally, and sends NOTHING", async () => {
    const base = createMockClient({ seed: false });
    const createCostCentre = vi.fn(base.createCostCentre);
    const createCostCategory = vi.fn(base.createCostCategory);
    const client = {
      ...base,
      createCostCentre,
      createCostCategory,
    } as unknown as CoredClient;
    const h = renderScreen(client);
    await loaded();
    await h.create();

    // (a) Which master? There is no request to send until this is known — they are
    // different routes, so this is not duplicated validation.
    const noKind = await h.lastMessage("accept");
    expect(noKind).toMatch(/Say which you are creating first/);
    expect(createCostCentre).not.toHaveBeenCalled();
    expect(createCostCategory).not.toHaveBeenCalled();

    // (b) A centre with no axis. cost_category_id is REQUIRED — there is no
    // uncategorised centre — and the panel refuses before the route has to.
    fireEvent.click(
      screen.getByLabelText("Create a cost centre (a position on an axis)"),
    );
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Pune" },
    });
    const noAxis = await h.lastMessage("accept");
    expect(noAxis).toMatch(/no uncategorised cost/);
    expect(createCostCentre).not.toHaveBeenCalled();
  });

  it("renders a 403 on the READ as the DECISION it is", async () => {
    const denied = {
      ...createMockClient({ seed: false }),
      listCostCentres: vi.fn().mockRejectedValue(new ApiError(403, "forbidden")),
    } as unknown as CoredClient;
    const h = renderScreen(denied);
    const panel = await screen.findByTestId("cost-masters-denied");
    // A decision, with the REASON: creating an axis decides how every future
    // allocation is grouped, and a wrong axis is invisible on every voucher.
    expect(panel.textContent).toMatch(/decision, not a fault/);
    expect(panel.textContent).toMatch(/invisible on every voucher/);
    expect(panel.textContent).toMatch(/allow cost-centre/);
    // And Alt+C answers the same decision rather than opening a panel that could
    // only be refused on save.
    const msg = await h.lastMessage("createCostMaster");
    expect(msg).toMatch(/not permitted/);
  });

  it("renders a 403 on the WRITE with the same framing, naming the create rule", async () => {
    const base = createMockClient({ seed: false });
    const client = {
      ...base,
      createCostCategory: vi
        .fn()
        .mockRejectedValue(
          new ApiError(403, "forbidden: create cost-centre is not allowed"),
        ),
    } as unknown as CoredClient;
    const h = renderScreen(client);
    await loaded();
    await h.create();
    fireEvent.click(screen.getByLabelText("Create a cost category (an axis)"));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Project" },
    });
    await h.accept();

    // The server's sentence, verbatim...
    expect(screen.getByTestId("cost-master-refusal").textContent).toBe(
      "forbidden: create cost-centre is not allowed",
    );
    // ...plus the framing, because Display and Create are separate actions on one
    // token and an operator who has just keyed a master is owed more than an ACL line.
    const framing =
      screen.getByTestId("cost-master-create-denied").textContent ?? "";
    expect(framing).toMatch(/decision, not a fault/);
    expect(framing).toMatch(/allow create cost-centre/);
    expect(screen.getByTestId("cost-master-panel")).toBeTruthy();
  });

  it("Alt+C refuses honestly when the list could not be read", async () => {
    // The Alt+O signature in the other direction: a status line that reports
    // success while nothing appears. The panel's axis picker is built from the
    // list, so it cannot stand without one.
    const client = {
      ...createMockClient({ seed: false }),
      listCostCentres: vi
        .fn()
        .mockRejectedValue(new ApiError(500, "masters: read cost centres: boom")),
    } as unknown as CoredClient;
    const h = renderScreen(client);
    expect(
      (await screen.findByTestId("cost-masters-error")).textContent,
    ).toContain("boom");

    const msg = (await h.lastMessage("createCostMaster")) ?? "";
    const panelUp = screen.queryByTestId("cost-master-panel") !== null;
    // Either a panel appears or the message is an honest refusal — never a success
    // line with no panel.
    expect(panelUp || !/Ctrl\+A accepts it/.test(msg)).toBe(true);
    expect(panelUp).toBe(false);
    expect(msg).toMatch(/could not be read/);
  });
});

// ---------------------------------------------------------------------------
// THROUGH THE REAL SHELL — the only tests here that prove the KEY works.
//
// ⛔ Alt+O shipped completely dead with its keymap binding, its ScreenActionKind,
// its navMachine case, its bar item and its screen handler ALL present, because
// AppShell's SCREEN_ACTIONS map had no entry: the shell resolved the combo, found
// no screen action, and forwarded the key to a machine that deliberately does
// nothing. No error, no status line, nothing.
//
// Each test carries a CONTROL asserting the panel is ABSENT first, so it cannot
// pass on a screen that renders the panel from the start.
// ---------------------------------------------------------------------------
describe("a screen-owned key is actually ROUTED by the shell", () => {
  function renderShell() {
    render(
      <CompanyProvider
        client={createMockClient()}
        initialCompany={{ id: COMPANY, name: "Demo Traders" }}
      >
        <AppShell />
      </CompanyProvider>,
    );
    return screen.getByRole("application");
  }

  /** Gateway -> Cost Centres, with the masters loaded. */
  async function openMasters() {
    const app = renderShell();
    // Through the GATEWAY's own list, which is also the assertion that the machine
    // matches this label BEFORE the report-name fallback: a fallback match would
    // push a `report` frame called "Cost Centres" and the panel below would never
    // appear.
    fireEvent.mouseDown(screen.getByText("Cost Centres"));
    fireEvent.keyDown(app, { key: "Enter" });
    await screen.findByText(/Alt\+C creates a cost category/);
    return app;
  }

  it("Alt+C opens the create panel on the cost masters screen", async () => {
    const app = await openMasters();
    // THE CONTROL: we are on the masters screen and the panel is NOT up.
    expect(screen.queryByTestId("cost-master-panel")).toBeNull();
    expect(
      screen.getByRole("button", { name: /Alt\+C Create/ }),
    ).toBeTruthy();

    fireEvent.keyDown(app, { key: "c", altKey: true });

    // If SCREEN_ACTIONS is missing its `createCostMaster` entry, THIS is the line
    // that fails — and it is the only kind of test that does. Note what would still
    // be green: Alt+C resolves, the action has a label, the button is on the bar,
    // and the screen's handler is registered.
    expect(screen.getByTestId("cost-master-panel")).toBeTruthy();
    // And once a CENTRE is being created, the axis picker inside it is populated
    // from the same response the list came in — so nothing has to be typed. (The
    // picker only exists for a centre: a category has no axis to sit on.)
    fireEvent.click(
      screen.getByLabelText("Create a cost centre (a position on an axis)"),
    );
    const axisPicker = screen.getByLabelText(
      "Cost category (axis)",
    ) as HTMLSelectElement;
    expect([...axisPicker.options].map((o) => o.textContent)).toContain(
      "Department",
    );
  });

  it("the Alt+C BUTTON does the same thing as the key", async () => {
    await openMasters();
    expect(screen.queryByTestId("cost-master-panel")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Alt\+C Create/ }));
    expect(screen.getByTestId("cost-master-panel")).toBeTruthy();
  });

  it("F5 does NOT open a Payment voucher over the panel", async () => {
    // An unbound key is not inert. Without this screen's own binding the GLOBAL F5
    // pushes a Payment voucher on top of a half-keyed centre and the frame that
    // held it is gone. F9 and F8 would do the same with Purchase and Sales.
    const app = await openMasters();
    fireEvent.keyDown(app, { key: "c", altKey: true });
    expect(screen.getByTestId("cost-master-panel")).toBeTruthy();

    fireEvent.keyDown(app, { key: "F5" });
    expect(screen.queryByText(/Payment Voucher/)).toBeNull();
    expect(screen.getByTestId("cost-master-panel")).toBeTruthy();
  });

  it("the bar is the COST MASTERS bar, not the Gateway's", async () => {
    // Without a case in buttonBarFor this screen falls through to GATEWAY_BAR and
    // NO OTHER TEST CATCHES IT: the bar would advertise F3 Company and F11 Features
    // over a half-keyed centre and say nothing about Alt+C.
    await openMasters();
    expect(screen.getByRole("button", { name: /Alt\+C Create/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /F11 Features/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /F3 Company/ })).toBeNull();
  });
});
