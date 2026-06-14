import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
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
    // The gateway renders regardless of bootstrap state.
    expect(screen.getByText("Gateway of Tally")).toBeTruthy();
  });

  it("does NOT mint a company when an explicit company prop is given", async () => {
    const { client, spies } = spyClient();
    render(<App client={client} company={{ id: "co-x", name: "Explicit Co" }} />);
    await waitFor(() => expect(screen.getByText("Explicit Co")).toBeTruthy());
    expect(spies.createCompany).not.toHaveBeenCalled();
  });
});
