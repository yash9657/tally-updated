// Package actions holds the ActionSpec registry: one row per (method, route)
// pair the HTTP surface mounts under a company, carrying the ACL key the PDP
// will actually evaluate for it.
//
// It is W0's third deliverable (PLAN-AGENTIC §3 W0). Everything the agent layer
// is ever allowed to do has to arrive here first, so this package is the single
// place a reviewer can look to answer "what can be driven, and under whose
// authority".
//
// ===========================================================================
// THE ONE INVARIANT: THE ACL KEY *IS* THE ARM
// ===========================================================================
// Every field on an ActionSpec is DERIVED by calling the real router and the
// real routeAction() (see internal/server/action_registry_golden_test.go).
// Nothing here is typed by hand. That is not tidiness — it is the whole safety
// argument of the agent layer, stated in PLAN-AGENTIC §2.1: "the agent never
// gains a write path the human does not have."
//
// The way that promise gets broken is banal and has one shape. Someone writes a
// second table of what the agent may do, keyed on something adjacent to the ACL
// key — a screen name, a tool name, a hand-copied string — and from that moment
// the tenant's `disallow` rules and the agent's permission checks are two
// vocabularies that agree only by luck. An agent tool named `budget_variance`
// and a rule reading `disallow display report:budget-variance` look like they
// are about the same thing, and nothing in the type system says whether they
// are. Deriving the key by CALLING routeAction on the route the agent will
// actually POST to removes the second vocabulary entirely: there is only ever
// the arm, and this package is a cache of what the arm said.
//
// This is the same argument internal/server/route_vocabulary_test.go makes for
// the router and internal/authz/vocabulary.go makes for the rule vocabulary. It
// is the third leg of the same table.
//
// ===========================================================================
// WHY THIS PACKAGE IMPORTS internal/authz, AND ONLY internal/authz
// ===========================================================================
// ActionSpec.Action is authz.Action and ActionSpec.Resource is authz.Resource —
// the PDP's own types, not strings.
//
// ⛔ DO NOT "SIMPLIFY" THESE TO string. The proposal comes up because the
// registry is generated and a string round-trips through JSON with less
// ceremony. What the named types buy is that a spec CANNOT NAME AN ACTION
// OUTSIDE THE CLOSED VOCABULARY without the compiler or ValidateRule saying so,
// which turns "the ACL key equals the arm" from a discipline into a property.
// internal/authz/vocabulary.go:24 is the monument to what the string version
// costs: the token `export` was legal to type into an ACL rule, was reported
// back to the owner as saved, and governed nothing. "A security setting that
// reports success and does nothing is worse than a missing one: the owner stops
// looking." A registry of stringly-typed action names is that defect with a
// larger blast radius, because the agent layer reads it to decide what it may
// do unattended.
//
// The import is for the TYPES and the validators. This package holds no policy,
// evaluates nothing, and reads no database.
//
// ===========================================================================
// ⛔ THIS PACKAGE MUST NEVER IMPORT internal/server
// ===========================================================================
// internal/server is the composition root; every architectural edge in this
// tree points INWARD, and internal/server is where they all terminate. An edge
// from actions back to server would be a cycle in the design even before it
// became one in the compiler.
//
// It is also why the GENERATOR lives in package server rather than here:
// routeAction is unexported ON PURPOSE (it is enforcement wiring, not API), so
// the only place it can be called is beside it. internal/server's
// TestActionRegistryGolden walks the real router, calls the real routeAction,
// and writes registry_gen.go into this directory. This package holds the type
// and the generated data and nothing else — which is exactly why it can be
// imported by internal/agent, internal/policy and the MCP server later without
// dragging the HTTP surface in behind it.
//
// ===========================================================================
// WHAT IS DELIBERATELY ABSENT IN W0
// ===========================================================================
// There is no RiskClass, no AutonomyDefault and no RequiresApproval field, and
// their absence is the same lesson as `export` above, applied prospectively.
// Nothing reads them until W2's internal/policy exists. A `RiskClass: "high"`
// that no evaluator consults is a field a reviewer will read as a control, and
// it is not one. They land in the commit that adds the code that reads them.
package actions
