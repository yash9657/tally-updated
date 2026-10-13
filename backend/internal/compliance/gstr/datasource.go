package gstr

import "errors"

// datasource.go — TELLING "THE BOOKS CANNOT ANSWER THIS" APART FROM "THE BOOKS
// COULD NOT BE READ" (D117).
//
// ⛔⛔ THE DEFECT THIS FILE CLOSES. Every failure out of this package reached the
// filer as a 422 carrying whatever sentence it happened to have, because both
// handlers ended in `writeError(w, http.StatusUnprocessableEntity, err.Error())`
// as their DEFAULT arm. A 422 means "the request was well-formed but the books
// cannot answer it" — a statement about the FILER'S DATA. A failed query is a
// statement about the SERVER, and the two send an operator to completely
// different places: one goes and looks at their vouchers, the other reports a
// fault. So a dead database asked a filer to go and fix their books, in a
// sentence reading `gstr: query 3b versions: ... (SQLSTATE 42703)`.
//
// ⚠️ THE WRAPS KEEP THEIR PREFIX AND THEIR WORDING. `errors_operator_facing_test.go`
// says exactly why: "they are not refusals; they are diagnostics for whoever
// reads a log, and the package name is the useful part of them". Nothing about
// those sentences changes here. What changes is WHO IS SHOWN THEM — the log,
// not the filer — and that needs the two classes to be distinguishable, which
// they were not.
//
// ⛔ IT MARKS RATHER THAN REWRITES. `dataSource` wraps an error in a value whose
// Error() is the SAME STRING, so a diagnostic in a log is byte-for-byte what it
// was before this file existed and no test that reads a message had to change.
// The only thing added is an answer to `errors.Is(err, ErrDataSource)`.
//
// ⛔⛔ AND IT IS APPLIED AT THE FOUR TRANSACTION BOUNDARIES, NOT AT EACH `%w`.
// Every `s.runner.RunInTx(...)` closure in this package contains queries and
// scans and NOTHING ELSE, so marking the transaction's own result catches every
// plumbing failure inside it — including the two that carry no `gstr: ` prefix
// at all and would have been missed by marking the wrap sites: a `RunInTx` that
// fails to BEGIN (the actual dead-database case) and a bare `return rows.Err()`
// from a connection that died mid-iteration.

// ErrDataSource marks a failure of the PLUMBING under this package — the
// database was unreachable, a query was refused, a scan failed — as opposed to a
// refusal about the books or about the request.
//
// It is a sentinel to test with (`errors.Is`), not a sentence to show: its own
// message is deliberately short and operator-shaped, because the one place it
// could surface is a log line that already carries the real diagnostic.
var ErrDataSource = errors.New("the books could not be read")

// dataSourceError marks an error without altering it.
type dataSourceError struct{ err error }

// Error is the WRAPPED error's own string, unchanged. See the file header.
func (e dataSourceError) Error() string { return e.err.Error() }

func (e dataSourceError) Unwrap() error { return e.err }

// Is answers only for the sentinel; everything else defers to the chain below.
func (e dataSourceError) Is(target error) bool { return target == ErrDataSource }

// dataSource marks err as a plumbing failure. nil in, nil out, so it can wrap a
// transaction's result at the call site without a preceding nil check changing
// shape.
func dataSource(err error) error {
	if err == nil {
		return nil
	}
	return dataSourceError{err: err}
}

// IsDataSource reports whether err is (or wraps) a failure to READ the books,
// rather than a refusal about them.
//
// ⛔ IT IS THE STATUS DECISION, AND CALLERS ARE EXPECTED TO MAKE IT. An HTTP
// handler that answers 422 for everything this package returns tells a filer
// their data is at fault when the server is; one that answers 500 for
// everything tells them a genuine refusal — a negative table 4B, a disclosure
// outside the closed vocabulary — is somebody else's problem to fix, when it is
// theirs and the message says how.
func IsDataSource(err error) bool { return errors.Is(err, ErrDataSource) }
