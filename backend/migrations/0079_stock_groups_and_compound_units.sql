-- 0079_stock_groups_and_compound_units.sql
-- FEATURE T10: stock items stop being flat, and a unit stops being a bare symbol.
--
-- TWO CHANGES THAT LOOK UNRELATED AND ARE NOT
-- -------------------------------------------
-- Both are about the same thing: a QUANTITY IN THIS PRODUCT HAS BEEN A NUMBER
-- WITH A LABEL ON IT. 100 of an item, in a unit called "BOX", with no way to
-- say what a BOX holds and no way to ask what a whole category of items is
-- worth. A stock group makes items ADD UP; a compound unit makes a quantity
-- CONVERTIBLE. Neither invents a figure: both are structure the reports read.
--
-- ===========================================================================
-- 1. stock_group -- the item hierarchy (Tally: Stock Groups)
-- ===========================================================================
-- Modelled on account_group deliberately, down to the LTREE path, because it is
-- the same problem with the same rollup: a group's total is its own items plus
-- every descendant's, and `path <@ ancestor_path` answers that in one indexed
-- predicate rather than in a recursive CTE per row. Copying the established
-- shape also means the reporting side already knows how to read it.
--
-- WHY THERE IS NO `nature` COLUMN, unlike account_group. A stock group carries
-- no accounting meaning at all: every item in every group lands in the same
-- Stock-in-Hand asset, so there is nothing for a nature to decide. account_group
-- needs one because a primary group's nature is what puts a ledger on the
-- Balance Sheet rather than the P&L; a stock group only decides how the Stock
-- Summary is SUBTOTALLED.
--
-- WHY stock_item.stock_group_id IS NULLABLE, and why that is not laziness:
-- every stock item that exists today has no group, and there is no group it
-- could be given without inventing one. An UNGROUPED item is a real state (it is
-- what Tally calls being directly under "Primary"), so the reports must render
-- it as a stated bucket rather than hide it or pretend it belongs somewhere.
-- ⛔ A NOT NULL with a seeded "Primary" group would have been the tidier schema
-- and the wrong one: it would put every existing item in a group the operator
-- never created, and a group that cannot be empty cannot be deleted either.
CREATE TABLE stock_group (
    id         UUID PRIMARY KEY,
    company_id UUID NOT NULL,
    name       TEXT NOT NULL,
    parent_id  UUID NULL REFERENCES stock_group (id),
    -- The materialised ancestry, exactly as account_group.path: "g<id>" for a
    -- top-level group, "<parent path>.s<id>" for a child. It is what makes a
    -- rollup over a whole branch one predicate.
    path       LTREE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The composite tenant key every FK in the inventory plane points at (see
    -- 0025's header: RLS hides another tenant's rows from a SELECT but a plain
    -- REFERENCES would still accept an id that leaked into a request body).
    CONSTRAINT stock_group_company_scope UNIQUE (company_id, id)
);
CREATE INDEX stock_group_company   ON stock_group (company_id);
CREATE INDEX stock_group_path_gist ON stock_group USING GIST (path);

-- The name is unique per company and NORMALISED, which is migration 0075's rule
-- and its reasoning applies here unchanged: "Raw Materials" and "raw materials"
-- are one group to a human, and two rows means one half of the stock summary
-- subtotals under each.
CREATE UNIQUE INDEX stock_group_company_name_normalised
    ON stock_group (company_id, lower(btrim(name)));

COMMENT ON TABLE stock_group IS
    'Stock item hierarchy (Tally: Stock Groups). Carries no accounting nature: '
    'every item lands in the same Stock-in-Hand asset whatever its group, so a '
    'group decides only how the Stock Summary is subtotalled.';

ALTER TABLE stock_item
    ADD COLUMN stock_group_id UUID NULL,
    ADD CONSTRAINT stock_item_stock_group_fk
        FOREIGN KEY (company_id, stock_group_id)
        REFERENCES stock_group (company_id, id);
CREATE INDEX stock_item_stock_group ON stock_item (company_id, stock_group_id);

COMMENT ON COLUMN stock_item.stock_group_id IS
    'The item''s stock group, or NULL for an ungrouped item -- which is a real '
    'state (Tally''s "under Primary"), not a missing value, and the reports '
    'render it as a stated bucket.';

-- ===========================================================================
-- 2. Compound units -- "BOX of 12 NOS"
-- ===========================================================================
-- A compound unit is a unit DEFINED IN TERMS OF ANOTHER: 1 BOX = 12 NOS. Tally
-- ships this as a first-class master and exports it as ISSIMPLEUNIT=No with
-- BASEUNITS / ADDITIONALUNITS / CONVERSION.
--
-- ⛔⛔ THE ONE DECISION THAT MATTERS HERE, AND IT IS NOT A SCHEMA DECISION:
-- THE STORED QUANTITY IS ALWAYS IN THE ITEM'S OWN UNIT, AND NOTHING CONVERTS IT
-- ON THE WAY IN. A movement of 5 BOX stores 5.0000, not 60.0000. The conversion
-- is applied at REPORT time, exactly as valuation is, and for the same reason:
-- an inbound conversion is irreversible and unauditable. If a movement stored 60
-- NOS, then
--
--   * the invoice that said "5 BOX" and the stock ledger would no longer agree
--     on the number keyed;
--   * correcting the conversion factor later (a supplier repacks 12 to a box as
--     10) could not restate the history -- the boxes are gone, and 60 NOS is a
--     fact about a box size that is no longer true;
--   * and every quantity in the database would be in a different unit from the
--     one on the document it came from, which is exactly the ambiguity units
--     exist to remove.
--
-- So the factor is stored, the base is stored, and a report that wants pieces
-- multiplies. A quantity that is never converted is never wrongly converted.
--
-- WHY base_unit_id IS SELF-REFERENTIAL AND CHAINS ARE REFUSED IN CODE: Tally
-- allows a compound unit whose base is itself compound (a PALLET of 20 BOX of
-- 12 NOS). Nothing in the schema forbids it, and masters refuses it for now
-- rather than shipping a conversion that only resolves one level -- which is the
-- shape of bug that reads as a rounding error and is a factor of twelve.
ALTER TABLE unit
    ADD COLUMN base_unit_id UUID NULL,
    ADD COLUMN conversion_factor NUMERIC(19,4) NULL,
    ADD CONSTRAINT unit_base_unit_fk
        FOREIGN KEY (base_unit_id) REFERENCES unit (id),
    -- The two travel together or not at all. A base with no factor cannot be
    -- converted and a factor with no base converts to nothing; either half alone
    -- is a unit that LOOKS compound to a reader and cannot be restated.
    ADD CONSTRAINT unit_compound_is_whole
        CHECK ((base_unit_id IS NULL) = (conversion_factor IS NULL)),
    -- A factor of zero would make every quantity in the unit worth nothing in
    -- its base; a negative one would flip the sign of a quantity, which is the
    -- one thing a quantity in this system never carries.
    ADD CONSTRAINT unit_conversion_positive
        CHECK (conversion_factor IS NULL OR conversion_factor > 0),
    -- A unit cannot be its own base: 1 BOX = 12 BOX is not a conversion, it is a
    -- loop, and any report that followed it would not terminate.
    ADD CONSTRAINT unit_base_is_not_self
        CHECK (base_unit_id IS NULL OR base_unit_id <> id);

CREATE INDEX unit_base_unit ON unit (base_unit_id);

COMMENT ON COLUMN unit.base_unit_id IS
    'For a COMPOUND unit, the unit it is defined in terms of (1 <this> = '
    'conversion_factor <base>). NULL for a simple unit.';
COMMENT ON COLUMN unit.conversion_factor IS
    'How many base units one of this unit holds. ⛔ Quantities are ALWAYS stored '
    'in the item''s own unit and converted at REPORT time -- converting on the '
    'way in would lose the number that was keyed and could never be restated if '
    'the factor changed.';

-- ---------------------------------------------------------------------------
-- Row-level security for stock_group: default-deny, ENABLE + FORCE, USING +
-- WITH CHECK pinned to the total app_current_company_id() resolver — identical
-- to every other tenant table since 0004.
-- ---------------------------------------------------------------------------
-- ⛔ THE POLICY IS NAMED `tenant_isolation`, AND THE NAME IS LOAD-BEARING RATHER
-- THAN A CONVENTION: internal/security's TestEveryTenantTableHasIsolationPolicy
-- enumerates every table with a company_id and asserts a policy OF THAT NAME
-- exists on it. It caught this table with a policy called stock_group_tenant —
-- correctly written, correctly scoped, and invisible to the census. A security
-- gate that can be passed by a differently-named policy is a gate that stops
-- working the first time somebody is creative.
ALTER TABLE stock_group ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_group FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_group
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
GRANT SELECT, INSERT, UPDATE, DELETE ON stock_group TO tally_app;
