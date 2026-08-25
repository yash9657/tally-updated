-- 0078_valuation_methods.sql
-- FEATURE T9: widen stock_item.valuation_method to admit FIFO and LIFO.
--
-- WHAT THIS IS AND IS NOT
-- -----------------------
-- It is three lines of CHECK and one comment. There is NO new column, NO new
-- table and nothing to backfill, and that is the whole point of the shape
-- migration 0025 chose: stock VALUE is never stored, so a costing method is not
-- data, it is the name of the fold a report applies to the movement ledger it
-- already has. Adding a method adds an arm to inventory.ValuerFor and a string
-- to this constraint. Nothing is re-derived, nothing is migrated, and no
-- existing row moves -- every stock_item keeps 'WEIGHTED_AVG' and every figure
-- ever printed stays reproducible.
--
-- WHY THE OLD CHECK HAD TO NAME ONE VALUE, AND WHY THIS ONE NAMES THREE
-- --------------------------------------------------------------------
-- The constraint is deliberately an enumeration and not a free TEXT column.
-- inventory.ValuerFor ERRORS on a method it does not implement rather than
-- falling back to weighted average, so the database and the binary are two
-- halves of one rule: the column may only hold a method some valuer implements,
-- and a deployment running an older binary FAILS LOUDLY on the Stock Summary
-- and the Balance Sheet instead of quietly reporting average numbers under a
-- FIFO heading. Widening the CHECK is therefore a DEPLOYMENT-ORDERED change:
-- this migration must not run against a binary that predates the FIFO arm.
--
-- ⚠️ AND IT IS THE ONE MIGRATION IN THIS TREE THAT CAN MOVE A BALANCE SHEET
-- WITHOUT TOUCHING A SINGLE ROW OF DATA. It does not move one by itself -- no
-- item is switched here -- but the moment an operator switches one, that item's
-- closing stock, its cost of goods sold and therefore the Profit & Loss all
-- change, on figures that were already printed and read. That is why the switch
-- is its own route with its own audit row (masters.SetStockItemValuationMethod)
-- and not a field on a general master editor: the change is a restatement, and
-- it has to be as traceable as one.
--
-- WHAT IS STILL REFUSED, AND WHY IT IS NOT AN OVERSIGHT
-- ----------------------------------------------------
-- Tally ships eight costing methods. Five are absent here on purpose:
--
--   * 'AT_ZERO_COST' is trivial and useless without a reason to hold stock at
--     nil, and it silently zeroes the Balance Sheet's closing stock;
--   * 'LAST_PURCHASE_COST' and 'STANDARD_COST' need a price a movement ledger
--     does not carry (the standard, and what to do when no purchase exists);
--   * 'MONTHLY_AVG' needs a period boundary the replay has no notion of;
--   * LIFO ANNUAL costs an issue at a purchase made LATER in the same year, so
--     no figure can be stated until the year closes and every interim Profit &
--     Loss would be restated by the next purchase keyed. The LIFO admitted here
--     is the PERPETUAL variant, which is what a movement replay can express.
--
-- Each of those is a decision, not a gap, and admitting one to the CHECK before
-- a valuer exists for it would let a row name a method the reports cannot serve.

ALTER TABLE stock_item
    DROP CONSTRAINT stock_item_valuation_method_check;

ALTER TABLE stock_item
    ADD CONSTRAINT stock_item_valuation_method_check
        CHECK (valuation_method IN ('WEIGHTED_AVG', 'FIFO', 'LIFO'));

COMMENT ON COLUMN stock_item.valuation_method IS
    'Which valuer replays this item''s movements: WEIGHTED_AVG (Tally''s '
    'default, and this build''s), FIFO, or LIFO (perpetual). Value is NEVER '
    'stored, so this names a fold and not a figure; changing it restates every '
    'report that has ever priced this item.';
