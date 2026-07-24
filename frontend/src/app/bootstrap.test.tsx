import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { App } from "../App";
import { createMockClient } from "../api";
import type { CoredClient } from "../api";

afterEach(cleanup);

beforeEach(() => {
  try {
    globalThis.localStorage?.clear();
  } catch {
    // jsdom always provides localStorage; ignore if absent.
  }
});

function spyClient(): {
  client: CoredClient;
  spies: Record<string, ReturnType<typeof vi.fn>>;
} {
  const base = createMockClient({ seed: false });
  const spies: Record<string, ReturnType<typeof vi.fn>> = {};
  const client = {} as Record<string, unknown>;
  for (const key of Object.keys(base) as (keyof CoredClient)[]) {
    const orig = base[key] as unknown as (...a: unknown[]) => unknown;
    const fn = vi.fn((...args: unknown[]) => orig.apply(base, args));
    spies[key] = fn;
    client[key] = fn;
  }
  return { client: client as unknown as CoredClient, spies };
}

describe("App company bootstrap", () => {
  it("mints a company once on first mount when no company prop is given", async () => {
    const { client, spies } = spyClient();
    render(<App client={client} />);
    await waitFor(() => expect(spies.createCompany).toHaveBeenCalledTimes(1));
    const [req] = spies.createCompany!.mock.calls[0]!;
    expect((req as { fy_start_date: string }).fy_start_date).toMatch(
      /-04-01$/,
    );
    expect((req as { base_currency: string }).base_currency).toBe("INR");
    // WS8a: the bootstrap MUST persist a home GST state on the company master
    // (company.state_code), because that column is what cored uses as the
    // default SupplierStateCode when deriving a voucher's GST split. Without it
    // the company would be created stateless and every intra-state sale would
    // derive as inter-state (IGST).
    const stateCode = (req as { state_code?: string }).state_code;
    expect(stateCode).toBeTruthy();
    expect(stateCode).toMatch(/^\d{2}$/);
    // The gateway renders regardless of bootstrap state.
    expect(screen.getByText("Gateway of Tally")).toBeTruthy();
  });

  it("persists the created company id and hydrates it (not a constant) on reload", async () => {
    // First mount: mint a company whose home state is NOT the "29" default, so a
    // regression to a hardcoded constant is detectable.
    const { client } = spyClient();
    await client.createCompany({
      id: "co-mh",
      name: "Mumbai Traders",
      fy_start_date: "2026-04-01",
      base_currency: "INR",
      state_code: "27",
    });
    globalThis.localStorage?.setItem("tally.companyId", "co-mh");

    render(<App client={client} />);
    // The reload path reads the company master back: the NAME shown comes from
    // the server, not from the "My Company" bootstrap placeholder.
    await waitFor(() => expect(screen.getByText("Mumbai Traders")).toBeTruthy());
  });

  it("does not wedge on a stale persisted id: a 404 mints a fresh company", async () => {
    const { client, spies } = spyClient();
    globalThis.localStorage?.setItem("tally.companyId", "co-does-not-exist");
    render(<App client={client} />);
    await waitFor(() => expect(spies.getCompany).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(spies.createCompany).toHaveBeenCalledTimes(1));
    // The dead id was discarded in favour of the newly minted one.
    await waitFor(() =>
      expect(globalThis.localStorage?.getItem("tally.companyId")).not.toBe(
        "co-does-not-exist",
      ),
    );
  });

  it("fails CLOSED (no company selected) when hydration fails for a non-404 reason", async () => {
    const { client } = spyClient();
    // Transport/permission failure — NOT a 404. Guessing a home state here would
    // make the client derive a split the server rejects with a 422, so the shell
    // must leave the company unselected rather than proceed on an assumption.
    (client as unknown as Record<string, unknown>).getCompany = vi.fn(async () => {
      throw new Error("network down");
    });
    const createSpy = vi.fn();
    (client as unknown as Record<string, unknown>).createCompany = createSpy;
    globalThis.localStorage?.setItem("tally.companyId", "co-1");

    render(<App client={client} />);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });
    await waitFor(() => expect(screen.getByText("Preparing company…")).toBeTruthy());
    // It must NOT silently mint a second company over a transient failure.
    expect(createSpy).not.toHaveBeenCalled();
  });

  it("does NOT mint a company when an explicit company prop is given", async () => {
    const { client, spies } = spyClient();
    render(<App client={client} company={{ id: "co-x", name: "Explicit Co" }} />);
    await waitFor(() => expect(screen.getByText("Explicit Co")).toBeTruthy());
    expect(spies.createCompany).not.toHaveBeenCalled();
  });
});
