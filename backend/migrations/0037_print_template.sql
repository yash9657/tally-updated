-- 0037_print_template.sql
-- PRINT TEMPLATE OVERRIDES: the per-company, per-document-kind customisation of
-- a printed document. One row = one company's overrides for one DocKind.
--
-- WHY THIS TABLE HOLDS ONLY *OVERRIDES*, AND NEVER A WHOLE TEMPLATE
-- ----------------------------------------------------------------
-- The built-in default template lives in CODE (internal/printing), not here.
-- This table stores only the fields a company chose to CHANGE, so a company that
-- has just been created -- zero rows in this table -- prints a correct Rule 46
-- tax invoice on its first day, with no seed migration, no per-company backfill
-- and nothing to go wrong at 2 a.m. on the day a company is opened.
--
-- The opposite design (store the full template per company, seeded on company
-- creation) fails in two ways this one cannot: a company created before a
-- template change keeps printing the old layout forever with nobody able to tell
-- why, and a seeding step that fails halfway leaves a company that cannot print
-- at all. An absent row here means exactly one thing -- "use the built-in
-- default" -- and printingdb.LoadOverrides returns the zero value with NO error
-- for it, which is what makes that guarantee real rather than aspirational.
--
-- WHY ONE ROW PER KIND AND NOT ONE ROW PER COMPANY
-- -----------------------------------------------
-- A receipt and a tax invoice do not share a declaration, a terms block or a
-- signature label: "Certified that the particulars given above are true and
-- correct" belongs on an invoice, not on a payment voucher. Keying the row by
-- (company_id, doc_kind) is what lets a company customise the invoice without
-- silently changing what its delivery notes say.
--
-- UNIQUE (company_id, doc_kind) is therefore not a tidiness constraint. Two rows
-- for the same kind would make "which template printed this document?"
-- unanswerable: the renderer would pick whichever row the planner returned
-- first, two invoices printed minutes apart could carry different bank details,
-- and there is no evidence afterwards of which one a customer was actually sent.
--
-- WHY THE SCALAR TEXT COLUMNS ARE NOT NULL DEFAULT '' AND THE ARRAYS ARE NULLABLE
-- -----------------------------------------------------------------------------
-- The two halves are deliberately different, because Go's types make them
-- different:
--
--   * printing.TemplateOverrides carries `Title string`, not `*string`. A Go
--     string has NO third state, so NULL and '' would both have to arrive as ""
--     and the distinction could not survive a round trip. Storing '' as the
--     single representation of "not overridden" means the column has exactly as
--     many states as the Go field it maps to -- and a mapping with a state the
--     other side cannot represent is a bug waiting for a reader.
--
--   * `HeaderLines []string` DOES have a third state: nil. So NULL (nil, "no
--     override -- print the built-in lines") and '{}' (an empty, non-nil slice,
--     "override with NOTHING -- print no footer at all") are both meaningful and
--     both reachable, and this is a real business distinction: a company that
--     wants its footer SUPPRESSED is saying something different from a company
--     that has never been asked about its footer. printingdb round-trips both,
--     and TestAnEmptyFooterOverrideIsNotTheSameAsNoFooterOverride pins it.
--
-- WHY doc_kind IS CHECKED AGAINST A CLOSED LIST
-- --------------------------------------------
-- The eight values below ARE printing.DocKind's constants, spelled identically
-- (internal/printing/doc.go). The DocKind strings are part of the on-disk
-- contract, and the CHECK is the same closed vocabulary written a second time on
-- purpose: a kind one side accepts and the other does not becomes a loud INSERT
-- failure at the moment somebody adds a document type, instead of a row that is
-- stored, is never read by any renderer, and looks from the UI like a
-- customisation that silently does not apply.
--
-- RE-RUNNABLE. migrations.go applyOne wraps each file in ONE transaction and
-- stamps schema_migrations INSIDE it, so a file that cannot be re-run walls off
-- every later migration: the documented recovery path (somebody applies a file by
-- hand with `psql -f`, which has no transaction envelope and therefore no stamp,
-- and then runs `cored migrate`) would abort on "already exists", roll back, and
-- no migration after 0037 would ever land. Hence IF NOT EXISTS on the table and
-- a pg_constraint / pg_policies guard on everything that has no such clause.
-- 0028's header records the by-hand run that proved this failure mode is real.

-- ---------------------------------------------------------------------------
-- The composite tenant key on `company`, so print_template can reference the
-- PAIR (company_id, id) rather than the bare id.
--
-- Same argument 0025 made for stock_item, 0030 for account_group and 0034 for
-- ledger, applied to the tenant ROOT. company already carries
-- `company_self_scope CHECK (company_id = id)`, so today the pair is always two
-- copies of one value -- but a plain REFERENCES company (id) would depend on
-- that CHECK still holding to mean anything, and the point of a composite FK is
-- that it needs no such dependence. If a company row ever existed whose
-- company_id and id disagreed, a pair FK would refuse to attach a template to it
-- while a single-column FK would happily accept it and the template would be
-- scoped to a company nobody can see.
--
-- Guarded by name because ALTER TABLE ... ADD CONSTRAINT has no IF NOT EXISTS.
-- Additive: company's primary key and its self-scope CHECK are untouched.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'company_company_scope'
           AND conrelid = 'company'::regclass
    ) THEN
        ALTER TABLE company
            ADD CONSTRAINT company_company_scope UNIQUE (company_id, id);
    END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- print_template.
--
-- It carries a real company_id column, like every other tenant table, and that
-- is load-bearing beyond RLS: internal/platform/testdb derives its teardown plan
-- FROM THE LIVE SCHEMA by enumerating tables with a company_id, so this table is
-- torn down by every adopting test with no registry edit and
-- TestEveryTableIsClassifiedForTeardown stays green.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS print_template (
    id                UUID PRIMARY KEY,
    company_id        UUID NOT NULL,
    -- The DocKind these overrides apply to. Closed vocabulary; see the header.
    doc_kind          TEXT NOT NULL,
    -- Scalars: '' means "not overridden". See the header for why these are NOT
    -- NULL and the arrays below are not.
    title             TEXT NOT NULL DEFAULT '',
    declaration       TEXT NOT NULL DEFAULT '',
    terms             TEXT NOT NULL DEFAULT '',
    signature_label   TEXT NOT NULL DEFAULT '',
    -- Arrays: NULL means "not overridden", '{}' means "override with nothing".
    header_lines      TEXT[] NULL,
    footer_lines      TEXT[] NULL,
    bank_lines        TEXT[] NULL,
    -- Whether the bank block prints at all. It is a separate flag rather than
    -- "bank_lines is non-empty" because a company may want its bank details
    -- staged (entered, checked by somebody senior) before they start appearing
    -- on every invoice that leaves the building. A wrong account number printed
    -- on an invoice is a customer paying a stranger.
    show_bank_details BOOLEAN NOT NULL DEFAULT false,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- ONE override set per kind. See the header: a second row makes "which
    -- template printed this?" unanswerable.
    CONSTRAINT print_template_one_per_kind UNIQUE (company_id, doc_kind),
    -- Not redundant with the primary key: a future child of this table (a logo
    -- blob, a per-column report layout) must be able to reference the PAIR, so
    -- a cross-tenant print_template_id is refused by the FK itself rather than
    -- resting on RLS alone.
    CONSTRAINT print_template_company_scope UNIQUE (company_id, id),
    -- The company FK, as a PAIR. Both referencing columns are company_id on
    -- purpose: the parent key is (company_id, id) and on the tenant root those
    -- are the same value, so "this template's company exists AND is its own
    -- tenant" is one constraint. See the company_company_scope block above.
    CONSTRAINT print_template_company_fk
        FOREIGN KEY (company_id, company_id)
        REFERENCES company (company_id, id),
    -- The eight printing.DocKind values, spelled exactly as internal/printing
    -- declares them. Not an ENUM: adding a document kind would then need a
    -- migration to ALTER TYPE, and CHECK reads the same, guards the same, and is
    -- greppable next to the Go constants.
    CONSTRAINT print_template_doc_kind_known
        CHECK (doc_kind IN (
            'tax-invoice',
            'bill-of-supply',
            'delivery-note',
            'receipt',
            'payment',
            'credit-note',
            'debit-note',
            'report'
        ))
);

-- NO separate index on (company_id). print_template_one_per_kind's btree already
-- has company_id as its LEADING column, so every company-scoped read this table
-- has (one kind, or all kinds for a company) is served by it. A second index on
-- (company_id) alone would be write cost with no read it can serve that the
-- unique index cannot -- which is why this file deviates from the
-- `<table>_company` index that 0002/0030/0034 each add: those tables' uniques
-- are on other columns or they have none.

COMMENT ON TABLE print_template IS
    'Per-company, per-DocKind OVERRIDES of a printed document. The built-in '
    'default template lives in code (internal/printing), so a company with ZERO '
    'rows here prints correctly on its first day and an absent row means '
    'exactly "use the built-in default". At most one row per (company, kind): '
    'two would make "which template printed this document?" unanswerable.';

COMMENT ON COLUMN print_template.doc_kind IS
    'A printing.DocKind value, spelled exactly as internal/printing/doc.go '
    'declares it. The CHECK is that closed vocabulary written a second time, so '
    'an unknown kind fails the INSERT instead of being stored and never read.';

COMMENT ON COLUMN print_template.header_lines IS
    'NULL = not overridden (print the built-in letterhead lines). '
    '''{}'' = overridden with NOTHING (print no extra lines at all). The two are '
    'different instructions and both are reachable from Go, where the field is a '
    'nil-able []string.';

COMMENT ON COLUMN print_template.title IS
    'Replaces the document title. '''' (the default) means not overridden: '
    'printing.TemplateOverrides.Title is a Go string with no third state, so '''' '
    'is the single representation of absence and the column is NOT NULL.';

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, and a policy with BOTH
-- USING and WITH CHECK -- identical to every other tenant table since 0004.
--
-- BOTH CLAUSES, AND WHY EITHER ALONE IS WORSE THAN USELESS HERE:
--   * USING alone filters reads but lets a session INSERT a row for ANOTHER
--     company -- a row it can then never see, cannot correct, and which changes
--     what that other tenant's invoices say. Silent cross-tenant contamination.
--   * WITH CHECK alone would police writes and leak every tenant's overrides to
--     every read.
--
-- FORCE matters because the migrations run as the table OWNER, and an owner
-- BYPASSES its own table's policies unless forced. Without FORCE, any code that
-- ever connected as the owner would silently see and write every tenant's rows.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    EXECUTE 'ALTER TABLE print_template ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE print_template FORCE  ROW LEVEL SECURITY';
    -- Dropped-if-present first so a re-run of a half-applied file does not fail
    -- on a duplicate policy name (CREATE POLICY has no IF NOT EXISTS).
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON print_template';
    EXECUTE 'CREATE POLICY tenant_isolation ON print_template '
         || 'USING (company_id = app_current_company_id()) '
         || 'WITH CHECK (company_id = app_current_company_id())';
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON print_template TO tally_app;
