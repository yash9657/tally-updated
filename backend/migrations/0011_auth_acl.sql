-- 0011_auth_acl.sql
-- AUTH + ACL CATALOG (Task D, WS1). Closes the unauthenticated seam: today a
-- caller's company comes ONLY from the {companyID} URL path param, so any caller
-- can touch any company. This migration adds the Postgres-side catalog the PDP
-- evaluates on every request:
--
--   * new GUCs + total resolvers app_current_user_id() / app_current_subject(),
--     mirroring app_current_company_id() (0004): a missing/empty/malformed GUC
--     resolves to NULL (default-deny) instead of erroring.
--   * app_user: the global user directory, RLS-scoped so a verified caller sees
--     ONLY their own row (subject = their JWT sub, OR id = the user GUC).
--   * security_level + acl_rule: per-company Tally Disallow/Allow levels, RLS on
--     the company GUC (read AFTER membership is confirmed for {companyID}).
--   * company_membership: the membership oracle, RLS on the user GUC (a user sees
--     only their own memberships).
--   * webauthn_credential: stored WebAuthn public keys (SPKI DER), RLS on user.
--   * auth_login_attempt: the lockout audit trail. Written DURING pre-auth login
--     (before any user/company GUC is set), so it is NOT RLS-scoped; it is a
--     bigserial-PK table, so tally_app needs USAGE on the implicit sequence too.
--
-- Every new RLS table gets EXPLICIT WITH CHECK = USING (mirroring 0004) so the
-- write-side guard is intentional, and the inserting tx is required to set the
-- matching GUC first (subject for app_user, company for security_level/acl_rule,
-- user for company_membership/webauthn_credential).
--
-- This file runs as the ADMIN DSN, and is the SOLE owner of all auth DDL.

-- ---------------------------------------------------------------------------
-- 1. New GUC resolvers (mirror app_current_company_id() from 0004).
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION app_current_user_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
    SELECT CASE
        WHEN nullif(current_setting('app.current_user_id', true), '') ~*
             '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN nullif(current_setting('app.current_user_id', true), '')::uuid
        ELSE NULL
    END
$$;

COMMENT ON FUNCTION app_current_user_id() IS
    'Auth scope resolver: returns the validated app.current_user_id GUC, or NULL '
    'when unset/empty/malformed so user-scoped RLS denies by default.';

GRANT EXECUTE ON FUNCTION app_current_user_id() TO tally_app;

CREATE OR REPLACE FUNCTION app_current_subject()
RETURNS text
LANGUAGE sql
STABLE
AS $$
    SELECT nullif(current_setting('app.current_subject', true), '')
$$;

COMMENT ON FUNCTION app_current_subject() IS
    'Auth scope resolver: returns the JWT subject from the app.current_subject '
    'GUC, or NULL when unset/empty so subject-scoped RLS denies by default.';

GRANT EXECUTE ON FUNCTION app_current_subject() TO tally_app;

-- ---------------------------------------------------------------------------
-- 2. Catalog tables.
-- ---------------------------------------------------------------------------

-- app_user: global user directory. A verified caller sees ONLY their own row
-- (matched by JWT subject OR by the user GUC). The create-on-first-login INSERT
-- runs under the subject GUC, so subject = app_current_subject() holds.
CREATE TABLE app_user (
    id              UUID PRIMARY KEY,
    subject         TEXT NOT NULL UNIQUE,   -- JWT sub claim
    email           TEXT NOT NULL DEFAULT '',
    display_name    TEXT NOT NULL DEFAULT '',
    is_active       BOOLEAN NOT NULL DEFAULT true,
    locked_until    TIMESTAMPTZ NULL,
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- security_level: a Tally Disallow/Allow level scoped to one company. The Owner
-- level (is_owner=true, default_access='full', requires_webauthn=true) and a
-- 'Data Entry' level are seeded by seed_company_auth_defaults().
CREATE TABLE security_level (
    id               UUID PRIMARY KEY,
    company_id       UUID NOT NULL,
    name             TEXT NOT NULL,
    is_owner         BOOLEAN NOT NULL DEFAULT false,
    default_access   TEXT NOT NULL DEFAULT 'none' CHECK (default_access IN ('full', 'none')),
    requires_webauthn BOOLEAN NOT NULL DEFAULT false,
    backdated_cutoff DATE NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, name)
);

-- acl_rule: one Disallow/Allow rule for a security level. action/resource use
-- the canonical lowercase tokens the PDP matches; '*' is the wildcard.
CREATE TABLE acl_rule (
    id                UUID PRIMARY KEY,
    company_id        UUID NOT NULL,
    security_level_id UUID NOT NULL REFERENCES security_level(id),
    effect            TEXT NOT NULL CHECK (effect IN ('allow', 'disallow')),
    action            TEXT NOT NULL,   -- create|alter|delete|display|print|export|*
    resource          TEXT NOT NULL,   -- sales|purchase|...|master|report|import|sync|* (or report:NAME)
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- company_membership: the membership oracle. A user sees only their own
-- memberships (RLS on the user GUC). PRIMARY KEY (user_id, company_id) means a
-- user has exactly one security level per company.
CREATE TABLE company_membership (
    user_id           UUID NOT NULL REFERENCES app_user(id),
    company_id        UUID NOT NULL,
    security_level_id UUID NOT NULL REFERENCES security_level(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, company_id)
);

-- webauthn_credential: a registered WebAuthn credential for a user. public_key
-- is SPKI DER (COSE-decoded into a stdlib key by WS2). RLS on the user GUC.
CREATE TABLE webauthn_credential (
    id            UUID PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES app_user(id),
    credential_id BYTEA NOT NULL UNIQUE,
    public_key    BYTEA NOT NULL,
    alg           INTEGER NOT NULL,   -- -7 ES256, -8 EdDSA
    sign_count    BIGINT NOT NULL DEFAULT 0,
    aaguid        BYTEA,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- auth_login_attempt: the lockout audit trail. Written during PRE-AUTH login
-- (before any user/company is established), so it has NO RLS. bigserial PK.
CREATE TABLE auth_login_attempt (
    id        BIGSERIAL PRIMARY KEY,
    subject   TEXT NOT NULL,
    succeeded BOOLEAN NOT NULL,
    ip        TEXT NOT NULL DEFAULT '',
    at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX auth_login_attempt_subject_at
    ON auth_login_attempt (subject, at DESC);

-- ---------------------------------------------------------------------------
-- 3. Row-level security on the user/company-scoped catalog tables. Each gets
--    ENABLE + FORCE and an EXPLICIT WITH CHECK = USING (mirror 0004/0009). The
--    USING predicate differs per table (subject / company / user GUC).
-- ---------------------------------------------------------------------------

ALTER TABLE app_user ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_user FORCE  ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app_user
    USING (subject = app_current_subject() OR id = app_current_user_id())
    WITH CHECK (subject = app_current_subject() OR id = app_current_user_id());

ALTER TABLE security_level ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_level FORCE  ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON security_level
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

ALTER TABLE acl_rule ENABLE ROW LEVEL SECURITY;
ALTER TABLE acl_rule FORCE  ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON acl_rule
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

ALTER TABLE company_membership ENABLE ROW LEVEL SECURITY;
ALTER TABLE company_membership FORCE  ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON company_membership
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());

ALTER TABLE webauthn_credential ENABLE ROW LEVEL SECURITY;
ALTER TABLE webauthn_credential FORCE  ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON webauthn_credential
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());

-- ---------------------------------------------------------------------------
-- 4. Least-privilege grants. auth_login_attempt is bigserial, so tally_app must
--    also be able to USE the implicit sequence (every prior table used app-
--    supplied uuid PKs, so no sequence has ever been granted before).
-- ---------------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE, DELETE ON
    app_user, security_level, acl_rule, company_membership,
    webauthn_credential, auth_login_attempt
    TO tally_app;

GRANT USAGE, SELECT ON SEQUENCE auth_login_attempt_id_seq TO tally_app;

-- ---------------------------------------------------------------------------
-- 5. Seed helper: on company creation, create the Owner + Data Entry security
--    levels and make the creating user an Owner member. Invoked by the server's
--    handleCreateCompany AFTER CreateCompany, under the appropriate GUCs:
--      * the company GUC must be set (security_level/acl_rule inserts), and
--      * the user GUC must be set (company_membership insert).
--    Both are SECURITY INVOKER (default), so they still pass RLS WITH CHECK.
--    Idempotent: ON CONFLICT DO NOTHING on the (company,name) and (user,company)
--    keys, so a re-seed of the same company/user is a no-op.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION seed_company_auth_defaults(
    p_company_id UUID,
    p_user_id    UUID
)
RETURNS uuid   -- the Owner security_level id the caller was made a member of
LANGUAGE plpgsql
AS $$
DECLARE
    owner_level_id UUID;
    de_level_id    UUID;
    cls            TEXT;
BEGIN
    -- Owner: full access, mandatory WebAuthn step-up.
    INSERT INTO security_level (id, company_id, name, is_owner, default_access, requires_webauthn)
    VALUES (gen_random_uuid(), p_company_id, 'Owner', true, 'full', true)
    ON CONFLICT (company_id, name) DO NOTHING;

    SELECT id INTO owner_level_id
      FROM security_level
     WHERE company_id = p_company_id AND name = 'Owner';

    -- Data Entry: default-deny, with allow rules for create/alter/display on the
    -- six voucher classes (a Tally data-entry operator can enter but not delete).
    INSERT INTO security_level (id, company_id, name, is_owner, default_access, requires_webauthn)
    VALUES (gen_random_uuid(), p_company_id, 'Data Entry', false, 'none', false)
    ON CONFLICT (company_id, name) DO NOTHING;

    SELECT id INTO de_level_id
      FROM security_level
     WHERE company_id = p_company_id AND name = 'Data Entry';

    -- Seed Data Entry allow rules only if it has none yet (idempotent).
    IF NOT EXISTS (SELECT 1 FROM acl_rule WHERE security_level_id = de_level_id) THEN
        FOREACH cls IN ARRAY ARRAY['sales','purchase','receipt','payment','contra','journal'] LOOP
            INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
            VALUES
                (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'create',  cls),
                (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'alter',   cls),
                (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'display', cls);
        END LOOP;
        -- Data entry can read reports too.
        INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
        VALUES (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'display', 'report');
    END IF;

    -- Make the creating user an Owner member.
    INSERT INTO company_membership (user_id, company_id, security_level_id)
    VALUES (p_user_id, p_company_id, owner_level_id)
    ON CONFLICT (user_id, company_id) DO NOTHING;

    RETURN owner_level_id;
END
$$;

GRANT EXECUTE ON FUNCTION seed_company_auth_defaults(UUID, UUID) TO tally_app;
