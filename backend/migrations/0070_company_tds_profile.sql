-- 0070_company_tds_profile.sql
-- WHO IS FILING -- the deductor block of the return, which no table holds.
--
-- Design memo: docs/design/t6-model.md §2.3, every row of which reads
-- MIGRATION. Migration number from PLAN-TALLY-PARITY.md §2.5; 0069 took the
-- challan register.
--
-- ===========================================================================
-- ⚠️ THE TAN IS NOT THE GSTIN, AND IT IS NOT DERIVABLE FROM IT
-- ===========================================================================
-- `company` carries `gstin`, `state_code`, `address`, `city`, `pincode` and
-- `country`. It carries NO TAN and NO PAN. A Tax Deduction and Collection
-- Account Number is separately allotted, has a different shape (AAAA99999A
-- against a GSTIN's 15 characters), and a company can hold one without the
-- other. Nothing in this schema can produce it, so it is asked for.
--
-- The TAN NAME is a distinct fact from the company's own name, and this is not
-- pedantry: the file format's General Note 13 reads "TAN & TAN name present in
-- TDS Statement should match with TAN & TAN name present under .csi file
-- downloaded from TIN website". The name that must match is the one the
-- department has against the TAN, which may differ from a display name or a
-- mailing name by punctuation alone -- and that is enough to fail validation.
-- So deductor_name is stored here rather than read from `company`.
--
-- ===========================================================================
-- WHY THE ADDRESS IS RE-STATED RATHER THAN READ FROM `company`
-- ===========================================================================
-- `company.address` is one free-text column. The return wants FIVE separately
-- numbered lines of at most 25 characters each, plus a country/region, plus a
-- two-digit Annexure 1 STATE CODE rather than a state name. Splitting a blob
-- into 25-character lines is a guess, and a guess that silently truncates a
-- deductor's address on a statutory filing. These are structured statutory
-- fields that happen to describe the same building, in the same way
-- `party_address` (0039) is not `ledger.address`.
--
-- ===========================================================================
-- ONE ROW PER COMPANY, NOT ONE PER TAX YEAR -- and why that differs from 0049
-- ===========================================================================
-- 0049's company_deductor_status is keyed (company_id, tax_year_start) because
-- the STATUTE makes it per-year: "during the tax year immediately preceding".
-- Nothing here has that property. The return asks who is responsible for
-- deduction NOW, at the moment of filing -- a Q1 return filed late in Q3 is
-- signed by whoever is responsible in Q3, not by whoever held the role in June.
-- So this is current-state, and a change overwrites rather than appends.
--
-- THE CONSEQUENCE, STATED SO IT IS A DECISION AND NOT A DISCOVERY: re-emitting
-- a return for an old quarter after the responsible person changes produces a
-- file naming the NEW person. That is correct for a fresh filing and wrong as a
-- reproduction of what was filed. If reproducing a past filing ever matters,
-- the artefact to keep is the emitted file, not a history of this table.
--
-- ===========================================================================
-- THE ABSENCE OF A ROW IS THE REFUSAL
-- ===========================================================================
-- Same shape as 0049. A company with no row here cannot have a return built for
-- it, and the reporting layer must say so by name rather than emitting a file
-- with blank mandatory fields -- which the FVU would reject, at the portal,
-- after the operator believed they had filed.
--
-- WHAT THIS TABLE CHECKS AND WHAT IT LEAVES TO form140
-- ---------------------------------------------------------------------------
-- Shapes and closed vocabularies are checked HERE, because they are cheap,
-- unambiguous, and catch a bad value at entry instead of at filing. The
-- CONDITIONAL rules that depend on the deductor category are here too, because
-- they are cross-field within one row, which is what a CHECK is for.
--
-- What is NOT duplicated here is the Annexure 1 state-code LIST (37 values).
-- internal/compliance/form140 is its authority and refuses an unknown code with
-- the annexure named; a third copy of a closed vocabulary is what
-- 0049's header warns about, and this one is large enough that the copies would
-- drift. The shape check below is deliberately only a shape.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it.

CREATE TABLE IF NOT EXISTS company_tds_profile (
    -- One row per company: the key IS the company. See the header.
    company_id             UUID PRIMARY KEY,

    -- ---- Identity ---------------------------------------------------------
    tan                    TEXT NOT NULL,
    -- The deductor's own PAN. The format prescribes the literal 'PANNOTREQD'
    -- for a deductor not required to hold one -- a real value with a meaning,
    -- never a blank.
    pan                    TEXT NOT NULL,
    -- The name as held against the TAN. See the header: it must match the .csi.
    deductor_name          TEXT NOT NULL,
    -- Annexure 4 of the file format. NOT the s.402(11) entity category that
    -- 0049 stores -- see the CHECK below.
    deductor_category      TEXT NOT NULL,

    -- ---- Address, as the return numbers it -------------------------------
    address1               TEXT NOT NULL,
    address2               TEXT NOT NULL DEFAULT '',
    address3               TEXT NOT NULL DEFAULT '',
    address4               TEXT NOT NULL DEFAULT '',
    address5               TEXT NOT NULL DEFAULT '',
    country                TEXT NOT NULL DEFAULT 'India',
    -- Annexure 1 two-digit CODE, not a name.
    state_code             TEXT NOT NULL,
    pincode                TEXT NOT NULL,

    -- ---- Contact ----------------------------------------------------------
    -- May be blank IF responsible_email is present. The format prints both as
    -- mandatory and then makes each conditional on the other; the CHECK below
    -- is that pair rule.
    email                  TEXT NOT NULL DEFAULT '',
    -- This format version splits the calling code from the number.
    contact_country_code   TEXT NOT NULL DEFAULT '91',
    contact_number         TEXT NOT NULL,

    -- ---- Category-conditional fields --------------------------------------
    -- Annexure 5 state code. Mandatory for the four State-Government
    -- categories and FORBIDDEN for every other -- conditional in BOTH
    -- directions, which is why the CHECK tests emptiness as well as presence.
    state_name_code        TEXT NOT NULL DEFAULT '',
    -- Annexure 3 ministry code, and its free-text expansion when the code is
    -- 99 ("Other").
    ministry_code          TEXT NOT NULL DEFAULT '',
    ministry_other         TEXT NOT NULL DEFAULT '',
    -- Account Office Identification Number: Government deductors paying by
    -- book entry only.
    ain                    TEXT NOT NULL DEFAULT '',
    -- The one field of this block `company` already holds. Carried here anyway
    -- because the return's copy is the one that was filed, and because a
    -- company may file TDS returns without being GST-registered at all.
    gstin                  TEXT NOT NULL DEFAULT '',

    -- ---- The person responsible for deduction ----------------------------
    -- A human, named on the return, personally answerable for it. Every field
    -- below is mandatory on the form except the four optional address lines.
    responsible_name       TEXT NOT NULL,
    responsible_designation TEXT NOT NULL,
    -- Their OWN PAN, a different field from the deductor's.
    responsible_pan        TEXT NOT NULL,
    responsible_address1   TEXT NOT NULL,
    responsible_address2   TEXT NOT NULL DEFAULT '',
    responsible_address3   TEXT NOT NULL DEFAULT '',
    responsible_address4   TEXT NOT NULL DEFAULT '',
    responsible_address5   TEXT NOT NULL DEFAULT '',
    responsible_country    TEXT NOT NULL DEFAULT 'India',
    responsible_state_code TEXT NOT NULL,
    responsible_pincode    TEXT NOT NULL,
    responsible_email      TEXT NOT NULL DEFAULT '',
    responsible_contact_country_code TEXT NOT NULL DEFAULT '91',
    responsible_contact_number TEXT NOT NULL,

    recorded_by            TEXT NOT NULL,
    recorded_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT company_tds_profile_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id),

    -- A TAN is four letters, five digits and a letter. Getting this wrong is
    -- not a formatting problem: the statement is rejected against the .csi.
    CONSTRAINT company_tds_profile_tan_shape
        CHECK (tan ~ '^[A-Z]{4}[0-9]{5}[A-Z]$'),
    -- A PAN is five letters, four digits and a letter -- or the format's own
    -- literal for a deductor not required to hold one.
    CONSTRAINT company_tds_profile_pan_shape
        CHECK (pan ~ '^[A-Z]{5}[0-9]{4}[A-Z]$' OR pan = 'PANNOTREQD'),
    CONSTRAINT company_tds_profile_responsible_pan_shape
        CHECK (responsible_pan ~ '^[A-Z]{5}[0-9]{4}[A-Z]$'),

    -- Annexure 4, all sixteen. Pinned against
    -- form140.DeductorCategories by TestDeductorCategoriesMatchTheMigration --
    -- SIXTEEN LETTERS THAT ARE NOT THE s.402(11) CATEGORIES 0049 STORES. The
    -- two answer different questions ("what letter goes on the return" against
    -- "must this company deduct at all"), use different vocabularies, and
    -- diverge concretely: this list splits Government into Central (A) and
    -- State (S) and has "Branch / Division of Company" (M), neither of which
    -- the statute knows. Mapping between them is a DECISION, not a derivation.
    CONSTRAINT company_tds_profile_category_known
        CHECK (deductor_category IN ('A','S','D','E','G','H','L','N',
                                     'K','M','P','T','J','B','Q','F')),

    -- Shapes only -- form140 owns the Annexure 1 and 5 code LISTS. See header.
    CONSTRAINT company_tds_profile_state_code_shape
        CHECK (state_code ~ '^[0-9]{2}$'),
    CONSTRAINT company_tds_profile_responsible_state_code_shape
        CHECK (responsible_state_code ~ '^[0-9]{2}$'),
    CONSTRAINT company_tds_profile_pincode_shape
        CHECK (pincode ~ '^[0-9]{6}$'),
    CONSTRAINT company_tds_profile_responsible_pincode_shape
        CHECK (responsible_pincode ~ '^[0-9]{6}$'),

    -- The names the format forbids from being only blanks, dots or special
    -- characters. Expressed as "must contain at least one alphanumeric".
    CONSTRAINT company_tds_profile_names_not_blank
        CHECK (deductor_name ~ '[A-Za-z0-9]'
           AND responsible_name ~ '[A-Za-z0-9]'
           AND responsible_designation ~ '[A-Za-z0-9]'),
    CONSTRAINT company_tds_profile_address_present
        CHECK (btrim(address1) <> '' AND btrim(country) <> ''
           AND btrim(responsible_address1) <> '' AND btrim(responsible_country) <> ''),
    CONSTRAINT company_tds_profile_contacts_present
        CHECK (btrim(contact_number) <> '' AND btrim(contact_country_code) <> ''
           AND btrim(responsible_contact_number) <> ''
           AND btrim(responsible_contact_country_code) <> ''),

    -- The e-mail pair rule: the format prints both fields M and then makes each
    -- conditional on the other, so ONE is enough and NONE is not.
    CONSTRAINT company_tds_profile_an_email_is_present
        CHECK (btrim(email) <> '' OR btrim(responsible_email) <> ''),

    -- Annexure 5: mandatory for S, E, H, N; forbidden for everything else.
    CONSTRAINT company_tds_profile_state_name_follows_category
        CHECK (
            (deductor_category IN ('S','E','H','N') AND state_name_code ~ '^[0-9]{2}$')
            OR
            (deductor_category NOT IN ('S','E','H','N') AND state_name_code = '')
        ),
    -- Annexure 3: mandatory for A, D, G; optional for E, H, L, N; forbidden
    -- otherwise.
    CONSTRAINT company_tds_profile_ministry_follows_category
        CHECK (
            (deductor_category IN ('A','D','G') AND btrim(ministry_code) <> '')
            OR
            (deductor_category IN ('E','H','L','N'))
            OR
            (deductor_category NOT IN ('A','D','G','E','H','L','N') AND ministry_code = '')
        ),
    -- Code 99 is "Other", and field 58 exists to name it.
    CONSTRAINT company_tds_profile_ministry_other_iff_99
        CHECK ((ministry_code = '99') = (btrim(ministry_other) <> ''))
);

COMMENT ON TABLE company_tds_profile IS
    'The deductor block of Form 140 -- TAN, PAN, Annexure 4 category, statutory '
    'address, and the person responsible for deduction. The ABSENCE of a row is '
    'the refusal: a company without one cannot have a return built, and the '
    'reporting layer names the gap rather than emitting blank mandatory fields '
    'that the FVU rejects after the operator believes they have filed. Current '
    'state, not history -- a return is signed by whoever is responsible at '
    'FILING time.';

COMMENT ON COLUMN company_tds_profile.tan IS
    'Tax Deduction and Collection Account Number, AAAA99999A. NOT the GSTIN and '
    'not derivable from it.';

COMMENT ON COLUMN company_tds_profile.deductor_name IS
    'The name held against the TAN. Stored separately from company.name because '
    'General Note 13 requires it to match the TAN name in the .csi challan file, '
    'and a difference of punctuation alone fails validation.';

COMMENT ON COLUMN company_tds_profile.deductor_category IS
    'Annexure 4 of the FILE FORMAT (K = Company, Q = Individual/HUF, ...). This '
    'is NOT company_deductor_status.entity_category, which is s.402(11) and '
    'answers whether the company must deduct at all.';

-- ---------------------------------------------------------------------------
-- Row-level security. Same reasoning as every tenant table: ENABLE and FORCE,
-- USING and WITH CHECK. This one carries a named individual's PAN, so a read
-- leak is a personal-data leak and not merely a tenancy bug.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    EXECUTE 'ALTER TABLE company_tds_profile ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE company_tds_profile FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON company_tds_profile';
    EXECUTE 'CREATE POLICY tenant_isolation ON company_tds_profile '
         || 'USING (company_id = app_current_company_id()) '
         || 'WITH CHECK (company_id = app_current_company_id())';
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON company_tds_profile TO tally_app;
