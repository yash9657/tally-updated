package backup

import "sort"

// notRestored maps a table that IS ARCHIVED but that a restore must NOT
// recreate, to the reason why.
//
// It is a registry of DELIBERATE OMISSIONS in the same shape as
// tenant.NotTenantScoped, and for the same reason: "no rule matched this table"
// and "we decided not to restore this table" look identical from the outside,
// and only one of them is a bug. TestEveryNotRestoredTableIsInTheCensus keeps it
// from naming a table that no longer exists, which would silently narrow the
// round-trip oracle's exception set.
//
// ⛔ IT IS A RESTORE-SIDE REGISTRY, NOT A BACKUP-SIDE ONE. Nothing here is
// dropped from the archive: the file stays a complete record of what the company
// was, and the refusal happens where the privilege would actually be granted.
var notRestored = map[string]string{
	"company_membership": "the only foreign key from tenant data to app_user: the archive names " +
		"users who need not exist in this database, and silently recreating access rights from a " +
		"file somebody handed you is a privilege-escalation vector. The restore seeds the RESTORING " +
		"user as Owner against the archive's own Owner level, and reports the rest by name.",
}

// NotRestoredTables lists the registry's tables, sorted so the archive header is
// byte-stable.
func NotRestoredTables() []string {
	out := make([]string, 0, len(notRestored))
	for t := range notRestored {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// IsNotRestored reports whether a table is deliberately skipped by restore.
func IsNotRestored(table string) bool {
	_, ok := notRestored[table]
	return ok
}
