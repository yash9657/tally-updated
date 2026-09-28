-- 0100_voucher_idempotency.sql
-- W5 (PLAN-AGENTIC.md §3, gate: "the Bill Inbox duplicate-post defect class
-- (row 30 of the parity register) is closed by idempotency keys on commit").
-- Design memo: docs/design/d123-w5-documents-bank.md §4.
--
-- ===========================================================================
-- ⛔ THE DEFECT CLASS, AND WHY 0077 DID NOT CLOSE ALL OF IT
-- ===========================================================================
-- Row 30 (HANDOFF-PARITY-AUDIT.md D15, §7.3 B) is ONE supplier bill posted as
-- TWO vouchers. 0077 put the insightd draft id on voucher_head under a partial
-- unique index, which closes it WHEN THE CLIENT HAS A DRAFT ID. The residue is
-- named in draftAccept.ts's "THE RESIDUAL WINDOW, STATED IN FULL":
--
--   * NO DRAFT ID RESOLVABLE — a body the client could not tie to an insightd
--     draft (insightd unreachable, a hand-built body). Nothing for 0077 to key
--     on, so two posts of the same bill are two vouchers.
--   * AN AMBIGUOUS FAILURE — a timeout on the voucher POST where the voucher
--     DID land. The retry either posts again (no draft id) or is answered 409
--     draft_already_accepted (with one) — a refusal where the honest answer is
--     "yes, that is posted, here it is".
--   * AND OUTSIDE THE INBOX, EVERY HAND-KEYED VOUCHER (D108): a double Ctrl+A
--     or a network retry has no server-side identity at all. D108 was closed in
--     the client by clearing the form; a retried request never reaches a form.
--
-- The class is "the same REQUEST, delivered twice, is two writes". The fix for
-- that class is the standard one — an idempotency key chosen by the client,
-- recorded by the server IN THE SAME TRANSACTION AS THE WRITE, and a second
-- request under the same key REPLAYS the first's result instead of writing.
--
-- ===========================================================================
-- WHY THE ROW IS WRITTEN INSIDE THE VOUCHER'S TRANSACTION
-- ===========================================================================
-- The same reason 0077 gives for source_draft_id: a key recorded in a separate
-- transaction is a check-then-act, and two concurrent requests both see "no
-- key yet" and both post. Inserted in the post transaction, the second request
-- BLOCKS on this primary key until the first commits, then trips it, rolls back
-- everything it did (voucher, numbering advance, tax rows) and is answered with
-- the first request's stored result. If the first rolls back instead, the
-- second's insert succeeds and it posts — a failed write consumes no key.
--
-- ⛔ THE KEY IS BOUND TO THE REQUEST, NOT MERELY RECORDED. request_sha256 is a
-- fingerprint of the normalised voucher input (ledger.requestFingerprint). The
-- same key with a DIFFERENT body is refused (422), never replayed: replaying
-- the first voucher for a second, different request would tell the operator
-- that what they keyed was posted when it was not.
--
-- ===========================================================================
-- IMMUTABLE, BOTH HALVES (HANDOFF-AGENTIC-AFTER-W0.md §8's rule for a log)
-- ===========================================================================
-- A BEFORE UPDATE OR DELETE trigger that raises, AND GRANT SELECT, INSERT only.
-- A key that could be deleted is a key whose second request posts again; one
-- that could be updated could be re-pointed at another voucher. The stored
-- result is the voucher's CREATE result and never changes: an ALTER of that
-- voucher later does not update it, which is correct — the replay answers the
-- question "what did THIS request do?", and it created version 1.
--
-- ⚠️ RETENTION IS OWED, NOT DECIDED. Keys are kept forever, one row per keyed
-- create. A sweep needs a TTL longer than any client's retry horizon (the
-- offline outbox can hold a request for days) and is recorded as owed in d123.
--
-- No ACL: no route reads this table; it is written by internal/ledger inside
-- POST /vouchers/{class}, which is already authorised per class. The seeder
-- census is unchanged (no seed_company_auth_defaults restatement).

CREATE TABLE IF NOT EXISTS voucher_idempotency (
    company_id      UUID NOT NULL,
    -- The CLIENT's key. Opaque: a UUID from a browser, "bill-inbox:<sha>" from
    -- the draft path, an outbox intent id. Bounded so a client cannot store a
    -- document here.
    idempotency_key TEXT NOT NULL,
    -- sha256 hex of the normalised voucher input, EXCLUDING the key itself.
    request_sha256  TEXT NOT NULL,
    -- The voucher the FIRST request created. Company-scoped FK, so the key can
    -- only ever name a voucher in its own tenant.
    voucher_id      UUID NOT NULL,
    -- The first request's result (ledger.PostResult as JSON), replayed verbatim
    -- to every later request under this key.
    result          JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT voucher_idempotency_pkey PRIMARY KEY (company_id, idempotency_key),
    CONSTRAINT voucher_idempotency_voucher_fk
        FOREIGN KEY (company_id, voucher_id) REFERENCES voucher_head (company_id, voucher_id),
    CONSTRAINT voucher_idempotency_key_shape
        CHECK (length(idempotency_key) BETWEEN 8 AND 200 AND idempotency_key !~ '[[:space:][:cntrl:]]'),
    CONSTRAINT voucher_idempotency_sha_shape
        CHECK (request_sha256 ~ '^[0-9a-f]{64}$')
);

-- "Which request created this voucher?" — the audit direction.
CREATE INDEX IF NOT EXISTS voucher_idempotency_voucher
    ON voucher_idempotency (company_id, voucher_id);

COMMENT ON TABLE voucher_idempotency IS
    'W5. One row per voucher CREATE that carried a client idempotency key, '
    'written by internal/ledger in the SAME transaction as the voucher. A second '
    'request under the same key REPLAYS result instead of posting; the same key '
    'with a different body (request_sha256) is refused. Closes the row-30 '
    'duplicate-post class (one request delivered twice = one voucher). '
    'INSERT-only: trigger + grants. See the file header.';
COMMENT ON COLUMN voucher_idempotency.idempotency_key IS
    'The client-chosen key, opaque, 8..200 chars, no whitespace or control '
    'characters. Scoped per company by the primary key.';
COMMENT ON COLUMN voucher_idempotency.request_sha256 IS
    'sha256 (hex) of the normalised voucher input minus the key. The same key '
    'with a different fingerprint is refused, never replayed.';
COMMENT ON COLUMN voucher_idempotency.result IS
    'The first request''s ledger.PostResult, replayed verbatim. It describes the '
    'CREATE and is never updated by a later alter of the voucher.';

CREATE OR REPLACE FUNCTION voucher_idempotency_no_mutate() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'voucher_idempotency is insert-only: a key that could be % could post twice', lower(TG_OP)
        USING ERRCODE = 'integrity_constraint_violation';
END
$$;

DROP TRIGGER IF EXISTS voucher_idempotency_no_mutate ON voucher_idempotency;
CREATE TRIGGER voucher_idempotency_no_mutate
    BEFORE UPDATE OR DELETE ON voucher_idempotency
    FOR EACH ROW EXECUTE FUNCTION voucher_idempotency_no_mutate();

DO $$
BEGIN
    EXECUTE 'ALTER TABLE voucher_idempotency ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE voucher_idempotency FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON voucher_idempotency';
    EXECUTE 'CREATE POLICY tenant_isolation ON voucher_idempotency '
            'USING (company_id = app_current_company_id()) '
            'WITH CHECK (company_id = app_current_company_id())';
END
$$;

-- SELECT and INSERT only — the second half of immutability.
REVOKE ALL ON voucher_idempotency FROM tally_app;
GRANT SELECT, INSERT ON voucher_idempotency TO tally_app;
