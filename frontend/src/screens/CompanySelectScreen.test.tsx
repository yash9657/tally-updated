import { describe, it, expect, afterEach, vi } from "vitest";
import { useEffect } from "react";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { CompanySelectScreen } from "./CompanySelectScreen";
import { CompanyProvider, useCompanyContext } from "../app/CompanyContext";
import { createMockClient, ApiError } from "../api";
import type { CoredClient, MeResponse } from "../api";
import type { CompanyInfo } from "../app/CompanyContext";
import { COMPANY_SWITCHED_SENTINEL } from "../keystroke/navMachine";

afterEach(cleanup);

/**
 * The F3 company picker. Two properties matter more than the rendering:
 *
 *  1. the list comes from the SERVER's membership answer (`GET /v1/me`) — not
 *     from anything the client could guess; and
 *  2. a switch that cannot hydrate the target company's master must NOT switch.
 *     homeStateCode drives the in-browser intra/inter-state GST split, so a
 *     half-hydrated switch would build CGST+SGST postings while the server
 *     derived IGST and rejected the post.
 */

const ME: MeResponse = {
  user_id: "user-1",
  subject: "browser-dev-owner",
  companies: [
    {
      company_id: "co-1",
      name: "Demo Traders",
      security_level_id: "lvl-owner",
      security_level_name: "Owner",
      is_owner: true,
    },
    {
      company_id: "co-2",
      name: "Sharma Distributors",
      security_level_id: "lvl-de",
      security_level_name: "Data Entry",
      is_owner: false,
    },
  ],
  claimed_invites: [],
};

/** Captures the live CompanyInfo so a switch is observable. */
function CaptureCompany({ onChange }: { onChange: (c: CompanyInfo | null) => void }) {
  const { company } = useCompanyContext();
  useEffect(() => {
    onChange(company);
  }, [company, onChange]);
  return null;
}

function renderPicker(
  client: CoredClient,
  onSelect: (item: string) => void = () => {},
  onCompanyChange: (c: CompanyInfo | null) => void = () => {},
) {
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Demo Traders", homeStateCode: "29" }}
    >
      <CaptureCompany onChange={onCompanyChange} />
      <CompanySelectScreen
        snapshot={{
          screen: "company-select",
          screenStackDepth: 2,
          canReturnToVoucher: false,
        }}
        onSelect={onSelect}
      />
    </CompanyProvider>,
  );
}

/** A client whose /v1/me is fixed and whose getCompany is a spy. */
function clientWithMe(
  getCompany: CoredClient["getCompany"],
  me: MeResponse = ME,
): CoredClient {
  const base = createMockClient({ seed: false });
  return { ...base, getMe: async () => me, getCompany };
}

/**
 * Pick a row the way a keyboard user does: type enough of the label into the
 * as-you-type combobox to isolate it, then press Enter. (SelectionList selects
 * on Enter or mouseDown; a plain click does nothing, which is itself the
 * behaviour a real Tally user relies on.)
 */
function pick(filter: string): void {
  const combobox = screen.getByRole("combobox");
  fireEvent.change(combobox, { target: { value: filter } });
  fireEvent.keyDown(combobox, { key: "Enter" });
}

describe("CompanySelectScreen (F3)", () => {
  it("lists the companies /v1/me returns, with the level held in each", async () => {
    const getMe = vi.fn(async () => ME);
    const base = createMockClient({ seed: false });
    renderPicker({ ...base, getMe });

    // ⚠️ Wait on the RENDERED ROW, not on the spy. `getMe` records its call the
    // moment it is invoked, so waiting on the spy can return a tick before the
    // resolved membership list has been committed to the DOM — this test flaked
    // exactly that way under full-suite worker contention while passing 8/8 in
    // isolation. The observable the assertions depend on is the only sound wait.
    await waitFor(() => expect(screen.getByText(/Demo Traders · Owner/)).toBeTruthy());
    expect(getMe).toHaveBeenCalled();
    expect(screen.getByText(/Sharma Distributors · Data Entry/)).toBeTruthy();
    // The company already open is marked, so switching to it is obviously a no-op.
    expect(screen.getByText(/Demo Traders · Owner · \(current\)/)).toBeTruthy();
  });

  it("switching re-scopes the session from the SERVER's company master", async () => {
    const getCompany = vi.fn(async (id: string) => ({
      id,
      name: "Sharma Distributors",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
      // The whole point: company B is Maharashtra, not company A's Karnataka.
      state_code: "27",
      gstin: "",
    })) as unknown as CoredClient["getCompany"];
    const onSelect = vi.fn();
    const seen: { current: CompanyInfo | null } = { current: null };
    renderPicker(clientWithMe(getCompany), onSelect, (c) => {
      seen.current = c;
    });

    await waitFor(() =>
      expect(screen.getByText(/Sharma Distributors/)).toBeTruthy(),
    );
    pick("Sharma");

    await waitFor(() =>
      expect(onSelect).toHaveBeenCalledWith(COMPANY_SWITCHED_SENTINEL),
    );
    expect(seen.current).toEqual({
      id: "co-2",
      name: "Sharma Distributors",
      homeStateCode: "27",
    });
  });

  it("does NOT switch when the target company cannot be hydrated", async () => {
    const getCompany = vi.fn(async () => {
      throw new ApiError(403, "not a member of this company", "forbidden");
    }) as unknown as CoredClient["getCompany"];
    const onSelect = vi.fn();
    const seen: { current: CompanyInfo | null } = { current: null };
    renderPicker(clientWithMe(getCompany), onSelect, (c) => {
      seen.current = c;
    });

    await waitFor(() =>
      expect(screen.getByText(/Sharma Distributors/)).toBeTruthy(),
    );
    pick("Sharma");

    await waitFor(() =>
      expect(screen.getByTestId("company-select-error").textContent).toContain(
        "Could not open Sharma Distributors",
      ),
    );
    // The session stayed on company A — a half-hydrated switch would derive the
    // wrong GST split in the browser.
    expect(seen.current?.id).toBe("co-1");
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("re-selecting the CURRENT company just leaves the picker", async () => {
    const getCompany = vi.fn() as unknown as CoredClient["getCompany"];
    const onSelect = vi.fn();
    renderPicker(clientWithMe(getCompany), onSelect);

    await waitFor(() => expect(screen.getByText(/Demo Traders/)).toBeTruthy());
    pick("Demo");

    await waitFor(() =>
      expect(onSelect).toHaveBeenCalledWith(COMPANY_SWITCHED_SENTINEL),
    );
    expect(getCompany).not.toHaveBeenCalled();
  });

  it("says so when the user is a member of nothing", async () => {
    const base = createMockClient({ seed: false });
    renderPicker({
      ...base,
      getMe: async () => ({ ...ME, companies: [] }),
    });
    await waitFor(() =>
      expect(screen.getByTestId("company-select-empty").textContent).toContain(
        "not a member of any company",
      ),
    );
  });

  it("surfaces a failed /v1/me instead of rendering an empty list", async () => {
    const base = createMockClient({ seed: false });
    renderPicker({
      ...base,
      getMe: async () => {
        throw new ApiError(401, "missing bearer token", "unauthorized");
      },
    });
    await waitFor(() =>
      expect(screen.getByTestId("company-select-error").textContent).toContain(
        "missing bearer token",
      ),
    );
  });
});
