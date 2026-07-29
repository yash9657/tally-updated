package itc

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// TxRunner is the minimal database capability this package needs: run a unit of
// work inside a tenant-scoped transaction. *db.Pool satisfies it. It mirrors
// gstr.TxRunner rather than importing it, because this package must not depend
// on gstr for anything except the ITC-decision type (gstr_adapter.go).
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// Service is the ITC reconciliation service over the tenant-scoped runner.
type Service struct {
	runner TxRunner
}

// NewService constructs the reconciliation Service.
func NewService(runner TxRunner) *Service { return &Service{runner: runner} }

// Statement kinds, matching gst_inward_statement.kind's CHECK constraint.
const (
	KindGSTR2A = "GSTR2A"
	KindGSTR2B = "GSTR2B"
)

// FormGSTR3B is the only value gst_return_filing.form takes today.
const FormGSTR3B = "GSTR3B"

// IngestInput is one uploaded statement file.
type IngestInput struct {
	// Kind is GSTR2A or GSTR2B. It is stated by the CALLER and never guessed
	// from the file: the two are structurally identical in the rendering this
	// parser reads, and guessing wrong would put a 2A's documents under a 2B's
	// deemed-acceptance clock.
	Kind string
	// Raw is the file exactly as uploaded. The sha256 is taken over these bytes.
	Raw []byte
	// IngestedAt / IngestedBy are threaded from the handler boundary. Nothing in
	// this package calls time.Now().
	IngestedAt time.Time
	IngestedBy string
}

// IngestResult reports what an ingest did.
type IngestResult struct {
	StatementID  uuid.UUID
	ReturnPeriod string
	// Inserted is false when the identical file (same sha256, same kind, same
	// period) had already been ingested. Re-uploading is idempotent, not an
	// error: an operator who clicks twice must not end up with two statements.
	Inserted bool
	// Superseded is the number of previously-live statements this one replaced.
	Superseded int
	Documents  int
	Rejected   int
	// Skipped is what the parser could not read. Non-empty raises the
	// `statement-partially-parsed` disclosure on every return derived from it.
	Skipped []string
}

// ErrNoPeriod is returned when an uploaded statement carries no return period.
// It is fatal rather than inferred: the period decides which return the
// documents belong to and which deemed-acceptance clock applies to them, and a
// guessed period silently files credit in the wrong month.
var ErrNoPeriod = fmt.Errorf("itc: the statement carries no return period")

// Ingest parses and stores one statement file.
func (s *Service) Ingest(ctx context.Context, in IngestInput) (IngestResult, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return IngestResult{}, db.ErrNoCompany
	}
	if in.Kind != KindGSTR2A && in.Kind != KindGSTR2B {
		return IngestResult{}, fmt.Errorf("itc: unknown statement kind %q", in.Kind)
	}
	if in.IngestedAt.IsZero() {
		return IngestResult{}, fmt.Errorf("itc: IngestedAt is required (this package never calls time.Now)")
	}

	parsed, err := Parse2B(in.Raw, in.Kind)
	if err != nil {
		return IngestResult{}, err
	}
	if parsed.ReturnPeriod == "" {
		return IngestResult{}, ErrNoPeriod
	}
	if _, err := ParsePeriod(parsed.ReturnPeriod); err != nil {
		return IngestResult{}, err
	}

	out := IngestResult{ReturnPeriod: parsed.ReturnPeriod, Skipped: parsed.Skipped}
	for _, d := range parsed.Documents {
		if d.Rejected {
			out.Rejected++
		}
	}
	out.Documents = len(parsed.Documents)

	id, err := uuid.NewV7()
	if err != nil {
		return IngestResult{}, fmt.Errorf("itc: new statement id: %w", err)
	}

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var before int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM gst_inward_statement
			 WHERE kind = $1 AND return_period = $2 AND superseded_by IS NULL`,
			in.Kind, parsed.ReturnPeriod).Scan(&before); err != nil {
			return fmt.Errorf("itc: count live statements: %w", err)
		}

		stmtID, inserted, err := insertStatement(ctx, tx, companyID, id,
			in.Kind, parsed.ReturnPeriod, parsed.FilerGSTIN, parsed.GenDate,
			parsed.Chksum, parsed.Version, parsed.SourceSHA256, in.Raw,
			in.IngestedAt, in.IngestedBy)
		if err != nil {
			return err
		}
		out.StatementID = stmtID
		out.Inserted = inserted
		if !inserted {
			return nil
		}
		out.Superseded = before
		return insertDocuments(ctx, tx, companyID, stmtID, parsed.Documents)
	})
	if err != nil {
		return IngestResult{}, err
	}
	return out, nil
}

// ActionInput records one IMS action against one ingested document.
type ActionInput struct {
	DocumentID uuid.UUID
	Kind       string
	Reason     string
	// PendingCase classifies the record for the PENDING restriction. It is an
	// explicit input because three of the four restricted categories turn on
	// facts about the ORIGINAL record that live outside any single statement row.
	PendingCase PendingCase
	ActedAt     time.Time
	ActedBy     string
}

// RecordAction appends one IMS action.
//
// THE LOG IS APPEND-ONLY AND IS NEVER UPDATED. The effective action is a
// last-write-wins PROJECTION over it (EffectiveAction). Storing the effective
// action instead would destroy the only evidence of WHEN the filer acted, and
// "when" is exactly what the freeze rule turns on.
func (s *Service) RecordAction(ctx context.Context, in ActionInput) error {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	if in.ActedAt.IsZero() {
		return fmt.Errorf("itc: ActedAt is required (this package never calls time.Now)")
	}
	if in.PendingCase == "" {
		in.PendingCase = PendingOrdinary
	}

	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("itc: new action id: %w", err)
	}

	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			section  string
			reverse  bool
			rejected bool
			period   string
		)
		if err := tx.QueryRow(ctx, `
			SELECT d.section, d.reverse_charge, d.rejected, st.return_period
			  FROM gst_inward_document d
			  JOIN gst_inward_statement st
			    ON st.company_id = d.company_id AND st.id = d.statement_id
			 WHERE d.id = $1`, in.DocumentID).Scan(&section, &reverse, &rejected, &period); err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("itc: no such inward document %s", in.DocumentID)
			}
			return fmt.Errorf("itc: load document for action: %w", err)
		}
		if rejected {
			// A docRejdata row is VIEW-ONLY (GSTN IMS FAQ 24). Accepting an
			// action against one would create a log entry the portal has no
			// counterpart for.
			return fmt.Errorf("%w: the document came from docRejdata and is view-only",
				ErrIMSNotApplicable)
		}

		p, err := ParsePeriod(period)
		if err != nil {
			return err
		}
		filed, err := loadFiling(ctx, tx, FormGSTR3B, period)
		if err != nil {
			return err
		}
		if err := ValidateAction(in.Kind, section, reverse, p, in.PendingCase, filed != nil); err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO gst_ims_action
			  (id, company_id, document_id, action, reason, acted_at, acted_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			id, companyID, in.DocumentID, in.Kind, nullStr(in.Reason), in.ActedAt, in.ActedBy)
		if err != nil {
			return fmt.Errorf("itc: insert ims action: %w", err)
		}
		return nil
	})
}

// RecordFiling stamps the IMS FREEZE instant for a period. After it, no IMS
// action counts (GSTN IMS FAQ 12).
func (s *Service) RecordFiling(ctx context.Context, form, period string, filedAt time.Time, filedBy string) error {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	if form != FormGSTR3B {
		return fmt.Errorf("itc: unknown return form %q", form)
	}
	if _, err := ParsePeriod(period); err != nil {
		return err
	}
	if filedAt.IsZero() {
		return fmt.Errorf("itc: filedAt is required (this package never calls time.Now)")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("itc: new filing id: %w", err)
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO gst_return_filing (id, company_id, form, return_period, filed_at, filed_by)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (company_id, form, return_period) DO NOTHING`,
			id, companyID, form, period, filedAt, filedBy)
		if err != nil {
			return fmt.Errorf("itc: insert return filing: %w", err)
		}
		return nil
	})
}

// Options configures a reconciliation.
type Options struct {
	// Kind selects which statement to reconcile against. Defaults to GSTR2B.
	Kind string
	// AsOn is the instant the reconciliation is evaluated at. REQUIRED: nothing
	// in this package calls time.Now(), so the handler boundary owns the clock.
	AsOn time.Time
	// AnnualReturnFiledOn brings the s.16(4) expiry forward when the annual
	// return for the relevant year has been filed.
	AnnualReturnFiledOn *time.Time
	// Rule4243Supplied reports whether the caller supplied rule 42/43 inputs to
	// the RETURN. This package neither holds them nor computes the formula
	// (compliance/gstr owns it); it only needs to know so the
	// `rule-42-43-not-computed` disclosure is right.
	Rule4243Supplied bool
	// RoundingTolerance overrides RoundingToleranceRupees when non-zero. The
	// zero value means "use the default", which is why it is a plain
	// decimal.Decimal and not a pointer: a tolerance of exactly zero would mean
	// "no rounding tolerance at all", and nobody wants that.
	RoundingTolerance decimal.Decimal
}

// Report is one reconciliation, ready to be projected into a return.
type Report struct {
	// Period is the tax period reconciled, in MMYYYY form.
	Period string
	// StatementID is the statement the reconciliation was derived FROM, or the
	// nil UUID when none has been ingested. A return that cannot name which 2B
	// produced it is not explainable.
	StatementID uuid.UUID
	// StatementGenDate is that statement's own generation date.
	StatementGenDate *time.Time
	// Resolution / Freeze are the two instants that decide the IMS projection.
	Resolution time.Time
	Freeze     *time.Time
	Lines      []Line
	// RejectedStatementRows are the docRejdata rows: VIEW-ONLY records (GSTN IMS
	// FAQ 24) that produce no reconciliation verdict and contribute nothing to
	// any table, but which the operator must be able to see — a supplier has
	// reported something against this GSTIN and it was refused.
	RejectedStatementRows []StatementRow
	Totals                Totals
	// Disclosures is the CLOSED vocabulary of reasons this return is not
	// filing-ready. It is a FIELD, not prose, so a frontend can refuse to
	// present the return as final.
	Disclosures []string
}

// ErrWindowNotOnePeriod is returned when [from, to] is not exactly one whole
// calendar month.
//
// A RECONCILIATION IS PER TAX PERIOD. GSTR-2B is generated per return period
// (MMYYYY) and this function loads exactly ONE of them — the one `from` falls
// in — while the BOOKS side would happily load whatever range it is given. On a
// wider window the two halves are keyed differently: every purchase outside the
// statement's own month has nothing to match against, is classified
// MISSING_IN_2B and is REVERSED IN FULL in table 4(B)(2). A quarterly call
// would therefore reverse two thirds of the company's input tax credit while
// looking entirely successful.
//
// Refusing is the deliberate choice over silently widening the load (the same
// stance ValuerFor takes): the caller knows which period it wants a return for,
// and a reconciliation that quietly answered for a different span than it was
// asked about would be a worse failure than an error.
var ErrWindowNotOnePeriod = fmt.Errorf(
	"itc: a reconciliation window must be exactly one whole tax period (calendar month)")

// checkOnePeriod enforces the one-period contract on an already-parsed window.
//
// It is split out of Reconcile so it can be exercised without a database, and
// so the three ways a window can be wrong — spanning months, starting late,
// ending early — each get their own message naming what would go wrong in
// rupees rather than a generic "invalid range".
func checkOnePeriod(fromT, toT time.Time, from, to string) error {
	p, q := PeriodOf(fromT), PeriodOf(toT)
	if p != q {
		n := (q.Year*12 + int(q.Month)) - (p.Year*12 + int(p.Month)) + 1
		unloaded := ""
		for i := 1; i < n; i++ {
			if i > 1 {
				unloaded += " and "
			}
			unloaded += p.AddMonths(i).String()
		}
		return fmt.Errorf("%w: window %s..%s spans %d tax periods but a reconciliation is "+
			"per-period; only %s's GSTR-2B would be loaded and every purchase in %s would be "+
			"wrongly reversed in 4(B)(2). Call Reconcile once per period",
			ErrWindowNotOnePeriod, from, to, n, p, unloaded)
	}
	// Calendar-month arithmetic, never day arithmetic: the last day of the
	// period is the day before the first day of the next one, which is right
	// for 28, 29, 30 and 31 day months alike (see Period.AddMonths).
	first := p.Start()
	last := p.AddMonths(1).Start().AddDate(0, 0, -1)
	if !fromT.Equal(first) {
		return fmt.Errorf("%w: window %s..%s starts inside tax period %s rather than on its "+
			"first day %s; the purchases dated before %s would be dropped from the books side "+
			"while the whole of %s's GSTR-2B is loaded, so their input tax would go missing "+
			"from 4(A) and the supplier's matching documents would be reported as excess "+
			"credit. Call Reconcile with the whole period",
			ErrWindowNotOnePeriod, from, to, p, first.Format("2006-01-02"), from, p)
	}
	if !toT.Equal(last) {
		return fmt.Errorf("%w: window %s..%s ends inside tax period %s rather than on its "+
			"last day %s; the purchases dated after %s would be dropped from the books side "+
			"while the whole of %s's GSTR-2B is loaded, so their input tax would go missing "+
			"from 4(A) and the supplier's matching documents would be reported as excess "+
			"credit. Call Reconcile with the whole period",
			ErrWindowNotOnePeriod, from, to, p, last.Format("2006-01-02"), to, p)
	}
	return nil
}

// Reconcile builds the reconciliation for the [from, to] inclusive date range.
//
// CONTRACT: ONE CALL PER TAX PERIOD. [from, to] must be exactly one whole
// calendar month — the first day of a month to the last day of that same month.
// Anything else returns ErrWindowNotOnePeriod (match it with errors.Is) and no
// report. A caller that wants a quarter or a year ITERATES THE MONTHS and calls
// this once per month; it must not widen the window, because only ONE period's
// GSTR-2B is ever loaded and everything outside that month would be classified
// MISSING_IN_2B and reversed in full in 4(B)(2).
//
// THE RESULT IS NEVER STORED. It is a function of four versioned inputs — the
// books, the statement, the action log and the policy — and it goes stale the
// instant any of them moves. This is the same architectural stance the inventory
// wave took with "STOCK VALUE IS NEVER STORED", for the same reason: a
// materialised answer is wrong the moment an input changes, and nothing tells
// you which.
func (s *Service) Reconcile(ctx context.Context, from, to string, opts Options) (Report, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return Report{}, db.ErrNoCompany
	}
	fromT, err := time.ParseInLocation("2006-01-02", from, IST)
	if err != nil {
		return Report{}, fmt.Errorf("itc: invalid from date %q: %w", from, err)
	}
	toT, err := time.ParseInLocation("2006-01-02", to, IST)
	if err != nil {
		return Report{}, fmt.Errorf("itc: invalid to date %q: %w", to, err)
	}
	if toT.Before(fromT) {
		return Report{}, fmt.Errorf("itc: period end %s precedes start %s", to, from)
	}
	// The one-period guard runs BEFORE any work: a wrong window must never
	// reach the loaders, because the failure mode is a plausible-looking report
	// that reverses real credit rather than an obvious crash.
	if err := checkOnePeriod(fromT, toT, from, to); err != nil {
		return Report{}, err
	}
	if opts.AsOn.IsZero() {
		return Report{}, fmt.Errorf("itc: Options.AsOn is required — this package never calls time.Now()")
	}
	kind := opts.Kind
	if kind == "" {
		kind = KindGSTR2B
	}

	period := PeriodOf(fromT)
	rep := Report{Period: period.String()}

	var (
		books   []BooksRow
		stmt    []StatementRow
		actions map[uuid.UUID][]Action
		haveStm bool
	)

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if books, err = loadBooks(ctx, tx, from, to); err != nil {
			return err
		}
		stID, genDate, ok, err := loadLatestStatement(ctx, tx, kind, period.String())
		if err != nil {
			return err
		}
		if ok {
			haveStm = true
			rep.StatementID = stID
			rep.StatementGenDate = genDate
			if stmt, err = loadStatementDocuments(ctx, tx, stID); err != nil {
				return err
			}
			if actions, err = loadActions(ctx, tx, stID); err != nil {
				return err
			}
		}
		if rep.Freeze, err = loadFiling(ctx, tx, FormGSTR3B, period.String()); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Report{}, err
	}

	rep.Resolution = ResolutionInstant(period, rep.StatementGenDate)

	policy := Policy{
		AsOn: opts.AsOn,
		// Period is set explicitly rather than left for Policy.claimPeriod to
		// recover from Resolution: the recovery walks one calendar month back
		// from the 2B instant and gets the WRONG month for a statement
		// generated more than a month late, which would move the s.16(4)
		// time-bar. The one-period contract above is what makes this single
		// value correct for the whole report.
		Period:              period,
		Resolution:          rep.Resolution,
		Freeze:              rep.Freeze,
		Actions:             actions,
		AnnualReturnFiledOn: opts.AnnualReturnFiledOn,
		RoundingTolerance:   opts.RoundingTolerance,
		Rule4243Supplied:    opts.Rule4243Supplied,
	}

	rep.Lines = Match(books, stmt, policy)
	rep.RejectedStatementRows = RejectedRows(stmt)
	rep.Totals = Totalise(rep.Lines, policy)
	rep.Disclosures = Disclosures(rep.Lines, haveStm, policy, nil)
	return rep, nil
}

// ---------------------------------------------------------------------------
// The as-on instant, threaded through the context
// ---------------------------------------------------------------------------

// asOnKey is the private context key carrying the reconciliation instant.
type asOnKeyType struct{}

var asOnKey asOnKeyType

// WithAsOn puts the reconciliation instant on the context.
//
// It exists for ONE reason: gstr's ITC-source interface is
// `Decisions(ctx, from, to)` — it has no place for a clock — and NOTHING in this
// package may call time.Now(). A boundary that reads the wall clock cannot be
// tested at the boundary, and every boundary in this package (deemed
// acceptance, the IMS freeze, s.16(4) expiry) decides which TAX PERIOD a credit
// lands in. So the handler that knows what "now" means puts it here, and this
// package reads it.
//
// The alternative — defaulting to time.Now() when the context carries nothing —
// was rejected: it would make the no-wall-clock rule true only of the code paths
// somebody remembered, which is the same as not having the rule.
func WithAsOn(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, asOnKey, t)
}

// ErrNoAsOn is returned when a reconciliation is requested through an interface
// that carries no clock and the context does not supply one.
var ErrNoAsOn = fmt.Errorf(
	"itc: no as-on instant on the context — call itc.WithAsOn(ctx, t) at the handler " +
		"boundary; this package never calls time.Now()")

// asOnFromContext reads the instant WithAsOn placed on the context.
func asOnFromContext(ctx context.Context) (time.Time, bool) {
	t, ok := ctx.Value(asOnKey).(time.Time)
	return t, ok && !t.IsZero()
}
