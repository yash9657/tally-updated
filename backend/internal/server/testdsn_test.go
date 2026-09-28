package server

import "os"

// testEnvDSN lets ADMIN_DATABASE_URL / DATABASE_URL point this package's
// database tests at a database other than the shared dev one. The Makefile
// exports both with the same defaults these constants carried, so an ordinary
// `make` run is unchanged; a parallel lane working on its own database (W2 ran
// on tally_w2) sets them and never migrates or writes the shared `tally`.
func testEnvDSN(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
