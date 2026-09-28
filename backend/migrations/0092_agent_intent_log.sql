-- 0092 — the agent INTENT LOG (W2, D121). PLAN-AGENTIC §2.6: "No agent action
-- without an attributable, immutable record. Intent-log row per attempt,
-- linked into the hash chain, exportable as an AI action register."
--
-- ===========================================================================
-- ⛔ APPEND-ONLY BY BOTH HALVES, NOT ONE
-- ===========================================================================
-- HANDOFF-AGENTIC-AFTER-W0.md §8: "both halves are required". A BEFORE UPDATE
-- OR DELETE trigger that raises (the voucher_version_no_mutate shape, 0002:115)
-- stops the app role AND the table owner AND a superuser without
-- session_replication_role; the GRANT of SELECT, INSERT only stops the app role
-- even if a later migration drops the trigger by accident. Either alone has a
-- hole the other closes: a trigger can be disabled by an owner, and a grant does
-- not bind the owner at all.
--
-- ===========================================================================
-- ⛔ THE CHAIN IS PER RUN, NOT PER TENANT
-- ===========================================================================
-- §8 again: "a tenant-wide chain serialises every agent write behind one row".
-- Every append would have to read the tenant's head and write the next link
-- under a lock on it, so two agent runs in one company — one reconciling the
-- bank, one drafting a sales invoice — would queue behind each other on an
-- audit table. Chained per run, an append contends only with its own run:
-- intentlog.Append takes a transaction-scoped advisory lock on the run for the
-- head read and the insert. (The run's durable lock does NOT cover it — an
-- approval decision and a cancel append from the HTTP path without that lock;
-- the first cut of this comment claimed otherwise and was wrong.) The
-- tenant-level tamper evidence comes from the ANCHOR table below: a periodic row
-- that commits to every run head, chained to the previous anchor.
--
-- ===========================================================================
-- ⛔ THE HASH IS COMPUTED IN GO, WITH BLAKE3, NEVER IN SQL
-- ===========================================================================
-- The same rule as voucher_version (internal/ledger/hash.go). hash =
-- BLAKE3(canonical row fields || prev_hash); internal/intentlog owns the
-- canonical encoding and Verify recomputes it. The database stores bytes it
-- cannot produce, which is what makes a quietly edited row detectable by
-- anyone holding the code — the trigger stops edits, the chain proves none
-- happened before it existed or while it was disabled.
--
-- ===========================================================================
-- WHAT A ROW IS
-- ===========================================================================
-- One row per EVENT in a run: the instruction, each model decision, each tool
-- call and its result digest, each preview, each policy verdict, each approval
-- request and decision, each commit or refusal. ⛔ NO PROMPT OR COMPLETION TEXT
-- (PLAN-AGENTIC §2.7 and the trajectory rule): payload carries identifiers,
-- verdicts, amounts in paise computed by Go, and HASHES of model-visible
-- content — never the content. The instruction a human typed IS stored, on the
-- run row (0094), because the approver must be able to read what was asked;
-- this log carries its hash.
--
-- This file restates no seeder and writes no ACL rule. Runs as the ADMIN DSN.

CREATE TABLE IF NOT EXISTS agent_intent_log (
    id            UUID PRIMARY KEY,               -- uuidv7, minted app-side
    company_id    UUID NOT NULL,
    run_id        UUID NOT NULL,
    seq           INTEGER NOT NULL,               -- 1-based position in the run's chain
    kind          TEXT NOT NULL,
    -- WHO. An agent event names the agent AND the human it acted for; a human
    -- event (a decision) names the human; a system event (an expiry) names
    -- neither person, and says so.
    actor_kind    TEXT NOT NULL,
    actor_id      TEXT NOT NULL,
    on_behalf_of  UUID NULL,
    -- WHAT. The ActionSpec id and ACL key, when the event concerns an action.
    action_id     TEXT NULL,
    acl_key       TEXT NULL,
    payload       JSONB NOT NULL,
    prev_hash     BYTEA NULL,                     -- NULL exactly at seq 1
    hash          BYTEA NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,           -- set app-side: it is hashed
    CONSTRAINT agent_intent_log_one_seq_per_run UNIQUE (company_id, run_id, seq),
    CONSTRAINT agent_intent_log_company_scope UNIQUE (company_id, id),
    CONSTRAINT agent_intent_log_seq_positive CHECK (seq >= 1),
    -- The genesis rule, in the database as well as in Go: a chain has exactly
    -- one link with no predecessor, and it is the first.
    CONSTRAINT agent_intent_log_genesis CHECK ((seq = 1) = (prev_hash IS NULL)),
    CONSTRAINT agent_intent_log_hash_len CHECK (length(hash) = 32),
    CONSTRAINT agent_intent_log_prev_hash_len CHECK (prev_hash IS NULL OR length(prev_hash) = 32),
    CONSTRAINT agent_intent_log_kind CHECK (kind IN (
        'run_started', 'decision_step', 'tool_call', 'preview', 'verifier_error',
        'policy', 'approval_requested', 'approval_decided', 'commit',
        'commit_refused', 'run_finished', 'run_failed')),
    CONSTRAINT agent_intent_log_actor_kind CHECK (actor_kind IN ('agent', 'user', 'system')),
    CONSTRAINT agent_intent_log_agent_names_principal
        CHECK (actor_kind <> 'agent' OR on_behalf_of IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS agent_intent_log_company_created
    ON agent_intent_log (company_id, created_at);

CREATE OR REPLACE FUNCTION agent_intent_log_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'agent_intent_log is append-only; % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS agent_intent_log_no_mutate ON agent_intent_log;
CREATE TRIGGER agent_intent_log_no_mutate
    BEFORE UPDATE OR DELETE ON agent_intent_log
    FOR EACH ROW EXECUTE FUNCTION agent_intent_log_immutable();

-- ---------------------------------------------------------------------------
-- The ANCHOR: tenant-level tamper evidence over the per-run chains.
--
-- root = BLAKE3(prev_anchor_root || for each run head in (run_id) order:
-- run_id || head_seq || head_hash), computed in Go (intentlog.Anchor). Each
-- anchor commits to the head of EVERY run in the company as of anchored_at, so
-- truncating a run's tail after it was anchored — the edit the per-run chain
-- cannot see by itself, because a chain with its last links removed is still a
-- valid chain — changes the recomputed root. Chained to the previous anchor so
-- deleting an anchor is visible too. "Nightly" is the scheduling intent
-- (PLAN-AGENTIC W2); the table does not care how often it is written.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent_intent_anchor (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    anchored_at   TIMESTAMPTZ NOT NULL,
    run_count     INTEGER NOT NULL,
    event_count   BIGINT NOT NULL,
    prev_root     BYTEA NULL,
    root          BYTEA NOT NULL,
    CONSTRAINT agent_intent_anchor_company_scope UNIQUE (company_id, id),
    CONSTRAINT agent_intent_anchor_counts CHECK (run_count >= 0 AND event_count >= 0),
    CONSTRAINT agent_intent_anchor_root_len CHECK (length(root) = 32),
    CONSTRAINT agent_intent_anchor_prev_len CHECK (prev_root IS NULL OR length(prev_root) = 32)
);

CREATE INDEX IF NOT EXISTS agent_intent_anchor_company_at
    ON agent_intent_anchor (company_id, anchored_at);

CREATE OR REPLACE FUNCTION agent_intent_anchor_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'agent_intent_anchor is append-only; % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS agent_intent_anchor_no_mutate ON agent_intent_anchor;
CREATE TRIGGER agent_intent_anchor_no_mutate
    BEFORE UPDATE OR DELETE ON agent_intent_anchor
    FOR EACH ROW EXECUTE FUNCTION agent_intent_anchor_immutable();

-- The anchoring job's ONLY cross-tenant read: which companies have intent-log
-- rows newer than their latest anchor. SECURITY DEFINER and ids only, exactly
-- the shape of 0023's notification_pending_companies — preferable to handing
-- the app role an RLS bypass.
CREATE OR REPLACE FUNCTION agent_intent_unanchored_companies(p_limit INT DEFAULT 100)
RETURNS TABLE (company_id UUID)
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
    SELECT l.company_id
      FROM agent_intent_log l
     GROUP BY l.company_id
    HAVING max(l.created_at) > COALESCE(
               (SELECT max(a.anchored_at) FROM agent_intent_anchor a
                 WHERE a.company_id = l.company_id), '-infinity'::timestamptz)
     ORDER BY 1
     LIMIT p_limit;
$$;

REVOKE ALL ON FUNCTION agent_intent_unanchored_companies(INT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_intent_unanchored_companies(INT) TO tally_app;

DO $$
BEGIN
    EXECUTE 'ALTER TABLE agent_intent_log ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE agent_intent_log FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON agent_intent_log';
    EXECUTE 'CREATE POLICY tenant_isolation ON agent_intent_log '
            'USING (company_id = app_current_company_id()) '
            'WITH CHECK (company_id = app_current_company_id())';
    EXECUTE 'ALTER TABLE agent_intent_anchor ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE agent_intent_anchor FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON agent_intent_anchor';
    EXECUTE 'CREATE POLICY tenant_isolation ON agent_intent_anchor '
            'USING (company_id = app_current_company_id()) '
            'WITH CHECK (company_id = app_current_company_id())';
END
$$;

-- ⛔ SELECT AND INSERT, NOTHING ELSE. See the header: this grant is the half of
-- append-only that binds the app role even if the trigger is ever lost.
REVOKE ALL ON agent_intent_log FROM PUBLIC;
REVOKE ALL ON agent_intent_anchor FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE ON agent_intent_log FROM tally_app;
REVOKE UPDATE, DELETE, TRUNCATE ON agent_intent_anchor FROM tally_app;
GRANT SELECT, INSERT ON agent_intent_log TO tally_app;
GRANT SELECT, INSERT ON agent_intent_anchor TO tally_app;

COMMENT ON TABLE agent_intent_log IS
    'W2/D121. The agent intent log: one row per event of an agent run, BLAKE3 '
    'hash-chained PER RUN (prev_hash -> hash, computed in Go by '
    'internal/intentlog), append-only by trigger AND by grant. Carries hashes '
    'of model-visible content, never the content.';
COMMENT ON TABLE agent_intent_anchor IS
    'W2/D121. Periodic tenant-level anchors over the per-run intent chains: '
    'root commits to every run head as of anchored_at, chained to the previous '
    'anchor, so truncating an anchored run tail is detectable.';
COMMENT ON COLUMN agent_intent_log.payload IS
    'Event detail: identifiers, verdicts, paise computed by Go, and hashes of '
    'model-visible text. NEVER prompt or completion content (PLAN-AGENTIC §2.7).';
COMMENT ON COLUMN agent_intent_log.hash IS
    'BLAKE3(canonical(run_id, seq, kind, actor, on_behalf_of, action_id, '
    'acl_key, payload, created_at) || prev_hash). Computed in Go; see '
    'internal/intentlog/chain.go for the canonical encoding.';
