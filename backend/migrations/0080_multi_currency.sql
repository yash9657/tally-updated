-- 0080_multi_currency.sql
-- FEATURE T8: a second currency, and the three values a foreign figure needs.
--
-- The design document is docs/design/t8-multi-currency-model.md. Its measured
-- claims are not repeated here; what IS repeated is every reason a column has
-- the shape it has, because that is the part a later reader cannot re-derive.
--
-- ===========================================================================
-- ⛔⛔ THE INVARIANT THIS WHOLE MIGRATION IS BUILT AROUND
-- ===========================================================================
-- posting.amount IS THE BASE AMOUNT AND STAYS THE BASE AMOUNT. It is summed by
-- check_double_entry() (0002:157), a DEFERRED constraint trigger that refuses a
-- voucher whose debits and credits disagree, and it is the figure the
-- incrementally-maintained ledger_balance table accumulates. Put a foreign
-- amount in it and you break, in one commit, every existing voucher's balance
-- check, the Trial Balance, the Balance Sheet and the P&L.
--
-- So the foreign figures go BESIDE amount, never instead of it. And there is a
-- second, independent reason base cannot be derived from them:
--
--   ⚠️ A MULTI-CURRENCY JOURNAL BALANCES IN BASE AND NEED NOT BALANCE IN ANY
--   FOREIGN CURRENCY. A sale of $1,000 settled partly in EUR does not sum to
--   zero in dollars or in euros. Only the base column is a column in which the
--   double-entry identity is even MEANINGFUL.
--
-- Hence three STORED values -- base, foreign, rate -- and not two plus
-- arithmetic. Every pair of them can be made to agree with a wrong third.
--
-- ===========================================================================
-- ⛔ NULL MEANS BASE. THERE IS NO BACKFILL, AND THERE CANNOT BE ONE.
-- ===========================================================================
-- posting carries posting_no_mutate (0002:150), BEFORE UPDATE OR DELETE, so no
-- existing row can be given a currency retroactively. That is not an obstacle
-- worked around: every voucher already in the books IS base-currency, so
-- absent-means-base is the TRUTHFUL encoding rather than a convenience.
--
-- ⚠️ ADDING A NULLABLE COLUMN IS DDL AND DOES NOT FIRE A ROW-LEVEL BEFORE
-- UPDATE TRIGGER. That was proved on a scratch database WITH A CONTROL (an
-- UPDATE was refused in the same session, so the trigger was demonstrably armed
-- when the ALTER succeeded) before this file was written. Without the control,
-- a trigger that never fired and a trigger that correctly ignores DDL look
-- identical.

-- ===========================================================================
-- 1. currency -- the master
-- ===========================================================================
-- MANY foreign currencies per company, not one. A company that imports from the
-- US and exports to the UAE needs both, and a schema that allows exactly one
-- forces a second company or a lie.
--
-- ⛔⛔ THE COMPANY'S OWN BASE CURRENCY MUST NOT HAVE A ROW HERE, and the trigger
-- below enforces it. This is the direct consequence of NULL-means-base: if an
-- INR-base company could also hold an 'INR' currency master, then "this posting
-- is in rupees" would have TWO encodings -- currency_id IS NULL, and
-- currency_id -> the INR row -- and every report would have to know both.
-- A voucher keyed against such a row would carry rate 1.0 for ever, appear in
-- the unadjusted-forex report as a bill with a permanently zero difference, and
-- be indistinguishable from a genuine foreign bill that happens to be flat.
-- One encoding, enforced, is cheaper than two encodings reconciled everywhere.
CREATE TABLE currency (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    -- The ISO-4217-ish code ('USD'), which is what a rate, a report column and
    -- an operator all name the currency by.
    code          TEXT NOT NULL CHECK (btrim(code) <> ''),
    -- Presentation, mirroring the three fields company already carries for the
    -- base currency (0015): a symbol to render with, a formal name for
    -- documents, and how many decimals this currency actually has.
    symbol        TEXT NOT NULL CHECK (btrim(symbol) <> ''),
    formal_name   TEXT NOT NULL CHECK (btrim(formal_name) <> ''),
    -- ⚠️ BOUNDED AT 4 BECAUSE MONEY IN THIS PRODUCT IS FIXED-4 END TO END --
    -- NUMERIC(19,4) in the database and exact BigInt-scaled fixed-4 arithmetic
    -- in the client. A currency claiming 6 decimals would be one the wire
    -- cannot carry, so it is refused here rather than truncated silently three
    -- layers away. Identical bound and reasoning to company.decimal_places.
    decimal_places INTEGER NOT NULL DEFAULT 2
        CHECK (decimal_places BETWEEN 0 AND 4),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The composite tenant key every FK in this plane points at: RLS hides
    -- another tenant's rows from a SELECT, but a plain REFERENCES would still
    -- accept an id that leaked in through a request body.
    CONSTRAINT currency_company_scope UNIQUE (company_id, id)
);
CREATE INDEX currency_company ON currency (company_id);

-- Migration 0075's rule, and its reasoning applies unchanged: "usd" and "USD"
-- are one currency to a human, and two rows means half the forex report
-- subtotals under each.
CREATE UNIQUE INDEX currency_company_code_normalised
    ON currency (company_id, lower(btrim(code)));

COMMENT ON TABLE currency IS
    'Foreign currencies defined for a company. ⛔ The company''s OWN base '
    'currency is deliberately absent: a base-currency posting is encoded by '
    'currency_id IS NULL, and a base row here would be a second encoding of '
    'the same fact. Enforced by currency_is_not_base().';

CREATE OR REPLACE FUNCTION currency_is_not_base()
RETURNS TRIGGER AS $$
DECLARE
    base TEXT;
BEGIN
    SELECT c.base_currency INTO base FROM company c WHERE c.id = NEW.company_id;
    -- ⛔ A TRIGGER THAT CANNOT SEE ITS COMPANY MUST FAIL LOUDLY, NOT PASS.
    -- company carries RLS (FORCE), so a session with no app.current_company_id
    -- set sees no row at all. Treating "not found" as "not the base currency"
    -- would make this guard silently inert in exactly the situation where the
    -- caller has already lost track of which tenant it is writing as.
    IF NOT FOUND THEN
        RAISE EXCEPTION
            'currency %: company % is not visible from this session, so the '
            'base-currency check could not run', NEW.code, NEW.company_id;
    END IF;
    IF lower(btrim(base)) = lower(btrim(NEW.code)) THEN
        RAISE EXCEPTION
            'currency %: this is the company base currency. A base-currency '
            'amount is recorded with NO currency at all (currency_id IS NULL), '
            'so a master for it would be a second encoding of the same fact',
            NEW.code;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER currency_not_base
    BEFORE INSERT OR UPDATE ON currency
    FOR EACH ROW EXECUTE FUNCTION currency_is_not_base();

-- ===========================================================================
-- 2. currency_rate -- one rate per (currency, effective date)
-- ===========================================================================
-- ⛔⛔ THIS TABLE IS NOT WHAT A VOUCHER READS. A voucher stores its OWN rate
-- (section 3) and that stored rate is the historical truth of that voucher for
-- ever. This table exists for exactly two jobs:
--
--   1. supply a DEFAULT when an operator is keying, and
--   2. supply "the rate as on <date>" to the unadjusted-forex report.
--
-- A LATER RATE MUST NEVER MOVE AN EARLIER VOUCHER. If the ledger joined to this
-- table instead of storing the rate, then entering today's rate would silently
-- restate every foreign voucher ever posted -- the Balance Sheet would move
-- with no voucher altered, no edit log entry, and nothing on any screen to
-- explain it. That is why the join does not exist.
CREATE TABLE currency_rate (
    id             UUID PRIMARY KEY,
    company_id     UUID NOT NULL,
    currency_id    UUID NOT NULL,
    effective_date DATE NOT NULL,
    -- ⛔ NUMERIC(19,10), AND IT IS NOT Money. See section 3's header for why a
    -- rate needs more decimals than every other number in this product.
    rate           NUMERIC(19,10) NOT NULL CHECK (rate > 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT currency_rate_company_scope UNIQUE (company_id, id),
    CONSTRAINT currency_rate_currency_fk
        FOREIGN KEY (company_id, currency_id)
        REFERENCES currency (company_id, id)
);

-- One rate per currency per day. A second row for the same date is not extra
-- information, it is an ambiguity: "the rate as on 3 March" would have two
-- answers and the report would silently pick whichever the planner returned
-- first.
CREATE UNIQUE INDEX currency_rate_one_per_day
    ON currency_rate (company_id, currency_id, effective_date);

COMMENT ON TABLE currency_rate IS
    'Exchange rates by effective date, quoted as BASE UNITS PER ONE FOREIGN '
    'UNIT. ⛔ Supplies DEFAULTS and the revaluation report only -- a voucher '
    'stores its own rate, so a later rate never restates an earlier voucher.';

-- ===========================================================================
-- 3. The three values, on all THREE tables that carry a bill or a posting
-- ===========================================================================
-- ⛔⛔ WHY ledger_opening_bill IS IN THIS LIST, AND WHY OMITTING IT WOULD BE A
-- SILENT DATA DEFECT RATHER THAN A MISSING FEATURE:
--
-- Outstandings does not read bill_allocation alone. It nets each
-- (ledger, ref_name) across a UNION of bill_allocation AND ledger_opening_bill
-- (internal/reporting/outstanding.go). Give the foreign columns to
-- bill_allocation only, and a foreign party's OPENING bill -- the balance
-- brought in at migration time, which for a new customer IS their whole
-- history -- stays base-only, then nets into the same ref_name group as the
-- foreign allocations that settle it. The rupee total stays correct while the
-- foreign column shows a partial figure, and nothing on the report disagrees.
--
-- ⚠️ THE RATE IS QUOTED AS BASE UNITS PER ONE FOREIGN UNIT (1 USD = 83.65 INR),
-- NOT THE INVERSE, and the direction is a decision rather than a habit:
-- quoting foreign-per-base makes a strong currency's rate a small number
-- (1 INR = 0.0120 USD) where the trailing digits ARE the precision, and at 4dp
-- a fifty-lakh invoice moves by thousands.
--
-- ⛔ AND 10 DECIMALS IS DELIBERATELY GENEROUS. 6 would still be wrong for a
-- weak currency, where even the base-per-foreign quote is small: 1 IDR
-- = 0.003333... INR carries a relative error of ~1.5e-4 at 6dp. The extra
-- digits cost nothing and the failure they prevent is silent.
--
-- ⛔⛔ A RATE IS THEREFORE *NOT* THE Money TYPE, and the client must not reach
-- for parseFixed4 to read one: that function TRUNCATES the fifth decimal
-- onward (screens/vouchers/itemInvoice.ts), so a rate put through it silently
-- becomes a different rate and every figure derived from it is plausible.

-- --- posting ---------------------------------------------------------------
ALTER TABLE posting
    ADD COLUMN currency_id    UUID NULL,
    ADD COLUMN foreign_amount NUMERIC(19,4) NULL CHECK (foreign_amount >= 0),
    ADD COLUMN rate           NUMERIC(19,10) NULL CHECK (rate > 0),
    ADD CONSTRAINT posting_currency_fk
        FOREIGN KEY (company_id, currency_id)
        REFERENCES currency (company_id, id),
    -- ⛔ THE "THREE STORED VALUES" RULE, MADE A CONSTRAINT. Two-of-three is the
    -- state in which a report can compute a plausible wrong number: a foreign
    -- amount with no rate cannot be checked against base, and a rate with no
    -- foreign amount describes nothing. The database refuses the half-state
    -- rather than leaving every reader to detect it.
    ADD CONSTRAINT posting_forex_all_or_none CHECK (
           (currency_id IS NULL AND foreign_amount IS NULL AND rate IS NULL)
        OR (currency_id IS NOT NULL AND foreign_amount IS NOT NULL AND rate IS NOT NULL));

-- Partial: only foreign postings are ever selected by currency, and on a
-- single-currency company (which is every company today) the index is empty.
CREATE INDEX posting_currency ON posting (company_id, currency_id)
    WHERE currency_id IS NOT NULL;

COMMENT ON COLUMN posting.foreign_amount IS
    'The amount in currency_id, or NULL for a base-currency posting. ⛔ NEVER '
    'authoritative: posting.amount is the base figure check_double_entry() '
    'sums and ledger_balance accumulates.';
COMMENT ON COLUMN posting.rate IS
    'Base units per ONE foreign unit, as at THIS voucher. ⛔ Historical and '
    'immutable: never re-read from currency_rate, which would restate posted '
    'vouchers when a later rate is entered.';

-- --- bill_allocation -------------------------------------------------------
-- ⭐ THIS IS WHAT MAKES REALISED GAIN/LOSS COMPUTABLE AT ALL. The difference
-- arises on the SETTLEMENT voucher, and it is the settlement rate measured
-- against THE BILL'S OWN rate -- so the NEW ref row must have carried its rate
-- since the day the bill was raised. A schema that stored the rate only on the
-- posting could not answer "what rate was this bill raised at?" for a voucher
-- with several bills on one party line.
ALTER TABLE bill_allocation
    ADD COLUMN currency_id    UUID NULL,
    ADD COLUMN foreign_amount NUMERIC(19,4) NULL CHECK (foreign_amount >= 0),
    ADD COLUMN rate           NUMERIC(19,10) NULL CHECK (rate > 0),
    ADD CONSTRAINT bill_allocation_currency_fk
        FOREIGN KEY (company_id, currency_id)
        REFERENCES currency (company_id, id),
    ADD CONSTRAINT bill_allocation_forex_all_or_none CHECK (
           (currency_id IS NULL AND foreign_amount IS NULL AND rate IS NULL)
        OR (currency_id IS NOT NULL AND foreign_amount IS NOT NULL AND rate IS NOT NULL));

CREATE INDEX bill_allocation_currency ON bill_allocation (company_id, currency_id)
    WHERE currency_id IS NOT NULL;

-- --- ledger_opening_bill ---------------------------------------------------
ALTER TABLE ledger_opening_bill
    ADD COLUMN currency_id    UUID NULL,
    ADD COLUMN foreign_amount NUMERIC(19,4) NULL CHECK (foreign_amount >= 0),
    ADD COLUMN rate           NUMERIC(19,10) NULL CHECK (rate > 0),
    ADD CONSTRAINT ledger_opening_bill_currency_fk
        FOREIGN KEY (company_id, currency_id)
        REFERENCES currency (company_id, id),
    ADD CONSTRAINT ledger_opening_bill_forex_all_or_none CHECK (
           (currency_id IS NULL AND foreign_amount IS NULL AND rate IS NULL)
        OR (currency_id IS NOT NULL AND foreign_amount IS NOT NULL AND rate IS NOT NULL));

COMMENT ON COLUMN ledger_opening_bill.foreign_amount IS
    'The opening bill''s foreign amount. ⛔ Present because Outstandings nets '
    'this table in a UNION with bill_allocation: base-only opening bills would '
    'net into the same ref_name group as foreign allocations and show a '
    'partial foreign figure that no total on the report contradicts.';

-- ===========================================================================
-- 4. Where realised gain/loss is posted -- CHOSEN, never invented
-- ===========================================================================
-- ⛔ NOTHING IS AUTO-CREATED. A settlement that would realise a difference on a
-- company with no ledger chosen here is REFUSED, naming this setting. The two
-- alternatives were both worse:
--
--   * seeding a "Forex Gain/Loss" ledger on enable writes a master the operator
--     never asked for, into a chart of accounts that may already have one under
--     a different name -- and then the books have two;
--   * posting to a guessed ledger, or absorbing the difference into the party
--     balance, produces a Trial Balance that still balances and a debtor figure
--     that is quietly wrong. Silent absorption is the failure mode this whole
--     track exists to remove.
--
-- ⛔⛔ WHY THIS IS ITS OWN TABLE AND NOT A COLUMN ON `company` -- FOUND BY A
-- CENSUS, NOT BY TASTE. The first draft of this migration put
-- forex_gain_loss_ledger_id directly on `company`. That compiled, migrated
-- cleanly, and broke internal/platform/tenant's delete-order census with:
--
--     tenant: foreign-key cycle among {account_group, ledger}: no order exists
--     that removes children first
--
-- The cycle is real and it is a LAYERING INVERSION, not a graph technicality:
--
--     company -> ledger -> account_group -> (company_id) -> company
--
-- `company` is the tenant ROOT. Every scoped table is its child, so the moment
-- the root points back at one of its own descendants there is no order in which
-- a tenant can be torn down -- which breaks backup/restore and every test
-- harness that mints and removes a company. ⚠️ A settings row is a CHILD of the
-- company and may point wherever it likes; the root may not.
--
-- ⭐ THE CENSUS IS WHY THIS WAS CAUGHT AT ALL, and it is worth saying plainly:
-- nothing about the column version was wrong at the SQL level. It is exactly
-- the class of defect that ships silently and surfaces later as "tenant delete
-- sometimes fails".
--
-- Shape mirrors company_tds_profile (0070): the key IS the company, one row.
CREATE TABLE company_forex_settings (
    company_id                UUID PRIMARY KEY,
    -- ⚠️ NULLABLE ON PURPOSE. The row may exist to hold a future setting while
    -- no gain/loss ledger has been chosen yet, and "not chosen" must stay
    -- distinguishable from "chosen". A settlement that would realise a
    -- difference with this NULL is REFUSED, naming this setting.
    forex_gain_loss_ledger_id UUID NULL,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT company_forex_settings_ledger_fk
        FOREIGN KEY (company_id, forex_gain_loss_ledger_id)
        REFERENCES ledger (company_id, id)
);

COMMENT ON TABLE company_forex_settings IS
    'Per-company forex settings. ⛔ A TABLE rather than columns on `company` '
    'because the tenant root must not reference its own descendants: '
    'company -> ledger -> account_group -> company is a delete-order cycle and '
    'no tenant could then be torn down.';
COMMENT ON COLUMN company_forex_settings.forex_gain_loss_ledger_id IS
    'The ledger realised exchange differences are posted to. ⛔ Chosen by the '
    'operator and never auto-created; a settlement that would realise a '
    'difference with this unset is REFUSED rather than posted somewhere '
    'plausible.';

-- ⚠️ PostgreSQL MATCH SIMPLE does not enforce a multi-column FK when any column
-- is NULL, so an unset choice is simply unconstrained -- exactly the intent --
-- while a ledger id belonging to ANOTHER company is refused outright rather
-- than merely hidden by RLS.

ALTER TABLE company_forex_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE company_forex_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON company_forex_settings
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
GRANT SELECT, INSERT, UPDATE, DELETE ON company_forex_settings TO tally_app;

-- ⛔⛔ THERE IS DELIBERATELY NO `forex_enabled` FLAG, and that is a decision.
-- The presence of a currency master IS the enablement: a company with no
-- currency rows cannot key a foreign voucher, because every foreign column is
-- FK-bound to one. A boolean beside that would be a SECOND source of truth,
-- free to be off while currencies exist or on while none do, and every screen
-- would then have to decide which of the two it believed. F11 reports the
-- count. This also honours FeaturesScreen's own doctrine -- a toggle that
-- flips a flag with nothing behind it is worse than no toggle at all.

-- ===========================================================================
-- 5. Row-level security for the two new tenant tables
-- ===========================================================================
-- Default-deny, ENABLE + FORCE, USING + WITH CHECK pinned to the total
-- app_current_company_id() resolver -- identical to every tenant table since
-- 0004.
--
-- ⛔ THE POLICY NAME `tenant_isolation` IS LOAD-BEARING, NOT DECORATIVE:
-- internal/security's TestEveryTenantTableHasIsolationPolicy enumerates every
-- table carrying a company_id and asserts a policy OF THAT NAME exists on it. A
-- correctly-written policy under any other name is invisible to that census.
ALTER TABLE currency ENABLE ROW LEVEL SECURITY;
ALTER TABLE currency FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON currency
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
GRANT SELECT, INSERT, UPDATE, DELETE ON currency TO tally_app;

ALTER TABLE currency_rate ENABLE ROW LEVEL SECURITY;
ALTER TABLE currency_rate FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON currency_rate
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
GRANT SELECT, INSERT, UPDATE, DELETE ON currency_rate TO tally_app;
