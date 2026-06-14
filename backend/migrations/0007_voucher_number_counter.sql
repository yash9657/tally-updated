-- Migration 0007: server-side Tally auto-numbering counter.
--
-- voucher_number_counter holds one monotonic running counter per
-- (company, voucher_type, financial year). The allocator (internal/ledger/
-- numbering.go) advances it with a single atomic
-- `INSERT ... ON CONFLICT DO UPDATE ... RETURNING` run INSIDE the post()
-- transaction, so a number is allocated atomically with the voucher it labels
-- and concurrent CREATEs of the same series serialize on the PK row lock and
-- receive distinct consecutive numbers without any application-level locking.
--
-- next_no stores the NEXT FREE number for the series; the allocator's RETURNING
-- yields the number just consumed (1 on first insert, then 2, 3, ...). The
-- counter is monotonic: it advances ONLY when the server allocates (AUTOMATIC
-- create with an empty voucher_no). It is NEVER decremented or backfilled — a
-- DELETE leaves a gap and a manual override does not bump it. Numbers are
-- therefore allocation-ordered, not necessarily contiguous after deletes.
--
-- The financial year is the Indian FY start-year integer (Apr–Mar), derived
-- from the voucher_date in the allocator; a new FY restarts the series at 1.
--
-- No backfill is needed: fresh series start at 1, and pre-existing manual
-- numbers are irrelevant to the auto counter. The PK is the only index.
-- ---------------------------------------------------------------------------

CREATE TABLE voucher_number_counter (
    company_id      UUID NOT NULL,
    voucher_type_id UUID NOT NULL REFERENCES voucher_type (id),
    fin_year        INTEGER NOT NULL,
    next_no         BIGINT NOT NULL DEFAULT 1,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, voucher_type_id, fin_year)
);

COMMENT ON TABLE voucher_number_counter IS
    'Monotonic per-(company, voucher_type, financial year) running counter for '
    'Tally AUTOMATIC voucher numbering. next_no is the next free number; the '
    'allocator returns next_no-1 (the consumed number) and advances atomically '
    'inside the post() transaction. Never decremented: deletes leave gaps, '
    'manual overrides do not bump it.';

-- Default-deny RLS, matching voucher_head (0006) and every other tenant table.
ALTER TABLE voucher_number_counter ENABLE ROW LEVEL SECURITY;
ALTER TABLE voucher_number_counter FORCE  ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON voucher_number_counter
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON voucher_number_counter TO tally_app;
