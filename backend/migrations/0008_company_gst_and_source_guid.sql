-- 0008_company_gst_and_source_guid.sql
-- Two folded concerns ship together (latest before this was 0007):
--
--   WS3 — company GST identity: state_code + gstin on the company row, so the
--   posting path can default a Sales/Purchase voucher's SupplierStateCode from
--   the company's home state (server-side, atomic, inside ledger.post). Both are
--   NOT NULL DEFAULT '' so existing rows and tests that omit them are unaffected.
--   RLS already covers the company row (company_self_scope / the company table's
--   tenant policy), so no policy change is required.
--
--   WS2 — import idempotency on the stable Tally GUID: voucher_head gains a
--   nullable source_guid plus a PARTIAL unique index over (company_id,
--   source_guid). Re-importing the same export is then a no-op at the
--   (company, guid) grain — matching Tally's own same-GUID semantics. A voucher
--   with NO guid (source_guid IS NULL) is excluded from the index and always
--   posts, preserving the pre-existing behaviour for guid-less exports.
--   voucher_head is already RLS-protected (0006), so the new column inherits it.

-- ---------------------------------------------------------------------------
-- WS3: company home-state + GSTIN.
-- ---------------------------------------------------------------------------
ALTER TABLE company
    ADD COLUMN state_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN gstin      TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN company.state_code IS
    'Two-digit GST state code of the company''s home/registered place of business. '
    'Used as the default SupplierStateCode when posting Sales/Purchase vouchers '
    'whose GST params leave it empty (e.g. imported vouchers).';
COMMENT ON COLUMN company.gstin IS
    'The company''s own GSTIN (filer), surfaced as the GSTR-1 top-level gstin.';

-- ---------------------------------------------------------------------------
-- WS2: idempotent import keyed on the stable Tally GUID.
-- ---------------------------------------------------------------------------
ALTER TABLE voucher_head
    ADD COLUMN source_guid TEXT NULL;

COMMENT ON COLUMN voucher_head.source_guid IS
    'The stable Tally GUID (<GUID>, falling back to <REMOTEID>) of the source '
    'voucher this head was imported from, or NULL for natively-created vouchers. '
    'Idempotency on re-import is keyed on (company_id, source_guid).';

-- Partial unique index: enforce one head per (company, guid) only when a guid is
-- present. NULL guids (hand-keyed or guid-less imports) are excluded, so they are
-- never blocked and always post.
CREATE UNIQUE INDEX ux_voucher_head_source_guid
    ON voucher_head (company_id, source_guid)
    WHERE source_guid IS NOT NULL;
