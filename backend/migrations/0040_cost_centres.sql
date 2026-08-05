-- 0040_cost_centres.sql
-- WHICH DEPARTMENT BORE THIS COST -- the allocation dimension postings have
-- never had.
--
-- Migration number from PLAN-TALLY-PARITY.md §2.5, which reserves 0040 for T2's
-- schema. ⛔ NOTE FOR THE ACL HALF: §2.5 also reserves 0041 for T2's ACL rules
-- and 0041 CANNOT BE USED FOR THEM. seed_company_auth_defaults is extended only
-- by restating its whole plpgsql body, migrations.go applies files in
-- sort.Strings order, and the highest-numbered restatement is the only body that
-- survives -- currently 0071_seeder_order_repair.sql. A rule set written at 0041
-- would be silently overwritten by 0071's older body on every fresh database,
-- which is precisely the defect 0071 exists to repair. The cost reports' ACL
-- counterweight therefore takes a number ABOVE 0071 and 0041 is left unused.
--
-- ===========================================================================
-- A COST CENTRE AND A COST CATEGORY ARE DIFFERENT AXES. THIS IS THE WHOLE MODEL.
-- ===========================================================================
-- The naive shape is one nullable cost_centre_id column on posting, and it is
-- wrong in a way that only shows up once a business uses the feature properly.
--
-- A category is an AXIS OF ANALYSIS; a centre is a POSITION ON THAT AXIS. Tally
-- ships "Primary Cost Category" and businesses add their own -- Department,
-- Branch, Project, Salesperson. The point of having more than one is PARALLEL
-- ALLOCATION: the SAME Rs 1,00,000 of salary cost is allocated once across
-- Departments (Sales 60,000 / Admin 40,000) and again, in full, across Branches
-- (Mumbai 70,000 / Delhi 30,000). Both allocations are complete and both are
-- correct, and they describe the same money from two directions.
--
-- Two consequences follow, and every naive model gets at least one wrong:
--
--   1. THE SUM-TO-POSTING INVARIANT IS PER CATEGORY, NOT PER POSTING. Summing
--      the example above across all categories gives 2,00,000 against a
--      1,00,000 posting. A constraint that checked the posting total would make
--      the second category impossible to enter; one that checked nothing would
--      let 60,000 of Department allocation stand against 1,00,000 of cost with
--      40,000 silently unattributed. So the check is grouped by
--      (posting, category) -- see cost_allocation_sums_to_posting below.
--
--   2. AN ALLOCATION BELONGS TO BOTH. cost_centre_id alone is not enough to
--      group by, because the category is what separates the two complete sets.
--      It is stored explicitly AND constrained to agree with the centre's own
--      category (cost_allocation_centre_matches_category), because a composite
--      FK is the only thing that stops an allocation claiming a Mumbai centre
--      sits on the Department axis.
--
-- T11 (budgets) and T24 both inherit this shape, so an error here costs three
-- tracks rather than one.
--
-- ===========================================================================
-- ALLOCATIONS ARE INSIDE THE HASHED PAYLOAD. THE TABLE IS THE SECOND RENDERING.
-- ===========================================================================
-- "Which department bore this cost" is what a manager's budget and a segment
-- disclosure are both computed from, so it is part of the voucher's AUDIT
-- IDENTITY rather than metadata beside it -- exactly the argument inward_doc
-- (migration 0032) records. It rides in CanonicalPayload.CostAllocations and is
-- written to this table from the SAME in-memory value inside the SAME
-- transaction, so the signature and the rows cannot disagree.
--
-- ⚠️ WHY THIS TABLE KEYS ON posting_id WHILE THE PAYLOAD KEYS ON A SEQUENCE.
-- The payload cannot name a posting id: `posting` rows are re-created wholesale
-- by every ALTER, so an id in the signed bytes would change on correction. It
-- uses the 0-based index into its own postings array instead. This table has the
-- opposite requirement -- the constraint trigger must compare against the
-- posting's AMOUNT, and `posting` has no ordinal column to join a sequence to.
-- Both keys are derived from ONE list in ONE transaction by ledger.post(), which
-- inserts postings in payload order and therefore knows both. Neither key is
-- reconstructed from the other afterwards.
--
-- This is NOT the 0045/0069 "store what you matched" case, and the difference is
-- worth stating because the remedy there was the opposite. Those tables key on
-- voucher_id and record the matched amount BECAUSE they are metadata added later
-- by a different act, which an ALTER can leave stale without ever seeing it. An
-- allocation is written by post() itself: an ALTER re-derives it in the same
-- transaction as the postings it splits, so a stale allocation is not reachable
-- and there is nothing to detect. The FK to posting can therefore be an honest
-- cascade.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration.

-- ===========================================================================
-- cost_category -- THE AXES
-- ===========================================================================
CREATE TABLE IF NOT EXISTS cost_category (
    id                UUID PRIMARY KEY,
    company_id        UUID NOT NULL,
    name              TEXT NOT NULL,

    -- Tally's "Allocate Revenue Items" / "Allocate Non-Revenue Items" pair,
    -- collapsed to the question that actually gates a form: may this axis be
    -- used on a balance-sheet ledger, or only on a P&L one? Default FALSE
    -- because departmental analysis of revenue and expense is the ordinary case
    -- and allocating capital items by department is the unusual one -- and a
    -- default that permits MORE than the operator intended is the wrong way for
    -- a default to be wrong.
    allocate_non_revenue BOOLEAN NOT NULL DEFAULT FALSE,

    created_by        TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT cost_category_company_scope UNIQUE (company_id, id),
    -- Case-insensitive within a company: "Department" and "department" are the
    -- same axis to an operator, and two of them is how an allocation set gets
    -- split in half and both halves look complete.
    CONSTRAINT cost_category_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT cost_category_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id)
);
CREATE UNIQUE INDEX IF NOT EXISTS cost_category_name_unique
    ON cost_category (company_id, lower(btrim(name)));

COMMENT ON TABLE cost_category IS
    'An AXIS of cost analysis (Department, Branch, Project). Multiple categories '
    'allocate the SAME posting in full and in parallel, which is why the '
    'sum-to-posting invariant on posting_cost_allocation is grouped by category '
    'and not by posting.';

-- ===========================================================================
-- cost_centre -- THE POSITIONS ON AN AXIS
-- ===========================================================================
CREATE TABLE IF NOT EXISTS cost_centre (
    id                UUID PRIMARY KEY,
    company_id        UUID NOT NULL,
    -- ⛔ NOT NULL. A centre with no category cannot be grouped, and an
    -- allocation naming it could not be summed against anything -- see the
    -- header. Tally's "Primary Cost Category" exists precisely so that this is
    -- never null, and a company that wants one axis gets one row here.
    cost_category_id  UUID NOT NULL,
    name              TEXT NOT NULL,

    -- Tally allows a centre hierarchy (a centre under a centre). Kept as a
    -- self-reference rather than a materialised path so a re-parent is one
    -- UPDATE; reports roll up by recursive walk.
    --
    -- ⚠️ A PARENT MUST SIT ON THE SAME AXIS, enforced by the composite FK below
    -- rather than left to the application: a Mumbai branch parented under a
    -- Sales department would make one allocation set roll up into the other and
    -- the Category Summary would double-count.
    parent_id         UUID NULL,

    created_by        TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT cost_centre_company_scope UNIQUE (company_id, id),
    -- Carries the category so a child's axis can be checked against its parent's
    -- without a second lookup, and so posting_cost_allocation can constrain
    -- (centre, category) as a pair.
    CONSTRAINT cost_centre_axis_scope UNIQUE (company_id, id, cost_category_id),

    CONSTRAINT cost_centre_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT cost_centre_not_its_own_parent CHECK (parent_id IS NULL OR parent_id <> id),

    CONSTRAINT cost_centre_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id),
    CONSTRAINT cost_centre_category_fk
        FOREIGN KEY (company_id, cost_category_id)
        REFERENCES cost_category (company_id, id),
    -- Same company AND same axis as its parent. See the parent_id comment.
    CONSTRAINT cost_centre_parent_shares_axis_fk
        FOREIGN KEY (company_id, parent_id, cost_category_id)
        REFERENCES cost_centre (company_id, id, cost_category_id)
);
-- A centre name is unique WITHIN ITS AXIS, not within the company: "Mumbai" may
-- legitimately be both a Branch and a Project.
CREATE UNIQUE INDEX IF NOT EXISTS cost_centre_name_unique
    ON cost_centre (company_id, cost_category_id, lower(btrim(name)));
CREATE INDEX IF NOT EXISTS cost_centre_by_parent
    ON cost_centre (company_id, parent_id);

COMMENT ON COLUMN cost_centre.cost_category_id IS
    'The axis this centre sits on. NOT NULL: a centre with no category cannot be '
    'grouped, and the per-category sum-to-posting invariant would have nothing '
    'to group by.';

-- ---------------------------------------------------------------------------
-- posting needs a COMPANY-SCOPED unique key before anything can FK to it.
--
-- 0002 gave posting a bare `id` primary key, which predates the composite
-- (company_id, id) pattern every tenant table has used since. A single-column FK
-- to posting(id) would let an allocation in company A name a posting in company
-- B: the RLS policy on posting_cost_allocation checks the ALLOCATION's
-- company_id and has nothing to say about the row it points at, so the write
-- would be accepted and the leak would surface as company A's Cost Centre
-- Summary quietly including company B's cost.
--
-- Additive and index-only -- no row is touched, so posting_immutable (which
-- raises on UPDATE and DELETE) is not involved. Guarded on the catalogue rather
-- than IF NOT EXISTS because ALTER TABLE ... ADD CONSTRAINT has no such form.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'posting_company_scope'
           AND conrelid = 'posting'::regclass
    ) THEN
        ALTER TABLE posting ADD CONSTRAINT posting_company_scope
            UNIQUE (company_id, id);
    END IF;
END
$$;

-- ===========================================================================
-- posting_cost_allocation -- THE SPLIT ITSELF
-- ===========================================================================
CREATE TABLE IF NOT EXISTS posting_cost_allocation (
    id                UUID PRIMARY KEY,
    company_id        UUID NOT NULL,
    posting_id        UUID NOT NULL,

    -- Stored EXPLICITLY even though cost_centre carries it, because the
    -- per-category invariant groups on it and a trigger that had to join to
    -- cost_centre to learn the axis would be both slower and able to disagree
    -- with the row it is checking. Constrained to agree via the composite FK.
    cost_category_id  UUID NOT NULL,
    cost_centre_id    UUID NOT NULL,

    -- A MAGNITUDE, never a sign: the posting's own dr_cr carries the direction,
    -- exactly as stock_movement.qty leaves the sign to `direction`. Mirrors
    -- posting_amount_check's shape.
    amount            NUMERIC(19,4) NOT NULL,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT posting_cost_allocation_company_scope UNIQUE (company_id, id),

    -- One row per (posting, category, centre). A second row for the same centre
    -- on the same axis is the same allocation keyed twice, and it would pass the
    -- sum check by making the FIRST one wrong.
    CONSTRAINT posting_cost_allocation_one_per_centre
        UNIQUE (company_id, posting_id, cost_category_id, cost_centre_id),

    CONSTRAINT posting_cost_allocation_amount_positive CHECK (amount > 0),

    CONSTRAINT posting_cost_allocation_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id),
    -- ON DELETE CASCADE is honest here and would be a defect in 0045/0069 --
    -- see the header. `posting` is additionally protected by posting_immutable,
    -- which raises on DELETE, so in practice this cascade can never fire.
    CONSTRAINT posting_cost_allocation_posting_fk
        FOREIGN KEY (company_id, posting_id)
        REFERENCES posting (company_id, id) ON DELETE CASCADE,
    -- ⛔ THE PAIR, not two independent FKs. This is what stops an allocation
    -- naming a centre that does not sit on the axis it claims -- which would
    -- make one category's allocation set roll up into another's and silently
    -- double-count the Category Summary.
    CONSTRAINT posting_cost_allocation_centre_matches_category_fk
        FOREIGN KEY (company_id, cost_centre_id, cost_category_id)
        REFERENCES cost_centre (company_id, id, cost_category_id)
);
CREATE INDEX IF NOT EXISTS posting_cost_allocation_by_posting
    ON posting_cost_allocation (company_id, posting_id);
-- The Cost Centre Summary walks a centre's allocations across a period; the
-- Category Summary rolls up by axis. Both directions get an index.
CREATE INDEX IF NOT EXISTS posting_cost_allocation_by_centre
    ON posting_cost_allocation (company_id, cost_centre_id);
CREATE INDEX IF NOT EXISTS posting_cost_allocation_by_category
    ON posting_cost_allocation (company_id, cost_category_id);

COMMENT ON TABLE posting_cost_allocation IS
    'How one posting was split across cost centres. The SECOND rendering of a '
    'value that also rides in the hashed voucher payload '
    '(CanonicalPayload.CostAllocations), written from the same in-memory value '
    'in the same transaction by ledger.post() so the signature and the rows '
    'cannot disagree. Keyed on posting_id while the payload keys on a posting '
    'SEQUENCE -- see the migration header for why the two keys differ and why '
    'neither is reconstructed from the other.';

-- ---------------------------------------------------------------------------
-- ⛔⛔ THE SUM-TO-POSTING INVARIANT, PER CATEGORY, AS A DEFERRED CONSTRAINT
-- TRIGGER. Read this before changing it to a CHECK or an AFTER trigger.
-- ---------------------------------------------------------------------------
-- THREE properties are load-bearing and each is a separate decision:
--
--   * DEFERRABLE INITIALLY DEFERRED, exactly as posting_balanced (0002) is. A
--     split into three centres is three INSERTs, and the first two are
--     legitimately short of the posting total. Checked per-statement this
--     feature would be unusable; checked at COMMIT the whole split validates
--     atomically.
--
--   * IT FIRES ON THE ALLOCATION, NOT ON THE POSTING, and therefore says
--     nothing about a posting that has NO allocation rows. Allocation is
--     OPTIONAL per ledger -- most postings in most books will never be
--     allocated, and a voucher with none must remain perfectly legal. This is
--     why the invariant cannot be expressed as a CHECK or as a NOT NULL column.
--
--   * IT GROUPS BY CATEGORY. See the header: parallel allocation means the same
--     posting is allocated in full once per axis, so a posting-wide total is
--     2x the cost for two axes and the check would refuse correct data.
--
-- The message quotes the ledger, the axis and both figures in rupees, because
-- the operator's actual question is "which axis is short, and by how much".
CREATE OR REPLACE FUNCTION check_cost_allocation_sums_to_posting()
RETURNS TRIGGER AS $$
DECLARE
    v_posting_amount NUMERIC(19,4);
    v_allocated      NUMERIC(19,4);
    v_category       TEXT;
    v_ledger         TEXT;
BEGIN
    -- The row may have been deleted by the time a deferred trigger fires (an
    -- allocation replaced within one transaction), in which case there is
    -- nothing left to check for that key.
    SELECT p.amount INTO v_posting_amount
      FROM posting p
     WHERE p.id = NEW.posting_id;
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    SELECT COALESCE(SUM(a.amount), 0) INTO v_allocated
      FROM posting_cost_allocation a
     WHERE a.posting_id = NEW.posting_id
       AND a.cost_category_id = NEW.cost_category_id;

    -- Zero means every allocation on this axis was removed in this transaction.
    -- That is "this posting is not allocated on this axis", which is legal.
    IF v_allocated = 0 THEN
        RETURN NULL;
    END IF;

    -- ⛔ EXACT equality, to the paisa. No tolerance, no rounding window. A
    -- tolerance here is how a real misallocation hides for a year: it would let
    -- every voucher be a few paise short and the Cost Centre Summary would
    -- disagree with the P&L by an amount that grows with volume and matches
    -- nothing.
    IF v_allocated <> v_posting_amount THEN
        SELECT c.name INTO v_category
          FROM cost_category c
         WHERE c.id = NEW.cost_category_id;
        SELECT l.name INTO v_ledger
          FROM posting p JOIN ledger l ON l.id = p.ledger_id
         WHERE p.id = NEW.posting_id;
        RAISE EXCEPTION
            'cost allocation for % on the % axis totals % but the posting is %: % is unattributed, so the Cost Centre Summary would disagree with the Profit & Loss by that amount',
            COALESCE(v_ledger, '(unknown ledger)'),
            COALESCE(v_category, '(unknown category)'),
            v_allocated, v_posting_amount,
            (v_posting_amount - v_allocated);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS cost_allocation_sums_to_posting ON posting_cost_allocation;
CREATE CONSTRAINT TRIGGER cost_allocation_sums_to_posting
    AFTER INSERT OR UPDATE ON posting_cost_allocation
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_cost_allocation_sums_to_posting();

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, policy with BOTH USING
-- and WITH CHECK. USING alone would let a session write an allocation for
-- another company it can never see or correct; WITH CHECK alone would leak every
-- tenant's departmental profitability on read. FORCE because migrations run as
-- the table owner and an owner bypasses its own policies unless forced.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['cost_category', 'cost_centre', 'posting_cost_allocation'] LOOP
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

GRANT SELECT, INSERT, UPDATE, DELETE ON cost_category           TO tally_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON cost_centre             TO tally_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON posting_cost_allocation TO tally_app;
