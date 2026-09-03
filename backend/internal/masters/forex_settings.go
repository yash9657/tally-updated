package masters

// forex_settings.go — WHERE REALISED EXCHANGE DIFFERENCES GO (T8 step 5, migration 0080).
//
// ⛔ THIS FILE EXISTS BECAUSE A REFUSAL AN OPERATOR CANNOT SATISFY IS AN OUTAGE.
// ledger/forex_settlement.go refuses any settlement that would realise an exchange
// difference at a company with no gain/loss ledger chosen, naming the setting. Until
// this route existed the setting could only be written with raw SQL — so the refusal
// was a wall, not a prompt, and the whole of step 5 was unreachable from the product.
// It is therefore built FIRST of step 5's parts, and it is the cheapest to drive.
//
// ---------------------------------------------------------------------------
// ⛔ NOTHING IS AUTO-CREATED, AND THAT IS 0080's DECISION, NOT A LIMITATION
// ---------------------------------------------------------------------------
// The operator names an EXISTING ledger. The two alternatives 0080's header records
// are both worse: seeding a "Forex Gain/Loss" ledger on enable writes a master the
// operator never asked for into a chart of accounts that may already have one under
// another name, and guessing at post time puts a P&L line into an account nobody
// chose. So this route's only job is to record a choice, and its only refusals are
// about whether the thing chosen can hold one.
//
// ---------------------------------------------------------------------------
// ⭐ CLEARING THE CHOICE IS A REAL OPERATION, AND IT IS NOT A DELETE
// ---------------------------------------------------------------------------
// 0080 makes the column NULLABLE ON PURPOSE — "the row may exist to hold a future
// setting while no gain/loss ledger has been chosen yet, and 'not chosen' must stay
// distinguishable from 'chosen'." So an empty body CLEARS the choice and leaves the
// row, and ledger/forexGainLossLedger reads that state and the no-row state as the
// same "unset". ⚠️ A caller that cleared the choice and expected settlements to keep
// working has misread the feature: clearing it makes every difference-realising
// settlement refuse again, which is exactly what it should do.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// ErrForexLedgerUnsuitable is returned when the chosen ledger cannot hold a realised
// exchange difference.
//
// ⭐ A DISTINCT ERROR RATHER THAN THE DATABASE'S SENTENCE, for the reason
// ErrCurrencyIsBase is one: the operator's next action is to pick a different ledger,
// and a foreign-key violation names a constraint instead of saying so.
var ErrForexLedgerUnsuitable = errors.New(
	"that ledger cannot hold a realised exchange difference. Choose a revenue or " +
		"expense ledger — a gain is credited to it and a loss debited, so a party, " +
		"bank or cash ledger would move a balance that belongs to somebody else")

// ForexSettings is this company's forex configuration. One field today; a struct
// because 0080 named the table for the plural deliberately.
type ForexSettings struct {
	// GainLossLedgerID is uuid.Nil when no choice has been recorded — for BOTH of
	// 0080's encodings of that (no row at all, or a row with a NULL column). A
	// caller that needed to tell them apart would be reconstructing a distinction
	// the feature does not have.
	GainLossLedgerID uuid.UUID
	// GainLossLedgerName is the operator's spelling of it, so a screen need not
	// resolve a uuid against the ledger list to render what is configured. Empty
	// when nothing is chosen.
	GainLossLedgerName string
}

// ForexSettings returns the company's choice, or a zero value when none is recorded.
//
// ⚠️ ABSENCE IS NOT AN ERROR HERE, unlike TDSProfile's ErrNoTDSProfile, and the
// difference is real: an unfilled deductor profile makes a return unbuildable and a
// screen must not look saved, whereas "no forex ledger chosen" is the correct and
// ordinary state of every company that never trades in a foreign currency. Returning
// a sentinel would make every F11 screen render an error for the common case.
func (s *Service) ForexSettings(ctx context.Context) (ForexSettings, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return ForexSettings{}, db.ErrNoCompany
	}
	var out ForexSettings
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id *uuid.UUID
		var name *string
		// LEFT JOIN, because the choice may be NULL and an inner join would report
		// "no settings" for a row that exists with the choice cleared — collapsing
		// the two states 0080 went out of its way to keep apart.
		//
		// No WHERE company_id: RLS is ENABLE + FORCE on both tables and RunInTx sets
		// the GUC as the transaction's first statement, so a hand-written predicate
		// would be a second, weaker copy of the policy.
		err := tx.QueryRow(ctx, `
			SELECT f.forex_gain_loss_ledger_id, l.name
			  FROM company_forex_settings f
			  LEFT JOIN ledger l ON l.id = f.forex_gain_loss_ledger_id`).Scan(&id, &name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("masters: read company_forex_settings: %w", err)
		}
		if id != nil {
			out.GainLossLedgerID = *id
			if name != nil {
				out.GainLossLedgerName = *name
			}
		}
		return nil
	})
	return out, err
}

// SetForexSettings records the operator's choice. uuid.Nil CLEARS it.
//
// ⛔ THE LEDGER IS CHECKED FOR SUITABILITY, NOT MERELY FOR EXISTENCE. 0080's
// composite foreign key already refuses a ledger belonging to another company — and
// that is the tenancy guarantee, not a usability one. What it cannot refuse is a
// SUNDRY DEBTORS ledger, and choosing one is the mistake this screen invites: the
// realised difference would then be credited to a customer's own balance, which makes
// a settled bill show a permanent residue and is precisely the silent wrong number
// that Q3's refusal exists to prevent. The check is by GROUP NATURE rather than by
// group name, because a chart of accounts can rename any group.
func (s *Service) SetForexSettings(ctx context.Context, ledgerID uuid.UUID) error {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return db.ErrNoCompany
	}
	return s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var arg any
		if ledgerID != uuid.Nil {
			var nature string
			err := tx.QueryRow(ctx, `
				SELECT g.nature
				  FROM ledger l
				  JOIN account_group g ON g.id = l.group_id
				 WHERE l.id = $1`, ledgerID).Scan(&nature)
			if errors.Is(err, pgx.ErrNoRows) {
				// RLS makes another tenant's ledger invisible rather than forbidden,
				// so "no such ledger" is the honest sentence for both cases and it
				// leaks nothing about the other company's chart of accounts.
				return fmt.Errorf("masters: no such ledger: %s", ledgerID)
			}
			if err != nil {
				return fmt.Errorf("masters: read the chosen ledger's group: %w", err)
			}
			// ⚠️ THE VOCABULARY IS 0002's account_nature ENUM — 'ASSET', 'LIABILITY',
			// 'INCOME', 'EXPENSE': UPPERCASE AND SINGULAR. Spelled "Income"/"Expenses"
			// (the words the Gateway prints) this comparison matches nothing and every
			// ledger in every chart of accounts is refused as unsuitable, which is a
			// total outage of the setter that no build or type check would have caught.
			// account_group.is_pl agrees with this test and is not used instead only
			// because the enum is the primitive and is_pl is derived from it.
			if nature != "INCOME" && nature != "EXPENSE" {
				return fmt.Errorf("%w (that ledger is under a group of nature %q)",
					ErrForexLedgerUnsuitable, nature)
			}
			arg = ledgerID
		}
		// The company IS the key (company_forex_settings.company_id is its whole
		// primary key), so this is an upsert on one row and never an insert of a
		// second. company_id is passed rather than defaulted because the INSERT
		// branch has to state it; RLS's WITH CHECK then confirms it is this tenant's.
		if _, err := tx.Exec(ctx, `
			INSERT INTO company_forex_settings (company_id, forex_gain_loss_ledger_id)
			VALUES ($1, $2)
			ON CONFLICT (company_id) DO UPDATE
			  SET forex_gain_loss_ledger_id = EXCLUDED.forex_gain_loss_ledger_id`,
			companyID, arg); err != nil {
			return fmt.Errorf("masters: upsert company_forex_settings: %w", err)
		}
		return nil
	})
}
