-- 0012_webauthn_challenge.sql
-- PG-BACKED WEBAUTHN CEREMONY CHALLENGE STORE (WS6, auth prod-hardening).
--
-- The register/assert ceremonies hand a server-minted challenge to the browser
-- in `begin` and verify it in `finish`. Until now the server stashed that
-- challenge in an in-process map, which made the WebAuthn step-up a
-- SINGLE-INSTANCE-ONLY feature: behind two cored replicas, a `finish` routed to
-- the other instance would 400 "no pending challenge". This table moves the
-- handoff into Postgres so any instance can complete a ceremony any other
-- instance began.
--
-- Semantics preserved from the in-memory store (see the ChallengeStore
-- conformance suite, backend/internal/server/auth_challenge_store_test.go):
--   * ONE live challenge per user  -> user_id is the PRIMARY KEY and Put is an
--     INSERT ... ON CONFLICT (user_id) DO UPDATE, so a second `begin` OVERWRITES
--     the first (only the latest challenge can be finished).
--   * SINGLE USE                   -> Take is
--     `DELETE ... WHERE user_id = $1 RETURNING challenge, expires_at`, so the row
--     is consumed even when it turns out to be expired (no replay window).
--   * TTL                          -> expires_at is written by the server (clock
--     injectable for tests) and evaluated in Go after the DELETE, so a stale row
--     can never satisfy a finish.
--
-- Housekeeping: an abandoned ceremony (begin with no finish) leaves one row
-- until that user next begins or finishes. Growth is bounded by the PRIMARY KEY
-- (at most one row per app_user), so no reaper job is warranted; the row is
-- harmless because Take rejects it on expiry.
--
-- RLS mirrors webauthn_credential (0011): ENABLE + FORCE with a single ALL
-- policy carrying BOTH USING and WITH CHECK, keyed on the app.current_user_id
-- GUC. Both halves are required: the ON CONFLICT probe needs USING and the write
-- needs WITH CHECK. The server sets the GUC transaction-locally as the first
-- statement of every ceremony transaction (webauthn.PGStore.withUser pattern),
-- so one user can never read or overwrite another user's live challenge.

CREATE TABLE webauthn_challenge (
    user_id    UUID PRIMARY KEY REFERENCES app_user(id) ON DELETE CASCADE,
    challenge  TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

COMMENT ON TABLE webauthn_challenge IS
    'WebAuthn ceremony challenge handoff between begin and finish. One live '
    'challenge per user (PK), single-use (Take DELETEs and RETURNs), TTL '
    'enforced in Go against expires_at. FORCE RLS on app_current_user_id().';

ALTER TABLE webauthn_challenge ENABLE ROW LEVEL SECURITY;
ALTER TABLE webauthn_challenge FORCE  ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON webauthn_challenge
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON webauthn_challenge TO tally_app;
