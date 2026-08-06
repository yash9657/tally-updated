-- 0047_e_way_bill.sql
-- T5 -- WHERE A RAISED e-WAY BILL IS KEPT.
--
-- Migration number from PLAN-TALLY-PARITY.md §2.5 and HANDOFF §6, both of which
-- reserve 0047-0048 for T5. This takes 0047. ⛔ 0048 IS STILL FREE and this
-- migration deliberately does not spend it: HANDOFF §6's rule is "reserve blocks
-- for TABLES, take the seeder number at the end", and T5 needs no ACL seeder at
-- all (see the ACL note at the foot of this file).
--
-- ===========================================================================
-- ⛔⛔ THE IDENTIFIER IN THIS TABLE IS NOT AN EBN, AND THE COLUMN IS NAMED SO
-- THAT NOBODY CAN FORGET IT
-- ===========================================================================
-- An EBN (e-Way Bill Number) is a 12-digit identifier ISSUED BY NIC. The NIC
-- e-Way Bill API is unreachable from this deployment, so nothing here has ever
-- been submitted to anybody. What is stored is:
--
--   local_reference -- a deterministic LOCAL identifier, prefixed "LOCAL-EWB-"
--                      and deliberately not 12 digits; and
--   payload         -- the exact JSON that WOULD be submitted.
--
-- The column is `local_reference` and NOT `ebn`, `e_way_bill_no` or `number`,
-- because a column name is the shortest documentation there is and the wrong one
-- would be copied onto a screen and then onto a printed sheet. A document
-- carrying a fabricated EBN tells a driver at a checkpost that the consignment
-- is covered when it is not -- which is worse than printing nothing, and is the
-- same argument invQRNotPrintedNotice records for the rule 48(4) QR code.
--
-- ===========================================================================
-- WHY THE ROW IS KEYED ON voucher_id AND NOT ON voucher_version_id
-- ===========================================================================
-- HANDOFF §4.3 requires every new table to answer this, because four tables have
-- now been caught destroying data on the way out. The answer here is that this
-- table is NOT in that class, and the reason is worth stating precisely.
--
-- order_fulfilment, posting, stock_movement and posting_cost_allocation are all
-- keyed on voucher_version_id and are RE-STATED by the voucher write path: an
-- ALTER writes a new version and rebuilds them from the request body, so
-- whatever the body omits is deleted. That is the §4.3 hazard.
--
-- An e-way bill is raised against the DOCUMENT, not against a version of it, and
-- it is NEVER written by the voucher path -- ledger.post()/alter() do not touch
-- this table and voucherFullJSON does not emit it. An ALTER therefore cannot
-- re-state it and cannot drop it. It is keyed on (company_id, voucher_id).
--
-- ⚠️ BUT THE ALTER STILL MATTERS, AND THIS IS THE PART A NAIVE DESIGN MISSES.
-- If the invoice is altered after the bill is raised -- the amount changes, a
-- line is added, the party changes -- then the payload on file no longer
-- describes the invoice it names. Nothing breaks, nothing refuses, and the
-- stored document silently becomes a claim about a voucher that no longer says
-- that.
--
-- So generated_from_version_id records WHICH VERSION the payload was built from,
-- as a FACT and not as a key. A reader compares it to
-- voucher_head.current_version_id: if they differ, the invoice has been altered
-- since the bill was raised and the bill is STALE. That is a derived flag with
-- no stored copy -- deliberately, and for the reason HANDOFF §8.2 gives for
-- refusing a fulfilled_qty column: a stored staleness flag goes wrong the first
-- time somebody alters a voucher without visiting this table, which is every
-- time.
--
-- ===========================================================================
-- ONE LIVE BILL PER VOUCHER, AND REGENERATION IS COUNTED RATHER THAN SILENT
-- ===========================================================================
-- UNIQUE (company_id, voucher_id). Two reasons, and the first is mechanical:
--
--   The local reference is DETERMINISTIC in the document (it is derived from
--   docNo and the seller GSTIN). Two rows for one voucher would therefore carry
--   THE SAME identifier and be indistinguishable from each other -- a schema
--   that permits two rows claiming to be the same thing.
--
--   And it matches the real model: NIC updates Part B on an existing EBN. It
--   does not issue a second bill because a vehicle changed.
--
-- Regeneration (a corrected distance, a vehicle finally assigned) UPDATEs the
-- row and increments generation_count. It is counted rather than silent because
-- replacing a statutory-shaped artefact without trace is the kind of thing this
-- schema should not make easy: a reader seeing generation 3 knows to ask what
-- the first two said.
--
-- ⚠️ THIS IS NOT AN AUDIT TRAIL AND MUST NOT BE READ AS ONE. The superseded
-- payloads are GONE -- only the count survives. A full history would be a second
-- table, and it is not built here because nothing in this build can submit a
-- bill, so no superseded generation was ever relied on by anyone. The day a real
-- provider exists that changes, and the history table is the first thing to add.

CREATE TABLE IF NOT EXISTS e_way_bill (
    id                       UUID PRIMARY KEY,
    company_id               UUID NOT NULL,

    -- The document the movement is against. The HEAD, not a version -- see the
    -- header.
    voucher_id               UUID NOT NULL,

    -- ⛔ WHICH VERSION THE PAYLOAD WAS BUILT FROM. A fact, not a key. Compare it
    -- to voucher_head.current_version_id to detect that the invoice has been
    -- altered since the bill was raised. NOT a foreign key to voucher_version
    -- with ON DELETE anything: this must survive as a record of what was
    -- generated even if that version is superseded.
    generated_from_version_id UUID NOT NULL,

    -- ⛔ A LOCAL, DETERMINISTIC IDENTIFIER. NOT AN EBN. NEVER LABEL IT ONE.
    local_reference          TEXT NOT NULL,

    -- Which adapter answered. Carried and never assumed, because the disclosure
    -- block NAMES it ("generated by ...") and the whole honesty of the artefact
    -- rests on a reader learning that it was a stand-in.
    provider                 TEXT NOT NULL,

    -- The exact JSON that WOULD be submitted to NIC.
    payload                  JSONB NOT NULL,

    -- The disclosure block, as an ordered JSON array of strings. ⛔ STORED
    -- RATHER THAN RE-DERIVED ON READ, because it is what was shown to the
    -- operator at the moment they raised the bill. Re-deriving it later would
    -- silently restate today's rules over yesterday's document -- and the whole
    -- point of rule138.go's provenance block is that these figures are PENDING
    -- RE-VERIFICATION and will change.
    disclosures              JSONB NOT NULL,

    -- ===================================================================
    -- THE ASSESSMENT'S CONCLUSIONS, MACHINE-READABLE
    -- ===================================================================
    -- Stored as booleans as well as in the prose basis, so a screen or a
    -- register can filter without parsing English. The prose remains the
    -- authority on WHY; these are only the WHAT.

    -- Whether the consignment value reached the threshold at all. A bill raised
    -- below the threshold is legal and sometimes wanted -- the basis says it was
    -- not required, and that sentence reaches the artefact.
    assessed_required        BOOLEAN NOT NULL,
    -- Whether Part B (the vehicle number) is required for this movement.
    part_b_required          BOOLEAN NOT NULL,

    -- ⛔ NOT NULL, AND THAT IS AN INVARIANT THE WRITE PATH ENFORCES RATHER THAN
    -- A CONVENIENCE. Assess refuses to compute a validity from an unstated
    -- distance -- it returns a blocker instead of quietly granting the one-day
    -- minimum. cored turns that case into a 422 and stores nothing, so a row can
    -- only exist when the validity was actually computed. A nullable column here
    -- would make "we do not know when this expires" a storable state, and an
    -- e-way bill whose expiry the system guessed is a detained vehicle.
    valid_until              DATE NOT NULL,
    validity_days            INTEGER NOT NULL CHECK (validity_days >= 1),

    -- ===================================================================
    -- THE CONSIGNMENT AS THE OPERATOR STATED IT
    -- ===================================================================
    -- Kept so the row is self-describing and a regeneration can be diffed
    -- against what it replaced. Everything here is an INPUT to Assess, so a
    -- reader can re-run the assessment and get the same answer.

    from_pincode             TEXT NOT NULL,
    to_pincode               TEXT NOT NULL,
    from_state_code          TEXT NOT NULL,
    to_state_code            TEXT NOT NULL,

    transporter_id           TEXT NOT NULL DEFAULT '',
    transporter_name         TEXT NOT NULL DEFAULT '',

    -- Part B. Empty is legal: an intra-State short hop does not require it, and
    -- a Part-A-only bill is a real thing an operator raises while the vehicle is
    -- still being arranged. When part_b_required is true and this is empty, the
    -- blocker saying the goods may not move is IN the disclosures.
    vehicle_number           TEXT NOT NULL DEFAULT '',
    transport_mode           TEXT NOT NULL DEFAULT ''
        CHECK (transport_mode IN ('', 'ROAD', 'RAIL', 'AIR', 'SHIP')),

    -- ⛔ CHECK (> 0), NOT (>= 0). Zero means "not stated" to Assess and is not a
    -- zero-distance movement; that case never reaches storage (it is the 422
    -- above). Permitting 0 here would let the one state Assess refuses to reason
    -- about become a stored fact.
    distance_km              INTEGER NOT NULL CHECK (distance_km > 0),

    -- ⛔ ASSERTED BY THE OPERATOR, NEVER INFERRED. Nothing in a voucher says
    -- whether a consignment is over-dimensional, and assuming ordinary cargo
    -- grants TEN TIMES the validity the rule allows (200 km/day vs 20). The
    -- column has no DEFAULT for exactly that reason: the write path must state
    -- it, so it cannot be acquired by omission.
    over_dimensional_cargo   BOOLEAN NOT NULL,

    -- ===================================================================

    -- How many times a bill has been raised for this voucher. 1 on first
    -- generation. See the header: counted, not silent, and NOT an audit trail.
    generation_count         INTEGER NOT NULL DEFAULT 1 CHECK (generation_count >= 1),

    -- When the bill was raised. Fed to Assess as GeneratedAt rather than read
    -- from the clock inside it, which is what makes the validity reproducible
    -- and the golden test a byte comparison.
    generated_at             TIMESTAMPTZ NOT NULL,

    created_by               TEXT NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT e_way_bill_company_scope UNIQUE (company_id, id),

    -- ⛔ ONE LIVE BILL PER VOUCHER. See the header.
    CONSTRAINT e_way_bill_one_per_voucher UNIQUE (company_id, voucher_id),

    CONSTRAINT e_way_bill_local_reference_not_blank
        CHECK (btrim(local_reference) <> ''),
    CONSTRAINT e_way_bill_provider_not_blank
        CHECK (btrim(provider) <> ''),
    -- The disclosure block is the point of the artefact. An empty array would be
    -- a document with no statement that it is not a government e-way bill.
    CONSTRAINT e_way_bill_disclosures_present
        CHECK (jsonb_typeof(disclosures) = 'array' AND jsonb_array_length(disclosures) > 0),
    CONSTRAINT e_way_bill_payload_is_object
        CHECK (jsonb_typeof(payload) = 'object'),

    CONSTRAINT e_way_bill_company_fk
        FOREIGN KEY (company_id) REFERENCES company (id),
    CONSTRAINT e_way_bill_voucher_fk
        FOREIGN KEY (company_id, voucher_id)
        REFERENCES voucher_head (company_id, voucher_id)
);

CREATE INDEX IF NOT EXISTS e_way_bill_by_voucher
    ON e_way_bill (company_id, voucher_id);
-- "What is expiring" -- the register read an operator actually wants.
CREATE INDEX IF NOT EXISTS e_way_bill_by_validity
    ON e_way_bill (company_id, valid_until);

COMMENT ON TABLE e_way_bill IS
    'A raised e-way bill: the NIC payload that WOULD be submitted, plus the '
    'Rule 138 assessment that produced it. Nothing here has been submitted to '
    'NIC -- the portal is unreachable from this deployment. Keyed on the '
    'voucher HEAD, never written by the voucher path, so an ALTER cannot '
    're-state it; generated_from_version_id is how staleness is detected '
    'instead.';

COMMENT ON COLUMN e_way_bill.local_reference IS
    'A deterministic LOCAL identifier, NOT an EBN. An EBN is a 12-digit number '
    'issued by NIC; this is prefixed LOCAL-EWB- and is deliberately not 12 '
    'digits. Never label it an e-Way Bill Number on any screen or artefact.';

COMMENT ON COLUMN e_way_bill.generated_from_version_id IS
    'The voucher version the payload was built from. A fact, not a key. Differs '
    'from voucher_head.current_version_id exactly when the invoice has been '
    'altered since the bill was raised, i.e. when the stored payload no longer '
    'describes the document it names.';

COMMENT ON COLUMN e_way_bill.over_dimensional_cargo IS
    'Asserted by the operator, never inferred. Nothing in a voucher says '
    'whether a consignment is over-dimensional, and assuming ordinary cargo '
    'grants ten times the validity the rule allows. No DEFAULT, so it cannot be '
    'acquired by omission.';

COMMENT ON COLUMN e_way_bill.disclosures IS
    'The disclosure block as shown when the bill was raised, stored rather than '
    're-derived: the Rule 138 figures are secondary material pending '
    're-verification, so re-deriving would restate today''s rules over '
    'yesterday''s document.';

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE *and* FORCE, USING + WITH CHECK --
-- identical to every tenant table since 0004.
--
-- A raised e-way bill names the consignor, the consignee, the goods, the route
-- and the vehicle. It is a movement schedule, and a leaked one tells a reader
-- what is worth intercepting and exactly where it will be.
-- ---------------------------------------------------------------------------
ALTER TABLE e_way_bill ENABLE ROW LEVEL SECURITY;
ALTER TABLE e_way_bill FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON e_way_bill;
CREATE POLICY tenant_isolation ON e_way_bill
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON e_way_bill TO tally_app;

-- ---------------------------------------------------------------------------
-- ⛔ NO ACL MIGRATION, AND THAT IS A DECISION RATHER THAN AN OMISSION.
--
-- HANDOFF §6.1: a BARE RESOURCE TOKEN needs none. The seeded `Data Entry` level
-- has default_access = 'none' and every seeded rule names a specific token, so a
-- token never mentioned is DENIED. `e-way-bill` is a bare resource token --
-- tds-challan, cost-centre, sales-order, purchase-order and bom all shipped the
-- same way with zero ACL rows.
--
-- The rule that WOULD have forced a migration is the other half of §6.1: a
-- `report:NAME` is GRANTED to Data Entry the moment it exists, because migration
-- 0011 seeds a coarse `allow display report` that internal/authz matches against
-- any report name at specificity 1. T5 registers no report name, so that hazard
-- is not in play -- and the moment somebody adds an "e-Way Bill Register"
-- report, it IS, and that report's ACL migration must ship in the same commit
-- (§6.2) writing BOTH `disallow display` AND `disallow print`.
-- ---------------------------------------------------------------------------
