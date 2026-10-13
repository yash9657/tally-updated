-- 0091_llm_token_budget.sql
-- THE PER-TENANT TOKEN BUDGET. W0 (PLAN-AGENTIC.md §3) deliverable D1's second
-- half: "per-tenant token budget reserve/settle". One row per
-- (company, month, feature); internal/llm's PGBudget is the only writer.
--
-- ===========================================================================
-- ⛔ WHY THE NUMBER IS 0091 AND NOT ONE OF THE FREE 0081-0084
-- ===========================================================================
-- PLAN-TALLY-PARITY.md §2.5 records 0081-0084 as permanently free, and its rule
-- is explicit: "a file that restates the seeder takes a number at the END; a
-- file that only creates a TABLE may take a reserved one." This file creates
-- only a table and restates NO seeder, so it could have taken 0081 — and does
-- not, because 0090 is the highest applied number and taking a gap for a table
-- that needs no gap spends a reservation for nothing. 0081-0084 stay free.
--
-- ===========================================================================
-- ⛔ WHY THERE IS NO seed_company_auth_defaults RESTATEMENT IN THIS FILE
-- ===========================================================================
-- Every migration that adds a `report:NAME` must write Data Entry a `disallow`,
-- because 0011's coarse `allow display report` reaches a BARE resource and
-- internal/authz matches it against ANY report name -- so the GRANT is the
-- default and the REFUSAL is what must be written (defect D95). Thirteen
-- migrations do it: 0026, 0028, 0033, 0035, 0046, 0063, 0068, 0072, 0073, 0076,
-- 0086, 0087 and 0090.
--
-- ⭐ THIS FILE ADDS NO REPORT NAME AND NO RESOURCE TOKEN, so it adds no rule --
-- and the reason is worth stating, because "no ACL migration" is exactly the
-- shape of an oversight:
--
--   * NO ROUTE REACHES THIS TABLE IN W0. It is written by internal/llm's
--     PGBudget, in the same transaction-scoped, RLS-bound session as the
--     feature that spent the tokens, and it is read by nothing an operator can
--     request. `routeAction` therefore gains no arm, and an unmapped
--     {companyID} sub-path is default-DENY (auth_middleware.go's terminal
--     `return "", "", "", false`), so there is nothing to permit or refuse.
--
--   * ⛔ AND WHEN A ROUTE DOES ARRIVE -- a spend report, an admin cap editor --
--     IT MUST NOT BE A `report:NAME`. A bare resource token (`llm-budget`) is
--     refused with ZERO acl_rule rows, because the seeded Data Entry level's
--     `default_access` is `none` and every rule names a specific token. That is
--     the authority `tds-challan`, `tds-profile`, `forex-settings`, `backup`
--     and `e-way-bill` all ship under. A report name would be the OPPOSITE case
--     and would make an ACL migration MANDATORY. Choose the token shape
--     deliberately then, as T6 did, rather than by analogy with whatever
--     report sits next to it in the menu.
--
-- The seeder census (backend/migrations/seeder_census_test.go) therefore still
-- reads the same 17 PERFORMs it read after 0090, unchanged, and that number is
-- the assertion that this file added no rule rather than forgot one.
--
-- ===========================================================================
-- ⛔ TOKENS ARE BIGINT. THERE IS NO MONEY COLUMN IN THIS TABLE, ON PURPOSE.
-- ===========================================================================
-- Counts are BIGINT: a token count is a count, and NUMERIC would invite
-- fractional tokens while DOUBLE PRECISION would lose exactness somewhere past
-- 2^53 for no benefit at all. Nothing here is money, so nothing here is
-- NUMERIC(19,4).
--
-- ⭐ THE ABSENT COST COLUMN IS A DECISION, NOT AN OMISSION. Storing rupees per
-- period would mean multiplying tokens by a price, and the price is a function
-- of the provider, the model id, whether the prefix was cached, and the date --
-- four things this table does not know and one it cannot know retrospectively.
-- A stored figure computed from a stale price list is worse than no figure: it
-- reads as authoritative, it is quoted in a review, and nothing about it ever
-- looks wrong. Cost belongs where the FINGERPRINT lives -- the trajectory store,
-- which records which model actually served each call (see
-- internal/llm/response.go's Fingerprint, and D3 on Fireworks changing weights
-- behind an id). This table answers "how many tokens has this tenant spent this
-- month?", which is the question a cap needs answered.
--
-- ===========================================================================
-- WHY THE ROW IS (company, month, feature) AND NOT (company, month)
-- ===========================================================================
-- The cap that matters operationally is per tenant, but the DIAGNOSIS is always
-- per feature: "the bill tripled" is answered by which feature moved, and a
-- single per-tenant counter cannot answer it. Splitting by feature costs one
-- extra key column and gives §6's "steps and rupees per task class" a source.
-- The tenant-wide cap is then SUM(...) over the tenant's rows for the period --
-- computed, not stored, for the same reason order fulfilment is derived in 0042:
-- a stored total goes stale silently the first time a row beneath it changes.

CREATE TABLE IF NOT EXISTS llm_token_budget (
    id           UUID PRIMARY KEY,
    company_id   UUID NOT NULL,

    -- The window. The FIRST DAY of a calendar month, UTC -- see
    -- internal/llm/budget.go's periodStart for why this is UTC and not
    -- Asia/Kolkata: a token cap is infrastructure, not a statutory period, and
    -- sharing a clock with the books would invite somebody to "align" the
    -- books' financial year to this one.
    period_start DATE NOT NULL,

    -- The call site: 'nlq.translate', 'bill.extract', 'agent.plan'. Never a
    -- tenant name, a party or anything a person typed -- this column is a
    -- CODE identifier, and it is read by dashboards that group across tenants.
    feature      TEXT NOT NULL,

    -- The cap for this (company, period, feature). Seeded from
    -- PGBudget.DefaultLimitTokens on the first spend of a window and raised or
    -- lowered per tenant by UPDATE. ⛔ There is no "unlimited" encoding: a
    -- deployment that wants no cap wires internal/llm's NoBudget, which says so
    -- in Go, rather than storing a sentinel here that every reader would have
    -- to remember to special-case.
    limit_tokens BIGINT NOT NULL,

    -- IN FLIGHT. Raised by Reserve before the provider call and returned by
    -- Settle or Release after it. ⛔ THIS COLUMN IS THE WHOLE POINT OF THE
    -- TABLE. Counting tokens only after they are served cannot stop a burst:
    -- N concurrent calls all read the same under-cap total and all proceed.
    -- The reservation is taken under this row's UPDATE lock, so concurrency
    -- serialises in the database rather than in hope.
    reserved_tokens BIGINT NOT NULL DEFAULT 0,

    -- SERVED. Settled from the provider's own usage block.
    prompt_tokens        BIGINT NOT NULL DEFAULT 0,
    -- A SUBSET of prompt_tokens, not an addition to it: every provider that
    -- reports a cached prefix reports it nested inside the prompt count. Stored
    -- separately so the Static/Dynamic ordering contract
    -- (internal/llm/request.go) has a measurable payoff -- without this column,
    -- "did the prefix cache work?" is unanswerable after the fact.
    cached_prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens    BIGINT NOT NULL DEFAULT 0,

    -- Calls that were SERVED. ⛔ Not incremented on Release: a call that never
    -- reached the model is not a call, and counting it would deflate every
    -- tokens-per-call figure computed from this table -- in the flattering
    -- direction.
    calls    BIGINT NOT NULL DEFAULT 0,
    -- Reservations handed back unspent: timeouts, cancellations, non-2xx.
    -- Kept because a feature whose released count approaches its calls count is
    -- a feature failing upstream, and that is visible here before it is visible
    -- anywhere else.
    released BIGINT NOT NULL DEFAULT 0,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- ONE row per window per feature. This is the constraint Reserve's
    -- ON CONFLICT targets, so two concurrent first-calls of a month resolve to
    -- one row instead of one row and one unique violation.
    CONSTRAINT llm_token_budget_one_per_window UNIQUE (company_id, period_start, feature),
    -- The company-scoped uniqueness every tenant table in this schema carries,
    -- so a future table can FK to this one company-scoped (0040 added the same
    -- to `posting` for exactly that reason).
    CONSTRAINT llm_token_budget_company_scope UNIQUE (company_id, id),

    -- The window is a MONTH, named by its first day. Without this check a
    -- caller with a subtly different periodStart (say, a Monday) would create a
    -- second row for the same month and both would be under the cap.
    --
    -- ⚠️ EXTRACT(DAY ...) AND NOT date_trunc('month', period_start)::date. The
    -- obvious spelling resolves to date_trunc(text, TIMESTAMPTZ), which is
    -- STABLE rather than IMMUTABLE because it reads the session TimeZone -- so
    -- the same row could satisfy the constraint on one connection and not on
    -- another, and the difference would show up as a random insert failure
    -- around midnight IST. A day-of-month test needs no timezone at all.
    CONSTRAINT llm_token_budget_period_is_month_start
        CHECK (EXTRACT(DAY FROM period_start) = 1),
    CONSTRAINT llm_token_budget_feature_not_blank
        CHECK (length(btrim(feature)) > 0),
    CONSTRAINT llm_token_budget_limit_positive
        CHECK (limit_tokens > 0),
    -- ⛔ THE NON-NEGATIVE CHECKS ARE NOT DECORATION. A double Settle, or a
    -- Settle after a Release, would subtract the same estimate twice and drive
    -- reserved_tokens negative -- at which point the cap silently gains
    -- headroom and the tenant overruns quietly. internal/llm's Release/Settle
    -- floor the subtraction at zero AND mark the reservation done; this check
    -- is the database refusing to be the place where that bug goes unnoticed.
    CONSTRAINT llm_token_budget_reserved_non_negative
        CHECK (reserved_tokens >= 0),
    CONSTRAINT llm_token_budget_prompt_non_negative
        CHECK (prompt_tokens >= 0),
    CONSTRAINT llm_token_budget_completion_non_negative
        CHECK (completion_tokens >= 0),
    CONSTRAINT llm_token_budget_calls_non_negative
        CHECK (calls >= 0),
    CONSTRAINT llm_token_budget_released_non_negative
        CHECK (released >= 0),
    -- Cached prompt tokens are a SUBSET of prompt tokens. A provider (or an
    -- adapter) reporting otherwise would make cached/prompt exceed 1 in every
    -- report built on this table, which is the kind of number that gets quoted
    -- once and doubted forever.
    CONSTRAINT llm_token_budget_cached_within_prompt
        CHECK (cached_prompt_tokens >= 0 AND cached_prompt_tokens <= prompt_tokens)
);

-- The read path: "what has this company spent this month, across features?" --
-- which is the tenant-wide cap, computed rather than stored (see the header).
CREATE INDEX IF NOT EXISTS llm_token_budget_company_period
    ON llm_token_budget (company_id, period_start);

COMMENT ON TABLE llm_token_budget IS
    'W0/D1. The per-tenant LLM token budget: one row per (company, calendar '
    'month, feature), written by internal/llm''s PGBudget under RESERVE-THEN-'
    'SETTLE. ⛔ IT HOLDS NO MONEY COLUMN, DELIBERATELY -- a rupee figure needs a '
    'price that depends on provider, model id, cache state and date, and a '
    'stored one computed from a stale price list reads as authoritative forever. '
    'Cost belongs with the trajectory store, which records which model actually '
    'served each call. See the file header.';

COMMENT ON COLUMN llm_token_budget.id IS
    'UUIDv7, minted app-side (uuid.NewV7 in internal/llm/budget.go), matching '
    'every other tenant table in this schema.';

COMMENT ON COLUMN llm_token_budget.company_id IS
    'The tenant. RLS scopes every read and write to app_current_company_id(), '
    'and internal/llm additionally refuses a Reserve whose argument disagrees '
    'with the company in context (ErrBudgetScopeMismatch) -- the check that '
    'stops company A''s session billing company B.';

COMMENT ON COLUMN llm_token_budget.period_start IS
    'First day of the calendar month this window covers, UTC. UTC and NOT '
    'Asia/Kolkata: a token cap is infrastructure, not a statutory period, and '
    'sharing a clock with the books invites somebody to align the wrong one.';

COMMENT ON COLUMN llm_token_budget.feature IS
    'The call site (''nlq.translate'', ''bill.extract'', ''agent.plan''). A CODE '
    'identifier, never anything a person typed: dashboards group on it across '
    'tenants. Per-feature rows exist because the cap is per tenant but the '
    'diagnosis of a tripled bill is always per feature.';

COMMENT ON COLUMN llm_token_budget.limit_tokens IS
    'The cap for this window and feature, seeded from PGBudget.'
    'DefaultLimitTokens on first spend and adjusted per tenant by UPDATE. There '
    'is no ''unlimited'' sentinel: a deployment with no cap wires NoBudget.';

COMMENT ON COLUMN llm_token_budget.reserved_tokens IS
    'IN FLIGHT. Raised by Reserve BEFORE the provider call and returned by '
    'Settle or Release after it, on every exit path including panic and context '
    'cancellation. ⛔ Counting tokens only after they are served cannot stop a '
    'burst -- N concurrent calls would all read the same under-cap total and all '
    'proceed. The conditional UPDATE that raises this column is what serialises '
    'them.';

COMMENT ON COLUMN llm_token_budget.prompt_tokens IS
    'SERVED input. The TOTAL prompt tokens the provider BILLED for this window '
    'and feature, settled from its own usage block -- never an estimate, which '
    'is what reserved_tokens holds until the real figure arrives. '
    '⛔ cached_prompt_tokens IS A PART OF THIS NUMBER, NOT AN ADDITION TO IT, and '
    'the cap arithmetic depends on it: internal/llm/budget.go admits a '
    'reservation only while reserved + prompt_tokens + completion_tokens stays '
    'within limit_tokens, so a writer that also added the cached count would '
    'bill the cached prefix twice and shrink every tenant''s real cap silently.';

COMMENT ON COLUMN llm_token_budget.cached_prompt_tokens IS
    'The part of prompt_tokens served from the provider''s cached prefix -- a '
    'SUBSET, never an addition. It is the only after-the-fact evidence that the '
    'Static/Dynamic message ordering in internal/llm/request.go is doing its '
    'job.';

COMMENT ON COLUMN llm_token_budget.completion_tokens IS
    'SERVED output: tokens the provider GENERATED. Kept as its own column and '
    'not folded into a single ''tokens'' total because every provider in '
    'internal/llm prices input and output differently -- a merged total could '
    'not be turned back into a rupee figure by the trajectory store, which is '
    'where cost is computed (see the file header on the absent cost column).';

COMMENT ON COLUMN llm_token_budget.calls IS
    'Calls the provider SERVED. Not incremented on Release: a call that never '
    'reached the model is not a call, and counting it would deflate every '
    'tokens-per-call figure computed from this table.';

COMMENT ON COLUMN llm_token_budget.released IS
    'Reservations handed back unspent (timeout, cancellation, non-2xx). A '
    'feature whose released count approaches its calls count is failing '
    'upstream, and that shows here before it shows anywhere else.';

COMMENT ON COLUMN llm_token_budget.created_at IS
    'When this (company, month, feature) window was OPENED -- the first spend of '
    'it, since Reserve''s INSERT ... ON CONFLICT DO NOTHING creates the row and '
    'no statement ever writes this column again. It is only useful next to '
    'updated_at: the pair is what separates a window that opened and went idle '
    'from one whose reservation is stuck.';

COMMENT ON COLUMN llm_token_budget.updated_at IS
    'Touched by every Reserve, Settle and Release. Not an audit trail -- the '
    'trajectory store is -- but it is what identifies a stuck reservation: a row '
    'with reserved_tokens > 0 whose updated_at is hours old.';

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, policy with BOTH USING
-- and WITH CHECK -- the same block 0062 and 0088 write, for the same reasons.
-- USING alone would let a session write a row for another company that it can
-- never see or correct; WITH CHECK alone would leak every tenant's spend on
-- read. FORCE because migrations run as the table owner and an owner bypasses
-- its own policies unless forced.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    EXECUTE 'ALTER TABLE llm_token_budget ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE llm_token_budget FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON llm_token_budget';
    EXECUTE 'CREATE POLICY tenant_isolation ON llm_token_budget '
            'USING (company_id = app_current_company_id()) '
            'WITH CHECK (company_id = app_current_company_id())';
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON llm_token_budget TO tally_app;
