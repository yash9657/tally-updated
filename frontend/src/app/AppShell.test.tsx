import { describe, it, expect, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { AppShell } from "./AppShell";
import { CompanyProvider } from "./CompanyContext";
import { createDefaultRegistry } from "./defaultScreens";
import { createMockClient } from "../api";
import type { ScreenComponentProps } from "./screenRegistry";

afterEach(cleanup);

function renderShell(registry = createDefaultRegistry()) {
  return render(
    <CompanyProvider
      client={createMockClient()}
      initialCompany={{ id: "company-demo", name: "Demo Traders" }}
    >
      <AppShell registry={registry} />
    </CompanyProvider>,
  );
}

describe("AppShell routing + chrome", () => {
  it("renders the company in the top bar and the gateway body", () => {
    renderShell();
    expect(screen.getByText("Demo Traders")).toBeTruthy();
    expect(screen.getByText("Gateway of Tally")).toBeTruthy();
  });

  it("routes F-keys to the voucher screen and Alt+G to the Go To overlay", () => {
    renderShell();
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "F9" });
    expect(screen.getByText(/Purchase Voucher/)).toBeTruthy();
    fireEvent.keyDown(app, { key: "g", altKey: true });
    expect(screen.getByRole("navigation", { name: "Button bar" })).toBeTruthy();
  });

  it("renders the bottom shortcut hints", () => {
    renderShell();
    expect(screen.getByLabelText("Shortcut hints")).toBeTruthy();
  });

  it("lets an agent register a custom report screen by name", () => {
    const CustomDayBook = ({ snapshot }: ScreenComponentProps) => (
      <div>CUSTOM {snapshot.reportName}</div>
    );
    const registry = createDefaultRegistry().register(
      "report:Day Book",
      CustomDayBook,
    );
    renderShell(registry);
    const app = screen.getByRole("application");
    fireEvent.keyDown(app, { key: "g", altKey: true });
    // The Go To overlay is open; the SelectionList is autofocused. Pick Day Book.
    fireEvent.mouseDown(screen.getByText("Day Book"));
    fireEvent.keyDown(app, { key: "Enter" });
    expect(screen.getByText("CUSTOM Day Book")).toBeTruthy();
  });
});
