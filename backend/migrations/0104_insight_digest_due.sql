-- 0104 — the morning digest's own due list (W4, D125 §5.3 as amended by the
-- wave-2 review).
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
