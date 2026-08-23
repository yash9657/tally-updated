-- 0075 — a master's name is unique CASE-INSENSITIVELY and IGNORING SURROUNDING
-- WHITESPACE, for ledgers, stock items and units.
--
-- REGISTER ROW D90, whose premise was wrong in both halves and whose
-- CONSEQUENCE was right. D90 said "a duplicate STOCK ITEM name is accepted while
-- a duplicate LEDGER name is refused". Measured on the wire 2026-08-22 against a
-- binary verified fresh by symbol:
--
--   POST the same stock item name twice  -> 201, then 422 "already has a stock
--                                           item named ..."
--   POST the same ledger name twice      -> 201, then 422
--
-- Both are refused. There is no asymmetry, and `UNIQUE (company_id, name)` has
-- been on `stock_item` since migration 0005. The D84 measurement that produced
-- D90 was simply wrong, and the recon it "overturned" had been right.
--
-- ⛔ BUT THE CONSEQUENCE D90 DESCRIBED IS REAL, VIA A MECHANISM NOBODY HAD
-- MEASURED. Uniqueness is BYTE-EXACT, so these all coexist today:
--
--   'Acme Traders'   'acme traders'   'Acme Traders '
--
-- Three masters, indistinguishable in every picker, and the damage is exactly
-- what D90 predicted for its own (non-existent) mechanism:
--
--   * a PARTY keyed twice with different capitalisation splits one supplier's
--     outstandings across two accounts, and neither ages correctly;
--   * a STOCK ITEM keyed twice splits its movements and its valuation, and each
--     copy is weighted-average costed on half the history;
--   * a UNIT keyed twice gives the item form two 'Nos' to choose between.
--
-- The ledger case is the worst of the three and was not in D90 at all.
--
-- ⛔ WHY AN EXPRESSION INDEX AND NOT A CITEXT COLUMN OR A NORMALISING TRIGGER.
--   * citext would change the column's type and every comparison in the product
--     with it, including ones that SHOULD stay exact.
--   * a trigger that rewrote the operator's capitalisation would silently
--     discard what they typed; 'HDFC Bank' must still PRINT as 'HDFC Bank'.
-- An expression index changes what COLLIDES and nothing about what is STORED,
-- which is the whole of the intent: the display name is the operator's, the
-- identity is normalised.
--
-- ⚠️ btrim, NOT a full whitespace fold. 'Acme  Traders' (two inner spaces) stays
-- distinct from 'Acme Traders'. That is deliberate: an inner double space is
-- VISIBLE in a picker, whereas a trailing space and a capital letter are not,
-- and this index is aimed at the collisions an operator cannot see.
--
-- ⚠️ SAFE ON EXISTING DATA, VERIFIED BEFORE WRITING THIS. The dev database's
-- 208 companies / 3,782 ledgers / 211 stock items / 301 units contain ZERO
-- groups that collide under lower(btrim(...)). The query that proved it is the
-- one this migration would fail on, and it is repeated below so a deployer can
-- run it against their own data first.
--
--   SELECT company_id, lower(btrim(name)), count(*) FROM ledger
--    GROUP BY 1,2 HAVING count(*) > 1;
--
-- ⛔ THE OLD CONSTRAINTS ARE DROPPED, NOT KEPT ALONGSIDE. A byte-exact unique
-- constraint is strictly IMPLIED by the normalised one — anything it would
-- refuse, the normalised index refuses too — so keeping both leaves a second
-- constraint name that can surface in an error message and a second index to
-- maintain on every insert, for no additional guarantee.
--
-- ⚠️ T10 (stock groups) WIDENS THE STOCK-ITEM INDEX rather than replacing it:
--   (company_id, group_id, lower(btrim(name)))
-- That admits rows this one refuses and can never fail on data that satisfies
-- this one, which is why refusing now is cheap and un-splitting a year of
-- half-costed valuation history would not be.

-- ⚠️ NO EXPLICIT BEGIN/COMMIT. migrations.go already runs each file inside its
-- own transaction (`conn.Begin` / `tx.Commit`), so a BEGIN here would nest and
-- the COMMIT would end the RUNNER's transaction early. None of the other 58
-- migrations carries one.

-- ---------------------------------------------------------------------------
-- LEDGER
-- ---------------------------------------------------------------------------
ALTER TABLE ledger DROP CONSTRAINT IF EXISTS ledger_company_id_name_key;

CREATE UNIQUE INDEX IF NOT EXISTS ledger_company_name_normalised
    ON ledger (company_id, lower(btrim(name)));

COMMENT ON INDEX ledger_company_name_normalised IS
    'D90: a ledger name is unique case-insensitively and ignoring surrounding '
    'whitespace. Two parties differing only in capitalisation split one '
    'supplier''s outstandings across two accounts, and nothing on screen '
    'distinguishes them. The STORED name keeps the operator''s capitalisation.';

-- ---------------------------------------------------------------------------
-- STOCK ITEM
-- ---------------------------------------------------------------------------
ALTER TABLE stock_item DROP CONSTRAINT IF EXISTS stock_item_company_id_name_key;

CREATE UNIQUE INDEX IF NOT EXISTS stock_item_company_name_normalised
    ON stock_item (company_id, lower(btrim(name)));

COMMENT ON INDEX stock_item_company_name_normalised IS
    'D90: a stock item name is unique case-insensitively and ignoring '
    'surrounding whitespace. Two items differing only in capitalisation split '
    'the item''s movements and its weighted-average valuation, each copy costed '
    'on half the history. T10 widens this to include group_id.';

-- ---------------------------------------------------------------------------
-- UNIT
-- ---------------------------------------------------------------------------
ALTER TABLE unit DROP CONSTRAINT IF EXISTS unit_company_id_symbol_key;

CREATE UNIQUE INDEX IF NOT EXISTS unit_company_symbol_normalised
    ON unit (company_id, lower(btrim(symbol)));

COMMENT ON INDEX unit_company_symbol_normalised IS
    'D90: a unit symbol is unique case-insensitively and ignoring surrounding '
    'whitespace, so an item form cannot offer two indistinguishable "Nos".';
