-- 0023_notification_outbox.sql
-- TRANSACTIONAL OUTBOX for outbound notifications. First user: company invites.
--
-- THE GAP THIS CLOSES
-- -------------------
-- An invite has been a database row that nothing tells anybody about: the flow
-- assumed the administrator would message the invitee out of band. That is a
-- real hole in a SaaS onboarding path (HANDOFF §10.1 item C).
--
-- WHY AN OUTBOX AND NOT A DIRECT SEND
-- -----------------------------------
-- Sending inside the invite's request would couple a transactional write to a
-- network call, and the two failure modes are both bad: send-then-fail-to-commit
-- notifies somebody about access they were not granted, and commit-then-fail-to-
-- send grants access nobody is told about (exactly today's behaviour). The row
-- is therefore enqueued in the SAME TRANSACTION as the invite — roll one back
-- and you roll back the other — and a dispatcher delivers it afterwards, at
-- least once, with the send made idempotent by a status transition.
--
-- REAL SMTP IS CONFIGURATION, NOT CODE. The dispatcher takes a Sender; the
-- default logs. Nothing here claims a message left the building.
--
-- RLS, AND HOW A BACKGROUND DISPATCHER LIVES WITH IT
-- --------------------------------------------------
-- The table is company-scoped and IS user-facing (Users & Roles shows whether
-- an invitee was notified), so it gets the full 0004 treatment: ENABLE + FORCE
-- RLS, explicit USING and WITH CHECK. Tenant isolation must be structural, not
-- a WHERE clause somebody might forget.
--
-- But a dispatcher has no company scope — it must find work ACROSS tenants
-- before it can adopt one. Rather than weaken the policy or hand the app role a
-- bypass, `notification_pending_companies()` is SECURITY DEFINER and returns
-- ONLY company ids — no recipient, no subject, no body. The dispatcher then
-- adopts each company's GUC in turn and does all its real reading and writing
-- under the ordinary policy. The privileged surface is one column of one query,
-- and it cannot leak a message.

CREATE TABLE notification_outbox (
    id           UUID PRIMARY KEY,
    company_id   UUID NOT NULL REFERENCES company (id),
    -- What kind of notification this is; the dispatcher renders by kind.
    kind         TEXT NOT NULL CHECK (kind <> ''),
    -- The row in the domain this notification is about (an invite id today).
    ref_id       UUID NULL,
    -- Where it goes. An invite may carry no email — the admin API does not
    -- require one — in which case this holds the JWT subject and the log sender
    -- says plainly that there is no address to reach them at.
    recipient    TEXT NOT NULL,
    subject_line TEXT NOT NULL,
    body         TEXT NOT NULL,
    -- pending  : waiting to be delivered
    -- sending  : CLAIMED by a dispatcher, under lease (see claimed_at)
    -- sent     : delivered
    -- failed   : dead-lettered after MaxAttempts
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
    attempts     INT  NOT NULL DEFAULT 0,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- When the current dispatcher claimed this row. A claim is a LEASE, not a
    -- lock: FOR UPDATE SKIP LOCKED only excludes other claimants for as long as
    -- the claiming TRANSACTION is open, and the send necessarily happens after
    -- that transaction commits. So the claim also moves the row OUT of
    -- 'pending', and a 'sending' row whose lease has expired (a dispatcher that
    -- died mid-send) becomes claimable again.
    --
    -- Without the status change this is a genuine double-send, and it is not
    -- theoretical: the first version of this table did only bump `attempts` at
    -- claim time, and four concurrent dispatchers handed SEVEN of eight messages
    -- to the sender twice. TestConcurrentDispatchersSendEachMessageOnce is that
    -- experiment, kept.
    claimed_at   TIMESTAMPTZ NULL,
    sent_at      TIMESTAMPTZ NULL
);

CREATE INDEX notification_outbox_claimable
    ON notification_outbox (company_id, created_at)
    WHERE status IN ('pending', 'sending');
CREATE INDEX notification_outbox_ref ON notification_outbox (company_id, ref_id);

COMMENT ON TABLE notification_outbox IS
    'Transactional outbox for outbound notifications (invites today). Enqueued '
    'in the same transaction as the domain write; delivered at-least-once by a '
    'dispatcher. RLS-scoped on company_id.';

ALTER TABLE notification_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_outbox FORCE  ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON notification_outbox
    USING      (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

GRANT SELECT, INSERT, UPDATE ON notification_outbox TO tally_app;
-- No DELETE: the outbox is the record of what was (or was not) sent. A failed
-- notification that quietly disappeared would be worse than one that sits there.

-- ---------------------------------------------------------------------------
-- The dispatcher's ONLY privileged read: which companies have work waiting.
-- SECURITY DEFINER (runs as the owner, so RLS does not apply) but the result is
-- a bare list of company ids — it cannot disclose a recipient or a message.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION notification_pending_companies(p_limit INT DEFAULT 100)
RETURNS TABLE (company_id UUID)
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
    SELECT DISTINCT n.company_id
      FROM notification_outbox n
     WHERE n.status IN ('pending', 'sending')
     ORDER BY 1
     LIMIT p_limit;
$$;

COMMENT ON FUNCTION notification_pending_companies(INT) IS
    'Company ids with pending notifications. SECURITY DEFINER so a background '
    'dispatcher can find work across tenants WITHOUT a policy bypass; returns '
    'ids only, never message content.';

REVOKE ALL ON FUNCTION notification_pending_companies(INT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION notification_pending_companies(INT) TO tally_app;
