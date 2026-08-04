-- 0069_tds_challan.sql
-- WHAT TAX WAS ACTUALLY DEPOSITED -- and which deductions each deposit covers.
--
-- Design memo: docs/design/t6-model.md §1.5 and §2.2 (every row of which reads
-- "MIGRATION"). Migration number from PLAN-TALLY-PARITY.md §2.5, which records
-- 0068 as the last consumed number.
--
-- ===========================================================================
-- WHY THIS IS NOT THE THIRD THING, THE WAY THE PLAN SAID
-- ===========================================================================
-- HANDOFF-NEXT-THREE.md ranked the challan register third of three, after the
-- return file, on the reasoning that the return would "tell you the shape this
-- must store". The first half of that was right and the ordering was wrong:
-- reading the prescribed file format showed that DEDUCTEE DETAIL FIELD 5 IS A
-- CHALLAN REFERENCE. Every deductee row of the return points at the challan
-- that paid its tax, so no deductee row can exist outside a challan. This table
-- is a PREREQUISITE for the return, not a companion to it.
--
-- It is also the answer to the register's loudest disclosure: everything T6 has
-- stored so far is tax DEDUCTED, and it is the DEPOSIT that stops interest
-- running under s.396.
--
-- ===========================================================================
-- ⛔ A NIL CHALLAN IS NOT STORED HERE, AND THAT IS DELIBERATE
-- ===========================================================================
-- The file format has a "NIL Challan Indicator" and General Note 11 requires
-- deductee records under a nil challan to carry a reason flag. It is tempting
-- to model that as a row with every amount zero.
--
-- IT IS NOT A DEPOSIT AND MUST NOT BE STORED AS ONE. A nil challan is a RETURN
-- ARTEFACT: it is how the format carries deductee rows that have no tax and
-- therefore no payment. It is derivable in full from the deductions themselves
-- (they are the ones with tds_amount_rupees = 0), its date is fixed by the
-- format to the last day of the quarter, and it corresponds to no event that
-- ever happened. Storing one would put a fiction in the same table as facts,
-- and the first report to SUM this table would count it.
--
-- So: this table holds REAL DEPOSITS ONLY -- enforced by
-- tds_challan_something_was_deposited -- and internal/compliance/form140
-- synthesises the nil challan at emission time.
--
-- ===========================================================================
-- THE CIN IS THE NATURAL KEY, AND DOUBLE-COUNTING IS WHAT IT PREVENTS
-- ===========================================================================
-- A challan is identified to the department by its CIN: BSR code of the
-- receiving branch + date of deposit + the bank's challan serial number. Two
-- rows carrying the same CIN are the same real-world payment entered twice,
-- and the damage is specific: the return's batch total is the sum of the
-- challan deposits, so a duplicate over-claims a deposit that the FVU will then
-- fail against the .csi file -- discovered at the portal, not here.
--
-- ===========================================================================
-- ⚠️ WHOLE RUPEES. READ THIS BEFORE RELAXING A CHECK.
-- ===========================================================================
-- The format is explicit for all five challan amount fields: "No fractional
-- portion is allowed in this field (value should be integer), i.e. value
-- '1000.50' will not be allowed, whereas value '1000.00' will be considered to
-- be valid value." The columns stay NUMERIC(19,4) so they are the same type and
-- scale as every other money column in this schema -- the constraint is on the
-- VALUE, not on the type, because a NUMERIC(19,0) here would make every join
-- and comparison against tds_deduction a cast.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration.

CREATE TABLE IF NOT EXISTS tds_challan (
    id                  UUID PRIMARY KEY,
    company_id          UUID NOT NULL,

    -- ---- The CIN: what identifies this payment to the department. ----------
    -- Seven digits. For a Government deductor paying by book entry this is the
    -- Form 24G receipt number issued by the AO instead, which is also seven
    -- digits -- the format overloads one field for both, so this column does
    -- too, and by_book_entry says which it is.
    bsr_code            TEXT NOT NULL,
    -- The date tax was PAID to the Government. Not the deduction date, and not
    -- the voucher date.
    deposit_date        DATE NOT NULL,
    -- The bank's challan serial number, or the DDO serial number under book
    -- entry. TEXT, not INTEGER: leading zeros are significant in an identifier
    -- and strconv would eat them.
    challan_no          TEXT NOT NULL,
    -- Selects the format's "Mode of payment" field: book adjustment (B) for a
    -- transfer voucher, bank challan (C) otherwise. A boolean rather than the
    -- letter because the two are exhaustive and a third mode is not something
    -- the format admits.
    by_book_entry       BOOLEAN NOT NULL DEFAULT FALSE,

    -- ---- The deposit, split the way the challan splits it. -----------------
    -- These four are SEPARATE FACTS, not a total with a breakdown: only
    -- tax_rupees discharges the deduction liability. Interest under s.396, the
    -- s.234E-equivalent late-filing fee and any penalty are paid on the same
    -- challan and are not tax. A single "amount" column would make it
    -- impossible to answer "how much of what I paid was actually tax", which
    -- is the question the annexure reconciliation asks.
    tax_rupees          NUMERIC(19,4) NOT NULL DEFAULT 0,
    interest_rupees     NUMERIC(19,4) NOT NULL DEFAULT 0,
    fee_rupees          NUMERIC(19,4) NOT NULL DEFAULT 0,
    penalty_rupees      NUMERIC(19,4) NOT NULL DEFAULT 0,

    -- Annexure 7. 200 is tax the deductor pays over of its own motion, 400 is a
    -- demand raised on assessment, 100 is advance tax. NOT DEFAULTED to 200:
    -- choosing it would assert that a deposit was voluntary rather than
    -- demanded, which is a statement about the company's assessment history
    -- that this software has no business making on its behalf.
    minor_head          TEXT NOT NULL,

    -- The tax year the deposit belongs to (2026 = 2026-27), same convention as
    -- 0049 and 0050. Stored rather than derived from deposit_date: a challan
    -- paid on 7 April can be for March of the previous year, and the deposit
    -- date alone cannot tell you which.
    tax_year_start      INTEGER NOT NULL,

    recorded_by         TEXT NOT NULL,
    recorded_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The department's own identity for a payment. See the header.
    CONSTRAINT tds_challan_cin_unique
        UNIQUE (company_id, bsr_code, deposit_date, challan_no),
    CONSTRAINT tds_challan_company_scope UNIQUE (company_id, id),

    CONSTRAINT tds_challan_bsr_shape CHECK (bsr_code ~ '^[0-9]{7}$'),
    -- The format sizes this field at 5.
    CONSTRAINT tds_challan_no_shape CHECK (challan_no ~ '^[0-9]{1,5}$'),
    CONSTRAINT tds_challan_minor_head_known
        CHECK (minor_head IN ('200', '400', '100')),
    CONSTRAINT tds_challan_tax_year_sane
        CHECK (tax_year_start BETWEEN 2025 AND 2200),

    -- Magnitudes, never signs. Mirrors posting_amount_check.
    CONSTRAINT tds_challan_amounts_non_negative
        CHECK (tax_rupees >= 0 AND interest_rupees >= 0
           AND fee_rupees >= 0 AND penalty_rupees >= 0),
    -- Whole rupees. See the header -- this is the format's rule, not a
    -- rounding preference.
    CONSTRAINT tds_challan_amounts_whole_rupees
        CHECK (tax_rupees     = trunc(tax_rupees)
           AND interest_rupees = trunc(interest_rupees)
           AND fee_rupees      = trunc(fee_rupees)
           AND penalty_rupees  = trunc(penalty_rupees)),
    -- A challan on which nothing was paid is a nil challan, and a nil challan
    -- is not a deposit. See the header.
    CONSTRAINT tds_challan_something_was_deposited
        CHECK (tax_rupees + interest_rupees + fee_rupees + penalty_rupees > 0),

    CONSTRAINT tds_challan_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id)
);

-- The return reads a tax year and orders by deposit date.
CREATE INDEX IF NOT EXISTS tds_challan_period
    ON tds_challan (company_id, tax_year_start, deposit_date);

COMMENT ON TABLE tds_challan IS
    'REAL DEPOSITS ONLY. A nil challan is a return artefact -- derivable from '
    'the zero-tax deductions themselves and corresponding to no event -- so it '
    'is synthesised at emission time by internal/compliance/form140 and never '
    'stored here. Identified by its CIN (bsr_code, deposit_date, challan_no); '
    'a duplicate CIN is the same payment entered twice and would over-claim a '
    'deposit the FVU then fails against the .csi file.';

COMMENT ON COLUMN tds_challan.tax_rupees IS
    'The TAX component only. Interest, fee and penalty ride on the same challan '
    'and are not tax -- only this column discharges the deduction liability.';

COMMENT ON COLUMN tds_challan.bsr_code IS
    'Seven-digit BSR code of the receiving branch, or -- when by_book_entry -- '
    'the seven-digit Form 24G receipt number issued by the AO. The file format '
    'overloads one field for both.';

-- ===========================================================================
-- tds_challan_allocation -- WHICH DEDUCTIONS A CHALLAN COVERS
-- ===========================================================================
-- docs/design/t6-model.md §2.2's last row: "a link table, and §4's I-2 lives on
-- it".
--
-- ⛔⛔ THE KEY IS THE STABLE DEDUCTION IDENTITY, AND THERE IS NO FOREIGN KEY TO
-- tds_deduction. READ THIS BEFORE ADDING ONE.
-- ---------------------------------------------------------------------------
-- internal/ledger/tds.go deletes EVERY tds_deduction row for a voucher and
-- re-derives them on ALTER (`DELETE FROM tds_deduction WHERE company_id = $1
-- AND voucher_id = $2`, then re-INSERT). The new rows carry new ids. So:
--
--   * an FK to tds_deduction.id with ON DELETE CASCADE would DESTROY the
--     allocation the first time anybody corrects a voucher -- silently losing
--     the record of a payment that really was made to the Government;
--   * an FK with ON DELETE RESTRICT would make the voucher UNALTERABLE once its
--     tax had been deposited, which is worse.
--
-- This is exactly the trap 0045 records for bank_reconciliation, and the remedy
-- is the same one: key on the identity that SURVIVES an alter -- (voucher_id,
-- section, leg_seq), which is tds_deduction's own natural key minus its
-- surrogate id -- and RECORD WHAT WAS ALLOCATED. The reporting layer then
-- compares allocated_amount_rupees against the current deduction: a
-- disagreement makes the allocation BROKEN BY ALTER and VISIBLE, never
-- silently valid. Storing the amount is not redundant with tds_deduction; it is
-- the entire safety mechanism.
--
-- The FK to voucher_head is kept, because voucher_head is the thing that is
-- stable, and it stops an allocation naming a voucher that never existed.
--
-- ONE CHALLAN PER DEDUCTION, AND THE FORMAT IS WHY
-- ---------------------------------------------------------------------------
-- Deductee Detail field 5 is a SINGLE challan reference. A deduction split
-- across two challans is therefore not expressible in the return at all,
-- whatever the schema allows -- so the uniqueness below is the format's
-- constraint surfaced at write time rather than a simplification. A part
-- payment is modelled as an allocation smaller than the deduction, which the
-- return reports through the "has the tax been deposited" flag.

CREATE TABLE IF NOT EXISTS tds_challan_allocation (
    id                      UUID PRIMARY KEY,
    company_id              UUID NOT NULL,
    challan_id              UUID NOT NULL,

    -- tds_deduction's natural key, MINUS its surrogate id. See the header.
    voucher_id              UUID NOT NULL,
    section                 TEXT NOT NULL,
    leg_seq                 INTEGER NOT NULL DEFAULT 0,

    -- WHAT WAS DEPOSITED against that deduction. The safety mechanism, not a
    -- cached copy: it is compared against the live deduction to detect an
    -- alter that moved the amount out from under a recorded payment.
    allocated_amount_rupees NUMERIC(19,4) NOT NULL,

    recorded_by             TEXT NOT NULL,
    recorded_at             TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One challan per deduction -- see the header; the format permits no more.
    CONSTRAINT tds_challan_allocation_one_per_deduction
        UNIQUE (company_id, voucher_id, section, leg_seq),
    CONSTRAINT tds_challan_allocation_company_scope UNIQUE (company_id, id),

    CONSTRAINT tds_challan_allocation_challan_fk
        FOREIGN KEY (company_id, challan_id)
        REFERENCES tds_challan (company_id, id) ON DELETE CASCADE,
    -- voucher_head, NOT tds_deduction. The header says why.
    CONSTRAINT tds_challan_allocation_voucher_fk
        FOREIGN KEY (company_id, voucher_id)
        REFERENCES voucher_head (company_id, voucher_id),

    CONSTRAINT tds_challan_allocation_leg_seq_non_negative CHECK (leg_seq >= 0),
    CONSTRAINT tds_challan_allocation_amount_non_negative
        CHECK (allocated_amount_rupees >= 0)
);

-- The return walks a challan's deductees; the register walks a deduction's
-- challan. Both directions get an index.
CREATE INDEX IF NOT EXISTS tds_challan_allocation_by_challan
    ON tds_challan_allocation (company_id, challan_id);
CREATE INDEX IF NOT EXISTS tds_challan_allocation_by_deduction
    ON tds_challan_allocation (company_id, voucher_id, section, leg_seq);

COMMENT ON TABLE tds_challan_allocation IS
    'Which deductions a challan paid. Keyed on (voucher_id, section, leg_seq) '
    '-- tds_deduction''s NATURAL key -- and deliberately carrying NO foreign '
    'key to tds_deduction, whose rows are deleted and re-derived wholesale on '
    'every voucher ALTER. A cascade would silently destroy the record of a real '
    'payment; a restrict would make a voucher unalterable once its tax was '
    'deposited. Same mechanism as bank_reconciliation in 0045.';

COMMENT ON COLUMN tds_challan_allocation.allocated_amount_rupees IS
    'What was deposited against this deduction, recorded so that an ALTER which '
    'moves the deduction amount makes the allocation BROKEN BY ALTER and '
    'visible rather than silently valid. Not a cache of tds_deduction.';

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, policy with BOTH USING
-- and WITH CHECK. USING alone would let a session write a challan for another
-- company it can never see or correct; WITH CHECK alone would leak every
-- tenant's tax deposits on read. FORCE because migrations run as the table
-- owner and an owner bypasses its own policies unless forced.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['tds_challan', 'tds_challan_allocation'] LOOP
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

GRANT SELECT, INSERT, UPDATE, DELETE ON tds_challan            TO tally_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON tds_challan_allocation TO tally_app;
