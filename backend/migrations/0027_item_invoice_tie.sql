-- 0027_item_invoice_tie.sql
-- THE STRUCTURAL TIE BETWEEN THE GOODS AND THE TAX ON THEM.
--
-- WHAT HOLE THIS CLOSES
-- ---------------------
-- Before this migration the ONLY cross-check on a taxed voucher was
-- ledger.deriveAndVerifyGST, which verifies the three TAX-ledger amounts
-- (CGST/SGST/IGST) against the pure engine -- and nothing at all verified the
-- TAXABLE BASE those amounts were derived from. A client could therefore post
-- 100 units of stock against a Rs 1 invoice: the movement ledger would record
-- goods worth 100 x rate leaving the warehouse, the GST lines would record a
-- taxable value of 1, the derived tax would agree with the posted tax (both
-- computed from the same wrong 1), the voucher would balance, and every layer
-- would report success. The books and the stock would then disagree
-- permanently, with no error anywhere and nothing in the audit log to show it.
--
-- Slice INV-G makes the server GENERATE the GST lines and the accounting
-- postings FROM the stock lines, so the two CANNOT disagree by construction.
-- This trigger is the backstop for everything that is not that Go code: a raw
-- SQL insert, a future importer, a migration, a bug in the generator itself.
-- The application layer is where the friendly error lives; THIS is where the
-- invariant lives.
--
-- WHY THE TIE IS CONDITIONAL ON BOTH SIDES EXISTING
-- -------------------------------------------------
-- Three perfectly ordinary documents exist in this schema:
--
--   * movements AND gst lines -> an ITEM INVOICE. The taxed base IS the goods,
--     so the two totals must be equal. That is the case enforced here.
--   * movements, NO gst lines -> a stock-only document: a Delivery Note, a
--     stock journal, a physical-stock adjustment, or a plain purchase entered
--     without GST parameters. There is no taxable base to tie to and demanding
--     one would refuse every such voucher.
--   * gst lines, NO movements  -> a SERVICE invoice. Nothing left the warehouse
--     and nothing should have.
--
-- A blanket equality would break the middle case (and every stock test that
-- predates this migration); requiring only that they agree WHEN BOTH ARE
-- PRESENT is the invariant that is actually true of the domain.
--
-- WHY DEFERRABLE INITIALLY DEFERRED
-- ---------------------------------
-- A multi-line invoice is only consistent at COMMIT. ledger.post() writes the
-- GST lines first and then the movements one row at a time, so after movement
-- line 1 of a 3-line invoice the stock total is a third of the taxed base --
-- an intermediate state that is WRONG in every multi-line invoice and RIGHT in
-- none. Checked per-statement, the constraint would reject every legitimate
-- invoice with more than one line while accepting single-line ones, which is
-- the most confusing possible failure mode. Checked at COMMIT, the whole
-- voucher version is visible and only the final totals matter -- the same
-- argument that makes posting_balanced and stock_movement_not_negative
-- deferred.
--
-- WHY THE TRIGGER IS ON BOTH TABLES
-- ---------------------------------
-- Deferral makes the write ORDER irrelevant within one transaction, but not
-- across transactions. Firing only on stock_movement would leave a hole: a
-- second transaction that appended voucher_gst_line rows to a version whose
-- movements were already committed would never be checked. Firing on both
-- means whichever side is written last brings the pair under the constraint.
-- One function serves both because both tables carry company_id and
-- voucher_version_id, which is the entire key the check needs.

-- ---------------------------------------------------------------------------
-- check_item_invoice_tie: SUM(stock_movement.amount) must equal
-- SUM(voucher_gst_line.taxable_value) for a voucher VERSION that has both.
--
-- Keyed on voucher_version_id, NOT voucher_id: movements and GST lines are both
-- immutable children of a VERSION. An ALTER appends a new version and restates
-- both sides under it, so summing per voucher would add the superseded version's
-- goods to the new version's tax and refuse every legitimate edit. (This is the
-- same versioning trap that forces the negative-stock trigger to join
-- voucher_head.current_version_id -- here the version key sidesteps it entirely,
-- because a version's children are exactly the rows written with it.)
--
-- SECURITY INVOKER (the default), like check_stock_not_negative: the reads run
-- under the caller's RLS with the company GUC already bound by the post
-- transaction, and both predicates are pinned to NEW.company_id anyway, so a
-- mis-scoped session sees zero rows on BOTH sides and the check short-circuits
-- rather than comparing one tenant's goods with another's tax.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION check_item_invoice_tie()
RETURNS TRIGGER AS $$
DECLARE
    v_moves BIGINT;
    v_lines BIGINT;
    v_stock NUMERIC(19,4);
    v_taxed NUMERIC(19,4);
BEGIN
    SELECT COUNT(*), COALESCE(SUM(sm.amount), 0)
      INTO v_moves, v_stock
      FROM stock_movement sm
     WHERE sm.company_id         = NEW.company_id
       AND sm.voucher_version_id = NEW.voucher_version_id;

    SELECT COUNT(*), COALESCE(SUM(gl.taxable_value), 0)
      INTO v_lines, v_taxed
      FROM voucher_gst_line gl
     WHERE gl.company_id         = NEW.company_id
       AND gl.voucher_version_id = NEW.voucher_version_id;

    -- Not an item invoice: a stock-only document or a service invoice. See the
    -- header -- refusing these would refuse a Delivery Note.
    IF v_moves = 0 OR v_lines = 0 THEN
        RETURN NULL;
    END IF;

    IF v_stock <> v_taxed THEN
        RAISE EXCEPTION
            'item invoice tie broken on voucher version %: stock lines total % but the taxed base is %',
            NEW.voucher_version_id, v_stock, v_taxed;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER stock_movement_ties_to_gst
    AFTER INSERT ON stock_movement
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_item_invoice_tie();

CREATE CONSTRAINT TRIGGER voucher_gst_line_ties_to_stock
    AFTER INSERT ON voucher_gst_line
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_item_invoice_tie();

COMMENT ON FUNCTION check_item_invoice_tie() IS
    'Deferred tie for an ITEM INVOICE: when a voucher version carries BOTH '
    'stock movements and GST lines, SUM(stock_movement.amount) must equal '
    'SUM(voucher_gst_line.taxable_value). Stops 100 units of stock being '
    'posted against a Rs 1 invoice by any path, including raw SQL.';
