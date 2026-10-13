// Package backup serialises one company's books to a portable archive, and
// restores an archive into a company that does not yet exist.
//
// # THE ARCHIVE
//
// Gzipped JSON Lines: one JSON object per line, a header first, then one record
// per row, then a trailer. Not pg_dump — the application role cannot run it, and
// a dump is not tenant-scoped, so it would either fail or hand the operator
// every other company in the database.
//
// Each row is carried as the EXACT jsonb Postgres itself produces for it
// (to_jsonb(t)) and is restored with jsonb_populate_record, which is what makes
// the round trip type-exact for bytea hashes, numerics with scale, timestamps,
// arrays and nested jsonb payloads alike without one line of per-type binding
// code. Verified against the live schema: for every row in all 61 scoped tables
// of the demo company, to_jsonb(jsonb_populate_record(NULL::t, to_jsonb(x))) IS
// NOT DISTINCT FROM to_jsonb(x).
//
// ⛔ THE ROW PAYLOAD IS NEVER PARSED IN GO. It is carried as json.RawMessage from
// the SELECT to the file and from the file to the INSERT. Decoding it into `any`
// would route every numeric through float64 and silently round a quantity or an
// amount — the archive would still restore, every test would stay green, and the
// books would differ in the last places.
//
// # WHICH TABLES
//
// Whatever internal/platform/tenant's census says, and nothing else. There is no
// list of tables in this package, deliberately: a second list is the defect. See
// the tenant package comment.
//
// # THE TRAILER IS THE ORACLE FOR A SHORT FILE
//
// A download that is cut off mid-stream is still valid gzip up to the cut and
// still parses as JSON Lines. Without a trailer, a restore of a truncated
// archive succeeds and produces a company missing however much did not arrive.
// The trailer carries a per-table row count, and the restore refuses an archive
// whose counts it cannot match — a restore that reads a short file must fail
// loudly, not restore 80% of a company.
package backup

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// FormatName and FormatVersion identify the archive. The version is bumped only
// when a reader that predates the change would MISREAD an archive, not when a
// field is added — the reader ignores unknown fields.
const (
	FormatName    = "tally-company-archive"
	FormatVersion = 1
)

// Record kinds, one per line of the archive.
const (
	KindHeader  = "header"
	KindRow     = "row"
	KindTrailer = "trailer"
)

// Header is the first line of every archive.
type Header struct {
	Kind    string `json:"kind"`
	Format  string `json:"format"`
	Version int    `json:"format_version"`

	CreatedAt time.Time `json:"created_at"`

	// SourceCompanyID is the company the archive was taken from, and — because
	// primary keys are PRESERVED (see restore.go) — also the id the company will
	// have when restored. It is what a restore checks for collision.
	SourceCompanyID uuid.UUID `json:"source_company_id"`
	SourceName      string    `json:"source_company_name"`

	// SchemaVersion is the highest applied migration at the time of the backup.
	// A restore compares it with its own and refuses to read an archive from the
	// FUTURE: jsonb_populate_record silently ignores keys with no matching
	// column, so restoring a newer archive into an older schema would drop
	// whole columns without erroring.
	SchemaVersion string `json:"schema_version"`

	// Tables is the census order the rows follow: parents before children, the
	// company row first. A reader does not depend on it — every row record names
	// its own table — but it makes the archive self-describing, and it is what a
	// human diffing two archives reads first.
	Tables []string `json:"tables"`

	// Contents states, IN THE ARCHIVE, what this archive does and does not carry
	// beyond the raw tables. An archive that silently drops a plane is the census
	// defect wearing a different hat, so the decisions are written down where the
	// person restoring can read them rather than left implicit in the code that
	// wrote the file. See D3/D4/D5 in HANDOFF-T14-BACKUP-RESTORE.md.
	Contents Contents `json:"contents"`
}

// Contents is the archive's own statement of what it carries and what a restore
// will refuse to act on. It is IN THE FILE, not only in this code, because an
// archive that silently drops a plane — or silently recreates access rights — is
// the census defect wearing a different hat, and the person restoring is the one
// who needs to know. See D3/D4/D5 in HANDOFF-T14-BACKUP-RESTORE.md.
type Contents struct {
	// InsightdDrafts: carried. insightd_draft is tenant data with a TEXT
	// company_id, written by the Python insightd service — a company's unposted
	// supplier bills. Leaving them out would restore a company whose bill inbox
	// had silently emptied. The census finds it BECAUSE the enumeration does not
	// filter on data_type='uuid'; see the warning in tenant.ScopedTables.
	InsightdDrafts bool `json:"insightd_drafts"`

	// StatutorySteps: carried. statutory_step is the IRN workflow log; without it
	// a restored company can re-attempt an IRN that was already raised against
	// the portal. Its tenancy lives inside its TEXT primary key, which makes it
	// the one row shape whose VALUE a restore has to validate.
	StatutorySteps bool `json:"statutory_steps"`

	// NotRestored names the tables that ARE IN THIS FILE but that a restore will
	// deliberately not recreate, so the archive stays a complete record of the
	// company while the restore stays safe. Today that is company_membership
	// alone, and the reason is a security decision rather than a technical one:
	// it is the only foreign key from tenant data to app_user, so the archive
	// names users who need not exist in the target database — and silently
	// recreating access rights from a file somebody handed you is a
	// privilege-escalation vector. The restore seeds the RESTORING user as Owner
	// and reports what it did not recreate.
	//
	// ⚠️ THE ARCHIVE STILL CARRIES THEM, and that is deliberate. Dropping rows at
	// BACKUP time would make the file an incomplete record of what the company
	// was, and would put a hand-maintained exclusion in the one place this track
	// is built to keep list-free. The refusal belongs at restore, where the
	// privilege is actually granted.
	NotRestored []string `json:"not_restored"`

	// MembershipNames is who could reach the SOURCE company, carried so a restore
	// can report by name what it did not re-grant. It is documentation for a
	// human; nothing acts on it.
	MembershipNames []string `json:"membership_names,omitempty"`
}

// Row is one archived database row.
type Row struct {
	Kind  string `json:"kind"`
	Table string `json:"table"`
	// Data is the row exactly as Postgres rendered it with to_jsonb, carried
	// unparsed. See the package comment on why it is never decoded.
	Data json.RawMessage `json:"data"`
}

// Trailer is the last line of every archive and the oracle for a short file.
type Trailer struct {
	Kind string `json:"kind"`
	// Counts is per-table, INCLUDING zeros for tables the census covers that this
	// company had no rows in. The zeros are what let a restore distinguish "this
	// company genuinely has no price lists" from "the price lists did not arrive".
	Counts map[string]int64 `json:"counts"`
	Rows   int64            `json:"row_count"`
}

// envelope is the minimal shape every record shares, used to dispatch on kind
// without decoding a record twice.
type envelope struct {
	Kind string `json:"kind"`
}
