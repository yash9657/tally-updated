-- 0106 — the MCP transport's OWN credential (W6, D126 §4 as amended by the
-- wave-2 review).
--
-- ===========================================================================
-- WHY 0105's "THE HUMAN'S OWN BEARER TOKEN" WAS NOT A BOUNDARY
-- ===========================================================================
-- 0105 authenticated the MCP transport with the person's IdP JWT. That token
-- is the person: the same bearer that reaches POST /agent/approvals/{id}/
-- decision and POST /vouchers/payment. An MCP client holds its bearer in a
-- config file it can read, so a prompt-injected IDE agent could propose a
-- payment through commit_preview and then APPROVE ITS OWN PROPOSAL with the
-- token it was configured with, and "every money commit waits for a human"
-- held against the model but not against the client. And the token a user
-- naturally pastes after making a grant is the stepped-up one, so it met an
-- Owner level's requires_webauthn too.
--
-- ===========================================================================
-- WHAT THIS FILE STORES
-- ===========================================================================
-- A grant now carries a TRANSPORT TOKEN: 32 random bytes, returned once in
-- the POST /agent/mcp/grants response, stored here only as its SHA-256. The
-- router (auth_middleware.go) accepts it on exactly one route — this grant's
-- transport, POST/DELETE /v1/companies/{c}/agent/mcp/{grant} — and refuses a
-- person's JWT there. Everywhere else it is not a credential at all. So the
-- client is configured with something that cannot decide an approval, post
-- a voucher or make a grant, and the approval is again a human's.
--
-- user_subject is the grant-maker's JWT subject, captured at creation: the
-- transport token authenticates AS that subject through the rest of
-- companyAuthMiddleware (membership, level, PDP, session revocation), with
-- the grant's created_at as its issued-at, so locking the account after the
-- grant was made kills the transport exactly as it kills a JWT.
--
-- Both columns are NULL on a grant made before this file; such a grant has no
-- transport token and cannot be used (the human makes a new one). Both are
-- immutable (the 0105 trigger is replaced below with them in the frozen set).
--
-- Runs as ADMIN.

ALTER TABLE agent_mcp_grant ADD COLUMN IF NOT EXISTS transport_token_sha256 BYTEA NULL;
ALTER TABLE agent_mcp_grant ADD COLUMN IF NOT EXISTS user_subject TEXT NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_mcp_grant_transport_token_len') THEN
        ALTER TABLE agent_mcp_grant ADD CONSTRAINT agent_mcp_grant_transport_token_len
            CHECK (transport_token_sha256 IS NULL OR octet_length(transport_token_sha256) = 32);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_mcp_grant_token_has_subject') THEN
        ALTER TABLE agent_mcp_grant ADD CONSTRAINT agent_mcp_grant_token_has_subject
            CHECK ((transport_token_sha256 IS NULL) = (user_subject IS NULL));
    END IF;
END
$$;

CREATE OR REPLACE FUNCTION agent_mcp_narrow_update()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION '% rows are not deleted; revoke or close them', TG_TABLE_NAME;
    END IF;
    IF TG_TABLE_NAME = 'agent_mcp_grant' THEN
        IF (NEW.id, NEW.company_id, NEW.user_id, NEW.client_name, NEW.acl_keys, NEW.max_amount_paise,
            NEW.step_up, NEW.expires_at, NEW.created_at, NEW.transport_token_sha256, NEW.user_subject)
           IS DISTINCT FROM
           (OLD.id, OLD.company_id, OLD.user_id, OLD.client_name, OLD.acl_keys, OLD.max_amount_paise,
            OLD.step_up, OLD.expires_at, OLD.created_at, OLD.transport_token_sha256, OLD.user_subject) THEN
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

COMMENT ON COLUMN agent_mcp_grant.transport_token_sha256 IS
    'W6/D126 (wave-2 review). SHA-256 of the grant''s transport token, which '
    'authenticates this grant''s MCP transport route and nothing else.';
