-- 0014_insightd_draft.sql
-- DURABLE DRAFT STORE for the insightd AI plane: proposals survive an insightd
-- restart. Drafts are PROPOSALS, never ledger rows — nothing here is money and
-- nothing here can become a posting without a human pressing Ctrl+A in F9.
-- cored owns ALL schema (insightd has no migration mechanism), hence this table
-- lives here even though only insightd reads/writes it.
--
-- TWO DELIBERATE DIVERGENCES from the 0005/0009 tenant-table template — both
-- forced by the fact that insightd company ids are opaque STRINGS end-to-end,
-- not uuids (every benchmark corpus fixture uses "company_id": "CO-DEMO"):
--
--   (a) The tenant_isolation policy is pinned to the RAW text GUC
--       nullif(current_setting('app.current_company_id', true), '') instead of
--       the validated uuid resolver app_current_company_id() (0004). The
--       resolver RETURNS uuid, so `company_id (text) = app_current_company_id()
--       (uuid)` is a hard "operator does not exist" error, and even with an
--       explicit ::text cast the resolver returns NULL for anything that is not
--       a canonical 8-4-4-4-12 UUID — i.e. every non-UUID insightd company id
--       would be permanently invisible to itself.
--       The raw-GUC predicate is still TOTAL and FAIL-CLOSED: text equality
--       never raises, an unset GUC yields NULL (deny), and an empty GUC is
--       normalised to NULL by nullif (deny). The CHECK (company_id <> '')
--       below guarantees no stored row can ever be matched by that NULL/''
--       case, so "GUC not set" can never accidentally select rows.
--
--   (b) There is deliberately NO FOREIGN KEY to company(id): that column is
--       uuid and this one is text. Referential integrity for AI-plane proposals
--       is not a ledger invariant; tenant ISOLATION (below) is, and that is
--       enforced structurally by RLS rather than by an FK.
--
-- Scoping is on the EXACT company-id string the caller supplies (no case
-- folding). cored writes the canonical lowercase uuid form, so an
-- insightd caller must pass the same string cored used.

CREATE TABLE insightd_draft (
    company_id TEXT NOT NULL CHECK (company_id <> ''),
    draft_id   TEXT NOT NULL CHECK (draft_id <> ''),
    body       JSONB NOT NULL,   -- the full PurchaseVoucherDraft, serialized
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, draft_id)
);

COMMENT ON TABLE insightd_draft IS
    'insightd AI-plane draft proposals (PurchaseVoucherDraft JSON). Never a '
    'ledger row. RLS-scoped on the raw app.current_company_id text GUC because '
    'insightd company ids are strings, not uuids.';

-- ---------------------------------------------------------------------------
-- Row-level security: default-deny, ENABLE + FORCE (so even a table owner is
-- subject to it), USING + WITH CHECK pinned EXPLICITLY to the same predicate.
-- WITH CHECK is stated rather than left to Postgres's implicit "defaults to
-- USING" so the write-side guard is intentional (see 0004's rationale): an
-- INSERT/UPDATE carrying another company's id is refused, not silently kept.
-- ---------------------------------------------------------------------------
ALTER TABLE insightd_draft ENABLE ROW LEVEL SECURITY;
ALTER TABLE insightd_draft FORCE  ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON insightd_draft
    USING      (company_id = nullif(current_setting('app.current_company_id', true), ''))
    WITH CHECK (company_id = nullif(current_setting('app.current_company_id', true), ''));

-- ---------------------------------------------------------------------------
-- Least-privilege grants for the app role (NOBYPASSRLS).
-- ---------------------------------------------------------------------------
GRANT SELECT, INSERT, UPDATE, DELETE ON insightd_draft TO tally_app;
