import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  waitFor,
  fireEvent,
} from "@testing-library/react";
import {
  COMPANY_STORAGE_KEY,
  CompanyProvider,
  useCompanyContext,
} from "./CompanyContext";
import { createMockClient, ApiError } from "../api";
import type { CoredClient } from "../api";

/**
 * The company BOOTSTRAP precedence: which set of books the app opens, and what
 * it does when the server refuses the one that was asked for.
 *
 * The defect these tests exist to prevent is the most dangerous shape in this
 * codebase, because it is INVISIBLE: a `?company=<id>` the server answered 403
 * for used to be treated as a dead id, dropped, and REPLACED WITH A BRAND-NEW
 * COMPANY owned by the current user. Every screen then answered 200 against
 * that empty tenant, so the app looked perfectly healthy while the operator (or
 * an auditor) believed they were looking at the requested company's books.
 *
 * So each test below asserts on BOTH halves: what is on screen, and whether a
 * company was created.
 */

afterEach(cleanup);

/** A uuid-shaped id, which is the only shape readCompanyIdFromURL accepts. */
const URL_ID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
const OTHER_URL_ID = "11111111-2222-3333-4444-555555555555";

beforeEach(() => {
  globalThis.localStorage?.clear();
  // Every test declares its own URL; a leftover ?company= from a previous one
  // would silently change what "the operator asked for".
  globalThis.history.replaceState({}, "", "/");
});

function openWith(company: string | null): void {
  globalThis.history.replaceState({}, "", company ? `/?company=${company}` : "/");
}

/** Observes the selected company (null renders as "none"). */
function ShowCompany() {
  const { company } = useCompanyContext();
  return <div data-testid="active-company">{company ? company.id : "none"}</div>;
}

function bootClient(getCompany: CoredClient["getCompany"]): {
  client: CoredClient;
  createCompany: ReturnType<typeof vi.fn>;
} {
  const base = createMockClient({ seed: false });
  const createCompany = vi.fn(async (req: unknown) =>
    base.createCompany(req as Parameters<CoredClient["createCompany"]>[0]),
  );
  return {
    client: {
      ...base,
      getCompany,
      createCompany,
    } as unknown as CoredClient,
    createCompany,
  };
}

const refuse = (status: number, msg: string) =>
  vi.fn(async () => {
    throw new ApiError(status, msg, msg);
  }) as unknown as CoredClient["getCompany"];

function renderBootstrap(client: CoredClient) {
  return render(
    <CompanyProvider client={client} bootstrap>
      <ShowCompany />
    </CompanyProvider>,
  );
}

/**
 * Wait for the bootstrap to reach a TERMINAL state — deliberately one that BOTH
 * the fixed and the broken behaviour reach (the fix renders a notice; the old
 * code minted and opened a company). Waiting on the notice instead would make a
 * regression fail with "could not find the notice", burying the fact that a
 * whole new tenant was created. The assertion that must fire on a regression is
 * the one that names the danger.
 */
async function bootstrapSettled(): Promise<void> {
  await waitFor(() =>
    expect(
      screen.queryByTestId("company-refused-notice") !== null ||
        screen.getByTestId("active-company").textContent !== "none",
    ).toBe(true),
  );
}

describe("CompanyProvider bootstrap — a refused ?company= (403)", () => {
  it("does NOT mint a new tenant, and does not forget the persisted company", async () => {
    // A previous session's company sits in localStorage; the refusal must not
    // destroy that either, because a 403 can be transient (an unclaimed invite).
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, "co-previous");
    openWith(URL_ID);
    const { client, createCompany } = bootClient(
      refuse(403, "not a member of this company"),
    );

    renderBootstrap(client);
    await bootstrapSettled();

    // THE defect: a refused ?company= used to mint "My Company" with this user
    // as Owner and open that instead, so every screen answered 200 against an
    // empty tenant nobody had asked for.
    expect(createCompany).not.toHaveBeenCalled();
    // Nothing opened either — a refusal is not recovered from by opening
    // different books.
    expect(screen.getByTestId("active-company").textContent).toBe("none");
    // And the persisted id was NOT cleared: 403 is "you are not a member (yet)",
    // not "this id is junk".
    expect(globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY)).toBe(
      "co-previous",
    );
  });

  it("says WHY, naming the company that was asked for, and points at F3", async () => {
    openWith(URL_ID);
    const { client } = bootClient(refuse(403, "not a member of this company"));

    renderBootstrap(client);

    const notice = await screen.findByTestId("company-refused-notice");
    // Naming the id is the point: the failure it replaces was invisible because
    // nothing on screen distinguished the requested company from a new empty one.
    expect(notice.textContent).toContain(URL_ID);
    expect(notice.textContent).toMatch(/403/);
    expect(notice.textContent).toMatch(/F3/);
    // A console.warn is not a UI: the notice must be in the accessibility tree.
    expect(notice.getAttribute("role")).toBe("alert");
  });

});

/**
 * ROUND 2 OF THE SAME DEFECT — the localStorage door.
 *
 * The first fix only honoured a 403 when the id had come from `?company=`. A
 * browser re-drive then planted a foreign company id straight into localStorage
 * (which is what any operator has after an earlier visit, and what a second user
 * signing in on the same browser has) and loaded a bare `/`:
 *
 *   planted:  {"tally.companyId":"c25d18d7-…"}   (not a member)
 *   after:    {"tally.companyId":"9309584a-…"}   (a DIFFERENT, brand-new id)
 *   on screen: "My Company / browser-dev-owner / Gateway of Tally", no notice
 *
 * i.e. the identical invisible substitution, through the other door. A 403 says
 * "you are not a member of this company" no matter where the id was found, so
 * these tests pin the refusal to the STATUS, not to the source.
 */
describe("CompanyProvider bootstrap — a refused PERSISTED id (403)", () => {
  const FOREIGN_ID = "c25d18d7-765e-4aa8-a86a-e8d7ca4f02f1";

  it("does NOT mint a new tenant for a foreign id left in localStorage", async () => {
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, FOREIGN_ID);
    openWith(null); // no ?company= — exactly the re-driven case
    const { client, createCompany } = bootClient(
      refuse(403, "not a member of this company"),
    );

    renderBootstrap(client);
    await bootstrapSettled();

    // THE defect: a brand-new company owned by this user, opened silently.
    expect(createCompany).not.toHaveBeenCalled();
    expect(screen.getByTestId("active-company").textContent).toBe("none");
    // And storage must not hold a DIFFERENT id: "planted c25d18d7…, found
    // 9309584a…" is the exact fingerprint the re-drive caught. Forgetting the
    // bad id (null) is fine and is asserted below; substituting another is not.
    const after = globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY) ?? null;
    expect(after === null || after === FOREIGN_ID).toBe(true);
  });

  it("says WHY without claiming the operator asked for it, and names the id", async () => {
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, FOREIGN_ID);
    openWith(null);
    const { client } = bootClient(refuse(403, "not a member of this company"));

    renderBootstrap(client);

    const notice = await screen.findByTestId("company-refused-notice");
    expect(notice.getAttribute("role")).toBe("alert");
    expect(notice.textContent).toContain(FOREIGN_ID);
    expect(notice.textContent).toMatch(/403/);
    expect(notice.textContent).toMatch(/F3/);
    // The wording must match the door it came through: there is no ?company= in
    // this address bar, so telling the operator they "asked for" it is a lie
    // that also hides the reload advice they actually need.
    expect(notice.getAttribute("data-refused-source")).toBe("persisted");
    expect(notice.textContent).toMatch(/earlier session/i);
  });

  it("FORGETS the bad id, so the next load is not wedged on it", async () => {
    // The escape hatch that makes refusing-to-mint safe. A persisted id does go
    // genuinely stale (wiped DB, switched backend), and F3 can only OPEN a
    // company you are already a member of — it cannot create one. So the id is
    // dropped even though nothing is minted on THIS load.
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, FOREIGN_ID);
    openWith(null);
    const { client, createCompany } = bootClient(
      refuse(403, "not a member of this company"),
    );

    const { unmount } = renderBootstrap(client);
    await screen.findByTestId("company-refused-notice");
    expect(createCompany).not.toHaveBeenCalled();
    expect(globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY)).toBeNull();

    // …and a RELOAD (a fresh provider) is then the ordinary "no id at all"
    // case, which mints. A developer whose DB was wiped has a way back.
    unmount();
    cleanup();
    const { client: client2, createCompany: create2 } = bootClient(
      refuse(403, "not a member of this company"),
    );
    renderBootstrap(client2);
    await waitFor(() => expect(create2).toHaveBeenCalledTimes(1));
    expect(screen.queryByTestId("company-refused-notice")).toBeNull();
  });

  it("a 5xx on a PERSISTED id still FAILS CLOSED and keeps the id", async () => {
    // Symmetry with the URL case: a server that is merely broken says nothing
    // about membership, so neither mint nor accuse — and do not throw away an id
    // that is probably fine.
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, FOREIGN_ID);
    openWith(null);
    const getCompany = vi.fn(async () => {
      throw new ApiError(503, "unavailable", "unavailable");
    });
    const { client, createCompany } = bootClient(
      getCompany as unknown as CoredClient["getCompany"],
    );

    renderBootstrap(client);

    await waitFor(() => expect(getCompany).toHaveBeenCalled());
    expect(createCompany).not.toHaveBeenCalled();
    expect(screen.queryByTestId("company-refused-notice")).toBeNull();
    expect(screen.getByTestId("active-company").textContent).toBe("none");
    expect(globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY)).toBe(
      FOREIGN_ID,
    );
  });
});

describe("CompanyProvider bootstrap — dead ids still fall through to minting", () => {
  it("404 (company no longer exists) mints a fresh company", async () => {
    openWith(URL_ID);
    const { client, createCompany } = bootClient(refuse(404, "company not found"));

    renderBootstrap(client);

    await waitFor(() => expect(createCompany).toHaveBeenCalledTimes(1));
    expect(screen.queryByTestId("company-refused-notice")).toBeNull();
  });

  it("400 (a non-uuid id carried over from the in-memory mock) mints a fresh company", async () => {
    // The documented mock path: mock ids are not uuids, so cored 400s on them.
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, "co-1");
    openWith(null);
    const { client, createCompany } = bootClient(refuse(400, "invalid company id"));

    renderBootstrap(client);

    await waitFor(() => expect(createCompany).toHaveBeenCalledTimes(1));
    expect(globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY)).not.toBe("co-1");
  });

  it("a transport/5xx failure still FAILS CLOSED — no company, no mint", async () => {
    openWith(URL_ID);
    const { client, createCompany } = bootClient(
      vi.fn(async () => {
        throw new Error("network down");
      }) as unknown as CoredClient["getCompany"],
    );

    renderBootstrap(client);

    await waitFor(() =>
      expect(screen.getByTestId("active-company").textContent).toBe("none"),
    );
    expect(createCompany).not.toHaveBeenCalled();
    // Not a refusal: the id may be perfectly good, so it is not reported as one.
    expect(screen.queryByTestId("company-refused-notice")).toBeNull();
  });
});

describe("CompanyProvider bootstrap — precedence", () => {
  it("a ?company= id still WINS over the persisted id, and becomes it", async () => {
    // This is how every audit probe and the demo seed open the app; it must not
    // have changed.
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, OTHER_URL_ID);
    openWith(URL_ID);
    const getCompany = vi.fn(async (id: string) => ({
      id,
      name: "Demo Traders",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
      state_code: "29",
      gstin: "",
    })) as unknown as CoredClient["getCompany"];
    const { client, createCompany } = bootClient(getCompany);

    renderBootstrap(client);

    await waitFor(() =>
      expect(screen.getByTestId("active-company").textContent).toBe(URL_ID),
    );
    expect(globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY)).toBe(URL_ID);
    expect(createCompany).not.toHaveBeenCalled();
  });
});

/**
 * ═══════════════════════════════════════════════════════════════════════════
 * §7.3 C — A `?company=` THAT IS NOT UUID-SHAPED USED TO OPEN SOMEBODY ELSE'S
 *          BOOKS, SILENTLY.
 * ═══════════════════════════════════════════════════════════════════════════
 * `readCompanyIdFromURL` returned `null` for BOTH "no ?company= at all" and
 * "?company= present but not a uuid", so the bootstrap could not tell the two
 * apart and went on to read localStorage — opening the previous session's REAL,
 * POPULATED company. Nothing on screen said the id in the address bar had been
 * discarded, so every screen answered 200 about the wrong tenant: the same
 * invisible substitution as the 403 defect above, through a third door.
 *
 * ⚠️ THE ASSERTION THAT MATTERS IS "THE STORED COMPANY WAS NOT OPENED", not
 * "a notice appeared" — a regression must fail on the danger, not on the UI.
 */
describe("CompanyProvider bootstrap — a MALFORMED ?company= (§7.3 C)", () => {
  const GOOD_STORED_ID = "7a3f1c2e-4b5d-4e6f-8a9b-0c1d2e3f4a5b";

  /** A client whose getCompany always succeeds, so opening is the easy path. */
  function hydratingClient() {
    return bootClient(
      vi.fn(async (id: string) => ({
        id,
        name: "Someone Else Traders",
        fy_start_date: "2026-04-01",
        base_currency: "INR",
        state_code: "29",
        gstin: "",
      })) as unknown as CoredClient["getCompany"],
    );
  }

  it("does NOT fall through to the stored company, and mints nothing", async () => {
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, GOOD_STORED_ID);
    globalThis.history.replaceState({}, "", "/?company=demo-co");
    const { client, createCompany } = hydratingClient();

    renderBootstrap(client);
    await bootstrapSettled();

    // THE defect: the stored company opened instead, with no notice.
    expect(screen.getByTestId("active-company").textContent).toBe("none");
    expect(createCompany).not.toHaveBeenCalled();
    // The stored id is not accused of anything, so it is left alone.
    expect(globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY)).toBe(
      GOOD_STORED_ID,
    );
  });

  it("says WHAT was ignored, and that the stored company was not opened instead", async () => {
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, GOOD_STORED_ID);
    globalThis.history.replaceState({}, "", "/?company=demo-co");
    const { client } = hydratingClient();

    renderBootstrap(client);

    const notice = await screen.findByTestId("company-refused-notice");
    expect(notice.getAttribute("role")).toBe("alert");
    expect(notice.getAttribute("data-refused-source")).toBe("url-malformed");
    // Name the thing that was ignored — the operator has to be able to see the
    // typo they made.
    expect(notice.textContent).toContain("demo-co");
    // And state the substitution that did NOT happen; that is the invisible half.
    expect(notice.textContent).toMatch(/not been quietly switched/i);
    // It never claims the server said anything: nothing was sent.
    expect(notice.textContent).not.toMatch(/403/);
  });

  it("is NOT sent to the server — there is no id to judge", async () => {
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, GOOD_STORED_ID);
    globalThis.history.replaceState({}, "", "/?company=demo-co");
    const getCompany = vi.fn(async () => {
      throw new Error("must not be called");
    });
    const { client } = bootClient(
      getCompany as unknown as CoredClient["getCompany"],
    );

    renderBootstrap(client);
    await screen.findByTestId("company-refused-notice");

    expect(getCompany).not.toHaveBeenCalled();
  });

  it("an EMPTY ?company= is still 'no preference' and falls through", async () => {
    // Deliberately NOT malformed: it names nothing, and `App.tsx`'s
    // readCompanyIdInURL — the other reader of this parameter, which gates D25's
    // invite adoption — treats an empty value as no preference too. Two readers
    // disagreeing about one URL is how this class of defect starts.
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, GOOD_STORED_ID);
    globalThis.history.replaceState({}, "", "/?company=");
    const { client, createCompany } = hydratingClient();

    renderBootstrap(client);

    await waitFor(() =>
      expect(screen.getByTestId("active-company").textContent).toBe(
        GOOD_STORED_ID,
      ),
    );
    expect(createCompany).not.toHaveBeenCalled();
    expect(screen.queryByTestId("company-refused-notice")).toBeNull();
  });

  it("the notice's action drops the parameter and THEN opens the stored company", async () => {
    // The refusal is not a wedge: dropping `?company=` is a deliberate operator
    // act on a notice that has already said what was ignored, so reaching the
    // stored company afterwards is a choice rather than a substitution.
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, GOOD_STORED_ID);
    globalThis.history.replaceState({}, "", "/?company=demo-co&x=keep");
    const { client, createCompany } = hydratingClient();

    renderBootstrap(client);
    await screen.findByTestId("company-refused-notice");

    fireEvent.click(screen.getByTestId("company-refused-forget"));

    await waitFor(() =>
      expect(screen.getByTestId("active-company").textContent).toBe(
        GOOD_STORED_ID,
      ),
    );
    // No reload was needed, and only the company parameter was removed.
    expect(globalThis.location.search).toBe("?x=keep");
    expect(createCompany).not.toHaveBeenCalled();
    expect(screen.queryByTestId("company-refused-notice")).toBeNull();
  });
});

/**
 * ═══════════════════════════════════════════════════════════════════════════
 * §7.3 D — A WELL-FORMED BUT NONEXISTENT `?company=` UUID WEDGED FOR EVER.
 * ═══════════════════════════════════════════════════════════════════════════
 * The documented recovery valve for a bad id is "400 or 404 → drop it and mint".
 * ⛔ AGAINST CORED THE 404 ARM IS DEAD CODE: the auth middleware checks
 * MEMBERSHIP before any handler can look for the row, so a uuid naming a company
 * that never existed answers **403 "not a member of this company"** — the same
 * status as someone else's real books, on purpose, so a stranger cannot
 * enumerate tenants. A URL id is (correctly) not forgotten, so the refusal
 * repeats on every reload and nothing the operator can do from the keyboard
 * changes the answer.
 *
 * These tests pin BOTH halves: the refusal keeps repeating (it must — minting
 * over it is the original defect), AND there is now an exit that ends it.
 */
describe("CompanyProvider bootstrap — a nonexistent ?company= uuid (§7.3 D)", () => {
  const DEAD_ID = "deadbeef-0000-4000-8000-000000000000";

  it("does not claim the company EXISTS — a 403 cannot tell the two apart", async () => {
    globalThis.history.replaceState({}, "", `/?company=${DEAD_ID}`);
    const { client } = bootClient(refuse(403, "not a member of this company"));

    renderBootstrap(client);

    const notice = await screen.findByTestId("company-refused-notice");
    expect(notice.getAttribute("data-refused-source")).toBe("url");
    expect(notice.textContent).toContain(DEAD_ID);
    // The old wording asserted membership was the reason, so "claim your invite
    // and reload" read as the whole story. For a dead id that advice can never
    // come true, and the operator waits for an invite that will never arrive.
    expect(notice.textContent).toMatch(/no such company exists/i);
    // And it must say that reloading cannot help, because that is what an
    // operator tries first.
    expect(notice.textContent).toMatch(/reload will NOT change this answer/i);
  });

  it("STILL refuses on a reload — the 400/404 valve genuinely never fires", async () => {
    // Not a regression to fix by minting: this is the refusal working. It is
    // pinned so that the exit below is understood as the answer, and so nobody
    // "solves" the wedge by reintroducing the silent substitution.
    globalThis.history.replaceState({}, "", `/?company=${DEAD_ID}`);
    const first = bootClient(refuse(403, "not a member of this company"));
    const { unmount } = renderBootstrap(first.client);
    await screen.findByTestId("company-refused-notice");
    expect(first.createCompany).not.toHaveBeenCalled();

    unmount();
    cleanup();

    const second = bootClient(refuse(403, "not a member of this company"));
    renderBootstrap(second.client);
    await screen.findByTestId("company-refused-notice");
    expect(second.createCompany).not.toHaveBeenCalled();
    expect(screen.getByTestId("active-company").textContent).toBe("none");
  });

  it("the notice's action ENDS the wedge, without a reload", async () => {
    globalThis.history.replaceState({}, "", `/?company=${DEAD_ID}`);
    const { client, createCompany } = bootClient(
      refuse(403, "not a member of this company"),
    );

    renderBootstrap(client);
    await screen.findByTestId("company-refused-notice");
    expect(createCompany).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId("company-refused-forget"));

    // Nothing is persisted and the parameter is gone, so this is the ordinary
    // "no id at all" case — step 4, the only minting path.
    await waitFor(() => expect(createCompany).toHaveBeenCalledTimes(1));
    expect(globalThis.location.search).toBe("");
    expect(screen.queryByTestId("company-refused-notice")).toBeNull();
  });

  it("the PERSISTED door's action mints too, and its own id is already gone", async () => {
    // Symmetry: the persisted 403 clears the id on the way in, so its exit is
    // "carry on" rather than "drop a parameter" — and it must not need a reload
    // either, which is what the wording used to demand.
    globalThis.localStorage?.setItem(COMPANY_STORAGE_KEY, DEAD_ID);
    globalThis.history.replaceState({}, "", "/");
    const { client, createCompany } = bootClient(
      refuse(403, "not a member of this company"),
    );

    renderBootstrap(client);
    const notice = await screen.findByTestId("company-refused-notice");
    expect(notice.getAttribute("data-refused-source")).toBe("persisted");
    expect(globalThis.localStorage?.getItem(COMPANY_STORAGE_KEY)).toBeNull();

    fireEvent.click(screen.getByTestId("company-refused-forget"));

    await waitFor(() => expect(createCompany).toHaveBeenCalledTimes(1));
    expect(screen.queryByTestId("company-refused-notice")).toBeNull();
  });

  it("the action does not steal focus, so the shell still sees F3", async () => {
    // The notice's own advice names F3, and the shell captures keys at the
    // document. An autofocused button would swallow them.
    globalThis.history.replaceState({}, "", `/?company=${DEAD_ID}`);
    const { client } = bootClient(refuse(403, "not a member of this company"));

    renderBootstrap(client);
    await screen.findByTestId("company-refused-notice");

    expect(document.activeElement).toBe(document.body);
  });
});
