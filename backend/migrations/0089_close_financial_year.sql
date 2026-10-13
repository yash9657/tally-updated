-- 0089_close_financial_year.sql
-- CLOSING A FINANCIAL YEAR IN PLACE: the books are locked where they are, and
-- nothing is copied, split or moved.
--
-- ===========================================================================
-- ⛔ WHY THIS IS NOT "SPLIT COMPANY DATA", AND WHY THAT MATTERS
-- ===========================================================================
-- Tally splits a company by financial year because it loads the whole company
-- into memory and RAM is the binding constraint (RESEARCH.md:987). This product
-- keeps unlimited multi-year history queryable at once, which is one of the
-- things it competes on, so splitting would be building a workaround for a
-- limit we do not have. FeaturesScreen's "Split company data" row has argued
-- that since it was written, and it stays a deliberate non-goal.
--
-- But that row's own note names a REAL need next door, and this migration is
-- that need and nothing more:
--
--   "closing a financial year into a READ-ONLY archive ... for statutory
--    year-end and for handing a closed year to an auditor"
--
-- The need is that a finalised year MUST NOT CHANGE. It is not a need for a
-- second company. So the year is frozen WHERE IT IS: the history stays in one
-- place, every cross-year report keeps working, and nothing is duplicated —
-- because a copy is a second thing that can disagree with the first.
--
-- ===========================================================================
-- ⭐ THE ENFORCEMENT ALREADY EXISTED. THIS COLUMN ONLY AIMS IT.
-- ===========================================================================
-- security_level.backdated_cutoff (migration 0011) already refuses Create,
-- Alter and Delete on any voucher dated before it, and authz has been hardened
-- twice over three rounds to close the ways around it — a DELETE carries no
-- date, and an ALTER's body states only where the voucher is GOING, so the PDP
-- also judges the date the document ALREADY HOLDS. Re-implementing a year-end
-- lock anywhere else would have re-opened both of those doors.
--
-- ⛔ SO WHY A NEW COLUMN AT ALL, RATHER THAN SETTING backdated_cutoff ON EVERY
-- LEVEL? Two reasons, and the second is a hole:
--
--   1. THEY ARE DIFFERENT STATEMENTS. "This clerk may not back-date more than a
--      week" is a fact about a ROLE. "The 2026-27 books are closed" is a fact
--      about the COMPANY, true for everyone including the Owner. Spelling the
--      second one as N copies of the first makes them impossible to tell apart
--      afterwards, and an administrator raising a clerk's limit would silently
--      re-open a filed year.
--   2. A LEVEL CREATED AFTERWARDS WOULD DEFAULT TO NULL. Close the year by
--      stamping every level, then add a "Reviewer" level next month, and that
--      level can post into the closed year. A lock with a hole that opens by
--      itself is not a lock.
--
-- The two compose instead: the effective cutoff for a request is the LATER of
-- the level's own cutoff and the day after the company's closed-through date.
-- See authz.effectiveCutoff.

ALTER TABLE company
    -- The last date of the CLOSED period. NULL means no year has been closed.
    --
    -- ⚠️ IT IS "THROUGH", INCLUSIVE, AND NOT A CUTOFF. A cutoff is the earliest
    -- date still WRITEABLE; this is the latest date already CLOSED. The two are
    -- one day apart and the off-by-one is the whole meaning of a year-end, so
    -- they are deliberately not the same type of thing and not the same name.
    -- Closing FY 2026-27 stores 2027-03-31, never 2027-04-01.
    ADD COLUMN books_closed_through DATE NULL,

    -- WHO closed it and WHEN. Not decoration: closing a year is the act that
    -- makes a period statutory, and "who decided this was final" is the first
    -- question anybody asks about a locked period — an auditor most of all.
    -- Both stay set after a re-open so the record is of what HAPPENED, not of
    -- what is currently true.
    ADD COLUMN books_closed_at TIMESTAMPTZ NULL,
    ADD COLUMN books_closed_by UUID NULL;

COMMENT ON COLUMN company.books_closed_through IS
    'Inclusive last date of the closed period; NULL = nothing closed. Enforced '
    'by authz alongside security_level.backdated_cutoff (the effective cutoff is '
    'the later of the two). Closing FY 2026-27 stores 2027-03-31.';

-- ⛔ NO ACL MIGRATION IS NEEDED AND THAT IS DELIBERATE, NOT AN OMISSION.
-- Both routes map to (Alter, "company") — the same authority as the F11 feature
-- settings and the print templates, because closing a year IS company
-- configuration. Migration 0022 seeds the Data Entry level `allow display
-- company` and nothing wider, and that level's default_access is 'none', so
-- `alter company` is default-DENIED for every seeded non-Owner level the day
-- these routes appear. Modelling it under anything reached by 0011's coarse
-- `allow display report` would have handed year-end closure to every operator
-- in every tenant.
