-- 0050_tds_deduction.sql
-- THE DEDUCTION FACTS — what makes cumulation, the register and the return
-- possible at all.
--
-- Design memo: docs/design/t6-gaps-model.md gap 1, and docs/design/t6-model.md.
-- Migration number from PLAN-TALLY-PARITY.md §2.5, which reserves 0049-0050 for
-- T6 TDS/TCS persistence. 0049 took liability and declarations; this is the
-- second and last of that block.
--
-- Until now the engine PERSISTED NOTHING: ledger.go derived a deduction and
-- verified the posted leg, but no schema recorded the section, the deductee or
-- the amount, so no register and no return could see it. That is the disclosed
-- deferral in internal/ledger/tds.go's header, and it is what made §3.1 gap 1
-- unfixable rather than merely unfixed.
--
-- ===========================================================================
-- ⚠️ A ZERO-AMOUNT ROW IS A FACT, NOT AN ABSENCE. Read this before adding a
-- WHERE tds_amount_rupees > 0 anywhere.
-- ===========================================================================
-- This table records EVERY payment of a nature liable to TDS, whether or not
-- tax was actually deducted on it. Two independent reasons, and each alone
-- would be sufficient:
--
--   1. CUMULATION. s.393(1)(a) charges tax "on the entire amount of such income
--      or sum, where the amount OR AGGREGATE OF AMOUNTS exceeds the threshold
--      limit". The aggregate is of amounts PAID, not of amounts deducted upon.
--      Twenty Rs 20,000 contractor bills deduct nothing individually and it is
--      precisely those twenty rows that carry the aggregate past Rs 1,00,000.
--      Storing only the deductions would make the aggregate permanently zero and
--      re-create the bug this table exists to fix.
--
--   2. THE RETURN. Annexure 6 of the Form 140 file format defines flag `Y` as
--      "payment below the threshold limit" -- a sub-threshold payment is
--      REPORTED, with a reason flag, not omitted. A deductee-level annexure
--      built from non-zero rows only would file an incomplete quarter.
--
-- ===========================================================================
-- THE AGGREGATE IS DERIVED, NEVER STORED
-- ===========================================================================
-- There is no running-total column and there must not be. A counter that can
-- drift from the rows it summarises is the classic form of this bug: it is
-- correct on the day it is written and wrong after the first deleted voucher,
-- back-dated entry or altered amount, with nothing to compare it against.
--
-- The aggregate is SUM(taxable_base_rupees) over
-- (company_id, deductee_ledger_id, section, tax_year_start), and the index
-- below is that access path. Invariant V-4 of the memo.
--
-- ===========================================================================
-- WHY THE KEY IS (company_id, voucher_id, section, leg_seq)
-- ===========================================================================
-- voucher_id -- NOT voucher_version_id -- is the identity that survives an
-- ALTER, exactly as bank_reconciliation records in 0045. A deduction keyed on
-- the version would be orphaned by the first correction and the aggregate would
-- silently change.
--
-- BUT UNLIKE A BANK DATE, THIS IS A FACT ABOUT THE VOUCHER, not about a third
-- party asserting something later. 0045's header explicitly contrasts the two.
-- The deduction is derived from the payment inside the same transaction that
-- posts it, so it is written with the voucher and re-derived on alter, rather
-- than surviving alongside it.
--
-- leg_seq disambiguates one voucher carrying TWO deductions of the SAME section
-- against the same deductee, which a consolidated contractor payment does.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration.

CREATE TABLE IF NOT EXISTS tds_deduction (
    id                    UUID PRIMARY KEY,
    company_id            UUID NOT NULL,
    -- The STABLE voucher identity. See the header: never the version id.
    voucher_id            UUID NOT NULL,
    -- Ordinal within (voucher, section), 0-based, for a voucher carrying more
    -- than one deduction of the same nature.
    leg_seq               INTEGER NOT NULL DEFAULT 0,
    -- WHO was paid. The aggregate is per deductee, so this is half the key that
    -- makes cumulation possible.
    deductee_ledger_id    UUID NOT NULL,
    -- The nature-of-payment key, e.g. '194C'. These still name the REPEALED
    -- 1961 Act while the return keys off s.393 Table Sl. Nos.; the four-digit
    -- return_code below is the bridge, and renaming both together is
    -- PLAN-TALLY-PARITY.md open item 10.
    section               TEXT NOT NULL,
    -- IND_HUF or OTHER. Stored because it selects BOTH the rate (194C splits
    -- 1%/2%) and the return code (1023/1024), and because a deductee's type can
    -- be corrected later without silently restating past deductions.
    deductee_type         TEXT NOT NULL,
    -- Calendar year the tax year BEGINS in: 2026 = 2026-27. Same convention as
    -- 0049. The aggregate resets at the tax-year boundary, so this is part of
    -- its key -- a payment on 1 April starts a new cumulation.
    tax_year_start        INTEGER NOT NULL,
    -- The date the sum was credited or paid, whichever is earlier
    -- (s.393(1)(c)). Cumulation is ordered by THIS, not by insertion: a
    -- back-dated bill can retro-trigger a crossing.
    deduction_date        DATE NOT NULL,

    -- The amount paid or credited. THIS is what the aggregate sums, and it is
    -- present even when no tax was deducted. Unit in the name.
    taxable_base_rupees   NUMERIC(19,4) NOT NULL,
    -- The rate actually applied, after any s.397(2)(b)(i) no-PAN override.
    -- Written to the return at 4 dp ("2" becomes "2.0000"), which is what this
    -- scale is for.
    rate_pct              NUMERIC(9,4) NOT NULL,
    -- The tax deducted. ZERO IS LEGITIMATE -- see the header.
    tds_amount_rupees     NUMERIC(19,4) NOT NULL,
    -- The Form 140 Annexure 2 payment code, e.g. '1023'. STORED, not derived
    -- later from the section: 194J-P and 194J-D share statutory limb
    -- 6(iii).D(b) at the same rate and carry codes 1027 and 1028, so a return
    -- builder re-deriving from `section` would file directors' fees as
    -- professional fees. compliance.TDSReturnCode decides it once, at the
    -- moment the amount is decided.
    return_code           TEXT NOT NULL,
    -- Whether a PAN was on record. Drives the s.397(2)(b)(i) rate AND Annexure
    -- 6 flag `C` (higher rate for non-furnishing of PAN).
    pan_available         BOOLEAN NOT NULL,
    -- The effective-dated rate table this was derived under, so a deduction can
    -- be re-derived and checked years later against the law as it then stood.
    ruleset_version       TEXT NOT NULL,

    -- Audit.
    recorded_by           TEXT NOT NULL,
    recorded_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT tds_deduction_one_per_leg
        UNIQUE (company_id, voucher_id, section, leg_seq),
    CONSTRAINT tds_deduction_company_scope UNIQUE (company_id, id),
    -- Composite FKs: a cross-tenant voucher or ledger is refused BY THE FK
    -- itself, not by RLS alone.
    CONSTRAINT tds_deduction_voucher_fk
        FOREIGN KEY (company_id, voucher_id)
        REFERENCES voucher_head (company_id, voucher_id),
    CONSTRAINT tds_deduction_deductee_fk
        FOREIGN KEY (company_id, deductee_ledger_id)
        REFERENCES ledger (company_id, id),

    CONSTRAINT tds_deduction_leg_seq_non_negative CHECK (leg_seq >= 0),
    CONSTRAINT tds_deduction_deductee_type_known
        CHECK (deductee_type IN ('IND_HUF', 'OTHER')),
    -- Magnitudes, never signs. Mirrors posting_amount_check.
    CONSTRAINT tds_deduction_base_non_negative CHECK (taxable_base_rupees >= 0),
    CONSTRAINT tds_deduction_amount_non_negative CHECK (tds_amount_rupees >= 0),
    CONSTRAINT tds_deduction_rate_sane CHECK (rate_pct >= 0 AND rate_pct <= 100),
    -- You cannot deduct more than the sum you are paying.
    CONSTRAINT tds_deduction_amount_within_base
        CHECK (tds_amount_rupees <= taxable_base_rupees),
    -- Annexure 2 codes are four digits. A blank or short code is a return the
    -- department rejects, discovered at filing time rather than here.
    CONSTRAINT tds_deduction_return_code_shape
        CHECK (return_code ~ '^[0-9]{4}$'),
    CONSTRAINT tds_deduction_tax_year_sane
        CHECK (tax_year_start BETWEEN 2025 AND 2200),
    CONSTRAINT tds_deduction_ruleset_present
        CHECK (length(btrim(ruleset_version)) > 0)
);

-- THE CUMULATION ACCESS PATH, and the reason there is no running-total column.
-- The aggregate for a deductee under a section in a tax year is a SUM over this
-- index. deduction_date trails it so "the aggregate as at a date" -- which is
-- what a back-dated entry needs -- is the same index.
CREATE INDEX IF NOT EXISTS tds_deduction_aggregate
    ON tds_deduction (company_id, deductee_ledger_id, section, tax_year_start, deduction_date);

-- The register and the return read a period, so they need a date-leading path.
CREATE INDEX IF NOT EXISTS tds_deduction_period
    ON tds_deduction (company_id, tax_year_start, deduction_date);

COMMENT ON TABLE tds_deduction IS
    'Every payment of a nature liable to TDS, WHETHER OR NOT tax was deducted '
    'on it. A zero tds_amount_rupees row is a FACT, not an absence: the '
    's.393(1)(a) aggregate is of amounts PAID (so sub-threshold payments are '
    'exactly what carries it past the limit), and Annexure 6 flag Y reports a '
    'below-threshold payment in the return. Never filter these out. The '
    'aggregate is DERIVED by SUM over tds_deduction_aggregate and is '
    'deliberately not a stored counter, which would drift from its rows on the '
    'first deleted, altered or back-dated voucher.';

COMMENT ON COLUMN tds_deduction.taxable_base_rupees IS
    'The sum paid or credited, in rupees. This -- not tds_amount_rupees -- is '
    'what the s.393(1) aggregate sums.';

COMMENT ON COLUMN tds_deduction.return_code IS
    'Form 140 Annexure 2 payment code, STORED rather than re-derived from '
    'section. 194J-P and 194J-D share statutory limb 6(iii).D(b) at the same '
    'rate and differ only by code (1027 vs 1028), so a return builder deriving '
    'from the section would file directors'' fees as professional fees.';

COMMENT ON COLUMN tds_deduction.deduction_date IS
    'Date the sum was credited or paid, whichever is earlier (s.393(1)(c)). '
    'Cumulation is ordered by this and NOT by insertion order, so a back-dated '
    'bill can retrospectively trigger a threshold crossing.';

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, policy with BOTH USING
-- and WITH CHECK. USING alone would let a session write a deduction for another
-- company it can never see or correct; WITH CHECK alone would leak every
-- tenant's supplier payments on read. FORCE because migrations run as the table
-- owner and an owner bypasses its own policies unless forced.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    EXECUTE 'ALTER TABLE tds_deduction ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE tds_deduction FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON tds_deduction';
    EXECUTE 'CREATE POLICY tenant_isolation ON tds_deduction '
         || 'USING (company_id = app_current_company_id()) '
         || 'WITH CHECK (company_id = app_current_company_id())';
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON tds_deduction TO tally_app;
