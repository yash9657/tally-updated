import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
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
