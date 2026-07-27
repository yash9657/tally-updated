-- 0021_user_credential.sql
-- FIRST-PARTY PASSWORD CREDENTIAL (WS-A1: real login).
--
-- Until now the only way to obtain a Bearer token was the dev-gated
-- POST /auth/token (which mints for ANY subject) or an external IdP, and
-- POST /auth/login was a shared env-var password check (AUTH_DEV_PASSWORD). This
-- table gives each user a REAL, per-user, salted+iterated password verifier so
-- POST /auth/login can authenticate a specific person.
--
-- WHY A SEPARATE TABLE AND NOT COLUMNS ON app_user
-- ------------------------------------------------
-- Migration 0020 §3 deliberately WIDENED app_user's RLS read policy: it now also
-- admits every co-member of the company currently in scope
--
--     OR EXISTS (SELECT 1 FROM company_membership m
--                 WHERE m.user_id = app_user.id
--                   AND m.company_id = app_current_company_id())
--
-- so that an Owner can list the members of their own company. A password hash
-- stored ON app_user would therefore inherit that widening and be readable by
-- EVERY colleague in the company — an offline-cracking corpus handed to any
-- Data-Entry operator. user_credential keeps a strictly SELF-ONLY policy
-- (user_id = app_current_user_id(), with no company clause anywhere), so a hash
-- is visible only inside a transaction that has already established the caller's
-- own identity.
--
-- READ PATH (pre-auth, and the reason the policy is keyed on the USER guc):
-- login does not know a user id yet, only a JWT-style subject. authz.Store's
-- CredentialBySubject therefore performs a TWO-STEP GUC dance inside ONE
-- transaction: set app.current_subject, resolve app_user by subject (admitted by
-- app_user's subject clause), then set app.current_user_id mid-transaction and
-- read this table. app_current_user_id() is STABLE (not IMMUTABLE), so the
-- mid-transaction set_config is observed by the next statement. That dance is
-- only safe while NO company GUC is in scope (RunInAuthTx never sets one), which
-- is why the read must never be reached through a company-scoped context.
--
-- ON DELETE CASCADE is a deliberate house-style OUTLIER: every other auth FK is
-- a bare REFERENCES (0011) except company_invite.security_level_id, which is
-- ON DELETE SET NULL to preserve an audit trail. A credential is NOT audit
-- material — a deleted user must never leave an orphaned password verifier
-- behind — so it cascades.
--
-- No sequence GRANT is needed (the PK is a supplied UUID, unlike 0011's BIGSERIAL
-- auth_login_attempt), and no new GRANT EXECUTE is needed (0011 already granted
-- EXECUTE on app_current_user_id() to tally_app).
--
-- This file runs as the ADMIN DSN.

CREATE TABLE user_credential (
    user_id     UUID PRIMARY KEY REFERENCES app_user(id) ON DELETE CASCADE,
    algo        TEXT NOT NULL CHECK (algo = 'pbkdf2-sha256'),
    iterations  INTEGER NOT NULL CHECK (iterations >= 100000),
    salt        BYTEA NOT NULL,
    hash        BYTEA NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE user_credential IS
    'First-party password verifier, one row per app_user. Deliberately NOT on '
    'app_user: 0020 widened app_user reads to company co-members, which would '
    'have exposed every colleague''s hash. FORCE RLS, self-only on '
    'app_current_user_id().';

COMMENT ON COLUMN user_credential.algo IS
    'KDF identifier; CHECK-pinned to pbkdf2-sha256 so an unknown/legacy algo can '
    'never be stored. authn.VerifyPassword returns false (never panics) for any '
    'algo it does not recognise.';

COMMENT ON COLUMN user_credential.iterations IS
    'PBKDF2 iteration count, CHECK-floored at 100000. The application writes '
    'authn.PBKDF2Iterations (210000, the OWASP 2023 floor for PBKDF2-HMAC-SHA256); '
    'the column floor stops a downgrade write.';

COMMENT ON COLUMN user_credential.salt IS
    'Per-credential random salt (crypto/rand, 16 bytes).';

COMMENT ON COLUMN user_credential.hash IS
    'PBKDF2-HMAC-SHA256 derived key (32 bytes), compared with '
    'crypto/subtle.ConstantTimeCompare.';

ALTER TABLE user_credential ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_credential FORCE  ROW LEVEL SECURITY;

-- Named tenant_isolation to match EVERY other RLS policy in this schema (0011
-- and 0020 use that one name on app_user, security_level, acl_rule,
-- company_membership, webauthn_credential, webauthn_challenge and
-- company_invite, and 0020 relies on the uniformity when it DROPs and recreates
-- them). The SEMANTICS here are strictly self-only: no company clause, so
-- widening app_user again can never widen this table.
--
-- Both halves are required: the ON CONFLICT (user_id) DO UPDATE probe in
-- authz.Store.SetPassword needs USING, and the write needs WITH CHECK. WITH
-- CHECK is stated explicitly (= USING) exactly as 0004/0011/0012 do, so the
-- write-side guard is intentional rather than inherited.
CREATE POLICY tenant_isolation ON user_credential
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON user_credential TO tally_app;
