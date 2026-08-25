import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";

import { AppShell } from "../app/AppShell";
import { CompanyProvider } from "../app/CompanyContext";
import { createDefaultRegistry } from "../app/defaultScreens";
import { createMockClient } from "../api";
import type { CoredClient } from "../api";

afterEach(cleanup);

function renderShell(client: CoredClient = createMockClient()) {
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "company-demo", name: "Demo Traders" }}
    >
      <AppShell registry={createDefaultRegistry()} />
    </CompanyProvider>,
  );
}

/**
 * macOS COMPOSES Option+Y INTO "¥".
 *
 * ⛔ THIS IS NOT A DETAIL. Every Alt binding in this product was once found dead
 * on a Mac while every browser test passed, because the tests pressed the key
 * the way a Linux keyboard reports it and the resolver reads `code`, not `key`.
 * A test that fires { key: "y", altKey: true } proves the binding works on the
 * machines nobody develops on. This one fires the composed character with the
 * physical `code`, which is what a Mac actually sends — so if the resolver ever
 * goes back to reading `key`, this goes red instead of shipping a dead key.
 */
function altMacY(target: Element): void {
  fireEvent.keyDown(target, { key: "¥", code: "KeyY", altKey: true });
}

describe("Alt+Y opens the DATA screen", () => {
  it("is ABSENT before the key — the control that makes the next test mean something", () => {
    renderShell();
    expect(screen.queryByTestId("data-screen")).toBeNull();
  });

  it("opens on the macOS-composed Option+Y, not only on a Linux Alt+y", () => {
    renderShell();
    altMacY(screen.getByRole("application"));
    expect(screen.getByTestId("data-screen")).toBeTruthy();
  });

  it("titles the frame Data, not Backup", () => {
    renderShell();
    altMacY(screen.getByRole("application"));
    // The screen is Tally's DATA menu: backup is one thing on it, restore
    // another, and the third is a stated refusal. A title naming only the first
    // would send an operator looking for the other two elsewhere.
    expect(screen.getAllByText("Data").length).toBeGreaterThan(0);
  });

  it("does not stack a second copy when Alt+Y is pressed twice", () => {
    renderShell();
    const app = screen.getByRole("application");
    altMacY(app);
    altMacY(app);
    expect(screen.getAllByTestId("data-screen")).toHaveLength(1);
  });
});

describe("the Data screen says the consequences BEFORE the operator acts", () => {
  it("warns that a restore CREATES a company and never replaces one", () => {
    renderShell();
    altMacY(screen.getByRole("application"));
    const warning =
      screen.getByTestId("data-restore-warning").textContent ?? "";
    // Learning this from a 409 AFTER choosing a file is learning it too late.
    expect(warning).toMatch(/creates a new company/i);
    expect(warning).toMatch(/never replaces/i);
  });

  it("warns that access rights are NOT re-granted from the archive", () => {
    renderShell();
    altMacY(screen.getByRole("application"));
    const warning =
      screen.getByTestId("data-restore-memberships-warning").textContent ?? "";
    expect(warning).toMatch(/not restored/i);
    expect(warning).toMatch(/owner/i);
  });

  it("says the archive is as sensitive as the books", () => {
    renderShell();
    altMacY(screen.getByRole("application"));
    expect(screen.getByTestId("data-screen").textContent ?? "").toMatch(
      /every party.{0,40}every salary/i,
    );
  });

  it("⛔ states that Split Company Data is absent ON PURPOSE, with the reason", () => {
    renderShell();
    altMacY(screen.getByRole("application"));
    const split = screen.getByTestId("data-split-absent").textContent ?? "";
    // "Not built" alone would read as a gap. The claim being made is that the
    // limit split works around is Tally's and not this product's, and a screen
    // that does not say so leaves the operator to assume the feature is late.
    expect(split).toMatch(/deliberately/i);
    expect(split).toMatch(/memory|RAM/i);
    expect(split).not.toMatch(/coming soon/i);
  });
});

describe("the Data screen renders the SERVER's sentence and never its own", () => {
  it("shows the refusal verbatim when a restore is refused", async () => {
    const client = createMockClient();
    const refusal =
      'backup: that company already exists in this database: "Demo Traders" is ' +
      "already here. This archive preserves every primary key";
    client.restoreCompany = vi.fn().mockRejectedValue(
      Object.assign(new Error(refusal), { status: 409 }),
    );
    renderShell(client);
    altMacY(screen.getByRole("application"));

    const input = screen.getByTestId("data-restore-file") as HTMLInputElement;
    fireEvent.change(input, {
      target: { files: [new File(["x"], "a.gz", { type: "application/gzip" })] },
    });

    await waitFor(() => {
      expect(screen.getByTestId("data-error").textContent).toBe(refusal);
    });
  });

  it("lists the memberships a successful restore did NOT re-grant, by name", async () => {
    const client = createMockClient();
    client.restoreCompany = vi.fn().mockResolvedValue({
      companyID: "company-restored",
      companyName: "Archived Traders",
      rowCount: 528,
      rows: {},
      skippedTables: ["company_membership"],
      skippedRows: 3,
      membershipsNotRestored: ["asha@example.com", "ravi@example.com"],
      sourceSchemaVersion: "0079_x",
      schemaVersion: "0079_x",
    });
    renderShell(client);
    altMacY(screen.getByRole("application"));

    fireEvent.change(screen.getByTestId("data-restore-file"), {
      target: { files: [new File(["x"], "a.gz", { type: "application/gzip" })] },
    });

    await waitFor(() => {
      const names = screen.getByTestId("data-restore-memberships").textContent ?? "";
      // A COUNT would be skimmed past. The people who lost access have to be
      // named, because somebody has to re-grant each of them deliberately.
      expect(names).toContain("asha@example.com");
      expect(names).toContain("ravi@example.com");
    });
  });

  it("downloads the archive under the SERVER's filename", async () => {
    const client = createMockClient();
    client.backupCompany = vi.fn().mockResolvedValue({
      blob: new Blob(["archive"], { type: "application/gzip" }),
      filename: "demo-traders-2026-08-25.tallybak.jsonl.gz",
    });
    // jsdom implements neither; the screen only needs them not to throw.
    const createURL = vi.fn().mockReturnValue("blob:x");
    vi.stubGlobal("URL", { ...URL, createObjectURL: createURL, revokeObjectURL: vi.fn() });

    renderShell(client);
    altMacY(screen.getByRole("application"));
    fireEvent.click(screen.getByTestId("data-backup-button"));

    await waitFor(() => {
      // The SERVER names the file: it is the side that knows the company's name
      // and the date it took the archive.
      expect(screen.getByTestId("data-backup-saved").textContent).toContain(
        "demo-traders-2026-08-25.tallybak.jsonl.gz",
      );
    });
    expect(client.backupCompany).toHaveBeenCalledWith("company-demo");
    vi.unstubAllGlobals();
  });
});
