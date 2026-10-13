-- 0088_bank_statement_match.sql
-- BANK STATEMENT MATCHING (T4 slice 2b): the link between a row the BANK sent
-- and a leg the BOOKS hold -- recorded only once a HUMAN has asserted it.
--
-- Design memo: docs/design/t4-slice2-model.md. Read §0 first (an ambiguous
-- match is never auto-cleared), then §2.1, which is why this table does NOT
-- record a clearance and instead points at the row that does.
--
-- ===========================================================================
-- ⛔ WHAT THIS TABLE IS NOT: IT IS NOT THE CANDIDATE LIST
-- ===========================================================================
-- The memo calls it "the candidate links", and slice 2b DEPARTS from that word
-- deliberately. Candidates are DERIVED and never stored, for the reason every
-- report in this product is derived: a stored candidate is a snapshot, and the
-- moment a voucher is altered, deleted, cleared by the manual loop, or claimed
-- by another line, the stored answer is wrong while still looking authoritative.
-- An operator would then be choosing from a list of proposals that no longer
-- describe the books -- which is the "confident-looking artefact" failure this
-- repository's register is full of, in its most expensive form: it would be
-- offering a match to a voucher that has already been matched.
--
-- So `internal/bankstatement.Propose` recomputes the tiers on every read, and
-- THIS TABLE HOLDS ONLY RESOLUTIONS -- the ones a person asserted. The
-- distinction is the handoff's own design rule for this slice: a proposed match
-- and a reconciled one must never render the same, and the surest way to keep
-- them apart is for only one of them to exist in the database.
--
-- ===========================================================================
-- THE CLEARANCE LIVES IN bank_reconciliation, AND THAT IS A FOREIGN KEY
-- ===========================================================================
-- Memo §2.1: a resolved match writes a `bank_reconciliation` row, exactly as the
-- manual bank-date loop does, because that table carries the alter-detection
-- mechanism (0045's header) and a second source of "cleared" is two statements
-- that can disagree.
--
-- ⭐ THIS MIGRATION MAKES THAT STRUCTURAL RATHER THAN A CONVENTION. A matched
-- row carries `reconciliation_id`, a composite FK to bank_reconciliation ON
-- DELETE CASCADE. Three consequences, all of them invariants the memo asks for
-- and none of them enforced by discipline:
--
--   * B-2 -- "a resolved match writes a bank_reconciliation row and NOTHING
--     ELSE records clearance". A match row cannot exist without one; the FK
--     refuses it.
--   * B-8 -- "unlinking removes the bank_reconciliation row AND returns the
--     line to unmatched". The EXISTING DELETE .../bank-reconciliation/{...}
--     route deletes the clearance and the match row cascades away with it. Alt+F8
--     is therefore not a second write path that could drift from the first; it is
--     the same one.
--   * B-9 -- "a voucher altered after matching shows as BROKEN". The matcher
--     gains no exemption because it stores no clearance of its own: the BRS reads
--     bank_reconciliation, compares cleared_amount/cleared_dr_cr against the
--     current version, and flags the item exactly as it does for a manual tick.
--
-- ===========================================================================
-- ⛔ THE CROSS-ACCOUNT MATCH, AND WHY ledger_id IS CARRIED HERE
-- ===========================================================================
-- A match links a line to a clearance. Both know their own bank account, and
-- NOTHING would stop them disagreeing -- a line off the HDFC statement resolved
-- against a leg cleared on the ICICI account. That failure is silently
-- plausible in the worst way: both rows are real, both amounts are correct, both
-- accounts still balance, and the only symptom is that two reconciliations are
-- quietly wrong.
--
-- So the ledger is carried on the match and pinned to BOTH sides by
-- three-column foreign keys. The two UNIQUE constraints those FKs need did not
-- exist; they are added below, additively, guarded by pg_constraint.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration. Hence IF NOT EXISTS on tables and indexes and
-- pg_constraint guards on every ADD CONSTRAINT (which has no IF NOT EXISTS
-- form).
--
-- ⚠️ NO ACL MIGRATION ACCOMPANIES THIS ONE, AND THAT IS A DECISION -- see the
-- block at the foot of this file.

-- ---------------------------------------------------------------------------
-- The two composite UNIQUEs the three-column FKs below require.
--
-- Both are ADDITIVE to tables that already exist (0045 and 0062). Neither
-- changes what those tables accept: (company_id, id) is already unique on each,
-- so appending a third functionally-dependent column removes nothing.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'bank_statement_line_company_ledger_scope'
    ) THEN
        ALTER TABLE bank_statement_line
            ADD CONSTRAINT bank_statement_line_company_ledger_scope
            UNIQUE (company_id, id, ledger_id);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'bank_reconciliation_company_ledger_scope'
    ) THEN
        ALTER TABLE bank_reconciliation
            ADD CONSTRAINT bank_reconciliation_company_ledger_scope
            UNIQUE (company_id, id, ledger_id);
    END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- THE RESOLUTION: what a person decided about one statement line.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS bank_statement_match (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,

    -- The statement line this resolution is about.
    line_id       UUID NOT NULL,
    -- The bank account, carried so the two three-column FKs below can refuse a
    -- cross-account match. See the header.
    ledger_id     UUID NOT NULL,

    -- ---------------------------------------------------------------------
    -- WHAT WAS DECIDED. Exactly two things can be true of a line a human has
    -- looked at, and they are not the same statement:
    --
    --   'matched'   -- this line IS that voucher leg. Carries a clearance.
    --   'set-aside' -- this line corresponds to NOTHING in the books, and I have
    --                  checked. Carries no clearance and moves no balance.
    --
    -- ⛔ 'set-aside' IS NOT A WEAKER FORM OF 'matched' AND MUST NEVER RENDER AS
    -- ONE. It is the operator recording that a row is bank-only -- a real
    -- category (RESEARCH.md:873), the bank charge or standing order no voucher
    -- exists for yet. It asserts nothing about the books, changes no balance,
    -- and appears in the Bank Reconciliation Statement not at all, because the
    -- BRS reads bank_reconciliation and this row writes none.
    -- ---------------------------------------------------------------------
    status        TEXT NOT NULL,

    -- The clearance this match produced, in the ONE table that records
    -- clearance. NULL for a set-aside. See the header for the three invariants
    -- this column carries.
    reconciliation_id UUID,

    -- ---------------------------------------------------------------------
    -- ON WHAT EVIDENCE. The four tiers are RESEARCH.md:879's own, and the
    -- column exists because the answer CANNOT BE RECOVERED LATER: re-running
    -- the proposer months afterwards asks about today's books, not about what
    -- the operator was shown when they decided.
    --
    --   'exact'     -- amount, direction and a strong key agreed; one candidate
    --   'potential' -- amount and direction agreed, the date did not; one candidate
    --   'partial'   -- amounts did not agree one-for-one (a part payment or a
    --                  netted pair); NEVER proposed for acceptance
    --   'manual'    -- a person's say-so alone. The proposer offered this leg at
    --                  no tier, or offered nothing at all.
    --
    -- ⛔ THE SERVER DERIVES THIS AND NEVER ACCEPTS IT FROM A CLIENT. A tier
    -- taken on trust is an audit trail that records what the caller CLAIMED the
    -- evidence was, which is worth less than no column at all -- it would read
    -- as evidence while being assertion. internal/bankstatement re-runs the
    -- comparison at resolve time and stores its OWN verdict; a leg the proposer
    -- does not offer is stored 'manual' however the request described it.
    --
    -- A set-aside is 'manual': nobody's arithmetic supports it and it is not
    -- meant to.
    -- ---------------------------------------------------------------------
    tier          TEXT NOT NULL,

    -- Free text, for a set-aside especially: "bank charge, no voucher raised".
    note          TEXT,

    -- Audit. matched_at IS an instant, unlike the statement's value_date.
    matched_by    TEXT NOT NULL,
    matched_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- ONE resolution per statement line. A line is one thing the bank did; it
    -- is either that voucher, or nothing, and a second row would make "what did
    -- we decide about this line?" unanswerable -- the same reasoning
    -- bank_reconciliation_one_per_leg records for a leg.
    CONSTRAINT bank_statement_match_one_per_line UNIQUE (company_id, line_id),
    CONSTRAINT bank_statement_match_company_scope UNIQUE (company_id, id),

    -- The three-column FKs. Each pins the match's ledger to the ledger the row
    -- on the other end already believes it belongs to, so a cross-account match
    -- is refused BY THE DATABASE rather than by the service remembering to look.
    CONSTRAINT bank_statement_match_line_fk
        FOREIGN KEY (company_id, line_id, ledger_id)
        REFERENCES bank_statement_line (company_id, id, ledger_id)
        ON DELETE CASCADE,
    CONSTRAINT bank_statement_match_reconciliation_fk
        FOREIGN KEY (company_id, reconciliation_id, ledger_id)
        REFERENCES bank_reconciliation (company_id, id, ledger_id)
        ON DELETE CASCADE,

    CONSTRAINT bank_statement_match_status_known
        CHECK (status IN ('matched', 'set-aside')),
    CONSTRAINT bank_statement_match_tier_known
        CHECK (tier IN ('exact', 'potential', 'partial', 'manual')),

    -- ⛔ THE CHECK THAT KEEPS THE TWO STATUSES FROM BLURRING. A 'matched' row
    -- WITHOUT a clearance would be a match that reconciled nothing while
    -- reading as reconciled; a 'set-aside' row WITH one would be a clearance
    -- asserted by a row whose whole meaning is "there is nothing to clear".
    -- Both are the same defect -- a link the system cannot honestly state --
    -- and the database refuses each.
    CONSTRAINT bank_statement_match_clearance_matches_status
        CHECK (
            (status = 'matched'   AND reconciliation_id IS NOT NULL) OR
            (status = 'set-aside' AND reconciliation_id IS NULL)
        )
);

-- ONE resolution per CLEARANCE, so two statement lines cannot both claim the
-- same voucher leg. Without it a part-payment mis-resolved twice would leave
-- one leg cleared by two lines, the second silently overwriting the first's
-- meaning while the BRS -- which counts the leg once -- showed nothing wrong.
--
-- A partial unique index rather than a table constraint, because set-aside rows
-- are all NULL here and a plain UNIQUE would permit only one of those in total.
CREATE UNIQUE INDEX IF NOT EXISTS bank_statement_match_one_per_clearance
    ON bank_statement_match (company_id, reconciliation_id)
    WHERE reconciliation_id IS NOT NULL;

-- The read path: "what has been resolved on this account?", which is what the
-- proposer asks before it offers anything.
CREATE INDEX IF NOT EXISTS bank_statement_match_ledger
    ON bank_statement_match (company_id, ledger_id, status);

COMMENT ON TABLE bank_statement_match IS
    'T4 slice 2b. What a PERSON decided about one imported statement line: that '
    'it is a particular voucher leg (''matched'', carrying the '
    'bank_reconciliation row that records the clearance), or that it '
    'corresponds to nothing in the books (''set-aside'', carrying none). '
    'CANDIDATES ARE NOT STORED HERE -- they are derived on every read by '
    'internal/bankstatement.Propose, because a stored candidate goes stale '
    'silently while still looking authoritative. See the file header.';

COMMENT ON COLUMN bank_statement_match.tier IS
    'The evidence the resolution rested on, DERIVED BY THE SERVER at resolve '
    'time and never accepted from a client: exact / potential / partial / '
    'manual. A leg the proposer does not offer is recorded ''manual'' however '
    'the request described it, because a tier taken on trust records what the '
    'caller claimed rather than what was true.';

COMMENT ON COLUMN bank_statement_match.status IS
    '''matched'' or ''set-aside''. A set-aside is the operator recording that a '
    'bank row corresponds to NOTHING in the books -- a real category, not a '
    'weaker match. It writes no clearance and moves no balance, and a client '
    'must never render it as reconciled.';

COMMENT ON COLUMN bank_statement_match.reconciliation_id IS
    'The bank_reconciliation row this match produced -- the ONE table that '
    'records clearance (memo §2.1). ON DELETE CASCADE, which is what makes '
    'un-linking a match and un-ticking a bank date the SAME operation rather '
    'than two that can drift apart.';

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, policy with BOTH USING
-- and WITH CHECK -- the same block 0062 writes, for the same reasons. USING
-- alone would let a session write a row for another company that it can never
-- see or correct; WITH CHECK alone would leak every tenant's matching on read.
-- FORCE because migrations run as the table owner and an owner bypasses its own
-- policies unless forced.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    EXECUTE 'ALTER TABLE bank_statement_match ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE bank_statement_match FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON bank_statement_match';
    EXECUTE 'CREATE POLICY tenant_isolation ON bank_statement_match '
            'USING (company_id = app_current_company_id()) '
            'WITH CHECK (company_id = app_current_company_id())';
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON bank_statement_match TO tally_app;

-- ===========================================================================
-- ⛔ WHY THERE IS NO seed_company_auth_defaults RESTATEMENT IN THIS FILE
-- ===========================================================================
-- Every migration that adds a `report:NAME` must write Data Entry a `disallow`,
-- because 0011's coarse `allow display report` reaches a BARE resource and
-- internal/authz matches it against ANY report name -- so the GRANT is the
-- default and the REFUSAL is what must be written (defect D95). Twelve
-- migrations do it: 0026, 0028, 0033, 0035, 0046, 0063, 0068, 0072, 0073, 0076,
-- 0086, 0087.
--
-- ⭐ SLICE 2b ADDS NO REPORT NAME AND NO RESOURCE TOKEN, so it adds no rule --
-- and the reason is worth stating, because "no ACL migration" is exactly the
-- shape of an oversight:
--
--   * The PROPOSALS read is GET .../bank-statement/{ledgerID}/matches. It sits
--     under the /bank-statement/ prefix that routeAction already maps to
--     Display / report:bank-statement, which migration 0063 already DISALLOWS
--     to Data Entry at specificity 2. Proposing a match discloses the bank's own
--     file, so it is governed by the rule that governs the bank's own file.
--
--   * The RESOLVE write is the EXISTING PUT
--     .../bank-reconciliation/{ledgerID}/{voucherID}/{legNo}, which slice 2b
--     extends with an optional statement_line_id rather than adding a route.
--     ⭐ THAT IS THE LOAD-BEARING CHOICE IN THIS SLICE. Matching must not become
--     a SECOND WAY TO ACQUIRE THE AUTHORITY TO ASSERT A CLEARANCE: a level
--     refused `alter bank-reconciliation` but granted the statement would
--     otherwise reconcile by matching what it may not reconcile by ticking, and
--     the rule list would say the opposite. Sharing the route makes that
--     impossible rather than merely unintended -- and the back-dated cutoff,
--     the WebAuthn step-up and peekBankDate's `bank_date` all keep working
--     BECAUSE it is the same route, none of which would be true of a new one.
--
--   * The SET-ASIDE write is POST .../bank-statement/{ledgerID}/lines/{lineID}
--     -> Create / bank-statement, again by the prefix arm that already exists.
--     It annotates the bank's file and writes no clearance, so it is governed
--     as the file is: 0063 grants Data Entry nothing here, deliberately.
--
-- The seeder census (backend/migrations/seeder_census_test.go) therefore still
-- reads 16 PERFORMs, unchanged from 0087, and that number is the assertion that
-- this file added no rule rather than forgot one.
