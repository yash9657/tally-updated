-- 0096_entity_facts_and_tenant_profile.sql
-- W3 (PLAN-AGENTIC.md §3): what the agent KNOWS about a business, as opposed
-- to what the books SAY. Three tables:
--
--   entity_facts    one number (or class) about one party / item / stream /
--                   the tenant, computed by a deterministic Go feature job
--                   (internal/profile), bi-temporal, with the evidence ids it
--                   was computed from.
--   entity_edges    one relationship between two entities (supplier supplies
--                   item, customer buys item), same temporal discipline.
--   tenant_profile  the ≤1.5k-token card the agent reads first, versioned,
--                   user-visible, with an operator-editable notes half.
--
-- ===========================================================================
-- ⛔ A FACT IS DERIVED, AND THE BOOKS ARE THE AUTHORITY. NOTHING READS A FACT
-- TO COMPUTE MONEY.
-- ===========================================================================
-- PLAN-AGENTIC §2.2: "No LLM-computed money. Go computes in paise." A fact is
-- a cache of a Go computation over the books, and it can be stale the moment a
-- voucher is posted. So: (1) every money fact names the report it summarises
-- in `reconciles_to`, and internal/profile's reconciliation test re-derives
-- each one from that report and fails on a one-paisa difference; (2) no write
-- path — no voucher, no payment, no reminder amount — may take a figure from
-- this table. A narrative may QUOTE a fact; an action must re-read the books.
--
-- ===========================================================================
-- WHY BI-TEMPORAL, AND WHAT THE TWO AXES MEAN HERE
-- ===========================================================================
-- VALID TIME [valid_from, valid_to): the business dates the fact is TRUE
--   for. valid_from is the as-of date it was computed at; valid_to is NULL
--   while it is the latest as-of, and is set (on a closed COPY, see below)
--   when a later as-of replaces it. "What was Acme's DSO at 31 March?" reads
--   valid time.
-- TRANSACTION TIME [recorded_at, superseded_at): when WE BELIEVED it.
--   superseded_at is set when a recompute replaces the row. "What did the
--   agent believe about Acme when it drafted that reminder on 2 April?" reads
--   transaction time — and that question is the one an AI action register
--   (PLAN-AGENTIC §2.6) has to be able to answer after the books moved.
--
-- ⛔ THE ONLY UPDATE THIS TABLE ACCEPTS IS superseded_at FROM NULL TO A TIME.
-- Everything else is immutable, enforced by the trigger below. Closing the
-- valid-time interval is done by superseding the open row and INSERTING a
-- closed copy, never by editing valid_to in place: an in-place edit would
-- rewrite what we believed at recorded_at, which is the one thing transaction
-- time exists to preserve.
--
-- ===========================================================================
-- WHY THERE IS NO ACL RULE AND NO seed_company_auth_defaults RESTATEMENT
-- ===========================================================================
-- No route reaches these tables in W3. They are written by internal/profile's
-- jobs inside an RLS-bound session and read (a) by the profile tools, which
-- W2's agent loop will call under the agent principal, and (b) through the
-- query DSL's `profile_facts` entity, which rides the EXISTING `query`
-- resource token and its existing rules. routeAction gains no arm; the seeder
-- census stays at the 17 PERFORMs it read after 0090, and that number is the
-- assertion that this file added no rule rather than forgot one. ⛔ When the
-- tenant-profile EDIT route arrives it must be a BARE resource token
-- (`agent-profile`), not a `report:NAME` — the 0091 row of PLAN-TALLY-PARITY
-- §2.5 says why (D95: a report name is GRANTED to every Data Entry level by
-- 0011's coarse `allow display report`).

-- ---------------------------------------------------------------------------
-- entity_facts
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS entity_facts (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,

    -- WHAT the fact is about. entity_key is the stable identity (a ledger or
    -- stock item id as text, a stream's derived key, or 'tenant'); entity_id
    -- is the same id typed, NULL for the tenant and for streams. entity_name is
    -- the DISPLAY name at compute time, denormalised on purpose: a renamed
    -- ledger must not rewrite what the agent was told last month.
    entity_kind   TEXT NOT NULL,
    entity_key    TEXT NOT NULL,
    entity_id     UUID NULL,
    entity_name   TEXT NOT NULL,

    -- fact_key is a CODE identifier from internal/profile's closed vocabulary
    -- ('receivable_outstanding', 'dso_days', 'abc_class' ...). Never free text.
    fact_key      TEXT NOT NULL,
    -- Exactly one of the two is set: a number, or a class from a closed
    -- vocabulary written by Go. value_num is NUMERIC(19,4) because money
    -- facts are money; days and quantities share the column and `unit` says
    -- which.
    value_num     NUMERIC(19,4) NULL,
    value_text    TEXT NULL,
    unit          TEXT NOT NULL,

    -- WHO asserted it. 'computed' = a deterministic Go job (everything W3
    -- writes). 'user' = an operator correction. 'import' = the cold-start
    -- miner over a Tally import. 'model' = an LLM-written fact, which W3 does
    -- not write and which no money path may ever read.
    source        TEXT NOT NULL,
    job           TEXT NOT NULL,
    job_version   TEXT NOT NULL,

    -- The evidence window the job read, and the valid-time interval.
    window_from   DATE NULL,
    valid_from    DATE NOT NULL,
    valid_to      DATE NULL,
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at TIMESTAMPTZ NULL,

    -- The ids the fact was computed from (voucher ids, bank line ids), so
    -- "why does the agent think Acme pays late?" is answered by the vouchers
    -- and not by the agent. Capped: a fact over 10,000 vouchers keeps the ones
    -- that decided it (internal/profile says which), not all of them.
    evidence_kind TEXT NOT NULL DEFAULT 'none',
    evidence_ids  UUID[] NOT NULL DEFAULT '{}',

    -- The report NAME (authz.KnownReportNames) a money fact must reconcile to,
    -- or NULL for a statistic no report prints (a p90 lag, an XYZ class).
    reconciles_to TEXT NULL,

    CONSTRAINT entity_facts_company_scope UNIQUE (company_id, id),
    CONSTRAINT entity_facts_kind_known
        CHECK (entity_kind IN ('party', 'item', 'tenant', 'stream')),
    CONSTRAINT entity_facts_source_known
        CHECK (source IN ('computed', 'user', 'import', 'model')),
    CONSTRAINT entity_facts_unit_known
        CHECK (unit IN ('inr', 'days', 'qty', 'qty_per_day', 'pct', 'count', 'ratio', 'class')),
    CONSTRAINT entity_facts_evidence_kind_known
        CHECK (evidence_kind IN ('none', 'voucher', 'bank_line')),
    CONSTRAINT entity_facts_exactly_one_value
        CHECK ((value_num IS NULL) <> (value_text IS NULL)),
    CONSTRAINT entity_facts_class_is_text
        CHECK ((unit = 'class') = (value_text IS NOT NULL)),
    CONSTRAINT entity_facts_valid_ordered
        CHECK (valid_to IS NULL OR valid_from < valid_to),
    CONSTRAINT entity_facts_window_ordered
        CHECK (window_from IS NULL OR window_from <= valid_from),
    CONSTRAINT entity_facts_superseded_after_recorded
        CHECK (superseded_at IS NULL OR superseded_at >= recorded_at),
    CONSTRAINT entity_facts_evidence_capped
        CHECK (cardinality(evidence_ids) <= 50),
    CONSTRAINT entity_facts_tenant_has_no_id
        CHECK ((entity_kind IN ('tenant', 'stream')) = (entity_id IS NULL))
);

-- ONE BELIEF PER (entity, fact, valid_from) AT A TIME. A recompute at the same
-- as-of supersedes; two concurrent jobs racing to write the same fact collide
-- here instead of both becoming "current".
CREATE UNIQUE INDEX IF NOT EXISTS entity_facts_one_current
    ON entity_facts (company_id, entity_kind, entity_key, fact_key, valid_from)
    WHERE superseded_at IS NULL;
CREATE INDEX IF NOT EXISTS entity_facts_lookup
    ON entity_facts (company_id, entity_kind, entity_key)
    WHERE superseded_at IS NULL AND valid_to IS NULL;

COMMENT ON TABLE entity_facts IS
    'W3. A deterministic fact about a party/item/stream/the tenant, computed by '
    'internal/profile from the books. Bi-temporal: [valid_from, valid_to) is the '
    'business time it is true for, [recorded_at, superseded_at) is when we '
    'believed it. Only superseded_at may ever be updated. A cache of the books, '
    'never an input to money: reconciles_to names the report it summarises.';
COMMENT ON COLUMN entity_facts.source IS
    'computed (a Go job) | user (operator correction) | import (cold-start miner) '
    '| model (LLM-written; never read by a money path).';
COMMENT ON COLUMN entity_facts.evidence_ids IS
    'The voucher / bank-line ids the fact was computed from, capped at 50.';
COMMENT ON COLUMN entity_facts.reconciles_to IS
    'The report name (authz.KnownReportNames) a money fact must agree with to '
    'the paisa, or NULL for a statistic no report prints.';

-- ---------------------------------------------------------------------------
-- entity_edges
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS entity_edges (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    src_kind      TEXT NOT NULL,
    src_key       TEXT NOT NULL,
    src_name      TEXT NOT NULL,
    dst_kind      TEXT NOT NULL,
    dst_key       TEXT NOT NULL,
    dst_name      TEXT NOT NULL,
    edge_kind     TEXT NOT NULL,
    -- The strength of the edge in the edge's own unit (for supplies_item /
    -- buys_item: the quantity moved in the window). A count or a quantity,
    -- never a price.
    weight        NUMERIC(19,4) NOT NULL,
    source        TEXT NOT NULL,
    job           TEXT NOT NULL,
    job_version   TEXT NOT NULL,
    window_from   DATE NULL,
    valid_from    DATE NOT NULL,
    valid_to      DATE NULL,
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at TIMESTAMPTZ NULL,
    evidence_ids  UUID[] NOT NULL DEFAULT '{}',

    CONSTRAINT entity_edges_company_scope UNIQUE (company_id, id),
    CONSTRAINT entity_edges_kind_known
        CHECK (src_kind IN ('party', 'item', 'tenant', 'stream')
           AND dst_kind IN ('party', 'item', 'tenant', 'stream')),
    CONSTRAINT entity_edges_edge_kind_known
        CHECK (edge_kind IN ('supplies_item', 'buys_item')),
    CONSTRAINT entity_edges_source_known
        CHECK (source IN ('computed', 'user', 'import', 'model')),
    CONSTRAINT entity_edges_valid_ordered
        CHECK (valid_to IS NULL OR valid_from < valid_to),
    CONSTRAINT entity_edges_superseded_after_recorded
        CHECK (superseded_at IS NULL OR superseded_at >= recorded_at),
    CONSTRAINT entity_edges_evidence_capped
        CHECK (cardinality(evidence_ids) <= 50),
    CONSTRAINT entity_edges_weight_non_negative CHECK (weight >= 0)
);
CREATE UNIQUE INDEX IF NOT EXISTS entity_edges_one_current
    ON entity_edges (company_id, edge_kind, src_key, dst_key, valid_from)
    WHERE superseded_at IS NULL;

COMMENT ON TABLE entity_edges IS
    'W3. A relationship between two entities (supplier supplies item, customer '
    'buys item), same bi-temporal discipline as entity_facts.';

-- ---------------------------------------------------------------------------
-- tenant_profile
-- ---------------------------------------------------------------------------
-- One VERSIONED row per recompute or edit. The card the agent reads is
-- `body` (computed half) + `user_notes` (the operator's own words), rendered
-- by internal/profile and bounded by token_estimate <= 1500.
--
-- ⛔ user_notes IS UNTRUSTED FOR THE SAME REASON A NARRATION IS. It is typed by
-- a person, it reaches the model verbatim, and the person may not be the
-- owner. internal/profile renders it inside a delimited, labelled block and W2
-- must carry that provenance label (PLAN-AGENTIC §2.4). It can steer tone and
-- vocabulary; it can never raise an autonomy level or name an amount.
CREATE TABLE IF NOT EXISTS tenant_profile (
    id             UUID PRIMARY KEY,
    company_id     UUID NOT NULL,
    version        INTEGER NOT NULL,
    as_of          DATE NOT NULL,
    body           TEXT NOT NULL,
    body_json      JSONB NOT NULL,
    user_notes     TEXT NOT NULL DEFAULT '',
    token_estimate INTEGER NOT NULL,
    job_version    TEXT NOT NULL,
    edited_by      TEXT NULL,
    recorded_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at  TIMESTAMPTZ NULL,

    CONSTRAINT tenant_profile_company_scope UNIQUE (company_id, id),
    CONSTRAINT tenant_profile_one_per_version UNIQUE (company_id, version),
    CONSTRAINT tenant_profile_version_positive CHECK (version >= 1),
    CONSTRAINT tenant_profile_token_cap
        CHECK (token_estimate >= 0 AND token_estimate <= 1500),
    CONSTRAINT tenant_profile_notes_bounded
        CHECK (char_length(user_notes) <= 2000),
    CONSTRAINT tenant_profile_superseded_after_recorded
        CHECK (superseded_at IS NULL OR superseded_at >= recorded_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS tenant_profile_one_current
    ON tenant_profile (company_id) WHERE superseded_at IS NULL;

COMMENT ON TABLE tenant_profile IS
    'W3. The tenant card (<=1.5k tokens): a computed body plus operator notes, '
    'versioned. Only superseded_at may be updated; an edit is a new version.';
COMMENT ON COLUMN tenant_profile.token_estimate IS
    'internal/profile.EstimateTokens of the rendered card: a deliberately '
    'CONSERVATIVE upper bound (ASCII/3 + one per non-ASCII rune), not a '
    'tokenizer. The CHECK is the cap; W1b''s tokenizer choice may tighten it.';

-- ---------------------------------------------------------------------------
-- Immutability: only superseded_at, NULL -> a time.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION w3_only_supersede()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION '% is append-only; DELETE is not permitted', TG_TABLE_NAME;
    END IF;
    IF OLD.superseded_at IS NOT NULL THEN
        RAISE EXCEPTION '% row % is already superseded', TG_TABLE_NAME, OLD.id;
    END IF;
    IF NEW.superseded_at IS NULL THEN
        RAISE EXCEPTION '% row %: the only permitted UPDATE sets superseded_at', TG_TABLE_NAME, OLD.id;
    END IF;
    -- Every other column must be unchanged. Comparing the whole row with
    -- superseded_at blanked on both sides is the one spelling that cannot
    -- forget a column added later.
    IF (to_jsonb(NEW) - 'superseded_at') IS DISTINCT FROM (to_jsonb(OLD) - 'superseded_at') THEN
        RAISE EXCEPTION '% row %: only superseded_at may change', TG_TABLE_NAME, OLD.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS entity_facts_only_supersede ON entity_facts;
CREATE TRIGGER entity_facts_only_supersede
    BEFORE UPDATE OR DELETE ON entity_facts
    FOR EACH ROW EXECUTE FUNCTION w3_only_supersede();
DROP TRIGGER IF EXISTS entity_edges_only_supersede ON entity_edges;
CREATE TRIGGER entity_edges_only_supersede
    BEFORE UPDATE OR DELETE ON entity_edges
    FOR EACH ROW EXECUTE FUNCTION w3_only_supersede();
DROP TRIGGER IF EXISTS tenant_profile_only_supersede ON tenant_profile;
CREATE TRIGGER tenant_profile_only_supersede
    BEFORE UPDATE OR DELETE ON tenant_profile
    FOR EACH ROW EXECUTE FUNCTION w3_only_supersede();

-- ---------------------------------------------------------------------------
-- RLS: ENABLE and FORCE, USING and WITH CHECK — the 0088 shape.
-- ---------------------------------------------------------------------------
DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['entity_facts', 'entity_edges', 'tenant_profile'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I '
                       'USING (company_id = app_current_company_id()) '
                       'WITH CHECK (company_id = app_current_company_id())', t);
    END LOOP;
END
$$;

-- No DELETE grant: the trigger refuses it anyway, and a missing grant is the
-- second, independent wall (the intent-log shape HANDOFF-AGENTIC §8 asks for).
GRANT SELECT, INSERT, UPDATE ON entity_facts   TO tally_app;
GRANT SELECT, INSERT, UPDATE ON entity_edges   TO tally_app;
GRANT SELECT, INSERT, UPDATE ON tenant_profile TO tally_app;
