-- 0098_embeddings_and_golden_queries.sql
-- W3 wave 2 (PLAN-AGENTIC §3 W3; d122 "Wave 2"): the retrieval store for
-- narrations, bank lines, party aliases, items, document chunks, the
-- catalogue and golden queries — LEXICAL (pg_trgm) everywhere, VECTOR
-- (pgvector halfvec(1024), HNSW) where the server has pgvector — and the
-- golden-query few-shot store emb_golden_query(tenant, question, dsl,
-- accepted).
--
-- ===========================================================================
-- ⛔ IT DEGRADES LOUDLY, NOT SILENTLY, WHEN pgvector IS ABSENT
-- ===========================================================================
-- The shared dev container (tally_pg) and CI ran postgres:16, which has no
-- pgvector; docker-compose.yml and ci.yml now name pgvector/pgvector:pg16,
-- but a database created before the switch — or a managed Postgres without
-- the extension — must still migrate, because every other lane's tests run
-- on it. So this file splits the store in two:
--
--   ALWAYS      emb_document (text + pg_trgm GIN), emb_golden_query, the
--               lexical half of every search, and emb_vector_status().
--   pgvector    emb_vector (halfvec(1024) + HNSW), created by
--               emb_enable_vectors() — which this file calls, and which an
--               operator calls AGAIN after switching the image (it is
--               idempotent; d122 "Wave 2" has the exact steps).
--
-- "Loudly" is three separate things, because any one alone is easy to miss:
--   1. emb_enable_vectors() RAISEs a WARNING naming the fix;
--   2. `cored migrate` reads emb_vector_status() after applying and logs
--      "embeddings: DISABLED — …" with the same fix (cmd/cored);
--   3. internal/embed refuses a vector search with ErrVectorUnavailable and
--      marks a hybrid search Degraded — it never returns an empty or
--      lexical-only result dressed as a semantic one.
--
-- ===========================================================================
-- TENANCY
-- ===========================================================================
-- Every table: company_id, ENABLE and FORCE RLS, USING and WITH CHECK on
-- app_current_company_id(), a (company_id, id) company-scope UNIQUE, and
-- composite foreign keys that carry company_id, so a vector can never hang
-- off another tenant's document. The HNSW index is ONE index over all
-- tenants with RLS filtering after the scan (pgvector 0.8's iterative scan
-- keeps recall; tenant-hash partitioning is OWED, d122 "Wave 2").
--
-- NO ACL RULE, NO SEEDER RESTATEMENT (census stays 17 PERFORMs): no route
-- reaches the store in this lane. When one does, it is a bare token.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ---------------------------------------------------------------------------
-- emb_document: one text the agent may retrieve, per (kind, source).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS emb_document (
    id           UUID PRIMARY KEY,
    company_id   UUID NOT NULL,
    -- What the text is. Closed: a new kind is a migration, not a string.
    kind         TEXT NOT NULL,
    -- The id (as text) of what the text came from: a bank line, a ledger, an
    -- item, a document chunk, a catalogue report name, a golden query.
    source_ref   TEXT NOT NULL,
    content      TEXT NOT NULL,
    -- sha256 hex of content: the idempotency half of the natural key, so
    -- re-indexing an unchanged narration is a no-op, and a changed one is a
    -- NEW row (the old vector described the old text).
    content_hash TEXT NOT NULL,
    -- ⚠️ UNTRUSTED marks third-party text (a bank narration, a supplier's
    -- document). A retrieval hit on it is PLAN-AGENTIC §2.4's injection
    -- surface: the caller must taint the context, exactly as W2 does for a
    -- read of stored text. Derived from kind below, stored so a query can
    -- filter on it without re-deriving the rule.
    untrusted    BOOLEAN NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT emb_document_company_scope UNIQUE (company_id, id),
    CONSTRAINT emb_document_one_per_text UNIQUE (company_id, kind, source_ref, content_hash),
    CONSTRAINT emb_document_kind_known
        CHECK (kind IN ('narration', 'bank_line', 'party_alias', 'item', 'doc_chunk', 'catalogue', 'golden_query')),
    CONSTRAINT emb_document_untrusted_by_kind
        CHECK (untrusted = (kind IN ('narration', 'bank_line', 'doc_chunk'))),
    CONSTRAINT emb_document_content_bounded
        CHECK (length(content) BETWEEN 1 AND 4000),
    CONSTRAINT emb_document_hash_shape
        CHECK (content_hash ~ '^[0-9a-f]{64}$')
);
CREATE INDEX IF NOT EXISTS emb_document_trgm ON emb_document USING gin (content gin_trgm_ops);
CREATE INDEX IF NOT EXISTS emb_document_by_kind ON emb_document (company_id, kind);

-- ---------------------------------------------------------------------------
-- emb_golden_query: a question and the DSL that answered it, PROPOSED by a
-- run and ACCEPTED (or rejected) by a human. Only accepted rows are ever
-- served as few-shot examples. The question/dsl are immutable once written
-- (column grants below); only the decision columns move, once.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS emb_golden_query (
    id             UUID PRIMARY KEY,
    company_id     UUID NOT NULL,
    question       TEXT NOT NULL,
    dsl            JSONB NOT NULL,
    -- The translator prompt version the DSL was produced under (nlq-dsl-vN):
    -- a few-shot example from another prompt's grammar is not guaranteed to
    -- compile under this one, so a caller re-compiles before serving it.
    prompt_version TEXT NOT NULL,
    proposed_by    TEXT NOT NULL,
    accepted       BOOLEAN NULL,          -- NULL = undecided
    decided_by     TEXT NULL,
    decided_at     TIMESTAMPTZ NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT emb_golden_query_company_scope UNIQUE (company_id, id),
    CONSTRAINT emb_golden_query_question_bounded CHECK (length(question) BETWEEN 1 AND 500),
    CONSTRAINT emb_golden_query_dsl_is_object CHECK (jsonb_typeof(dsl) = 'object'),
    CONSTRAINT emb_golden_query_decision_complete
        CHECK ((accepted IS NULL) = (decided_by IS NULL) AND (accepted IS NULL) = (decided_at IS NULL))
);
CREATE INDEX IF NOT EXISTS emb_golden_query_trgm ON emb_golden_query USING gin (question gin_trgm_ops);

-- A decision is made once: undecided -> accepted/rejected, never back, never
-- re-decided, and the question/dsl never change (the grant is also column-
-- scoped; this is the second wall).
CREATE OR REPLACE FUNCTION emb_golden_decide_once() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.accepted IS NOT NULL THEN
        RAISE EXCEPTION 'emb_golden_query %: already decided', OLD.id USING ERRCODE = '23514';
    END IF;
    IF NEW.question IS DISTINCT FROM OLD.question OR NEW.dsl IS DISTINCT FROM OLD.dsl
       OR NEW.prompt_version IS DISTINCT FROM OLD.prompt_version OR NEW.company_id IS DISTINCT FROM OLD.company_id THEN
        RAISE EXCEPTION 'emb_golden_query %: only the decision may change', OLD.id USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;
DROP TRIGGER IF EXISTS emb_golden_query_decide_once ON emb_golden_query;
CREATE TRIGGER emb_golden_query_decide_once
    BEFORE UPDATE ON emb_golden_query
    FOR EACH ROW EXECUTE FUNCTION emb_golden_decide_once();

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['emb_document', 'emb_golden_query'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I '
                       'USING (company_id = app_current_company_id()) '
                       'WITH CHECK (company_id = app_current_company_id())', t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT ON emb_document TO tally_app;
GRANT SELECT, INSERT ON emb_golden_query TO tally_app;
GRANT UPDATE (accepted, decided_by, decided_at) ON emb_golden_query TO tally_app;

-- ---------------------------------------------------------------------------
-- The vector half. EVERY statement that names a pgvector type is inside
-- EXECUTE, so this file PARSES on a server without the extension.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION emb_enable_vectors() RETURNS TEXT
    LANGUAGE plpgsql AS $$
DECLARE
    v TEXT;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector') THEN
        RAISE WARNING 'embeddings DISABLED: pgvector is not installed on this server, so emb_vector was not created and only lexical (pg_trgm) search works. Fix: run Postgres from pgvector/pgvector:pg16, then SELECT emb_enable_vectors(); (docs/design/d122-w3-data-understanding.md, Wave 2).';
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
               ELSE 'disabled: pgvector is not installed on this server; run Postgres from pgvector/pgvector:pg16, then SELECT emb_enable_vectors();'
           END
$$;
GRANT EXECUTE ON FUNCTION emb_vector_status() TO tally_app;

SELECT emb_enable_vectors();
