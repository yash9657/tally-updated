-- 0034_payroll_schema.sql
-- THE PAYROLL SCHEMA: the employee master, its effective-dated salary structure
-- and statutory elections, the pay run, and the immutable payslips a pay run
-- produces -- plus the control ledgers and the `Payroll` voucher type the run
-- posts through.
--
-- WHAT CHANGED, AND WHY IT IS NOT OPTIONAL PAPERWORK
-- --------------------------------------------------
-- Before this migration there is NO employee surface anywhere in the product.
-- internal/payroll (the pure engine) can compute a payslip but has nowhere to
-- read a salary from and nowhere to write a result to, and internal/ledger has
-- a `Payroll` voucher class with no voucher_type row behind it -- which is not
-- a cosmetic gap: ledger.defaultVoucherTypeID returns uuid.Nil WITH NO ERROR
-- when it finds no system type for a class, so the first pay run would post
-- with voucher_version.voucher_type_id NULL. That is the exact NULL that
-- silently dropped altered invoices out of GSTR-1 (see the carry-forward note
-- in ledger.post) -- present in the Trial Balance, absent from every report
-- that reaches the class through a JOIN on voucher_type.
--
-- ===========================================================================
-- THE JUDGEMENT: `employee` IS ITS OWN TABLE AND NOT A LEDGER
-- ===========================================================================
-- Tally models an employee as a LEDGER under a payroll group. That was the real
-- alternative here, it is cheaper, and it is refused. Three reasons, the first
-- of which is decisive.
--
-- 1. DISCLOSURE. internal/reporting/daybook.go returns per-posting LedgerName +
--    Amount, and migration 0011 seeds the `Data Entry` level a COARSE
--    `allow display report` that matches EVERY report:NAME. A ledger named
--    "Ravi Kumar" carrying a credit of 84,317.00 would therefore be readable by
--    every operator in every tenant through the Day Book, the Trial Balance and
--    the Balance Sheet -- none of which have per-ledger access control, and
--    none of which we can retrofit one onto. Individual salaries are the most
--    disclosure-sensitive number in this product.
--
--    An AGGREGATE control ledger discloses the company's payroll TOTAL, which
--    is already a line on the P&L. That is the whole difference, and it is the
--    difference between "the wage bill was 8.4 lakh" and "Ravi earns 84,317 and
--    Priya earns 61,020".
--
-- 2. PII SHAPE. `ledger` has room for exactly ONE of the fields an employee
--    needs (pan). UAN, ESIC IP number, IFSC, date of joining, date of leaving
--    and the PF/ESI/regime elections would all have to be bolted onto the table
--    that EVERY voucher posts to and that the masters API returns. That is
--    widening the wrong table.
--
-- 3. EFFECTIVE DATING. A salary structure is versioned by nature (revisions,
--    arrears, a mid-year promotion). `ledger` has no versioning at all --
--    master_audit is an append-only audit log, not a queryable history -- and
--    "what was Ravi's basic in August" is a question a back-dated pay run MUST
--    be able to answer to recompute the month.
--
-- WHAT IS KEPT FROM TALLY'S MODEL: ledger.PostVoucher needs a LedgerID on every
-- posting, so `employee.payable_ledger_id` is a NULLABLE composite FK to
-- `ledger`. The DEFAULT is one aggregate "Salary Payable" for the whole run; a
-- company that WANTS per-employee outstandings sets the column and the net-pay
-- leg splits per employee. The posting path is identical either way.
--
-- ===========================================================================
-- WHAT IS DELIBERATELY ABSENT, AND WHY THAT IS A DECISION AND NOT AN OMISSION
-- ===========================================================================
-- There is NO `aadhaar` column and NO full bank account number column, and
-- neither is an oversight.
--
--   * AADHAAR: no statutory output in scope needs it. Form 138 (ex-24Q) needs
--     the PAN, the EPFO ECR needs the UAN, the ESI return needs the IP number.
--     There is no at-rest protection in the Go/DB plane to fall back on --
--     ledger.pan is plaintext today, pgcrypto is installed but used only for
--     gen_random_uuid(), and insightd's redaction boundary is Python and covers
--     only outbound cross-border LLM calls. Storing an Aadhaar would be a
--     brand-new exposure bought for nothing. internal/payroll/payrolldb goes
--     further and REJECTS an Aadhaar-shaped (Verhoeff-valid 12-digit) value
--     typed into any employee free-text field, so nobody smuggles one into
--     `designation`.
--
--   * FULL BANK ACCOUNT NUMBER: v1 emits no bank advice file, so the full
--     number would be pure exposure. IFSC + last four digits is enough to
--     reconcile a payment somebody else made. When a bank advice file ships,
--     the at-rest encryption decision falls due THEN, and this migration
--     deliberately does not pre-empt it.
--
-- WHAT IS STORED IS STORED BECAUSE A SHIPPED STATUTORY OUTPUT NEEDS IT: PAN
-- (Form 138 / Form 130), UAN (PF ECR), ESIC IP number (ESI return), bank IFSC
-- and bank_account_last4. None of it ever reaches the pure engine, which takes
-- `PANAvailable bool` and no identifier at all.
--
-- ===========================================================================
-- RE-RUNNABILITY
-- ===========================================================================
-- `cored migrate` applies each file in its own transaction, but a by-hand
-- `psql -f` has no such envelope and a run killed halfway leaves partial writes
-- behind WITHOUT stamping schema_migrations. Everything below is therefore a
-- no-op on a second run: IF NOT EXISTS on every object, pg_constraint /
-- pg_policies / pg_trigger guards on everything with no IF NOT EXISTS form, and
-- anti-joined (never looped) backfills. 0028's header records what a
-- per-company loop costs on this project's own dev database: 38,748 companies
-- reached 13% in ONE HOUR AND FIFTY-SEVEN MINUTES before it was killed.

-- ---------------------------------------------------------------------------
-- The composite tenant key on `ledger`, so employee.payable_ledger_id can
-- reference the PAIR.
--
-- Same argument 0025 made for stock_item and 0030 for account_group: RLS hides
-- another tenant's rows from a SELECT, but a plain REFERENCES ledger (id) would
-- ACCEPT a ledger id belonging to another company if one ever leaked in through
-- a request body, and an employee's net pay would then be payable to a control
-- ledger neither tenant can see. The composite FK makes the DATABASE refuse it,
-- with no dependence on the session GUC being right.
--
-- Guarded by name, not by IF NOT EXISTS (which ALTER TABLE ... ADD CONSTRAINT
-- does not have). The guard is doing real work here and not just ceremony:
-- migration 0032 (the supplier-document slice) adds a constraint of THIS EXACT
-- NAME for its own composite FK, so on any database where 0032 has run this
-- block is already a no-op. Two migrations needing the same key is not a
-- conflict -- it is two features discovering the same missing invariant -- and
-- the guard is what lets both state the requirement without either depending on
-- the other having landed.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'ledger_company_scope'
    ) THEN
        ALTER TABLE ledger
            ADD CONSTRAINT ledger_company_scope UNIQUE (company_id, id);
    END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- employee: the master.
--
-- It carries a real company_id column, like every other tenant table, and that
-- is load-bearing beyond RLS: internal/platform/testdb derives its teardown
-- plan FROM THE LIVE SCHEMA by enumerating tables with a company_id, so these
-- tables are torn down by every adopting test for free and
-- TestEveryTableIsClassifiedForTeardown stays green without anybody editing a
-- registry.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS employee (
    id                 UUID PRIMARY KEY,
    company_id         UUID NOT NULL,
    -- The employer's own staff number. It is what appears on a payslip and what
    -- an operator types, so it is the natural key within a company.
    code               TEXT NOT NULL,
    name               TEXT NOT NULL,
    doj                DATE NOT NULL,
    -- Date of leaving. NULL = still employed. It is not merely descriptive: the
    -- ESI return carries a Last Working Day and a leaving reason code, and the
    -- income-tax engine deducts the WHOLE remaining balance in an employee's
    -- final month rather than dividing it over months that will never happen.
    dol                DATE NULL,
    designation        TEXT NOT NULL DEFAULT '',
    -- The five identifiers, each present because a shipped statutory output
    -- needs it and for no other reason. Format is validated in Go
    -- (payrolldb/employee.go) rather than by a CHECK, so the operator gets a
    -- message naming the field instead of a constraint name -- but they are
    -- NULLABLE here on purpose: an employee below the PF/ESI thresholds may
    -- genuinely have no UAN and no IP number, and refusing to record them at
    -- all would be worse than recording them without one.
    pan                TEXT NULL,
    uan                TEXT NULL,
    esic_ip_number     TEXT NULL,
    bank_ifsc          TEXT NULL,
    bank_account_last4 TEXT NULL,
    -- OPT-IN per-employee outstanding. NULL (the default) means this employee's
    -- net pay lands in the company's aggregate "Salary Payable" control ledger
    -- and their individual figure never reaches the Day Book. See the header.
    payable_ledger_id  UUID NULL,
    is_active          BOOLEAN NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, code),
    -- Not redundant with the primary key: the child tables reference the PAIR,
    -- so a cross-tenant employee_id is refused by the FK itself rather than
    -- resting on RLS alone.
    CONSTRAINT employee_company_scope UNIQUE (company_id, id),
    -- MATCH SIMPLE (the default) means the FK is not checked when
    -- payable_ledger_id is NULL, which is exactly the opt-in semantics wanted.
    CONSTRAINT employee_payable_ledger_fk
        FOREIGN KEY (company_id, payable_ledger_id)
        REFERENCES ledger (company_id, id),
    CONSTRAINT employee_dol_not_before_doj
        CHECK (dol IS NULL OR dol >= doj),
    -- Four digits, or nothing. A column named "last4" holding a full account
    -- number is the failure this whole design is avoiding, so the shape is
    -- enforced where it cannot be argued with.
    CONSTRAINT employee_bank_account_last4_shape
        CHECK (bank_account_last4 IS NULL OR bank_account_last4 ~ '^[0-9]{4}$')
);
CREATE INDEX IF NOT EXISTS employee_company ON employee (company_id);

COMMENT ON TABLE employee IS
    'The payroll employee master. NOT a ledger: an employee-named ledger would '
    'put every individual salary into the Day Book, the Trial Balance and the '
    'Balance Sheet, none of which have per-ledger access control and all of '
    'which the seeded Data Entry level can read. There is deliberately NO '
    'aadhaar column and NO full bank account number: no shipped statutory '
    'output needs either, and there is no at-rest protection in this plane to '
    'store them behind. Their absence is a decision, not an omission.';

COMMENT ON COLUMN employee.payable_ledger_id IS
    'OPT-IN per-employee payable ledger. NULL (default) routes this employee''s '
    'net pay into the aggregate Salary Payable control ledger, so the '
    'individual figure never reaches the Day Book.';

-- ---------------------------------------------------------------------------
-- employee_salary_structure: what an employee is paid, effective-dated.
--
-- HALF-OPEN [effective_from, effective_to): effective_to is EXCLUSIVE and
-- NULLABLE (NULL = still in force). This copies migration 0030's price-list
-- convention verbatim in spirit, and 0030's header explains why the inclusive
-- alternative is worse: an inclusive upper bound makes the day of a revision
-- belong to BOTH the old structure and the new one, and a pay run for that
-- month would have two answers to "what is the basic".
--
-- The EXCLUDE constraint is the determinism guarantee. Without it, two
-- overlapping rows for the same component are not an error anywhere -- the
-- reader would pick one by whatever ORDER BY it happened to use, and the same
-- month would compute differently depending on insertion order.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS employee_salary_structure (
    id             UUID PRIMARY KEY,
    company_id     UUID NOT NULL,
    employee_id    UUID NOT NULL,
    effective_from DATE NOT NULL,
    effective_to   DATE NULL,
    component_code TEXT NOT NULL,
    -- The CLOSED vocabulary internal/payroll.ComponentTypeFor resolves, and the
    -- reason it is closed: s.2(88) of the Code on Social Security, 2020 lists
    -- the EXCLUSIONS from "wages" exhaustively, and everything not on that list
    -- IS wages. A free-text component name cannot be classified, so a component
    -- that fell through to "excluded" would under-contribute PF and ESI for the
    -- whole of that employee's service with no error anywhere. This CHECK and
    -- ComponentTypeFor's erroring default arm are the same closed set written
    -- twice, on purpose: a value one side accepts and the other refuses is a
    -- loud INSERT failure, not a silently wrong contribution.
    component_type TEXT NOT NULL,
    amount         NUMERIC(19,4) NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT employee_salary_structure_company_scope UNIQUE (company_id, id),
    CONSTRAINT employee_salary_structure_employee_fk
        FOREIGN KEY (company_id, employee_id)
        REFERENCES employee (company_id, id),
    -- The seventeen members of internal/payroll.ComponentType, verbatim. The
    -- first five ARE wages under s.2(88); the remaining twelve are its
    -- exhaustive exclusion list. SPECIAL_ALLOWANCE is on the INCLUDED side
    -- deliberately: it is not one of the s.2(88) exclusions, per Regional PF
    -- Commissioner v. Vivekananda Vidyamandir (SC, 2019), and treating it as
    -- excluded was the single most common PF under-contribution before that
    -- holding.
    CONSTRAINT employee_salary_structure_component_type_check
        CHECK (component_type IN (
            'BASIC',
            'DA',
            'RETAINING_ALLOWANCE',
            'SPECIAL_ALLOWANCE',
            'OTHER_ALLOWANCE',
            'HRA',
            'CONVEYANCE_ALLOWANCE',
            'OVERTIME_ALLOWANCE',
            'COMMISSION',
            'BONUS',
            'HOUSE_ACCOMMODATION_VALUE',
            'SPECIAL_EXPENSES',
            'AWARD_OR_SETTLEMENT',
            'EMPLOYER_PF_CONTRIBUTION',
            'GRATUITY',
            'RETRENCHMENT_COMPENSATION',
            'EX_GRATIA')),
    CONSTRAINT employee_salary_structure_window_half_open
        CHECK (effective_to IS NULL OR effective_to > effective_from),
    -- A negative earning is a deduction keyed into the wrong column, and it
    -- would flow straight into gross wages and out through the PF base.
    CONSTRAINT employee_salary_structure_amount_nonneg
        CHECK (amount >= 0)
);
CREATE INDEX IF NOT EXISTS employee_salary_structure_company
    ON employee_salary_structure (company_id);
CREATE INDEX IF NOT EXISTS employee_salary_structure_employee
    ON employee_salary_structure (company_id, employee_id, effective_from);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'employee_salary_structure_no_overlap'
    ) THEN
        ALTER TABLE employee_salary_structure
            ADD CONSTRAINT employee_salary_structure_no_overlap
            EXCLUDE USING gist (
                company_id     WITH =,
                employee_id    WITH =,
                component_code WITH =,
                daterange(effective_from, effective_to, '[)') WITH &&
            );
    END IF;
END
$$;

COMMENT ON TABLE employee_salary_structure IS
    'One EARNING component, for one employee, over one HALF-OPEN '
    '[effective_from, effective_to) window (effective_to NULL = still in '
    'force). The EXCLUDE constraint forbids two overlapping windows for the '
    'same component, so a back-dated pay run has exactly one answer to "what '
    'was this employee''s basic in that month". VOLUNTARY DEDUCTIONS (loan '
    'recovery, salary advance, LWF) are NOT modelled in v1 and this table '
    'holds no deduction rows: the only deductions a payslip carries are the '
    'statutory ones the engine computes plus voluntary PF, which comes from '
    'employee_statutory_election. That is a documented gap, not an oversight.';

-- ---------------------------------------------------------------------------
-- employee_statutory_election: the choices an employee (or the employer on
-- their behalf) has made that change the arithmetic.
--
-- These are genuinely COMPANY DATA and genuinely effective-dated, which is
-- exactly why they live in the database while the RATES do not. A rate is LAW:
-- it is identical for every tenant, it must never differ between two companies,
-- and it lives in Go (internal/payroll/rates.go) where it changes in a diff
-- with a reviewer and is re-derived independently by the corpus oracle. An
-- election is a fact about one person.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS employee_statutory_election (
    id             UUID PRIMARY KEY,
    company_id     UUID NOT NULL,
    employee_id    UUID NOT NULL,
    effective_from DATE NOT NULL,
    effective_to   DATE NULL,
    -- CEILING: contribute on min(statutory wage, the PF wage ceiling) -- the
    -- ordinary case. ACTUAL: contribute on the full statutory wage above the
    -- ceiling. The two produce materially different numbers for anyone paid
    -- above the ceiling, which is most salaried staff, so there is no safe
    -- default and payrolldb makes the caller state one.
    pf_base        TEXT NOT NULL,
    -- A voluntary provident fund base ABOVE the statutory one. NULL = none.
    pf_employee_voluntary_base    NUMERIC(19,4) NULL,
    -- Whether the employer matches the voluntary excess. It usually does not,
    -- and Form V / the remittance must be able to tell the statutory share from
    -- the voluntary one -- which is why the posting keeps them as separate
    -- credit lines rather than one netted figure.
    pf_employer_matches_voluntary BOOLEAN NOT NULL DEFAULT false,
    -- EPS membership. A member who FIRST joined above the wage ceiling is not
    -- an EPS member at all and the employer's whole share goes to EPF.
    eps_member     BOOLEAN NOT NULL DEFAULT true,
    esi_applicable BOOLEAN NOT NULL DEFAULT true,
    -- s.202 of the Income-tax Act 2025 makes the NEW regime the default; OLD is
    -- an election the employee makes. payrolldb defaults an empty value to
    -- 'NEW' and the engine defaults NOTHING, which is the split ledger/tds.go
    -- already uses.
    tax_regime     TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT employee_statutory_election_company_scope UNIQUE (company_id, id),
    CONSTRAINT employee_statutory_election_employee_fk
        FOREIGN KEY (company_id, employee_id)
        REFERENCES employee (company_id, id),
    CONSTRAINT employee_statutory_election_pf_base_check
        CHECK (pf_base IN ('CEILING', 'ACTUAL')),
    CONSTRAINT employee_statutory_election_tax_regime_check
        CHECK (tax_regime IN ('NEW', 'OLD')),
    CONSTRAINT employee_statutory_election_window_half_open
        CHECK (effective_to IS NULL OR effective_to > effective_from),
    CONSTRAINT employee_statutory_election_voluntary_base_nonneg
        CHECK (pf_employee_voluntary_base IS NULL OR pf_employee_voluntary_base >= 0)
);
CREATE INDEX IF NOT EXISTS employee_statutory_election_company
    ON employee_statutory_election (company_id);
CREATE INDEX IF NOT EXISTS employee_statutory_election_employee
    ON employee_statutory_election (company_id, employee_id, effective_from);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'employee_statutory_election_no_overlap'
    ) THEN
        ALTER TABLE employee_statutory_election
            ADD CONSTRAINT employee_statutory_election_no_overlap
            EXCLUDE USING gist (
                company_id  WITH =,
                employee_id WITH =,
                daterange(effective_from, effective_to, '[)') WITH &&
            );
    END IF;
END
$$;

COMMENT ON TABLE employee_statutory_election IS
    'One employee''s PF base / voluntary PF / EPS / ESI / tax-regime elections '
    'over one HALF-OPEN [effective_from, effective_to) window. Elections are '
    'company data and live here; statutory RATES are law, are identical for '
    'every tenant, and live in Go (internal/payroll/rates.go) -- a '
    'company_id-scoped rate table would let two companies compute different '
    'statutory tax, which is a wrong-number bug with no error path.';

-- ---------------------------------------------------------------------------
-- pay_run: one wage month's computation, and the voucher it posted as.
--
-- WHY voucher_id HAS NO FOREIGN KEY TO voucher_head, WHICH LOOKS LIKE AN
-- OVERSIGHT AND IS THE OPPOSITE OF ONE.
--
-- (*db.Pool).RunInTx always BEGINs a fresh pool transaction and does not nest,
-- and ledger.post() opens its own. "Write the pay-run rows AND post the
-- voucher" is therefore unavoidably TWO transactions, and the chosen order is
-- WRITE-DRAFT-THEN-POST: mint the voucher UUID app-side, write pay_run with
-- status='DRAFT' and voucher_id ALREADY SET and posted_at NULL, post the
-- voucher under that id, then set status='POSTED', posted_at=now().
--
-- A foreign key would refuse the DRAFT row outright, because the voucher does
-- not exist yet. Adding one would force the other order -- post first, record
-- afterwards -- and that order's crash window leaves a SALARY VOUCHER WITH NO
-- PAY RUN BEHIND IT: money moved, with no computation record, unauditable and
-- un-reproducible. This order's crash window leaves a DRAFT that names a
-- voucher, and recovery is then a pure function of state:
--
--   DRAFT + a voucher_head row for that id exists  -> just set posted_at
--   DRAFT + no voucher_head row                    -> re-post under the SAME id
--
-- The absence of the FK is what makes that recovery expressible at all.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pay_run (
    id                 UUID PRIMARY KEY,
    company_id         UUID NOT NULL,
    -- The FIRST of the wage month. Stored as a date rather than (year, month)
    -- so the half-open range comparisons the reports use are ordinary date
    -- arithmetic; the CHECK is what stops "2026-07-15" meaning July.
    wage_month         DATE NOT NULL,
    status             TEXT NOT NULL,
    -- Set at DRAFT time, before the voucher exists. See the header.
    voucher_id         UUID NULL,
    -- THE REPRODUCIBILITY STAMP. The rates themselves are in Go; what makes a
    -- re-run of this month verifiable is knowing WHICH ruleset window resolved
    -- when it was first computed. Without it, a re-run after a scheme change
    -- would quietly produce different numbers and nothing would say why.
    ss_ruleset_version TEXT NOT NULL,
    it_ruleset_version TEXT NOT NULL,
    created_by         TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    posted_at          TIMESTAMPTZ NULL,
    CONSTRAINT pay_run_company_scope UNIQUE (company_id, id),
    CONSTRAINT pay_run_status_check
        CHECK (status IN ('DRAFT', 'POSTED', 'REVERSED')),
    CONSTRAINT pay_run_wage_month_is_first_of_month
        CHECK (wage_month = date_trunc('month', wage_month)::date),
    -- posted_at is the REPLAY GUARD's authority (payrolldb re-reads the row and
    -- returns the existing result when it is set, the way internal/sync's
    -- headAny does), so it must agree with status or the guard reads a lie.
    -- REVERSED is exempt on both sides: a reversed run was posted and then
    -- deleted, so it legitimately keeps the timestamp of the post that happened.
    CONSTRAINT pay_run_posted_at_matches_status
        CHECK ((status = 'DRAFT'  AND posted_at IS NULL)
            OR (status = 'POSTED' AND posted_at IS NOT NULL)
            OR  status = 'REVERSED'),
    CONSTRAINT pay_run_posted_has_voucher
        CHECK (status = 'DRAFT' OR voucher_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS pay_run_company ON pay_run (company_id);
CREATE INDEX IF NOT EXISTS pay_run_month ON pay_run (company_id, wage_month);

-- ONE LIVE RUN PER MONTH. Partial, so a REVERSED run does not block the
-- corrected one that replaces it -- which is the entire point: reversing and
-- re-running a month is the normal remedy for a wrong pay run, and a plain
-- UNIQUE would make the remedy impossible without deleting the audit trail.
CREATE UNIQUE INDEX IF NOT EXISTS pay_run_one_live_per_month
    ON pay_run (company_id, wage_month)
 WHERE status <> 'REVERSED';

COMMENT ON TABLE pay_run IS
    'One wage month''s pay run. voucher_id is set while the row is still DRAFT '
    'and deliberately carries NO foreign key: the voucher is posted in a second '
    'transaction, and the alternative order would leave a salary voucher with '
    'no computation behind it. Crash recovery is a pure function of state -- '
    'DRAFT with the voucher present means finish the linkage, DRAFT without it '
    'means re-post under the same id.';

-- ---------------------------------------------------------------------------
-- payslip: one employee's result for one pay run. IMMUTABLE.
--
-- Immutable for the same reason posting, bill_allocation, voucher_gst_line and
-- stock_movement are: a payslip is what an employee was told they were paid and
-- what a statutory return was filed from. A correction REVERSES the pay run and
-- re-runs the month, which leaves both figures and the reason visible; an
-- UPDATE would leave the return filed from a number that no longer exists
-- anywhere.
--
-- MONEY IS NUMERIC(19,4) EVERYWHERE, never a float, matching decimal.Decimal on
-- the server. The statutory figures are nonetheless WHOLE RUPEES by the time
-- they land here (s.288B/s.516 for tax and the EPFO ECR's integer fields round
-- to the nearest rupee; Rule 51 of the ESI (Central) Rules 1950 rounds ESI UP
-- to the next higher rupee) -- the wider column stores them exactly rather than
-- inviting a second rounding.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS payslip (
    id               UUID PRIMARY KEY,
    company_id       UUID NOT NULL,
    pay_run_id       UUID NOT NULL,
    employee_id      UUID NOT NULL,
    -- Paid days and loss-of-pay days. The ESI return carries "No. of Days for
    -- which wages paid" and the ECR carries NCP days, so both are results of
    -- the run rather than annotations on it.
    days_paid        NUMERIC(9,4) NOT NULL DEFAULT 0,
    lop_days         NUMERIC(9,4) NOT NULL DEFAULT 0,
    gross_earnings   NUMERIC(19,4) NOT NULL DEFAULT 0,
    total_deductions NUMERIC(19,4) NOT NULL DEFAULT 0,
    net_pay          NUMERIC(19,4) NOT NULL DEFAULT 0,
    -- The Code on Social Security, 2020 wage (including the 50% deeming
    -- add-back), and the FOUR DIFFERENT BASES derived from it. EPF, EPS and
    -- EDLI wages are three different numbers and are never collapsed: the ECR
    -- has a column for each, and collapsing them is how an EPS contribution
    -- gets computed on a wage above the pension cap.
    statutory_wages  NUMERIC(19,4) NOT NULL DEFAULT 0,
    epf_wages        NUMERIC(19,4) NOT NULL DEFAULT 0,
    eps_wages        NUMERIC(19,4) NOT NULL DEFAULT 0,
    edli_wages       NUMERIC(19,4) NOT NULL DEFAULT 0,
    esi_wages        NUMERIC(19,4) NOT NULL DEFAULT 0,
    -- Statutory and VOLUNTARY provident fund kept apart, both sides. Form V and
    -- the remittance have to distinguish them, and a netted figure cannot be
    -- un-netted later.
    epf_employee_statutory NUMERIC(19,4) NOT NULL DEFAULT 0,
    epf_employee_voluntary NUMERIC(19,4) NOT NULL DEFAULT 0,
    epf_employer_statutory NUMERIC(19,4) NOT NULL DEFAULT 0,
    epf_employer_voluntary NUMERIC(19,4) NOT NULL DEFAULT 0,
    eps_employer     NUMERIC(19,4) NOT NULL DEFAULT 0,
    esi_employee     NUMERIC(19,4) NOT NULL DEFAULT 0,
    esi_employer     NUMERIC(19,4) NOT NULL DEFAULT 0,
    -- s.392 tax deducted this month. It is an ORDINARY CREDIT LEG on the pay
    -- run voucher and never ledger.VoucherInput.TDS -- that field drives the
    -- s.194x purchase/payment engine, which is a different computation on a
    -- different base.
    tds              NUMERIC(19,4) NOT NULL DEFAULT 0,
    -- WHAT THE ENGINE REFUSED TO GUESS. The EDLI Scheme 2026 contribution rate
    -- is expressly "to be notified separately", so the engine emits a Deferral
    -- rather than a number, and the deferral is recorded HERE rather than
    -- dropped -- a payslip that silently shows zero for an unnotified component
    -- is indistinguishable from one where the component genuinely does not
    -- apply. [{"component":...,"reason":...,"citation":...}].
    deferrals        JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, pay_run_id, employee_id),
    CONSTRAINT payslip_company_scope UNIQUE (company_id, id),
    CONSTRAINT payslip_pay_run_fk
        FOREIGN KEY (company_id, pay_run_id)
        REFERENCES pay_run (company_id, id),
    CONSTRAINT payslip_employee_fk
        FOREIGN KEY (company_id, employee_id)
        REFERENCES employee (company_id, id),
    CONSTRAINT payslip_deferrals_is_array
        CHECK (jsonb_typeof(deferrals) = 'array')
);
CREATE INDEX IF NOT EXISTS payslip_company ON payslip (company_id);
CREATE INDEX IF NOT EXISTS payslip_run ON payslip (company_id, pay_run_id);
CREATE INDEX IF NOT EXISTS payslip_employee ON payslip (company_id, employee_id);

-- ---------------------------------------------------------------------------
-- payslip_component: the LINES behind a payslip's totals. IMMUTABLE.
--
-- The tag is the disclosure the register needs: a statutory deduction is one
-- the law compelled, a voluntary one is one the employee chose, and a payslip
-- that cannot tell them apart cannot answer the only question an employee ever
-- asks about a deduction.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS payslip_component (
    id             UUID PRIMARY KEY,
    company_id     UUID NOT NULL,
    payslip_id     UUID NOT NULL,
    line_no        INTEGER NOT NULL,
    component_code TEXT NOT NULL,
    -- NOT constrained to the ComponentType vocabulary, and that is deliberate:
    -- a payslip line may be an earning drawn from the salary structure (BASIC,
    -- HRA) OR a computed statutory line that has no ComponentType at all
    -- (EPF_EMPLOYEE, ESI_EMPLOYEE, TDS). Constraining it to the earnings
    -- vocabulary would forbid exactly the lines the payslip exists to show.
    component_type TEXT NOT NULL,
    -- internal/payroll.ComponentTag: TagStatutory / TagVoluntary / TagNA.
    tag            TEXT NOT NULL,
    direction      TEXT NOT NULL,
    amount         NUMERIC(19,4) NOT NULL,
    CONSTRAINT payslip_component_company_scope UNIQUE (company_id, id),
    CONSTRAINT payslip_component_payslip_fk
        FOREIGN KEY (company_id, payslip_id)
        REFERENCES payslip (company_id, id),
    CONSTRAINT payslip_component_tag_check
        CHECK (tag IN ('STATUTORY', 'VOLUNTARY', 'NA')),
    CONSTRAINT payslip_component_direction_check
        CHECK (direction IN ('EARNING', 'DEDUCTION')),
    CONSTRAINT payslip_component_line_no_positive
        CHECK (line_no >= 1),
    UNIQUE (company_id, payslip_id, line_no)
);
CREATE INDEX IF NOT EXISTS payslip_component_company
    ON payslip_component (company_id);
CREATE INDEX IF NOT EXISTS payslip_component_payslip
    ON payslip_component (company_id, payslip_id);

-- ---------------------------------------------------------------------------
-- The immutability triggers, in migration 0025's form.
--
-- UPDATE and DELETE are still GRANTed to tally_app below, deliberately and for
-- 0025's stated reason: the trigger produces a clear domain error ('payslip is
-- immutable; UPDATE is not permitted') instead of a bare permission-denied that
-- says nothing about why. The teardown harness runs under a transaction-scoped
-- SET LOCAL session_replication_role = replica, which is how a test company's
-- payslips can be removed without ever disarming the trigger for anybody else.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION payslip_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'payslip is immutable; % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION payslip_component_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'payslip_component is immutable; % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger WHERE tgname = 'payslip_no_mutate'
    ) THEN
        CREATE TRIGGER payslip_no_mutate
            BEFORE UPDATE OR DELETE ON payslip
            FOR EACH ROW EXECUTE FUNCTION payslip_immutable();
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger WHERE tgname = 'payslip_component_no_mutate'
    ) THEN
        CREATE TRIGGER payslip_component_no_mutate
            BEFORE UPDATE OR DELETE ON payslip_component
            FOR EACH ROW EXECUTE FUNCTION payslip_component_immutable();
    END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- THE CONTROL LEDGERS, SEEDED BY NAME IN EVERY TENANT.
--
-- Seeded by NAME so the chart of accounts is IDENTICAL in every company and
-- internal/payroll/payrolldb can resolve them with a name lookup instead of
-- importing internal/masters (which would be a new module edge for nothing) or
-- making the caller pass seven ledger ids on every pay run (which would let one
-- tenant post its PF liability to its Sales ledger).
--
-- The seven, and why each side of the entry is where it is:
--
--   Dr Salaries & Wages            (Indirect Expenses)  gross earnings
--   Dr Employer PF Contribution    (Indirect Expenses)  employer EPF + EPS
--   Dr Employer ESI Contribution   (Indirect Expenses)  employer's 3.25% share
--     Cr Salary Payable            (Current Liabilities) net pay
--     Cr TDS on Salary Payable     (Duties & Taxes)      s.392 deduction
--     Cr PF Payable                (Current Liabilities) employee + employer PF
--     Cr ESI Payable               (Current Liabilities) employee + employer ESI
--
-- The employer's contributions are a COST (a debit to expense) and a LIABILITY
-- (a credit to payable) simultaneously; the employee's share is not a cost at
-- all -- it is part of gross pay that is withheld -- which is why the employee
-- half reaches PF Payable through the net-pay arithmetic and not through a
-- second expense debit. Booking the employee share as an expense would overstate
-- the wage bill by 12% of the PF wage in every company that used it.
--
-- ONE set-based statement, anti-joined. NOT a per-company loop: see the header.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION payroll_control_ledgers()
RETURNS TABLE (ledger_name TEXT, group_name TEXT) AS $$
    SELECT * FROM (VALUES
        ('Salaries & Wages',          'Indirect Expenses'),
        ('Employer PF Contribution',  'Indirect Expenses'),
        ('Employer ESI Contribution', 'Indirect Expenses'),
        ('Salary Payable',            'Current Liabilities'),
        ('PF Payable',                'Current Liabilities'),
        ('ESI Payable',               'Current Liabilities'),
        ('TDS on Salary Payable',     'Duties & Taxes')
    ) AS v(ledger_name, group_name);
$$ LANGUAGE sql IMMUTABLE;

CREATE OR REPLACE FUNCTION seed_payroll_control_ledgers(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    rec RECORD;
    grp UUID;
BEGIN
    FOR rec IN SELECT * FROM payroll_control_ledgers() LOOP
        SELECT id INTO grp
          FROM account_group
         WHERE company_id = p_company_id AND name = rec.group_name;
        -- A company whose chart is missing the group is left alone rather than
        -- failed: seed_company_defaults creates all three, so the only way to
        -- get here is a hand-built chart, and refusing to create a company
        -- because payroll could not place a ledger would be the wrong trade.
        IF grp IS NULL THEN
            CONTINUE;
        END IF;
        IF NOT EXISTS (
            SELECT 1 FROM ledger
             WHERE company_id = p_company_id AND name = rec.ledger_name
        ) THEN
            INSERT INTO ledger (id, company_id, name, group_id, is_system)
            VALUES (gen_random_uuid(), p_company_id, rec.ledger_name, grp, true);
        END IF;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION payroll_control_ledgers() TO tally_app;
GRANT EXECUTE ON FUNCTION seed_payroll_control_ledgers(UUID) TO tally_app;

INSERT INTO ledger (id, company_id, name, group_id, is_system)
SELECT gen_random_uuid(), ag.company_id, g.ledger_name, ag.id, true
  FROM payroll_control_ledgers() AS g
  JOIN account_group ag ON ag.name = g.group_name
 WHERE NOT EXISTS (
        SELECT 1 FROM ledger l
         WHERE l.company_id = ag.company_id AND l.name = g.ledger_name
 );

-- ---------------------------------------------------------------------------
-- THE `Payroll` VOUCHER TYPE.
--
-- parent_class MUST be the string 'PAYROLL' -- that is
-- upper(replace('Payroll', ' ', '_')), the exact form
-- ledger.defaultVoucherTypeID matches on:
--
--     WHERE parent_class = upper(replace($2, ' ', '_')) AND is_system = true
--
-- Get the spelling wrong (say 'Payroll') and that function returns
-- uuid.Nil WITH NO ERROR. The post then succeeds, numbering silently does not
-- run, and voucher_version.voucher_type_id goes NULL -- the exact NULL that
-- dropped altered invoices out of GSTR-1 and GSTR-3B while leaving them in the
-- Trial Balance. TestPayRunVoucherTypeIDIsNotNull exists solely to pin this
-- line, because nothing else in the system will complain.
--
-- is_accounting = true is DESCRIPTIVE (0029's header and the COMMENT on that
-- column explain why the post path never reads it); the authority is
-- ledger.accountingClasses, where `Payroll: true` is what makes a pay run state
-- balanced double-entry. affects_stock = false: a pay run moves no goods.
-- numbering AUTOMATIC gives salary vouchers their OWN series --
-- voucher_number_counter is keyed (company_id, voucher_type_id, fin_year) -- so
-- they do not interleave with journal numbers.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_payroll_voucher_types(p_company_id UUID)
RETURNS VOID AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM voucher_type
         WHERE company_id = p_company_id AND name = 'Payroll'
    ) THEN
        INSERT INTO voucher_type (id, company_id, name, parent_class,
                                  numbering_method, affects_stock,
                                  is_accounting, is_system)
        VALUES (gen_random_uuid(), p_company_id, 'Payroll', 'PAYROLL',
                'AUTOMATIC', false, true, true);
    END IF;
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_payroll_voucher_types(UUID) TO tally_app;

INSERT INTO voucher_type (id, company_id, name, parent_class,
                          numbering_method, affects_stock, is_accounting, is_system)
SELECT gen_random_uuid(), c.id, 'Payroll', 'PAYROLL',
       'AUTOMATIC', false, true, true
  FROM company c
 WHERE NOT EXISTS (
        SELECT 1 FROM voucher_type vt
         WHERE vt.company_id = c.id AND vt.name = 'Payroll'
 );

-- ---------------------------------------------------------------------------
-- Row-level security for the six new tenant tables: default-deny, ENABLE +
-- FORCE, USING + WITH CHECK pinned to the total app_current_company_id()
-- resolver -- identical to every other tenant table since 0004. The policy is
-- dropped-if-present first so a re-run of a half-applied migration does not
-- fail on a duplicate policy name.
--
-- FORCE is not decoration. Without it the table OWNER bypasses the policy, and
-- the owner is the role every migration and every by-hand psql session runs as
-- -- so a cross-tenant read would be invisible in exactly the sessions most
-- likely to perform one. TestPayrollTablesAreTenantIsolated proves a second
-- company sees zero rows through tally_app.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'employee', 'employee_salary_structure', 'employee_statutory_election',
        'pay_run', 'payslip', 'payslip_component'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I '
            || 'USING (company_id = app_current_company_id()) '
            || 'WITH CHECK (company_id = app_current_company_id())',
            t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON
    employee, employee_salary_structure, employee_statutory_election,
    pay_run, payslip, payslip_component
TO tally_app;

-- ---------------------------------------------------------------------------
-- Fold the two seeders into the per-company default seed so NEW companies get
-- the control ledgers and the Payroll voucher type at creation.
--
-- The whole body is restated (rather than wrapped) for the same reason 0025 and
-- 0029 restated theirs: a fresh database applying 0003 -> 0005 -> 0018 -> 0025
-- -> 0029 -> 0034 must end with ONE complete, self-describing definition. The
-- only change from 0029's version is the two final PERFORMs.
--
-- Without this, a company created after this migration would have no Payroll
-- voucher type and no control ledgers, and its first pay run would post with a
-- NULL voucher_type_id -- the same failure the backfill above exists to prevent
-- for companies created before it.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION seed_company_defaults(p_company_id UUID)
RETURNS VOID AS $$
DECLARE
    rec RECORD;
    grp_id      UUID;
    cash_grp    UUID;
    pl_primary  UUID;
BEGIN
    FOR rec IN
        SELECT * FROM (VALUES
            ('Capital Account',       'LIABILITY', false),
            ('Loans (Liability)',     'LIABILITY', false),
            ('Current Liabilities',   'LIABILITY', false),
            ('Fixed Assets',          'ASSET',     false),
            ('Investments',           'ASSET',     false),
            ('Current Assets',        'ASSET',     false),
            ('Branch / Divisions',    'ASSET',     false),
            ('Misc. Expenses (ASSET)','ASSET',     false),
            ('Suspense A/c',          'ASSET',     false),
            ('Sales Accounts',        'INCOME',    true),
            ('Purchase Accounts',     'EXPENSE',   true),
            ('Direct Incomes',        'INCOME',    true),
            ('Indirect Incomes',      'INCOME',    true),
            ('Direct Expenses',       'EXPENSE',   true),
            ('Indirect Expenses',     'EXPENSE',   true)
        ) AS v(name, nature, is_pl)
    LOOP
        grp_id := gen_random_uuid();
        INSERT INTO account_group (id, company_id, name, parent_id, path,
                                   nature, is_pl, is_system)
        VALUES (grp_id, p_company_id, rec.name, NULL,
                text2ltree('g' || replace(grp_id::text, '-', '')),
                rec.nature::account_nature, rec.is_pl, true);
    END LOOP;

    FOR rec IN
        SELECT * FROM (VALUES
            ('Bank Accounts',          'Current Assets',      false),
            ('Cash-in-Hand',           'Current Assets',      false),
            ('Deposits (Asset)',       'Current Assets',      false),
            ('Loans & Advances (Asset)','Current Assets',     false),
            ('Stock-in-Hand',          'Current Assets',      false),
            ('Sundry Debtors',         'Current Assets',      true),
            ('Duties & Taxes',         'Current Liabilities', false),
            ('Provisions',             'Current Liabilities', false),
            ('Sundry Creditors',       'Current Liabilities', true),
            ('Bank OD A/c',            'Loans (Liability)',   false),
            ('Secured Loans',          'Loans (Liability)',   false),
            ('Unsecured Loans',        'Loans (Liability)',   false),
            ('Reserves & Surplus',     'Capital Account',     false)
        ) AS v(name, parent_name, behaves_as_subledger)
    LOOP
        grp_id := gen_random_uuid();
        INSERT INTO account_group (id, company_id, name, parent_id, path,
                                   nature, is_pl, behaves_as_subledger, is_system)
        SELECT grp_id, p_company_id, rec.name, parent.id,
               parent.path || text2ltree('s' || replace(grp_id::text, '-', '')),
               parent.nature, parent.is_pl, rec.behaves_as_subledger, true
          FROM account_group parent
         WHERE parent.company_id = p_company_id AND parent.name = rec.parent_name;
    END LOOP;

    SELECT id INTO cash_grp FROM account_group
        WHERE company_id = p_company_id AND name = 'Cash-in-Hand';
    INSERT INTO ledger (id, company_id, name, group_id, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Cash', cash_grp, true);

    SELECT id INTO pl_primary FROM account_group
        WHERE company_id = p_company_id AND name = 'Capital Account';
    INSERT INTO ledger (id, company_id, name, group_id, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Profit & Loss A/c', pl_primary, true);

    INSERT INTO voucher_type (id, company_id, name, parent_class,
                              numbering_method, is_system)
    VALUES (gen_random_uuid(), p_company_id, 'Sales', 'SALES', 'AUTOMATIC', true);

    PERFORM seed_phase1_voucher_types(p_company_id);
    PERFORM seed_credit_debit_note_types(p_company_id);
    PERFORM seed_inventory_defaults(p_company_id);
    PERFORM seed_stock_voucher_types(p_company_id);
    -- The payroll control ledgers and the Payroll voucher type (this migration).
    PERFORM seed_payroll_control_ledgers(p_company_id);
    PERFORM seed_payroll_voucher_types(p_company_id);
END;
$$ LANGUAGE plpgsql;

GRANT EXECUTE ON FUNCTION seed_company_defaults(UUID) TO tally_app;
