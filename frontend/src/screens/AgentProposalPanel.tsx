import {
  ApiError,
  approvalEffect,
  formatPaise,
  type AgentApproval,
  type AgentEffect,
  type CoredClient,
} from "../api";

/**
 * WHAT AN AGENT PROPOSED, AND WHY A HUMAN IS BEING ASKED (W2) — the read-only
 * block shared by the Approval Inbox, the Ctrl+K side panel and the native
 * voucher screen in proposal mode.
 *
 * ⛔ IT IS NOT A FORM AND MUST NEVER BECOME ONE. PLAN-AGENTIC W2: "the agent
 * never renders its own voucher form". Changing a figure happens in the native
 * F4/F5/F6/F7 screen, where the validators and the master gate an operator
 * already trusts apply; this block only SHOWS.
 *
 * Three things on it are load-bearing, and each has a test:
 *
 *  1. THE LEDGER-EFFECT DIFF is `preview.effect.postings` — what the server's
 *     REAL handler posted inside a transaction it then rolled back — and never
 *     the proposed body. The body is what the model said; the effect is what
 *     the books would say. Where there is no effect, the block says so and the
 *     inbox refuses to approve (the diff is mandatory, at every level).
 *  2. `reasons` VERBATIM. They are the policy's own sentences ("autonomy for
 *     create payment is L1: every commit waits for a human"); paraphrasing them
 *     is how "the amount is above your delegation" turns into "please review".
 *  3. PROVENANCE, LOUDLY. A body value found word for word in an untrusted
 *     input (a bank narration, an invoice line, a WhatsApp forward) is the
 *     shape every injection in the 32-case suite takes, so the field is named
 *     beside its source in the warning colour, above the diff.
 */

const WARN: React.CSSProperties = { color: "#d97706" };

/**
 * Re-read an approval immediately before APPROVING it, and refuse if the server
 * no longer holds what the operator was shown.
 *
 * ⛔ WHY AN APPROVE PAYS A SECOND READ. The inbox list — or the approval the
 * panel stashed for the native voucher — was read when the screen opened, and
 * the operator may have looked at it for an hour. One decided elsewhere, or
 * whose preview is not the one drawn on screen, must not be approved on the
 * strength of a diff that is no longer its diff. Reject and ask-back skip it:
 * neither commits anything, and the server refuses a second decision anyway.
 */
export async function assertStillAsShown(
  client: CoredClient,
  companyID: string,
  shown: AgentApproval,
): Promise<void> {
  const { approval: now } = await client.getAgentApproval(companyID, shown.id);
  if (now.state !== "pending") {
    throw new Error(`this proposal is already ${now.state.replace("_", " ")} — refresh the inbox.`);
  }
  if (JSON.stringify(approvalEffect(now)) !== JSON.stringify(approvalEffect(shown))) {
    throw new Error(
      "the proposal's preview changed since it was shown; nothing was approved — review it again.",
    );
  }
}

/** A decision failure, in words an operator can act on. */
export function decisionErrorText(err: unknown): string {
  if (err instanceof ApiError) {
    const body = (err.body ?? {}) as { code?: string; approval?: AgentApproval };
    if (body.code === "ALREADY_DECIDED") {
      const st = body.approval?.state ?? "decided";
      return `this proposal was already decided (${st.replace("_", " ")}) — refresh the inbox.`;
    }
    if (body.code === "EDIT_REFUSED") {
      return `the server refused YOUR edit, so nothing was approved: ${err.message}. Fix the form and press Ctrl+A again.`;
    }
    if (body.code === "APPROVAL_EXPIRED") {
      return "the 72-hour approval window closed before this decision, and it has been recorded as EXPIRED. Nothing will be committed.";
    }
    if (err.status === 403) return `refused (403): ${err.message}`;
    return `${err.status}: ${err.message}`;
  }
  return err instanceof Error ? err.message : String(err);
}

/** The ledger-effect table: what the server's handler would post. */
export function LedgerEffectTable({ effect }: { effect: AgentEffect }) {
  return (
    <table className="mt-1 w-full max-w-xl text-sm" data-testid="ledger-effect">
      <thead>
        <tr className="text-left text-tally-title">
          <th className="pr-4">Ledger</th>
          <th className="pr-4 text-right">Debit</th>
          <th className="text-right">Credit</th>
        </tr>
      </thead>
      <tbody>
        {effect.postings.map((p, i) => {
          const dr = p.dr_cr.toUpperCase().startsWith("D");
          return (
            <tr key={`${p.ledger_id}-${i}`} data-testid="ledger-effect-row">
              <td className="pr-4">{p.ledger}</td>
              <td className="pr-4 text-right">{dr ? formatPaise(p.amount_paise) : ""}</td>
              <td className="text-right">{dr ? "" : formatPaise(p.amount_paise)}</td>
            </tr>
          );
        })}
        <tr className="border-t border-tally-field font-bold" data-testid="ledger-effect-total">
          <td className="pr-4">Total</td>
          <td className="pr-4 text-right">{formatPaise(effect.debit_paise)}</td>
          <td className="text-right">{formatPaise(effect.credit_paise)}</td>
        </tr>
      </tbody>
    </table>
  );
}

/**
 * THE REST OF THE DIFF (W2 breadth): what an alteration replaces, and what a
 * stock voucher, a master, an order or a bank tick changes — each read back by
 * the server from what its real handler wrote, never from the proposed body.
 *
 * ⛔ A MASTER'S BANK DETAILS ARE DRAWN IN THE WARNING COLOUR. A forged "our
 * bank details have changed" letter is the fraud a master create exists to
 * carry; the approver must read the account number, not skim past it.
 */
export function EffectDetails({ effect }: { effect: AgentEffect }) {
  const loud = new Set(["bank_account_number", "bank_ifsc", "gstin", "pan"]);
  return (
    <div data-testid="effect-details">
      {(effect.replaces ?? []).map((r) => (
        <div key={`${r.voucher_id}-${r.version_no}`} className="mt-1 text-sm" data-testid="effect-replaces">
          <p>
            <span className="text-tally-title">Replaces</span> version {r.version_no} of this voucher (Dr{" "}
            {formatPaise(r.debit_paise)}). The old version is not changed; it stays in the Edit Log and a new
            version supersedes it.
          </p>
          <LedgerEffectTable effect={{ postings: r.postings, debit_paise: r.debit_paise, credit_paise: r.credit_paise, vouchers: 1 }} />
        </div>
      ))}
      {(effect.delta ?? []).length > 0 ? (
        <table className="mt-1 text-sm" data-testid="effect-delta">
          <thead>
            <tr className="text-left text-tally-title">
              <th className="pr-4">Ledger</th>
              <th className="pr-4 text-right">Before</th>
              <th className="pr-4 text-right">After</th>
              <th className="text-right">Change (Dr +)</th>
            </tr>
          </thead>
          <tbody>
            {(effect.delta ?? []).map((d) => (
              <tr key={d.ledger_id} data-testid="effect-delta-row">
                <td className="pr-4">{d.ledger}</td>
                <td className="pr-4 text-right">{formatPaise(d.before_paise)}</td>
                <td className="pr-4 text-right">{formatPaise(d.after_paise)}</td>
                <td className="text-right font-bold">{formatPaise(d.change_paise)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      {(effect.stock ?? []).length > 0 ? (
        <table className="mt-1 text-sm" data-testid="effect-stock">
          <thead>
            <tr className="text-left text-tally-title">
              <th className="pr-4">Item</th>
              <th className="pr-4">Godown</th>
              <th className="pr-4">In/Out</th>
              <th className="pr-4 text-right">Qty</th>
              <th className="pr-4 text-right">Rate</th>
              <th className="text-right">Amount</th>
            </tr>
          </thead>
          <tbody>
            {(effect.stock ?? []).map((m) => (
              <tr key={`${m.voucher_id}-${m.line_no}`} data-testid="effect-stock-row">
                <td className="pr-4">{m.item}</td>
                <td className="pr-4">{m.godown || "—"}</td>
                <td className="pr-4">{m.direction}</td>
                <td className="pr-4 text-right">{m.qty}</td>
                <td className="pr-4 text-right">{m.rate}</td>
                <td className="text-right">{m.amount}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      {(effect.masters ?? []).map((m, i) => (
        <div key={`${m.entity_id}-${i}`} className="mt-1 text-sm" data-testid="effect-master">
          <p>
            <span className="text-tally-title">{m.op === "CREATE" ? "Creates" : "Changes"} {m.entity.replace("_", " ")}</span>
            {m.after?.["blocked"] === true ? " — born BLOCKED: nothing can post to it until a person unblocks it (Agent Settings)" : ""}
          </p>
          <ul className="ml-4">
            {Object.entries(m.after ?? {})
              .filter(([k, v]) => k !== "blocked" && k !== "created_by_run" && v !== "" && v !== null && v !== false)
              .map(([k, v]) => {
                const before = m.before?.[k];
                return (
                  <li key={k} style={loud.has(k) ? WARN : undefined} className={loud.has(k) ? "font-bold" : undefined}>
                    {k.replace(/_/g, " ")}: {before !== undefined && before !== v ? `${String(before)} → ` : ""}
                    {String(v)}
                  </li>
                );
              })}
          </ul>
        </div>
      ))}
      {(effect.orders ?? []).map((o) => (
        <div key={o.order_id} className="mt-1 text-sm" data-testid="effect-order">
          <p>
            <span className="text-tally-title">{o.direction === "SALES" ? "Sales" : "Purchase"} order</span> {o.order_no}{" "}
            dated {o.date}
            {o.closed ? " — CLOSED" : ""}
          </p>
          <ul className="ml-4">
            {(o.lines ?? []).map((l) => (
              <li key={l.line_no}>
                {l.item}: {l.qty} @ {l.rate}
              </li>
            ))}
          </ul>
        </div>
      ))}
      {(effect.bank_dates ?? []).map((b) => (
        <p key={`${b.voucher_id}-${b.ledger_id}-${b.leg_no}`} className="mt-1 text-sm" data-testid="effect-bank-date">
          <span className="text-tally-title">Bank date</span>: {b.before ?? "not reconciled"} →{" "}
          <span className="font-bold">{b.after ?? "not reconciled"}</span>
        </p>
      ))}
    </div>
  );
}

/** The reasons, the provenance warnings and the diff for one approval. */
export function ProposalFacts({ approval }: { approval: AgentApproval }) {
  const effect = approvalEffect(approval);
  const fields = Object.entries(approval.provenance?.fields ?? {});
  const sources = (approval.provenance?.context_sources ?? []).filter(
    (s) => s !== "user" && s !== "books",
  );
  return (
    <div>
      <p className="text-sm">
        <span className="text-tally-title">Asked: </span>
        <span data-testid="proposal-instruction">{approval.instruction}</span>
      </p>
      <p className="text-sm">
        <span className="text-tally-title">Action: </span>
        {approval.acl_key} ({approval.action_id}) · risk {approval.risk} · amount{" "}
        {formatPaise(approval.amount_paise)} · decide by{" "}
        {approval.deadline_at.replace("T", " ").slice(0, 16)}
      </p>
      <div className="mt-1 text-sm" data-testid="proposal-reasons">
        <span className="text-tally-title">Why a human is asked:</span>
        <ul className="ml-4 list-disc">
          {(approval.reasons ?? []).map((r) => (
            <li key={r}>{r}</li>
          ))}
        </ul>
      </div>
      {sources.length > 0 ? (
        <p className="mt-1 text-sm" style={WARN} data-testid="proposal-tainted">
          ⚠ Untrusted input reached this run: {sources.join(", ")}.
        </p>
      ) : null}
      {fields.map(([path, src]) => (
        <p
          key={path}
          className="text-sm font-bold"
          style={WARN}
          data-testid="proposal-provenance-field"
        >
          ⚠ {path} was copied verbatim from {src} input — check it against the source.
        </p>
      ))}
      <div className="mt-2">
        <span className="text-sm text-tally-title">
          Effect on the books (the server's preview, rolled back — nothing is posted yet):
        </span>
        {effect ? (
          <>
            {effect.postings.length > 0 ? <LedgerEffectTable effect={effect} /> : null}
            <EffectDetails effect={effect} />
          </>
        ) : (
          <p className="text-sm" style={WARN} data-testid="ledger-effect-missing">
            The server sent no preview for this proposal. It cannot be approved
            without one.
          </p>
        )}
      </div>
    </div>
  );
}

/**
 * The block VoucherScreen renders above the form in proposal mode.
 *
 * `edited` is a DISPLAY hint (the live form differs from the seeded one). What
 * decides which body is committed is `editedFrom` on the body the builder
 * produced at Ctrl+A — see VoucherScreen's proposal branch.
 */
export function AgentProposalPanel({
  approval,
  edited,
}: {
  approval: AgentApproval;
  edited: boolean;
}) {
  return (
    <div
      className="mb-3 border border-tally-active bg-tally-panel p-3"
      data-testid="agent-proposal"
    >
      <h2 className="mb-1 text-sm font-bold text-tally-title">
        AGENT PROPOSAL — NOT IN THE BOOKS
      </h2>
      <ProposalFacts approval={approval} />
      <p className="mt-2 text-sm" data-testid="proposal-accept-hint">
        {edited
          ? "You have changed this proposal. Ctrl+A approves YOUR version (edit-then-approve); the agent commits it through the same validators."
          : "Ctrl+A approves it exactly as previewed. Change any field first to approve your own version instead."}{" "}
        Esc returns without deciding; reject or ask back from the Approval Inbox.
      </p>
    </div>
  );
}
