-- 0074_lockout_revocation_and_audit.sql
-- ACCOUNT LOCKOUT, ROUND 2 (defect burndown D56/D55/D57/D73/D74).
--
-- Migration 0011 gave app_user `locked_until` / `failed_attempts` and gave the
-- product a login lockout. Five things were missing, and all five need schema:
--
--  D56  LOCKING AN ACCOUNT REVOKED NOTHING. `locked_until` was consulted on
--       exactly one code path — POST /auth/login — so a bearer already in the
--       attacker's hand kept working for the FULL 3600-second token TTL,
--       including authenticated WRITES. A locked account posted a voucher and
--       got 201. Locking must kill the sessions the subject ALREADY HOLDS, and
--       it must kill them PERMANENTLY: a 15-minute lock that merely pauses a
--       stolen session for 15 minutes has revoked nothing at all. Hence
--       `sessions_invalid_before` — a REVOCATION EPOCH. Every token whose `iat`
--       is at or before it is dead, whatever its expiry says, and it OUTLIVES
--       the lock (clearing a lock does not resurrect them).
--
--  D55  THERE WAS NO WAY TO UNLOCK AN ACCOUNT. No route, no key, no screen —
--       `ClearLock` was reachable only from a SUCCESSFUL login, which a locked
--       account cannot perform. app_user's RLS (0011 §3, widened by 0020) lets
--       an administrator READ a co-member's row but its WITH CHECK deliberately
--       forbids WRITING one ("an admin never writes another person's identity
--       row"). So the remedy cannot be an ordinary UPDATE from the app role and
--       is `auth_admin_clear_lock()` below: SECURITY DEFINER, but narrower than
--       a general grant — it clears the lock COLUMNS ONLY, only for a member of
--       the named company, only at the request of another member of that same
--       company, and it never touches the revocation epoch.
--
--  D57  THE THRESHOLD/WINDOW/DURATION WERE COMPILE-TIME CONSTANTS while the
--       plan sells them as configurable with exponential backoff
--       (RESEARCH.md:2285 "configurable failed-attempt lockout, exponential
--       backoff"). The knobs themselves are env, not schema (see
--       authn.DefaultLockoutPolicy) — but backoff needs to remember how many
--       times this subject has been locked before, hence `lock_count`.
--
--  D73  ATTEMPTS MADE WHILE LOCKED WERE NEVER AUDITED. The IsLocked branch
--       returned 423 without writing auth_login_attempt, so the traffic during
--       the lock window — the exact period an investigator cares about — left
--       no trace at all. It is now recorded with outcome='locked'.
--
--  D74  `failed_attempts` WAS NOT A RUNNING COUNTER: it stayed 0 through
--       failures 1-4 and was written only at the instant the lock tripped, so
--       nothing could observe an account partway to being locked. It is now
--       written on every failure (see PGLockoutStore.SetFailureCount); no DDL
--       is needed for that, but the CHECK below stops it going negative.
--
-- ⛔ WHY auth_login_attempt NEEDS `outcome` AND NOT JUST `succeeded`. D73's rows
-- are attempts that were REFUSED BEFORE A PASSWORD WAS EVEN LOOKED AT. Writing
-- them as succeeded=false would fold them into the failure count that decides
-- locking, and the lock would then renew itself off its own refusals — a
-- brute-forcer hammering a locked account would extend the lock indefinitely,
-- and a confused operator who kept typing during their 15 minutes would be
-- re-locked the instant it expired. They must be RECORDED and NOT COUNTED, and
-- a boolean cannot say that. Same for the 'unlocked' row an administrator's
-- remedy writes: it is not a login, but it IS the point from which failures are
-- counted afresh, or an unlock would leave the account one typo from re-locking.
--
-- Every change here is ADDITIVE (new nullable/defaulted columns, new
-- functions). No existing row changes meaning: `outcome` is backfilled from
-- `succeeded`, which is exactly what the old code meant by it.

-- ---------------------------------------------------------------------------
-- 1. app_user: the revocation epoch and the consecutive-lock counter.
-- ---------------------------------------------------------------------------

ALTER TABLE app_user
    ADD COLUMN IF NOT EXISTS sessions_invalid_before TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS lock_count INTEGER NOT NULL DEFAULT 0;

COMMENT ON COLUMN app_user.sessions_invalid_before IS
    'REVOCATION EPOCH. A verified token whose iat is at or before this instant '
    'is refused on EVERY authenticated route, however long its exp says it has '
    'left. Advanced (never rewound) when the account is locked. Deliberately '
    'NOT cleared by an unlock: unlocking restores the ability to SIGN IN, it '
    'does not resurrect the sessions the lock killed.';

COMMENT ON COLUMN app_user.lock_count IS
    'How many times this subject has been locked since the last successful '
    'login or administrative unlock. Drives the exponential backoff in '
    'authn.Locker (lock duration = base * 2^lock_count, capped).';

ALTER TABLE app_user
    ADD CONSTRAINT app_user_failed_attempts_nonneg CHECK (failed_attempts >= 0),
    ADD CONSTRAINT app_user_lock_count_nonneg      CHECK (lock_count >= 0);

-- ---------------------------------------------------------------------------
-- 2. auth_login_attempt: say WHAT the attempt was, not merely whether it won.
-- ---------------------------------------------------------------------------

ALTER TABLE auth_login_attempt
    ADD COLUMN IF NOT EXISTS outcome TEXT NULL;

-- Backfill carries the OLD meaning forward exactly: before this migration the
-- only two things that were ever recorded were a success and a password
-- failure, and `succeeded` said which. Nothing is reinterpreted.
UPDATE auth_login_attempt
   SET outcome = CASE WHEN succeeded THEN 'success' ELSE 'failure' END
 WHERE outcome IS NULL;

ALTER TABLE auth_login_attempt
    ALTER COLUMN outcome SET DEFAULT 'failure',
    ALTER COLUMN outcome SET NOT NULL,
    ADD CONSTRAINT auth_login_attempt_outcome_known
        CHECK (outcome IN ('success', 'failure', 'locked', 'unlocked'));

COMMENT ON COLUMN auth_login_attempt.outcome IS
    'success  = the password verified. '
    'failure  = a password was checked and rejected; ONLY these count toward '
    '           the lockout threshold. '
    'locked   = an attempt made WHILE the account was locked, refused before '
    '           any password was looked at (D73). Recorded, never counted, or '
    '           the lock would renew itself off its own refusals. '
    'unlocked = an administrator cleared the lock. Not a login, but it IS a '
    '           reset point for the failure window, so an unlocked account is '
    '           not one typo from re-locking.';

-- The failure count and its reset point are both read by subject over a time
-- window, and both now filter on outcome. auth_login_attempt_subject_at
-- (0011) already orders by (subject, at DESC); this narrows the same access
-- path for the two outcome-filtered reads on the login hot path.
CREATE INDEX IF NOT EXISTS auth_login_attempt_subject_outcome_at
    ON auth_login_attempt (subject, outcome, at DESC);

-- ---------------------------------------------------------------------------
-- 3. auth_session_state() — the revocation read, on the AUTHENTICATED HOT PATH.
-- ---------------------------------------------------------------------------
-- Every authenticated request asks this question once, so it must be ONE round
-- trip. The app role cannot read app_user without first setting the
-- app.current_subject GUC, and setting a GUC transaction-locally means BEGIN +
-- set_config + SELECT + COMMIT — four round trips to read two timestamps, on
-- every request in the product. Hence SECURITY DEFINER, the same instrument and
-- the same justification as 0023's notification_pending_companies(): the
-- function is the narrowest possible hole, returning two timestamps for one
-- named subject and nothing else — no email, no display name, no id.
--
-- IT IS NOT A USER-ENUMERATION ORACLE. Its only caller (Server.sessionRevoked)
-- runs AFTER the JWT signature has been verified, so the caller already holds a
-- token this deployment's own key signed for that subject; and an unknown
-- subject returns NO ROW, which the caller treats identically to a known
-- subject that is neither locked nor revoked. There is no observable
-- difference to enumerate.
CREATE OR REPLACE FUNCTION auth_session_state(p_subject TEXT)
RETURNS TABLE (locked_until TIMESTAMPTZ, sessions_invalid_before TIMESTAMPTZ)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public
AS $$
    SELECT u.locked_until, u.sessions_invalid_before
      FROM app_user u
     WHERE u.subject = p_subject
$$;

COMMENT ON FUNCTION auth_session_state(TEXT) IS
    'Credential-revocation state for one subject: (locked_until, '
    'sessions_invalid_before). SECURITY DEFINER so the authenticated hot path '
    'costs ONE round trip instead of a GUC transaction. Returns no row for an '
    'unknown subject, which callers must treat as "nothing revoked".';

GRANT EXECUTE ON FUNCTION auth_session_state(TEXT) TO tally_app;

-- ---------------------------------------------------------------------------
-- 4. auth_admin_clear_lock() — the REMEDY (D55).
-- ---------------------------------------------------------------------------
-- The narrowest thing that can undo a lock. It is SECURITY DEFINER because
-- app_user's WITH CHECK forbids the app role from writing another person's row
-- at all (0011 §3 / 0020 §3), which is a rule worth keeping: an admin must not
-- be able to rewrite a colleague's subject or email. So instead of widening
-- that policy, this function writes the three LOCK columns and nothing else.
--
-- ⛔ IT RE-DERIVES ITS OWN AUTHORITY. The HTTP layer has already run the PDP
-- (PUT /members/{userID} is Alter/security, and it inherits the Owner level's
-- WebAuthn step-up like every other admin write). This does NOT trust that: it
-- independently requires that the ACTOR is a member of p_company and that the
-- TARGET is a member of the same company, so even a caller who reached the
-- function with the wrong company id cannot unlock a stranger. A SECURITY
-- DEFINER function that took the caller's word for it would be a global unlock
-- oracle wearing an admin's clothes.
--
-- ⛔ IT DOES NOT TOUCH sessions_invalid_before. Unlocking says "this person may
-- sign in again", not "the sessions that were live when we locked them are fine
-- after all" — the whole point of D56 is that those are dead for good.
--
-- The 'unlocked' audit row is not decoration: authn.PGLockoutStore counts
-- failures only since the last success-or-unlock, so without it an account
-- unlocked while five failures are still inside the sliding window would
-- re-lock on the very next typo, and the administrator's remedy would look
-- like it had not worked.
CREATE OR REPLACE FUNCTION auth_admin_clear_lock(
    p_company UUID,
    p_actor   UUID,
    p_user    UUID
) RETURNS BOOLEAN
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
    n INTEGER;
BEGIN
    IF p_company IS NULL OR p_actor IS NULL OR p_user IS NULL THEN
        RETURN false;
    END IF;
    -- The actor must administer THIS company...
    IF NOT EXISTS (
        SELECT 1 FROM company_membership m
         WHERE m.company_id = p_company AND m.user_id = p_actor
    ) THEN
        RETURN false;
    END IF;
    -- ...and the target must be somebody in it.
    IF NOT EXISTS (
        SELECT 1 FROM company_membership m
         WHERE m.company_id = p_company AND m.user_id = p_user
    ) THEN
        RETURN false;
    END IF;

    UPDATE app_user
       SET locked_until = NULL, failed_attempts = 0, lock_count = 0
     WHERE id = p_user;
    GET DIAGNOSTICS n = ROW_COUNT;
    IF n = 0 THEN
        RETURN false;
    END IF;

    INSERT INTO auth_login_attempt (subject, succeeded, ip, outcome)
    SELECT u.subject, false, '', 'unlocked'
      FROM app_user u
     WHERE u.id = p_user;

    RETURN true;
END
$$;

COMMENT ON FUNCTION auth_admin_clear_lock(UUID, UUID, UUID) IS
    'Administrative unlock: clears locked_until/failed_attempts/lock_count for '
    'a member of p_company at the request of another member of p_company, and '
    'audits it as outcome=unlocked. Does NOT clear sessions_invalid_before — '
    'the sessions the lock revoked stay revoked. Returns false (writing '
    'nothing) when either party is not a member of that company.';

GRANT EXECUTE ON FUNCTION auth_admin_clear_lock(UUID, UUID, UUID) TO tally_app;
