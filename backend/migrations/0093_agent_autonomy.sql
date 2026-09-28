-- 0093 — the agent AUTONOMY table (W2, D121): (tenant, user, action,
-- amount_band) -> L0..L3, defaulting to L1 when no row matches.
--
-- ===========================================================================
-- THE LEVELS, AS internal/policy READS THEM
-- ===========================================================================
--   L0  the agent may not propose this action at all.
--   L1  the agent may propose; EVERY commit waits for a human decision.
--       ⛔ THIS IS THE DEFAULT, AND IT IS THE DEFAULT BY ABSENCE: no row means
--       L1, so a tenant that never opens this table gets PLAN-AGENTIC §2.1's
--       "no unapproved write" with nothing to configure and nothing to forget.
--   L2  the agent may commit without waiting, within the band, when every
--       argument is of trusted provenance; the human is shown it afterwards.
--   L3  as L2, and the after-the-fact notice is not required.
--
-- ⛔ NO LEVEL OVERRIDES THE STRUCTURAL RULES OR THE PROVENANCE RULE. A row that
-- says L3 for `delete sales` changes nothing (deletes are structurally denied to
-- an agent); a row that says L3 for `create payment` still parks for approval
-- when the payee or the amount came from a bank narration or a WhatsApp forward
-- (PLAN-AGENTIC §2.4, the CaMeL rule). Those are Go, in internal/policy, and a
-- tenant cannot configure them away from a table.
--
-- ===========================================================================
-- THE KEY
-- ===========================================================================
-- acl_key is the PDP's key ("create payment", "alter master"), not a route or
-- an ActionSpec id: the tenant writes ACL rules in these words, and one key is
-- one door (internal/actions/risk.go on why a finer key would let two routes the
-- PDP treats as one carry two autonomy levels).
--
-- user_id NULL is the tenant-wide row; a non-NULL row for a user beats it.
-- amount_max_paise is the band's INCLUSIVE upper bound in paise; NULL is
-- "any amount". A lookup takes the most specific user match, then the
-- narrowest band that still contains the amount. BIGINT paise, not NUMERIC
-- rupees: a band edge is an integer and comparing a computed paise amount to it
-- must never involve rounding.
--
-- This file restates no seeder and writes no ACL rule. Runs as the ADMIN DSN.

CREATE TABLE IF NOT EXISTS agent_autonomy (
    id                UUID PRIMARY KEY,
    company_id        UUID NOT NULL,
    user_id           UUID NULL,
    acl_key           TEXT NOT NULL,
    amount_max_paise  BIGINT NULL,
    level             SMALLINT NOT NULL,
    set_by            UUID NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT agent_autonomy_company_scope UNIQUE (company_id, id),
    -- One row per (tenant, user, key, band). NULLS NOT DISTINCT so the
    -- tenant-wide row (user_id NULL) and the any-amount band (NULL) are unique
    -- too — without it two "tenant-wide, any amount" rows with different levels
    -- could coexist and the lookup would pick one by accident.
    CONSTRAINT agent_autonomy_one_per_band
        UNIQUE NULLS NOT DISTINCT (company_id, user_id, acl_key, amount_max_paise),
    CONSTRAINT agent_autonomy_level CHECK (level BETWEEN 0 AND 3),
    CONSTRAINT agent_autonomy_band_non_negative
        CHECK (amount_max_paise IS NULL OR amount_max_paise >= 0),
    CONSTRAINT agent_autonomy_key_shape CHECK (acl_key ~ '^[a-z]+ [a-z0-9:-]+$')
);

CREATE INDEX IF NOT EXISTS agent_autonomy_lookup
    ON agent_autonomy (company_id, acl_key);

DO $$
BEGIN
    EXECUTE 'ALTER TABLE agent_autonomy ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE agent_autonomy FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON agent_autonomy';
    EXECUTE 'CREATE POLICY tenant_isolation ON agent_autonomy '
            'USING (company_id = app_current_company_id()) '
            'WITH CHECK (company_id = app_current_company_id())';
END
$$;

REVOKE ALL ON agent_autonomy FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON agent_autonomy TO tally_app;

COMMENT ON TABLE agent_autonomy IS
    'W2/D121. (company, user, acl_key, amount band) -> autonomy level L0..L3. '
    'No matching row means L1 (every commit waits for a human). No level '
    'overrides internal/policy''s structural rules or the untrusted-provenance '
    'rule for money-moving actions.';
COMMENT ON COLUMN agent_autonomy.amount_max_paise IS
    'Inclusive upper bound of the band, in paise. NULL = any amount.';
