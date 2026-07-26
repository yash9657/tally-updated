-- 0016_voucher_gst_line.sql
-- PER-LINE GST detail, so the GSTR-1 table-12 HSN summary can report a real
-- HSN/SAC code instead of the empty string.
--
-- THE GAP THIS CLOSES
-- -------------------
-- GST was derived per line (compliance.TaxDerive rounds per line) but persisted
-- only as a VOUCHER-LEVEL aggregate on voucher_version (taxable_value, cgst,
-- sgst, igst). The HSN a caller supplied was used for nothing and then dropped
-- on the floor: it is not in the canonical hashed payload and it had no column.
-- gstr1.go consequently hardcoded `hsnKey{hsn: ""}` and every generated return
-- carried "hsn_sc": "". HSN is mandatory in a real GSTR-1 table 12, so that
-- could never have been filed.
--
-- WHY A TABLE AND NOT A COLUMN
-- ----------------------------
-- The HSN summary rolls up by (hsn, rate). Both are LINE-grain: one invoice may
-- carry 18% steel articles and 12% kitchenware under different HSN codes. A
-- single voucher_version.hsn column would be correct only for single-line
-- invoices and would silently mis-attribute every mixed-rate one -- the same
-- class of quiet wrongness this whole pass is removing.
--
-- RELATIONSHIP TO THE AGGREGATE
-- -----------------------------
-- These rows are DERIVED, never independent: TaxResult.Lines sums exactly to
-- TaxResult's aggregate (pinned by TestTaxLinesSumToAggregate), and the ledger
-- writes both from the same derivation inside one transaction. The aggregate
-- columns stay the source for b2b/b2cl/b2cs; these are the source for table 12.
--
-- Versioned with the voucher, exactly like bill_allocation: an ALTER appends a
-- new voucher_version and re-states its lines under it, so reports must join
-- voucher_head.current_version_id (gstr1.go does).

CREATE TABLE voucher_gst_line (
    id                 UUID PRIMARY KEY,
    company_id         UUID NOT NULL,
    voucher_version_id UUID NOT NULL REFERENCES voucher_version (id),
    -- 1-based position in the invoice, preserving input order.
    line_no            INTEGER NOT NULL CHECK (line_no >= 1),
    -- HSN (goods) / SAC (services). '' when the caller supplied none, which is
    -- still recorded so the line is present in the rollup.
    hsn                TEXT NOT NULL DEFAULT '',
    taxable_value      NUMERIC(19,4) NOT NULL,
    gst_rate_pct       NUMERIC(9,4)  NOT NULL,
    cgst               NUMERIC(19,4) NOT NULL DEFAULT 0,
    sgst               NUMERIC(19,4) NOT NULL DEFAULT 0,
    igst               NUMERIC(19,4) NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (voucher_version_id, line_no)
);

CREATE INDEX voucher_gst_line_company ON voucher_gst_line (company_id);
CREATE INDEX voucher_gst_line_version ON voucher_gst_line (voucher_version_id);
CREATE INDEX voucher_gst_line_hsn     ON voucher_gst_line (company_id, hsn, gst_rate_pct);

COMMENT ON TABLE voucher_gst_line IS
    'Per-line GST detail (hsn, taxable value, rate, derived tax) for a voucher '
    'version. Sums exactly to the aggregate columns on voucher_version. Source '
    'of the GSTR-1 table-12 HSN summary.';

-- Immutable, like posting and bill_allocation: a correction appends a new
-- version and re-states its lines, never edits the old ones.
CREATE OR REPLACE FUNCTION voucher_gst_line_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'voucher_gst_line is immutable; % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER voucher_gst_line_no_mutate
    BEFORE UPDATE OR DELETE ON voucher_gst_line
    FOR EACH ROW EXECUTE FUNCTION voucher_gst_line_immutable();

-- Default-deny, ENABLE + FORCE RLS with USING + WITH CHECK pinned to the total
-- app_current_company_id() resolver -- identical to every other tenant table.
ALTER TABLE voucher_gst_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE voucher_gst_line FORCE  ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON voucher_gst_line
    USING      (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON voucher_gst_line TO tally_app;
