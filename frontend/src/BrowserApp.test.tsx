import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BrowserApp } from "./BrowserApp";

// Exercise routing independently of the ledger transport. The real auth and
// session lifecycle are covered by auth/auth.test.tsx and App.test.tsx.
vi.mock("./App", async () => {
  const { useEffect, useState } = await import("react");
  return { App: ({ onSessionChange }: { onSessionChange: (signedIn: boolean) => void }) => {
    const [signedIn, setSignedIn] = useState(false);
    useEffect(() => onSessionChange(signedIn), [signedIn, onSessionChange]);
    return signedIn ? <div><h1>Accounting workspace</h1><button onClick={() => setSignedIn(false)}>Sign out</button></div>
      : <div><h1>Workspace sign in</h1><button onClick={() => setSignedIn(true)}>Complete sign in</button></div>;
  } };
});

beforeEach(() => window.history.replaceState(null, "", "/"));
afterEach(cleanup);

describe("public and workspace routes", () => {
  it("serves the public landing page and points workspace links to sign-in", () => {
    render(<BrowserApp />);
    expect(screen.getByRole("heading", { level: 1 }).textContent).toContain("Familiar keys.");
    expect(screen.getByRole("link", { name: "Open your workspace" }).getAttribute("href")).toBe("/signin");
    expect(screen.queryByText("Workspace sign in")).toBeNull();
  });

  it("redirects unauthenticated app access and retains the selected company through sign-in and sign-out", async () => {
    window.history.replaceState(null, "", "/app?company=company-a");
    render(<BrowserApp />);
    await screen.findByRole("heading", { name: "Workspace sign in" });
    await waitFor(() => expect(window.location.pathname).toBe("/signin"));
    expect(window.location.search).toBe("?company=company-a");
    fireEvent.click(screen.getByRole("button", { name: "Complete sign in" }));
    await waitFor(() => expect(window.location.pathname).toBe("/app"));
    expect(screen.getByRole("heading", { name: "Accounting workspace" })).toBeTruthy();
    expect(window.location.search).toBe("?company=company-a");
    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(window.location.pathname).toBe("/signin"));
  });

  it("normalizes the separate sign-in route with a trailing slash", async () => {
    window.history.replaceState(null, "", "/signin/");
    render(<BrowserApp />);
    await screen.findByRole("heading", { name: "Workspace sign in" });
    fireEvent.click(screen.getByRole("button", { name: "Complete sign in" }));
    await waitFor(() => expect(window.location.pathname).toBe("/app"));
  });

  it("preserves old root company links", async () => {
    window.history.replaceState(null, "", "/?company=legacy-company");
    render(<BrowserApp />);
    await screen.findByRole("heading", { name: "Workspace sign in" });
    await waitFor(() => expect(window.location.pathname).toBe("/signin"));
    expect(window.location.search).toBe("?company=legacy-company");
  });

  it("rechecks the session after browser history navigation", async () => {
    window.history.replaceState(null, "", "/signin");
    render(<BrowserApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Complete sign in" }));
    await waitFor(() => expect(window.location.pathname).toBe("/app"));
    act(() => {
      window.history.pushState(null, "", "/signin");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await waitFor(() => expect(window.location.pathname).toBe("/app"));
    expect(screen.getByRole("heading", { name: "Accounting workspace" })).toBeTruthy();
  });

  it("makes the illustrative preview operable by keyboard", () => {
    render(<BrowserApp />);
    fireEvent.keyDown(screen.getByRole("tab", { name: "Day Book" }), { key: "ArrowRight" });
    expect(screen.getByRole("tab", { name: "Stock Summary" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.getByText("₹91,200.00")).toBeTruthy();
    fireEvent.keyDown(screen.getByRole("tab", { name: "Stock Summary" }), { key: "End" });
    expect(screen.getByText("A payment needs a follow-up.")).toBeTruthy();
    fireEvent.keyDown(screen.getByRole("tab", { name: "Insights" }), { key: "Home" });
    expect(screen.getByRole("tab", { name: "Day Book" }).getAttribute("aria-selected")).toBe("true");
  });

  it("provides a way home from an unknown route", () => {
    window.history.replaceState(null, "", "/missing");
    render(<BrowserApp />);
    expect(screen.getByRole("link", { name: "Back to home" }).getAttribute("href")).toBe("/");
  });
});
