-- 0002_core_schema.sql
-- Core multi-tenant, double-entry, immutable-versioned schema.
-- All money is NUMERIC(19,4). Every tenant table carries company_id and is
-- protected by default-deny, FORCE row-level security.

-- ---------------------------------------------------------------------------
-- Enumerated types
-- ---------------------------------------------------------------------------
CREATE TYPE account_nature AS ENUM ('ASSET', 'LIABILITY', 'INCOME', 'EXPENSE');
CREATE TYPE voucher_op    AS ENUM ('CREATE', 'ALTER', 'DELETE');
CREATE TYPE dr_cr         AS ENUM ('D', 'C');

-- ---------------------------------------------------------------------------
-- company (tenant root)
-- ---------------------------------------------------------------------------
CREATE TABLE company (
    id                UUID PRIMARY KEY,
    -- company rows are themselves scoped by id for RLS purposes: a session
    -- may only see the single company it is bound to.
    company_id        UUID NOT NULL,
    name              TEXT NOT NULL,
    parent_company_id UUID NULL,
    fy_start_date     DATE NOT NULL,
    base_currency     TEXT NOT NULL DEFAULT 'INR',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT company_self_scope CHECK (company_id = id)
);

-- ---------------------------------------------------------------------------
-- account_group: 28 seeded predefined Tally groups + user groups.
-- ---------------------------------------------------------------------------
CREATE TABLE account_group (
    id                    UUID PRIMARY KEY,
    company_id            UUID NOT NULL,
    name                  TEXT NOT NULL,
    parent_id             UUID NULL REFERENCES account_group (id),
    path                  LTREE NOT NULL,
    nature                account_nature NOT NULL,
    is_pl                 BOOLEAN NOT NULL,
    behaves_as_subledger  BOOLEAN NOT NULL DEFAULT false,
    nett_balances         BOOLEAN NOT NULL DEFAULT false,
    used_for_calc         BOOLEAN NOT NULL DEFAULT false,
    is_system             BOOLEAN NOT NULL DEFAULT false,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, name)
);
CREATE INDEX account_group_path_gist ON account_group USING GIST (path);
CREATE INDEX account_group_company   ON account_group (company_id);

-- ---------------------------------------------------------------------------
-- ledger: posting target. Cash & Profit & Loss A/c are seeded per company.
-- ---------------------------------------------------------------------------
CREATE TABLE ledger (
    id                    UUID PRIMARY KEY,
    company_id            UUID NOT NULL,
    name                  TEXT NOT NULL,
    group_id              UUID NOT NULL REFERENCES account_group (id),
    opening_balance       NUMERIC(19,4) NOT NULL DEFAULT 0,
    opening_dc            CHAR(1) NOT NULL DEFAULT 'D' CHECK (opening_dc IN ('D','C')),
    maintain_bill_by_bill BOOLEAN NOT NULL DEFAULT false,
    gstin                 TEXT NULL,
    pan                   TEXT NULL,
    credit_days           INTEGER NULL,
    is_system             BOOLEAN NOT NULL DEFAULT false,
    external_id           TEXT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, name)
);
CREATE INDEX ledger_company ON ledger (company_id);
CREATE INDEX ledger_group   ON ledger (group_id);

-- ---------------------------------------------------------------------------
-- voucher_type: F8 Sales, etc.
-- ---------------------------------------------------------------------------
CREATE TABLE voucher_type (
    id               UUID PRIMARY KEY,
    company_id       UUID NOT NULL,
    name             TEXT NOT NULL,
    parent_class     TEXT NOT NULL,
    numbering_method TEXT NOT NULL DEFAULT 'AUTOMATIC',
    prefix           TEXT NULL,
    suffix           TEXT NULL,
    width            INTEGER NULL,
    restart_rule     TEXT NULL,
    is_system        BOOLEAN NOT NULL DEFAULT false,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, name)
);
CREATE INDEX voucher_type_company ON voucher_type (company_id);

-- ---------------------------------------------------------------------------
-- voucher_version: APPEND-ONLY, hash-chained immutable log.
-- The logical voucher is identified by voucher_id; each create/alter/delete
-- appends a new immutable row with an incrementing version_no. The row hash is
-- BLAKE3 over (canonical payload || prev_hash), forming a tamper-evident chain
-- per logical voucher.
-- ---------------------------------------------------------------------------
CREATE TABLE voucher_version (
    id          UUID PRIMARY KEY,             -- uuidv7, minted app-side
    company_id  UUID NOT NULL,
    voucher_id  UUID NOT NULL,                -- logical voucher identity
    version_no  INTEGER NOT NULL,
    op          voucher_op NOT NULL,
    node_id     TEXT NOT NULL DEFAULT 'cored-0',
    prev_hash   BYTEA NULL,                   -- NULL for version 1
    hash        BYTEA NOT NULL,
    payload     JSONB NOT NULL,
    created_by  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, voucher_id, version_no)
);
CREATE INDEX voucher_version_company ON voucher_version (company_id);
CREATE INDEX voucher_version_voucher ON voucher_version (company_id, voucher_id);

-- Append-only guard: forbid UPDATE/DELETE on the immutable log.
CREATE OR REPLACE FUNCTION voucher_version_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'voucher_version is append-only; % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER voucher_version_no_mutate
    BEFORE UPDATE OR DELETE ON voucher_version
    FOR EACH ROW EXECUTE FUNCTION voucher_version_immutable();

-- ---------------------------------------------------------------------------
-- posting: immutable double-entry lines belonging to a voucher_version.
-- ---------------------------------------------------------------------------
CREATE TABLE posting (
    id                 UUID PRIMARY KEY,
    company_id         UUID NOT NULL,
    voucher_version_id UUID NOT NULL REFERENCES voucher_version (id),
    ledger_id          UUID NOT NULL REFERENCES ledger (id),
    dr_cr              dr_cr NOT NULL,
    amount             NUMERIC(19,4) NOT NULL CHECK (amount >= 0),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX posting_company ON posting (company_id);
CREATE INDEX posting_version ON posting (voucher_version_id);
CREATE INDEX posting_ledger  ON posting (ledger_id);

CREATE OR REPLACE FUNCTION posting_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'posting is immutable; % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER posting_no_mutate
    BEFORE UPDATE OR DELETE ON posting
    FOR EACH ROW EXECUTE FUNCTION posting_immutable();

-- ---------------------------------------------------------------------------
-- Deferred double-entry constraint: SUM(debit) = SUM(credit) per
-- voucher_version, checked at COMMIT so multi-line inserts validate atomically.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION check_double_entry()
RETURNS TRIGGER AS $$
DECLARE
    v_id     UUID;
    debit    NUMERIC(19,4);
    credit   NUMERIC(19,4);
BEGIN
    v_id := COALESCE(NEW.voucher_version_id, OLD.voucher_version_id);
    SELECT
        COALESCE(SUM(amount) FILTER (WHERE dr_cr = 'D'), 0),
        COALESCE(SUM(amount) FILTER (WHERE dr_cr = 'C'), 0)
      INTO debit, credit
      FROM posting
     WHERE voucher_version_id = v_id;
    IF debit <> credit THEN
        RAISE EXCEPTION
            'double-entry violation for voucher_version %: debit % <> credit %',
            v_id, debit, credit;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER posting_balanced
    AFTER INSERT ON posting
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_double_entry();

-- ---------------------------------------------------------------------------
-- ledger_balance: incrementally maintained running totals per ledger.
-- ---------------------------------------------------------------------------
CREATE TABLE ledger_balance (
    company_id UUID NOT NULL,
    ledger_id  UUID NOT NULL REFERENCES ledger (id),
    debit      NUMERIC(19,4) NOT NULL DEFAULT 0,
    credit     NUMERIC(19,4) NOT NULL DEFAULT 0,
    PRIMARY KEY (company_id, ledger_id)
);

-- ---------------------------------------------------------------------------
-- schema_migrations: applied-version bookkeeping for the migration runner.
-- Not tenant-scoped; no RLS.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny on EVERY tenant table.
-- ENABLE + FORCE so even the table owner is subject to the policy. The policy
-- uses current_setting('app.current_company_id', true) with missing_ok = true,
-- so an unset GUC yields NULL, the predicate is NULL (false), and zero rows
-- are visible. An unknown/forged company id simply matches no rows.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'company', 'account_group', 'ledger', 'voucher_type',
        'voucher_version', 'posting', 'ledger_balance'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING '
            || '(company_id = current_setting(''app.current_company_id'', true)::uuid)',
            t);
    END LOOP;
END
$$;

-- ---------------------------------------------------------------------------
-- Least-privilege grants for the application role. CRUD on tenant tables and
-- read on schema_migrations; explicitly NO truncate/references/trigger.
-- Append-only and immutability are enforced by triggers above, not grants, so
-- that the trigger emits a clear domain error rather than a permission denied.
-- ---------------------------------------------------------------------------
GRANT SELECT, INSERT, UPDATE, DELETE ON
    company, account_group, ledger, voucher_type,
    voucher_version, posting, ledger_balance
    TO tally_app;
GRANT SELECT ON schema_migrations TO tally_app;
