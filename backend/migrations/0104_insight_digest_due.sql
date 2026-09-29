-- 0104 — the morning digest's own due list, and a wider voucher_ids cap (W4,
-- D125 as amended by the wave-2 review).
--
-- 0102's poller composed a company's digest only in the same pass as its
-- nightly run: insight_nightly_due_companies returns a company whose night has
-- NOT run, and ComposeDigest was reached only for those. So a night that ran
-- by another road — POST /insights/run ("R, run now") — dropped the company
-- from the list and that day's digest was never composed; and a ComposeDigest
-- that failed after a successful night was never retried. The digest is now
-- due on its own condition: the night for the date has run, and the company
-- digest (recipient '') for the date does not exist.
--
-- The poller's cross-tenant read, as 0102's two: SECURITY DEFINER, ids only.
-- Runs as ADMIN.

CREATE OR REPLACE FUNCTION insight_digest_due_companies(p_date DATE, p_limit INT DEFAULT 100)
RETURNS TABLE (company_id UUID)
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
    SELECT r.company_id
      FROM insight_run r
     WHERE r.run_date = p_date AND r.kind = 'nightly'
       AND NOT EXISTS (SELECT 1 FROM insight_digest d
                        WHERE d.company_id = r.company_id AND d.digest_date = p_date AND d.recipient = '')
     ORDER BY r.company_id
     LIMIT p_limit;
$$;
REVOKE ALL ON FUNCTION insight_digest_due_companies(DATE, INT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION insight_digest_due_companies(DATE, INT) TO tally_app;

-- ---------------------------------------------------------------------------
-- voucher_ids: 200 -> 5000.
--
-- The W4 contract is that an insight's voucher_ids reproduce its numbers. A
-- credit-limit breach's balance is the opening balance plus the party's legs
-- on EVERY voucher that ever touched it, and the list was capped at 200 on
-- both the detector's side and the evidence SQL's, so the gate stayed green
-- while an auditor summing the listed vouchers could not reach the figure
-- (the wave-2 review). The list is now complete up to 5000 (80 kB of UUIDs,
-- TOASTed); a party beyond that is the residual d125 states.
-- ---------------------------------------------------------------------------
ALTER TABLE insight DROP CONSTRAINT IF EXISTS insight_voucher_ids_capped;
ALTER TABLE insight ADD CONSTRAINT insight_voucher_ids_capped CHECK (cardinality(voucher_ids) <= 5000);
