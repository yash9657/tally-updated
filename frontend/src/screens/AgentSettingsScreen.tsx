import { useCallback, useEffect, useState } from "react";

import {
  ApiError,
  formatPaise,
  type AgentAutonomyResponse,
  type AgentVerifyAnchorsResponse,
  type LedgerListItem,
} from "../api";
import { useClient, useCompanyID } from "../app/CompanyContext";

/**
 * AGENT SETTINGS (W2 breadth, D121 "Wave 2 breadth") — the three things about
 * the agent that are an OWNER's to decide, on one screen:
 *
 *  1. THE AUTONOMY TABLE. Which agent commits may happen without a human, per
 *     action and amount band (L0 may not propose · L1 every commit waits · L2/L3
 *     commit within the band on a clean context). No row = L1.
 *  2. BLOCKED LEDGERS. Every ledger the agent created is born blocked: nothing
 *     can post to it until a person has looked at it — its bank account above
 *     all, which is what a forged "our bank details changed" letter changes — and
 *     released it here.
 *  3. THE AGENT'S RECORD. The intent log's anchors, recomputed on demand: a
 *     truncated or edited log fails here even when each run's own chain looks
 *     intact.
 *
 * Reached from `Gateway of Tally > Agent Settings` and Alt+G.
 *
 * ⛔ THE SERVER DECIDES WHO MAY, NOT THIS SCREEN. Reading or changing autonomy is
 * refused to anyone but an Owner (403, rendered as the server's sentence), and a
 * change ALWAYS needs a passkey step-up — the transport asks for one when the
 * server answers STEPUP_REQUIRED. Unblocking is the bare token `master-unblock`,
 * which Data Entry never holds. The screen offers the controls and shows the
 * refusal; it never hides a control to imply a permission it cannot check.
 *
 * ⛔ AND IT CANNOT RAISE THE AGENT PAST A HUMAN THAT POLICY REQUIRES. Untrusted
 * input in a run, a master create, an act toward a government system and the
 * run's own delegated ceiling all force approval at every level; a level here
 * only decides the commits that are left. The note under the table says so, in
 * those words, because "L3" reads like "no human, ever" and it is not.
 */
export function AgentSettingsScreen(): JSX.Element {
  const client = useClient();
  const companyID = useCompanyID();

  const [autonomy, setAutonomy] = useState<AgentAutonomyResponse | null>(null);
  const [autonomyError, setAutonomyError] = useState<string | null>(null);
  const [key, setKey] = useState("");
  const [level, setLevel] = useState(1);
  const [band, setBand] = useState("");
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [blocked, setBlocked] = useState<LedgerListItem[] | null>(null);
  const [anchors, setAnchors] = useState<AgentVerifyAnchorsResponse | null>(null);

  const errText = (e: unknown) =>
    e instanceof ApiError ? `${e.status}: ${e.message}` : e instanceof Error ? e.message : String(e);

  const loadAutonomy = useCallback(async () => {
    setAutonomyError(null);
    try {
      const res = await client.listAgentAutonomy(companyID);
      setAutonomy(res);
      setKey((k) => k || res.keys[0] || "");
    } catch (e) {
      setAutonomy(null);
      setAutonomyError(
        e instanceof ApiError && e.status === 403
          ? `Only an Owner may read or change the agent's autonomy (${e.message}).`
          : `The autonomy table could not be read: ${errText(e)}`,
      );
    }
  }, [client, companyID]);

  const loadBlocked = useCallback(async () => {
    try {
      const res = await client.listLedgers(companyID);
      setBlocked(res.ledgers.filter((l) => l.blocked === true));
    } catch (e) {
      setBlocked([]);
      setNotice(`The ledgers could not be read: ${errText(e)}`);
    }
  }, [client, companyID]);

  useEffect(() => {
    void loadAutonomy();
    void loadBlocked();
  }, [loadAutonomy, loadBlocked]);

  /**
   * Rupees → paise, EXACTLY: a band is a statement about amounts, and a float
   * conversion would move the edge of it. "" = any amount.
   */
  const bandPaise = (s: string): number | undefined | "bad" => {
    const t = s.trim().replace(/,/g, "");
    if (t === "") return undefined;
    const m = /^(\d+)(?:\.(\d{1,2}))?$/.exec(t);
    if (!m) return "bad";
    return Number(m[1]) * 100 + Number((m[2] ?? "").padEnd(2, "0"));
  };

  const onSave = async () => {
    const paise = bandPaise(band);
    if (paise === "bad") {
      setNotice("The amount band must be rupees, e.g. 5000 or 5000.50 — or empty for any amount.");
      return;
    }
    if (!key) {
      setNotice("Choose the action whose autonomy you are setting.");
      return;
    }
    setBusy(true);
    setNotice(null);
    try {
      const res = await client.setAgentAutonomy(companyID, {
        acl_key: key,
        level,
        ...(paise !== undefined ? { amount_max_paise: paise } : {}),
      });
      setAutonomy(res);
      setNotice(`Saved: ${key} is now L${level}${paise !== undefined ? ` up to Rs ${formatPaise(paise)}` : ""}.`);
    } catch (e) {
      setNotice(`Not saved — ${errText(e)}`);
    } finally {
      setBusy(false);
    }
  };

  const onUnblock = async (l: LedgerListItem) => {
    setBusy(true);
    setNotice(null);
    try {
      await client.unblockLedger(companyID, l.id);
      setNotice(`${l.name} is unblocked: vouchers may now post to it.`);
      await loadBlocked();
    } catch (e) {
      setNotice(`${l.name} was not unblocked — ${errText(e)}`);
    } finally {
      setBusy(false);
    }
  };

  const onVerify = async () => {
    setBusy(true);
    try {
      setAnchors(await client.verifyAgentAnchors(companyID));
    } catch (e) {
      setAnchors({ anchors: 0, ok: false, error: errText(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="p-4" data-testid="agent-settings">
      <h1 className="mb-2 text-lg font-bold text-tally-title">Agent Settings</h1>

      <section className="mb-4" data-testid="agent-autonomy">
        <h2 className="text-sm font-bold text-tally-title">Autonomy — which agent commits may happen without a person</h2>
        {autonomyError ? (
          <p className="text-sm" data-testid="agent-autonomy-error">
            {autonomyError}
          </p>
        ) : autonomy === null ? (
          <p className="text-sm">Loading…</p>
        ) : (
          <>
            <table className="mt-1 text-sm" data-testid="agent-autonomy-rows">
              <thead>
                <tr className="text-left text-tally-title">
                  <th className="pr-4">Action</th>
                  <th className="pr-4">Up to</th>
                  <th className="pr-4">For</th>
                  <th>Level</th>
                </tr>
              </thead>
              <tbody>
                {autonomy.rows.length === 0 ? (
                  <tr>
                    <td colSpan={4} data-testid="agent-autonomy-empty">
                      No rows: every action is at the default, L{autonomy.default_level} — every commit waits for a person.
                    </td>
                  </tr>
                ) : (
                  autonomy.rows.map((r) => (
                    <tr key={r.id} data-testid="agent-autonomy-row">
                      <td className="pr-4">{r.acl_key}</td>
                      <td className="pr-4">
                        {r.amount_max_paise !== undefined ? `Rs ${formatPaise(r.amount_max_paise)}` : "any amount"}
                      </td>
                      <td className="pr-4">{r.user_id ? `user ${r.user_id}` : "everyone"}</td>
                      <td>L{r.level}</td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
            <div className="mt-2 flex flex-wrap items-end gap-2 text-sm">
              <label>
                Action{" "}
                <select aria-label="Action" value={key} onChange={(e) => setKey(e.target.value)}>
                  {autonomy.keys.map((k) => (
                    <option key={k} value={k}>
                      {k}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Level{" "}
                <select aria-label="Level" value={level} onChange={(e) => setLevel(Number(e.target.value))}>
                  <option value={0}>L0 — may not propose</option>
                  <option value={1}>L1 — every commit waits (default)</option>
                  <option value={2}>L2 — within the band, clean context</option>
                  <option value={3}>L3 — as L2, no after-notice</option>
                </select>
              </label>
              <label>
                Up to Rs{" "}
                <input
                  aria-label="Amount band"
                  value={band}
                  placeholder="any amount"
                  onChange={(e) => setBand(e.target.value)}
                  className="w-28"
                />
              </label>
              <button type="button" onClick={() => void onSave()} disabled={busy} data-testid="agent-autonomy-save">
                Save (needs a passkey)
              </button>
            </div>
            <p className="mt-1 text-xs" data-testid="agent-autonomy-note">
              A level never removes a person the policy requires: untrusted input in a run, creating a master, an act
              toward a government system and a run&apos;s delegated ceiling force an approval at every level.
            </p>
          </>
        )}
      </section>

      <section className="mb-4" data-testid="agent-blocked">
        <h2 className="text-sm font-bold text-tally-title">Blocked ledgers — created by the agent, not yet released</h2>
        {blocked === null ? (
          <p className="text-sm">Loading…</p>
        ) : blocked.length === 0 ? (
          <p className="text-sm" data-testid="agent-blocked-empty">
            No ledger is blocked.
          </p>
        ) : (
          <ul className="text-sm">
            {blocked.map((l) => (
              <li key={l.id} className="mb-1" data-testid="agent-blocked-row">
                <span className="font-bold">{l.name}</span> ({l.group_name}) — bank a/c{" "}
                {l.bank_account_number || "not recorded"}, IFSC {l.bank_ifsc || "not recorded"}
                {l.blocked_reason ? <span className="block text-xs">{l.blocked_reason}</span> : null}
                <button type="button" onClick={() => void onUnblock(l)} disabled={busy} data-testid="agent-unblock">
                  Unblock — I have checked it
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="mb-4" data-testid="agent-record">
        <h2 className="text-sm font-bold text-tally-title">The agent&apos;s record</h2>
        <button type="button" onClick={() => void onVerify()} disabled={busy} data-testid="agent-verify-anchors">
          Verify the intent log
        </button>
        {anchors ? (
          <p className="text-sm" data-testid="agent-verify-result">
            {anchors.ok
              ? `Intact: ${anchors.anchors} anchor(s) recomputed from the log.`
              : `NOT INTACT: ${anchors.error ?? "the log does not match its anchors"}`}
          </p>
        ) : null}
      </section>

      {notice ? (
        <p className="text-sm" data-testid="agent-settings-notice">
          {notice}
        </p>
      ) : null}
    </div>
  );
}
