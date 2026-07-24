-- 0013_statutory_step.sql
-- DURABLE StepStore for the statutoryd IRN workflow (WS7a). Closes the
-- HANDOFF §6 gap "no Postgres-backed StepStore (the durable runner is
-- in-memory, so workflow state does not survive a statutoryd restart)".
--
-- statutoryd is a SEPARATE deployable that talks to cored only over HTTP. It has
-- no migration mechanism of its own, so cored owns this DDL like all other
-- schema (see §1.3 of IMPLEMENTATION_PLAN.md). cored itself never reads or
-- writes this table.
--
-- Contract mirrored from internal/statutory/runner.go (MemStore):
--   * PRIMARY KEY (workflow_id, step_name) is what makes Append idempotent:
--     re-appending an already-completed step is a silent no-op
--     (INSERT ... ON CONFLICT DO NOTHING), exactly like MemStore's name scan.
--   * `seq` (identity) preserves append order for Load's ORDER BY. Identity
--     values are consumed even by a DO-NOTHING conflict, so seq HAS GAPS: it is
--     an ordering key only, never a step index or a count.
--   * `output` is the step's recorded JSON output. It is JSONB (not JSON), so
--     Postgres normalizes key order and whitespace. That is safe here because
--     every stored output is consumed via json.Unmarshal into a struct
--     (irn_workflow.go) and the provider hashes a freshly-marshalled struct, not
--     the stored bytes. Byte-for-byte fidelity would require type JSON.
--
-- *** DISCLOSED DIVERGENCE FROM THE RLS-EVERYWHERE NORM ***
-- This table has NO row-level security, unlike every user-facing table in this
-- schema. Rationale: it is not user-facing and is never reached by any cored
-- request path — it is written and read solely by the statutoryd service
-- process, which holds no user/company GUC (it authenticates to cored as the
-- service subject "svc:statutoryd" over HTTP, not as a tenant). There is no
-- app_current_company_id() to scope by at write time. Precedent inside this
-- schema: auth_login_attempt (0011) is likewise un-RLS'd because it is written
-- pre-auth.
--
-- EXPOSURE this buys, stated plainly so it is not rediscovered later: `output`
-- holds full e-invoice payloads (seller/buyer GSTINs and legal names, line
-- detail) for EVERY company in ONE unscoped table, readable by the same
-- `tally_app` role cored uses. Today nothing in cored can reach it (the NL-query
-- engine compiles a closed entity whitelist that does not include it, and no
-- repository references it), but any future cored code path that reaches
-- arbitrary tables WOULD cross tenants here. Tracked follow-up: give statutoryd
-- its own least-privilege role (`tally_statutoryd`) granted on this table only,
-- and revoke it from tally_app.
--
-- Concurrency: Append takes NO row lock (ON CONFLICT DO NOTHING), so two
-- statutoryd instances sharing this DSN could both execute the `generate` step
-- and call the real IRP twice, the loser's output being silently discarded.
-- Single-instance statutoryd remains the deployment shape (§7 deferral 7:
-- multi-instance statutoryd HA / leader election).
--
-- This file runs as the ADMIN DSN.

CREATE TABLE IF NOT EXISTS statutory_step (
    workflow_id TEXT        NOT NULL,
    step_name   TEXT        NOT NULL,
    seq         BIGINT      GENERATED ALWAYS AS IDENTITY,
    output      JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workflow_id, step_name)
);

-- Load() reads one workflow's steps in append order.
CREATE INDEX IF NOT EXISTS statutory_step_workflow_seq_idx
    ON statutory_step (workflow_id, seq);

-- Least-privilege: statutoryd connects as tally_app (NOBYPASSRLS). No sequence
-- grant is needed — GENERATED ALWAYS AS IDENTITY is driven by the table's
-- INSERT privilege (unlike 0011's BIGSERIAL auth_login_attempt).
REVOKE ALL ON statutory_step FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON statutory_step TO tally_app;
