-- Durable leases and retry dates prevent a failing prefix from occupying every
-- batch. Failed work keeps its business date across midnight. A digest is due
-- only for a real company with a completed night, including earlier dates.
CREATE TABLE IF NOT EXISTS insight_job (
    company_id UUID NOT NULL REFERENCES company(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('nightly', 'digest')),
    work_date DATE NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
    lease_until TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
    claim_token UUID,
    last_error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (company_id, kind, work_date)
);
ALTER TABLE insight_job ENABLE ROW LEVEL SECURITY;
ALTER TABLE insight_job FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON insight_job;
CREATE POLICY tenant_isolation ON insight_job
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
GRANT SELECT, INSERT, UPDATE, DELETE ON insight_job TO tally_app;

-- Like the existing due-list functions, only scheduler identifiers cross
-- tenants. Only this claim function writes globally; tenant-scoped INSERT is
-- also granted for the existing company backup/restore path.
CREATE OR REPLACE FUNCTION insight_claim_jobs(p_kind TEXT, p_date DATE, p_now TIMESTAMPTZ, p_limit INT DEFAULT 100, p_company UUID DEFAULT NULL)
RETURNS TABLE(company_id UUID, work_date DATE, claim_token UUID)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    IF p_kind IS NULL OR p_kind NOT IN ('nightly', 'digest') OR p_date IS NULL OR p_now IS NULL OR p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 1000 THEN
        RAISE EXCEPTION 'invalid insight job claim';
    END IF;
    IF p_kind = 'nightly' THEN
        INSERT INTO public.insight_job(company_id, kind, work_date)
        SELECT c.id, 'nightly', p_date FROM public.company c
        WHERE (p_company IS NULL OR c.id=p_company) AND NOT EXISTS (SELECT 1 FROM public.insight_run r
            WHERE r.company_id=c.id AND r.kind='nightly' AND r.run_date=p_date)
        FOR KEY SHARE OF c SKIP LOCKED
        ON CONFLICT DO NOTHING;
    ELSE
        INSERT INTO public.insight_job(company_id, kind, work_date)
        SELECT r.company_id, 'digest', r.run_date
        FROM public.insight_run r JOIN public.company c ON c.id=r.company_id
        WHERE (p_company IS NULL OR r.company_id=p_company) AND r.kind='nightly' AND r.run_date<=p_date AND NOT EXISTS (
            SELECT 1 FROM public.insight_digest d WHERE d.company_id=r.company_id
                AND d.digest_date=r.run_date AND d.recipient='')
        FOR KEY SHARE OF c SKIP LOCKED
        ON CONFLICT DO NOTHING;
    END IF;
    RETURN QUERY
    WITH due AS (
        SELECT j.company_id, j.kind, j.work_date
        FROM public.insight_job j
        WHERE (p_company IS NULL OR j.company_id=p_company) AND j.kind=p_kind AND j.work_date<=p_date
            AND j.next_attempt_at<=p_now AND j.lease_until<=p_now
        ORDER BY j.work_date, j.next_attempt_at, j.company_id
        LIMIT p_limit FOR UPDATE SKIP LOCKED
    )
    UPDATE public.insight_job j SET attempts=j.attempts+1,
        lease_until=p_now+interval '5 minutes', claim_token=gen_random_uuid()
    FROM due WHERE (j.company_id,j.kind,j.work_date)=(due.company_id,due.kind,due.work_date)
    RETURNING j.company_id, j.work_date, j.claim_token;
END
$$;
REVOKE ALL ON FUNCTION insight_claim_jobs(TEXT, DATE, TIMESTAMPTZ, INT, UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION insight_claim_jobs(TEXT, DATE, TIMESTAMPTZ, INT, UUID) TO tally_app;
