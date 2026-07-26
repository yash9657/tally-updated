import { useCallback, useEffect, useState } from "react";
import { ApiError } from "../api";
import type { MembersResponse, SecurityLevelView } from "../api";
import { useClient, useCompanyID } from "../app/CompanyContext";
import type { ScreenComponentProps } from "../app/screenRegistry";

/**
 * Users & Roles — the browser half of the SaaS admin API.
 *
 * Shows, for the active company: its MEMBERS (with the security level each
 * holds), its PENDING INVITES, and its SECURITY LEVELS with their Disallow/Allow
 * rules. Inviting is the one write offered here, because it is the flow a real
 * administrator needs first; changing a level, revoking a member and creating a
 * custom level remain API-only for now (see docs/TENANCY-AND-ACCESS.md §6).
 *
 * TWO THINGS THIS SCREEN MUST NOT HIDE:
 *
 *  1. **An invite is not a membership.** The server records an invite addressed
 *     to a JWT subject and the INVITEE materialises their own membership on
 *     their next `GET /v1/me`. Pending invites are therefore listed separately
 *     and explicitly, or an administrator would think the invite failed.
 *
 *  2. **A 403 here is the product working.** This screen is PDP-gated on the
 *     `security` resource, so a Data Entry operator gets a 403 — and the error
 *     says why, rather than rendering an empty page that looks broken. A 403
 *     STEPUP_REQUIRED on the invite (an Owner without a WebAuthn AMR) says that
 *     too: granting access is a write, and writes need the second factor.
 */

/** Human explanation for the status codes this screen can legitimately receive. */
function explain(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 403 && /STEPUP|step-up/i.test(err.message)) {
      return "WebAuthn step-up required: granting access is a write, so it needs a second factor (403 STEPUP_REQUIRED).";
    }
    if (err.status === 403) {
      return `Not permitted for your security level (403). ${err.message}`;
    }
    if (err.status === 409) {
      return `Already a member of this company (409). Change their level from the API instead.`;
    }
    return `${err.status}: ${err.message}`;
  }
  return err instanceof Error ? err.message : String(err);
}

function ruleText(l: SecurityLevelView): string {
  if (l.is_owner) return "full access (owner)";
  if (l.rules.length === 0) {
    return l.default_access === "full" ? "full access" : "no rules — default deny";
  }
  return l.rules.map((r) => `${r.effect} ${r.action} ${r.resource}`).join(", ");
}

export function MembersScreen(_props: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();

  const [data, setData] = useState<MembersResponse | null>(null);
  const [levels, setLevels] = useState<SecurityLevelView[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [subject, setSubject] = useState("");
  const [levelName, setLevelName] = useState("");
  const [inviting, setInviting] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

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

  const invite = useCallback(async () => {
    const s = subject.trim();
    if (!s || !levelName) return;
    setInviting(true);
    setError(null);
    setNotice(null);
    try {
      await client.inviteMember(companyID, {
        subject: s,
        security_level_name: levelName,
      });
      setSubject("");
      setNotice(
        `Invited ${s} as ${levelName}. They become a member when they next sign in — until then this is a PENDING invite, not access.`,
      );
      await refresh();
    } catch (e) {
      setError(explain(e));
    } finally {
      setInviting(false);
    }
  }, [subject, levelName, client, companyID, refresh]);

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="mb-2 text-lg font-bold text-tally-title">Users &amp; Roles</h1>
      <p className="mb-3 text-sm">
        Who may open this company, and what they may do. Esc to go back.
      </p>

      {error ? (
        <p data-testid="members-error" className="mb-2 max-w-2xl text-sm text-red-400">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p data-testid="members-notice" className="mb-2 max-w-2xl text-sm text-tally-title">
          {notice}
        </p>
      ) : null}

      {loading ? (
        <p data-testid="members-loading" className="text-sm">
          Loading users…
        </p>
      ) : data ? (
        <>
          <div className="mb-4 max-w-2xl bg-tally-panel p-3">
            <h2 className="mb-1 text-sm text-tally-title">Members</h2>
            <table className="w-full text-sm" data-testid="members-table">
              <thead>
                <tr className="text-left text-xs uppercase text-tally-title">
                  <th className="pr-4">Subject</th>
                  <th className="pr-4">Security level</th>
                  <th>Since</th>
                </tr>
              </thead>
              <tbody>
                {data.members.map((m) => (
                  <tr key={m.user_id}>
                    <td className="pr-4">
                      {m.subject}
                      {m.is_self ? " (you)" : ""}
                    </td>
                    <td className="pr-4">{m.security_level_name}</td>
                    <td>{m.created_at.slice(0, 10)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="mb-4 max-w-2xl bg-tally-panel p-3">
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
                    {i.subject} · {i.security_level_name} · pending
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="mb-4 max-w-2xl bg-tally-panel p-3">
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

          <div className="max-w-2xl bg-tally-panel p-3">
            <h2 className="mb-1 text-sm text-tally-title">Security levels</h2>
            <ul data-testid="levels-list" className="text-sm">
              {levels.map((l) => (
                <li key={l.id}>
                  <span className="text-tally-title">{l.name}</span> ·{" "}
                  {l.member_count} member{l.member_count === 1 ? "" : "s"} ·{" "}
                  {ruleText(l)}
                  {l.requires_webauthn ? " · WebAuthn for writes" : ""}
                  {l.backdated_cutoff
                    ? ` · no writes before ${l.backdated_cutoff}`
                    : ""}
                </li>
              ))}
            </ul>
          </div>
        </>
      ) : null}
    </section>
  );
}
