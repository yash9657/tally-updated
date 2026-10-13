package printingdb

// template.go — the print_template override store: one row per (company,
// document kind), read on every print and written when somebody customises a
// document.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/printing"
)

// ErrUnknownDocKind is returned for a DocKind this system cannot print.
//
// IT IS AN ERROR AND NOT A ZERO VALUE, and that is the entire point of having
// it. LoadOverrides returns the zero overrides for an ABSENT ROW, so if it also
// returned the zero overrides for a MISSPELLED KIND the two would be
// indistinguishable: a caller asking for "invoice" instead of "tax-invoice"
// would be told, forever and silently, that this company has no customisation —
// and the company's letterhead, declaration and bank details would quietly stop
// appearing on its invoices with nothing anywhere reporting a fault.
var ErrUnknownDocKind = errors.New("printingdb: unknown document kind")

// knownKinds is the CLOSED set of printing.DocKind values, and it is the same
// vocabulary migration 0037's print_template_doc_kind_known CHECK spells out.
//
// WRITTEN TWICE ON PURPOSE. A kind one side accepts and the other refuses is a
// loud failure at the moment a document type is added — either an INSERT that
// fails with the constraint name or an ErrUnknownDocKind naming the value —
// rather than a row that is stored, is never matched by any renderer, and looks
// from the UI like a customisation that simply does not apply.
var knownKinds = map[printing.DocKind]bool{
	printing.KindTaxInvoice:   true,
	printing.KindBillOfSupply: true,
	printing.KindDeliveryNote: true,
	printing.KindReceipt:      true,
	printing.KindPayment:      true,
	printing.KindCreditNote:   true,
	printing.KindDebitNote:    true,
	printing.KindReport:       true,
}

// KnownKind reports whether kind is a document this system can print. Exported
// so an HTTP layer can reject a bad path segment with a 400 before it opens a
// transaction.
func KnownKind(kind printing.DocKind) bool { return knownKinds[kind] }

// knownKindList renders the vocabulary for an error message, sorted so the
// message is byte-stable (a map range would reorder it every run and make the
// error impossible to assert on).
func knownKindList() string {
	out := make([]string, 0, len(knownKinds))
	for k := range knownKinds {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// checkKind validates kind before any database round-trip.
func checkKind(kind printing.DocKind) error {
	if !knownKinds[kind] {
		return fmt.Errorf("%w: %q is not a document this system prints (known kinds: %s)",
			ErrUnknownDocKind, string(kind), knownKindList())
	}
	return nil
}

// loadColumns is the read projection, in the order scanOverrides expects.
const loadColumns = `title, declaration, terms, signature_label,
       header_lines, footer_lines, bank_lines, show_bank_details`

// LoadOverrides returns the company's overrides for kind.
//
// A COMPANY WITH NO ROW GETS THE ZERO VALUE AND NO ERROR. That is the contract
// the whole design rests on: the built-in default template lives in code, this
// table holds only what a company changed, and so a company created moments ago
// prints a correct document with nothing seeded on its behalf. Callers must NOT
// treat the zero value as a failure — it is the ordinary case, and it will be
// the case for most companies forever.
//
// The transaction is READ-ONLY and tenant-scoped. Printing is not a write.
func (s *Store) LoadOverrides(ctx context.Context, companyID uuid.UUID, kind printing.DocKind) (printing.TemplateOverrides, error) {
	o, _, err := s.LoadOverridesRow(ctx, companyID, kind)
	return o, err
}

// LoadOverridesRow is LoadOverrides plus the ONE BIT LoadOverrides throws away:
// whether a row exists at all.
//
// ⛔ WHY AN EDITING SCREEN CANNOT USE LoadOverrides. The zero value is returned
// both for "this company has never customised this document" and for "this
// company customised it to blank on purpose", and those are the same PDF but not
// the same sentence. A form that cannot tell them apart shows empty boxes to a
// company that has never been asked anything and reads as "you have no
// declaration" — and, worse, it cannot offer to CLEAR an override, because it
// does not know there is one to clear.
//
// The printing path deliberately keeps using LoadOverrides: a renderer that
// branched on presence would print two different documents from one row state.
// Presence is a fact about the CONFIGURATION, not about the paper.
func (s *Store) LoadOverridesRow(ctx context.Context, companyID uuid.UUID, kind printing.DocKind) (printing.TemplateOverrides, bool, error) {
	var o printing.TemplateOverrides
	if err := checkKind(kind); err != nil {
		return o, false, err
	}
	found := false
	err := s.inTenantTx(ctx, companyID, true, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+loadColumns+`
			   FROM print_template
			  WHERE company_id = $1 AND doc_kind = $2`,
			companyID, string(kind))
		switch err := scanOverrides(row, &o); {
		case err == nil:
			found = true
			return nil
		case errors.Is(err, pgx.ErrNoRows):
			// The ordinary case. Leave o at its zero value: the renderer falls
			// back to the built-in default for every field.
			o = printing.TemplateOverrides{}
			return nil
		default:
			return fmt.Errorf("printingdb: load overrides for %s: %w", kind, err)
		}
	})
	if err != nil {
		return printing.TemplateOverrides{}, false, err
	}
	return o, found, nil
}

// DeleteOverrides removes the company's override row for kind, returning whether
// there was one to remove.
//
// ⛔ WHY A DELETE AND NOT A SAVE OF BLANKS. Both make the same document come out
// of the printer, so the temptation is to let "clear" be an upsert of empty
// strings and save a method. That loses the only two facts the table exists to
// carry. First, an all-blank row still says "this company HAS a customisation",
// so the screen can never again tell the operator they are on the built-in
// layout. Second, print_template.updated_at answers "when did our invoice layout
// last change?", and a blank row answers it with the date somebody reverted —
// while the honest answer is that there is no customisation to date.
//
// IDEMPOTENT ON PURPOSE: clearing an override that is not there is not an error,
// it is a company already on the built-in layout. The bool is for a caller that
// wants to SAY which happened, not for one that wants to fail.
func (s *Store) DeleteOverrides(ctx context.Context, companyID uuid.UUID, kind printing.DocKind) (bool, error) {
	if err := checkKind(kind); err != nil {
		return false, err
	}
	removed := false
	err := s.inTenantTx(ctx, companyID, false, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM print_template
			  WHERE company_id = $1 AND doc_kind = $2`,
			companyID, string(kind))
		if err != nil {
			return fmt.Errorf("printingdb: delete overrides for %s: %w", kind, err)
		}
		removed = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return removed, nil
}

// scanOverrides reads one row into o.
//
// The array fields are scanned into plain []string. A SQL NULL arrives as nil
// and an empty array as a non-nil, zero-length slice, and KEEPING THOSE TWO
// APART is deliberate: nil means "not overridden, print the built-in lines" and
// empty means "overridden with nothing, print no lines at all". A company that
// asked for its footer to be suppressed is saying something different from a
// company that has never been asked about its footer.
func scanOverrides(row pgx.Row, o *printing.TemplateOverrides) error {
	return row.Scan(
		&o.Title,
		&o.Declaration,
		&o.Terms,
		&o.SignatureLabel,
		&o.HeaderLines,
		&o.FooterLines,
		&o.BankLines,
		&o.ShowBankDetails,
	)
}

// SaveOverrides upserts the company's overrides for kind.
//
// UPSERT, KEYED ON (company_id, doc_kind), which is the table's unique
// constraint. The row's id is preserved across edits: the INSERT supplies a
// fresh v7 uuid, and ON CONFLICT ... DO UPDATE leaves the existing one alone. A
// caller therefore cannot accidentally accumulate two template rows for one
// kind — and if it could, "which template printed this document?" would have two
// answers, which is precisely the ambiguity migration 0037's UNIQUE exists to
// prevent.
//
// created_at is left to its column default and updated_at is set to now() on
// both paths, so "when did our invoice layout last change?" is answerable from
// the row itself.
func (s *Store) SaveOverrides(ctx context.Context, companyID uuid.UUID, kind printing.DocKind, o printing.TemplateOverrides) error {
	if err := checkKind(kind); err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("printingdb: new template id: %w", err)
	}
	return s.inTenantTx(ctx, companyID, false, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO print_template
			     (id, company_id, doc_kind, title, declaration, terms, signature_label,
			      header_lines, footer_lines, bank_lines, show_bank_details)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			 ON CONFLICT (company_id, doc_kind) DO UPDATE SET
			     title             = EXCLUDED.title,
			     declaration       = EXCLUDED.declaration,
			     terms             = EXCLUDED.terms,
			     signature_label   = EXCLUDED.signature_label,
			     header_lines      = EXCLUDED.header_lines,
			     footer_lines      = EXCLUDED.footer_lines,
			     bank_lines        = EXCLUDED.bank_lines,
			     show_bank_details = EXCLUDED.show_bank_details,
			     updated_at        = now()`,
			id, companyID, string(kind),
			o.Title, o.Declaration, o.Terms, o.SignatureLabel,
			o.HeaderLines, o.FooterLines, o.BankLines, o.ShowBankDetails)
		if err != nil {
			return fmt.Errorf("printingdb: save overrides for %s: %w", kind, err)
		}
		return nil
	})
}
