-- 0025_inventory_schema.sql
-- THE INVENTORY PLANE'S SCHEMA FOUNDATION: locations, batches, the movement
-- ledger, and opening positions.
--
-- WHY THERE IS NO STOCK-VALUE COLUMN ANYWHERE IN HERE
-- ---------------------------------------------------
-- Stock VALUE is never stored. Not on stock_item, not on stock_movement, not in
-- a balance table. It is derived at REPORT time by replaying the movement
-- ledger through a pure valuer, because valuation is ORDER-DEPENDENT: a
-- back-dated inward must re-value every later outward, and an ALTER of an April
-- invoice must change the JULY closing value. A materialised value cannot
-- express that -- it can only be stale or wrong, and "stale or wrong" in a
-- valuation is a misstated balance sheet.
--
-- This is the same choice the accounting side already made: the reports derive
-- from postings joined to voucher_head.current_version_id, NOT from
-- ledger_balance. Quantity is a different animal -- it is order-INdependent, a
-- plain sum -- which is why the negative-stock trigger below can compute a
-- closing QUANTITY cheaply and still be correct, while a value trigger could
-- not be.
--
-- WHY A MOVEMENT IS A SIBLING OF voucher_gst_line, NOT A DERIVATIVE OF posting
-- ---------------------------------------------------------------------------
-- A stock movement is an immutable child of voucher_version written ONLY inside
-- ledger.post()'s transaction, at the same seam the GST-line loop uses. It is
-- NOT derived from postings and postings are NOT derived from it, because the
-- two grains genuinely differ: a 3-item invoice billed to one Sales ledger is
-- ONE posting and THREE movements, and a Delivery Note is THREE movements and
-- ZERO postings. Deriving either from the other would force one of those two
-- perfectly ordinary documents to be recorded wrongly.
--
-- WHY EVERY FK IN HERE IS COMPOSITE ON (company_id, id)
-- ----------------------------------------------------
-- RLS hides another tenant's rows from a SELECT, but a plain
-- REFERENCES godown (id) would still happily accept a godown id belonging to
-- another company if one ever leaked into a request body -- and the row would
-- then be invisible to BOTH tenants' reports while quietly holding stock. The
-- composite FK makes that structurally impossible: the DATABASE refuses the
-- cross-tenant reference, with no dependence on the session GUC being right.
--
-- godown_id and batch_id are NULLABLE dimensions and are NEVER part of any
-- unique constraint on a movement: a legal invoice may ship the same
-- item/godown/batch twice at two different rates (a price break on the tail of
-- a quantity). The ONLY uniqueness is (voucher_version_id, line_no), verbatim
-- voucher_gst_line's shape.

-- ---------------------------------------------------------------------------
-- Direction of a movement. An enum, exactly like dr_cr: there are two values,
-- there will only ever be two values, and the sign of a quantity belongs in a
-- named column rather than in a negative number that a careless SUM can flip.
-- qty is therefore always POSITIVE and direction carries the sign.
-- ---------------------------------------------------------------------------
CREATE TYPE stock_direction AS ENUM ('IN', 'OUT');

-- ---------------------------------------------------------------------------
-- Composite tenant keys on the two EXISTING tables the inventory plane points
-- at. Additive: the primary keys and existing uniques are untouched. These
-- exist so the composite FKs below have something to reference.
-- ---------------------------------------------------------------------------
ALTER TABLE stock_item
    ADD CONSTRAINT stock_item_company_scope UNIQUE (company_id, id);
ALTER TABLE voucher_version
    ADD CONSTRAINT voucher_version_company_scope UNIQUE (company_id, id);

-- ---------------------------------------------------------------------------
-- godown: a physical (or notional) stock location. Tally calls these Godowns /
-- Locations; every company gets exactly one system 'Main Location' so that a
-- user who never enables multi-location still has a real, named place for
-- stock to sit, rather than a NULL that every report has to special-case.
-- ---------------------------------------------------------------------------
CREATE TABLE godown (
    id          UUID PRIMARY KEY,
    company_id  UUID NOT NULL,
    name        TEXT NOT NULL,
    is_system   BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, name),
    -- Not redundant with the primary key: children reference the PAIR so a
    -- cross-tenant godown_id is refused by the FK itself.
    CONSTRAINT godown_company_scope UNIQUE (company_id, id)
);
CREATE INDEX godown_company ON godown (company_id);

COMMENT ON TABLE godown IS
    'Stock location. Every company carries a system "Main Location" so stock '
    'always has a named place to sit even when multi-location is never enabled.';

-- ---------------------------------------------------------------------------
-- stock_batch: a MASTER row, never a free-text column on a movement.
--
-- Batch names are scoped to their ITEM, not to the company: two different items
-- may each have a batch called 'B-01' and they are unrelated. Making the name
-- company-unique instead would collide the day a second supplier uses the same
-- numbering, and the collision would silently merge two physical batches with
-- different expiry dates -- which for a pharma or food tenant is the whole
-- reason batches exist.
-- ---------------------------------------------------------------------------
CREATE TABLE stock_batch (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    stock_item_id UUID NOT NULL,
    name          TEXT NOT NULL,
    mfg_date      DATE NULL,
    expiry_date   DATE NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, stock_item_id, name),
    CONSTRAINT stock_batch_company_scope UNIQUE (company_id, id),
    CONSTRAINT stock_batch_item_fk
        FOREIGN KEY (company_id, stock_item_id)
        REFERENCES stock_item (company_id, id)
);
CREATE INDEX stock_batch_company ON stock_batch (company_id);
CREATE INDEX stock_batch_item    ON stock_batch (company_id, stock_item_id);

COMMENT ON TABLE stock_batch IS
    'Batch/lot master, scoped to its stock item (two items may each have a '
    '"B-01"). Carries mfg/expiry dates for expiry reporting.';

-- ---------------------------------------------------------------------------
-- stock_movement: the immutable movement ledger. One row per inventory LINE of
-- one voucher VERSION. Written ONLY inside ledger.post()'s transaction; there
-- must be no other code path that inserts here.
--
-- Versioned with the voucher exactly like voucher_gst_line and bill_allocation:
-- an ALTER appends a new voucher_version and re-states its movements under it,
-- so every reader must join voucher_head.current_version_id or it will count
-- the superseded lines twice. The negative-stock trigger below does exactly
-- that, and it is the reference implementation for the report-time replay.
-- ---------------------------------------------------------------------------
CREATE TABLE stock_movement (
    id                 UUID PRIMARY KEY,
    company_id         UUID NOT NULL,
    voucher_version_id UUID NOT NULL,
    -- 1-based position in the voucher, preserving input order. Part of the
    -- replay ordering key, which is why it is NOT NULL and checked.
    line_no            INTEGER NOT NULL CHECK (line_no >= 1),
    stock_item_id      UUID NOT NULL,
    -- Nullable DIMENSIONS. NULL means "not tracked on this line" and, per the
    -- MATCH SIMPLE default, suppresses the composite FK check for that
    -- dimension -- which is what we want: a company that never enables
    -- multi-location or batches should not be forced to invent master rows.
    godown_id          UUID NULL,
    batch_id           UUID NULL,
    direction          stock_direction NOT NULL,
    -- Always positive; `direction` carries the sign. NOT stored as a signed
    -- quantity so that a report which forgets the direction filter reads
    -- obviously wrong rather than subtly wrong.
    qty                NUMERIC(19,4) NOT NULL CHECK (qty > 0),
    -- The line's own rate and discount as entered. These are INPUTS to
    -- valuation, not a valuation: the closing VALUE of the item is derived by
    -- replaying the whole ledger, never read off a row.
    rate               NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (rate >= 0),
    discount_pct       NUMERIC(9,4)  NOT NULL DEFAULT 0
                       CHECK (discount_pct >= 0 AND discount_pct <= 100),
    amount             NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (amount >= 0),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The ONLY uniqueness. Deliberately does NOT include the dimensions: one
    -- invoice may legitimately ship the same item/godown/batch on two lines at
    -- two rates.
    UNIQUE (voucher_version_id, line_no),
    CONSTRAINT stock_movement_version_fk
        FOREIGN KEY (company_id, voucher_version_id)
        REFERENCES voucher_version (company_id, id),
    CONSTRAINT stock_movement_item_fk
        FOREIGN KEY (company_id, stock_item_id)
        REFERENCES stock_item (company_id, id),
    CONSTRAINT stock_movement_godown_fk
        FOREIGN KEY (company_id, godown_id)
        REFERENCES godown (company_id, id),
    CONSTRAINT stock_movement_batch_fk
        FOREIGN KEY (company_id, batch_id)
        REFERENCES stock_batch (company_id, id)
);
CREATE INDEX stock_movement_company ON stock_movement (company_id);
CREATE INDEX stock_movement_version ON stock_movement (voucher_version_id);
-- The balance/replay key: everything that asks "how much of X is where" reads
-- through this.
CREATE INDEX stock_movement_position
    ON stock_movement (company_id, stock_item_id, godown_id, batch_id);

COMMENT ON TABLE stock_movement IS
    'Immutable inventory movement line belonging to a voucher_version. Written '
    'only inside ledger.post(). Quantity is authoritative; stock VALUE is never '
    'stored and is derived at report time by replaying these rows.';

-- Immutable, like posting, bill_allocation and voucher_gst_line: a correction
-- appends a new version and re-states its lines, never edits the old ones.
CREATE OR REPLACE FUNCTION stock_movement_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'stock_movement is immutable; % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER stock_movement_no_mutate
    BEFORE UPDATE OR DELETE ON stock_movement
    FOR EACH ROW EXECUTE FUNCTION stock_movement_immutable();

-- ---------------------------------------------------------------------------
-- stock_opening: an opening position, as a FIRST-CLASS ROW.
--
-- An opening is a position carried from before the books -- 40 units already in
-- the Main Location on day one -- exactly as a ledger opening balance is. It is
-- therefore per (item, godown, batch), because that is the grain at which a
-- position exists: "40 units" is not a fact, "40 units of item X in godown Y
-- from batch Z at rate R" is.
--
-- This replaces stock_item.opening_qty/opening_rate, which could only ever
-- express a single dimensionless number per item and had nowhere to put the
-- location or the batch.
-- ---------------------------------------------------------------------------
CREATE TABLE stock_opening (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    stock_item_id UUID NOT NULL,
    godown_id     UUID NULL,
    batch_id      UUID NULL,
    qty           NUMERIC(19,4) NOT NULL DEFAULT 0,
    -- The rate the opening position is carried at. An INPUT to the valuer's
    -- initial state, not a stored valuation of anything later.
    rate          NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (rate >= 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One opening per position. NULLS NOT DISTINCT so that "no godown" and "no
    -- batch" are a POSITION like any other -- without it, Postgres treats every
    -- NULL as distinct and a company could accumulate ten different openings
    -- for the same untracked item, each invisible to the others.
    CONSTRAINT stock_opening_position
        UNIQUE NULLS NOT DISTINCT (company_id, stock_item_id, godown_id, batch_id),
    CONSTRAINT stock_opening_item_fk
        FOREIGN KEY (company_id, stock_item_id)
        REFERENCES stock_item (company_id, id),
    CONSTRAINT stock_opening_godown_fk
        FOREIGN KEY (company_id, godown_id)
        REFERENCES godown (company_id, id),
    CONSTRAINT stock_opening_batch_fk
        FOREIGN KEY (company_id, batch_id)
        REFERENCES stock_batch (company_id, id)
);
CREATE INDEX stock_opening_company ON stock_opening (company_id);
CREATE INDEX stock_opening_item    ON stock_opening (company_id, stock_item_id);

COMMENT ON TABLE stock_opening IS
    'Opening stock position per (item, godown, batch), carried from before the '
    'books. Seeds the valuer''s initial state; replaces the dimensionless '
    'stock_item.opening_qty/opening_rate pair.';

-- ---------------------------------------------------------------------------
-- company.allow_negative_stock: the opt-in escape hatch, defaulting to REFUSE.
--
-- Tally ships this as "Allow Negative Stock" and defaults it OFF for a good
-- reason: stock that goes negative is almost always a missing purchase entry,
-- and letting the sale through turns a data-entry error into a wrong valuation
-- that survives into the balance sheet. Some trades genuinely need it (goods
-- sold before the supplier's invoice arrives), so it is a company setting and
-- not a hard rule.
-- ---------------------------------------------------------------------------
ALTER TABLE company
    ADD COLUMN allow_negative_stock BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN company.allow_negative_stock IS
    'When false (default) a voucher that would drive any (item, godown, batch) '
    'position negative is REFUSED at commit by stock_movement_not_negative.';

-- ---------------------------------------------------------------------------
-- stock_item.valuation_method: which valuer replays this item's movements.
--
-- v1 ships WEIGHTED_AVG only, and the CHECK says so out loud rather than
-- accepting 'FIFO' and silently valuing it as weighted average. Weighted
-- average's replay state {Qty, Value} is a strict SUBSET of what FIFO needs (a
-- lot queue), so adding FIFO later is a new arm in ValuerFor plus fixtures --
-- no schema change, no change to the post path.
-- ---------------------------------------------------------------------------
ALTER TABLE stock_item
    ADD COLUMN valuation_method TEXT NOT NULL DEFAULT 'WEIGHTED_AVG'
        CHECK (valuation_method IN ('WEIGHTED_AVG'));

-- ---------------------------------------------------------------------------
-- Retire the dimensionless opening columns.
--
-- The RENAME IS DELIBERATE AND THE BREAKAGE IS THE POINT. Dropping them would
-- lose the data; leaving them would leave TWO sources of truth for an opening
-- position, and the one that is easier to write to (a column on the master) is
-- the one that is wrong. Renaming makes masters.CreateStockItem's INSERT fail
-- LOUDLY at the next call instead of quietly maintaining a second, unreconciled
-- opening. The caller is fixed in slice INV-E.
-- ---------------------------------------------------------------------------
ALTER TABLE stock_item RENAME COLUMN opening_qty  TO legacy_opening_qty;
ALTER TABLE stock_item RENAME COLUMN opening_rate TO legacy_opening_rate;

COMMENT ON COLUMN stock_item.legacy_opening_qty IS
    'Pre-0025 dimensionless opening quantity. Retained only as the provenance '
    'of the backfilled stock_opening rows; nothing may write here again.';

-- ---------------------------------------------------------------------------
-- Row-level security for the four new tenant tables: default-deny, ENABLE +
-- FORCE, USING + WITH CHECK pinned to the total app_current_company_id()
-- resolver -- identical to every other tenant table since 0004.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'godown', 'stock_batch', 'stock_movement', 'stock_opening'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I '
            || 'USING (company_id = app_current_company_id()) '
            || 'WITH CHECK (company_id = app_current_company_id())',
            t);
    END LOOP;
END
$$;

-- Least-privilege grants. UPDATE/DELETE are granted on stock_movement on
-- purpose: immutability is enforced by the trigger above so the caller gets a
-- clear domain error ('stock_movement is immutable; UPDATE is not permitted')
-- rather than a bare permission-denied, exactly as 0002 argued for posting.
GRANT SELECT, INSERT, UPDATE, DELETE ON
    godown, stock_batch, stock_movement, stock_opening
    TO tally_app;

-- ---------------------------------------------------------------------------
-- NEGATIVE STOCK IS REFUSED AT COMMIT.
--
-- Enforced TWICE, mirroring posting_balanced exactly: this deferred CONSTRAINT
-- TRIGGER is AUTHORITATIVE, and the Go pre-check in the post path exists only
-- to produce a friendlier error that names the item. If they ever disagree, the
-- database wins -- that is the whole point of putting it here, where a bug in
-- (or a bypass of) the application layer cannot reach.
--
-- WHY DEFERRABLE INITIALLY DEFERRED. A single voucher can contain both the
-- inward and the outward of the same item (a manufacturing journal consumes
-- components and produces a finished good; a transfer moves stock between
-- godowns). Checked per-statement, the OUT line would raise before the IN line
-- had been inserted, and a perfectly balanced voucher would be rejected
-- depending on the order its lines happened to be written. Checked at COMMIT,
-- the whole voucher is visible and only the FINAL position matters -- the same
-- argument that makes posting_balanced deferred.
--
-- The closing position joins voucher_head.current_version_id because movements
-- are versioned: after an ALTER, the superseded version's rows are still in the
-- table and counting them would invent stock that was never there.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION check_stock_not_negative()
RETURNS TRIGGER AS $$
DECLARE
    v_allow    BOOLEAN;
    v_opening  NUMERIC(19,4);
    v_net      NUMERIC(19,4);
    v_closing  NUMERIC(19,4);
    v_item     TEXT;
    v_godown   TEXT;
    v_batch    TEXT;
BEGIN
    -- Fail CLOSED: an invisible/absent company row yields NULL, which COALESCE
    -- turns into "do not allow negative", so a mis-scoped session can never
    -- accidentally acquire the permissive behaviour.
    SELECT allow_negative_stock INTO v_allow
      FROM company WHERE id = NEW.company_id;
    IF COALESCE(v_allow, false) THEN
        RETURN NULL;
    END IF;

    SELECT COALESCE(SUM(so.qty), 0) INTO v_opening
      FROM stock_opening so
     WHERE so.company_id    = NEW.company_id
       AND so.stock_item_id = NEW.stock_item_id
       AND so.godown_id IS NOT DISTINCT FROM NEW.godown_id
       AND so.batch_id  IS NOT DISTINCT FROM NEW.batch_id;

    SELECT COALESCE(SUM(CASE WHEN sm.direction = 'IN' THEN sm.qty
                             ELSE -sm.qty END), 0)
      INTO v_net
      FROM stock_movement sm
      JOIN voucher_head vh
        ON vh.company_id         = sm.company_id
       AND vh.current_version_id = sm.voucher_version_id
     WHERE sm.company_id    = NEW.company_id
       AND sm.stock_item_id = NEW.stock_item_id
       AND sm.godown_id IS NOT DISTINCT FROM NEW.godown_id
       AND sm.batch_id  IS NOT DISTINCT FROM NEW.batch_id
       AND vh.is_deleted = false;

    v_closing := v_opening + v_net;
    IF v_closing < 0 THEN
        SELECT name INTO v_item FROM stock_item
         WHERE company_id = NEW.company_id AND id = NEW.stock_item_id;
        SELECT name INTO v_godown FROM godown
         WHERE company_id = NEW.company_id AND id = NEW.godown_id;
        SELECT name INTO v_batch FROM stock_batch
         WHERE company_id = NEW.company_id AND id = NEW.batch_id;
        RAISE EXCEPTION
            'negative stock refused for item % (godown %, batch %): closing balance would be %',
            COALESCE(v_item, NEW.stock_item_id::text),
            COALESCE(v_godown, '-'), COALESCE(v_batch, '-'), v_closing;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER stock_movement_not_negative
    AFTER INSERT ON stock_movement
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_stock_not_negative();

-- ---------------------------------------------------------------------------
-- Per-company inventory seed: the system 'Main Location' godown.
-- Idempotent via a NOT EXISTS guard, so it is safe to call on every company
-- creation AND to re-run over existing tenants.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_inventory_defaults(p_company_id UUID)
RETURNS VOID AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM godown
         WHERE company_id = p_company_id AND name = 'Main Location'
    ) THEN
        INSERT INTO godown (id, company_id, name, is_system)
        VALUES (gen_random_uuid(), p_company_id, 'Main Location', true);
    END IF;
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_inventory_defaults(UUID) TO tally_app;

-- Backfill EVERY existing company. Without this, only companies created after
-- this migration would have a location to hold stock, and the difference would
-- surface on older tenants as an inventory voucher that cannot be entered.
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN SELECT id FROM company LOOP
        PERFORM seed_inventory_defaults(c.id);
    END LOOP;
END
$$;

-- ---------------------------------------------------------------------------
-- BEGIN BACKFILL: legacy stock_item openings -> stock_opening
-- Every stock_item that carried a non-zero opening quantity gets exactly ONE
-- stock_opening row, at that company's 'Main Location' with no batch -- which
-- is the only position the old dimensionless columns could have meant.
-- Guarded by NOT EXISTS so it is idempotent (and so it can be re-executed
-- verbatim by the test that proves it works).
-- ---------------------------------------------------------------------------
DO $backfill$
DECLARE
    r           RECORD;
    main_godown UUID;
BEGIN
    FOR r IN
        SELECT id, company_id, legacy_opening_qty, legacy_opening_rate
          FROM stock_item
         WHERE legacy_opening_qty <> 0
    LOOP
        SELECT id INTO main_godown FROM godown
         WHERE company_id = r.company_id AND name = 'Main Location';

        IF NOT EXISTS (
            SELECT 1 FROM stock_opening
             WHERE company_id    = r.company_id
               AND stock_item_id = r.id
               AND godown_id IS NOT DISTINCT FROM main_godown
               AND batch_id  IS NULL
        ) THEN
            INSERT INTO stock_opening
                (id, company_id, stock_item_id, godown_id, batch_id, qty, rate)
            VALUES (gen_random_uuid(), r.company_id, r.id, main_godown, NULL,
                    r.legacy_opening_qty, r.legacy_opening_rate);
        END IF;
    END LOOP;
END
$backfill$;
-- END BACKFILL: legacy stock_item openings -> stock_opening
-- ---------------------------------------------------------------------------

-- ---------------------------------------------------------------------------
-- Extend the per-company seed so NEW companies get their 'Main Location' at
-- creation. The whole body is restated (rather than wrapped) for the same
-- reason 0018 restated it: a fresh database applying 0003 -> 0005 -> 0018 ->
-- 0025 must end with one complete, self-describing definition.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_company_defaults(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    rec RECORD;
    grp_id      UUID;
    cash_grp    UUID;
    pl_primary  UUID;
BEGIN
    FOR rec IN
        SELECT * FROM (VALUES
            ('Capital Account',       'LIABILITY', false),
            ('Loans (Liability)',     'LIABILITY', false),
            ('Current Liabilities',   'LIABILITY', false),
            ('Fixed Assets',          'ASSET',     false),
            ('Investments',           'ASSET',     false),
            ('Current Assets',        'ASSET',     false),
            ('Branch / Divisions',    'ASSET',     false),
            ('Misc. Expenses (ASSET)','ASSET',     false),
            ('Suspense A/c',          'ASSET',     false),
            ('Sales Accounts',        'INCOME',    true),
            ('Purchase Accounts',     'EXPENSE',   true),
            ('Direct Incomes',        'INCOME',    true),
            ('Indirect Incomes',      'INCOME',    true),
            ('Direct Expenses',       'EXPENSE',   true),
            ('Indirect Expenses',     'EXPENSE',   true)
        ) AS v(name, nature, is_pl)
    LOOP
        grp_id := gen_random_uuid();
        INSERT INTO account_group (id, company_id, name, parent_id, path,
                                   nature, is_pl, is_system)
        VALUES (grp_id, p_company_id, rec.name, NULL,
                text2ltree('g' || replace(grp_id::text, '-', '')),
                rec.nature::account_nature, rec.is_pl, true);
    END LOOP;

    FOR rec IN
        SELECT * FROM (VALUES
            ('Bank Accounts',          'Current Assets',      false),
            ('Cash-in-Hand',           'Current Assets',      false),
            ('Deposits (Asset)',       'Current Assets',      false),
            ('Loans & Advances (Asset)','Current Assets',     false),
            ('Stock-in-Hand',          'Current Assets',      false),
            ('Sundry Debtors',         'Current Assets',      true),
            ('Duties & Taxes',         'Current Liabilities', false),
            ('Provisions',             'Current Liabilities', false),
            ('Sundry Creditors',       'Current Liabilities', true),
            ('Bank OD A/c',            'Loans (Liability)',   false),
            ('Secured Loans',          'Loans (Liability)',   false),
            ('Unsecured Loans',        'Loans (Liability)',   false),
            ('Reserves & Surplus',     'Capital Account',     false)
        ) AS v(name, parent_name, behaves_as_subledger)
    LOOP
        grp_id := gen_random_uuid();
        INSERT INTO account_group (id, company_id, name, parent_id, path,
                                   nature, is_pl, behaves_as_subledger, is_system)
        SELECT grp_id, p_company_id, rec.name, parent.id,
               parent.path || text2ltree('s' || replace(grp_id::text, '-', '')),
               parent.nature, parent.is_pl, rec.behaves_as_subledger, true
          FROM account_group parent
         WHERE parent.company_id = p_company_id AND parent.name = rec.parent_name;
    END LOOP;

    SELECT id INTO cash_grp FROM account_group
        WHERE company_id = p_company_id AND name = 'Cash-in-Hand';
    INSERT INTO ledger (id, company_id, name, group_id, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Cash', cash_grp, true);

    SELECT id INTO pl_primary FROM account_group
        WHERE company_id = p_company_id AND name = 'Capital Account';
    INSERT INTO ledger (id, company_id, name, group_id, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Profit & Loss A/c', pl_primary, true);

    INSERT INTO voucher_type (id, company_id, name, parent_class,
                              numbering_method, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Sales', 'SALES', 'AUTOMATIC', true);

    PERFORM seed_phase1_voucher_types(p_company_id);
    PERFORM seed_credit_debit_note_types(p_company_id);
    PERFORM seed_inventory_defaults(p_company_id);
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_company_defaults(UUID) TO tally_app;
