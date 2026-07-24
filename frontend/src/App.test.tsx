import { describe, it, expect, afterEach, beforeEach } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { App } from "./App";

afterEach(cleanup);

// Each case renders a FRESH default (in-memory mock) client, so a company id
// persisted by a previous case in this file names a company the new client has
// never heard of. Clearing the key keeps every case on the same "no company yet"
// starting line (bootstrap.test.tsx does the same). Without this, case 2 onwards
// take the persisted-company path and hydrate against a client that 404s.
beforeEach(() => {
  try {
    globalThis.localStorage?.clear();
  } catch {
    // jsdom always provides localStorage; ignore if absent.
  }
});

/**
 * Wiring test: the shell must translate REAL DOM key events into navigation
 * machine transitions and re-render the active screen accordingly.
 */
describe("App key-event wiring", () => {
  it("renders the Gateway by default", () => {
    render(<App />);
    expect(screen.getByText("Gateway of Tally")).toBeTruthy();
  });

  // The company bootstrap is asynchronous in EVERY path now (a brand-new company
  // is minted with createCompany; a persisted one is hydrated with getCompany so
  // its real home state code is known before any GST post can be built). Until it
  // resolves, company-scoped screens render the "Preparing company…" placeholder,
  // so these cases await the screen instead of asserting synchronously.
  it("F8 from the Gateway drills into the Sales voucher", async () => {
    render(<App />);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });
    await waitFor(() => expect(screen.getByText(/Sales Voucher/)).toBeTruthy());
  });

  it("Esc from a voucher returns to the Gateway", async () => {
    render(<App />);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });
    await waitFor(() => expect(screen.getByText(/Sales Voucher/)).toBeTruthy());
    fireEvent.keyDown(app, { key: "Escape" });
    expect(screen.getByText("Gateway of Tally")).toBeTruthy();
  });

  it("renders the fixed button bar with the voucher F-key labels", () => {
    render(<App />);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });
    const bar = screen.getByRole("navigation", { name: "Button bar" });
    expect(bar.textContent).toContain("F8");
    expect(bar.textContent).toContain("Purchase");
    expect(bar.textContent).toContain("Ctrl+A");
  });
});
