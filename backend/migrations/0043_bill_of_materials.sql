-- 0043_bill_of_materials.sql
-- WHAT A FINISHED GOOD IS MADE OF -- the recipe, as a master.
--
-- Migration number from PLAN-TALLY-PARITY.md §2.5, which reserves 0043 for T3's
-- BOM schema. ⛔ See 0042's header for why §2.5's 0044 (T3's ACL) CANNOT be used
-- and the order-book counterweight is 0073 instead.
--
-- ===========================================================================
-- A BOM IS A MASTER. IT MOVES NOTHING, COMMITS NOTHING, AND BALANCES NOTHING.
-- ===========================================================================
-- It rides in T3 with orders because it shares their shape -- a quantity that is
-- not a movement -- but it is one step further from the ledger than an order is.
-- An order is at least a fact about the world (a customer has promised to buy);
-- a BOM is a STATEMENT OF INTENT about arithmetic: "one Table takes 4 Legs, 1
-- Top and 200g of screws".
--
-- Nothing in these two tables is read by closing stock, the trial balance, the
-- P&L or the balance sheet, and nothing in them reads those either. A BOM
-- becomes real only when it is EXPLODED into the inventory lines of a Stock
-- Journal (class 'Stock Journal', migration 0025 / ledger/nonaccounting.go),
-- which already exists and already consumes components and produces a finished
-- good as ordinary movements. This migration adds no voucher path and touches
-- ledger.post() not at all.
--
-- ===========================================================================
-- ⛔⛔ THE RECIPE IS STORED AS WRITTEN, AND NEVER PRE-DIVIDED PER UNIT
-- ===========================================================================
-- This is the one arithmetic decision in the file and the naive shape gets it
-- wrong irrecoverably.
--
-- Recipes are stated as a YIELD: "10 kg of resin makes 3 mouldings". The
-- tempting normalisation is to store a per-unit figure and drop output_qty --
-- 10/3 = 3.3333 kg per moulding. NUMERIC(19,4) truncates that, and exploding it
-- back for a batch of 3 gives 9.9999 kg. The consumption is short by a tenth of
-- a gram, every batch, forever; it accumulates in closing stock as phantom
-- material that reconciles against nothing, and it is exactly the tolerance
-- HANDOFF §7.5 names as how a real error hides for a year.
--
-- So the recipe keeps BOTH numbers exactly as the operator wrote them, and the
-- explosion scales by the RATIO (wanted / output_qty) once, at the end. A batch
-- of 3 consumes exactly 10 kg because that is the number that was stored.
--
-- ===========================================================================
-- SINGLE-LEVEL EXPLOSION ONLY IN THIS SLICE, AND THAT IS WHY THERE IS NO CYCLE
-- CHECK
-- ===========================================================================
-- A component that ITSELF has a BOM is not exploded recursively here: a Table
-- explodes to 4 Legs, and if a Leg has its own BOM the operator issues a second
-- Stock Journal for the legs. That is Tally's own default behaviour and it is
-- what makes work-in-progress visible as stock rather than vanishing inside one
-- entry.
--
-- ⚠️ CONSEQUENTLY THE ONLY CYCLE REFUSED HERE IS THE DIRECT ONE (an item as its
-- own component), by CHECK. A cross-level cycle -- A needs B, B needs A -- is
-- storable and is HARMLESS while nothing recurses, because no reader ever
-- follows the second edge. It stops being harmless the moment a multi-level
-- explosion is written, and at that point a recursive guard belongs on the
-- WRITE path (a WITH RECURSIVE probe before insert), not here: a constraint
-- trigger would have to walk the graph on every component insert, and the
-- direct-self case is the only one it can settle cheaply. Stated so the next
-- author knows the guard is absent BY DECISION and what condition changes it.

-- ===========================================================================
-- bom_head -- THE RECIPE
-- ===========================================================================
CREATE TABLE IF NOT EXISTS bom_head (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,

    -- The FINISHED GOOD this recipe produces.
    stock_item_id UUID NOT NULL,

    -- An item may have SEVERAL recipes -- a standard build and a cheaper
    -- substitute build, or a summer and winter formulation -- so the BOM is
    -- named and the name is what an operator picks at explosion time.
    name          TEXT NOT NULL,

    -- ⛔ HOW MANY OF THE FINISHED GOOD THIS RECIPE YIELDS. See the header: the
    -- recipe is stored as written and the explosion scales by
    -- (wanted / output_qty). Never divide the components by this to store a
    -- per-unit figure.
    output_qty    NUMERIC(19,4) NOT NULL CHECK (output_qty > 0),

    -- The one offered first when an operator explodes this item. At most one per
    -- item, enforced by a partial unique index below: two defaults is a silent
    -- coin flip over which recipe a manufacture used, and the two recipes
    -- consume different materials.
    is_default    BOOLEAN NOT NULL DEFAULT false,

    created_by    TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT bom_head_company_scope UNIQUE (company_id, id),
    CONSTRAINT bom_head_name_not_blank CHECK (btrim(name) <> ''),

    CONSTRAINT bom_head_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id),
    CONSTRAINT bom_head_item_fk
        FOREIGN KEY (company_id, stock_item_id)
        REFERENCES stock_item (company_id, id)
);
-- A recipe name is unique WITHIN ITS ITEM, not within the company: "Standard"
-- is the obvious name for the standard build of every item there is.
-- Case-insensitive for the reason cost_category_name_unique records.
CREATE UNIQUE INDEX IF NOT EXISTS bom_head_name_unique
    ON bom_head (company_id, stock_item_id, lower(btrim(name)));
-- At most ONE default per item. A partial index rather than a CHECK, because the
-- constraint is across rows.
CREATE UNIQUE INDEX IF NOT EXISTS bom_head_one_default_per_item
    ON bom_head (company_id, stock_item_id) WHERE is_default;
CREATE INDEX IF NOT EXISTS bom_head_by_item
    ON bom_head (company_id, stock_item_id);

COMMENT ON TABLE bom_head IS
    'A recipe: what one finished good is made of. A MASTER -- it moves nothing '
    'and balances nothing, and becomes real only when exploded into the '
    'inventory lines of a Stock Journal. output_qty is the YIELD and the '
    'components are stored against it, never pre-divided per unit.';

COMMENT ON COLUMN bom_head.output_qty IS
    'How many of the finished good this recipe yields. The explosion scales '
    'components by (wanted / output_qty); storing per-unit component figures '
    'instead would truncate at NUMERIC(19,4) and short every batch forever.';

-- ===========================================================================
-- bom_component -- WHAT GOES IN
-- ===========================================================================
CREATE TABLE IF NOT EXISTS bom_component (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    bom_id        UUID NOT NULL,

    -- 1-based position, preserving the order the operator wrote the recipe in,
    -- exactly as stock_movement.line_no and order_line.line_no do.
    line_no       INTEGER NOT NULL CHECK (line_no >= 1),

    stock_item_id UUID NOT NULL,

    -- The quantity consumed to yield bom_head.output_qty of the finished good --
    -- NOT per unit of it. In the component item's own unit of measure.
    qty           NUMERIC(19,4) NOT NULL CHECK (qty > 0),

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT bom_component_company_scope UNIQUE (company_id, id),
    CONSTRAINT bom_component_position UNIQUE (company_id, bom_id, line_no),
    -- The same item twice in one recipe is the same requirement keyed twice, and
    -- it could only make a total come out right by making the other line wrong --
    -- the argument posting_cost_allocation_one_per_centre records. An operator
    -- who wants 4 legs writes 4, not two lines of 2.
    CONSTRAINT bom_component_one_per_item UNIQUE (company_id, bom_id, stock_item_id),

    CONSTRAINT bom_component_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id),
    CONSTRAINT bom_component_bom_fk
        FOREIGN KEY (company_id, bom_id)
        REFERENCES bom_head (company_id, id) ON DELETE CASCADE,
    CONSTRAINT bom_component_item_fk
        FOREIGN KEY (company_id, stock_item_id)
        REFERENCES stock_item (company_id, id)
);
CREATE INDEX IF NOT EXISTS bom_component_by_bom
    ON bom_component (company_id, bom_id);
-- "What is this item a component of" -- the reverse read, for the question "if I
-- run out of screws, what can I not build".
CREATE INDEX IF NOT EXISTS bom_component_by_item
    ON bom_component (company_id, stock_item_id);

COMMENT ON TABLE bom_component IS
    'One material consumed by a recipe, in the quantity that yields '
    'bom_head.output_qty of the finished good -- not per unit of it.';

-- ---------------------------------------------------------------------------
-- ⛔ AN ITEM MAY NOT BE ITS OWN COMPONENT.
--
-- A trigger and not a CHECK because the finished good lives on the PARENT row:
-- a CHECK can only see the component row it is defined on, and bom_component
-- has no column naming the item its bom_head produces. Duplicating
-- stock_item_id onto the component to make a CHECK possible would be a second
-- copy of the parent's item that can disagree with it.
--
-- Immediate, NOT deferred, unlike the order triggers in 0042: this is a
-- property of ONE row against ONE parent row, with nothing to accumulate and no
-- intermediate state that is legitimately in violation. Deferring it would only
-- move the same refusal to commit time and make the message arrive further from
-- the statement that caused it.
--
-- ⚠️ THE CROSS-LEVEL CYCLE IS NOT REFUSED. See the header: it is harmless while
-- explosion is single-level and needs a recursive guard on the write path the
-- moment that changes.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION check_bom_component_not_self()
RETURNS TRIGGER AS $$
DECLARE
    v_output_item UUID;
    v_item        TEXT;
BEGIN
    SELECT bh.stock_item_id INTO v_output_item
      FROM bom_head bh
     WHERE bh.company_id = NEW.company_id AND bh.id = NEW.bom_id;

    IF v_output_item = NEW.stock_item_id THEN
        SELECT name INTO v_item FROM stock_item
         WHERE company_id = NEW.company_id AND id = NEW.stock_item_id;
        RAISE EXCEPTION
            '% cannot be a component of its own bill of materials: exploding it would consume the very thing it produces, and the stock journal would both issue and receive the same item with no material actually converted',
            COALESCE(v_item, NEW.stock_item_id::text);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS bom_component_not_self ON bom_component;
CREATE TRIGGER bom_component_not_self
    BEFORE INSERT OR UPDATE ON bom_component
    FOR EACH ROW EXECUTE FUNCTION check_bom_component_not_self();

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, USING + WITH CHECK --
-- identical to every tenant table since 0004. A recipe is manufacturing IP: it
-- states exactly what a competitor would need to reproduce the product and at
-- what material cost, so a read leak here is a different and worse thing than a
-- leaked master list of item names.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['bom_head', 'bom_component'] LOOP
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

GRANT SELECT, INSERT, UPDATE, DELETE ON bom_head      TO tally_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON bom_component TO tally_app;
