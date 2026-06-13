import { describe, it, expect, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { App } from "./App";

afterEach(cleanup);

/**
 * Wiring test: the shell must translate REAL DOM key events into navigation
 * machine transitions and re-render the active screen accordingly.
 */
describe("App key-event wiring", () => {
  it("renders the Gateway by default", () => {
    render(<App />);
    expect(screen.getByText("Gateway of Tally")).toBeTruthy();
  });

  it("F8 from the Gateway drills into the Sales voucher", () => {
    render(<App />);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });
    expect(screen.getByText(/Sales Voucher/)).toBeTruthy();
  });

  it("Esc from a voucher returns to the Gateway", () => {
    render(<App />);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F8" });
    expect(screen.getByText(/Sales Voucher/)).toBeTruthy();
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
