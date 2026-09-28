-- 0105 — the MCP server's standing GRANTS and its SESSIONS (W6, D126).
--
-- ===========================================================================
-- A GRANT IS A DELEGATION WITH A LIFETIME, NOT AN IDENTITY
-- ===========================================================================
-- An MCP client (Claude Desktop, ChatGPT, an IDE) authenticates with the SAME
-- bearer token a person's browser does — the IdP's JWT, verified by
-- internal/authn — so this file mints no credential and stores no secret.
-- What it stores is what the human DELEGATED to that client: the ACL keys
-- (no wildcards, as policy.Delegation requires), an optional paise ceiling,
-- an expiry, and whether the human had stepped up when granting it. The
-- transport URL names the grant, and a request is served only when the
-- token's user IS the grant's user, in the grant's company (RLS below), and
-- the grant is unrevoked and unexpired. A leaked grant id is therefore
-- worthless without that user's token; a leaked token without a grant is
-- exactly as powerful as it always was, and no more.
--
-- ===========================================================================
-- ONE SESSION = ONE AGENT RUN = ONE PINNED TOOL MANIFEST
-- ===========================================================================
-- initialize creates the session row and an agent_run (0094) for it
-- (agent_id 'mcp:<grant id>', channel 'mcp'). The SHA-256 of the tool
-- manifest the client was shown is pinned here; every later tools/list and
-- tools/call recomputes it and a mismatch is refused, so a registry or grant
-- change mid-session cannot hand a running client a tool it never listed.
--
-- MUTABLE, UNLIKE 0094's FACT TABLES: a grant is revoked (once), a session is
-- touched and closed. The record of what either DID is the run's intent log
-- (0092), which stays append-only. The trigger below narrows each UPDATE to
-- exactly those columns, so a grant's keys, ceiling or expiry can never be
-- widened after the human read and granted them.
--
-- This file restates no seeder and writes no ACL rule: the routes use the
-- BARE token agent-mcp, refused for every seeded non-Owner level by Data
-- Entry's default_access = 'none' (the 0091 row of PLAN-TALLY-PARITY §2.5).
-- Runs as ADMIN.

CREATE TABLE IF NOT EXISTS agent_mcp_grant (
    id               UUID PRIMARY KEY,
    company_id       UUID NOT NULL,
    -- The human whose rights the client borrows; only this user's token may
    -- use the grant.
    user_id          UUID NOT NULL,
    -- What the human called the client ("Claude Desktop on the office
    -- laptop"). Shown to the approver; never trusted for anything.
    client_name      TEXT NOT NULL,
    acl_keys         TEXT[] NOT NULL,
    max_amount_paise BIGINT NULL,
    step_up          BOOLEAN NOT NULL DEFAULT false,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    revoked_at       TIMESTAMPTZ NULL,
    revoked_by       UUID NULL,
    CONSTRAINT agent_mcp_grant_company_scope UNIQUE (company_id, id),
    CONSTRAINT agent_mcp_grant_client_name CHECK (length(btrim(client_name)) BETWEEN 1 AND 100),
    CONSTRAINT agent_mcp_grant_expiry_after_creation CHECK (expires_at > created_at),
    CONSTRAINT agent_mcp_grant_ceiling_positive CHECK (max_amount_paise IS NULL OR max_amount_paise > 0),
    CONSTRAINT agent_mcp_grant_revocation_attributed CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)),
    CONSTRAINT agent_mcp_grant_keys_bounded CHECK (cardinality(acl_keys) <= 64)
);
CREATE INDEX IF NOT EXISTS agent_mcp_grant_company_created ON agent_mcp_grant (company_id, created_at DESC);

CREATE TABLE IF NOT EXISTS agent_mcp_session (
    -- The Mcp-Session-Id header value: a random v4 UUID (122 bits), which the
    -- Streamable HTTP transport requires to be globally unique and
    -- unguessable. Guessing one still gets nothing: the bearer's user must
    -- own the session's grant.
    id               UUID PRIMARY KEY,
    company_id       UUID NOT NULL,
    grant_id         UUID NOT NULL,
    run_id           UUID NOT NULL,
    protocol_version TEXT NOT NULL,
    -- SHA-256 hex of the canonical tool manifest shown at initialize.
    manifest_hash    TEXT NOT NULL,
    client_name      TEXT NOT NULL DEFAULT '',
    client_version   TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL,
    initialized_at   TIMESTAMPTZ NULL,
    last_seen_at     TIMESTAMPTZ NOT NULL,
    closed_at        TIMESTAMPTZ NULL,
    CONSTRAINT agent_mcp_session_company_scope UNIQUE (company_id, id),
    CONSTRAINT agent_mcp_session_one_per_run UNIQUE (company_id, run_id),
    CONSTRAINT agent_mcp_session_grant_fk FOREIGN KEY (company_id, grant_id)
        REFERENCES agent_mcp_grant (company_id, id),
    CONSTRAINT agent_mcp_session_run_fk FOREIGN KEY (company_id, run_id)
        REFERENCES agent_run (company_id, id),
    CONSTRAINT agent_mcp_session_manifest_hash CHECK (manifest_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT agent_mcp_session_protocol_version CHECK (protocol_version ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),
    CONSTRAINT agent_mcp_session_client_bounded CHECK (length(client_name) <= 200 AND length(client_version) <= 100)
);
CREATE INDEX IF NOT EXISTS agent_mcp_session_grant ON agent_mcp_session (company_id, grant_id);

-- ⛔ WHAT MAY CHANGE, COLUMN BY COLUMN. A grant: revoked_at/revoked_by, once.
-- A session: initialized_at (once), last_seen_at, closed_at (once). Anything
-- else — a grant's keys, ceiling, expiry, user; a session's pinned manifest,
-- grant or run — is refused, so the thing the human read is the thing in force.
CREATE OR REPLACE FUNCTION agent_mcp_narrow_update()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION '% rows are not deleted; revoke or close them', TG_TABLE_NAME;
    END IF;
    IF TG_TABLE_NAME = 'agent_mcp_grant' THEN
        IF (NEW.id, NEW.company_id, NEW.user_id, NEW.client_name, NEW.acl_keys, NEW.max_amount_paise,
            NEW.step_up, NEW.expires_at, NEW.created_at)
           IS DISTINCT FROM
           (OLD.id, OLD.company_id, OLD.user_id, OLD.client_name, OLD.acl_keys, OLD.max_amount_paise,
            OLD.step_up, OLD.expires_at, OLD.created_at) THEN
            RAISE EXCEPTION 'agent_mcp_grant: only revocation may change a grant';
        END IF;
        IF OLD.revoked_at IS NOT NULL THEN
            RAISE EXCEPTION 'agent_mcp_grant: a revoked grant stays revoked';
        END IF;
    ELSE
        IF (NEW.id, NEW.company_id, NEW.grant_id, NEW.run_id, NEW.protocol_version, NEW.manifest_hash,
            NEW.client_name, NEW.client_version, NEW.created_at)
           IS DISTINCT FROM
           (OLD.id, OLD.company_id, OLD.grant_id, OLD.run_id, OLD.protocol_version, OLD.manifest_hash,
            OLD.client_name, OLD.client_version, OLD.created_at) THEN
            RAISE EXCEPTION 'agent_mcp_session: the pinned manifest, grant and run of a session never change';
        END IF;
        IF OLD.closed_at IS NOT NULL THEN
            RAISE EXCEPTION 'agent_mcp_session: a closed session stays closed';
        END IF;
        IF OLD.initialized_at IS NOT NULL AND NEW.initialized_at IS DISTINCT FROM OLD.initialized_at THEN
            RAISE EXCEPTION 'agent_mcp_session: initialized_at is written once';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['agent_mcp_grant', 'agent_mcp_session'] LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', t || '_narrow_update', t);
        EXECUTE format('CREATE TRIGGER %I BEFORE UPDATE OR DELETE ON %I '
                       'FOR EACH ROW EXECUTE FUNCTION agent_mcp_narrow_update()', t || '_narrow_update', t);
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I '
                       'USING (company_id = app_current_company_id()) '
                       'WITH CHECK (company_id = app_current_company_id())', t);
        EXECUTE format('REVOKE ALL ON %I FROM PUBLIC', t);
    END LOOP;
END
$$;

REVOKE DELETE, TRUNCATE ON agent_mcp_grant, agent_mcp_session FROM tally_app;
GRANT SELECT, INSERT, UPDATE ON agent_mcp_grant, agent_mcp_session TO tally_app;

COMMENT ON TABLE agent_mcp_grant IS
    'W6/D126. A standing delegation a human grants one MCP client: ACL keys, '
    'ceiling, expiry. Used only with that human''s own bearer token. Only '
    'revocation may change a row.';
COMMENT ON TABLE agent_mcp_session IS
    'W6/D126. One MCP Streamable-HTTP session: its agent_run, its negotiated '
    'protocol version and the SHA-256 of the tool manifest pinned at initialize.';
