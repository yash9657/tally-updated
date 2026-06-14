// Package authz is the server-side Policy Decision Point (PDP). It replicates
// Tally's Owner / Data-Entry / custom security levels with Disallow/Allow rules,
// evaluated in Go (OPA/Rego is a documented future swap — Evaluate is the
// equivalent, hermetically testable today).
//
// The PDP is enforced on EVERY request after authn + membership: given the
// caller's security Level and its Rules for the {companyID}, plus the requested
// (Action, Resource), Evaluate returns an allow/deny Decision. Precedence is
// pinned and tested non-vacuously (see authz_test.go):
//
//  1. service principal (handled by the middleware via Claims.IsService) -> full
//     allow scoped to the targeted company (back-dated cutoff + webauthn do NOT
//     apply to svc principals). EvaluateService encodes this.
//  2. level.IsOwner -> allow, BUT still apply the back-dated cutoff for writes.
//  3. else most-specific rule match wins; DISALLOW beats ALLOW at equal
//     specificity; exact resource+action beats a '*' wildcard. A matching
//     disallow denies; else a matching allow allows; else default_access:
//     full -> allow, none -> deny.
//  4. back-dated cutoff: Action in {Create,Alter,Delete} and cutoff != nil and
//     VoucherDate < cutoff -> deny (403).
//  5. webauthn: level.RequiresWebAuthn and Action is a write and !HasWebAuthn ->
//     deny with the STEPUP_REQUIRED contract (403; the middleware emits the
//     distinguishable step-up body, reserving 401 for missing/invalid tokens).
//
// This file (and store.go) are owned by WS1.
package authz

import (
	"time"

	"github.com/google/uuid"
)

// Action is a Tally operation verb. The canonical wire/DB tokens are lowercase.
type Action string

const (
	Create  Action = "create"
	Alter   Action = "alter"
	Delete  Action = "delete"
	Display Action = "display"
	Print   Action = "print"
	Export  Action = "export"
)

// Resource is the object class an Action targets: a voucher class, or one of the
// coarse resources master/report/import/sync, or the wildcard "*".
type Resource string

// Wildcard is the rule token that matches any action or resource.
const Wildcard = "*"

// Effect is a rule's allow/disallow polarity.
type Effect string

const (
	Allow    Effect = "allow"
	Disallow Effect = "disallow"
)

// IsWrite reports whether an action mutates the ledger (subject to the back-dated
// cutoff and the WebAuthn step-up requirement).
func (a Action) IsWrite() bool {
	switch a {
	case Create, Alter, Delete:
		return true
	default:
		return false
	}
}

// Request is one access-control question the PDP answers.
type Request struct {
	UserID       uuid.UUID
	CompanyID    uuid.UUID
	Action       Action
	Resource     Resource
	ResourceName string     // e.g. a report name, when Resource == "report"
	VoucherDate  *time.Time // for the back-dated cutoff on writes; nil if unknown
	HasWebAuthn  bool       // from the token AMR
}

// Decision is the PDP verdict.
type Decision struct {
	Allow      bool
	Reason     string
	HTTPStatus int // 403 on an ordinary deny; 0 when Allow
}

// StepUpRequired is the canonical reason string for a WebAuthn step-up denial.
// The middleware keys its distinguishable 403 STEPUP_REQUIRED response off it.
const StepUpReason = "webauthn step-up required"

// Level mirrors a security_level row.
type Level struct {
	ID               uuid.UUID
	Name             string
	IsOwner          bool
	DefaultAccess    string // "full" or "none"
	RequiresWebAuthn bool
	BackdatedCutoff  *time.Time // nil if no cutoff
}

// Rule mirrors an acl_rule row.
type Rule struct {
	Effect   Effect
	Action   Action   // may be the wildcard "*"
	Resource Resource // may be the wildcard "*" (and "report" can carry a :NAME via ResourceName matching)
}

// allow / deny constructors.
func allow(reason string) Decision { return Decision{Allow: true, Reason: reason} }
func deny(reason string) Decision  { return Decision{Allow: false, Reason: reason, HTTPStatus: 403} }

// stepUp is the distinguishable WebAuthn step-up denial (still 403; the
// middleware adds the STEPUP_REQUIRED code/body so a client can tell it apart
// from a missing-token 401).
func stepUp() Decision {
	return Decision{Allow: false, Reason: StepUpReason, HTTPStatus: 403}
}

// EvaluateService is the synthetic full-access decision for a service principal
// (subject prefix "svc:"). It is scoped to the targeted company by the caller
// (the middleware sets the company GUC). The back-dated cutoff and WebAuthn
// step-up do NOT apply to service principals.
func EvaluateService() Decision { return allow("service principal: full access") }

// Evaluate applies the pinned precedence to a (level, rules, request) triple.
func Evaluate(level Level, rules []Rule, req Request) Decision {
	// (2) Owner: allow, but still enforce the back-dated cutoff on writes.
	if level.IsOwner {
		if d, blocked := cutoffDeny(level, req); blocked {
			return d
		}
		if d, blocked := webauthnDeny(level, req); blocked {
			return d
		}
		return allow("owner: full access")
	}

	// (3) Most-specific rule match. Specificity: exact action+resource (2) >
	// one wildcard (1) > both wildcard (0). DISALLOW beats ALLOW at equal
	// specificity, so a tie resolves to deny.
	bestSpec := -1
	var bestEffect Effect
	matched := false
	for _, r := range rules {
		spec, ok := ruleSpecificity(r, req)
		if !ok {
			continue
		}
		if spec > bestSpec {
			bestSpec = spec
			bestEffect = r.Effect
			matched = true
		} else if spec == bestSpec && r.Effect == Disallow {
			// disallow beats allow at equal specificity
			bestEffect = Disallow
		}
	}

	if matched {
		if bestEffect == Disallow {
			return deny("disallowed by rule")
		}
		// allow: still subject to cutoff + webauthn on writes.
		if d, blocked := cutoffDeny(level, req); blocked {
			return d
		}
		if d, blocked := webauthnDeny(level, req); blocked {
			return d
		}
		return allow("allowed by rule")
	}

	// (3 cont.) No rule matched -> fall back to default_access.
	if level.DefaultAccess == "full" {
		if d, blocked := cutoffDeny(level, req); blocked {
			return d
		}
		if d, blocked := webauthnDeny(level, req); blocked {
			return d
		}
		return allow("default access: full")
	}
	return deny("default access: none")
}

// ruleSpecificity returns how specifically a rule matches the request and
// whether it matches at all. A non-matching rule returns ok=false.
//
// resource matching supports a "report:NAME" form: a rule resource of
// "report:trial-balance" matches a request with Resource=="report" and
// ResourceName=="trial-balance" exactly (and counts as a more specific match
// than a bare "report").
func ruleSpecificity(r Rule, req Request) (int, bool) {
	actExact, actMatch := matchAction(r.Action, req.Action)
	if !actMatch {
		return 0, false
	}
	resSpec, resMatch := matchResource(r.Resource, req)
	if !resMatch {
		return 0, false
	}
	spec := 0
	if actExact {
		spec++
	}
	spec += resSpec // 0 wildcard, 1 exact resource, 2 exact resource:name
	return spec, true
}

func matchAction(ruleAct, reqAct Action) (exact bool, match bool) {
	if string(ruleAct) == Wildcard {
		return false, true
	}
	if ruleAct == reqAct {
		return true, true
	}
	return false, false
}

// matchResource returns a specificity weight (0 wildcard, 1 exact resource, 2
// exact resource:name) and whether it matches.
func matchResource(ruleRes Resource, req Request) (int, bool) {
	rs := string(ruleRes)
	if rs == Wildcard {
		return 0, true
	}
	// "report:NAME" form: match only when the request names that report exactly.
	if i := indexColon(rs); i >= 0 {
		base := rs[:i]
		name := rs[i+1:]
		if Resource(base) == req.Resource && name == req.ResourceName {
			return 2, true
		}
		return 0, false
	}
	if Resource(rs) == req.Resource {
		return 1, true
	}
	return 0, false
}

func indexColon(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}

// cutoffDeny applies the back-dated cutoff: a write whose voucher date is BEFORE
// the level's cutoff is denied. Returns (decision, true) when blocked.
func cutoffDeny(level Level, req Request) (Decision, bool) {
	if level.BackdatedCutoff == nil || !req.Action.IsWrite() || req.VoucherDate == nil {
		return Decision{}, false
	}
	if req.VoucherDate.Before(*level.BackdatedCutoff) {
		return deny("voucher date is before the back-dated cutoff"), true
	}
	return Decision{}, false
}

// webauthnDeny applies the WebAuthn step-up requirement: a write under a level
// that requires WebAuthn, by a caller without a WebAuthn AMR, is denied with the
// STEPUP_REQUIRED contract. Returns (decision, true) when blocked.
func webauthnDeny(level Level, req Request) (Decision, bool) {
	if level.RequiresWebAuthn && req.Action.IsWrite() && !req.HasWebAuthn {
		return stepUp(), true
	}
	return Decision{}, false
}
