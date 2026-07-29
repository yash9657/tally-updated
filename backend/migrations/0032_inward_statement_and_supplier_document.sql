-- 0032_inward_statement_and_supplier_document.sql
-- THE SUPPLIER'S OWN DOCUMENT, ON THE BOOKS -- plus the GSTR-2A/2B statement,
-- the IMS action log and the filing instant that freezes it.
--
-- WHAT CHANGED, AND WHY THIS IS NOT OPTIONAL PAPERWORK
-- ----------------------------------------------------
-- GET /gstr3b reports input tax credit as ALL-ELIGIBLE. It has to, because the
-- one fact every ITC reconciliation is keyed on -- THE SUPPLIER'S INVOICE
-- NUMBER -- is stored NOWHERE in this database as a typed field. Not in one
-- place badly; in no place at all. Three columns look like they hold it and
-- none of them does:
--
--   * bill_allocation.ref_name. A Tally BILL reference. It exists only when the
--     party ledger has maintain_bill_by_bill = true AND the operator typed
--     something into the bill-wise sub-screen. One bill reference legitimately
--     spans several invoices ("MARCH-RUNNING"), and several references
--     legitimately split one invoice into instalments. It is populated on all
--     three of Demo Traders' purchase-side vouchers, which is exactly why it
--     LOOKS reliable to anyone who checks the only data anybody eyeballs.
--
--   * voucher_version.voucher_no. OUR numbering -- allocated by
--     internal/ledger/numbering.go, with no UNIQUE constraint and no relation
--     to anything the supplier printed. Worse, it currently carries TWO
--     meanings: insightd's OCR mapper writes the SCANNED invoice number into
--     it (adapters/mapper.py, `voucher_no = inferred.invoice_no`) while a
--     hand-keyed purchase gets an auto serial. A column with two meanings
--     cannot be matched against anything.
--
--   * voucher_external_reference (0009). CHECK-constrained to ('IRN',
--     'FILED_GSTR1'), and -- decisively -- registering a row HARD-FREEZES the
--     voucher against alter and delete. A purchase must stay alterable; the
--     whole point of reconciliation is that you find out later that you keyed
--     something wrong.
--
-- Without a typed supplier document number, matching a 2B is not "approximate",
-- it is impossible: there is no key. And an ITC figure that nobody can tie to
-- what suppliers actually filed is the single largest correctness gap in this
-- product's compliance surface. It is the number the department cross-checks
-- FIRST.
--
-- THE JUDGEMENT: A COLUMN ON voucher_version AND A FIELD IN THE HASH,
-- NOT A SIDE TABLE
-- ------------------------------------------------------------------
-- voucher_version is APPEND-ONLY. An ALTER does not update a row, it writes a
-- NEW version and repoints voucher_head.current_version_id. That single fact
-- decides this design.
--
-- A side table keyed (company_id, voucher_version_id) would be written by
-- whatever code path happened to know about it. Unless that write lives INSIDE
-- ledger.post()'s transaction, the row silently ORPHANS on the next ALTER: the
-- new current version has no side row, so the supplier's invoice number
-- disappears from an amended purchase, the matcher finds no key, and the report
-- tells the filer their supplier did not file. That is a false accusation
-- produced by a schema choice. And if the side row IS written inside post(),
-- then it is the same coupling to internal/ledger that a column has -- with an
-- extra join on every read, an extra RLS surface, and an extra restatement
-- guard to get wrong. Zero benefit, three new ways to be wrong.
--
-- So: four nullable columns on voucher_version, written inside post()'s
-- transaction from VoucherInput.Inward, and CARRIED IN THE HASHED CANONICAL
-- PAYLOAD (internal/ledger/hash.go, CanonicalInward).
--
-- Hashing it is the part that deserves a sentence of its own. "Which supplier
-- document is this credit claimed against" is the question a GST officer asks,
-- so it is part of the voucher's AUDIT IDENTITY, not decoration beside it. An
-- unhashed column can be changed by anything that can UPDATE -- and the price
-- provenance lesson in HANDOFF.md section 6 is the same lesson: a figure that
-- drives an outcome and is not signed is a figure nobody can defend. The cost
-- of retrofitting a hashed field rises with every voucher ever written, so it
-- is decided now, at four vouchers, rather than later at four million.
--
-- ITC ELIGIBILITY IS AN INPUT, NEVER AN INFERENCE
-- -----------------------------------------------
-- ledger.itc_eligibility is the per-ledger DEFAULT ('Motor Car Expenses' is
-- blocked for us); voucher_version.itc_eligibility is the per-voucher OVERRIDE.
-- Neither can be derived from the HSN code, and no amount of cleverness will
-- change that: every section 17(5) clause turns on the USE of the thing and on
-- the buyer's own business, not on the goods. The identical car is blocked
-- credit for us and fully creditable for a taxi operator. An engine that
-- guesses will either deny credit that is real or claim credit that is blocked,
-- and both of those are notices.
--
-- The CHECK holds the four ELIGIBILITY values only and NOT the clause letters.
-- The list of 17(5) clauses in circulation is secondary material that must be
-- re-verified against the bare Act before release; keeping the clause as free
-- text means that re-verification is a documentation change rather than a
-- migration.
--
-- WHAT IS DELIBERATELY NOT HERE
-- -----------------------------
--   * NO UNIQUE on (company_id, party_ledger_id, supplier_invoice_no). A
--     supplier genuinely restarting its number series each financial year is an
--     ordinary event, not corruption. Refusing valid data at INSERT is strictly
--     worse than reporting it: the operator cannot key the bill they are
--     holding. The matcher reports two live vouchers sharing a normalised key
--     as AMBIGUOUS and pairs NEITHER, which is the honest answer. A plain
--     partial index gives the lookup its speed without the refusal.
--
--   * NO normalised/generated column. Document-number normalisation lives in
--     exactly one place, the Go function itc.NormalizeDocNo. A normalisation
--     stated twice is a normalisation that drifts, and the SQL copy would drift
--     silently -- it would keep matching, just differently.
--
--   * NO gst_itc_match TABLE. The match result is NEVER stored. It is a
--     function of four independently versioned inputs (the books, the
--     statement, the action log, the policy) and goes stale the instant any one
--     of them moves -- exactly the argument 0025 made for stock VALUE. It is
--     recomputed on read.
--
-- WHY EVERY FK IN HERE IS COMPOSITE ON (company_id, id)
-- -----------------------------------------------------
-- Verbatim 0025's reasoning. RLS hides another tenant's rows from a SELECT, but
-- a plain REFERENCES gst_inward_statement (id) would happily accept a statement
-- id belonging to another company if one ever leaked in through a request body,
-- and the document would then be invisible to BOTH tenants while feeding one
-- tenant's ITC. The composite FK makes the DATABASE refuse it, with no
-- dependence on the session GUC being right.
--
-- MONEY IS NUMERIC(19,4) EVERYWHERE IN HERE, and the Go side decodes the GSTN
-- JSON through json.Number -> decimal.NewFromString. Never float64. A rupee
-- that arrives through a float has already lost the argument with the officer.

-- ---------------------------------------------------------------------------
-- The composite tenant key on ledger, so a supplier ledger can be referenced
-- as a PAIR. Same argument 0025 and 0030 made for stock_item and account_group.
--
-- VERIFIED before writing this: `ledger` carries only ledger_pkey,
-- ledger_company_id_name_key (company_id, name) and ledger_group_id_fkey. There
-- is NO (company_id, id) unique, so every composite FK pointed at a supplier
-- ledger -- here or in any later wave -- fails to create without this. It is
-- additive: the primary key and the existing (company_id, name) unique are
-- untouched.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'ledger_company_scope'
    ) THEN
        ALTER TABLE ledger
            ADD CONSTRAINT ledger_company_scope UNIQUE (company_id, id);
    END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- THE SUPPLIER'S DOCUMENT, ON THE VERSION ROW.
--
-- All four are NULLABLE and there is NO BACKFILL, deliberately. Every purchase
-- posted before this migration genuinely has no supplier document number, and
-- inventing one would be worse than admitting it. The matcher classifies those
-- vouchers NOT_COMPARABLE -- which is a DIFFERENT WORD from MISSING_IN_2B and
-- produces ZERO reversal. Calling a legacy purchase "missing in 2B" would tell
-- the filer their supplier did not file (a lie) and reverse credit that is
-- perfectly good (a real loss of money).
--
-- These columns are also carried in the HASHED payload; see hash.go. The column
-- and the payload field are written from ONE source inside ONE transaction, so
-- they cannot disagree -- the same discipline voucher_gst_line and
-- stock_movement already follow.
-- ---------------------------------------------------------------------------
ALTER TABLE voucher_version
    ADD COLUMN IF NOT EXISTS supplier_invoice_no   TEXT NULL;
ALTER TABLE voucher_version
    ADD COLUMN IF NOT EXISTS supplier_invoice_date DATE NULL;
ALTER TABLE voucher_version
    ADD COLUMN IF NOT EXISTS itc_eligibility       TEXT NULL;
ALTER TABLE voucher_version
    ADD COLUMN IF NOT EXISTS itc_blocked_clause    TEXT NULL;

COMMENT ON COLUMN voucher_version.supplier_invoice_no IS
    'The number the SUPPLIER printed on the document, verbatim as keyed. NOT '
    'voucher_no (ours) and NOT bill_allocation.ref_name (a Tally bill '
    'reference). Normalisation for matching happens in Go, at match time, in '
    'itc.NormalizeDocNo -- nothing normalised is stored.';
COMMENT ON COLUMN voucher_version.supplier_invoice_date IS
    'The date on the supplier''s document. Statutory: it fixes the financial '
    'year for section 16(4) and therefore the date the credit expires, which is '
    'why the matcher tolerates ZERO days of drift on it.';
COMMENT ON COLUMN voucher_version.itc_eligibility IS
    'Per-voucher OVERRIDE of ledger.itc_eligibility. NULL means "the ledger''s '
    'default applies". Same closed vocabulary as ledger.itc_eligibility.';

-- The eligibility vocabulary on the version row is enforced by a named CHECK
-- rather than inline, so a re-run of a half-applied migration finds it by name.
-- NULL passes: NULL means "inherit the ledger", not "unknown value".
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'voucher_version_itc_eligibility_check'
    ) THEN
        ALTER TABLE voucher_version
            ADD CONSTRAINT voucher_version_itc_eligibility_check
            CHECK (itc_eligibility IS NULL OR itc_eligibility IN
                   ('ELIGIBLE', 'BLOCKED_17_5', 'CAPITAL_GOODS', 'ISD'));
    END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- The matcher's probe: every live purchase of a period, by party, that carries
-- a supplier document number.
--
-- PARTIAL, and NOT UNIQUE. See the header. The WHERE clause keeps the index off
-- every voucher ever posted before this migration and off every sales voucher
-- forever -- which is most of the table.
-- ---------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS voucher_version_supplier_doc
    ON voucher_version (company_id, party_ledger_id, supplier_invoice_no)
    WHERE supplier_invoice_no IS NOT NULL;

-- ---------------------------------------------------------------------------
-- ITC ELIGIBILITY ON THE LEDGER MASTER -- the default the voucher inherits.
--
-- NOT NULL DEFAULT 'ELIGIBLE' IS THE BACKFILL. Postgres materialises a default
-- on an existing table as a catalogue change, not a table rewrite, so this is
-- one statement over the whole fleet and there is NO PER-COMPANY LOOP ANYWHERE
-- IN THIS FILE. 0028's header records what a loop costs here: 38,748 companies
-- reached 13% in one hour and fifty-seven minutes before it was killed.
--
-- 'ELIGIBLE' is the right default because it is the status quo: today every
-- rupee of input tax is reported as available, so defaulting to ELIGIBLE
-- changes no existing answer. Blocking is a decision somebody makes about a
-- specific ledger, and it should have to be made.
-- ---------------------------------------------------------------------------
ALTER TABLE ledger
    ADD COLUMN IF NOT EXISTS itc_eligibility TEXT NOT NULL DEFAULT 'ELIGIBLE';
ALTER TABLE ledger
    ADD COLUMN IF NOT EXISTS blocked_clause  TEXT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'ledger_itc_eligibility_check'
    ) THEN
        ALTER TABLE ledger
            ADD CONSTRAINT ledger_itc_eligibility_check
            CHECK (itc_eligibility IN
                   ('ELIGIBLE', 'BLOCKED_17_5', 'CAPITAL_GOODS', 'ISD'));
    END IF;
END
$$;

COMMENT ON COLUMN ledger.itc_eligibility IS
    'Default ITC treatment of input tax booked against this ledger. A '
    'CLASSIFICATION INPUT, never inferred: section 17(5) turns on the USE of '
    'the supply and on the buyer''s business, not on the goods.';
COMMENT ON COLUMN ledger.blocked_clause IS
    'Free text, e.g. "17(5)(a)". Deliberately NOT in a CHECK: the clause list '
    'in circulation is secondary material pending re-verification against the '
    'bare Act, and that re-verification must be a doc change, not a migration.';

-- ===========================================================================
-- THE INGESTED STATEMENT AND WHAT THE FILER DID ABOUT IT
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- gst_inward_statement -- ONE ingested GSTR-2A or GSTR-2B file.
--
-- APPEND-ONLY. The raw JSON is kept verbatim in `raw` because the file IS the
-- evidence: when a figure is challenged, "this is the bytes GSTN served us, and
-- here is their checksum" is the only answer that ends the conversation. A
-- parsed projection alone would leave us arguing about our own parser.
--
-- UNIQUE (company_id, kind, return_period, source_sha256) -- re-uploading the
-- IDENTICAL file is idempotent, and a DIFFERENT file for the same period is a
-- NEW row that SUPERSEDES the previous one rather than overwriting it. GSTN
-- regenerates 2B without limit whenever an IMS action changes (IMS FAQ 27), so
-- "the 2B for July" is not one document, it is a series -- and a return that
-- cannot name WHICH 2B it was derived from is not explainable to anybody.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS gst_inward_statement (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('GSTR2A', 'GSTR2B')),
    -- MMYYYY, the form the GSTN payloads use ("072025" is July 2025). Stored as
    -- the portal states it rather than as a date, so a round trip back to a
    -- GSTN artifact is lossless.
    return_period CHAR(6) NOT NULL CHECK (return_period ~ '^[0-9]{6}$'),
    -- The GSTIN the statement was generated FOR: us. Kept so a file uploaded
    -- into the wrong company is detectable by reading the file, not by trusting
    -- whoever uploaded it.
    filer_gstin   TEXT NULL,
    -- GSTN's own generation date. THE MATCHER PREFERS THIS OVER THE COMPUTED
    -- 14th when resolving deemed-accept: the 14th is not statutory and has
    -- already slipped (2B for December 2024 was generated on 16 January 2025).
    gen_date      DATE NULL,
    chksum        TEXT NULL,
    file_version  TEXT NULL,
    source_sha256 CHAR(64) NOT NULL,
    raw           JSONB NOT NULL,
    ingested_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    ingested_by   TEXT NOT NULL,
    -- Set ONCE, on the OLD row, when a newer file for the same period arrives.
    -- It is the only mutable field in this table and the GRANT below is
    -- column-scoped to say so in a way Postgres enforces.
    superseded_by UUID NULL,
    UNIQUE (company_id, kind, return_period, source_sha256),
    -- Composite so gst_inward_document can reference the PAIR.
    CONSTRAINT gst_inward_statement_company_scope UNIQUE (company_id, id),
    CONSTRAINT gst_inward_statement_superseded_fk
        FOREIGN KEY (company_id, superseded_by)
        REFERENCES gst_inward_statement (company_id, id)
);

CREATE INDEX IF NOT EXISTS gst_inward_statement_period
    ON gst_inward_statement (company_id, kind, return_period, ingested_at DESC);

-- ---------------------------------------------------------------------------
-- gst_inward_document -- ONE document out of a statement.
--
-- `rejected` is the field with teeth. GSTN serves rejected records in a
-- separate `docRejdata` block, and those records are VIEW-ONLY (IMS FAQ 24):
-- they are shown so the filer knows what was thrown away, and they MUST NEVER
-- feed ITC. Summing them is a filing-grade overclaim, so it is a column and a
-- named test rather than an assumption about which parser branch ran.
--
-- itc_available carries GSTN'S OWN VERDICT (the `itcavl` flag). It OVERRIDES
-- our computation when the two disagree, because GSTN's determination is the
-- one the department will use -- but the disagreement is recorded as a
-- reconciliation FINDING rather than silently resolved. GSTN marks itcavl='N'
-- for exactly two reasons, section 16(4) expiry and the place-of-supply rule,
-- and expressly disclaims the rest: 2B DOES NOT FLAG SECTION 17(5).
--
-- doc_date is NOT constrained to fall inside return_period, and that is a
-- correctness requirement rather than laxity. The 2B window is on the
-- SUPPLIER'S FILING DATE: an invoice dated 15-05-2020 filed in the July 2020
-- GSTR-1 lands in the JULY 2B (GSTN 2B Advisory Q.4). A CHECK tying doc_date to
-- the period would reject the ordinary late-filing case outright.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS gst_inward_document (
    id                     UUID PRIMARY KEY,
    company_id             UUID NOT NULL,
    statement_id           UUID NOT NULL,
    section                TEXT NOT NULL CHECK (section IN (
                               'B2B', 'B2BA', 'CDNR', 'CDNRA',
                               'IMPG', 'IMPGSEZ', 'ISD', 'ISDA',
                               'ECOM', 'ECOMA')),
    -- true == came from docRejdata. VIEW ONLY. Never feeds ITC.
    rejected               BOOLEAN NOT NULL DEFAULT false,
    supplier_gstin         TEXT NULL,
    supplier_trade_name    TEXT NULL,
    supplier_return_period CHAR(6) NULL,
    supplier_filed_date    DATE NULL,
    doc_no                 TEXT NOT NULL,
    doc_date               DATE NOT NULL,
    -- I invoice, C credit note, D debit note. IN THE MATCH KEY, deliberately: a
    -- credit note whose number equals the invoice it adjusts is common, and a
    -- matcher without doc_type in the key pairs the two and claims the credit
    -- twice.
    doc_type               CHAR(1) NULL CHECK (doc_type IS NULL OR doc_type IN ('I', 'C', 'D')),
    doc_value              NUMERIC(19,4) NULL,
    pos                    CHAR(2) NULL,
    reverse_charge         BOOLEAN NOT NULL DEFAULT false,
    itc_available          BOOLEAN NOT NULL,
    itc_unavailable_reason TEXT NULL,
    taxable_value          NUMERIC(19,4) NOT NULL DEFAULT 0,
    cgst                   NUMERIC(19,4) NOT NULL DEFAULT 0,
    sgst                   NUMERIC(19,4) NOT NULL DEFAULT 0,
    igst                   NUMERIC(19,4) NOT NULL DEFAULT 0,
    cess                   NUMERIC(19,4) NOT NULL DEFAULT 0,
    -- The amendment's pointer at what it amends, on the amendment record's OWN
    -- terms. GSTN states the precedence explicitly, so nothing here is inferred.
    orig_doc_no            TEXT NULL,
    orig_doc_date          DATE NULL,
    ims_status_in_file     TEXT NULL,
    irn                    TEXT NULL,
    CONSTRAINT gst_inward_document_company_scope UNIQUE (company_id, id),
    CONSTRAINT gst_inward_document_statement_fk
        FOREIGN KEY (company_id, statement_id)
        REFERENCES gst_inward_statement (company_id, id)
);

CREATE INDEX IF NOT EXISTS gst_inward_document_statement
    ON gst_inward_document (company_id, statement_id);
-- The matcher's other probe: everything one supplier filed, in date order.
CREATE INDEX IF NOT EXISTS gst_inward_document_supplier
    ON gst_inward_document (company_id, supplier_gstin, doc_date);

-- ---------------------------------------------------------------------------
-- gst_ims_action -- the IMS action LOG.
--
-- APPEND-ONLY, WITH NO UPDATE EVER. GSTN's own semantics are last-write-wins
-- ("the latest action will overwrite the previous action", IMS FAQ 12), and the
-- naive way to implement that is one mutable status column. That would be
-- wrong twice over: the filer's own history of what they decided and when is
-- itself audit evidence, and -- decisively -- the EFFECTIVE action depends on
-- an `asOn` instant, because an action taken after the GSTR-3B was filed does
-- not count at all. A single status column cannot answer "what was the
-- effective action at the moment we filed", and that is the only question the
-- return actually asks.
--
-- So the effective action is a PROJECTION over this log, computed in
-- itc.EffectiveAction, which takes `asOn` explicitly and never reads the wall
-- clock.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS gst_ims_action (
    id          UUID PRIMARY KEY,
    company_id  UUID NOT NULL,
    document_id UUID NOT NULL,
    action      TEXT NOT NULL CHECK (action IN ('ACCEPT', 'REJECT', 'PENDING', 'RESET')),
    reason      TEXT NULL,
    acted_at    TIMESTAMPTZ NOT NULL,
    acted_by    TEXT NOT NULL,
    CONSTRAINT gst_ims_action_company_scope UNIQUE (company_id, id),
    CONSTRAINT gst_ims_action_document_fk
        FOREIGN KEY (company_id, document_id)
        REFERENCES gst_inward_document (company_id, id)
);

-- DESC on acted_at because every read of this table is "the latest action at or
-- before an instant", never "the earliest".
CREATE INDEX IF NOT EXISTS gst_ims_action_latest
    ON gst_ims_action (company_id, document_id, acted_at DESC);

-- ---------------------------------------------------------------------------
-- gst_return_filing -- THE FREEZE INSTANT.
--
-- Filing the GSTR-3B for a period freezes the IMS action set for that period
-- (IMS FAQ 12). Without a STORED instant, recomputing the same period on two
-- different days yields two different answers, because actions taken in between
-- would keep sliding into scope. A return that changes when you look at it
-- again is not a return.
--
-- One row per (company, form, period), UNIQUE. `form` is CHECK-constrained to
-- GSTR3B alone rather than left open: this table means "the thing that freezes
-- IMS", and the day another form needs an instant, whoever adds it should have
-- to think about whether it freezes anything.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS gst_return_filing (
    id            UUID PRIMARY KEY,
    company_id    UUID NOT NULL,
    form          TEXT NOT NULL CHECK (form IN ('GSTR3B')),
    return_period CHAR(6) NOT NULL CHECK (return_period ~ '^[0-9]{6}$'),
    filed_at      TIMESTAMPTZ NOT NULL,
    filed_by      TEXT NOT NULL,
    UNIQUE (company_id, form, return_period),
    CONSTRAINT gst_return_filing_company_scope UNIQUE (company_id, id)
);

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE + FORCE, USING + WITH CHECK pinned
-- to the total app_current_company_id() resolver -- identical to every other
-- tenant table since 0004. The policy is dropped-if-present first so a re-run
-- of a half-applied migration does not fail on a duplicate policy name.
--
-- FORCE is not decoration. Without it the TABLE OWNER bypasses the policy, and
-- the owner is the role migrations and several maintenance paths connect as --
-- so a query that looked tenant-safe in every test would return the whole fleet
-- the one time it ran as the owner. TestInwardTablesAreForceRLS asserts both
-- flags per table, by name.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'gst_inward_statement', 'gst_inward_document',
        'gst_ims_action', 'gst_return_filing'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I '
            || 'USING (company_id = app_current_company_id()) '
            || 'WITH CHECK (company_id = app_current_company_id())',
            t);
    END LOOP;
END
$$;

-- ---------------------------------------------------------------------------
-- Least-privilege grants.
--
-- SELECT and INSERT only -- NO UPDATE and NO DELETE on any of the four. All
-- four are append-only by design, and here that is enforced by the privilege
-- system rather than asserted in a comment: tally_app is the role the API runs
-- as, and it simply cannot issue the statement.
--
-- The ONE exception is deliberate and is scoped to a single COLUMN.
-- gst_inward_statement.superseded_by has to be set on the OLD row when a newer
-- file for the same period arrives -- the pointer runs old -> new, which is the
-- direction a reader needs ("is this the current 2B?") and the direction that
-- cannot be written at INSERT time. A column-level GRANT UPDATE keeps every
-- other field of an ingested statement, including `raw` and `source_sha256`,
-- immutable to the application: the evidence cannot be edited, only chained.
-- ---------------------------------------------------------------------------
GRANT SELECT, INSERT ON
    gst_inward_statement, gst_inward_document, gst_ims_action, gst_return_filing
    TO tally_app;
GRANT UPDATE (superseded_by) ON gst_inward_statement TO tally_app;

COMMENT ON TABLE gst_inward_statement IS
    'One ingested GSTR-2A/2B file, verbatim. Append-only; a newer file for the '
    'same period supersedes rather than replaces, because 2B is regenerated '
    'without limit and a return must be able to name which 2B it came from.';
COMMENT ON TABLE gst_inward_document IS
    'One document from a statement. rejected=true rows came from docRejdata: '
    'view-only, and they must never feed ITC.';
COMMENT ON TABLE gst_ims_action IS
    'Append-only IMS action log. The effective action is a projection over this '
    'log at an explicit instant, never a stored status.';
COMMENT ON TABLE gst_return_filing IS
    'The instant a form was filed. Filing GSTR-3B freezes the IMS action set '
    'for that period; without a stored instant the same period recomputes '
    'differently every time.';
