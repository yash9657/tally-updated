-- 0094 — agent RUNS, their durable STEPS, the APPROVAL inbox, the human
-- DECISIONS, and the COMMIT idempotency record (W2, D121).
--
-- ===========================================================================
-- WHY A NEW STEP TABLE AND NOT statutory_step
-- ===========================================================================
-- D120 decided the engine (internal/durable, née statutory.Run) and said, in
-- its §7, what the spike did not test: "No RLS, no tenancy. statutory_step
-- carries no RLS by design (0013's header). W2's runs are tenant data." An
-- agent step records a plan, a preview of a voucher, a party name — so it is
-- stored under FORCE RLS like every other tenant row, in agent_run_step, and
-- the engine reaches it through a store that sets the company GUC
-- (internal/agent's runStore). The engine is shared; the table is not.
--
-- ===========================================================================
-- ⛔ A PARKED RUN IS AN UNRECORDED STEP PLUS ROWS IN THESE TABLES
-- ===========================================================================
-- That is D120 §2's whole argument, and it shapes every table here. There is no
-- channel, no timer and no in-process registry: the approval step reads
-- agent_decision and parks (durable.ErrParked, recording nothing) when there is
-- no row. The poller finds work through agent_runs_due() and re-invokes. So a
-- 72-hour approval survives any number of restarts because there is nothing in
-- memory to lose.
--
-- ===========================================================================
-- ⛔ EXPIRY IS TERMINAL BECAUSE IT IS WRITTEN (D120 F2)
-- ===========================================================================
-- The spike measured a late approval committing an expired run, because expiry
-- recorded nothing. Here an expired approval gets an agent_decision row with
-- decision = 'expired' and decided_by NULL, and agent_decision allows ONE row
-- per approval (the UNIQUE below). So the late approve is refused by the
-- database — the second decision cannot be inserted — rather than by a poller
-- that has to remember to stop caring.
--
-- ===========================================================================
-- ⛔ THE COMMIT RECORD COMMITS WITH THE VOUCHER, IN ONE TRANSACTION
-- ===========================================================================
-- agent_commit is inserted inside the SAME transaction the voucher handler
-- opens (platform/db's TxHook.BeforeCommit), keyed by an idempotency key the
-- run derives (run id + step). Two consequences, both deliberate:
--   * a crash after the voucher committed but before the durable step recorded
--     it cannot double-post on resume: the re-dispatch finds the key and
--     answers from this row, and if it raced past that check the UNIQUE below
--     rolls the second voucher back with its own insert;
--   * PLAN-AGENTIC W5's "Bill Inbox duplicate-post defect class ... closed by
--     idempotency keys on commit" has its table.
--
-- Append-only by grant AND trigger for step, approval, decision and commit
-- rows: each is a fact about something that happened. agent_run is the one
-- MUTABLE row (status, next_poll_at), because it is the poller's work queue,
-- not the record — the record is 0092's intent log.
--
-- This file restates no seeder and writes no ACL rule: the routes that reach
-- these tables use the BARE tokens agent-run and agent-approval, which Data
-- Entry's default_access = 'none' refuses with zero acl_rule rows (the
-- authority tds-challan, backup and e-way-bill shipped under). Runs as ADMIN.

CREATE TABLE IF NOT EXISTS agent_run (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    -- The human the agent acts FOR. Every commit is judged against this
    -- user's ACL (intersected with the agent's and the delegation's).
    created_by    UUID NOT NULL,
    agent_id      TEXT NOT NULL,
    channel       TEXT NOT NULL,
    -- What the human asked, verbatim. Stored because the approver must be able
    -- to read it; the intent log carries only its hash.
    instruction   TEXT NOT NULL,
    -- Untrusted material the run was handed (bill text, a bank narration, a
    -- WhatsApp forward, a Tally XML NARRATION), each labelled with its
    -- provenance: [{"label":..,"provenance":..,"text":..}]. ⛔ Nothing in here
    -- may authorise a money-moving commit by itself (PLAN-AGENTIC §2.4).
    inputs        JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- The delegation the human granted this run, snapshotted at start so a
    -- later change to the human's rights cannot widen a run already in
    -- flight: {"acl_keys":[...], "expires_at":...}.
    delegation    JSONB NOT NULL,
    status        TEXT NOT NULL,
    outcome       JSONB NULL,
    last_error    TEXT NOT NULL DEFAULT '',
    attempts      INTEGER NOT NULL DEFAULT 0,
    next_poll_at  TIMESTAMPTZ NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ NULL,
    CONSTRAINT agent_run_company_scope UNIQUE (company_id, id),
    CONSTRAINT agent_run_status CHECK (status IN
        ('running', 'parked', 'completed', 'failed', 'expired', 'cancelled')),
    CONSTRAINT agent_run_channel CHECK (channel IN
        ('panel', 'api', 'whatsapp', 'voice', 'mcp', 'email')),
    CONSTRAINT agent_run_instruction_not_blank CHECK (length(btrim(instruction)) > 0),
    CONSTRAINT agent_run_inputs_is_array CHECK (jsonb_typeof(inputs) = 'array'),
    -- A finished run is never polled again; an unfinished one always has a
    -- next poll time. This is what keeps a completed run out of the queue.
    CONSTRAINT agent_run_poll_matches_status CHECK (
        (status IN ('running', 'parked')) = (next_poll_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS agent_run_due ON agent_run (next_poll_at)
    WHERE next_poll_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS agent_run_company_created ON agent_run (company_id, created_at DESC);

CREATE TABLE IF NOT EXISTS agent_run_step (
    company_id  UUID NOT NULL,
    run_id      UUID NOT NULL,
    step_name   TEXT NOT NULL,
    seq         BIGINT GENERATED ALWAYS AS IDENTITY,
    output      JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, run_id, step_name),
    CONSTRAINT agent_run_step_run_fk FOREIGN KEY (company_id, run_id)
        REFERENCES agent_run (company_id, id)
);
CREATE INDEX IF NOT EXISTS agent_run_step_order ON agent_run_step (company_id, run_id, seq);

CREATE TABLE IF NOT EXISTS agent_approval (
    id              UUID PRIMARY KEY,
    company_id      UUID NOT NULL,
    run_id          UUID NOT NULL,
    -- The durable step this approval gates; one approval per gate per run.
    step_key        TEXT NOT NULL,
    action_id       TEXT NOT NULL,
    acl_key         TEXT NOT NULL,
    risk            TEXT NOT NULL,
    -- EXACTLY what will be committed if approved unedited, and the ledger-
    -- effect preview the real handler produced for it. ⛔ The commit replays
    -- THESE args (or the approver's edit of them); it never asks the model
    -- again (PLAN-AGENTIC §2.1: "replayed from an approved plan id, never
    -- regenerated").
    args            JSONB NOT NULL,
    preview         JSONB NOT NULL,
    amount_paise    BIGINT NULL,
    -- Why a human is needed, in the policy's own words.
    reasons         JSONB NOT NULL,
    -- Which argument values came from untrusted inputs.
    provenance      JSONB NOT NULL,
    on_behalf_of    UUID NOT NULL,
    requested_at    TIMESTAMPTZ NOT NULL,
    -- Recorded HERE because the parked step records nothing (D120 §6: "the
    -- approval deadline must be recorded by an earlier step, or a column on
    -- the approvals row") and the inbox renders "expires in 2 days".
    deadline_at     TIMESTAMPTZ NOT NULL,
    CONSTRAINT agent_approval_company_scope UNIQUE (company_id, id),
    CONSTRAINT agent_approval_one_per_gate UNIQUE (company_id, run_id, step_key),
    CONSTRAINT agent_approval_run_fk FOREIGN KEY (company_id, run_id)
        REFERENCES agent_run (company_id, id),
    CONSTRAINT agent_approval_deadline_after_request CHECK (deadline_at > requested_at),
    CONSTRAINT agent_approval_amount_non_negative CHECK (amount_paise IS NULL OR amount_paise >= 0)
);
CREATE INDEX IF NOT EXISTS agent_approval_company_requested
    ON agent_approval (company_id, requested_at DESC);

CREATE TABLE IF NOT EXISTS agent_decision (
    id               UUID PRIMARY KEY,
    company_id       UUID NOT NULL,
    approval_id      UUID NOT NULL,
    decision         TEXT NOT NULL,
    -- Present only for edit-then-approve: the args the approver changed. The
    -- commit uses these instead of agent_approval.args.
    edited_args      JSONB NULL,
    note             TEXT NOT NULL DEFAULT '',
    decided_by       UUID NULL,
    decided_webauthn BOOLEAN NOT NULL DEFAULT false,
    decided_at       TIMESTAMPTZ NOT NULL,
    CONSTRAINT agent_decision_company_scope UNIQUE (company_id, id),
    -- ⛔ ONE DECISION PER APPROVAL. This is D120 F2 made structural: once
    -- 'expired' (or anything) is written, a late approve cannot be.
    CONSTRAINT agent_decision_one_per_approval UNIQUE (company_id, approval_id),
    CONSTRAINT agent_decision_approval_fk FOREIGN KEY (company_id, approval_id)
        REFERENCES agent_approval (company_id, id),
    CONSTRAINT agent_decision_kind CHECK (decision IN ('approve', 'reject', 'ask_back', 'expired')),
    -- Only the system expires; only a person decides otherwise.
    CONSTRAINT agent_decision_expired_is_system CHECK ((decision = 'expired') = (decided_by IS NULL)),
    CONSTRAINT agent_decision_edit_only_on_approve CHECK (edited_args IS NULL OR decision = 'approve'),
    CONSTRAINT agent_decision_ask_back_says_what CHECK (decision <> 'ask_back' OR length(btrim(note)) > 0)
);

CREATE TABLE IF NOT EXISTS agent_commit (
    company_id       UUID NOT NULL,
    idempotency_key  TEXT NOT NULL,
    run_id           UUID NOT NULL,
    approval_id      UUID NULL,
    action_id        TEXT NOT NULL,
    voucher_ids      UUID[] NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, idempotency_key),
    CONSTRAINT agent_commit_run_fk FOREIGN KEY (company_id, run_id)
        REFERENCES agent_run (company_id, id),
    CONSTRAINT agent_commit_key_not_blank CHECK (length(btrim(idempotency_key)) > 0)
);

-- Append-only guard for the four fact tables.
CREATE OR REPLACE FUNCTION agent_fact_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION '% is append-only; % is not permitted', TG_TABLE_NAME, TG_OP;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['agent_run_step', 'agent_approval', 'agent_decision', 'agent_commit'] LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', t || '_no_mutate', t);
        EXECUTE format('CREATE TRIGGER %I BEFORE UPDATE OR DELETE ON %I '
                       'FOR EACH ROW EXECUTE FUNCTION agent_fact_immutable()', t || '_no_mutate', t);
    END LOOP;
    FOREACH t IN ARRAY ARRAY['agent_run', 'agent_run_step', 'agent_approval', 'agent_decision', 'agent_commit'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I '
                       'USING (company_id = app_current_company_id()) '
                       'WITH CHECK (company_id = app_current_company_id())', t);
        EXECUTE format('REVOKE ALL ON %I FROM PUBLIC', t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE ON agent_run TO tally_app;
REVOKE UPDATE, DELETE, TRUNCATE ON agent_run_step, agent_approval, agent_decision, agent_commit FROM tally_app;
GRANT SELECT, INSERT ON agent_run_step, agent_approval, agent_decision, agent_commit TO tally_app;

-- The poller's ONLY cross-tenant read: which runs are due. SECURITY DEFINER,
-- ids only — 0023's notification_pending_companies shape. p_now is a parameter
-- rather than now() so the poller's clock is the one clock (the 72-hour test
-- drives it; a function that read now() would make that test lie).
CREATE OR REPLACE FUNCTION agent_runs_due(p_now TIMESTAMPTZ, p_limit INT DEFAULT 50)
RETURNS TABLE (company_id UUID, run_id UUID)
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
    SELECT r.company_id, r.id
      FROM agent_run r
     WHERE r.next_poll_at IS NOT NULL
       AND r.next_poll_at <= p_now
     ORDER BY r.next_poll_at, r.id
     LIMIT p_limit;
$$;

REVOKE ALL ON FUNCTION agent_runs_due(TIMESTAMPTZ, INT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_runs_due(TIMESTAMPTZ, INT) TO tally_app;

COMMENT ON TABLE agent_run IS
    'W2/D121. One agent run: the human it acts for, what they asked, the '
    'untrusted inputs it was handed (with provenance), the delegation '
    'snapshot, and the poller state. The only MUTABLE agent table.';
COMMENT ON TABLE agent_run_step IS
    'W2/D121. internal/durable step results for agent runs, under FORCE RLS '
    '(statutory_step carries none by design). Append-only.';
COMMENT ON TABLE agent_approval IS
    'W2/D121. The approval inbox: one row per commit gate that needs a human, '
    'carrying the exact args and the handler-produced preview. Append-only; '
    'status is derived from agent_decision.';
COMMENT ON TABLE agent_decision IS
    'W2/D121. At most one decision per approval (approve / reject / ask_back / '
    'expired). The UNIQUE is what makes expiry terminal (D120 F2).';
COMMENT ON TABLE agent_commit IS
    'W2/D121. Commit idempotency: inserted in the same transaction as the '
    'voucher the agent committed, keyed by run+step.';
