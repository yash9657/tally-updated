-- 0085_budgets.sql
-- THE SECOND COLUMN. A budget is the only figure in this product that is not
-- derived from a voucher, and that is the entire point of it.
--
-- Migration number from PLAN-TALLY-PARITY.md §2.5, which reserves 0085 for T11's
-- schema and 0086 for its ACL. ⛔ NOTE FOR THE ACL HALF: 0086 IS MANDATORY AND
-- CANNOT BE FOLDED INTO THIS FILE. `report:budget-variance` is a `report:NAME`,
-- and migration 0011 seeds the Data Entry level a coarse `allow display report`
-- that internal/authz matches against ANY report name -- so the report arrives
-- GRANTED and the REFUSAL is the thing that has to be written. See 0086.
--
-- ===========================================================================
-- ⛔⛔ A BUDGET POSTS NOTHING. NOTHING IN THIS FILE MAY EVER TOUCH THE LEDGER.
-- ===========================================================================
-- There is no voucher, no `posting` row, no ledger_balance effect and no entry in
-- the audit hash chain, and none of those is an omission to be corrected later.
-- A budget is a COMPARISON DIMENSION: it is what an actual is measured against,
-- and a plan that posted would appear in the Trial Balance as money that does not
-- exist. That is wrong in a way no report can undo, because the Balance Sheet
-- would foot -- against a fiction.
--
-- The practical consequence, stated so it is not rediscovered: a budget cannot be
-- faked by keying journals, and the FeaturesScreen row for T11 says exactly that.
-- It is also why this feature inherits none of the machinery every posting
-- feature must satisfy -- the double-entry trigger, the back-dated cutoff, GST
-- derivation, the lot queues. It inherits RLS, like every tenant table, and the
-- ACL, which is 0086's job.
--
-- ===========================================================================
-- ⛔ WHAT THIS FILE DELIBERATELY DOES *NOT* MODEL, AND WHY EACH ABSENCE IS A
-- DECISION RATHER THAN A GAP
-- ===========================================================================
-- Each of these is stated on the FeaturesScreen row too, because a budget report
-- that silently answers a question it was not asked is worse than an absent one.
--
--   1. NO PRO-RATA / PART-PERIOD COMPARISON. A budget has ONE period and the
--      variance report runs over that whole period. Comparing a full-year budget
--      against four months of actuals requires a SPREAD RULE -- straight-line by
--      day, by month, or seasonal -- and Tally makes the operator choose one.
--      There is no basis in these books to pick for them, and apportioning
--      silently by day count would produce a confident number for a business
--      whose sales are festival-weighted and be wrong by a mile every October.
--      So the report refuses to narrow rather than inventing the rule.
--
--   2. NO BUDGET REVISION HISTORY. A budget is a PLAN, and plans are revised, so
--      budget and budget_line are ordinary mutable rows -- UPDATE and DELETE are
--      granted below, which is the opposite of `posting` (posting_immutable
--      raises on both). A revised budget therefore overwrites the previous one
--      and the earlier figure is gone. That is a real limitation and it is
--      NAMED: what a business often wants is "the original budget, the revised
--      budget, and the actual" as three columns. Adding it later is a
--      budget_revision table, not a change to these two.
--
--   3. NO CASCADE FROM A DELETED TARGET. A budget line points at a group, a
--      ledger or a cost centre by FK with no ON DELETE clause, so the database
--      REFUSES to delete a ledger that a budget names. That is the honest
--      direction: a budget line silently vanishing because someone tidied the
--      chart of accounts is a variance report that gets quietly shorter.
--
-- ===========================================================================
-- ⛔⛔ THE TWO BASES ARE THE WHOLE FEATURE. GETTING THIS WRONG IS SILENT.
-- ===========================================================================
-- Tally sets every budget line on either "Closing Balance" or "Nett
-- Transactions", and the distinction is NOT cosmetic. A budget of Rs 10,00,000
-- against a CLOSING BALANCE and the same Rs 10,00,000 against NETT TRANSACTIONS
-- FOR THE PERIOD are different numbers, and the same variance figure means
-- different things:
--
--   CLOSING_BALANCE   -- a POSITION as at the period end. Opening balance plus
--                        every movement up to to_date. "Sundry Debtors must not
--                        exceed 40 lakh at year end" is this.
--   NETT_TRANSACTIONS -- MOVEMENT INSIDE [from_date, to_date], opening balance
--                        deliberately excluded. "Travel spend for the year must
--                        not exceed 5 lakh" is this.
--
-- ⛔ A REPORT THAT SILENTLY USED THE WRONG ONE IS WORSE THAN AN ABSENT FEATURE,
-- so `basis` is NOT NULL with no default -- every line states its own -- and the
-- variance report prints the basis on every row. It is per LINE and not per
-- budget because Tally sets it per line and because one budget legitimately
-- mixes them: a year's plan holds both a closing-debtors ceiling and a travel
-- spend cap.
--
-- ⭐ THE ACTUAL SIDE OF BOTH ALREADY EXISTS AND IS NOT REIMPLEMENTED.
-- internal/reporting has exactly these two loaders, for exactly this reason
-- (see its PERIOD SEMANTICS note): loadClosingBalances(asOn) is the first,
-- loadPeriodMovement(from, to) is the second. The budget column is the new work;
-- the actual column is the Trial Balance's and the Profit & Loss's own substrate.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration.

-- ===========================================================================
-- budget -- THE HEAD: A NAME AND A PERIOD
-- ===========================================================================
CREATE TABLE IF NOT EXISTS budget (
    id                UUID PRIMARY KEY,
    company_id        UUID NOT NULL,
    name              TEXT NOT NULL,

    -- ⛔ BOTH BOUNDS NOT NULL, unlike every report period in this codebase, which
    -- takes optional bounds. A report with no bounds means "everything ever
    -- posted", which is a sensible default for a POSITION. A budget with no
    -- bounds is a plan for no stated period, and the CLOSING_BALANCE basis would
    -- then have no date to be "as at" -- so the variance report could not name
    -- what it was comparing. The period is part of what a budget IS.
    from_date         DATE NOT NULL,
    to_date           DATE NOT NULL,

    created_by        TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT budget_company_scope UNIQUE (company_id, id),
    CONSTRAINT budget_name_not_blank CHECK (btrim(name) <> ''),
    -- A single-day budget is legal (to_date = from_date); a reversed one is not.
    -- Reversed bounds would make every NETT_TRANSACTIONS line match no movement
    -- at all and report the whole budget as unspent, which reads as a business
    -- fact rather than as a typo.
    CONSTRAINT budget_period_ordered CHECK (to_date >= from_date),
    CONSTRAINT budget_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id)
);
-- Case-insensitive within a company, matching every other master in this schema:
-- "Q1 Plan" and "q1 plan" are one plan to an operator, and two of them is how
-- half the lines end up on each and both look complete.
CREATE UNIQUE INDEX IF NOT EXISTS budget_name_unique
    ON budget (company_id, lower(btrim(name)));

COMMENT ON TABLE budget IS
    'A named plan over ONE period. Posts nothing and is not in the hash chain: a '
    'budget is what an actual is COMPARED AGAINST, and a plan that posted would '
    'show up in the Trial Balance as money that does not exist. Mutable by '
    'design -- plans are revised -- which is the opposite of `posting`; the '
    'consequence is that there is no revision history, stated in the migration '
    'header and on the FeaturesScreen row.';

-- ===========================================================================
-- budget_line -- ONE TARGET, ONE BASIS, ONE FIGURE
-- ===========================================================================
CREATE TABLE IF NOT EXISTS budget_line (
    id                UUID PRIMARY KEY,
    company_id        UUID NOT NULL,
    budget_id         UUID NOT NULL,

    -- ⛔ THE DIMENSION IS EXPLICIT AND NOT INFERRED FROM WHICH ID IS NON-NULL.
    -- Inferring it would make a row with two ids set, or none, a row whose
    -- meaning depends on the reader -- and the exactly-one CHECK below is far
    -- easier to state, and to trust, against a declared dimension than against a
    -- three-way nullability pattern. It is also what the report GROUPS BY, and a
    -- grouping key derived by a CASE over three nullable columns is a grouping
    -- key that will one day be derived differently in two places.
    dimension         TEXT NOT NULL,

    account_group_id  UUID NULL,
    ledger_id         UUID NULL,
    cost_centre_id    UUID NULL,

    -- CLOSING_BALANCE or NETT_TRANSACTIONS. ⛔ NO DEFAULT, DELIBERATELY. See the
    -- migration header: these two answer different questions, and a default
    -- would mean a line whose author never chose could still be reported against
    -- with total confidence. An operator who must state it cannot be silently
    -- wrong about it.
    basis             TEXT NOT NULL,

    -- ⛔ THE SIDE THE FIGURE IS EXPECTED ON, and it is NOT NULL for every line
    -- including the ones where it could be derived.
    --
    -- For a GROUP or a LEDGER the natural side follows from the group's nature
    -- (debit for ASSET/EXPENSE, credit for LIABILITY/INCOME) and
    -- internal/masters fills it in when the caller omits it -- so an operator
    -- never types it for the ordinary case. It is STORED anyway, for two
    -- reasons: a contra expectation is real (a bank account budgeted to sit
    -- overdrawn is a credit balance on an ASSET ledger), and a COST CENTRE has
    -- no nature at all, so without this column the cost-centre arm of the report
    -- would need sign logic the other two arms did not have. One column here
    -- buys uniform arithmetic in every arm.
    dr_cr             CHAR(1) NOT NULL,

    -- A MAGNITUDE, never a sign -- dr_cr carries the direction, exactly as
    -- posting_cost_allocation.amount leaves the sign to the posting's dr_cr and
    -- stock_movement.qty leaves it to `direction`.
    --
    -- NUMERIC(19,4), the money type everything in this schema uses. ⛔ Never a
    -- float: a budget compared against a NUMERIC actual through a float is a
    -- variance with a rounding tail that grows with the figure.
    amount            NUMERIC(19,4) NOT NULL,

    -- Free text an operator can leave for the next reader ("assumes 3 new hires
    -- in Q2"). Not interpreted anywhere.
    note              TEXT NOT NULL DEFAULT '',

    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT budget_line_company_scope UNIQUE (company_id, id),

    CONSTRAINT budget_line_dimension_known
        CHECK (dimension IN ('GROUP', 'LEDGER', 'COST_CENTRE')),
    CONSTRAINT budget_line_basis_known
        CHECK (basis IN ('CLOSING_BALANCE', 'NETT_TRANSACTIONS')),
    CONSTRAINT budget_line_dr_cr_known
        CHECK (dr_cr IN ('D', 'C')),

    -- ⛔ A BUDGET OF ZERO IS NOT A BUDGET, IT IS THE ABSENCE OF ONE, and the two
    -- must not be spellable the same way. A zero line would divide the variance
    -- percentage by nothing, and "0% of budget used" against a budget of zero is
    -- a sentence with no meaning. The report has a distinct answer for a target
    -- with no budget (see UNBUDGETED in reporting/budget_variance.go) and that is
    -- where "we plan to spend nothing here" belongs -- as no line at all.
    CONSTRAINT budget_line_amount_positive CHECK (amount > 0),

    -- ⛔⛔ EXACTLY ONE TARGET, AND IT MUST BE THE ONE `dimension` NAMES.
    -- Both halves matter. Without the "exactly one" half a line could name a
    -- ledger AND a cost centre and be counted twice by a report that grouped by
    -- either. Without the "must match dimension" half a line could declare
    -- itself a LEDGER line while carrying only a group id, and the LEDGER arm of
    -- the report would join on NULL, find nothing, and report the whole budget
    -- as unspent -- a plausible-looking number for a line that was never
    -- readable at all.
    CONSTRAINT budget_line_target_matches_dimension CHECK (
        (dimension = 'GROUP'
             AND account_group_id IS NOT NULL
             AND ledger_id        IS NULL
             AND cost_centre_id   IS NULL)
     OR (dimension = 'LEDGER'
             AND ledger_id        IS NOT NULL
             AND account_group_id IS NULL
             AND cost_centre_id   IS NULL)
     OR (dimension = 'COST_CENTRE'
             AND cost_centre_id   IS NOT NULL
             AND account_group_id IS NULL
             AND ledger_id        IS NULL)
    ),

    -- ⛔⛔ A COST CENTRE HAS NO CLOSING BALANCE, SO IT MAY NOT BE BUDGETED ON ONE.
    -- This is a real modelling statement and not a restriction for tidiness.
    -- A cost centre is not an account: it holds no opening balance and carries no
    -- position. What exists is posting_cost_allocation rows, each attached to a
    -- posting on a dated voucher -- i.e. FLOWS. Summing every allocation a centre
    -- has ever received and calling it a "closing balance" produces the total
    -- cost ever booked to that department since the books began, which is a
    -- number no business plans against and which would silently make a
    -- year's-worth budget look 90% unspent in year one and 300% overspent in
    -- year four.
    --
    -- Tally agrees: its cost centre budgets are on transactions. The refusal is
    -- HERE, in the schema, rather than only in Go, because a report cannot detect
    -- the difference after the fact -- both bases return a real, correctly-footed
    -- number, and only one of them answers the question asked.
    CONSTRAINT budget_line_cost_centre_is_transactional CHECK (
        dimension <> 'COST_CENTRE' OR basis = 'NETT_TRANSACTIONS'
    ),

    CONSTRAINT budget_line_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id),
    -- CASCADE, and it is honest here in a way it would not be on `posting`:
    -- deleting a budget SHOULD take its lines, they have no meaning apart from
    -- it, and a budget carries no signature for orphaned lines to contradict.
    CONSTRAINT budget_line_budget_fk
        FOREIGN KEY (company_id, budget_id)
        REFERENCES budget (company_id, id) ON DELETE CASCADE,

    -- ⛔ COMPANY-SCOPED COMPOSITE FKs, not single-column ones, on all three
    -- targets. The RLS policy below checks the LINE's company_id and has nothing
    -- to say about the row it points at, so a single-column FK to ledger(id)
    -- would let company A's budget name company B's ledger: the write would be
    -- accepted and the leak would surface as company A's variance report quietly
    -- reporting company B's actuals. Exactly the argument migration 0040 makes
    -- for posting_cost_allocation.
    --
    -- ⚠️ NO ON DELETE CLAUSE ON ANY OF THE THREE, deliberately -- see the header.
    -- The database refuses to delete a ledger a budget names, rather than letting
    -- the line disappear and the variance report get quietly shorter.
    CONSTRAINT budget_line_group_fk
        FOREIGN KEY (company_id, account_group_id)
        REFERENCES account_group (company_id, id),
    CONSTRAINT budget_line_ledger_fk
        FOREIGN KEY (company_id, ledger_id)
        REFERENCES ledger (company_id, id),
    CONSTRAINT budget_line_cost_centre_fk
        FOREIGN KEY (company_id, cost_centre_id)
        REFERENCES cost_centre (company_id, id)
);

-- ⛔⛔ ONE LINE PER (BUDGET, TARGET) -- THREE PARTIAL INDEXES, NOT ONE COMPOSITE.
--
-- A single UNIQUE (company_id, budget_id, dimension, account_group_id,
-- ledger_id, cost_centre_id) WOULD NOT WORK, and the reason is the classic one:
-- in SQL two NULLs are not equal, so every row for a LEDGER line has NULLs in the
-- other two columns and the constraint never fires. Two budget lines for the same
-- ledger in the same budget would both be stored, and the variance report would
-- show that ledger twice with the actual counted in full against each -- a report
-- that adds up to more budget than anyone set.
--
-- Partial indexes on the non-null column per dimension have no such hole.
--
-- ⚠️ NOTE WHAT IS *NOT* UNIQUE HERE: `basis` is not in any key. That is
-- deliberate. One target gets ONE line per budget, and its basis is a property of
-- that line -- not a second key that would let the same ledger carry both a
-- closing-balance and a nett-transactions budget in one plan. Both are legitimate
-- questions, but two rows for one ledger in one budget is how a report totals a
-- budget column that no operator ever set. A business wanting both writes two
-- budgets.
CREATE UNIQUE INDEX IF NOT EXISTS budget_line_one_per_group
    ON budget_line (company_id, budget_id, account_group_id)
    WHERE dimension = 'GROUP';
CREATE UNIQUE INDEX IF NOT EXISTS budget_line_one_per_ledger
    ON budget_line (company_id, budget_id, ledger_id)
    WHERE dimension = 'LEDGER';
CREATE UNIQUE INDEX IF NOT EXISTS budget_line_one_per_cost_centre
    ON budget_line (company_id, budget_id, cost_centre_id)
    WHERE dimension = 'COST_CENTRE';

-- The variance report reads every line of one budget, grouped by dimension.
CREATE INDEX IF NOT EXISTS budget_line_by_budget
    ON budget_line (company_id, budget_id, dimension);

COMMENT ON TABLE budget_line IS
    'One budgeted figure: a target (group, ledger or cost centre), a BASIS '
    '(closing balance or nett transactions -- these answer different questions '
    'and the report prints which), the side it is expected on, and a positive '
    'magnitude. A cost centre may only be budgeted on nett transactions: it holds '
    'no position, only allocations against dated postings.';

COMMENT ON COLUMN budget_line.basis IS
    'CLOSING_BALANCE = a position as at the budget period end, opening balance '
    'folded in. NETT_TRANSACTIONS = movement inside the period, opening balance '
    'excluded. NOT NULL with no default: the same figure means different things '
    'under each, so a line that never chose must not be reportable.';

COMMENT ON COLUMN budget_line.dr_cr IS
    'The side the budget is expected on. Filled from the target group''s nature '
    'by internal/masters when a caller omits it, but stored: a contra '
    'expectation is real, and a cost centre has no nature to derive it from.';

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, policy with BOTH USING
-- and WITH CHECK.
--
-- USING alone would let a session write a budget for another company it can
-- never see or correct; WITH CHECK alone would leak every tenant's plan on read
-- -- and a plan is a sharper disclosure than the books it is compared against,
-- because it states what a business INTENDS. FORCE because migrations run as the
-- table owner and an owner bypasses its own policies unless forced.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['budget', 'budget_line'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I '
            'USING (company_id = app_current_company_id()) '
            'WITH CHECK (company_id = app_current_company_id())', t);
    END LOOP;
END
$$;

-- UPDATE and DELETE are granted, unlike `posting`. A budget is a PLAN and plans
-- are revised -- see the header, and note the cost: no revision history.
GRANT SELECT, INSERT, UPDATE, DELETE ON budget      TO tally_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON budget_line TO tally_app;
