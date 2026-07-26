import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { MembersScreen } from "./MembersScreen";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient, ApiError } from "../api";
import type { CoredClient } from "../api";

afterEach(cleanup);

/**
 * Users & Roles. The behaviours worth pinning are the ones that would otherwise
 * mislead an administrator:
 *
 *  - an INVITE is not a membership (it is listed separately, and stays pending
 *    until the invitee signs in and claims it);
 *  - a 403 is the product working, not a broken page, so it must be EXPLAINED
 *    rather than rendered as an empty table;
 *  - the level offered by default is the least-privileged one, never Owner.
 */

const SNAPSHOT = {
  screen: "members" as const,
  screenStackDepth: 2,
  canReturnToVoucher: false,
};

function renderMembers(client: CoredClient) {
  return render(
    <CompanyProvider
      client={client}
      initialCompany={{ id: "co-1", name: "Demo Traders", homeStateCode: "29" }}
    >
      <MembersScreen snapshot={SNAPSHOT} onSelect={() => {}} />
    </CompanyProvider>,
  );
}

describe("MembersScreen (Users & Roles)", () => {
  it("lists members and security levels for the active company", async () => {
    const client = createMockClient({ seed: false });
    renderMembers(client);

    await waitFor(() =>
      expect(screen.getByTestId("members-table").textContent).toContain(
        "browser-dev-owner",
      ),
    );
    expect(screen.getByTestId("members-table").textContent).toContain("Owner");
    // The seeded levels, with what each may do — Data Entry is default-deny.
    const levels = screen.getByTestId("levels-list").textContent ?? "";
    expect(levels).toContain("Data Entry");
    expect(levels).toContain("allow create sales");
    expect(levels).toContain("WebAuthn for writes"); // the Owner level
    // Nobody has been invited yet, and that is stated rather than left blank.
    expect(screen.getByTestId("invites-empty").textContent).toContain(
      "until the invitee signs in",
    );
  });

  it("inviting adds a PENDING invite, not a member", async () => {
    const client = createMockClient({ seed: false });
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("invite-submit")).toBeTruthy());

    // The default level is the least-privileged one, never Owner.
    const levelSelect = screen.getByTestId("invite-level") as HTMLSelectElement;
    expect(levelSelect.value).toBe("Data Entry");

    fireEvent.change(screen.getByTestId("invite-subject"), {
      target: { value: "colleague@firm" },
    });
    fireEvent.click(screen.getByTestId("invite-submit"));

    await waitFor(() =>
      expect(screen.getByTestId("invites-list").textContent).toContain(
        "colleague@firm · Data Entry · pending",
      ),
    );
    // ...and they are NOT a member yet. That distinction is the whole invite
    // model: the invitee materialises their own membership on their next sign-in.
    expect(screen.getByTestId("members-table").textContent).not.toContain(
      "colleague@firm",
    );
    // The notice says so in words, so nobody thinks the invite failed.
    expect(screen.getByTestId("members-notice").textContent).toContain(
      "PENDING invite, not access",
    );
  });

  it("explains a 403 instead of rendering an empty page", async () => {
    const base = createMockClient({ seed: false });
    const client: CoredClient = {
      ...base,
      listMembers: async () => {
        throw new ApiError(403, "default access: none", "forbidden");
      },
    };
    renderMembers(client);

    await waitFor(() =>
      expect(screen.getByTestId("members-error").textContent).toContain(
        "Not permitted for your security level (403)",
      ),
    );
    expect(screen.queryByTestId("members-table")).toBeNull();
  });

  it("names the step-up requirement when an invite needs WebAuthn", async () => {
    const base = createMockClient({ seed: false });
    const client: CoredClient = {
      ...base,
      inviteMember: async () => {
        throw new ApiError(
          403,
          "webauthn step-up required",
          "STEPUP_REQUIRED",
        );
      },
    };
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("invite-submit")).toBeTruthy());

    fireEvent.change(screen.getByTestId("invite-subject"), {
      target: { value: "colleague@firm" },
    });
    fireEvent.click(screen.getByTestId("invite-submit"));

    await waitFor(() =>
      expect(screen.getByTestId("members-error").textContent).toContain(
        "WebAuthn step-up required",
      ),
    );
  });

  it("reports the 409 when the subject is already a member", async () => {
    const client = createMockClient({ seed: false });
    const spy = vi.spyOn(client, "inviteMember");
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("invite-submit")).toBeTruthy());

    // browser-dev-owner is the mock's own Owner member.
    fireEvent.change(screen.getByTestId("invite-subject"), {
      target: { value: "browser-dev-owner" },
    });
    fireEvent.click(screen.getByTestId("invite-submit"));

    await waitFor(() =>
      expect(screen.getByTestId("members-error").textContent).toContain(
        "Already a member of this company (409)",
      ),
    );
    expect(spy).toHaveBeenCalled();
  });
});
