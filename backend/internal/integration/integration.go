// Package integration will own the Tally-interoperability gateway (XML/ODBC
// import-export keyed on external_id) and third-party connector mapping
// (PLAN.md B.7). Every imported object maps 1:1 to a master/voucher and posts
// through the same idempotent path a human uses. Phase 0 establishes the module
// boundary only.
package integration

// Placeholder marks the integration module boundary for the Phase 0 walking
// skeleton. Real import/export types arrive in later phases.
type Placeholder struct{}
