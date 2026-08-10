import { useCallback, useEffect, useMemo, useState } from "react";
import { ApiError } from "../api";
import type {
  InviteView,
  MemberView,
  MembersResponse,
  SecurityLevelRule,
  SecurityLevelView,
  SecurityVocabulary,
  UpdateMemberRequest,
} from "../api";
import { useClient, useCompanyID } from "../app/CompanyContext";
import type { ScreenComponentProps } from "../app/screenRegistry";

/**
 * Users & Roles — the browser half of the SaaS admin API.
 *
 * Shows, for the active company: its MEMBERS (with the security level each
 * holds), its PENDING INVITES, and its SECURITY LEVELS with their Disallow/Allow
 * rules — and now performs every write the API offers: invite, CHANGE A
 * MEMBER'S LEVEL, REVOKE ACCESS, REVOKE A PENDING INVITE, and CREATE / EDIT /
 * DELETE a custom security level.
 *
 * FIVE THINGS THIS SCREEN MUST NOT HIDE:
 *
 *  1. **An invite is not a membership.** The server records an invite addressed
 *     to a JWT subject and the INVITEE materialises their own membership on
 *     their next `GET /v1/me`. Pending invites are therefore listed separately
 *     and explicitly, or an administrator would think the invite failed.
 *
 *  2. **A 403 is the product working.** This screen is PDP-gated on the
 *     `security` resource, so a Data Entry operator gets a 403 — and the error
 *     says why, rather than rendering an empty page that looks broken. A 403
 *     STEPUP_REQUIRED on any write (an Owner without a WebAuthn AMR) says that
 *     too: granting access is a write, and writes need the second factor.
 *
 *  3. **The four server guards are EXPLAINED, not swallowed.** Demoting or
 *     removing the last Owner is a 409 (a company can never be left
 *     unadministrable); the Owner level is immutable except its back-dated
 *     cutoff (422); a level still used by a member OR a pending invite cannot be
 *     deleted (409); and a rule naming a token outside the closed vocabulary is
 *     a 422. Each is rendered as the reason, not as a failed action.
 *
 *  4. **A rule's tokens come from the SERVER** (`getSecurityVocabulary`), never
 *     from a hardcoded list here. A misspelled rule is not rejected by the PDP —
 *     it silently never matches, so a `disallow` an administrator believes they
 *     wrote would permit the thing instead. Dropdowns built from the served
 *     vocabulary make that rule unrepresentable rather than merely rejected.
 *
 *  5. **Destructive actions confirm INLINE, never with `window.confirm`.** A
 *     native modal blocks the CDP channel the browser e2e drives, so a
 *     confirm() here would make revoke/delete permanently untestable — and an
 *     untested revoke path on an access-control screen is the wrong trade.
 */

/**
 * ACCOUNT-LOCK STATE, and the ONE remedy for it (D55).
 *
 * ⛔ THESE TWO TYPES BELONG IN `api/admin.ts` BESIDE `MemberView` /
 * `UpdateMemberRequest`, AND THEY ARE DECLARED HERE ON PURPOSE. `api/*.ts` is
 * shared surface that more than one workstream is editing, so this lane states
 * the shape it needs locally and REPORTS the two-field edit rather than making
 * it. The server (handler_admin.go's memberJSON / memberLevelReq) is the
 * authority for both; when the edit lands upstream these aliases collapse into
 * plain imports and nothing else here changes.
 *
 * The defect they exist for: a locked-out account was INVISIBLE. Users & Roles
 * is the only member-admin surface in the product and it rendered no lock state
 * at all — so the administrator's screen reported that somebody had access while
 * that person could not sign in, and there was no route, no key and no screen
 * anywhere that could free them.
 */
type MemberLockView = MemberView & {
  /** RFC3339 UTC, absent when not locked. */
  locked_until?: string;
  /** The RUNNING count of failed sign-ins since the last success (D74). */
  failed_attempts?: number;
};

/** `{"unlock": true}` clears the account lockout; see handleUpdateMember. */
type UnlockMemberRequest = UpdateMemberRequest & { unlock?: boolean };

/**
 * When the lock lifts, in the ADMINISTRATOR's clock and in plain words.
 *
 * The server sends a UTC instant. This audience is on IST, where a UTC instant
 * after 18:30 local names YESTERDAY'S DATE — which reads as a lock that expired
 * long ago and would send an administrator away without pressing Unlock. The
 * zone is the browser's own (a CA in London must be told London time), and it is
 * NAMED, so the sentence cannot be misread as some other clock.
 */
function lockText(m: MemberLockView): string {
  const raw = m.locked_until ?? "";
  const ms = Date.parse(raw);
  if (!Number.isFinite(ms)) return `locked (until ${raw})`;
  const when = new Intl.DateTimeFormat(undefined, {
    day: "numeric",
    month: "short",
    hour: "numeric",
    minute: "2-digit",
    timeZoneName: "short",
  }).format(new Date(ms));
  const mins = Math.ceil((ms - Date.now()) / 60000);
  if (mins <= 0) return `lock lapsing (ran to ${when})`;
  return `LOCKED OUT until ${when} (${mins} min)`;
}

/** Human explanation for the status codes this screen can legitimately receive. */
function explain(err: unknown): string {
  if (err instanceof ApiError) {
    const detail = err.message?.trim() ? ` ${err.message}` : "";
    if (err.status === 403 && /STEPUP|step-up/i.test(err.message)) {
      return "WebAuthn step-up required: administering users is a write, so it needs a second factor (403 STEPUP_REQUIRED).";
    }
    if (err.status === 403) {
      return `Not permitted for your security level (403).${detail}`;
    }
    if (err.status === 409) {
      // 409 now covers three distinct refusals, so the server's own sentence is
      // the message — a fixed string here would misreport two of them.
      if (/already a member/i.test(err.message)) {
        return `Already a member of this company (409). Change their level below instead of re-inviting.${""}`;
      }
      return `Refused (409).${detail}`;
    }
    if (err.status === 422) {
      return `Rejected (422).${detail}`;
    }
    return `${err.status}:${detail}`;
  }
  return err instanceof Error ? err.message : String(err);
}

/**
 * Could this invitation have reached ANYBODY?
 *
 * Mirrors the server's own predicate, notify.Message.HasAddress — "A subject is
 * an identifier, not an address." The outbox's recipient is `email` when one was
 * given and otherwise the sign-in SUBJECT (`recipient := email; if recipient ==
 * "" { recipient = subject }` in notify.InviteMessage), which is exactly the pair
 * this row already carries.
 *
 * WHY THIS IS ASKED HERE AND NOT JUST BELIEVED FROM `notification_status`.
 * 52 rows in the live outbox say status='sent'; 41 of them have a recipient with
 * no '@' in it, because the log transport correctly reported "NO EMAIL ON FILE …
 * nothing was sent" by returning success (a logger that could fail would
 * manufacture dead letters) and the outcome was then recorded as a send. The
 * server no longer writes that for NEW notifications, but nothing has restated
 * the rows already written — a data repair needs a migration or a script, which
 * is not this screen's to do. Until then this screen must not repeat their claim.
 */
function couldReachAnybody(i: InviteView): boolean {
  const recipient = (i.email || i.subject).trim();
  const at = recipient.indexOf("@");
  return at > 0 && at < recipient.length - 1;
}

/**
 * How the invitation itself is getting to the person — which is NOT the same
 * question as whether they have accepted it.
 *
 * Before the outbox, an invite was a database row that notified nobody and the
 * flow assumed the administrator would message them out of band. Showing only
 * "pending" would let a company believe it had onboarded somebody who never
 * heard about it, so the delivery state is stated in its own words. "not sent"
 * is deliberately louder than silence.
 */
function deliveryText(i: InviteView): string {
  // NO ADDRESS OUTRANKS EVERY STORED STATUS, including 'sent' and 'pending'.
  // There is nowhere for this invitation to go, so "invitation sent" is false,
  // "invitation queued" is false in substance (the queue can only ever
  // dead-letter it) and silence is the failure the delivery state exists to
  // prevent: "a company that believed it had onboarded somebody who never heard
  // about it". As loud as the server's own log line, and it names the remedy.
  if (
    !couldReachAnybody(i) ||
    /no email address on file/i.test(i.notification_error ?? "")
  ) {
    return "no invitation was sent — NO EMAIL ADDRESS on file, so nobody could be told; tell them yourself";
  }
  switch (i.notification_status) {
    case "sent":
      return "invitation sent";
    case "sending":
      return "invitation sending…";
    case "pending":
      return "invitation queued";
    case "failed":
      return `invitation NOT delivered after ${i.notification_attempts} attempt${
        i.notification_attempts === 1 ? "" : "s"
      }${i.notification_error ? ` (${i.notification_error})` : ""} — tell them yourself`;
    default:
      // No notification row at all: an invite created before delivery existed.
      return "no invitation sent — tell them yourself";
  }
}

function ruleText(l: SecurityLevelView): string {
  if (l.is_owner) return "full access (owner)";
  if (l.rules.length === 0) {
    return l.default_access === "full" ? "full access" : "no rules — default deny";
  }
  return l.rules.map((r) => `${r.effect} ${r.action} ${r.resource}`).join(", ");
}

/** The level editor's working copy. `levelID` empty means "creating". */
interface LevelDraft {
  levelID: string;
  name: string;
  defaultAccess: string;
  requiresWebauthn: boolean;
  cutoff: string;
  rules: SecurityLevelRule[];
  /** True when editing the Owner level: only the cutoff may be sent (422 otherwise). */
  ownerLocked: boolean;
}

function emptyDraft(vocab: SecurityVocabulary | null): LevelDraft {
  return {
    levelID: "",
    name: "",
    // Default-DENY, mirroring the server: a level created with no explicit
    // default must not silently grant everything.
    defaultAccess: "none",
    requiresWebauthn: false,
    cutoff: "",
    rules: vocab
      ? [{ effect: "allow", action: "display", resource: "report" }]
      : [],
    ownerLocked: false,
  };
}

function draftOf(l: SecurityLevelView): LevelDraft {
  return {
    levelID: l.id,
    name: l.name,
    defaultAccess: l.default_access,
    requiresWebauthn: l.requires_webauthn,
    cutoff: l.backdated_cutoff,
    rules: l.rules.map((r) => ({
      effect: r.effect,
      action: r.action,
      resource: r.resource,
    })),
    ownerLocked: l.is_owner,
  };
}

export function MembersScreen(_props: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();

  const [data, setData] = useState<MembersResponse | null>(null);
  const [levels, setLevels] = useState<SecurityLevelView[]>([]);
  const [vocab, setVocab] = useState<SecurityVocabulary | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [subject, setSubject] = useState("");
  /**
   * The address the invitation is actually DELIVERED to. Optional on the wire
   * (`InviteMemberRequest.email`, and the server has never required one) but
   * without it the outbox's recipient falls back to the sign-in subject, which
   * no transport can deliver to — so the form asks for it, and says what its
   * absence costs.
   */
  const [email, setEmail] = useState("");
  const [levelName, setLevelName] = useState("");
  const [inviting, setInviting] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  /** The chosen-but-not-yet-applied level per member row, keyed by user id. */
  const [pendingLevel, setPendingLevel] = useState<Record<string, string>>({});
  /** The single destructive action awaiting inline confirmation, if any. */
  const [confirming, setConfirming] = useState<string | null>(null);
  /** In-flight write id, so exactly one button shows a busy state. */
  const [busy, setBusy] = useState<string | null>(null);
  const [draft, setDraft] = useState<LevelDraft | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [members, lv] = await Promise.all([
        client.listMembers(companyID),
        client.listSecurityLevels(companyID),
      ]);
      setData(members);
      setLevels(lv.security_levels);
      setPendingLevel({});
      setConfirming(null);
      // Default the invite level to the first NON-owner level, so the obvious
      // action is the least-privileged one rather than "make them an Owner".
      setLevelName(
        (prev) =>
          prev ||
          lv.security_levels.find((l) => !l.is_owner)?.name ||
          lv.security_levels[0]?.name ||
          "",
      );
    } catch (e) {
      setError(explain(e));
    } finally {
      setLoading(false);
    }
  }, [client, companyID]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // The rule vocabulary is fetched separately and NON-FATALLY: it is only needed
  // to open the level editor, so a company whose members list loads must still
  // render if this one call fails.
  useEffect(() => {
    let live = true;
    client
      .getSecurityVocabulary(companyID)
      .then((v) => {
        if (live) setVocab(v);
      })
      .catch(() => {
        if (live) setVocab(null);
      });
    return () => {
      live = false;
    };
  }, [client, companyID]);

  /**
   * Run one admin write: clear the banners, report the outcome in the user's
   * language, and re-read from the SERVER rather than patching local state — the
   * server is the authority on what a write actually did (a rename cascades to
   * member rows, a revoke changes member_count).
   */
  const run = useCallback(
    async (key: string, what: () => Promise<string>) => {
      setBusy(key);
      setError(null);
      setNotice(null);
      try {
        setNotice(await what());
        await refresh();
      } catch (e) {
        setError(explain(e));
      } finally {
        setBusy(null);
        setConfirming(null);
      }
    },
    [refresh],
  );

  const invite = useCallback(async () => {
    const s = subject.trim();
    const em = email.trim();
    if (!s || !levelName) return;
    setInviting(true);
    setError(null);
    setNotice(null);
    try {
      await client.inviteMember(companyID, {
        subject: s,
        security_level_name: levelName,
        // Omitted rather than sent as "" when blank: the server distinguishes
        // "no address on file" from an address, and an empty string is the
        // former dressed as the latter.
        ...(em ? { email: em } : {}),
      });
      setSubject("");
      setEmail("");
      setNotice(
        em
          ? `Invited ${s} as ${levelName}, and an invitation is queued to ${em}. They become a member when they next sign in — until then this is a PENDING invite, not access.`
          : // SAID AT THE MOMENT IT IS TRUE. With no address the outbox has
            // nowhere to deliver to, so the invitation is never sent to anybody —
            // and an administrator who is not told that here will find out never.
            `Invited ${s} as ${levelName}, but NO EMAIL ADDRESS was given, so NO invitation was sent to anybody — tell them yourself. They become a member when they next sign in — until then this is a PENDING invite, not access.`,
      );
      await refresh();
    } catch (e) {
      setError(explain(e));
    } finally {
      setInviting(false);
    }
  }, [subject, email, levelName, client, companyID, refresh]);

  const saveDraft = useCallback(async () => {
    if (!draft) return;
    const creating = draft.levelID === "";
    await run(creating ? "level-create" : `level-save:${draft.levelID}`, async () => {
      if (creating) {
        await client.createSecurityLevel(companyID, {
          name: draft.name.trim(),
          default_access: draft.defaultAccess,
          requires_webauthn: draft.requiresWebauthn,
          backdated_cutoff: draft.cutoff,
          rules: draft.rules,
        });
        setDraft(null);
        return `Created security level "${draft.name.trim()}". Nobody holds it until you invite or move somebody to it.`;
      }
      // The Owner level accepts ONLY its cutoff; sending anything else is a 422,
      // so this sends only what that level is allowed to change.
      const body = draft.ownerLocked
        ? { backdated_cutoff: draft.cutoff }
        : {
            name: draft.name.trim(),
            default_access: draft.defaultAccess,
            requires_webauthn: draft.requiresWebauthn,
            backdated_cutoff: draft.cutoff,
            rules: draft.rules,
          };
      await client.updateSecurityLevel(companyID, draft.levelID, body);
      setDraft(null);
      return draft.ownerLocked
        ? `Updated the Owner level's back-dated cutoff${draft.cutoff ? ` to ${draft.cutoff}` : " (cleared)"}.`
        : `Updated security level "${draft.name.trim()}". Its holders' access changed immediately.`;
    });
  }, [draft, client, companyID, run]);

  const canSaveDraft =
    !!draft && (draft.ownerLocked || draft.name.trim().length > 0);

  const ownerCount = useMemo(
    () => (data?.members ?? []).filter((m) => m.is_owner).length,
    [data],
  );

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="mb-2 text-lg font-bold text-tally-title">Users &amp; Roles</h1>
      <p className="mb-3 text-sm">
        Who may open this company, and what they may do. Esc to go back.
      </p>

      {/* ANNOUNCED, not merely rendered.
          A refusal here is inserted mid-page, above a panel that unmounts and
          remounts on the refresh that follows — so focus is nowhere near it and
          a screen-reader operator who pressed Invite got silence: the same "I
          pressed the key and nothing happened" this shell refuses everywhere
          else (see Chrome's status line and stepup-failed banner, which is where
          role="alert" / role="status" is used for the same reason). The error is
          assertive because it means the write did NOT happen; the notice is
          polite because it only confirms what did. */}
      {error ? (
        <p
          role="alert"
          data-testid="members-error"
          className="mb-2 max-w-3xl text-sm text-red-400"
        >
          {error}
        </p>
      ) : null}
      {notice ? (
        <p
          role="status"
          data-testid="members-notice"
          className="mb-2 max-w-3xl text-sm text-tally-title"
        >
          {notice}
        </p>
      ) : null}

      {loading ? (
        <p data-testid="members-loading" className="text-sm">
          Loading users…
        </p>
      ) : data ? (
        <>
          <div className="mb-4 max-w-3xl bg-tally-panel p-3">
            <h2 className="mb-1 text-sm text-tally-title">Members</h2>
            <table className="w-full text-sm" data-testid="members-table">
              <thead>
                <tr className="text-left text-xs uppercase text-tally-title">
                  <th className="pr-4">Subject</th>
                  <th className="pr-4">Security level</th>
                  <th className="pr-4">Since</th>
                  {/* SIGN-IN, not "Access": the two are different questions and
                      conflating them is the defect. A member can hold Owner and
                      still be unable to get in. */}
                  <th className="pr-4">Sign-in</th>
                  <th>Access</th>
                </tr>
              </thead>
              <tbody>
                {(data.members as MemberLockView[]).map((m) => {
                  const chosen = pendingLevel[m.user_id] ?? m.security_level_name;
                  const changed = chosen !== m.security_level_name;
                  const locked = !!m.locked_until;
                  const failed = m.failed_attempts ?? 0;
                  // The last Owner cannot be demoted or removed (409). Saying so
                  // BEFORE the click is kinder than a refusal after it — and the
                  // server still enforces it either way.
                  const lastOwner = m.is_owner && ownerCount <= 1;
                  return (
                    <tr key={m.user_id} data-testid={`member-row:${m.subject}`}>
                      <td className="pr-4 align-top">
                        {m.subject}
                        {m.is_self ? " (you)" : ""}
                      </td>
                      <td className="pr-4 align-top">
                        <select
                          aria-label={`Security level for ${m.subject}`}
                          data-testid={`member-level:${m.subject}`}
                          className="bg-tally-field px-1 text-tally-fieldText outline-none disabled:opacity-50"
                          value={chosen}
                          disabled={lastOwner}
                          onChange={(e) =>
                            setPendingLevel((p) => ({
                              ...p,
                              [m.user_id]: e.target.value,
                            }))
                          }
                        >
                          {levels.map((l) => (
                            <option key={l.id} value={l.name}>
                              {l.name}
                            </option>
                          ))}
                        </select>
                        {changed ? (
                          <button
                            type="button"
                            data-testid={`member-apply:${m.subject}`}
                            disabled={busy !== null}
                            className="ml-2 bg-tally-active px-2 py-0.5 text-xs text-white disabled:opacity-50"
                            onClick={() =>
                              void run(`member-apply:${m.user_id}`, async () => {
                                await client.updateMember(companyID, m.user_id, {
                                  security_level_name: chosen,
                                });
                                return `${m.subject} is now ${chosen}. Their access changed immediately.`;
                              })
                            }
                          >
                            {busy === `member-apply:${m.user_id}` ? "Saving…" : "Apply"}
                          </button>
                        ) : null}
                      </td>
                      <td className="pr-4 align-top">{m.created_at.slice(0, 10)}</td>
                      {/* SIGN-IN STATE + THE ONLY REMEDY (D55).
                          A locked account had no way out anywhere in the
                          product: the failure counter resets on a SUCCESSFUL
                          login, which a locked account cannot perform. Unlock
                          is a write on the `security` resource, so it inherits
                          the Owner level's WebAuthn step-up exactly as granting
                          access does — freeing an account is at least as
                          sensitive as granting one. */}
                      <td className="pr-4 align-top">
                        {locked ? (
                          <>
                            <span
                              data-testid={`member-lock:${m.subject}`}
                              className="text-xs text-red-400"
                            >
                              {lockText(m)}
                            </span>{" "}
                            <button
                              type="button"
                              data-testid={`member-unlock:${m.subject}`}
                              disabled={busy !== null}
                              className="bg-tally-active px-2 py-0.5 text-xs text-white disabled:opacity-50"
                              onClick={() =>
                                void run(`member-unlock:${m.user_id}`, async () => {
                                  await client.updateMember(companyID, m.user_id, {
                                    unlock: true,
                                  } as UnlockMemberRequest);
                                  // Said in full, because the half that is NOT
                                  // true is the half an administrator would
                                  // otherwise assume: unlocking lets them sign
                                  // in again, it does not revive the sessions
                                  // the lock killed.
                                  return `Unlocked ${m.subject}. They can sign in again now — but any session they held when the account was locked stays revoked, so they must sign in afresh.`;
                                })
                              }
                            >
                              {busy === `member-unlock:${m.user_id}`
                                ? "Unlocking…"
                                : "Unlock"}
                            </button>
                          </>
                        ) : failed > 0 ? (
                          // PARTWAY THERE (D74). The running counter is what
                          // makes an attack visible BEFORE the lock, which is
                          // the only time anyone can do anything about it.
                          <span
                            data-testid={`member-failed:${m.subject}`}
                            className="text-xs"
                          >
                            {failed} failed sign-in{failed === 1 ? "" : "s"}
                          </span>
                        ) : (
                          <span className="text-xs">ok</span>
                        )}
                      </td>
                      <td className="align-top">
                        {lastOwner ? (
                          <span
                            data-testid={`member-lastowner:${m.subject}`}
                            className="text-xs"
                          >
                            last Owner — cannot be demoted or removed
                          </span>
                        ) : confirming === `member-remove:${m.user_id}` ? (
                          <>
                            <button
                              type="button"
                              data-testid={`member-remove-confirm:${m.subject}`}
                              disabled={busy !== null}
                              className="bg-red-700 px-2 py-0.5 text-xs text-white disabled:opacity-50"
                              onClick={() =>
                                void run(`member-remove:${m.user_id}`, async () => {
                                  await client.removeMember(companyID, m.user_id);
                                  return `Revoked ${m.subject}'s access. They can no longer open this company.`;
                                })
                              }
                            >
                              Confirm revoke
                            </button>
                            <button
                              type="button"
                              className="ml-2 px-2 py-0.5 text-xs underline"
                              onClick={() => setConfirming(null)}
                            >
                              Cancel
                            </button>
                          </>
                        ) : (
                          <button
                            type="button"
                            data-testid={`member-remove:${m.subject}`}
                            disabled={busy !== null}
                            className="px-2 py-0.5 text-xs underline disabled:opacity-50"
                            onClick={() => setConfirming(`member-remove:${m.user_id}`)}
                          >
                            Revoke access
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className="mb-4 max-w-3xl bg-tally-panel p-3">
            <h2 className="mb-1 text-sm text-tally-title">Pending invites</h2>
            {data.invites.length === 0 ? (
              <p data-testid="invites-empty" className="text-sm">
                None. An invite appears here until the invitee signs in and
                claims it.
              </p>
            ) : (
              <ul data-testid="invites-list" className="text-sm">
                {data.invites.map((i) => (
                  <li key={i.id}>
                    {i.subject} · {i.security_level_name} · pending ·{" "}
                    <span data-testid={`invite-delivery:${i.subject}`}>
                      {deliveryText(i)}
                    </span>{" "}
                    {confirming === `invite-revoke:${i.id}` ? (
                      <>
                        <button
                          type="button"
                          data-testid={`invite-revoke-confirm:${i.subject}`}
                          disabled={busy !== null}
                          className="bg-red-700 px-2 py-0.5 text-xs text-white disabled:opacity-50"
                          onClick={() =>
                            void run(`invite-revoke:${i.id}`, async () => {
                              await client.revokeInvite(companyID, i.id);
                              return `Revoked the invite for ${i.subject}. It can never be claimed now.`;
                            })
                          }
                        >
                          Confirm revoke
                        </button>
                        <button
                          type="button"
                          className="ml-2 px-2 py-0.5 text-xs underline"
                          onClick={() => setConfirming(null)}
                        >
                          Cancel
                        </button>
                      </>
                    ) : (
                      <button
                        type="button"
                        data-testid={`invite-revoke:${i.subject}`}
                        disabled={busy !== null}
                        className="px-2 py-0.5 text-xs underline disabled:opacity-50"
                        onClick={() => setConfirming(`invite-revoke:${i.id}`)}
                      >
                        Revoke invite
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="mb-4 max-w-3xl bg-tally-panel p-3">
            <h2 className="mb-1 text-sm text-tally-title">Invite someone</h2>
            <div className="flex flex-wrap items-center gap-2">
              <input
                aria-label="Subject"
                data-testid="invite-subject"
                className="bg-tally-field px-1 text-tally-fieldText outline-none"
                value={subject}
                placeholder="their sign-in subject"
                onChange={(e) => setSubject(e.target.value)}
              />
              {/* The address the invitation goes TO. Optional because the server
                  never required it — but an invite with no email is delivered to
                  nobody, so the placeholder says so rather than leaving an
                  administrator to discover it from an invitee who never wrote
                  back. */}
              <input
                aria-label="Email"
                data-testid="invite-email"
                type="email"
                className="bg-tally-field px-1 text-tally-fieldText outline-none"
                value={email}
                placeholder="email — without one, nobody is told"
                onChange={(e) => setEmail(e.target.value)}
              />
              <select
                aria-label="Security level"
                data-testid="invite-level"
                className="bg-tally-field px-1 text-tally-fieldText outline-none"
                value={levelName}
                onChange={(e) => setLevelName(e.target.value)}
              >
                {levels.map((l) => (
                  <option key={l.id} value={l.name}>
                    {l.name}
                  </option>
                ))}
              </select>
              <button
                type="button"
                data-testid="invite-submit"
                disabled={inviting || subject.trim().length === 0}
                className="bg-tally-active px-2 py-1 text-xs text-white disabled:opacity-50"
                onClick={() => void invite()}
              >
                {inviting ? "Inviting…" : "Invite"}
              </button>
            </div>
          </div>

          <div className="mb-4 max-w-3xl bg-tally-panel p-3">
            <div className="mb-1 flex items-center justify-between">
              <h2 className="text-sm text-tally-title">Security levels</h2>
              <button
                type="button"
                data-testid="level-new"
                disabled={!vocab || busy !== null}
                className="bg-tally-active px-2 py-0.5 text-xs text-white disabled:opacity-50"
                onClick={() => setDraft(emptyDraft(vocab))}
              >
                New level
              </button>
            </div>
            {!vocab ? (
              <p data-testid="vocab-unavailable" className="mb-2 text-xs">
                Rule vocabulary unavailable — levels are read-only until it
                loads. (Rules are validated against the server's closed
                vocabulary; this screen will not guess at it.)
              </p>
            ) : null}
            <ul data-testid="levels-list" className="text-sm">
              {levels.map((l) => (
                <li key={l.id} className="mb-1">
                  <span className="text-tally-title">{l.name}</span> ·{" "}
                  {l.member_count} member{l.member_count === 1 ? "" : "s"} ·{" "}
                  {ruleText(l)}
                  {l.requires_webauthn ? " · WebAuthn for writes" : ""}
                  {l.backdated_cutoff
                    ? ` · no writes before ${l.backdated_cutoff}`
                    : ""}{" "}
                  <button
                    type="button"
                    data-testid={`level-edit:${l.name}`}
                    disabled={!vocab || busy !== null}
                    className="px-1 text-xs underline disabled:opacity-50"
                    onClick={() => setDraft(draftOf(l))}
                  >
                    Edit
                  </button>
                  {l.is_owner ? (
                    <span
                      data-testid={`level-owner-note:${l.name}`}
                      className="text-xs"
                    >
                      {" "}
                      (built-in — only its back-dated cutoff is editable)
                    </span>
                  ) : confirming === `level-delete:${l.id}` ? (
                    <>
                      <button
                        type="button"
                        data-testid={`level-delete-confirm:${l.name}`}
                        disabled={busy !== null}
                        className="ml-1 bg-red-700 px-2 py-0.5 text-xs text-white disabled:opacity-50"
                        onClick={() =>
                          void run(`level-delete:${l.id}`, async () => {
                            await client.deleteSecurityLevel(companyID, l.id);
                            return `Deleted security level "${l.name}".`;
                          })
                        }
                      >
                        Confirm delete
                      </button>
                      <button
                        type="button"
                        className="ml-2 px-2 py-0.5 text-xs underline"
                        onClick={() => setConfirming(null)}
                      >
                        Cancel
                      </button>
                    </>
                  ) : (
                    <button
                      type="button"
                      data-testid={`level-delete:${l.name}`}
                      disabled={busy !== null}
                      className="px-1 text-xs underline disabled:opacity-50"
                      onClick={() => setConfirming(`level-delete:${l.id}`)}
                    >
                      Delete
                    </button>
                  )}
                </li>
              ))}
            </ul>
          </div>

          {draft && vocab ? (
            <div className="mb-4 max-w-3xl bg-tally-panel p-3" data-testid="level-editor">
              <h2 className="mb-2 text-sm text-tally-title">
                {draft.levelID === ""
                  ? "New security level"
                  : `Edit "${draft.name}"`}
              </h2>
              {draft.ownerLocked ? (
                <p data-testid="level-owner-locked" className="mb-2 text-xs">
                  The Owner level is built in and immutable — its name, default
                  access, WebAuthn requirement and rules cannot be changed (the
                  server answers 422). Only the back-dated cutoff is editable,
                  and it binds Owners too.
                </p>
              ) : null}

              <div className="mb-2 flex flex-wrap items-center gap-2 text-sm">
                <label>
                  Name{" "}
                  <input
                    aria-label="Level name"
                    data-testid="level-name"
                    className="bg-tally-field px-1 text-tally-fieldText outline-none disabled:opacity-50"
                    value={draft.name}
                    disabled={draft.ownerLocked}
                    onChange={(e) =>
                      setDraft((d) => (d ? { ...d, name: e.target.value } : d))
                    }
                  />
                </label>
                <label>
                  Default access{" "}
                  <select
                    aria-label="Default access"
                    data-testid="level-default-access"
                    className="bg-tally-field px-1 text-tally-fieldText outline-none disabled:opacity-50"
                    value={draft.defaultAccess}
                    disabled={draft.ownerLocked}
                    onChange={(e) =>
                      setDraft((d) =>
                        d ? { ...d, defaultAccess: e.target.value } : d,
                      )
                    }
                  >
                    {vocab.default_accesses.map((da) => (
                      <option key={da} value={da}>
                        {da}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  <input
                    type="checkbox"
                    aria-label="Requires WebAuthn"
                    data-testid="level-webauthn"
                    checked={draft.requiresWebauthn}
                    disabled={draft.ownerLocked}
                    onChange={(e) =>
                      setDraft((d) =>
                        d ? { ...d, requiresWebauthn: e.target.checked } : d,
                      )
                    }
                  />{" "}
                  WebAuthn step-up for writes
                </label>
                <label>
                  No writes before{" "}
                  <input
                    aria-label="Back-dated cutoff"
                    data-testid="level-cutoff"
                    placeholder="YYYY-MM-DD"
                    className="bg-tally-field px-1 text-tally-fieldText outline-none"
                    value={draft.cutoff}
                    onChange={(e) =>
                      setDraft((d) => (d ? { ...d, cutoff: e.target.value } : d))
                    }
                  />
                </label>
              </div>

              <h3 className="mb-1 text-xs uppercase text-tally-title">
                Rules (evaluated by the server: disallow beats allow at equal
                specificity; exact beats wildcard)
              </h3>
              <ul data-testid="level-rules" className="mb-2 text-sm">
                {draft.rules.map((r, idx) => (
                  <li key={idx} className="mb-1 flex flex-wrap items-center gap-1">
                    <select
                      aria-label={`Rule ${idx + 1} effect`}
                      data-testid={`rule-effect:${idx}`}
                      className="bg-tally-field px-1 text-tally-fieldText outline-none disabled:opacity-50"
                      value={r.effect}
                      disabled={draft.ownerLocked}
                      onChange={(e) =>
                        setDraft((d) =>
                          d
                            ? {
                                ...d,
                                rules: d.rules.map((x, i) =>
                                  i === idx ? { ...x, effect: e.target.value } : x,
                                ),
                              }
                            : d,
                        )
                      }
                    >
                      {vocab.effects.map((v) => (
                        <option key={v} value={v}>
                          {v}
                        </option>
                      ))}
                    </select>
                    <select
                      aria-label={`Rule ${idx + 1} action`}
                      data-testid={`rule-action:${idx}`}
                      className="bg-tally-field px-1 text-tally-fieldText outline-none disabled:opacity-50"
                      value={r.action}
                      disabled={draft.ownerLocked}
                      onChange={(e) =>
                        setDraft((d) =>
                          d
                            ? {
                                ...d,
                                rules: d.rules.map((x, i) =>
                                  i === idx ? { ...x, action: e.target.value } : x,
                                ),
                              }
                            : d,
                        )
                      }
                    >
                      {vocab.actions.map((v) => (
                        <option key={v} value={v}>
                          {v}
                        </option>
                      ))}
                    </select>
                    <select
                      aria-label={`Rule ${idx + 1} resource`}
                      data-testid={`rule-resource:${idx}`}
                      className="bg-tally-field px-1 text-tally-fieldText outline-none disabled:opacity-50"
                      value={r.resource}
                      disabled={draft.ownerLocked}
                      onChange={(e) =>
                        setDraft((d) =>
                          d
                            ? {
                                ...d,
                                rules: d.rules.map((x, i) =>
                                  i === idx
                                    ? { ...x, resource: e.target.value }
                                    : x,
                                ),
                              }
                            : d,
                        )
                      }
                    >
                      {vocab.resources.map((v) => (
                        <option key={v} value={v}>
                          {v}
                        </option>
                      ))}
                    </select>
                    <button
                      type="button"
                      data-testid={`rule-remove:${idx}`}
                      disabled={draft.ownerLocked}
                      className="px-1 text-xs underline disabled:opacity-50"
                      onClick={() =>
                        setDraft((d) =>
                          d
                            ? { ...d, rules: d.rules.filter((_, i) => i !== idx) }
                            : d,
                        )
                      }
                    >
                      remove
                    </button>
                  </li>
                ))}
              </ul>
              <button
                type="button"
                data-testid="rule-add"
                disabled={draft.ownerLocked}
                className="mb-2 px-2 py-0.5 text-xs underline disabled:opacity-50"
                onClick={() =>
                  setDraft((d) =>
                    d
                      ? {
                          ...d,
                          rules: [
                            ...d.rules,
                            {
                              effect: vocab.effects[0] ?? "allow",
                              action: "display",
                              resource: "report",
                            },
                          ],
                        }
                      : d,
                  )
                }
              >
                + Add rule
              </button>

              <div className="flex items-center gap-2">
                <button
                  type="button"
                  data-testid="level-save"
                  disabled={!canSaveDraft || busy !== null}
                  className="bg-tally-active px-2 py-1 text-xs text-white disabled:opacity-50"
                  onClick={() => void saveDraft()}
                >
                  {busy?.startsWith("level-") ? "Saving…" : "Save level"}
                </button>
                <button
                  type="button"
                  data-testid="level-cancel"
                  className="px-2 py-1 text-xs underline"
                  onClick={() => setDraft(null)}
                >
                  Cancel
                </button>
              </div>
            </div>
          ) : null}
        </>
      ) : null}
    </section>
  );
}
