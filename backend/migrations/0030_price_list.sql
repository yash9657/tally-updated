-- 0030_price_list.sql
-- PRICE LISTS: a named set of rates for stock items, in force over a date
-- range, optionally narrowed to a party group, optionally broken into quantity
-- slabs with a discount.
--
-- THE ONE PROPERTY THIS SCHEMA EXISTS TO GUARANTEE
-- -----------------------------------------------
-- For any (stock item, party group, date, quantity) there is AT MOST ONE
-- applicable rate, and which one it is does not depend on insertion order, on
-- row ids, or on anything else the user cannot see. A price list that can
-- answer "what do we charge for this?" two ways is not a feature with a
-- tie-break rule waiting to be written -- it is an invoice whose total nobody
-- can reproduce six months later when the customer disputes it.
--
-- WHY OVERLAPS ARE REFUSED RATHER THAN RESOLVED BY PRECEDENCE
-- ----------------------------------------------------------
-- Two lists claiming the same customers on the same day is, essentially always,
-- a data-entry mistake: somebody entered the new season's list without closing
-- the old one. Every precedence rule available to break that tie -- newest
-- created wins, narrowest range wins, highest/lowest rate wins -- silently
-- prices some orders one way and some the other, and the user is never told
-- that half their invoices went out at last year's rate. So
-- price_list_no_overlap_in_scope REFUSES the second list outright, at the
-- moment it is entered, when the user still knows what they meant. Closing the
-- old list (giving it an upper bound) is the fix, and it is one edit.
--
-- Precedence IS used for exactly one thing, and only because it can never tie:
-- a list narrowed to a party group beats the company-wide list. That is not an
-- ambiguity -- it is the entire purpose of a party-group list -- and there are
-- only ever two levels, so there is no ordering to get wrong. The resolver in
-- internal/masters/price_list.go implements that, and refuses to answer if it
-- ever sees two candidates at the SAME level (which the exclusion constraints
-- below make unreachable; the check is there so that dropping a constraint
-- produces a loud failure instead of an arbitrary rate).
--
-- WHY RANGES, NOT PAIRS OF COLUMNS
-- --------------------------------
-- effective is a DATERANGE and qty_range is a NUMRANGE, rather than
-- from/to column pairs, because "do these two intervals overlap" is then a
-- single indexable operator (&&) that an EXCLUDE constraint can enforce. Written
-- as column pairs the same rule becomes a hand-written trigger that has to get
-- NULL-as-unbounded and inclusive-vs-exclusive endpoints right by hand -- which
-- is precisely the off-by-one this table is trying to make impossible.
--
-- HALF-OPEN, [from, to), EVERYWHERE
-- ---------------------------------
-- A quantity or a date exactly on a boundary must land in EXACTLY ONE slab. So
-- the lower bound is inclusive and the upper bound is exclusive: slabs
-- [0,100) and [100,) put a quantity of exactly 100 in the second one and only
-- the second one, and lists [Apr 1, Jul 1) and [Jul 1, Oct 1) put Jul 1 in the
-- second one and only the second one. Both are checked:
--   * daterange is a DISCRETE range, so Postgres canonicalises every daterange
--     to '[)' itself -- '[]' bounds are rewritten as an exclusive upper one day
--     later. The half-open property is therefore free for dates (and passing
--     '[]' silently EXTENDS the range by a day rather than making it inclusive,
--     which the overlap constraint then catches).
--   * numrange is CONTINUOUS and is NOT canonicalised, so an inclusive upper
--     bound would survive and a quantity of exactly 100 would match both
--     [0,100] and [100,). price_list_entry_qty_half_open refuses that shape at
--     the door, which is the only place the mistake is still visible.
--
-- RE-RUNNABLE. cored migrate does not guarantee a migration is applied
-- atomically, so a run that dies halfway can leave partial writes behind
-- WITHOUT stamping schema_migrations. Everything below is therefore written to
-- be a no-op on a second run: IF NOT EXISTS on every object, pg_constraint /
-- pg_policies guards on everything that has no IF NOT EXISTS form. There is no
-- backfill in this migration -- nothing existed before it to backfill.

-- ---------------------------------------------------------------------------
-- btree_gist gives GiST index support for the plain-equality (=) operator on
-- scalar types, which is what lets an EXCLUDE constraint say "these two rows
-- conflict only if they are the same company AND the same party group AND their
-- date ranges overlap". Without it, an exclusion constraint can only speak
-- about the range column, and one tenant's price list would collide with
-- another tenant's.
-- ---------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- ---------------------------------------------------------------------------
-- The composite tenant key on account_group, so price_list.party_group_id can
-- reference the PAIR. Same argument 0025 made for stock_item: RLS hides another
-- tenant's rows from a SELECT, but a plain REFERENCES account_group (id) would
-- accept a group id belonging to another company if one ever leaked in through
-- a request body, and the price list would then be scoped to a party group
-- neither tenant can see. The composite FK makes the DATABASE refuse it, with
-- no dependence on the session GUC being right.
--
-- Additive: the primary key and the existing (company_id, name) unique are
-- untouched.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'account_group_company_scope'
    ) THEN
        ALTER TABLE account_group
            ADD CONSTRAINT account_group_company_scope UNIQUE (company_id, id);
    END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- price_list: the HEADER. A name, the window it is in force over, and the party
-- group it is narrowed to (NULL = every party).
--
-- effective_to is EXCLUSIVE and may be unbounded (an open-ended list, which is
-- the ordinary case: "these are our rates until we say otherwise").
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS price_list (
    id             UUID PRIMARY KEY,
    company_id     UUID NOT NULL,
    name           TEXT NOT NULL,
    -- NULL means the list applies to every party. See the precedence note in
    -- the header: a group-scoped list beats the NULL-scoped one, and that is
    -- the only precedence anywhere in this design.
    party_group_id UUID NULL,
    effective      DATERANGE NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, name),
    -- Not redundant with the primary key: price_list_entry references the PAIR
    -- so a cross-tenant price_list_id is refused by the FK itself.
    CONSTRAINT price_list_company_scope UNIQUE (company_id, id),
    CONSTRAINT price_list_party_group_fk
        FOREIGN KEY (company_id, party_group_id)
        REFERENCES account_group (company_id, id),
    -- A list with no start date is a list that has always been in force, which
    -- is never what anybody means and makes back-dated vouchers pick up rates
    -- from a season that had not happened yet.
    CONSTRAINT price_list_effective_has_start
        CHECK (lower(effective) IS NOT NULL),
    -- An empty range matches no date at all: the list would exist, be visible
    -- in the master list, and never once apply. Refuse it where the typo is.
    CONSTRAINT price_list_effective_nonempty
        CHECK (NOT isempty(effective))
);

CREATE INDEX IF NOT EXISTS price_list_company ON price_list (company_id);

-- The determinism constraint for DATES. Within one company and one party-group
-- scope, no two lists may be in force on the same day.
--
-- COALESCE around party_group_id is essential and not cosmetic: `NULL = NULL`
-- is UNKNOWN, so a bare `party_group_id WITH =` would consider two
-- company-wide lists to be in DIFFERENT scopes and happily accept both --
-- leaving the single most common case (two overlapping company-wide lists) as
-- the one case the constraint does not cover. The all-zeroes UUID is used as
-- the sentinel for "no party group"; it is not a legal uuid_generate_v7 output
-- and cannot collide with a real group id.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'price_list_no_overlap_in_scope'
    ) THEN
        ALTER TABLE price_list ADD CONSTRAINT price_list_no_overlap_in_scope
            EXCLUDE USING gist (
                company_id WITH =,
                (COALESCE(party_group_id, '00000000-0000-0000-0000-000000000000'::uuid)) WITH =,
                effective WITH &&
            );
    END IF;
END
$$;

COMMENT ON TABLE price_list IS
    'A named set of rates in force over a half-open date range, optionally '
    'narrowed to a party group. Two lists may never be in force on the same '
    'day in the same scope: the exclusion constraint refuses the second.';

-- ---------------------------------------------------------------------------
-- price_list_entry: one item's rate within one list, for one quantity slab.
--
-- The rate and the discount are stored SEPARATELY rather than as a single net
-- rate, because the invoice has to print both -- "1,000.00 less 5%" is a
-- different document from "950.00", and a customer who agreed a 5% trade
-- discount will ask where it went. The net rate is derived by the resolver.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS price_list_entry (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    price_list_id UUID NOT NULL,
    stock_item_id UUID NOT NULL,
    -- [from, to). NULL upper = "and everything above", the top slab.
    qty_range     NUMRANGE NOT NULL,
    rate          NUMERIC(19,4) NOT NULL CHECK (rate >= 0),
    -- Same shape and bounds as stock_movement.discount_pct, so a rate that
    -- flows from here onto a movement line needs no conversion.
    discount_pct  NUMERIC(9,4) NOT NULL DEFAULT 0
                  CHECK (discount_pct >= 0 AND discount_pct <= 100),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT price_list_entry_company_scope UNIQUE (company_id, id),
    CONSTRAINT price_list_entry_list_fk
        FOREIGN KEY (company_id, price_list_id)
        REFERENCES price_list (company_id, id) ON DELETE CASCADE,
    CONSTRAINT price_list_entry_item_fk
        FOREIGN KEY (company_id, stock_item_id)
        REFERENCES stock_item (company_id, id),
    -- THE HALF-OPEN RULE, enforced. numrange is continuous and so is NOT
    -- canonicalised by Postgres: an inclusive upper bound survives exactly as
    -- written, and slabs [0,100] and [100,) would BOTH contain a quantity of
    -- 100. That prices an order at whichever slab the planner happened to
    -- return first -- a wrong invoice with no error anywhere. Refuse the shape.
    CONSTRAINT price_list_entry_qty_half_open
        CHECK (lower_inc(qty_range) AND NOT upper_inc(qty_range)),
    CONSTRAINT price_list_entry_qty_bounded_below
        CHECK (lower(qty_range) IS NOT NULL AND lower(qty_range) >= 0),
    -- [100,100) contains nothing: a slab that can never be hit, sitting in the
    -- master looking like a rate somebody will be charged.
    CONSTRAINT price_list_entry_qty_nonempty
        CHECK (NOT isempty(qty_range))
);

CREATE INDEX IF NOT EXISTS price_list_entry_company
    ON price_list_entry (company_id);
-- "which lists mention this item" -- the direction the exclusion constraint's
-- index (list first) cannot serve.
CREATE INDEX IF NOT EXISTS price_list_entry_item
    ON price_list_entry (company_id, stock_item_id);

-- The determinism constraint for QUANTITY. Within one list, one item's slabs
-- may not overlap. Combined with price_list_no_overlap_in_scope this is what
-- makes the lookup total: at most one list is in force on a date, and within
-- it at most one slab contains the quantity.
--
-- The GiST index this creates is also the resolver's read path:
-- (company_id, price_list_id, stock_item_id, qty_range @> qty).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'price_list_entry_slabs_disjoint'
    ) THEN
        ALTER TABLE price_list_entry ADD CONSTRAINT price_list_entry_slabs_disjoint
            EXCLUDE USING gist (
                company_id    WITH =,
                price_list_id WITH =,
                stock_item_id WITH =,
                qty_range     WITH &&
            );
    END IF;
END
$$;

COMMENT ON TABLE price_list_entry IS
    'One item''s rate and discount within one price list, for one half-open '
    '[from, to) quantity slab. Slabs for an item within a list may not overlap.';

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE + FORCE, USING + WITH CHECK pinned
-- to the total app_current_company_id() resolver -- identical to every other
-- tenant table since 0004. The policy is dropped-if-present first so a re-run
-- of a half-applied migration does not fail on a duplicate policy name.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['price_list', 'price_list_entry'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I '
            || 'USING (company_id = app_current_company_id()) '
            || 'WITH CHECK (company_id = app_current_company_id())',
            t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON price_list, price_list_entry TO tally_app;
