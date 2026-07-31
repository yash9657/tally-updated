-- 0049_tds_liability_and_declarations.sql
-- WHO MUST DEDUCT AT ALL, and WHO IS EXEMPT FROM COLLECTION.
--
-- Design memo: docs/design/t6-gaps-model.md, gaps 4 and 5. Migration number from
-- PLAN-TALLY-PARITY.md §2.5, which reserves 0049-0050 for T6 TDS/TCS
-- persistence. This file takes 0049; the register and return take 0050.
--
-- These two tables answer questions the engine has been ASSUMING the answer to,
-- and in both cases assuming wrongly in the direction that touches somebody's
-- money.
--
-- ===========================================================================
-- 1. company_deductor_status -- gap 5, and the bigger of the two
-- ===========================================================================
-- The obligation to deduct is NOT universal. Every row of the s.393(1) Table we
-- ship names its payer in column C, and it is never "any person":
--
--     194C            Sl. No. 6(i)     "Any designated person."
--     194J-P/T/D      Sl. No. 6(iii)   "Specified person."
--     194H            Sl. No. 1(ii)    "Specified person."
--     194I-A/B        Sl. No. 2(ii)    "Specified person."
--
-- s.402(37) "specified person" means "(a) any person, not being an individual or
-- Hindu undivided family; or (b) an individual or a Hindu undivided family,
-- whose total sales, gross receipts or turnover from the business or profession
-- carried on by him exceed one crore rupees in case of business or fifty lakh
-- rupees in case of profession during the tax year immediately preceding the tax
-- year in which such income or sum is credited or paid".
--
-- s.402(11) "designated person" is an explicit list -- Government, local
-- authority, statutory corporation, company, co-operative society, housing or
-- planning authority, registered society, trust, university, foreign government
-- or enterprise, firm -- plus "(l) any person, being an individual or a Hindu
-- undivided family OR AN ASSOCIATION OF PERSONS OR A BODY OF INDIVIDUALS" that
-- does not fall in the preceding sub-clauses and meets the SAME turnover test.
--
-- THE TWO DEFINITIONS ARE NOT THE SAME TEST, AND ONE BOOLEAN CANNOT HOLD BOTH.
-- They diverge exactly at AOP and BOI: "specified person" catches them
-- unconditionally, because they are not individuals or HUFs; "designated person"
-- subjects them to the turnover test. So an AOP with Rs 40 lakh turnover MUST
-- deduct on professional fees and MUST NOT on contractor payments. That is why
-- this table stores the CATEGORY and the TURNOVER as facts, and leaves both
-- tests to be derived in Go, rather than storing a verdict.
--
-- WHY A TABLE AND NOT COLUMNS ON `company`
-- ----------------------------------------
-- The test reads "during the tax year IMMEDIATELY PRECEDING", so the answer is
-- per tax year by construction and (company_id, tax_year_start) is its natural
-- key. Columns on `company` would need a separate history table to say the same
-- thing. It also keeps `company` -- which every tenancy test, fixture and RLS
-- policy touches -- out of this change.
--
-- THE ABSENCE OF A ROW IS THE REFUSAL, AND THAT IS THE POINT. The engine must
-- REFUSE to derive a deduction for a company whose status is unknown, never
-- return zero: a zero is indistinguishable from "correctly below threshold", and
-- it is the DEDUCTOR who carries the liability plus interest. Getting that
-- behaviour free from an absent row -- rather than from a nullable-column
-- convention that someone will later default -- is the whole reason for the
-- shape.
--
-- ===========================================================================
-- 2. tcs_collection_declaration -- gap 4
-- ===========================================================================
-- s.394(2): "Irrespective of anything contained in sub-section (1) (Table: Sl.
-- Nos. 1 to 5), the collection of tax shall not to be made ... in respect of the
-- buyer, who is a resident in India, if he furnishes a written declaration in
-- duplicate ... mentioning that such goods are to be utilised-- (a) for the
-- purposes of manufacturing, processing or producing articles or things or for
-- generating power; and (b) not for trading purposes."
--
-- THREE THINGS THAT FALL STRAIGHT OUT OF THAT TEXT:
--
--   1. It covers Table Sl. Nos. 1 TO 5, which includes SCRAP (Sl. No. 4) but NOT
--      motor vehicles (Sl. No. 6). Our two shipped TCS sections fall on opposite
--      sides of that line, so a declaration presented against 206C(1F)-MV is
--      invalid and is refused by CHECK, not ignored.
--
--   2. (a) and (b) are CONJUNCTIVE. Goods for manufacturing that are also for
--      trading do not qualify.
--
--   3. ⚠️ s.397(2)(f)(i) AND (g) COUPLE THIS TO THE NO-PAN RULE, and this is the
--      coupling that is easy to get half-right. "if a person does not furnish
--      his valid Permanent Account Number in-- (i) any declaration under section
--      393(6) or 394(2), then such declaration BECOMES INVALID"; and (g) "if any
--      declaration becomes invalid ..., then the deductor or collector shall
--      deduct or collect tax as per the provisions of clause (b)(i) or (ii)".
--      So a declaration from a buyer with no valid PAN does not merely lose the
--      exemption -- it triggers the PENAL no-PAN rate. Exemption -> 5%, not
--      exemption -> 2%.
--
--      That is why buyer_pan is NULLABLE here rather than NOT NULL. A
--      declaration without a PAN is a real declaration in an invalid state, and
--      the engine has to be able to see it to apply (g). Refusing it at the
--      table would erase the very fact that makes the penal rate correct.
--
--   4. s.394(3) creates a DATED OBLIGATION we are taking on: the collector
--      "shall deliver ... one copy of the declaration to the Principal Chief
--      Commissioner or Chief Commissioner or Principal Commissioner or
--      Commissioner, on or before the seventh day of the month following the
--      month of receipt of that declaration." received_date exists so that due
--      date is DERIVABLE. Storing declarations without it would build a
--      compliance trap. The reminder itself is T6 register work.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration. Hence IF NOT EXISTS throughout and DROP ... IF EXISTS
-- before each CREATE lacking one.

-- ---------------------------------------------------------------------------
-- 1. company_deductor_status
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS company_deductor_status (
    id                        UUID PRIMARY KEY,
    company_id                UUID NOT NULL,
    -- The calendar year in which the tax year BEGINS: 2026 means the tax year
    -- 2026-27, running 1 April 2026 to 31 March 2027. The unit is in the name
    -- because "tax_year = 2026" alone is ambiguous between the two years it
    -- spans, and the whole test turns on which year is meant.
    tax_year_start            INTEGER NOT NULL,
    -- The entity class, mapped 1:1 onto s.402(11)(a)-(l). It is stored as the
    -- CATEGORY and not as a verdict because the designated-person and
    -- specified-person tests read it differently -- see the header on AOP/BOI.
    -- It is also what Form 140's Annexure 4 (deductor category) will need.
    entity_category           TEXT NOT NULL,
    -- The turnover test's operand, in RUPEES (the unit is in the name; the
    -- ledger's money scale is 4 dp and this follows it). NULL when the category
    -- makes the test irrelevant -- a company is a specified AND designated
    -- person whatever its turnover -- which is a different statement from zero.
    prior_year_turnover_rupees NUMERIC(19,4),
    -- BUSINESS or PROFESSION: they carry DIFFERENT limits (one crore against
    -- fifty lakh), so this is stored and never inferred from the turnover.
    prior_year_activity       TEXT,
    -- Audit.
    recorded_by               TEXT NOT NULL,
    recorded_at               TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- ONE status per company per tax year. A second row would make "was this
    -- company required to deduct in 2026-27?" unanswerable.
    CONSTRAINT company_deductor_status_one_per_year
        UNIQUE (company_id, tax_year_start),
    -- The composite tenant key, so a future child table can reference the PAIR
    -- and have a cross-tenant id refused BY THE FK rather than by RLS alone.
    CONSTRAINT company_deductor_status_company_scope UNIQUE (company_id, id),
    CONSTRAINT company_deductor_status_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id),

    -- The categories of s.402(11)(a)-(l). Held as a CHECK rather than an ENUM
    -- type so the list can be extended by a later migration without an ALTER
    -- TYPE, which cannot run inside a transaction on older servers.
    CONSTRAINT company_deductor_status_category_known CHECK (
        entity_category IN (
            'GOVERNMENT',            -- (a) Central or State Government
            'LOCAL_AUTHORITY',       -- (b)
            'STATUTORY_CORPORATION', -- (c) corporation under a Central/State Act
            'COMPANY',               -- (d)
            'COOPERATIVE_SOCIETY',   -- (e)
            'HOUSING_AUTHORITY',     -- (f) housing / town planning authority
            'REGISTERED_SOCIETY',    -- (g) Societies Registration Act 1860
            'TRUST',                 -- (h)
            'UNIVERSITY',            -- (i)
            'FOREIGN_ENTITY',        -- (j) foreign government or enterprise
            'FIRM',                  -- (k)
            'INDIVIDUAL',            -- (l)
            'HUF',                   -- (l)
            'AOP',                   -- (l) -- see the header: NOT (l) for s.402(37)
            'BOI'                    -- (l) -- ditto
        )
    ),
    CONSTRAINT company_deductor_status_activity_known CHECK (
        prior_year_activity IS NULL
        OR prior_year_activity IN ('BUSINESS', 'PROFESSION')
    ),
    CONSTRAINT company_deductor_status_turnover_non_negative CHECK (
        prior_year_turnover_rupees IS NULL OR prior_year_turnover_rupees >= 0
    ),
    -- A tax year before the 2025 Act commenced cannot be described by these
    -- categories at all.
    CONSTRAINT company_deductor_status_tax_year_sane CHECK (
        tax_year_start BETWEEN 2025 AND 2200
    ),
    -- THE TURNOVER TEST NEEDS BOTH OPERANDS OR NEITHER. For the five categories
    -- that are subject to it, a turnover with no activity cannot be compared
    -- against a limit (one crore or fifty lakh?), and an activity with no
    -- turnover has nothing to compare. Storing one alone would produce a row
    -- that looks answerable and is not.
    CONSTRAINT company_deductor_status_turnover_pair CHECK (
        (prior_year_turnover_rupees IS NULL) = (prior_year_activity IS NULL)
    ),
    -- ...and for those five categories the pair is REQUIRED, because without it
    -- neither test can be evaluated and the row would be a silent "unknown"
    -- masquerading as a recorded status.
    CONSTRAINT company_deductor_status_turnover_required_for_natural_persons CHECK (
        entity_category NOT IN ('INDIVIDUAL', 'HUF', 'AOP', 'BOI')
        OR prior_year_turnover_rupees IS NOT NULL
    )
);

COMMENT ON TABLE company_deductor_status IS
    'Whether THIS company is required to deduct tax at source, per tax year. '
    'Stores the entity CATEGORY and the prior-year turnover as facts, never a '
    'verdict, because s.402(11) "designated person" and s.402(37) "specified '
    'person" read them differently -- they diverge at AOP and BOI. THE ABSENCE '
    'OF A ROW IS A REFUSAL: the engine must refuse to derive a deduction for a '
    'company whose status is unknown, never return zero, because zero is '
    'indistinguishable from "below threshold" and the deductor carries the '
    'liability plus interest.';

COMMENT ON COLUMN company_deductor_status.tax_year_start IS
    'Calendar year in which the tax year BEGINS: 2026 = tax year 2026-27. The '
    'turnover test reads "the tax year immediately preceding", so callers look '
    'up tax_year_start = (year of payment) - 1.';

COMMENT ON COLUMN company_deductor_status.prior_year_turnover_rupees IS
    'Total sales, gross receipts or turnover of the PRECEDING tax year, in '
    'rupees. NULL means the test does not apply to this category -- a company is '
    'a specified and designated person whatever its turnover -- which is a '
    'different statement from zero.';

-- ---------------------------------------------------------------------------
-- 2. tcs_collection_declaration
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tcs_collection_declaration (
    id                UUID PRIMARY KEY,
    company_id        UUID NOT NULL,
    -- The BUYER who furnished it. A declaration is per buyer, not per invoice:
    -- s.394(2) exempts "the buyer", and one declaration covers their purchases.
    buyer_ledger_id   UUID NOT NULL,
    -- Which collection it was furnished against. Constrained below to the
    -- sections s.394(2) actually reaches.
    section           TEXT NOT NULL,
    -- The tax year the declaration governs. Same convention as
    -- company_deductor_status.tax_year_start.
    tax_year_start    INTEGER NOT NULL,
    -- The date on the declaration, and the date the collector RECEIVED it. They
    -- are different facts and s.394(3)'s due date runs off the second one.
    declaration_date  DATE NOT NULL,
    received_date     DATE NOT NULL,
    -- The two purposes of s.394(2)(a) and (b). Conjunctive -- see the CHECK.
    for_manufacturing BOOLEAN NOT NULL,
    not_for_trading   BOOLEAN NOT NULL,
    -- s.394(2) requires the buyer to be "a resident in India".
    buyer_is_resident BOOLEAN NOT NULL,
    -- NULLABLE ON PURPOSE. s.397(2)(f)(i): a declaration without a valid PAN
    -- BECOMES INVALID, and (g) then requires collection at the penal no-PAN
    -- rate. A NOT NULL here would erase the state that makes the penal rate
    -- correct, and the engine would silently exempt instead. See the header.
    buyer_pan         TEXT,
    -- Audit.
    recorded_by       TEXT NOT NULL,
    recorded_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT tcs_collection_declaration_one_per_buyer_year
        UNIQUE (company_id, buyer_ledger_id, section, tax_year_start),
    CONSTRAINT tcs_collection_declaration_company_scope UNIQUE (company_id, id),
    CONSTRAINT tcs_collection_declaration_buyer_fk
        FOREIGN KEY (company_id, buyer_ledger_id)
        REFERENCES ledger (company_id, id),

    -- s.394(2) reaches Table Sl. Nos. 1 TO 5 ONLY. Of the two sections this
    -- build ships, scrap is Sl. No. 4 and IS reachable; motor vehicles are
    -- Sl. No. 6 and are NOT. A declaration against the wrong section is refused
    -- here rather than silently ignored by the engine, because an operator who
    -- filed one believes their buyer is exempt.
    CONSTRAINT tcs_collection_declaration_section_eligible CHECK (
        section IN ('206C(1)-SCRAP')
    ),
    -- (a) AND (b), conjunctive. Goods for manufacturing that are ALSO for
    -- trading do not qualify.
    CONSTRAINT tcs_collection_declaration_purposes_conjunctive CHECK (
        for_manufacturing AND not_for_trading
    ),
    CONSTRAINT tcs_collection_declaration_buyer_resident CHECK (buyer_is_resident),
    CONSTRAINT tcs_collection_declaration_received_not_before_declared CHECK (
        received_date >= declaration_date
    ),
    CONSTRAINT tcs_collection_declaration_tax_year_sane CHECK (
        tax_year_start BETWEEN 2025 AND 2200
    ),
    -- An empty string is not a PAN and must not be storable as one: it would
    -- read as "a PAN was furnished" to any IS NOT NULL test.
    CONSTRAINT tcs_collection_declaration_pan_not_blank CHECK (
        buyer_pan IS NULL OR length(btrim(buyer_pan)) = 10
    )
);

CREATE INDEX IF NOT EXISTS tcs_collection_declaration_buyer
    ON tcs_collection_declaration (company_id, buyer_ledger_id, tax_year_start);

COMMENT ON TABLE tcs_collection_declaration IS
    's.394(2) manufacturing declarations: a resident buyer''s written statement '
    'that goods are for manufacturing, processing, producing or generating '
    'power AND not for trading, which exempts Table Sl. Nos. 1-5 from '
    'collection. Scrap (Sl. No. 4) is reachable; motor vehicles (Sl. No. 6) are '
    'not. buyer_pan is NULLABLE because s.397(2)(f)(i) makes a PAN-less '
    'declaration INVALID and (g) then requires the PENAL rate -- not the base '
    'rate, and not the exemption.';

COMMENT ON COLUMN tcs_collection_declaration.received_date IS
    'When the collector received the declaration. s.394(3) requires a copy to '
    'reach the Commissioner "on or before the seventh day of the month '
    'following the month of receipt", so this column is what makes that due '
    'date derivable.';

COMMENT ON COLUMN tcs_collection_declaration.buyer_pan IS
    'NULL means no valid PAN was furnished, which under s.397(2)(f)(i) makes '
    'the declaration INVALID -- and s.397(2)(g) then requires collection at the '
    'penal no-PAN rate. Do NOT make this NOT NULL: that erases the state the '
    'penal rate depends on and the engine would silently exempt instead.';

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, policy with BOTH USING
-- and WITH CHECK. USING alone would let a session write a row for another
-- company it can never see or correct; WITH CHECK alone would leak every
-- tenant's turnover on read. FORCE because migrations run as the table owner
-- and an owner bypasses its own policies unless forced.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['company_deductor_status', 'tcs_collection_declaration']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I '
            'USING (company_id = app_current_company_id()) '
            'WITH CHECK (company_id = app_current_company_id())', t);
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO tally_app', t);
    END LOOP;
END
$$;
