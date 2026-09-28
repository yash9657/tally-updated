-- 0095_agent_breadth.sql
-- W2 BREADTH (PLAN-AGENTIC.md §3 W2, D121 "Wave 2 breadth"): the three things
-- the agent plane needed from the schema once it could write more than the
-- eight accounting voucher creates. Design memo: docs/design/d121-w2-agent-plane.md,
-- section "Wave 2 breadth".
--
-- ===========================================================================
-- ⛔ 1. AN AGENT-CREATED LEDGER IS BORN BLOCKED, AND THE DATABASE REFUSES IT A POSTING
-- ===========================================================================
-- Wave 1 realised "agent-created parties born blocked" as "an agent-created
-- master exists only after a human decision" (D121 §9 item 7, HANDOFF §6.8),
-- and said what that leaves open: a human who approves a new party in a hurry
-- has, in the same breath, made it payable. The commonest SME fraud is exactly
-- that — a forged "our bank details have changed" letter becomes a new party,
-- and the next payment does the rest. So a ledger the agent creates now carries
-- blocked_at from birth, and NOTHING can post to it — not the agent, not a
-- human, not a sync replay, not a Tally import — until a human unblocks it
-- through POST /ledgers/{id}/unblock (the bare token `master-unblock`, which
-- internal/actions classes governance, so the agent can never lift its own
-- block).
--
-- Deliberately WIDER than "parties": every ledger the agent creates is born
-- blocked, not just those under Sundry Debtors/Creditors. A bank ledger the
-- agent made from an injected letter is the same fraud one hop earlier, and
-- deciding "is this a party?" means walking the group tree inside a trigger —
-- a rule that would be right until somebody re-groups a ledger.
--
-- The refusal is TWO layers. internal/ledger's post() refuses first, with a
-- sentence naming the ledger (the operator's message); the trigger below is the
-- floor under every other writer of `posting`, which has no sentence to give.
-- The blocked flag is set by the SERVER (handleCreateLedger, when the request is
-- an agent's in-process call) — never from a request body, so a client cannot
-- create a pre-blocked ledger or, more to the point, an agent cannot create an
-- unblocked one by omitting a field.
--
-- created_by_run is permanent provenance ("this ledger was born of agent run
-- R"); it survives the unblock. blocked_at / blocked_reason are the live
-- state, cleared by the unblock, whose who-and-when is unblocked_by /
-- unblocked_at AND a master_audit ALTER row (the edit log is the record; these
-- columns are what the ledger list can show without reading it).
--
-- ===========================================================================
-- 2. agent_commit RECORDS WHAT WAS WRITTEN, NOT ONLY VOUCHERS
-- ===========================================================================
-- 0094's agent_commit named its result column voucher_ids, because a voucher
-- create was all the agent could commit. A master, an order or a bank
-- reconciliation writes no voucher. object_kind + object_ids say what the
-- commit wrote; voucher_ids keeps meaning exactly what it meant (and is still
-- filled for a voucher commit, so every wave-1 reader is unchanged).
-- ⚠️ ADD COLUMN with a constant DEFAULT rewrites no row, so 0094's append-only
-- trigger (BEFORE UPDATE) never fires and no existing row changes meaning:
-- every row written before this file is a voucher commit, which is what the
-- default says.
--
-- ===========================================================================
-- 3. THE APPROVER'S EDIT IS PREVIEWED, AND THE PREVIEW IS KEPT WITH THE DECISION
-- ===========================================================================
-- Wave-1 review finding #2's owed half: an approver who edits a proposal must
-- see the recomputed ledger effect of THEIR body, not the model's. The
-- decision handler now runs the edited body through the real handler in
-- dry-run mode before recording the approve; the effect it saw is stored on
-- the decision (edited_preview), so the inbox and the audit both show what
-- the approver approved, and a body the validators refuse is refused at the
-- decision, not discovered as a commit_refused afterwards.
--
-- No new table, so no new RLS block: ledger, agent_commit and agent_decision
-- are all ENABLE + FORCE RLS with USING and WITH CHECK already (0003, 0094).
-- No ACL rule: `master-unblock` and `agent-autonomy` are BARE tokens, refused
-- to Data Entry by default_access = 'none' with zero acl_rule rows (the 0091
-- row of PLAN-TALLY-PARITY §2.5), so the seeder census is unchanged.
--
-- RE-RUNNABLE: every statement is IF NOT EXISTS, CREATE OR REPLACE, or a
-- DROP-then-ADD of a named object, because migrations.go wraps each file in one
-- transaction and a file that cannot re-run walls off every later migration.

-- ---------------------------------------------------------------------------
-- 1. born blocked
-- ---------------------------------------------------------------------------
ALTER TABLE ledger
    ADD COLUMN IF NOT EXISTS created_by_run UUID NULL,
    ADD COLUMN IF NOT EXISTS blocked_at     TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS blocked_reason TEXT NULL,
    ADD COLUMN IF NOT EXISTS unblocked_by   UUID NULL,
    ADD COLUMN IF NOT EXISTS unblocked_at   TIMESTAMPTZ NULL;

-- A block always says why; a reason with no block is a claim about nothing.
ALTER TABLE ledger DROP CONSTRAINT IF EXISTS ledger_block_has_reason;
ALTER TABLE ledger ADD CONSTRAINT ledger_block_has_reason
    CHECK ((blocked_at IS NULL) = (blocked_reason IS NULL));
-- Blocked and unblocked at once is a contradiction; a re-block (none today)
-- would have to clear the unblock first, on purpose.
ALTER TABLE ledger DROP CONSTRAINT IF EXISTS ledger_block_or_unblock;
ALTER TABLE ledger ADD CONSTRAINT ledger_block_or_unblock
    CHECK (blocked_at IS NULL OR unblocked_at IS NULL);
ALTER TABLE ledger DROP CONSTRAINT IF EXISTS ledger_unblock_says_who;
ALTER TABLE ledger ADD CONSTRAINT ledger_unblock_says_who
    CHECK ((unblocked_at IS NULL) = (unblocked_by IS NULL));

CREATE INDEX IF NOT EXISTS ledger_blocked ON ledger (company_id) WHERE blocked_at IS NOT NULL;

-- The floor. SECURITY INVOKER (the default): it runs as the writer, under the
-- writer's RLS scope, and the ledger it looks up is in the same company as the
-- posting (posting's own company FK guarantees that) — so it can only ever
-- see the row it is asking about.
CREATE OR REPLACE FUNCTION posting_ledger_not_blocked()
RETURNS TRIGGER AS $$
DECLARE
    l_name   TEXT;
    l_reason TEXT;
BEGIN
    SELECT name, blocked_reason INTO l_name, l_reason
      FROM ledger
     WHERE id = NEW.ledger_id AND blocked_at IS NOT NULL;
    IF FOUND THEN
        RAISE EXCEPTION 'ledger "%" is blocked and cannot take a posting until a person unblocks it (%)',
            l_name, l_reason
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS posting_ledger_not_blocked ON posting;
CREATE TRIGGER posting_ledger_not_blocked
    BEFORE INSERT ON posting
    FOR EACH ROW EXECUTE FUNCTION posting_ledger_not_blocked();

-- ---------------------------------------------------------------------------
-- 2. what a commit wrote
-- ---------------------------------------------------------------------------
ALTER TABLE agent_commit
    ADD COLUMN IF NOT EXISTS object_kind TEXT NOT NULL DEFAULT 'voucher',
    ADD COLUMN IF NOT EXISTS object_ids  UUID[] NOT NULL DEFAULT '{}';

ALTER TABLE agent_commit DROP CONSTRAINT IF EXISTS agent_commit_object_kind_known;
ALTER TABLE agent_commit ADD CONSTRAINT agent_commit_object_kind_known
    CHECK (object_kind IN ('voucher', 'master', 'order', 'bank_reconciliation'));

-- ---------------------------------------------------------------------------
-- 3. the approver's edit, previewed
-- ---------------------------------------------------------------------------
ALTER TABLE agent_decision
    ADD COLUMN IF NOT EXISTS edited_preview JSONB NULL;

ALTER TABLE agent_decision DROP CONSTRAINT IF EXISTS agent_decision_preview_only_with_edit;
ALTER TABLE agent_decision ADD CONSTRAINT agent_decision_preview_only_with_edit
    CHECK (edited_preview IS NULL OR edited_args IS NOT NULL);
