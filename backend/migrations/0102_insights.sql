-- 0102_insights.sql
-- W4 (PLAN-AGENTIC.md §3, RESEARCH-AGENTIC D8): the insight engine's tables.
--
--   insight               one thing a deterministic detector found in the
--                         books, with the NUMBERS it found (payload_json), the
--                         read-only SQL that REPRODUCES them (evidence_sql +
--                         evidence_args) and the vouchers behind them.
--   insight_feedback      a human's verdict on one insight (useful / not
--                         useful / wrong). Append-only. It is what p_real and
--                         affinity are learned from.
--   insight_suppression   "stop showing me this": a type, or a type for one
--                         entity, until a time or for good.
--   insight_run           the nightly job's ledger: ONE row per (company,
--                         business day), which is what makes the nightly run
--                         idempotent per (company, day).
--   insight_digest        the morning digest: ≤5 insight ids in rank order and
--                         the narrative that went with them, one per (company,
--                         day, recipient).
--   insight_voucher_event the real-time queue: one row per voucher_head move,
--                         written BY TRIGGER inside the voucher's own
--                         transaction, drained by the insight poller.
--
-- ===========================================================================
-- ⛔ AN INSIGHT'S NUMBERS MUST BE REPRODUCIBLE FROM THE BOOKS BY A READ
-- ===========================================================================
-- PLAN-AGENTIC W4's gate: "every insight row carries evidence_sql and
-- voucher_ids[] that reproduce its numbers". evidence_sql is written by Go
-- (internal/insights, one constant per detector) — NEVER by a model — and is
-- executed with evidence_args bound as $1..$n inside a READ ONLY, RLS-scoped
-- transaction. It returns ONE row whose columns are named after the numeric
-- keys of payload_json plus `voucher_ids`; internal/insights' evidence gate
-- re-runs every detector's output and requires equality to the paisa. The
-- CHECKs below make the contract structural where SQL can: an object payload,
-- an array of args, a SELECT/WITH statement with no statement separator.
--
-- ===========================================================================
-- WHY THE VOUCHER QUEUE IS A TRIGGER AND NOT A CALL IN THE POST PATH
-- ===========================================================================
-- Every write path that moves a voucher (the F-key screens, the agent's
-- dispatcher, the offline sync sequencer, the Tally importer) ends by moving
-- voucher_head. A trigger on voucher_head is therefore the ONE place a real-time
-- detector cannot be forgotten by a future path, and it commits or rolls back
-- WITH the voucher — a rolled-back post leaves no event, a committed one cannot
-- lose its event. D8 names Postgres NOTIFY; NOTIFY is not durable (a listener
-- that is down when the voucher commits never hears of it), so this is the
-- outbox half and polling is the delivery (the agent poller's shape, 0094).
-- The trigger does no detection itself: a detector that failed inside the post
-- transaction would refuse the voucher, and an insight must never be able to.
--
-- ===========================================================================
-- WHY THERE IS NO ACL RULE AND NO seed_company_auth_defaults RESTATEMENT
-- ===========================================================================
-- The routes ride a BARE resource token, `insights` (authz/vocabulary.go), per
-- the 0091 row of PLAN-TALLY-PARITY §2.5: Data Entry's default_access is 'none'
-- and no seeded rule names the token, so it is DENIED for every seeded
-- non-Owner level on the day it ships and needs no acl_rule row. A
-- report:NAME would have been GRANTED to every operator by 0011's coarse
-- `allow display report` — and the inbox names debtors, suppliers, TDS
-- arrears and users' back-dated edits. The seeder census stays at 17 PERFORMs.

-- ---------------------------------------------------------------------------
-- insight
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS insight (
    id               UUID PRIMARY KEY,
    company_id       UUID NOT NULL,
    -- insight_type is a CODE from internal/insights' closed registry
    -- (insights.Types), never free text; the CHECK below is its shape and the
    -- registry test pins the set.
    insight_type     TEXT NOT NULL,
    cadence          TEXT NOT NULL,
    entity_kind      TEXT NOT NULL,
    entity_key       TEXT NOT NULL,
    -- Display name at detection time, denormalised for the entity_facts
    -- reason: a renamed ledger must not rewrite what the owner was told.
    entity_name      TEXT NOT NULL,
    -- The DEDUPE identity: type + entity + (for a periodic insight) the
    -- period. Re-detecting the same thing on a later night finds the open row
    -- by fingerprint instead of adding a second.
    fingerprint      TEXT NOT NULL,
    as_of            DATE NOT NULL,
    detected_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The ranker's inputs as the DETECTOR stated them. score is recomputed at
    -- digest time (p_real, novelty and affinity move); these do not.
    impact_inr       NUMERIC(19,4) NOT NULL,
    actionability    NUMERIC(6,4) NOT NULL,
    payload_json     JSONB NOT NULL,
    evidence_sql     TEXT NOT NULL,
    evidence_args    JSONB NOT NULL DEFAULT '[]',
    voucher_ids      UUID[] NOT NULL DEFAULT '{}',
    -- The voucher whose commit raised a real-time insight; NULL for nightly.
    trigger_voucher_id UUID NULL,
    detector_version TEXT NOT NULL,
    state            TEXT NOT NULL DEFAULT 'open',
    state_changed_at TIMESTAMPTZ NULL,
    state_changed_by TEXT NULL,
    expires_at       TIMESTAMPTZ NULL,

    CONSTRAINT insight_company_scope UNIQUE (company_id, id),
    CONSTRAINT insight_type_shape CHECK (insight_type ~ '^[a-z][a-z0-9_]{2,63}$'),
    CONSTRAINT insight_cadence_known CHECK (cadence IN ('realtime', 'nightly')),
    CONSTRAINT insight_entity_kind_known
        CHECK (entity_kind IN ('party', 'item', 'voucher', 'tenant', 'user', 'period')),
    CONSTRAINT insight_state_known
        CHECK (state IN ('open', 'dismissed', 'superseded', 'expired')),
    CONSTRAINT insight_impact_non_negative CHECK (impact_inr >= 0),
    CONSTRAINT insight_actionability_unit CHECK (actionability > 0 AND actionability <= 1),
    CONSTRAINT insight_payload_is_object CHECK (jsonb_typeof(payload_json) = 'object'),
    CONSTRAINT insight_args_is_array CHECK (jsonb_typeof(evidence_args) = 'array'),
    -- A statement, not a script: SELECT/WITH only, and no separator anywhere
    -- (the Go constants never need one, and a `;` is how a second statement
    -- would ride along). It is ALSO executed read-only; this is the first wall.
    CONSTRAINT insight_evidence_is_one_select
        CHECK (evidence_sql ~* '^\s*(select|with)\s' AND position(';' IN evidence_sql) = 0),
    CONSTRAINT insight_voucher_ids_capped CHECK (cardinality(voucher_ids) <= 200),
    CONSTRAINT insight_fingerprint_not_blank CHECK (btrim(fingerprint) <> '')
);
-- ONE OPEN INSIGHT PER FINGERPRINT. Two pollers (or a poller and the nightly
-- run) racing to raise the same insight collide here instead of doubling it.
CREATE UNIQUE INDEX IF NOT EXISTS insight_one_open_per_fingerprint
    ON insight (company_id, fingerprint) WHERE state = 'open';
CREATE INDEX IF NOT EXISTS insight_open_by_type
    ON insight (company_id, insight_type, as_of) WHERE state = 'open';

COMMENT ON TABLE insight IS
    'W4/D8. One deterministic finding over the books: payload_json holds its '
    'numbers, evidence_sql (+ evidence_args) is the read-only SELECT that '
    'reproduces them to the paisa, voucher_ids are the vouchers behind them. '
    'Written by internal/insights detectors, never by a model.';
COMMENT ON COLUMN insight.evidence_sql IS
    'A Go constant per detector (never model text), run with evidence_args as '
    '$1..$n in a READ ONLY RLS-scoped tx; returns one row whose columns are the '
    'numeric payload keys plus voucher_ids.';

-- ---------------------------------------------------------------------------
-- insight_feedback — append-only verdicts
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS insight_feedback (
    id           UUID PRIMARY KEY,
    company_id   UUID NOT NULL,
    insight_id   UUID NOT NULL,
    -- Denormalised so per-type precision survives the insight being
    -- superseded, and so the prior update is one GROUP BY.
    insight_type TEXT NOT NULL,
    verdict      TEXT NOT NULL,
    -- The authenticated subject (user id or the JWT subject). Affinity is
    -- learned per recipient from this column.
    given_by     TEXT NOT NULL,
    note         TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT insight_feedback_company_scope UNIQUE (company_id, id),
    CONSTRAINT insight_feedback_verdict_known
        CHECK (verdict IN ('useful', 'not_useful', 'wrong')),
    CONSTRAINT insight_feedback_note_bounded CHECK (char_length(note) <= 500),
    CONSTRAINT insight_feedback_insight_fk
        FOREIGN KEY (company_id, insight_id) REFERENCES insight (company_id, id)
);
CREATE INDEX IF NOT EXISTS insight_feedback_by_type ON insight_feedback (company_id, insight_type);

-- ---------------------------------------------------------------------------
-- insight_suppression
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS insight_suppression (
    id           UUID PRIMARY KEY,
    company_id   UUID NOT NULL,
    insight_type TEXT NOT NULL,
    -- '' = every entity of the type. NOT NULL with '' for the entity_facts
    -- qualifier reason: NULLs are distinct in a unique index.
    entity_key   TEXT NOT NULL DEFAULT '',
    until        TIMESTAMPTZ NULL,   -- NULL = until lifted
    reason       TEXT NOT NULL,
    created_by   TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT insight_suppression_company_scope UNIQUE (company_id, id),
    CONSTRAINT insight_suppression_one_per_target UNIQUE (company_id, insight_type, entity_key),
    CONSTRAINT insight_suppression_reason_not_blank CHECK (btrim(reason) <> ''),
    CONSTRAINT insight_suppression_type_shape CHECK (insight_type ~ '^[a-z][a-z0-9_]{2,63}$')
);

-- ---------------------------------------------------------------------------
-- insight_run — the nightly ledger, idempotent per (company, business day)
-- ---------------------------------------------------------------------------
-- The nightly job for a company runs in ONE transaction that ends by
-- inserting this row. A second attempt the same day (a second replica, a
-- restart, an operator's "run now") finds it and does nothing; a crash
-- mid-run rolls the whole night back, row included, so the retry starts
-- clean. The UNIQUE is the idempotency; the advisory lock in Go is only
-- what stops the loser doing the work before it finds out.
CREATE TABLE IF NOT EXISTS insight_run (
    id           UUID PRIMARY KEY,
    company_id   UUID NOT NULL,
    run_date     DATE NOT NULL,
    kind         TEXT NOT NULL,
    finished_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    stats        JSONB NOT NULL,

    CONSTRAINT insight_run_company_scope UNIQUE (company_id, id),
    CONSTRAINT insight_run_one_per_day UNIQUE (company_id, run_date, kind),
    CONSTRAINT insight_run_kind_known CHECK (kind IN ('nightly')),
    CONSTRAINT insight_run_stats_is_object CHECK (jsonb_typeof(stats) = 'object')
);

-- ---------------------------------------------------------------------------
-- insight_digest
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS insight_digest (
    id             UUID PRIMARY KEY,
    company_id     UUID NOT NULL,
    digest_date    DATE NOT NULL,
    -- '' = the company digest (affinity 1 for everyone); otherwise the
    -- subject it was ranked for.
    recipient      TEXT NOT NULL DEFAULT '',
    -- [{insight_id, rank, score, impact_inr, p_real, actionability, novelty,
    --   affinity, text}] — the ranker's terms are kept per item so "why was
    -- this first?" is answered by the row, not re-derived.
    items          JSONB NOT NULL,
    narrative      TEXT NOT NULL,
    -- 'model' = narrated through internal/llm AND passed the number-provenance
    -- check; 'template' = the deterministic fallback (no model configured, the
    -- model failed, or its narration was REJECTED — narration_rejected says why).
    narrator       TEXT NOT NULL,
    model_id       TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT '',
    narration_rejected TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT insight_digest_company_scope UNIQUE (company_id, id),
    CONSTRAINT insight_digest_one_per_day UNIQUE (company_id, digest_date, recipient),
    CONSTRAINT insight_digest_items_is_array CHECK (jsonb_typeof(items) = 'array'),
    -- ⛔ THE ≤5 CAP IS A CONSTRAINT, NOT A CONVENTION (D8: "hard cap on digest
    -- size"). A ranker bug that returned six cannot store six.
    CONSTRAINT insight_digest_at_most_five CHECK (jsonb_array_length(items) <= 5),
    CONSTRAINT insight_digest_narrator_known CHECK (narrator IN ('model', 'template'))
);

-- ---------------------------------------------------------------------------
-- insight_voucher_event — the real-time queue, filled by trigger
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS insight_voucher_event (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id   UUID NOT NULL,
    voucher_id   UUID NOT NULL,
    version_id   UUID NOT NULL,
    op           voucher_op NOT NULL,
    enqueued_at  TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    processed_at TIMESTAMPTZ NULL,
    -- How many insights the detectors raised for it (0 is the common answer
    -- and is recorded, so "was it looked at?" and "did it find anything?" are
    -- different questions with different answers).
    raised       INTEGER NULL,

    CONSTRAINT insight_voucher_event_company_scope UNIQUE (company_id, id)
);
CREATE INDEX IF NOT EXISTS insight_voucher_event_pending
    ON insight_voucher_event (company_id, enqueued_at) WHERE processed_at IS NULL;

CREATE OR REPLACE FUNCTION insight_enqueue_voucher_event()
RETURNS TRIGGER AS $$
BEGIN
    -- SECURITY INVOKER on purpose: the row is written under the SAME session
    -- GUC as the voucher, so the RLS WITH CHECK below is satisfied by the
    -- company that actually posted — a trigger that ran as its owner could
    -- write an event into any tenant.
    IF TG_OP = 'UPDATE' AND NEW.current_version_id IS NOT DISTINCT FROM OLD.current_version_id THEN
        RETURN NULL;
    END IF;
    INSERT INTO insight_voucher_event (company_id, voucher_id, version_id, op)
    VALUES (NEW.company_id, NEW.voucher_id, NEW.current_version_id, NEW.op);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS voucher_head_insight_event ON voucher_head;
CREATE TRIGGER voucher_head_insight_event
    AFTER INSERT OR UPDATE OF current_version_id ON voucher_head
    FOR EACH ROW EXECUTE FUNCTION insight_enqueue_voucher_event();

-- ---------------------------------------------------------------------------
-- Immutability of the records (feedback, runs, digests): no UPDATE, no DELETE.
-- The insight row is the one mutable table (state), exactly as agent_run is
-- the one mutable agent table: it is the inbox, and the record of what was
-- believed is payload_json + the feedback rows.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION w4_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION '% is append-only; % is not permitted', TG_TABLE_NAME, TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS insight_feedback_append_only ON insight_feedback;
CREATE TRIGGER insight_feedback_append_only
    BEFORE UPDATE OR DELETE ON insight_feedback
    FOR EACH ROW EXECUTE FUNCTION w4_append_only();
DROP TRIGGER IF EXISTS insight_run_append_only ON insight_run;
CREATE TRIGGER insight_run_append_only
    BEFORE UPDATE OR DELETE ON insight_run
    FOR EACH ROW EXECUTE FUNCTION w4_append_only();
DROP TRIGGER IF EXISTS insight_digest_append_only ON insight_digest;
CREATE TRIGGER insight_digest_append_only
    BEFORE UPDATE OR DELETE ON insight_digest
    FOR EACH ROW EXECUTE FUNCTION w4_append_only();

-- The insight row: its NUMBERS are immutable (a re-detection with different
-- numbers supersedes and inserts; it never edits payload_json in place, or the
-- evidence a feedback verdict was given against would silently change). Only
-- state / state_changed_* / expires_at may move, and state only out of 'open'.
CREATE OR REPLACE FUNCTION insight_only_state_moves()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'insight rows are never deleted (dismiss or supersede instead)';
    END IF;
    IF (to_jsonb(NEW) - ARRAY['state', 'state_changed_at', 'state_changed_by', 'expires_at'])
       IS DISTINCT FROM
       (to_jsonb(OLD) - ARRAY['state', 'state_changed_at', 'state_changed_by', 'expires_at']) THEN
        RAISE EXCEPTION 'insight %: only state, state_changed_at, state_changed_by and expires_at may change', OLD.id;
    END IF;
    IF OLD.state <> 'open' AND NEW.state IS DISTINCT FROM OLD.state THEN
        RAISE EXCEPTION 'insight %: state % is terminal', OLD.id, OLD.state;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS insight_only_state_moves ON insight;
CREATE TRIGGER insight_only_state_moves
    BEFORE UPDATE OR DELETE ON insight
    FOR EACH ROW EXECUTE FUNCTION insight_only_state_moves();

-- ---------------------------------------------------------------------------
-- RLS: ENABLE and FORCE, USING and WITH CHECK — the 0088 / 0096 shape.
-- ---------------------------------------------------------------------------
DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['insight', 'insight_feedback', 'insight_suppression',
                             'insight_run', 'insight_digest', 'insight_voucher_event'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I '
                       'USING (company_id = app_current_company_id()) '
                       'WITH CHECK (company_id = app_current_company_id())', t);
    END LOOP;
END
$$;

-- Grants are the second wall behind the triggers: no DELETE anywhere except a
-- suppression (lifting one is deleting it), no UPDATE on the append-only
-- records.
GRANT SELECT, INSERT, UPDATE ON insight               TO tally_app;
GRANT SELECT, INSERT         ON insight_feedback      TO tally_app;
GRANT SELECT, INSERT, DELETE ON insight_suppression   TO tally_app;
GRANT SELECT, INSERT         ON insight_run           TO tally_app;
GRANT SELECT, INSERT         ON insight_digest        TO tally_app;
GRANT SELECT, INSERT, UPDATE ON insight_voucher_event TO tally_app;

-- ---------------------------------------------------------------------------
-- The poller's ONLY cross-tenant reads. SECURITY DEFINER, ids only — 0023's
-- notification_pending_companies shape and 0094's agent_runs_due.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION insight_event_pending_companies(p_limit INT DEFAULT 100)
RETURNS TABLE (company_id UUID)
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
    SELECT DISTINCT e.company_id
      FROM insight_voucher_event e
     WHERE e.processed_at IS NULL
     ORDER BY 1
     LIMIT p_limit;
$$;
REVOKE ALL ON FUNCTION insight_event_pending_companies(INT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION insight_event_pending_companies(INT) TO tally_app;

-- p_date is the caller's business date (IST), a parameter for the same reason
-- agent_runs_due takes p_now: the scheduler's clock is the one clock.
CREATE OR REPLACE FUNCTION insight_nightly_due_companies(p_date DATE, p_limit INT DEFAULT 100)
RETURNS TABLE (company_id UUID)
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
    SELECT c.id
      FROM company c
     WHERE NOT EXISTS (SELECT 1 FROM insight_run r
                        WHERE r.company_id = c.id AND r.run_date = p_date AND r.kind = 'nightly')
     ORDER BY c.id
     LIMIT p_limit;
$$;
REVOKE ALL ON FUNCTION insight_nightly_due_companies(DATE, INT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION insight_nightly_due_companies(DATE, INT) TO tally_app;

COMMENT ON TABLE insight_voucher_event IS
    'W4. Real-time detector queue: one row per voucher_head move, written by '
    'trigger in the voucher''s own transaction, drained by the insight poller.';
COMMENT ON TABLE insight_run IS
    'W4. One row per (company, business day, kind): the nightly run''s '
    'idempotency record, inserted in the same transaction as its insights.';
COMMENT ON TABLE insight_digest IS
    'W4. The morning digest: at most five ranked insight ids and the narrative, '
    'with whether the model or the template narrated it.';
