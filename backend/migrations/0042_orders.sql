-- 0042_orders.sql
-- SALES AND PURCHASE ORDERS -- A COMMITTED QUANTITY, WHICH IS NOT A MOVEMENT.
--
-- Migration number from PLAN-TALLY-PARITY.md §2.5, which reserves 0042 for T3's
-- order schema. ⛔ §2.5 ALSO reserves 0044 for T3's ACL rules AND 0044 CANNOT
-- CARRY THEM. seed_company_auth_defaults is extended only by restating its whole
-- plpgsql body, migrations.go applies files in sort.Strings order, and the
-- highest-numbered restatement is the only body that survives -- currently
-- 0072_cost_report_acl.sql. Rules written at 0044 would be silently overwritten
-- by 0072's body on every fresh database and simply would not exist. The order
-- book's ACL counterweight is therefore 0073_order_book_acl.sql and 0044 is left
-- unused, exactly as 0041 was.
--
-- ===========================================================================
-- ⛔⛔ AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT. THIS IS THE WHOLE MODEL.
-- ===========================================================================
-- The stock ledger has exactly ONE primitive: a positive quantity plus a
-- direction (stock_movement, migration 0025). An order is neither. Nothing has
-- moved when a customer orders 20 units -- the goods are still on our shelf,
-- they are still ours, and they are still in closing stock at their full value.
--
-- Pushing an order through stock_movement would therefore corrupt closing stock,
-- and closing stock now feeds the BALANCE SHEET. A sales order for goods we
-- still hold would show them as gone; a purchase order would show goods we do
-- not have. The error compounds with every open order and reconciles against
-- nothing.
--
-- So orders live HERE, in their own tables, and they affect REPORTS (the order
-- book, and later reorder status) and never BALANCES. Nothing in this migration
-- is read by closing stock, the trial balance, the P&L or the balance sheet, and
-- nothing in this migration reads them.
--
-- ===========================================================================
-- PARTIAL FULFILMENT IS THE NORMAL CASE, AND THE OUTSTANDING BALANCE IS THE POINT
-- ===========================================================================
-- An order is consumed PROGRESSIVELY. A 20-unit sales order is delivered 5 now,
-- 12 next week, 3 the week after -- three delivery notes, three fulfilment
-- links, and after the first one the order is 15 outstanding. That outstanding
-- figure is the entire reason the feature exists: it is what a business promises
-- customers and what it expects from suppliers.
--
-- ⛔⛔ THEREFORE THERE IS NO fulfilled_qty COLUMN, AND NO status COLUMN, ON
-- order_line OR order_head. Read this before adding one.
--
-- Both are DERIVED by summing order_fulfilment, exactly as stock VALUE is derived
-- by replaying stock_movement and is never stored (0025's header). A stored
-- fulfilled_qty is a second source of truth for a figure the movement rows
-- already answer, and it goes stale in a way nothing can detect the moment a
-- fulfilling voucher is ALTERED -- because an ALTER writes a NEW voucher version
-- and re-states its fulfilments under it, without ever visiting this table. The
-- order would sit at "15 outstanding" forever while the corrected delivery note
-- said 8. There is no reconciliation that recovers from that, because both
-- numbers look equally authoritative.
--
-- is_closed IS DIFFERENT AND IS NOT DERIVED: it records a HUMAN DECISION to
-- abandon an order that was never fulfilled ("the customer cancelled"). Nothing
-- can compute that from the fulfilment rows -- an unfulfilled order and an
-- abandoned one have identical fulfilment histories -- so it is stored, and it
-- carries a reason.
--
-- ===========================================================================
-- WHY THE FULFILMENT LINK IS VERSIONED WITH THE VOUCHER AND IS IN THE HASH
-- ===========================================================================
-- order_fulfilment keys on (voucher_version_id, line_no) -- the same key
-- stock_movement uses -- and NOT on voucher_id. It is versioned with the voucher
-- and re-stated by every ALTER, so every reader MUST join
-- voucher_head.current_version_id or it will count a superseded delivery twice
-- and report an order as fulfilled when it is half outstanding.
--
-- That choice follows from where the value comes from. A fulfilment is
-- OPERATOR-KEYED INPUT: someone chose, at entry, which order this shipment
-- satisfies. That places it with inward_doc (0032) and NOT with
-- bank_reconciliation (0045) or tds_challan (0069). Those two key on voucher_id
-- and store what they matched precisely BECAUSE they are metadata added later by
-- a different act, and can therefore be left stale by an ALTER they never see. A
-- fulfilment cannot be left stale: an ALTER re-states it from the new input in
-- the same transaction, so an alter that moves 5 units to a different order line
-- says so in the signed bytes and the outstanding balance recomputes from the
-- current version with nothing to detect and nothing to repair.
--
-- It therefore also rides in the hashed canonical payload as
-- CanonicalPayload.Fulfilments (ledger/hash.go), written to this table from the
-- SAME in-memory slice inside the SAME transaction by ledger.post(), so the
-- signature and the rows cannot disagree. The field order in that payload was
-- fixed permanently before either feature was built -- CostAllocations then
-- Fulfilments -- and must not be touched.
--
-- ⚠️ ITS qty IS A LINK, NEVER A STOCK POSITION. order_fulfilment.qty says how
-- much of an ORDER was consumed; stock_movement.qty says how much STOCK moved.
-- They are usually equal and they are NOT the same fact. Closing stock reads the
-- movement, the order book reads this, and neither reads the other. The one case
-- where they legitimately differ is the whole reason the distinction is written
-- down -- see the over-fulfilment note below.

-- ---------------------------------------------------------------------------
-- order_direction: SALES or PURCHASE, as one enum on one pair of tables.
--
-- ⚠️ ONE order_head WITH A DIRECTION, AND NOT sales_order + purchase_order.
-- §2.5's sketch names two head tables; this is a deliberate departure and the
-- reason is the house pattern it would have broken. The stock ledger already
-- made this exact decision: "exactly one primitive -- a positive quantity plus a
-- direction" (0025), because two tables mean two copies of every constraint,
-- every index, every trigger and every report query, and the copy that drifts is
-- the one somebody reaches first. A shared order_line table cannot FK to two
-- head tables at all without a polymorphic parent, which is the shape RLS and
-- composite FKs are least able to protect.
--
-- What is genuinely asymmetric between the two -- who may read the sales
-- pipeline versus the purchase commitments -- is an AUTHORISATION difference, and
-- it is expressed where authorisation lives: two resource tokens and two report
-- names (0073), not two tables.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'order_direction') THEN
        CREATE TYPE order_direction AS ENUM ('SALES', 'PURCHASE');
    END IF;
END
$$;

-- ===========================================================================
-- order_head -- THE COMMITMENT
-- ===========================================================================
CREATE TABLE IF NOT EXISTS order_head (
    id              UUID PRIMARY KEY,
    company_id      UUID NOT NULL,

    -- SALES: we have promised to deliver. PURCHASE: we have promised to pay for
    -- goods a supplier will send. The direction decides which end of the
    -- business the outstanding balance is a liability to, and it decides which
    -- way the goods must move to fulfil it -- enforced below.
    direction       order_direction NOT NULL,

    order_no        TEXT NOT NULL,
    order_date      DATE NOT NULL,

    -- The customer (SALES) or the supplier (PURCHASE). A party ledger, exactly
    -- as voucher_version.party_ledger_id is, so an order and the invoice that
    -- fulfils it name the same row and the order book can group by counterparty
    -- without a name match.
    party_ledger_id UUID NOT NULL,

    narration       TEXT NOT NULL DEFAULT '',

    -- ⛔ A HUMAN DECISION, NOT A DERIVED STATE. See the header: "fulfilled" is
    -- computed from order_fulfilment and must never be stored, but "abandoned"
    -- cannot be computed from anything -- an order nobody ever delivered and one
    -- the customer cancelled have identical fulfilment histories. Storing it is
    -- the only way to tell them apart, and an order book that showed a cancelled
    -- order as outstanding forever would be chased by a salesperson every month.
    is_closed       BOOLEAN NOT NULL DEFAULT false,
    -- Required WHEN closed and forbidden otherwise. A manual closure with no
    -- stated reason is precisely the row an auditor asks about, and a reason
    -- attached to an open order is a claim about something that has not
    -- happened.
    closed_reason   TEXT NOT NULL DEFAULT '',

    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_head_company_scope UNIQUE (company_id, id),
    CONSTRAINT order_head_no_not_blank CHECK (btrim(order_no) <> ''),
    CONSTRAINT order_head_closed_has_reason
        CHECK (NOT is_closed OR btrim(closed_reason) <> ''),
    CONSTRAINT order_head_open_has_no_reason
        CHECK (is_closed OR btrim(closed_reason) = ''),

    CONSTRAINT order_head_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id),
    CONSTRAINT order_head_party_fk
        FOREIGN KEY (company_id, party_ledger_id)
        REFERENCES ledger (company_id, id)
);
-- Unique WITHIN A DIRECTION, not within the company: a business numbers its own
-- sales orders and receives supplier order numbers it does not control, and
-- SO-1 colliding with PO-1 is not a collision an operator would accept being
-- told about. Case-insensitive for the reason cost_category_name_unique records:
-- "SO-1" and "so-1" are one order to an operator, and two of them is how half a
-- commitment goes missing while both halves look complete.
CREATE UNIQUE INDEX IF NOT EXISTS order_head_no_unique
    ON order_head (company_id, direction, lower(btrim(order_no)));
-- The order book's two axes: by counterparty, and by date within a direction.
CREATE INDEX IF NOT EXISTS order_head_by_party
    ON order_head (company_id, party_ledger_id);
CREATE INDEX IF NOT EXISTS order_head_by_date
    ON order_head (company_id, direction, order_date);

COMMENT ON TABLE order_head IS
    'A sales or purchase ORDER: a committed quantity, not a movement. Nothing '
    'here is read by closing stock, the trial balance or the balance sheet. '
    'Carries NO fulfilled/status column -- both are derived from '
    'order_fulfilment; is_closed records only a human decision to abandon.';

COMMENT ON COLUMN order_head.is_closed IS
    'Manual abandonment ("customer cancelled"). NOT a fulfilment state: '
    'fulfilment is derived by summing order_fulfilment across current voucher '
    'versions and is never stored.';

-- ===========================================================================
-- order_line -- WHAT WAS COMMITTED, PER ITEM
-- ===========================================================================
CREATE TABLE IF NOT EXISTS order_line (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    order_id      UUID NOT NULL,

    -- 1-based position within the order, preserving input order, exactly as
    -- stock_movement.line_no does within a voucher. It is what an operator reads
    -- off the printed order when they ask "which line is short".
    line_no       INTEGER NOT NULL CHECK (line_no >= 1),

    stock_item_id UUID NOT NULL,

    -- The COMMITTED quantity. Always positive: order_head.direction carries the
    -- sense, exactly as stock_movement leaves the sign to `direction`. A
    -- negative or zero commitment is not a smaller order, it is a data error
    -- that would make the outstanding balance meaningless.
    qty           NUMERIC(19,4) NOT NULL CHECK (qty > 0),

    -- The agreed rate, an INPUT to the order book's value column and never a
    -- valuation of anything. The invoice that fulfils this line may lawfully
    -- bill a different rate (a price revision, a discount agreed later), and
    -- that difference is a finding the order book can show -- it is not a
    -- disagreement to be resolved by overwriting either figure.
    rate          NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (rate >= 0),

    -- When this line was promised for. NULL means "no date agreed", which is
    -- common and is not the same as overdue -- a report that treated NULL as
    -- 'today' would invent an overdue commitment.
    due_date      DATE NULL,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_line_company_scope UNIQUE (company_id, id),
    CONSTRAINT order_line_position UNIQUE (company_id, order_id, line_no),

    CONSTRAINT order_line_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id),
    CONSTRAINT order_line_order_fk
        FOREIGN KEY (company_id, order_id)
        REFERENCES order_head (company_id, id) ON DELETE CASCADE,
    CONSTRAINT order_line_item_fk
        FOREIGN KEY (company_id, stock_item_id)
        REFERENCES stock_item (company_id, id)
);
CREATE INDEX IF NOT EXISTS order_line_by_order
    ON order_line (company_id, order_id);
-- "What is on order for this item" -- the reorder-status question, and the join
-- the order book uses to reach the item name.
CREATE INDEX IF NOT EXISTS order_line_by_item
    ON order_line (company_id, stock_item_id);

COMMENT ON TABLE order_line IS
    'One committed item quantity on an order. Carries NO fulfilled_qty: the '
    'outstanding balance is qty minus the sum of order_fulfilment rows on '
    'CURRENT, non-deleted voucher versions, and storing it would go stale '
    'undetectably the first time a fulfilling voucher was altered.';

-- ---------------------------------------------------------------------------
-- stock_movement needs a COMPANY-SCOPED key on its line before anything can FK
-- to one.
--
-- 0025 gave it UNIQUE (voucher_version_id, line_no) with no company_id, which
-- predates the (company_id, ...) composite pattern every tenant FK has used
-- since. Without this, order_fulfilment could only FK to voucher_version and the
-- line_no it names would be UNCHECKED -- a fulfilment could claim line 7 of a
-- three-line delivery note and the order book would report a delivery that
-- never happened, with the RLS policy on order_fulfilment checking the
-- fulfilment's own company_id and having nothing to say about the row it points
-- at.
--
-- Additive and index-only: no row is touched, so stock_movement_no_mutate (which
-- raises on UPDATE and DELETE) is not involved. Guarded on the catalogue rather
-- than IF NOT EXISTS because ALTER TABLE ... ADD CONSTRAINT has no such form --
-- the same shape 0040 used for posting_company_scope.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'stock_movement_company_line_scope'
           AND conrelid = 'stock_movement'::regclass
    ) THEN
        ALTER TABLE stock_movement ADD CONSTRAINT stock_movement_company_line_scope
            UNIQUE (company_id, voucher_version_id, line_no);
    END IF;
END
$$;

-- ===========================================================================
-- order_fulfilment -- THE LINK, AND ONLY THE LINK
-- ===========================================================================
CREATE TABLE IF NOT EXISTS order_fulfilment (
    id                 UUID PRIMARY KEY,
    company_id         UUID NOT NULL,

    -- Versioned with the voucher, exactly like stock_movement. See the header:
    -- an ALTER re-states these rows under a NEW version and the superseded ones
    -- stay in the table, so EVERY reader must join
    -- voucher_head.current_version_id.
    voucher_version_id UUID NOT NULL,

    -- The voucher's own 1-based inventory line -- the same line_no as the
    -- stock_movement row this fulfilment accompanies, and FK'd to it below so it
    -- cannot name a line the voucher does not have.
    line_no            INTEGER NOT NULL CHECK (line_no >= 1),

    -- The order LINE, never the order. Fulfilment is per item: a delivery note
    -- routinely completes one line of a three-line order and leaves the rest
    -- outstanding, and an order-level link could not say which.
    order_line_id      UUID NOT NULL,

    -- ⛔ HOW MUCH OF THE ORDER THIS LINE CONSUMED. NOT how much stock moved --
    -- that is stock_movement.qty on the very row this one FKs to. They are
    -- usually equal and they are different facts; see the header and the
    -- over-fulfilment note below for the case where they legitimately differ.
    qty                NUMERIC(19,4) NOT NULL CHECK (qty > 0),

    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_fulfilment_company_scope UNIQUE (company_id, id),

    -- One link per (voucher line, order line). ⚠️ The key deliberately does NOT
    -- stop one voucher line from fulfilling SEVERAL order lines: a single
    -- 20-unit delivery line legitimately covers a 12-unit order and an 8-unit
    -- one for the same item, and CanonicalFulfilment carries one entry each.
    -- What it stops is the same pair keyed twice, which could only make an
    -- order's total come out right by making the other entry wrong -- the
    -- argument posting_cost_allocation_one_per_centre records.
    CONSTRAINT order_fulfilment_one_per_pair
        UNIQUE (company_id, voucher_version_id, line_no, order_line_id),

    CONSTRAINT order_fulfilment_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id),
    CONSTRAINT order_fulfilment_version_fk
        FOREIGN KEY (company_id, voucher_version_id)
        REFERENCES voucher_version (company_id, id),
    -- ⛔ THE LINE, not just the version. This is what makes line_no a checked
    -- fact rather than a number the client asserted: a fulfilment can only name
    -- an inventory line that actually exists on that voucher version. It is also
    -- what lets the direction check below read the movement's own direction.
    CONSTRAINT order_fulfilment_movement_fk
        FOREIGN KEY (company_id, voucher_version_id, line_no)
        REFERENCES stock_movement (company_id, voucher_version_id, line_no),
    CONSTRAINT order_fulfilment_order_line_fk
        FOREIGN KEY (company_id, order_line_id)
        REFERENCES order_line (company_id, id)
);
CREATE INDEX IF NOT EXISTS order_fulfilment_by_version
    ON order_fulfilment (company_id, voucher_version_id);
-- THE ORDER BOOK'S KEY: "how much of this order line has been delivered".
CREATE INDEX IF NOT EXISTS order_fulfilment_by_order_line
    ON order_fulfilment (company_id, order_line_id);

COMMENT ON TABLE order_fulfilment IS
    'Which order LINE a voucher''s inventory line consumed, and how much of it. '
    'The second rendering of a value that also rides in the hashed voucher '
    'payload (CanonicalPayload.Fulfilments), written from the same in-memory '
    'slice in the same transaction by ledger.post(). Versioned with the voucher: '
    'every reader must join voucher_head.current_version_id.';

COMMENT ON COLUMN order_fulfilment.qty IS
    'Quantity of the ORDER consumed. NOT the quantity of stock moved -- that is '
    'stock_movement.qty on the row this one references.';

-- Immutable, exactly like stock_movement, posting and bill_allocation: a
-- correction appends a new voucher version and re-states its links, never edits
-- the old ones. Without this, an UPDATE here would silently change what an
-- already-signed voucher claims to have fulfilled, while the hashed payload went
-- on saying the original figure -- the two renderings would disagree and only
-- VerifyChain would know.
CREATE OR REPLACE FUNCTION order_fulfilment_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'order_fulfilment is immutable; % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS order_fulfilment_no_mutate ON order_fulfilment;
CREATE TRIGGER order_fulfilment_no_mutate
    BEFORE UPDATE OR DELETE ON order_fulfilment
    FOR EACH ROW EXECUTE FUNCTION order_fulfilment_immutable();

-- ---------------------------------------------------------------------------
-- ⛔⛔ OVER-FULFILMENT IS REFUSED, AND THE GOODS ARE STILL RECORDED. Read this
-- before relaxing it to a warning.
-- ---------------------------------------------------------------------------
-- PLAN-TALLY-PARITY.md §T3 requires this decision to be stated once. It is
-- REFUSE, and the reason it costs nothing is the qty distinction in the header:
--
--   A fulfilment quantity is a LINK, not a movement. An operator who genuinely
--   ships 105 units against a 100-unit order records a stock_movement of 105 --
--   the real goods really left the building, closing stock and the balance sheet
--   are correct -- and a fulfilment link of 100. The extra 5 consumed no order,
--   which is exactly true: nobody ordered them. The order line reads 0
--   outstanding, which is also true.
--
-- So refusing does NOT prevent recording an over-delivery. It prevents CLAIMING
-- that more of an order was consumed than the order ever committed, which is a
-- statement with no meaning: the outstanding balance would go negative and "the
-- customer owes us -5 units of promise" is not a fact about anything. Allowing
-- it with a finding was the alternative, and it was rejected because the finding
-- would sit beside a number the order book has to print, and every report that
-- summed outstanding across an order would quietly net a negative line against a
-- positive one somewhere else.
--
-- THREE properties are load-bearing, each a separate decision:
--
--   * DEFERRABLE INITIALLY DEFERRED, exactly as posting_balanced (0002),
--     stock_movement_not_negative (0025) and cost_allocation_sums_to_posting
--     (0040) are. During an ALTER the new version's fulfilment rows are inserted
--     while voucher_head.current_version_id is still being moved, and the
--     superseded version's rows are still present. Checked per-statement, a
--     correction that merely re-stated the same 20 units would total 40 against
--     a 20-unit order and be refused. Checked at COMMIT, only the final set of
--     current versions is visible.
--
--   * IT JOINS voucher_head.current_version_id AND is_deleted = false, for the
--     reason stock_movement_not_negative records: fulfilments are versioned, the
--     superseded version's rows are still in the table, and counting them would
--     invent a delivery that never happened. A DELETED voucher fulfils nothing
--     -- its tombstone carries no fulfilments at all (ledger.post()) and its
--     head is excluded here as a second line of defence.
--
--   * IT ALSO FIRES ON order_line UPDATE, via the same helper. An order may
--     legitimately be revised downwards, but revising a 20-unit line to 5 when
--     12 are already delivered would strand 7 units of delivery against a
--     commitment that no longer covers them -- the same violation reached from
--     the other side, and invisible to a trigger that only watched fulfilments.
--
-- AND IT ENFORCES THE DIRECTION, which no foreign key can: a SALES order is
-- fulfilled by goods going OUT and a PURCHASE order by goods coming IN. Without
-- this, a delivery note could be keyed against a purchase order and the order
-- book would report a supplier commitment as satisfied while the goods left the
-- building. The movement's own direction is read off the stock_movement row the
-- fulfilment already FKs to, so this is exact and needs nothing from the
-- application layer -- and in particular does not need voucher_version's class,
-- which lives only inside the signed payload JSON.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION check_order_line_commitment(
    p_company_id UUID, p_order_line_id UUID)
RETURNS VOID AS $$
DECLARE
    v_committed  NUMERIC(19,4);
    v_fulfilled  NUMERIC(19,4);
    v_line_no    INTEGER;
    v_order_no   TEXT;
    v_direction  order_direction;
    v_item       TEXT;
BEGIN
    -- The order line may have been deleted within this transaction (an order
    -- withdrawn, cascading its lines away), in which case there is no
    -- commitment left to check against.
    SELECT ol.qty, ol.line_no, oh.order_no, oh.direction
      INTO v_committed, v_line_no, v_order_no, v_direction
      FROM order_line ol
      JOIN order_head oh
        ON oh.company_id = ol.company_id AND oh.id = ol.order_id
     WHERE ol.company_id = p_company_id AND ol.id = p_order_line_id;
    IF NOT FOUND THEN
        RETURN;
    END IF;

    SELECT COALESCE(SUM(f.qty), 0) INTO v_fulfilled
      FROM order_fulfilment f
      JOIN voucher_head vh
        ON vh.company_id         = f.company_id
       AND vh.current_version_id = f.voucher_version_id
     WHERE f.company_id    = p_company_id
       AND f.order_line_id = p_order_line_id
       AND vh.is_deleted   = false;

    -- ⛔ A STRICT INEQUALITY, not equality. Partial fulfilment is the normal
    -- case and an order that is 15 outstanding is not an error -- it is the
    -- figure the whole feature exists to report. Only EXCEEDING the commitment
    -- is refused. (Contrast cost_allocation_sums_to_posting, which demands exact
    -- equality: an incomplete cost split leaves money unattributed, while an
    -- incomplete delivery is just a delivery that has not finished.)
    IF v_fulfilled > v_committed THEN
        SELECT si.name INTO v_item
          FROM order_line ol
          JOIN stock_item si
            ON si.company_id = ol.company_id AND si.id = ol.stock_item_id
         WHERE ol.company_id = p_company_id AND ol.id = p_order_line_id;
        RAISE EXCEPTION
            '% order % line % (%) committed % but % has now been fulfilled against it: % more than was ever ordered, which would leave the order book reporting a negative outstanding balance. The goods that moved are recorded in full on the voucher; only the claim against this order is refused',
            lower(v_direction::text), v_order_no, v_line_no,
            COALESCE(v_item, '(unknown item)'),
            v_committed, v_fulfilled, (v_fulfilled - v_committed);
    END IF;
END;
$$ LANGUAGE plpgsql;

-- ---------------------------------------------------------------------------
-- ⛔⛔ AND TWO MORE INVARIANTS THE ORDER-LINE CHECK ABOVE CANNOT SEE, because
-- each is about the VOUCHER LINE rather than about any one order.
-- ---------------------------------------------------------------------------
-- 1. THE SAME ITEM. An order line for Tables may only be fulfilled by a voucher
--    line that moved Tables. Without this, delivering Legs discharges the order
--    for Tables: the order book reports the commitment met, the customer has not
--    received what they ordered, and every quantity in the system is internally
--    consistent. No foreign key can express it -- it is an equality between two
--    columns in two different tables -- and the trigger already holds both rows.
--
-- 2. THE LINE CANNOT DISCHARGE MORE THAN IT MOVED. A 20-unit delivery line may
--    legitimately cover a 12-unit order and an 8-unit one (see
--    order_fulfilment_one_per_pair), so the per-order check above passes for
--    each. But it would ALSO pass for 20 against one order and 20 against
--    another: forty units of commitment discharged by twenty units of goods,
--    with neither order individually over-fulfilled and nothing else in the
--    system able to notice. That is the double-count shape the cost-centre model
--    warns about, one table over.
--
--    A STRICT INEQUALITY, like the order-side check: a line may discharge LESS
--    than it moved, which is exactly the over-delivery case in the header (ship
--    105 against a 100-unit order, link 100, and 5 units consumed no order).
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION check_order_fulfilment()
RETURNS TRIGGER AS $$
DECLARE
    v_move_dir  stock_direction;
    v_order_dir order_direction;
    v_order_no  TEXT;
    v_want_dir  stock_direction;
    v_move_item UUID;
    v_ord_item  UUID;
    v_move_qty  NUMERIC(19,4);
    v_linked    NUMERIC(19,4);
    v_item_a    TEXT;
    v_item_b    TEXT;
BEGIN
    -- THE DIRECTION. Read off the stock_movement row this fulfilment FKs to, so
    -- it is the direction the goods ACTUALLY moved and not a class the client
    -- asserted. The movement is guaranteed to exist by
    -- order_fulfilment_movement_fk; the lookup is written defensively anyway
    -- because a deferred trigger runs after every statement in the transaction
    -- and a NOT FOUND here must not be read as "direction agrees".
    SELECT sm.direction, sm.stock_item_id, sm.qty
      INTO v_move_dir, v_move_item, v_move_qty
      FROM stock_movement sm
     WHERE sm.company_id         = NEW.company_id
       AND sm.voucher_version_id = NEW.voucher_version_id
       AND sm.line_no            = NEW.line_no;

    SELECT oh.direction, oh.order_no, ol.stock_item_id
      INTO v_order_dir, v_order_no, v_ord_item
      FROM order_line ol
      JOIN order_head oh
        ON oh.company_id = ol.company_id AND oh.id = ol.order_id
     WHERE ol.company_id = NEW.company_id AND ol.id = NEW.order_line_id;

    IF v_order_dir IS NOT NULL AND v_move_dir IS NOT NULL THEN
        v_want_dir := CASE v_order_dir
                        WHEN 'SALES'    THEN 'OUT'::stock_direction
                        ELSE                 'IN'::stock_direction
                      END;
        IF v_move_dir <> v_want_dir THEN
            RAISE EXCEPTION
                '% order % is fulfilled by goods moving % but voucher line % moves them %: a delivery cannot satisfy a purchase commitment and a receipt cannot satisfy a sales one, so the order book would report a commitment as met while the goods went the other way',
                lower(v_order_dir::text), v_order_no, v_want_dir,
                NEW.line_no, v_move_dir;
        END IF;
    END IF;

    -- INVARIANT 1 -- the same item. See the note above the function.
    IF v_move_item IS NOT NULL AND v_ord_item IS NOT NULL
       AND v_move_item <> v_ord_item THEN
        SELECT name INTO v_item_a FROM stock_item
         WHERE company_id = NEW.company_id AND id = v_ord_item;
        SELECT name INTO v_item_b FROM stock_item
         WHERE company_id = NEW.company_id AND id = v_move_item;
        RAISE EXCEPTION
            '% order % commits % on the line this fulfilment names, but voucher line % moves %: delivering one item cannot discharge a commitment to supply a different one, and the order book would report the order as met',
            lower(v_order_dir::text), v_order_no,
            COALESCE(v_item_a, v_ord_item::text), NEW.line_no,
            COALESCE(v_item_b, v_move_item::text);
    END IF;

    -- INVARIANT 2 -- the line cannot discharge more than it moved. Summed across
    -- every order this ONE voucher line claims against; no voucher_head join is
    -- needed because all the rows share NEW.voucher_version_id, which is either
    -- current or not as a whole.
    IF v_move_qty IS NOT NULL THEN
        SELECT COALESCE(SUM(f.qty), 0) INTO v_linked
          FROM order_fulfilment f
         WHERE f.company_id         = NEW.company_id
           AND f.voucher_version_id = NEW.voucher_version_id
           AND f.line_no            = NEW.line_no;
        IF v_linked > v_move_qty THEN
            RAISE EXCEPTION
                'voucher line % moved % but claims to have fulfilled % across the orders it names: % units of commitment would be discharged by goods that never moved, and no single order looks over-fulfilled',
                NEW.line_no, v_move_qty, v_linked, (v_linked - v_move_qty);
        END IF;
    END IF;

    PERFORM check_order_line_commitment(NEW.company_id, NEW.order_line_id);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS order_fulfilment_within_commitment ON order_fulfilment;
CREATE CONSTRAINT TRIGGER order_fulfilment_within_commitment
    AFTER INSERT ON order_fulfilment
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_order_fulfilment();

-- The same invariant reached from the order side. See the third bullet above:
-- revising a commitment down below what is already delivered is the identical
-- violation and is invisible to a trigger that only watches fulfilments.
CREATE OR REPLACE FUNCTION check_order_line_revision()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM check_order_line_commitment(NEW.company_id, NEW.id);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS order_line_still_covers_fulfilments ON order_line;
CREATE CONSTRAINT TRIGGER order_line_still_covers_fulfilments
    AFTER UPDATE ON order_line
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_order_line_revision();

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, policy with BOTH USING
-- and WITH CHECK -- identical to every tenant table since 0004. USING alone
-- would let a session write an order for a company it can never see; WITH CHECK
-- alone would leak every tenant's forward sales pipeline on read, which is the
-- single most commercially sensitive list in the system. FORCE because
-- migrations run as the table owner and an owner bypasses its own policies
-- unless forced.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['order_head', 'order_line', 'order_fulfilment'] LOOP
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

-- UPDATE/DELETE are granted on order_fulfilment on purpose: immutability is
-- enforced by the trigger above, so a caller that tries gets a clear domain
-- error rather than a bare permission-denied -- the argument 0002 made for
-- posting and 0025 for stock_movement.
GRANT SELECT, INSERT, UPDATE, DELETE ON order_head       TO tally_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON order_line       TO tally_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON order_fulfilment TO tally_app;
