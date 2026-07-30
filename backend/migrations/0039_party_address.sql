-- 0039_party_address.sql
-- THE PARTY'S POSTAL ADDRESS. Without it NO TAX INVOICE IN THIS PRODUCT CAN BE
-- PRINTED AT ALL, and that is not a figure of speech.
--
-- ===========================================================================
-- THE DEFECT THIS CLOSES
-- ===========================================================================
-- Rule 46(d) of the CGST Rules 2017 makes the "name, address and Goods and
-- Services Tax Identification Number or Unique Identity Number, if registered,
-- of the recipient" a MANDATORY PARTICULAR of a tax invoice. Rule 49 carries the
-- same requirement onto a bill of supply. internal/printing/rule46.go therefore
-- refuses to render either document when Recipient.Address1 is blank, with the
-- reason written out: "the recipient's address is a mandatory particular whether
-- or not the recipient is registered".
--
-- That refusal is CORRECT. An invoice that does not state who it is addressed to
-- is not a valid tax invoice, and printing one anyway would put a document in a
-- customer's hands that their assessing officer can reject the input credit on.
--
-- The DEFECT is that the party master had nowhere to put an address. `ledger`
-- carried name, group, opening balance, gstin, pan, state_code,
-- registration_type, itc_eligibility, blocked_clause -- and no postal block at
-- all. So internal/printing/printingdb built its Recipient out of name +
-- state_code + gstin, Address1 was ALWAYS empty, ValidateRule46 ALWAYS refused,
-- and every sales voucher in every company answered 4xx on print. The feature
-- was unreachable for its whole first wave, for one missing column.
--
-- ===========================================================================
-- WHY THESE THREE COLUMNS, WITH THESE NAMES
-- ===========================================================================
-- They are the company block from migration 0015, restated on the other side of
-- the invoice: `address` as a NEWLINE-SEPARATED FREE-TEXT BLOCK, plus `city` and
-- `pincode`. That is deliberate to the point of being the main design decision
-- here.
--
--   * Tally treats an address as multi-line free text, not as parsed fields
--     (door number / street / locality). Any parse this schema imposed would be
--     wrong for somebody's address, and a mandatory statutory particular is the
--     last place to be clever.
--   * printingdb already has splitAddress(), which turns 0015's newline block
--     into the two lines printing.Company carries. Storing the party's address
--     in the SAME shape means ONE helper serves BOTH sides of the document. Had
--     this been modelled as address1/address2 columns instead, the supplier
--     block and the recipient block would be assembled by two different code
--     paths, and the paper would eventually disagree with itself about how a
--     three-line address renders.
--   * The names match `company` exactly, so a reader who has read 0015 needs to
--     learn nothing new, and so a SELECT written against one table transfers to
--     the other.
--
-- ===========================================================================
-- WHY NULLABLE, WHERE company's EQUIVALENTS ARE NOT NULL DEFAULT ''
-- ===========================================================================
-- This is the ONE place this migration departs from 0015, and it departs towards
-- the convention of the table it is altering rather than the table it is copying.
--
-- Every optional party attribute already on `ledger` -- gstin, pan, state_code,
-- credit_days, external_id -- is a NULLABLE column, and masters.CreateLedger
-- writes them as NULLIF($n,''). A NOT NULL DEFAULT '' address here would mean
-- one ledger row spelled "not on file" two different ways depending on which
-- field you looked at, and every reader would have to know which fields are
-- which. Nullable keeps one convention per table.
--
-- AND THE ABSENCE MUST STAY LEGAL *IN THE COLUMN*, which is the substantive half
-- of the point. Read the next two paragraphs together: the first states the rule,
-- the second states why the rule still does not belong in a constraint. An
-- earlier draft of this header ran them together and got the rule wrong, which
-- would have handed a future author a documented licence to weaken a check that
-- is correct.
--
-- WHAT THE RULE ACTUALLY SAYS. Rule 46(d) requires the "name, address and Goods
-- and Services Tax Identification Number or Unique Identity Number, IF
-- REGISTERED, of the recipient": the "if registered" attaches to the GSTIN/UIN
-- ALONE, so the recipient's name and address are particulars of EVERY tax
-- invoice, at every value, registered recipient or not. Rule 46(e) does not cut
-- that down -- it ADDS to it, requiring, where the recipient is UNREGISTERED and
-- the value of the taxable supply is FIFTY THOUSAND RUPEES OR MORE, the address
-- of delivery along with the NAME OF THE STATE AND ITS CODE. There is therefore
-- NO threshold below which a tax invoice may go out with no recipient address,
-- and internal/printing/rule46.go is right to refuse one at every value. NOTHING
-- IN THIS FILE IS A REASON TO RELAX THAT CHECK.
--
-- WHY THE COLUMN IS STILL NULLABLE. Because a `ledger` ROW IS NOT A DOCUMENT. A
-- party master is created for plenty of reasons that never produce a tax invoice
-- at all -- a cash receipt, an opening balance, a purchase ledger, a walk-in
-- recorded so an outstanding can be tracked -- and a NOT NULL constraint would
-- refuse to CREATE THE PARTY rather than refuse to PRINT THE INVOICE, which puts
-- the failure in front of the wrong person at the wrong time. The same is true a
-- second time over for clause (e)'s extra particulars: a constraint cannot see
-- the value of the supply, so it cannot tell the Rs 45,000 counter sale from the
-- Rs 50,000 one. So the schema must be able to say "no address", the API must be
-- able to create a party without one, and only the PRINT path -- which knows
-- which document is being produced and for how much -- gets to refuse.
--
--   Source: CGST Rules 2017, rule 46, clauses (d) and (e), as notified.
--   Effective from: 1 July 2017 (the rules as commenced).
--   NOT VERIFIED AGAINST THE PORTAL: the GSTN portal cannot be reached from
--   this environment. The clause substance above is transcribed from the rule
--   text; no online validation was performed.
--
-- ONE LOOSE SENTENCE SURVIVES BELOW, IN SQL, AND IT IS LEFT ALONE ON PURPOSE.
-- The COMMENT ON COLUMN ledger.address statement at the foot of this file carries
-- the same over-broad wording this header used to ("NULL ... is legal: Rule 46(e)
-- requires the address of an UNREGISTERED recipient only on a supply of fifty
-- thousand rupees or more"), qualified there by its own closing sentence
-- ("MANDATORY on any tax invoice (Rule 46(d)) -- printing refuses without it").
-- It is NOT edited here: 0039 is already applied and stamped in the dev database,
-- and changing an applied statement would leave the file and the live schema
-- disagreeing forever. Restating a column comment is idempotent and harmless, so
-- the correction belongs in a LATER migration that re-issues the COMMENT ON
-- COLUMN with the clause 46(d)/46(e) split above. Read this header, not that
-- string, for the rule.
--
-- ===========================================================================
-- WHAT THIS MIGRATION DELIBERATELY DOES *NOT* DO
-- ===========================================================================
--   * NO NEW TABLE. Three columns are added to `ledger`, which already has
--     FORCED row-level security with the `tenant_isolation` policy on
--     company_id (USING and WITH CHECK both). New columns of an existing table
--     inherit that policy automatically, so:
--       - there is NO new RLS policy to write, and none is missing;
--       - there is NO new GRANT to issue (tally_app's privileges are on the
--         table, not per column);
--       - there is NO new row for backend/internal/platform/testdb's
--         buildFixture, because that guard exists to prove every table in the
--         teardown DELETE PLAN is actually exercised, and this migration adds
--         no table to that plan. `ledger` is already in it.
--     This paragraph exists so a reviewer does not have to wonder whether the
--     three usual companions of a schema change were forgotten.
--   * NO BACKFILL. There is no source to backfill FROM -- nobody has ever typed
--     a party address into this product, so every existing row is legitimately
--     NULL and any value invented here would be fiction on an invoice. The
--     addresses arrive through the API (POST .../ledgers now carries them) and
--     through the demo seed, which is why this migration is one third of the
--     change and not the whole of it. THE COLUMN ALONE PRINTS NOTHING.
--   * NO ship-to / delivery address. Rule 46(o) wants that only where it
--     DIFFERS from the place of supply, and it belongs to the VOUCHER, not to
--     the party master. printingdb leaves ShipTo nil with a written reason.
--
-- ===========================================================================
-- IDEMPOTENCE
-- ===========================================================================
-- ADD COLUMN IF NOT EXISTS on all three, so a re-run inserts nothing and errors
-- on nothing. This matters more than usual here: migrations are go:embed'd and
-- every server test's newServer() calls migrations.Apply, so a concurrent test
-- run in another package can apply this file the moment it lands on disk. A
-- second application must be a no-op, and it is. Verified by applying the file
-- twice (the second run reported no change) before it was committed.

ALTER TABLE ledger
    ADD COLUMN IF NOT EXISTS address TEXT NULL,
    ADD COLUMN IF NOT EXISTS city    TEXT NULL,
    ADD COLUMN IF NOT EXISTS pincode TEXT NULL;

COMMENT ON COLUMN ledger.address IS
    'The party''s address, as a NEWLINE-SEPARATED BLOCK -- the same shape as '
    'company.address (migration 0015), so printingdb''s splitAddress helper '
    'serves the recipient block and the supplier block alike. NULL means no '
    'address is on file, which is legal: Rule 46(e) requires the address of an '
    'UNREGISTERED recipient only on a supply of fifty thousand rupees or more, '
    'so the threshold is enforced by the print path and not by this column. '
    'MANDATORY on any tax invoice (Rule 46(d)) -- printing refuses without it.';
COMMENT ON COLUMN ledger.city IS
    'City / town / village of the party, mirroring company.city. NULL = not on file.';
COMMENT ON COLUMN ledger.pincode IS
    'Postal index number of the party, mirroring company.pincode. NULL = not on file.';
