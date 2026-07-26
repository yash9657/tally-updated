-- 0015_company_profile_and_currency.sql
-- Company master fields that Tally's company-creation screen has had since
-- forever and this schema never modelled: the mailing block, the country, the
-- BOOKS-BEGINNING-FROM date, and the base currency as three separate fields
-- rather than a bare 'INR' string.
--
-- WHY books_beginning_from IS A SEPARATE COLUMN (the load-bearing one)
-- -------------------------------------------------------------------
-- Tally distinguishes two dates that are equal only in the easy case:
--
--   fy_start_date        — when the FINANCIAL YEAR begins (1 April in India).
--                          Fixes the reporting year, the voucher-numbering
--                          series restart, and every "current period" default.
--   books_beginning_from — when THIS COMPANY'S BOOKS begin in this system.
--
-- They differ whenever a business migrates mid-year: a company that switches
-- over on 1 October still has a financial year starting 1 April, but its books
-- here begin on 1 October, and its opening balances are stated AS AT that date
-- (carrying in the six months already recorded elsewhere). Collapsing the two
-- makes that migration unrepresentable and silently mis-dates opening balances.
--
-- CHECK (books_beginning_from >= fy_start_date) encodes the real invariant:
-- books may begin on or after the year opens, never before it.
--
-- All columns are NOT NULL. Every one except books_beginning_from carries a
-- literal column DEFAULT, so existing rows and any caller that omits them are
-- unaffected. books_beginning_from CANNOT use a column DEFAULT because its
-- correct value is another column of the same row (fy_start_date), so a BEFORE
-- INSERT/UPDATE trigger supplies it instead -- see company_default_books_beginning
-- below. That keeps raw `INSERT INTO company (...)` statements (test fixtures,
-- imports, the sync path) working unchanged while making "books begin when the
-- year does, unless told otherwise" a property of the SCHEMA rather than of one
-- Go constructor that callers can bypass.
--
-- The company table is already RLS-protected (company_self_scope + tenant
-- policy), so new columns inherit it and no policy or grant changes are needed.

-- ---------------------------------------------------------------------------
-- Mailing block + country.
-- ---------------------------------------------------------------------------
ALTER TABLE company
    ADD COLUMN mailing_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN address      TEXT NOT NULL DEFAULT '',
    ADD COLUMN city         TEXT NOT NULL DEFAULT '',
    ADD COLUMN pincode      TEXT NOT NULL DEFAULT '',
    ADD COLUMN country      TEXT NOT NULL DEFAULT 'India';

COMMENT ON COLUMN company.mailing_name IS
    'The name printed on invoices and statutory documents. Tally keeps this '
    'separate from the company name so a trading name can differ from the '
    'registered one; empty means "use name".';
COMMENT ON COLUMN company.address IS
    'Registered / principal place of business, as a newline-separated block '
    '(Tally treats the address as multi-line free text, not parsed fields).';

-- ---------------------------------------------------------------------------
-- Books beginning from. Added nullable, backfilled from fy_start_date, then
-- made NOT NULL — so existing companies land on the correct, meaningful value
-- (their books began when their year did) rather than on an arbitrary default.
-- ---------------------------------------------------------------------------
ALTER TABLE company ADD COLUMN books_beginning_from DATE NULL;

UPDATE company SET books_beginning_from = fy_start_date
 WHERE books_beginning_from IS NULL;

-- Default-from-a-sibling-column. A literal DEFAULT cannot reference another
-- column, so this BEFORE trigger fills the value when the caller omits it. It
-- runs before the NOT NULL check (Postgres evaluates constraints against the
-- final NEW row), so a plain
--     INSERT INTO company (id, company_id, name, fy_start_date) VALUES (...)
-- still succeeds and lands with books_beginning_from = fy_start_date.
CREATE OR REPLACE FUNCTION company_default_books_beginning()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.books_beginning_from IS NULL THEN
        NEW.books_beginning_from := NEW.fy_start_date;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER company_books_beginning_default
    BEFORE INSERT OR UPDATE ON company
    FOR EACH ROW EXECUTE FUNCTION company_default_books_beginning();

ALTER TABLE company
    ALTER COLUMN books_beginning_from SET NOT NULL;

ALTER TABLE company
    ADD CONSTRAINT company_books_begin_within_fy
    CHECK (books_beginning_from >= fy_start_date);

COMMENT ON COLUMN company.books_beginning_from IS
    'The date this company''s books begin in this system, and therefore the '
    'date every opening balance is stated AS AT. Equals fy_start_date for a '
    'company that starts fresh at the top of its financial year; LATER for a '
    'mid-year migration. Never earlier — see company_books_begin_within_fy.';

-- ---------------------------------------------------------------------------
-- Base currency as separate fields, mirroring Tally's currency screen.
-- base_currency (the ISO-ish code) is retained and unchanged; these describe
-- how amounts are RENDERED.
-- ---------------------------------------------------------------------------
ALTER TABLE company
    ADD COLUMN currency_symbol      TEXT NOT NULL DEFAULT '₹',
    ADD COLUMN currency_formal_name TEXT NOT NULL DEFAULT 'Indian Rupees',
    ADD COLUMN decimal_places       INTEGER NOT NULL DEFAULT 2
        CHECK (decimal_places BETWEEN 0 AND 4);

COMMENT ON COLUMN company.decimal_places IS
    'Digits shown after the decimal point. DISPLAY ONLY — money is stored and '
    'computed at NUMERIC(19,4) everywhere regardless of this value, so lowering '
    'it never loses stored precision.';
