-- Deliver the corrected function bodies to databases that already recorded
-- 0098/0099 before their in-place review edits. Do not rewrite old versions.
-- 0107-0109 remain reserved for W7 learning.
GRANT CREATE ON SCHEMA agent TO agent_ro;
CREATE OR REPLACE FUNCTION agent.tier_b_run(p_sql TEXT, p_max_rows INT) RETURNS SETOF JSON
    LANGUAGE plpgsql VOLATILE SECURITY DEFINER
    SET search_path = pg_catalog
AS $$
DECLARE
    r RECORD;
    n INT := 0;
BEGIN
    -- Loud, not empty: an unbound or writable transaction is a wiring bug in
    -- the caller, and "no rows" would read as "no data" to the model.
    IF agent.tier_b_company() IS NULL THEN
        RAISE EXCEPTION 'tier_b_run: no tenant binding in this transaction (call agent.tier_b_bind() first)'
            USING ERRCODE = '42501';
    END IF;
    IF current_setting('transaction_read_only') <> 'on' THEN
        RAISE EXCEPTION 'tier_b_run: the transaction must be READ ONLY'
            USING ERRCODE = '25006';
    END IF;
    FOR r IN EXECUTE p_sql LOOP
        n := n + 1;
        EXIT WHEN n > p_max_rows;
        RETURN NEXT row_to_json(r);
    END LOOP;
    RETURN;
END
$$;

CREATE OR REPLACE FUNCTION agent.tier_b_plan(p_sql TEXT) RETURNS JSON
    LANGUAGE plpgsql VOLATILE SECURITY DEFINER
    SET search_path = pg_catalog
AS $$
DECLARE
    r RECORD;
BEGIN
    IF agent.tier_b_company() IS NULL THEN
        RAISE EXCEPTION 'tier_b_plan: no tenant binding in this transaction'
            USING ERRCODE = '42501';
    END IF;
    IF current_setting('transaction_read_only') <> 'on' THEN
        RAISE EXCEPTION 'tier_b_plan: the transaction must be READ ONLY'
            USING ERRCODE = '25006';
    END IF;
    -- ⛔ A CURSOR, LIKE tier_b_run — NOT `EXECUTE … INTO` (the wave-2 review).
    -- EXECUTE … INTO runs EVERY statement of a multi-statement string: measured,
    -- 'SELECT 1; PREPARE planted AS SELECT 42; DO $d$…$d$; SET …; SELECT …'
    -- ran the DO as agent_ro, re-pointed the GUC, and left 'planted' prepared
    -- on the pooled connection after the Tier-B rollback. FOR … IN EXECUTE
    -- opens a cursor, which refuses a multi-query string before any of it runs.
    FOR r IN EXECUTE 'EXPLAIN (FORMAT JSON) ' || p_sql LOOP
        RETURN r."QUERY PLAN";
    END LOOP;
    RETURN NULL;
END
$$;

ALTER FUNCTION agent.tier_b_run(TEXT, INT) OWNER TO agent_ro;
ALTER FUNCTION agent.tier_b_plan(TEXT) OWNER TO agent_ro;
REVOKE CREATE ON SCHEMA agent FROM agent_binder, agent_ro;
REVOKE ALL ON FUNCTION agent.tier_b_run(TEXT, INT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent.tier_b_plan(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent.tier_b_run(TEXT, INT) TO agent_ro;
GRANT EXECUTE ON FUNCTION agent.tier_b_plan(TEXT) TO agent_ro;


CREATE OR REPLACE FUNCTION emb_enable_vectors() RETURNS TEXT
    LANGUAGE plpgsql AS $$
DECLARE
    v TEXT;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector') THEN
        RAISE WARNING 'embeddings DISABLED: pgvector is not installed on this server, so emb_vector was not created and only lexical (pg_trgm) search works. Fix: run Postgres from pgvector/pgvector:pg16-trixie (NOT the bare pg16 tag: a collation mismatch on an existing volume), then SELECT emb_enable_vectors(); (docs/design/d122-w3-data-understanding.md, Wave 2).';
        RETURN 'disabled: pgvector is not installed on this server';
    END IF;
    CREATE EXTENSION IF NOT EXISTS vector;
    SELECT extversion INTO v FROM pg_extension WHERE extname = 'vector';
    -- halfvec (and its HNSW opclass) arrived in pgvector 0.7.0.
    IF string_to_array(split_part(v, '-', 1), '.')::int[] < ARRAY[0, 7, 0] THEN
        RAISE EXCEPTION 'embeddings: pgvector % is installed; halfvec needs >= 0.7.0', v;
    END IF;

    EXECUTE $ddl$
        CREATE TABLE IF NOT EXISTS emb_vector (
            id          UUID PRIMARY KEY,
            company_id  UUID NOT NULL,
            document_id UUID NOT NULL,
            -- The embedding scheme (model + dimensions + preprocessing),
            -- llm.EmbedRequest.Version: vectors under two schemes are never
            -- compared, so every search names one.
            version     TEXT NOT NULL,
            model_id    TEXT NOT NULL,
            embedding   halfvec(1024) NOT NULL,
            created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
            CONSTRAINT emb_vector_company_scope UNIQUE (company_id, id),
            CONSTRAINT emb_vector_one_per_version UNIQUE (company_id, document_id, version),
            CONSTRAINT emb_vector_document_fk FOREIGN KEY (company_id, document_id)
                REFERENCES emb_document (company_id, id)
        )
    $ddl$;
    EXECUTE 'CREATE INDEX IF NOT EXISTS emb_vector_hnsw ON emb_vector USING hnsw (embedding halfvec_cosine_ops)';
    EXECUTE 'ALTER TABLE emb_vector ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE emb_vector FORCE  ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON emb_vector';
    EXECUTE 'CREATE POLICY tenant_isolation ON emb_vector '
            'USING (company_id = app_current_company_id()) '
            'WITH CHECK (company_id = app_current_company_id())';
    EXECUTE 'GRANT SELECT, INSERT ON emb_vector TO tally_app';
    RETURN 'enabled: pgvector ' || v;
END
$$;
REVOKE ALL ON FUNCTION emb_enable_vectors() FROM PUBLIC;

-- emb_vector_status(): what the store can do on THIS database, derived from
-- the catalogs (no status table to drift from the truth).
CREATE OR REPLACE FUNCTION emb_vector_status(OUT enabled BOOLEAN, OUT detail TEXT)
    LANGUAGE sql STABLE AS $$
    SELECT to_regclass('public.emb_vector') IS NOT NULL
               AND EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector'),
           CASE
               WHEN to_regclass('public.emb_vector') IS NOT NULL
                    AND EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')
                   THEN 'enabled: pgvector ' || (SELECT extversion FROM pg_extension WHERE extname = 'vector')
               WHEN EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector')
                   THEN 'disabled: pgvector is available on this server but not enabled; run SELECT emb_enable_vectors(); as the migration role'
               ELSE 'disabled: pgvector is not installed on this server; run Postgres from pgvector/pgvector:pg16-trixie (NOT the bare pg16 tag: a collation mismatch on an existing volume), then SELECT emb_enable_vectors();'
           END
$$;
GRANT EXECUTE ON FUNCTION emb_vector_status() TO tally_app;
