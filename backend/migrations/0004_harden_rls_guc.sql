-- 0004_harden_rls_guc.sql
-- RED-TEAM HARDENING of tenant-isolation RLS.
--
-- The 0002 policy compared company_id to
--     current_setting('app.current_company_id', true)::uuid
-- Using missing_ok = true means an UNSET GUC yields NULL (predicate NULL ->
-- false -> zero rows), which is correct. BUT three adversarial / operational
-- states make the bare ::uuid cast THROW instead of denying cleanly:
--
--   * the GUC is RESET mid-transaction   -> current_setting returns ''  -> ''::uuid ERRORS
--   * the GUC is set to an empty string  -> ''::uuid ERRORS
--   * the GUC is set to a non-UUID value -> 'garbage'::uuid ERRORS
--
-- An error is not a cross-tenant leak, but it violates the system's
-- default-deny contract ("a missing/forged tenant context must yield ZERO
-- rows, never all rows, and never destabilise the query"). A query that aborts
-- on a benign RESET is also a denial-of-service foot-gun for the pooled
-- connection model. We therefore make the tenant predicate TOTAL: any
-- missing / empty / malformed GUC deterministically resolves to NULL, so the
-- policy yields zero rows. A well-formed but unknown/forged company id simply
-- matches no rows, exactly as before.
--
-- Implementation: a tiny IMMUTABLE helper that reads the GUC with missing_ok,
-- normalises '' to NULL, and casts defensively. Postgres has no try_cast for
-- uuid, so we validate the textual shape before casting; anything that is not a
-- canonical 8-4-4-4-12 hex UUID resolves to NULL (deny) rather than raising.

CREATE OR REPLACE FUNCTION app_current_company_id()
RETURNS uuid
LANGUAGE sql
STABLE
-- Reads only session GUC + a regex; no table access, so it is safe to mark
-- LEAKPROOF-adjacent. We keep it STABLE (GUC can change within a session).
AS $$
    SELECT CASE
        WHEN nullif(current_setting('app.current_company_id', true), '') ~*
             '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN nullif(current_setting('app.current_company_id', true), '')::uuid
        ELSE NULL
    END
$$;

COMMENT ON FUNCTION app_current_company_id() IS
    'Tenant scope resolver: returns the validated app.current_company_id GUC, '
    'or NULL when the GUC is unset/empty/malformed so RLS denies by default '
    'instead of erroring. Never returns a value the caller did not set.';

-- The app role must be able to evaluate the policy predicate.
GRANT EXECUTE ON FUNCTION app_current_company_id() TO tally_app;

-- Recreate every tenant policy to use the total resolver. Drop-then-create so
-- the migration is idempotent under the run-once tracker, and so both USING
-- (read/update/delete visibility) and WITH CHECK (insert/update target) are
-- pinned explicitly to the same predicate. Pinning WITH CHECK makes the
-- write-side guard intentional rather than relying on Postgres's implicit
-- "WITH CHECK defaults to USING" behaviour.
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'company', 'account_group', 'ledger', 'voucher_type',
        'voucher_version', 'posting', 'ledger_balance'
    ] LOOP
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I '
            || 'USING (company_id = app_current_company_id()) '
            || 'WITH CHECK (company_id = app_current_company_id())',
            t);
    END LOOP;
END
$$;
