-- 0067_tcs_collection.sql
-- THE COLLECTION FACTS — the collection-side mirror of 0050's tds_deduction.
--
-- Migration number from PLAN-TALLY-PARITY.md §2.5, which reserves 0067 for this
-- table. T6's original block (0049-0050) was consumed on the assumption that TCS
-- needed only the declaration table; wiring TCSDerive into the post path showed
-- that a COLLECTION has nowhere to be recorded.
--
-- Without this table, wiring TCS would repeat on the collection side the exact
-- defect 0050 exists to end on the deduction side: derive the tax, verify the
-- posted leg, then discard every fact about it, so no Form 143 return and no
-- register could ever see it.
--
-- ===========================================================================
-- ⚠️ A ZERO-AMOUNT ROW IS A FACT HERE TOO, AND FOR ONE OF 0050'S TWO REASONS
-- ===========================================================================
-- 0050 gives two independent reasons to record a payment on which no tax was
-- deducted: the s.393(1) AGGREGATE is of amounts paid, and Annexure 6 reports a
-- sub-threshold payment with a flag. ONLY THE SECOND APPLIES HERE, and the
-- difference is worth stating so nobody "fixes" the asymmetry:
--
--   * THERE IS NO AGGREGATE ON THE TCS SIDE. Both shipped sections are
--     BasisPerTxn — s.394(1) Table Sl. No. 4 (scrap) carries no threshold at
--     all, and Sl. No. 6 (motor vehicles) is tested against the single sale
--     ("where the sale consideration exceeds ten lakh rupees"), not against a
--     running total. There is deliberately no cumulation here and no
--     TCSDeriveCumulative; adding one would collect tax the section does not
--     charge.
--
--   * THE RETURN STILL REPORTS THE SALE. A sale exempted by a s.394(2)
--     declaration, or one below Sl. No. 6's limit, is a transaction the
--     collector has to be able to account for — and an EXEMPT sale is the one
--     that most needs a record, because the exemption rests on a document the
--     department can ask to see (s.394(3) requires a copy to reach the
--     Commissioner by the 7th of the following month).
--
-- So: every sale of a nature liable to collection gets a row, whether or not tax
-- was collected. Do NOT add a WHERE tcs_amount_rupees > 0.
--
-- ===========================================================================
-- WHY THERE IS NO return_code COLUMN, WHEN 0050 INSISTED ON ONE
-- ===========================================================================
-- 0050 stores the Form 140 Annexure 2 code because 194J-P and 194J-D share
-- statutory limb 6(iii).D(b) AT THE SAME RATE and differ only by code (1027 vs
-- 1028) — so a return builder re-deriving from `section` would file directors'
-- fees as professional fees. That hazard DOES NOT EXIST HERE: the two shipped
-- collection sections are distinct rows of the s.394(1) Table at distinct rates
-- (scrap 2%, motor vehicles 1%), so `section` determines the code uniquely and
-- a later mapping cannot silently mis-file one as the other.
--
-- AND THE HONEST HALF OF THE REASON: the Form 143 collection codes have NOT been
-- read against primary text from this environment. Storing a column and filling
-- it with a guess would put an invented code in a filed return — the failure
-- HANDOFF-FEATURE-LOCK.md §1.1 forbids outright. When the codes are sourced, a
-- later migration adds the column and backfills it FROM `section`, which is
-- exactly the operation that is safe here and was not safe on the deduction
-- side.
--
-- ===========================================================================
-- KEYED ON voucher_id, NOT voucher_version_id — same rule as 0050 and 0045.
-- ===========================================================================
-- `posting` is hard-immutable and an ALTER re-creates every posting row, so
-- anything keyed on a version is orphaned by the first correction. The
-- collection is derived from the sale inside the same transaction that posts it
-- and is RE-DERIVED on alter, so it is written with the voucher rather than
-- surviving alongside it.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration.

CREATE TABLE IF NOT EXISTS tcs_collection (
    id                    UUID PRIMARY KEY,
    company_id            UUID NOT NULL,
    -- The STABLE voucher identity. See the header: never the version id.
    voucher_id            UUID NOT NULL,
    -- Ordinal within (voucher, section), 0-based, for a sale carrying more than
    -- one collection of the same nature.
    leg_seq               INTEGER NOT NULL DEFAULT 0,
    -- WHO bought. The s.394(2) declaration is per BUYER, and the return is filed
    -- per collectee, so this is not optional.
    buyer_ledger_id       UUID NOT NULL,
    -- The collection key, e.g. '206C(1)-SCRAP'. Constrained to the sections the
    -- engine actually ships, so a typo cannot create a row no ruleset can
    -- re-derive.
    section               TEXT NOT NULL,
    -- Calendar year the tax year BEGINS in: 2026 = 2026-27. Same convention as
    -- 0049 and 0050. The return is filed per quarter within a tax year.
    tax_year_start        INTEGER NOT NULL,
    -- The date the amount was debited to the buyer or received, whichever is
    -- earlier (s.394(1)).
    collection_date       DATE NOT NULL,

    -- The sale consideration the collection applies to. Unit in the name.
    taxable_base_rupees   NUMERIC(19,4) NOT NULL,
    -- The rate actually applied, AFTER any s.397(2)(b)(ii) no-PAN override.
    -- 4 dp because a return prints it at 4 dp.
    rate_pct              NUMERIC(9,4) NOT NULL,
    -- The tax collected. ZERO IS LEGITIMATE -- see the header.
    tcs_amount_rupees     NUMERIC(19,4) NOT NULL,
    -- Whether a PAN was on record. Drives the s.397(2)(b)(ii) penal rate.
    pan_available         BOOLEAN NOT NULL,

    -- ⚠️ WHAT A FURNISHED s.394(2) DECLARATION ACTUALLY DID, and the reason this
    -- is a three-valued column rather than a boolean.
    --
    -- 'NONE'           no declaration was furnished; collect normally.
    -- 'EXEMPT'         a valid declaration; nothing is collected.
    -- 'INVALID_NO_PAN' furnished, but with no valid PAN, so s.397(2)(f)(i)
    --                  makes it INVALID and (g) requires the PENAL rate.
    --
    -- 'NONE' and 'EXEMPT' both produce a zero-or-normal amount and would be
    -- indistinguishable if this were a boolean — but they are opposite facts
    -- about the same sale, and only one of them rests on a document the
    -- department can demand. 'INVALID_NO_PAN' is the third, and it is the state
    -- an implementation is most likely to collapse into one of the other two:
    -- it looks like an exemption to the operator and charges MORE than no
    -- declaration at all.
    declaration_outcome   TEXT NOT NULL,

    -- The effective-dated rate table this was derived under, so a collection can
    -- be re-derived and checked years later against the law as it then stood.
    -- This column is what makes the scrap 1%->2% correction auditable rather
    -- than merely fixed.
    ruleset_version       TEXT NOT NULL,

    -- Audit.
    recorded_by           TEXT NOT NULL,
    recorded_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT tcs_collection_one_per_leg
        UNIQUE (company_id, voucher_id, section, leg_seq),
    CONSTRAINT tcs_collection_company_scope UNIQUE (company_id, id),
    -- Composite FKs: a cross-tenant voucher or ledger is refused BY THE FK
    -- itself, not by RLS alone.
    CONSTRAINT tcs_collection_voucher_fk
        FOREIGN KEY (company_id, voucher_id)
        REFERENCES voucher_head (company_id, voucher_id),
    CONSTRAINT tcs_collection_buyer_fk
        FOREIGN KEY (company_id, buyer_ledger_id)
        REFERENCES ledger (company_id, id),

    CONSTRAINT tcs_collection_leg_seq_non_negative CHECK (leg_seq >= 0),
    -- The sections the engine ships. 206C(1H) is DELIBERATELY absent: it was
    -- omitted w.e.f. 1-Apr-2025 and collecting under it would be collecting a
    -- tax that no longer exists.
    CONSTRAINT tcs_collection_section_known CHECK (
        section IN ('206C(1)-SCRAP', '206C(1F)-MV')
    ),
    CONSTRAINT tcs_collection_declaration_outcome_known CHECK (
        declaration_outcome IN ('NONE', 'EXEMPT', 'INVALID_NO_PAN')
    ),
    -- ⚠️ ONLY SCRAP CAN BE EXEMPTED. s.394(2) reaches Table Sl. Nos. 1 to 5;
    -- motor vehicles are Sl. No. 6. Mirrors 0049's
    -- tcs_collection_declaration_section_eligible, and is repeated HERE because
    -- this table records the OUTCOME rather than the declaration — a row saying
    -- a motor-vehicle sale was exempted would be a false statement about the
    -- statute that the declaration table's own CHECK cannot prevent.
    CONSTRAINT tcs_collection_only_eligible_sections_exempt CHECK (
        declaration_outcome = 'NONE' OR section = '206C(1)-SCRAP'
    ),
    -- An EXEMPT sale collects nothing. Anything else is a row that claims a
    -- valid declaration and charges tax anyway.
    CONSTRAINT tcs_collection_exempt_collects_nothing CHECK (
        declaration_outcome <> 'EXEMPT' OR tcs_amount_rupees = 0
    ),
    -- Magnitudes, never signs. Mirrors posting_amount_check.
    CONSTRAINT tcs_collection_base_non_negative CHECK (taxable_base_rupees >= 0),
    CONSTRAINT tcs_collection_amount_non_negative CHECK (tcs_amount_rupees >= 0),
    -- s.397(2)(b)(ii): "...at the higher of the following rates, NOT EXCEEDING
    -- 20%". The ceiling is statutory, so the column carries it as well as the
    -- engine — a rate above 20% in this table is unfileable whatever produced it.
    CONSTRAINT tcs_collection_rate_within_statutory_ceiling
        CHECK (rate_pct >= 0 AND rate_pct <= 20),
    -- You cannot collect more than the consideration you are collecting on.
    CONSTRAINT tcs_collection_amount_within_base
        CHECK (tcs_amount_rupees <= taxable_base_rupees),
    CONSTRAINT tcs_collection_tax_year_sane
        CHECK (tax_year_start BETWEEN 2025 AND 2200),
    CONSTRAINT tcs_collection_ruleset_present
        CHECK (length(btrim(ruleset_version)) > 0)
);

-- The register and the return read a PERIOD, per collectee. There is no
-- aggregate access path of 0050's kind because there is no aggregate: both
-- shipped sections are per-transaction.
CREATE INDEX IF NOT EXISTS tcs_collection_period
    ON tcs_collection (company_id, tax_year_start, collection_date);

CREATE INDEX IF NOT EXISTS tcs_collection_buyer
    ON tcs_collection (company_id, buyer_ledger_id, section, tax_year_start);

COMMENT ON TABLE tcs_collection IS
    'Every sale of a nature liable to collection at source, WHETHER OR NOT tax '
    'was collected. A zero-amount row is a FACT: a sale exempted by a s.394(2) '
    'declaration, or below Sl. No. 6''s ten-lakh limit, is still a transaction '
    'the collector must account for -- and an exempt sale is the one that most '
    'needs a record, because the exemption rests on a document the department '
    'can demand. Unlike tds_deduction there is NO aggregate: both shipped '
    'sections are per-transaction, so there is deliberately no cumulation.';

COMMENT ON COLUMN tcs_collection.declaration_outcome IS
    'What a furnished s.394(2) declaration did: NONE, EXEMPT, or '
    'INVALID_NO_PAN. Three values and not a boolean -- NONE and EXEMPT are '
    'opposite facts that a boolean would collapse, and INVALID_NO_PAN is the '
    'state s.397(2)(f)(i)+(g) create, which looks like an exemption to the '
    'operator and charges MORE than furnishing nothing at all.';

COMMENT ON COLUMN tcs_collection.ruleset_version IS
    'The effective-dated rate table this was derived under. This column is what '
    'makes the scrap 1%%->2%% Finance Act 2026 correction auditable rather than '
    'merely fixed.';

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, policy with BOTH USING
-- and WITH CHECK. USING alone would let a session write a collection for
-- another company it can never see or correct; WITH CHECK alone would leak
-- every tenant's customer sales on read. FORCE because migrations run as the
-- table owner and an owner bypasses its own policies unless forced.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    EXECUTE 'ALTER TABLE tcs_collection ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE tcs_collection FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON tcs_collection';
    EXECUTE 'CREATE POLICY tenant_isolation ON tcs_collection '
         || 'USING (company_id = app_current_company_id()) '
         || 'WITH CHECK (company_id = app_current_company_id())';
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON tcs_collection TO tally_app;
