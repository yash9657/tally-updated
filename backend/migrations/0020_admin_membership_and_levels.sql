-- 0020_admin_membership_and_levels.sql
-- THE SaaS ADMIN SURFACE. Until now `POST /v1/companies` made its creator an
-- Owner and that was the ONLY membership any HTTP route could create: inviting a
-- colleague or assigning a role was SQL-only (frontend/scripts/seed-tenancy.mts
-- literally shelled out to psql). This migration adds the catalog the admin API
-- needs, and — just as important — the MINIMUM RLS widening that lets it work.
--
-- Three changes, each deliberately scoped:
--
--  1. company_invite: a pending grant addressed to a JWT SUBJECT. An admin never
--     writes another person's identity row (app_user stays self-write-only);
--     they record an INVITE, and the invitee materialises their own membership
--     when they first authenticate (GET /v1/me). That keeps every app_user
--     INSERT authored by the user it describes, which is what the 0011 WITH
--     CHECK (subject = app_current_subject()) already guaranteed.
--
--  2. company_membership RLS: was `user_id = app_current_user_id()` — a user can
--     see only their OWN memberships. Correct for the membership oracle the PDP
--     consults, but it makes "list the members of this company" return nothing,
--     even for an Owner. USING is widened to ALSO admit the company GUC, which
--     is set ONLY after the middleware has verified the caller is a member of
--     that company. The WRITE side is simultaneously NARROWED: WITH CHECK is now
--     company-GUC-only, so a membership row can never be written without a
--     company in scope (0011 allowed a user-GUC-only self-grant into ANY
--     company; nothing did that, and now nothing can).
--
--  3. app_user RLS USING: adds "…or this row belongs to a member of the company
--     currently in scope", so the member list can show a colleague's email and
--     display name. WITH CHECK is UNCHANGED (still self-only): reading a
--     co-member is not writing them. Note the added clause is itself evaluated
--     through company_membership's policy, so it can only ever see the company
--     already in scope — there is no path to enumerate the global directory.
--
-- Net effect on the threat model: everything new is gated on the company GUC,
-- which the auth middleware sets only after authn + membership succeed, and the
-- admin routes themselves are PDP-gated on the new (action, "security") pair.

-- ---------------------------------------------------------------------------
-- 1. company_invite
-- ---------------------------------------------------------------------------

CREATE TABLE company_invite (
    id                UUID PRIMARY KEY,
    company_id        UUID NOT NULL,
    -- The JWT subject the invite is addressed to. Deliberately the subject and
    -- not a user id: the invitee may not have an app_user row yet (they have
    -- never logged in), and an admin must not be able to create one for them.
    subject           TEXT NOT NULL,
    email             TEXT NOT NULL DEFAULT '',
    display_name      TEXT NOT NULL DEFAULT '',
    -- The level granted. NULLABLE with ON DELETE SET NULL, and the NAME is
    -- copied alongside it: accepted/revoked invites are an AUDIT TRAIL, so
    -- deleting a custom security level must neither be blocked by history nor
    -- destroy it. A pending invite still pins the level (DeleteLevel refuses
    -- while one exists), so this can only ever null out a historic row.
    security_level_id   UUID NULL REFERENCES security_level(id) ON DELETE SET NULL,
    security_level_name TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'accepted', 'revoked')),
    invited_by        UUID NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at       TIMESTAMPTZ NULL
);

-- At most ONE pending invite per (company, subject); accepted/revoked rows stay
-- as an audit trail, so this is a partial index rather than a table constraint.
CREATE UNIQUE INDEX company_invite_pending_unique
    ON company_invite (company_id, subject)
    WHERE status = 'pending';

CREATE INDEX company_invite_subject ON company_invite (subject) WHERE status = 'pending';

ALTER TABLE company_invite ENABLE ROW LEVEL SECURITY;
ALTER TABLE company_invite FORCE  ROW LEVEL SECURITY;

-- READ: the administering company (company GUC), OR the invitee themselves
-- (subject GUC) — the invitee must be able to see an invite addressed to them
-- BEFORE they are a member of anything, which is precisely when no company is in
-- scope.
-- WRITE: company GUC only. The subject clause is deliberately absent from WITH
-- CHECK: if a caller could write rows matching only `subject = me`, they could
-- mint themselves an invite into an arbitrary company and then accept it.
CREATE POLICY tenant_isolation ON company_invite
    USING (company_id = app_current_company_id() OR subject = app_current_subject())
    WITH CHECK (company_id = app_current_company_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON company_invite TO tally_app;

-- ---------------------------------------------------------------------------
-- 2. company_membership: widen READ to the company GUC, narrow WRITE to it.
-- ---------------------------------------------------------------------------

DROP POLICY tenant_isolation ON company_membership;

CREATE POLICY tenant_isolation ON company_membership
    USING (user_id = app_current_user_id() OR company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

-- ---------------------------------------------------------------------------
-- 3. app_user: co-members of the company in scope become READABLE.
-- ---------------------------------------------------------------------------

DROP POLICY tenant_isolation ON app_user;

CREATE POLICY tenant_isolation ON app_user
    USING (
        subject = app_current_subject()
        OR id = app_current_user_id()
        OR EXISTS (
            SELECT 1 FROM company_membership m
             WHERE m.user_id = app_user.id
               AND m.company_id = app_current_company_id()
        )
    )
    WITH CHECK (subject = app_current_subject() OR id = app_current_user_id());
