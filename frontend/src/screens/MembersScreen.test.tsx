import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { MembersScreen } from "./MembersScreen";
import { CompanyProvider } from "../app/CompanyContext";
import { createMockClient, ApiError } from "../api";
import type {
  CoredClient,
  InviteView,
  SecurityLevelView,
  SecurityVocabulary,
} from "../api";

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

/**
 * The writes this screen gained: change a member's level, revoke access, revoke
 * a pending invite, and create / edit / delete a custom security level.
 *
 * These tests drive a STUB client rather than the in-memory mock wherever the
 * claim is about the REQUEST the screen sends. The mock is a faithful stand-in
 * for the server's guards, but "what body did we PUT?" is a claim about this
 * component, and asserting it against a spy is the only way to see it — the
 * Owner-level case below sends a deliberately NARROWER body than the form
 * holds, and a test that only looked at the rendered result would not notice.
 */

const OWNER_LEVEL: SecurityLevelView = {
  id: "lvl-owner",
  name: "Owner",
  is_owner: true,
  default_access: "full",
  requires_webauthn: true,
  backdated_cutoff: "",
  member_count: 1,
  rules: [],
};
const ENTRY_LEVEL: SecurityLevelView = {
  id: "lvl-entry",
  name: "Data Entry",
  is_owner: false,
  default_access: "none",
  requires_webauthn: false,
  backdated_cutoff: "",
  member_count: 1,
  rules: [{ effect: "allow", action: "create", resource: "sales" }],
};

/** A vocabulary with a token no hardcoded client list could have guessed. */
const SERVED_VOCAB: SecurityVocabulary = {
  effects: ["allow", "disallow"],
  actions: ["display", "create", "*"],
  resources: ["report", "sales", "report:invented-by-the-server", "*"],
  default_accesses: ["full", "none"],
};

function member(subject: string, level: SecurityLevelView, isSelf = false) {
  return {
    user_id: `user-${subject}`,
    subject,
    email: "",
    display_name: "",
    security_level_id: level.id,
    security_level_name: level.name,
    is_owner: level.is_owner,
    is_self: isSelf,
    created_at: "2026-07-01T00:00:00Z",
  };
}

/**
 * A client with TWO members (so the last-Owner guard is not in the way), one
 * pending invite, both seeded levels, and spies on every write.
 */
function stubClient(over: Partial<CoredClient> = {}) {
  const base = createMockClient({ seed: false });
  const members = [
    member("browser-dev-owner", OWNER_LEVEL, true),
    member("second-owner", OWNER_LEVEL),
    member("clerk", ENTRY_LEVEL),
  ];
  const invites: InviteView[] = [
    {
      id: "inv-1",
      subject: "not-yet-signed-in",
      email: "",
      display_name: "",
      security_level_id: ENTRY_LEVEL.id,
      security_level_name: ENTRY_LEVEL.name,
      status: "pending",
      created_at: "2026-07-02T00:00:00Z",
      notification_status: "sent",
      notification_attempts: 1,
    },
  ];
  return {
    ...base,
    listMembers: vi.fn(async () => ({ members, invites })),
    listSecurityLevels: vi.fn(async () => ({
      security_levels: [OWNER_LEVEL, ENTRY_LEVEL],
    })),
    getSecurityVocabulary: vi.fn(async () => SERVED_VOCAB),
    updateMember: vi.fn(async () => ({
      user_id: "user-clerk",
      security_level_id: OWNER_LEVEL.id,
    })),
    removeMember: vi.fn(async () => ({ user_id: "user-clerk", status: "removed" })),
    revokeInvite: vi.fn(async () => ({ invite_id: "inv-1", status: "revoked" })),
    createSecurityLevel: vi.fn(async () => ({
      security_level_id: "lvl-new",
      name: "Auditor",
    })),
    updateSecurityLevel: vi.fn(async () => ({ security_level_id: "lvl-entry" })),
    deleteSecurityLevel: vi.fn(async () => ({
      security_level_id: "lvl-entry",
      status: "deleted",
    })),
    ...over,
  } as CoredClient;
}

describe("MembersScreen — changing a member's level", () => {
  it("applies the chosen level and reports the change", async () => {
    const client = stubClient();
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("member-level:clerk")).toBeTruthy());

    // No Apply button until the selection actually differs from what is held —
    // an admin should not be able to "apply" a no-op privilege change.
    expect(screen.queryByTestId("member-apply:clerk")).toBeNull();

    fireEvent.change(screen.getByTestId("member-level:clerk"), {
      target: { value: "Owner" },
    });
    fireEvent.click(screen.getByTestId("member-apply:clerk"));

    await waitFor(() =>
      expect(client.updateMember).toHaveBeenCalledWith("co-1", "user-clerk", {
        security_level_name: "Owner",
      }),
    );
    await waitFor(() =>
      expect(screen.getByTestId("members-notice").textContent).toContain(
        "clerk is now Owner",
      ),
    );
  });

  it("will not offer to demote or remove the LAST Owner, and says why", async () => {
    // One Owner only — the server would answer 409; the screen states the
    // reason up front rather than letting the click fail.
    const base = stubClient();
    const client: CoredClient = {
      ...base,
      listMembers: async () => ({
        members: [member("browser-dev-owner", OWNER_LEVEL, true), member("clerk", ENTRY_LEVEL)],
        invites: [],
      }),
    };
    renderMembers(client);

    await waitFor(() =>
      expect(screen.getByTestId("member-lastowner:browser-dev-owner")).toBeTruthy(),
    );
    expect(
      (screen.getByTestId("member-level:browser-dev-owner") as HTMLSelectElement).disabled,
    ).toBe(true);
    expect(screen.queryByTestId("member-remove:browser-dev-owner")).toBeNull();
    // ...while a non-Owner member is still revocable.
    expect(screen.getByTestId("member-remove:clerk")).toBeTruthy();
  });

  it("explains the 409 when the server refuses a demotion", async () => {
    const client = stubClient({
      updateMember: async () => {
        throw new ApiError(409, "the last Owner cannot be demoted", "conflict");
      },
    });
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("member-level:clerk")).toBeTruthy());

    fireEvent.change(screen.getByTestId("member-level:clerk"), {
      target: { value: "Owner" },
    });
    fireEvent.click(screen.getByTestId("member-apply:clerk"));

    await waitFor(() =>
      expect(screen.getByTestId("members-error").textContent).toContain(
        "the last Owner cannot be demoted",
      ),
    );
  });
});

describe("MembersScreen — revoking", () => {
  it("revokes a member only after an INLINE confirmation", async () => {
    const client = stubClient();
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("member-remove:clerk")).toBeTruthy());

    // First click ARMS the action; it must not have written anything yet.
    fireEvent.click(screen.getByTestId("member-remove:clerk"));
    expect(client.removeMember).not.toHaveBeenCalled();

    // The confirmation is INLINE (a second button), never window.confirm — a
    // native modal blocks the CDP channel the browser e2e drives.
    fireEvent.click(screen.getByTestId("member-remove-confirm:clerk"));
    await waitFor(() =>
      expect(client.removeMember).toHaveBeenCalledWith("co-1", "user-clerk"),
    );
    await waitFor(() =>
      expect(screen.getByTestId("members-notice").textContent).toContain(
        "Revoked clerk's access",
      ),
    );
  });

  it("cancelling the confirmation writes nothing", async () => {
    const client = stubClient();
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("member-remove:clerk")).toBeTruthy());

    fireEvent.click(screen.getByTestId("member-remove:clerk"));
    fireEvent.click(screen.getByText("Cancel"));
    expect(client.removeMember).not.toHaveBeenCalled();
    // ...and the un-armed button is back.
    expect(screen.getByTestId("member-remove:clerk")).toBeTruthy();
  });

  it("revokes a PENDING invite (two-step) and says it can never be claimed", async () => {
    const client = stubClient();
    renderMembers(client);
    await waitFor(() =>
      expect(screen.getByTestId("invite-revoke:not-yet-signed-in")).toBeTruthy(),
    );

    fireEvent.click(screen.getByTestId("invite-revoke:not-yet-signed-in"));
    expect(client.revokeInvite).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("invite-revoke-confirm:not-yet-signed-in"));

    await waitFor(() =>
      expect(client.revokeInvite).toHaveBeenCalledWith("co-1", "inv-1"),
    );
    await waitFor(() =>
      expect(screen.getByTestId("members-notice").textContent).toContain(
        "can never be claimed",
      ),
    );
  });
});

describe("MembersScreen — the security level editor", () => {
  it("offers ONLY the rule tokens the SERVER served", async () => {
    // The whole point of GET .../security-levels/vocabulary: a rule the PDP
    // cannot match is not rejected, it silently never fires — so for a
    // `disallow` the admin believes they forbade what the system permits. The
    // dropdowns must therefore be built from the server's list. Proven with a
    // resource no hardcoded client table could contain, and by the ABSENCE of a
    // real vocabulary token the stub deliberately withheld.
    const client = stubClient();
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("level-new")).toBeTruthy());
    fireEvent.click(screen.getByTestId("level-new"));

    const resource = (await screen.findByTestId("rule-resource:0")) as HTMLSelectElement;
    const offered = Array.from(resource.options).map((o) => o.value);
    expect(offered).toContain("report:invented-by-the-server");
    // Withheld by this server, so it must not be on offer — a hardcoded list
    // would have it.
    expect(offered).not.toContain("report:trial-balance");
    expect(offered).toEqual(SERVED_VOCAB.resources);

    const action = screen.getByTestId("rule-action:0") as HTMLSelectElement;
    expect(Array.from(action.options).map((o) => o.value)).toEqual(
      SERVED_VOCAB.actions,
    );
  });

  it("creates a custom level with default-DENY and the composed rules", async () => {
    const client = stubClient();
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("level-new")).toBeTruthy());
    fireEvent.click(screen.getByTestId("level-new"));

    // A NEW level defaults to deny-everything: it must not silently grant all.
    expect(
      (screen.getByTestId("level-default-access") as HTMLSelectElement).value,
    ).toBe("none");

    fireEvent.change(screen.getByTestId("level-name"), {
      target: { value: "Auditor" },
    });
    fireEvent.change(screen.getByTestId("rule-resource:0"), {
      target: { value: "report:invented-by-the-server" },
    });
    fireEvent.click(screen.getByTestId("level-save"));

    await waitFor(() =>
      expect(client.createSecurityLevel).toHaveBeenCalledWith("co-1", {
        name: "Auditor",
        default_access: "none",
        requires_webauthn: false,
        backdated_cutoff: "",
        rules: [
          {
            effect: "allow",
            action: "display",
            resource: "report:invented-by-the-server",
          },
        ],
      }),
    );
  });

  it("sends ONLY the cutoff when editing the built-in Owner level", async () => {
    // The Owner level is immutable except its back-dated cutoff: a PUT carrying
    // name/default_access/requires_webauthn/rules is a 422. The form still
    // DISPLAYS those fields (an admin should see the posture they cannot
    // change), so the narrowing has to happen in the request — which only a spy
    // on the body can see.
    const client = stubClient();
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("level-edit:Owner")).toBeTruthy());
    fireEvent.click(screen.getByTestId("level-edit:Owner"));

    await waitFor(() => expect(screen.getByTestId("level-owner-locked")).toBeTruthy());
    expect((screen.getByTestId("level-name") as HTMLInputElement).disabled).toBe(true);
    expect(
      (screen.getByTestId("level-default-access") as HTMLSelectElement).disabled,
    ).toBe(true);
    // The cutoff is the ONE editable field.
    const cutoff = screen.getByTestId("level-cutoff") as HTMLInputElement;
    expect(cutoff.disabled).toBe(false);
    fireEvent.change(cutoff, { target: { value: "2026-04-01" } });
    fireEvent.click(screen.getByTestId("level-save"));

    await waitFor(() =>
      expect(client.updateSecurityLevel).toHaveBeenCalledWith("co-1", "lvl-owner", {
        backdated_cutoff: "2026-04-01",
      }),
    );
  });

  it("explains a 409 when deleting a level that is still in use", async () => {
    const client = stubClient({
      deleteSecurityLevel: async () => {
        throw new ApiError(409, "1 member(s)/invite(s) still use it", "conflict");
      },
    });
    renderMembers(client);
    await waitFor(() =>
      expect(screen.getByTestId("level-delete:Data Entry")).toBeTruthy(),
    );

    fireEvent.click(screen.getByTestId("level-delete:Data Entry"));
    fireEvent.click(screen.getByTestId("level-delete-confirm:Data Entry"));

    await waitFor(() =>
      expect(screen.getByTestId("members-error").textContent).toContain(
        "1 member(s)/invite(s) still use it",
      ),
    );
    // The built-in Owner level offers no delete affordance at all.
    expect(screen.queryByTestId("level-delete:Owner")).toBeNull();
  });

  it("keeps the level list readable when the vocabulary cannot be fetched", async () => {
    // The vocabulary call is NON-FATAL: losing it must not blank a screen whose
    // members list loaded fine. Editing is what becomes unavailable.
    const client = stubClient({
      getSecurityVocabulary: async () => {
        throw new ApiError(403, "default access: none", "forbidden");
      },
    });
    renderMembers(client);

    await waitFor(() => expect(screen.getByTestId("vocab-unavailable")).toBeTruthy());
    expect(screen.getByTestId("levels-list").textContent).toContain("Data Entry");
    expect((screen.getByTestId("level-new") as HTMLButtonElement).disabled).toBe(true);
  });
});

/**
 * "Invited" and "NOTIFIED" are different questions (item C).
 *
 * Before the outbox an invite was a database row that told nobody, and the flow
 * assumed the administrator would message them out of band. A screen that showed
 * only "pending" would let a company believe it had onboarded somebody who never
 * heard about it — so the delivery state gets its own words, and the states that
 * need action are LOUDER than the ones that do not.
 */
describe("MembersScreen — invitation delivery", () => {
  const withInvite = (over: Partial<InviteView>): CoredClient => {
    const base = stubClient();
    return {
      ...base,
      listMembers: async () => ({
        members: [member("browser-dev-owner", OWNER_LEVEL, true)],
        invites: [
          {
            id: "inv-1",
            subject: "someone",
            email: "someone@firm.test",
            display_name: "",
            security_level_id: ENTRY_LEVEL.id,
            security_level_name: ENTRY_LEVEL.name,
            status: "pending",
            created_at: "2026-07-02T00:00:00Z",
            notification_status: "pending",
            notification_attempts: 0,
            ...over,
          },
        ],
      }),
    };
  };

  it("says the invitation was SENT", async () => {
    renderMembers(withInvite({ notification_status: "sent", notification_attempts: 1 }));
    await waitFor(() =>
      expect(screen.getByTestId("invite-delivery:someone").textContent).toContain(
        "invitation sent",
      ),
    );
  });

  it("says it is still QUEUED", async () => {
    renderMembers(withInvite({ notification_status: "pending" }));
    await waitFor(() =>
      expect(screen.getByTestId("invite-delivery:someone").textContent).toContain(
        "invitation queued",
      ),
    );
  });

  it("is LOUD when delivery was abandoned, and says to tell them yourself", async () => {
    renderMembers(
      withInvite({
        notification_status: "failed",
        notification_attempts: 5,
        notification_error: "smtp: connection refused",
      }),
    );
    await waitFor(() =>
      expect(screen.getByTestId("invite-delivery:someone").textContent).toContain(
        "NOT delivered",
      ),
    );
    const text = screen.getByTestId("invite-delivery:someone").textContent ?? "";
    expect(text).toContain("5 attempts");
    // The transport's own words, so an operator can act on it.
    expect(text).toContain("smtp: connection refused");
    expect(text).toContain("tell them yourself");
  });

  it("an invite with NO notification row says so rather than staying silent", async () => {
    // Invites created before delivery existed. Silence would read as "sent".
    renderMembers(withInvite({ notification_status: "" }));
    await waitFor(() =>
      expect(screen.getByTestId("invite-delivery:someone").textContent).toContain(
        "no invitation sent",
      ),
    );
  });

  /**
   * THE 41-ROW LIE. An invite carrying no email address has the sign-in SUBJECT
   * as its outbox recipient (notify.InviteMessage's fallback), and a subject is
   * an identifier, not an address — nothing could be delivered to it. The live
   * database holds 52 such notifications at status='sent', 41 with no '@' in the
   * recipient at all, because the log transport reported success after logging
   * "NO EMAIL ON FILE … nothing was sent". The server no longer records that as
   * a send, but those rows are still there, so the SCREEN must not repeat their
   * claim either.
   */
  it("will not say SENT for an invite that has no address to send to", async () => {
    renderMembers(
      withInvite({
        subject: "subject-with-no-at-sign",
        email: "",
        notification_status: "sent",
        notification_attempts: 1,
      }),
    );
    const cell = await waitFor(() =>
      screen.getByTestId("invite-delivery:subject-with-no-at-sign"),
    );
    const text = cell.textContent ?? "";
    expect(text).toContain("no invitation was sent");
    expect(text).toContain("NO EMAIL ADDRESS");
    expect(text).toContain("tell them yourself");
    // And specifically NOT the sentence that made 41 rows a lie.
    expect(text).not.toContain("invitation sent —");
    expect(text.startsWith("invitation sent")).toBe(false);
  });

  it("will not say QUEUED for an addressless invite either", async () => {
    // 'pending' is not honest here: the queue can only ever dead-letter this
    // one, so "invitation queued" describes work that will never deliver.
    renderMembers(
      withInvite({
        subject: "subject-with-no-at-sign",
        email: "",
        notification_status: "pending",
        notification_attempts: 0,
      }),
    );
    await waitFor(() =>
      expect(
        screen.getByTestId("invite-delivery:subject-with-no-at-sign").textContent,
      ).toContain("no invitation was sent"),
    );
    expect(
      screen.getByTestId("invite-delivery:subject-with-no-at-sign").textContent,
    ).not.toContain("queued");
  });

  it("renders the server's own no-address outcome in the loud words, not as a transport error", async () => {
    // What cored writes now: status 'failed' + notify.NoAddressReason as
    // last_error. It must NOT read as "NOT delivered after 1 attempt", which
    // suggests a transport that might work next time.
    renderMembers(
      withInvite({
        subject: "someone",
        email: "",
        notification_status: "failed",
        notification_attempts: 1,
        notification_error: "no email address on file — nothing was sent",
      }),
    );
    const text = await waitFor(
      () => screen.getByTestId("invite-delivery:someone").textContent ?? "",
    );
    expect(text).toContain("no invitation was sent");
    expect(text).not.toContain("1 attempt");
  });

  it("still says SENT when an email address was on file", async () => {
    // The guard above must not swallow real delivery.
    renderMembers(
      withInvite({
        subject: "someone",
        email: "someone@firm.test",
        notification_status: "sent",
        notification_attempts: 1,
      }),
    );
    await waitFor(() =>
      expect(screen.getByTestId("invite-delivery:someone").textContent).toContain(
        "invitation sent",
      ),
    );
  });

  it("still says SENT when the SUBJECT is itself an address", async () => {
    // No email column, but the subject is deliverable — which is what the
    // server's own fallback delivers to, so this one really was sent.
    renderMembers(
      withInvite({
        subject: "self@firm.test",
        email: "",
        notification_status: "sent",
        notification_attempts: 1,
      }),
    );
    await waitFor(() =>
      expect(
        screen.getByTestId("invite-delivery:self@firm.test").textContent,
      ).toContain("invitation sent"),
    );
  });
});

/**
 * The invite form collects the ADDRESS, and the banners are ANNOUNCED.
 *
 * The first is what makes delivery possible at all: with no email the outbox's
 * recipient is the sign-in subject and the invitation reaches nobody. The second
 * is the difference between a refusal a screen-reader operator hears and one
 * they do not — the banner is inserted mid-page, above a panel that unmounts and
 * remounts on the refresh, so nothing but a live region will speak it.
 */
describe("MembersScreen — an invitation that can actually arrive, announced", () => {
  /** stubClient + a SPY on inviteMember, since the claim is about the body sent. */
  const invitingClient = () =>
    stubClient({
      inviteMember: vi.fn(async () => ({
        invite_id: "inv-2",
        subject: "newcomer",
        status: "pending",
      })),
    });

  it("sends the email address with the invite", async () => {
    const client = invitingClient();
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("invite-submit")).toBeTruthy());

    fireEvent.change(screen.getByTestId("invite-subject"), {
      target: { value: "newcomer" },
    });
    fireEvent.change(screen.getByTestId("invite-email"), {
      target: { value: "newcomer@firm.test" },
    });
    fireEvent.click(screen.getByTestId("invite-submit"));

    await waitFor(() =>
      expect(client.inviteMember).toHaveBeenCalledWith("co-1", {
        subject: "newcomer",
        security_level_name: "Data Entry",
        email: "newcomer@firm.test",
      }),
    );
    expect(screen.getByTestId("members-notice").textContent).toContain(
      "queued to newcomer@firm.test",
    );
  });

  it("says plainly that an invite with NO email was sent to nobody", async () => {
    const client = invitingClient();
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("invite-submit")).toBeTruthy());

    fireEvent.change(screen.getByTestId("invite-subject"), {
      target: { value: "newcomer" },
    });
    fireEvent.click(screen.getByTestId("invite-submit"));

    await waitFor(() =>
      expect(screen.getByTestId("members-notice").textContent).toContain(
        "NO invitation was sent to anybody",
      ),
    );
    // An empty email is OMITTED, not sent as "": the server distinguishes
    // "no address on file" from an address.
    expect(client.inviteMember).toHaveBeenCalledWith("co-1", {
      subject: "newcomer",
      security_level_name: "Data Entry",
    });
  });

  it("announces a refusal to assistive technology (role=alert)", async () => {
    const client = stubClient({
      inviteMember: async () => {
        throw new ApiError(403, "webauthn step-up required", "STEPUP_REQUIRED");
      },
    });
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("invite-submit")).toBeTruthy());
    // Nothing is being announced before the refusal.
    expect(screen.queryAllByRole("alert")).toHaveLength(0);

    fireEvent.change(screen.getByTestId("invite-subject"), {
      target: { value: "newcomer" },
    });
    fireEvent.click(screen.getByTestId("invite-submit"));

    // The BANNER ITSELF is the live region — a probe that found the text but
    // `[role="alert"] count=0` is exactly the silence this closes.
    const alert = await waitFor(() => screen.getByRole("alert"));
    expect(alert).toBe(screen.getByTestId("members-error"));
    expect(alert.textContent).toContain("WebAuthn step-up required");
  });

  it("announces the outcome of a write that SUCCEEDED, politely", async () => {
    const client = invitingClient();
    renderMembers(client);
    await waitFor(() => expect(screen.getByTestId("invite-submit")).toBeTruthy());

    fireEvent.change(screen.getByTestId("invite-subject"), {
      target: { value: "newcomer" },
    });
    fireEvent.click(screen.getByTestId("invite-submit"));

    // role="status" (polite), not alert: it confirms what happened rather than
    // interrupting to report what did not.
    const status = await waitFor(() => screen.getByRole("status"));
    expect(status).toBe(screen.getByTestId("members-notice"));
  });
});
