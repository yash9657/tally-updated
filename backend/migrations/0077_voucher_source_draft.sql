-- 0077_voucher_source_draft.sql
-- §7.3 B — THE ACCEPTANCE OF AN insightd DRAFT BECOMES PART OF THE VOUCHER'S OWN
-- TRANSACTION, so "this supplier bill was posted once" stops being a claim four
-- client-side layers make and becomes a fact the database enforces.
--
-- ===========================================================================
-- ⛔ THE DEFECT, AND WHY THE CLIENT COULD NOT CLOSE IT
-- ===========================================================================
-- Accepting an AI bill draft is TWO writes to TWO services: cored posts the
-- purchase voucher, then the browser tells insightd the draft is `accepted`. If
-- the second one is lost — the tab is closed, the network drops, the laptop
-- sleeps — insightd still offers the bill as `ready` and the next accept posts
-- the SAME payable and the SAME input tax a second time.
--
-- The browser's own retry queue (draftAccept.ts) is localStorage, so only the
-- machine that posted can drive it. A DIFFERENT machine inside that window was
-- protected only by a day-book lookup on (party, supplier invoice no, date) —
-- and a draft whose invoice number the OCR never read gives that lookup nothing
-- to match on. draftAccept.test.ts pinned the residue with a test that asserted
-- TWO vouchers.
--
-- ===========================================================================
-- WHY A COLUMN ON voucher_head AND NOT A LIFECYCLE WRITE INTO insightd_draft
-- ===========================================================================
-- insightd_draft (0014) is in this same database, so cored COULD have marked the
-- draft accepted in the voucher's transaction. Three reasons it does not:
--
--   1. EVERY LIFECYCLE FIELD OF THAT TABLE LIVES INSIDE AN UNTYPED `body` JSONB
--      whose shape is owned by insightd's Python. A second writer mutating that
--      document has no schema holding the two sides to the same field names, and
--      a Python-side rename would break cored silently.
--
--   2. ⛔ ITS RLS POLICY IS PINNED TO THE RAW TEXT GUC app.current_company_id
--      (not the validated uuid resolver), because insightd company ids are opaque
--      strings. It is FORCE ROW LEVEL SECURITY, so any casing or format drift
--      makes the row invisible TO THE WRITER and the UPDATE silently affects zero
--      rows — which presents exactly like the duplicate it was meant to prevent.
--
--   3. A UNIQUE INDEX IS ENFORCED BY THE INDEX, NOT BY A VISIBLE ROW. That is the
--      decisive one. An RLS-filtered SELECT can fail to see a row and let a write
--      through; a unique index cannot. The guarantee below therefore does not
--      depend on the writer being able to READ the winner — it only depends on
--      the two writes naming the same (company_id, draft_id).
--
-- So the fact recorded here is cored's OWN: not "what insightd thinks the draft's
-- status is", but "WHICH VOUCHER THIS DRAFT PRODUCED". insightd's `accepted`
-- status stays insightd's view of its own document and is now a CACHE of this.
--
-- ===========================================================================
-- IT IS THE EXACT SHAPE 0008 ALREADY USES FOR THE TALLY IMPORT
-- ===========================================================================
-- voucher_head.source_guid + a PARTIAL unique index over (company_id, source_guid)
-- is how re-importing the same Tally export is a no-op at the (company, guid)
-- grain. "This voucher came from external document X, and X may produce at most
-- one voucher" is the same statement, so it gets the same mechanism rather than a
-- second invented one. Read 0008 beside this file.
--
-- ⚠️ THE PARTIAL CLAUSE IS ABOUT SIZE AND INTENT, NOT CORRECTNESS, AND SAYING SO
-- HERE IS DELIBERATE. A Postgres unique index is NULLS DISTINCT by default, so a
-- FULL index over (company_id, source_draft_id) would also let unlimited draftless
-- vouchers through — measured, by dropping the WHERE clause and re-running
-- internal/ledger's draft-acceptance tests, which all stayed green. What actually
-- keeps every hand-keyed voucher unconstrained is that its draft id is NULL rather
-- than ''. The WHERE clause is kept so the index carries only the handful of rows
-- it is about instead of one entry per voucher in the book.
--
-- ⛔ SO THE '' CASE IS THE ONE TO GUARD, and it is guarded twice. The Go side maps
-- "" to NULL (nullIfEmpty); the CHECK below is the backstop for any writer that
-- does not, because a stored '' would collide with EVERY OTHER stored '' and the
-- second hand-keyed voucher in a company would be refused as a duplicated bill.
--
-- ⚠️ ACCEPTANCE IS TERMINAL, INCLUDING ACROSS A DELETE. Deleting the voucher
-- tombstones the head; it does not remove the row, so the draft stays spent. That
-- matches insightd's own `accepted` state, which is terminal for the same reason:
-- an operator who deletes a mis-keyed purchase must key its replacement
-- deliberately, not have a second one offered to them by the inbox.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration.

ALTER TABLE voucher_head
    ADD COLUMN IF NOT EXISTS source_draft_id TEXT NULL;

COMMENT ON COLUMN voucher_head.source_draft_id IS
    'The insightd PurchaseVoucherDraft.draft_id this voucher was ACCEPTED from, '
    'or NULL for a voucher nobody accepted from a draft. Not a status and not a '
    'copy of one: it records WHICH VOUCHER A DRAFT PRODUCED, which is cored''s '
    'own fact. The partial unique index below is what makes accepting the same '
    'AI bill draft twice impossible rather than merely unlikely -- it is enforced '
    'in the SAME transaction that writes the voucher, so the loser posts nothing '
    'at all.';

-- ⚠️ '' IS NOT A DRAFT ID. Without this, two unrelated vouchers written by a
-- caller that sent an empty string would collide with each other and the SECOND
-- ONE WOULD BE REFUSED as a duplicate bill -- a refusal that names a draft
-- neither voucher came from.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'voucher_head_source_draft_id_non_empty'
    ) THEN
        EXECUTE 'ALTER TABLE voucher_head ADD CONSTRAINT '
             || 'voucher_head_source_draft_id_non_empty '
             || 'CHECK (source_draft_id IS NULL OR source_draft_id <> '''')';
    END IF;
END
$$;

-- THE GUARANTEE. One head per (company, draft), enforced only where a draft id
-- is present.
CREATE UNIQUE INDEX IF NOT EXISTS ux_voucher_head_source_draft_id
    ON voucher_head (company_id, source_draft_id)
    WHERE source_draft_id IS NOT NULL;
