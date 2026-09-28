import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  ApiError,
  approvalEffect,
  formatPaise,
  type AgentApproval,
  type AgentDecisionRequest,
} from "../api";
import { useClient, useCompanyID } from "../app/CompanyContext";
import { useLoadedProposal } from "../app/LoadedProposal";
import {
  screenHandled,
  screenRefusal,
  useRegisterScreenAction,
} from "../app/ScreenActions";
import type { ScreenComponentProps } from "../app/screenRegistry";
import { proposalRowKey } from "../keystroke/proposalKey";
import { configFor } from "./vouchers/voucherConfig";
import { proposalTarget } from "./vouchers/agentProposal";
import { assertStillAsShown, decisionErrorText, ProposalFacts } from "./AgentProposalPanel";

/**
 * THE APPROVAL INBOX (W2, D121 §8.3) — every agent proposal waiting for a human.
 *
 * Reached from `Gateway of Tally > Approval Inbox`, from Alt+G Go To, and from
 * the Ctrl+K side panel's approval cards. Keys, all from the keyboard:
 *
 *   ↑ / ↓    highlight a proposal; its reasons, provenance and LEDGER-EFFECT
 *            DIFF render below the list as it is highlighted
 *   Enter    open it in its NATIVE voucher screen (F4/F5/F6/F7), prefilled —
 *            edit there, and Ctrl+A THERE approves your version
 *   Ctrl+A   approve it exactly as previewed
 *   Alt+X    reject it (the Note is optional)
 *   Alt+B    ask the agent back (the Note is the question, and is required)
 *
 * ⛔⛔ THE DIFF IS MANDATORY, AT EVERY AUTONOMY LEVEL. PLAN-AGENTIC asks for it
 * "above L1 thresholds"; every proposal that reaches this screen is one the
 * policy refused to commit without a human, whatever the level, so this screen
 * does not try to tell the cases apart — Ctrl+A approves only a proposal whose
 * ledger effect is on screen, and refuses (before any request) one the server
 * sent no preview for. "Approve what you cannot see" is not a key this product
 * has.
 *
 * ⛔ NOTHING HERE POSTS A VOUCHER. A decision is recorded; the agent's poller
 * commits an approved proposal afterwards (≤ 2 s on a running server), replaying
 * the preview under an idempotency key. The screen says "approved", never
 * "saved" or "posted", and names Ctrl+K as where the commit shows up.
 *
 * ⚠️ WHAT THE SERVER STILL DECIDES. The approver must be allowed to take the
 * action themselves (403 otherwise); a decision after the 72-hour deadline is
 * recorded as EXPIRED (409); a second decision is refused (409). All three are
 * shown as the sentences in `decisionErrorText`, and the list is re-read after
 * each, because the row the operator was looking at is no longer pending.
 */
export function ApprovalInboxScreen({ onSelect }: ScreenComponentProps) {
  const client = useClient();
  const companyID = useCompanyID();
  const { setLoaded: setLoadedProposal } = useLoadedProposal();

  const [approvals, setApprovals] = useState<AgentApproval[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [active, setActive] = useState(0);
  const [note, setNote] = useState("");
  const [notice, setNotice] = useState<string | null>(null);
  const inFlight = useRef(false);
  const listRef = useRef<HTMLUListElement>(null);

  const load = useCallback(async () => {
    setListError(null);
    try {
      const res = await client.listAgentApprovals(companyID, { state: "pending" });
      setApprovals(res.approvals ?? []);
    } catch (e) {
      setApprovals([]);
      setListError(
        e instanceof ApiError && e.status === 403
          ? "Your security level may not see agent proposals (display agent-approval is refused). An Owner can grant it under Users & Roles."
          : `The approval inbox could not be read: ${e instanceof Error ? e.message : String(e)}`,
      );
    }
  }, [client, companyID]);

  useEffect(() => {
    void load();
  }, [load]);

  const rows = approvals ?? [];
  const idx = rows.length === 0 ? -1 : Math.min(active, rows.length - 1);
  const current = idx >= 0 ? (rows[idx] ?? null) : null;
  const target = useMemo(() => (current ? proposalTarget(current) : null), [current]);

  // PUBLISH THE HIGHLIGHT. The machine gets the proposal key (so Enter drills
  // into the native voucher) and the LoadedProposal slot gets the approval (so
  // that voucher can prefill) — in that order of importance, and BOTH before the
  // operator can press Enter, because both happen on highlight rather than on the
  // key. A proposal that cannot be opened publishes a plain `approval:` key,
  // which the machine's Enter arm refuses to drill (see navMachine).
  const currentID = current?.id ?? null;
  useEffect(() => {
    if (!current || !target) return;
    if (target.ok) {
      setLoadedProposal({ approval: current, key: target.key });
      onSelect(proposalRowKey(target.key));
    } else {
      onSelect(`approval:${current.id}`);
    }
    // `current` is keyed by id: re-publishing on every render would be a SELECT
    // per render, and the approval object is replaced on every reload.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentID, target?.ok]);

  const decide = useCallback(
    (req: AgentDecisionRequest, done: string) => {
      if (!current) return screenRefusal("No proposal is highlighted.");
      if (inFlight.current) return screenRefusal("A decision is already being sent.");
      inFlight.current = true;
      const approval = current;
      // ⛔ AN APPROVE RE-READS THE APPROVAL FIRST, AND REFUSES IF WHAT IS ON
      // SCREEN IS NOT WHAT THE SERVER HOLDS. The list was read when the screen
      // opened; the operator may have looked at it for an hour. A proposal that
      // was decided elsewhere, or whose preview is not the one drawn here, must
      // not be approved on the strength of a diff that is no longer its diff.
      const fresh: Promise<void> =
        req.decision === "approve"
          ? assertStillAsShown(client, companyID, approval)
          : Promise.resolve();
      void fresh
        .then(() => client.decideAgentApproval(companyID, approval.id, req))
        .then(() => {
          setNotice(done);
          setNote("");
          setLoadedProposal(null);
        })
        .catch((err: unknown) => setNotice(`Error: ${decisionErrorText(err)}`))
        .finally(() => {
          inFlight.current = false;
          void load();
        });
      return screenHandled("Sending the decision…");
    },
    [client, companyID, current, load, setLoadedProposal],
  );

  // Ctrl+A — APPROVE AS PREVIEWED. Refused before any request when there is no
  // diff to have looked at (see the header: the diff is mandatory).
  useRegisterScreenAction("accept", () => {
    if (!current) return screenRefusal("No proposal is highlighted.");
    if (!approvalEffect(current)) {
      return screenRefusal(
        "This proposal has no preview to show, so it cannot be approved. Reject it (Alt+X) or ask the agent back (Alt+B).",
      );
    }
    return decide(
      { decision: "approve" },
      `Approved as previewed: ${current.acl_key}, ${formatPaise(current.amount_paise)}. The agent commits it on its next poll — nothing is posted by this screen; Ctrl+K shows the commit.`,
    );
  });

  // Alt+X — REJECT. The note is optional here (the server accepts none).
  useRegisterScreenAction("rejectApproval", () => {
    if (!current) return screenRefusal("No proposal is highlighted.");
    const n = note.trim();
    return decide(
      { decision: "reject", ...(n ? { note: n } : {}) },
      `Rejected: ${current.acl_key}. Nothing is posted and the run finishes.`,
    );
  });

  // Alt+B — ASK BACK. The server refuses one with no note (422); refused here
  // first, so the operator is told what to type instead of reading a 422.
  useRegisterScreenAction("askBackApproval", () => {
    if (!current) return screenRefusal("No proposal is highlighted.");
    const n = note.trim();
    if (!n) {
      return screenRefusal(
        "Ask back needs a question: type it in the Note field, then press Alt+B.",
      );
    }
    return decide(
      { decision: "ask_back", note: n },
      "Sent back to the agent with your note. It may propose again — a new row will appear here.",
    );
  });

  const onListKey = (ev: React.KeyboardEvent<HTMLUListElement>) => {
    if (ev.key === "ArrowDown") {
      ev.preventDefault();
      setActive((a) => Math.min(a + 1, Math.max(rows.length - 1, 0)));
    } else if (ev.key === "ArrowUp") {
      ev.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    }
  };

  useEffect(() => {
    listRef.current?.focus({ preventScroll: true });
  }, [approvals === null]);

  return (
    <section className="flex h-full flex-col overflow-auto bg-tally-bg p-4 font-mono text-tally-field">
      <h1 className="mb-1 text-lg font-bold text-tally-title">Approval Inbox</h1>
      <p className="mb-3 text-sm">
        Agent proposals waiting for you. ↑/↓ highlight · Enter opens it in its
        voucher screen to edit · Ctrl+A approves as previewed · Alt+X rejects ·
        Alt+B asks the agent back. Nothing here posts a voucher: an approved
        proposal is committed by the agent afterwards.
      </p>

      {notice ? (
        <p className="mb-2 text-sm text-tally-active" role="status" data-testid="inbox-notice">
          {notice}
        </p>
      ) : null}
      {listError ? (
        <p className="mb-2 text-sm text-red-400" data-testid="inbox-error">
          {listError}
        </p>
      ) : null}

      {approvals === null ? (
        <p data-testid="inbox-loading">Loading proposals…</p>
      ) : rows.length === 0 && !listError ? (
        <p data-testid="inbox-empty">
          No proposal is waiting. Start a run from the agent panel (Ctrl+K).
        </p>
      ) : (
        <ul
          ref={listRef}
          role="listbox"
          aria-label="Pending proposals"
          tabIndex={0}
          onKeyDown={onListKey}
          className="mb-3 max-w-3xl bg-tally-panel p-2 outline-none"
          data-testid="inbox-list"
        >
          {rows.map((a, i) => (
            <li
              key={a.id}
              role="option"
              aria-selected={i === idx}
              data-testid="inbox-row"
              className={i === idx ? "bg-tally-active text-white" : ""}
              onClick={() => setActive(i)}
            >
              {a.acl_key} · {formatPaise(a.amount_paise)} · {a.instruction.slice(0, 80)}
            </li>
          ))}
        </ul>
      )}

      {current ? (
        <div className="max-w-3xl bg-tally-panel p-3" data-testid="inbox-detail">
          <ProposalFacts approval={current} />
          <p className="mt-2 text-sm" data-testid="inbox-open-hint">
            {target?.ok
              ? `Enter: open in ${configFor(target.key.voucherType).title} (${target.key.entryMode} entry) to edit — Ctrl+A there approves your version.`
              : target?.reason}
          </p>
          <label className="mt-2 block text-sm">
            Note (the question for Alt+B; optional for Alt+X)
            <input
              aria-label="Note"
              data-testid="inbox-note"
              className="ml-2 w-96 bg-tally-field px-1 text-tally-fieldText outline-none"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              onKeyDown={(e) => {
                // ⛔ Enter IN THE NOTE DOES NOT DRILL. The shell listens on the
                // document, so without this an operator finishing a sentence
                // would open the proposal's voucher mid-thought.
                if (e.key === "Enter") e.stopPropagation();
              }}
            />
          </label>
        </div>
      ) : null}
    </section>
  );
}
