import { useCallback, useEffect, useRef, useState } from "react";

import {
  AGENT_UNCONFIGURED,
  ApiError,
  describeAgentEvent,
  runFinished,
  type AgentApproval,
  type AgentEvent,
  type AgentProvenanceSource,
  type AgentRun,
  type StartAgentRunRequest,
} from "../api";
import { useCompanyContext } from "./CompanyContext";
import { useLoadedProposal } from "./LoadedProposal";
import { screenHandled, useRegisterScreenAction } from "./ScreenActions";
import type { NavSnapshot } from "../keystroke/navMachine";
import { comboFromEvent } from "../keystroke/normalize";
import { openProposalSentinel } from "../keystroke/proposalKey";
import { configFor } from "../screens/vouchers/voucherConfig";
import { proposalTarget } from "../screens/vouchers/agentProposal";
import { ProposalFacts } from "../screens/AgentProposalPanel";

/**
 * THE AGENT SIDE PANEL (W2) — Ctrl+K, from any screen.
 *
 * Start a run, watch its steps as the server's AG-UI-shaped event stream
 * reports them, cancel it, and open a proposal it is waiting on. Three rules
 * shape everything here:
 *
 *  1. ⛔ IT NEVER RENDERS A VOUCHER FORM. A proposal the panel is waiting on is
 *     opened in the SAME native F4/F5/F6/F7 screen an operator keys by hand,
 *     prefilled (app/LoadedProposal.tsx), so Ctrl+A remains the accept key and
 *     every validator on that form applies to the agent's work. Invoice-class
 *     proposals, which the native GST form cannot restate, are decided in the
 *     Approval Inbox from the read-only diff — the panel says so and does not
 *     improvise a form for them.
 *
 *  2. ⛔ WHILE IT HAS FOCUS, IT OWNS THE KEYBOARD. The shell listens on the
 *     DOCUMENT, so an instruction typed here would otherwise drive the screen
 *     underneath: Enter drills the Gateway, and Ctrl+A POSTS the voucher behind
 *     the panel. Every key but Ctrl+K is stopped at the panel's edge; Esc and
 *     Ctrl+K close it and hand the keyboard back.
 *
 *  3. IT SHOWS WHAT THE SERVER SENT AND NOTHING IT DID NOT. The event stream
 *     carries ids, verdicts, paise and HASHES of text — never a prompt (D121
 *     §8.2) — and `describeAgentEvent` renders only those fields. A run's final
 *     summary is its `outcome.final`, shown as the agent's words and not as a
 *     figure the books vouch for (number provenance is W4's, and OWED).
 *
 * ⚠️ WITH NO MODEL CONFIGURED (the shipped default) a start answers 503
 * AGENT_UNCONFIGURED, and the panel says exactly that. Runs started elsewhere
 * still list, and their proposals can still be decided.
 */

/** Poll interval for a live run's events (D121 §8.2 suggests ~1 s). */
let pollMs = 1000;

/** Override the event-poll interval (tests only). */
export function _setAgentPanelPollMs(ms: number): void {
  pollMs = ms;
}

/**
 * The eight voucher creates the agent can preview and commit today (D121 §5,
 * agent.previewable). Offered as the DELEGATION checklist, unticked: a run with
 * nothing delegated can read and propose nothing it could commit, which is the
 * safe default — a person ticks what they are handing over, and "*" is not a
 * thing the server accepts.
 */
const DELEGABLE = [
  "create payment",
  "create receipt",
  "create contra",
  "create journal",
  "create sales",
  "create purchase",
  "create credit-note",
  "create debit-note",
] as const;

const INPUT_SOURCES: readonly Exclude<AgentProvenanceSource, "user" | "books">[] = [
  "bank",
  "document",
  "whatsapp",
  "email",
  "tally_xml",
];

/** "25000" / "25000.5" / "25000.50" → paise, exactly; anything else → null. */
function rupeesToPaise(s: string): number | null | "invalid" {
  const t = s.trim().replace(/,/g, "");
  if (t === "") return null;
  const m = /^(\d+)(?:\.(\d{1,2}))?$/.exec(t);
  if (!m) return "invalid";
  return Number(m[1]) * 100 + Number((m[2] ?? "").padEnd(2, "0"));
}

function errorText(err: unknown): string {
  if (err instanceof ApiError) {
    const code = (err.body as { code?: string } | undefined)?.code;
    if (err.status === 503 && code === AGENT_UNCONFIGURED) {
      return "No agent model is configured on this server (LLM_PROVIDER is unset), so no run can start. Existing runs and their proposals are still shown.";
    }
    if (err.status === 403) {
      return `Refused (403): ${err.message}. Your security level needs the agent-run authority.`;
    }
    return `${err.status}: ${err.message}`;
  }
  return err instanceof Error ? err.message : String(err);
}

export function AgentPanel({
  snapshot,
  onSelect,
  onClose,
}: {
  snapshot: NavSnapshot;
  onSelect: (item: string) => void;
  /** Hand the keyboard back to the application root. */
  onClose: () => void;
}) {
  const { company, client } = useCompanyContext();
  const { setLoaded: setLoadedProposal } = useLoadedProposal();
  const [open, setOpen] = useState(false);
  const panelRef = useRef<HTMLElement>(null);
  const firstFieldRef = useRef<HTMLTextAreaElement>(null);

  const [runs, setRuns] = useState<AgentRun[] | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<AgentRun | null>(null);
  const [events, setEvents] = useState<AgentEvent[]>([]);
  const [message, setMessage] = useState<string | null>(null);

  const [instruction, setInstruction] = useState("");
  const [inputText, setInputText] = useState("");
  const [inputSource, setInputSource] =
    useState<(typeof INPUT_SOURCES)[number]>("bank");
  const [delegate, setDelegate] = useState<Set<string>>(new Set());
  const [maxRupees, setMaxRupees] = useState("");
  const [starting, setStarting] = useState(false);

  const companyID = company?.id ?? null;

  const close = useCallback(() => {
    setOpen(false);
    onClose();
  }, [onClose]);

  // Ctrl+K. ⚠️ REGISTERED BY THE SHELL'S PANEL, NOT A SCREEN — see the
  // ScreenActionKind. screenHandled: the machine has no frame for this.
  useRegisterScreenAction("agentPanel", () => {
    if (open) {
      close();
      return screenHandled("Agent panel closed.");
    }
    setOpen(true);
    return screenHandled("Agent panel open — Esc or Ctrl+K closes it.");
  });

  useEffect(() => {
    if (open) firstFieldRef.current?.focus({ preventScroll: true });
  }, [open]);

  const loadRuns = useCallback(async () => {
    if (!companyID) return;
    try {
      const res = await client.listAgentRuns(companyID, 20);
      setRuns(res.runs ?? []);
      setSelected((cur) => cur ?? res.runs?.[0]?.id ?? null);
    } catch (e) {
      setRuns([]);
      setMessage(errorText(e));
    }
  }, [client, companyID]);

  useEffect(() => {
    if (open) void loadRuns();
  }, [open, loadRuns]);

  const loadDetail = useCallback(
    async (runID: string) => {
      if (!companyID) return;
      try {
        const res = await client.getAgentRun(companyID, runID);
        setDetail(res.run);
      } catch (e) {
        setMessage(errorText(e));
      }
    },
    [client, companyID],
  );

  // THE EVENT POLL. From `after = 0` for a newly selected run, then from the
  // server's `next_after`; stops when the server says the run is finished. A
  // decision or a commit changes the run's approvals, so those kinds re-read
  // the run itself.
  useEffect(() => {
    if (!open || !selected || !companyID) return;
    let cancelled = false;
    let after = 0;
    let timer: number | undefined;
    setEvents([]);
    void loadDetail(selected);
    const tick = async () => {
      try {
        const res = await client.agentRunEvents(companyID, selected, after);
        if (cancelled) return;
        after = res.next_after;
        if (res.events.length > 0) {
          setEvents((prev) => [...prev, ...res.events]);
          if (
            res.events.some((e) =>
              ["approval_requested", "approval_decided", "commit", "commit_refused", "run_finished", "run_failed"].includes(e.kind),
            )
          ) {
            void loadDetail(selected);
          }
        }
        if (!res.finished) timer = window.setTimeout(() => void tick(), pollMs);
      } catch (e) {
        if (!cancelled) setMessage(errorText(e));
      }
    };
    void tick();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [open, selected, companyID, client, loadDetail]);

  const start = useCallback(async () => {
    if (!companyID) return;
    const text = instruction.trim();
    if (!text) {
      setMessage("Type what the agent should do first.");
      return;
    }
    const max = rupeesToPaise(maxRupees);
    if (max === "invalid") {
      setMessage("The maximum amount must be rupees, e.g. 25000 or 25000.50.");
      return;
    }
    const req: StartAgentRunRequest = {
      instruction: text,
      channel: "panel",
      delegate: DELEGABLE.filter((k) => delegate.has(k)),
      ...(inputText.trim()
        ? { inputs: [{ label: `pasted ${inputSource}`, provenance: inputSource, text: inputText }] }
        : {}),
      ...(max !== null ? { max_amount_paise: max } : {}),
    };
    setStarting(true);
    setMessage(null);
    try {
      const res = await client.startAgentRun(companyID, req);
      setInstruction("");
      setInputText("");
      setSelected(res.run.id);
      setMessage("Run started. Its steps appear below as the server records them.");
      await loadRuns();
    } catch (e) {
      setMessage(errorText(e));
    } finally {
      setStarting(false);
    }
  }, [client, companyID, instruction, maxRupees, delegate, inputText, inputSource, loadRuns]);

  const cancelRun = useCallback(async () => {
    if (!companyID || !selected) return;
    try {
      await client.cancelAgentRun(companyID, selected);
      setMessage("Run cancelled — nothing it parked will ever commit.");
      await loadRuns();
      await loadDetail(selected);
    } catch (e) {
      setMessage(errorText(e));
    }
  }, [client, companyID, selected, loadRuns, loadDetail]);

  // W2 breadth: recompute the selected run's intent chain on the server — the
  // record of what was asked, shown and decided, verified link by link.
  const verifyRun = useCallback(async () => {
    if (!companyID || !selected) return;
    try {
      const v = await client.verifyAgentRun(companyID, selected);
      setMessage(
        v.ok
          ? `The run's record is intact: ${v.events} event(s) re-hashed and chained.`
          : `The run's record does NOT verify: ${v.error ?? "a link is broken"}.`,
      );
    } catch (e) {
      setMessage(errorText(e));
    }
  }, [client, companyID, selected]);

  const openProposal = useCallback(
    (a: AgentApproval) => {
      const target = proposalTarget(a);
      if (!target.ok) {
        setMessage(target.reason);
        return;
      }
      // ⛔ NOT OVER A FORM BEING KEYED — the machine refuses the same thing, and
      // saying why here is what makes that refusal something other than a
      // button that does nothing.
      if (
        snapshot.screen === "voucher" ||
        snapshot.screen === "master-create" ||
        snapshot.screen === "goto-overlay"
      ) {
        setMessage(
          "Leave the form you are keying first (Esc) — opening a proposal over it would replace what you have typed.",
        );
        return;
      }
      setLoadedProposal({ approval: a, key: target.key });
      onSelect(openProposalSentinel(target.key));
      close();
    },
    [snapshot.screen, setLoadedProposal, onSelect, close],
  );

  if (!open) return null;

  const onPanelKeyDown = (ev: React.KeyboardEvent) => {
    const combo = comboFromEvent({
      key: ev.key,
      code: ev.code,
      ctrlKey: ev.ctrlKey,
      altKey: ev.altKey,
      shiftKey: ev.shiftKey,
      metaKey: ev.metaKey,
    });
    if (combo === "Ctrl+K") return; // the shell toggles the panel
    ev.stopPropagation();
    if (combo === "Esc") {
      ev.preventDefault();
      close();
    }
  };

  const pending = (detail?.approvals ?? []).filter((a) => a.state === "pending");
  const run = detail && detail.id === selected ? detail : null;

  return (
    <aside
      ref={panelRef}
      role="complementary"
      aria-label="Agent panel"
      data-testid="agent-panel"
      onKeyDown={onPanelKeyDown}
      className="fixed right-0 top-0 z-40 h-full w-[28rem] overflow-auto border-l border-tally-active bg-tally-bg p-3 font-mono text-sm text-tally-field"
    >
      <h2 className="mb-1 text-base font-bold text-tally-title">Agent</h2>
      <p className="mb-2 text-xs">
        Esc or Ctrl+K closes · Tab moves · the agent proposes, a person decides.
        Nothing it does is posted without an approval.
      </p>
      {message ? (
        <p className="mb-2 text-tally-active" role="status" data-testid="agent-panel-message">
          {message}
        </p>
      ) : null}
      {!companyID ? (
        <p data-testid="agent-panel-no-company">Open a company first (F3).</p>
      ) : (
        <>
          <label className="block">
            What should the agent do?
            <textarea
              ref={firstFieldRef}
              aria-label="Instruction"
              data-testid="agent-instruction"
              className="mt-1 h-16 w-full bg-tally-field px-1 text-tally-fieldText outline-none"
              value={instruction}
              onChange={(e) => setInstruction(e.target.value)}
            />
          </label>
          <label className="mt-1 block">
            Untrusted input (optional) — pasted from
            <select
              aria-label="Input source"
              className="ml-1 bg-tally-field text-tally-fieldText"
              value={inputSource}
              onChange={(e) => setInputSource(e.target.value as (typeof INPUT_SOURCES)[number])}
            >
              {INPUT_SOURCES.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
            <textarea
              aria-label="Untrusted input"
              data-testid="agent-input"
              className="mt-1 h-12 w-full bg-tally-field px-1 text-tally-fieldText outline-none"
              value={inputText}
              onChange={(e) => setInputText(e.target.value)}
            />
          </label>
          <fieldset className="mt-1">
            <legend>Hand over (nothing is ticked by default)</legend>
            {DELEGABLE.map((k) => (
              <label key={k} className="mr-3 inline-block">
                <input
                  type="checkbox"
                  aria-label={k}
                  checked={delegate.has(k)}
                  onChange={(e) =>
                    setDelegate((prev) => {
                      const next = new Set(prev);
                      if (e.target.checked) next.add(k);
                      else next.delete(k);
                      return next;
                    })
                  }
                />{" "}
                {k.replace("create ", "")}
              </label>
            ))}
          </fieldset>
          <label className="mt-1 block">
            Ask me for anything above ₹
            <input
              aria-label="Maximum amount"
              className="ml-1 w-28 bg-tally-field px-1 text-tally-fieldText outline-none"
              value={maxRupees}
              onChange={(e) => setMaxRupees(e.target.value)}
            />
          </label>
          <button
            type="button"
            data-testid="agent-start"
            disabled={starting}
            className="mt-2 bg-tally-active px-2 py-1 text-xs text-white disabled:opacity-50"
            onClick={() => void start()}
          >
            {starting ? "Starting…" : "Start run"}
          </button>

          <h3 className="mt-3 text-tally-title">Runs</h3>
          {runs === null ? (
            <p>Loading…</p>
          ) : runs.length === 0 ? (
            <p data-testid="agent-runs-empty">No runs yet.</p>
          ) : (
            <ul role="listbox" aria-label="Runs" data-testid="agent-runs">
              {runs.map((r) => (
                <li key={r.id} role="option" aria-selected={r.id === selected}>
                  <button
                    type="button"
                    className={r.id === selected ? "text-tally-active" : ""}
                    data-testid="agent-run-row"
                    onClick={() => setSelected(r.id)}
                  >
                    [{r.status}] {r.instruction.slice(0, 60)}
                  </button>
                </li>
              ))}
            </ul>
          )}

          {run ? (
            <div className="mt-3" data-testid="agent-run-detail">
              <p>
                <span className="text-tally-title">Status:</span>{" "}
                <span data-testid="agent-run-status">{run.status}</span>
                {!runFinished(run.status) ? (
                  <button
                    type="button"
                    data-testid="agent-cancel"
                    className="ml-2 underline"
                    onClick={() => void cancelRun()}
                  >
                    Cancel run
                  </button>
                ) : null}
                <button
                  type="button"
                  data-testid="agent-verify-run"
                  className="ml-2 underline"
                  onClick={() => void verifyRun()}
                >
                  Verify record
                </button>
              </p>
              {run.outcome?.final ? (
                <p data-testid="agent-run-final">
                  <span className="text-tally-title">Agent's summary (its words, not figures the books vouch for):</span>{" "}
                  {run.outcome.final}
                </p>
              ) : null}
              {run.outcome?.details ? (
                <details data-testid="agent-run-details" className="mt-2 whitespace-pre-wrap">
                  <summary>Full explanation or draft</summary>
                  <p>{run.outcome.details}</p>
                </details>
              ) : null}
              <h4 className="mt-2 text-tally-title">Steps</h4>
              <ol className="ml-4 list-decimal" data-testid="agent-steps">
                {events.map((e) => (
                  <li key={e.seq} data-testid="agent-step" data-kind={e.kind}>
                    {describeAgentEvent(e)}
                  </li>
                ))}
              </ol>
              {pending.map((a) => {
                const t = proposalTarget(a);
                return (
                  <div
                    key={a.id}
                    className="mt-2 border border-tally-active p-2"
                    data-testid="agent-approval-card"
                  >
                    <p className="font-bold text-tally-title">Waiting for you</p>
                    <ProposalFacts approval={a} />
                    {t.ok ? (
                      <button
                        type="button"
                        data-testid="agent-open-proposal"
                        className="mt-1 bg-tally-active px-2 py-1 text-xs text-white"
                        onClick={() => openProposal(a)}
                      >
                        Open in {configFor(t.key.voucherType).title} to review — Ctrl+A there approves
                      </button>
                    ) : (
                      <p className="mt-1" data-testid="agent-card-inbox-only">
                        {t.reason} (Gateway › Approval Inbox, or Alt+G.)
                      </p>
                    )}
                  </div>
                );
              })}
            </div>
          ) : null}
        </>
      )}
    </aside>
  );
}
