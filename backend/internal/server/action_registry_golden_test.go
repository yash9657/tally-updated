package server

// action_registry_golden_test.go is THE GENERATOR for internal/actions.
//
// It walks the REAL chi router, calls the REAL routeAction() on a synthesised
// request per mounted route, and writes what it observed to two checked-in
// artefacts:
//
//	../actions/registry_gen.go                        the Go registry
//	../../../frontend/src/api/goldens/action-registry.json  the same rows as JSON
//
// ===========================================================================
// ⛔ WHY THIS IS A ROUTER WALK AND NOT A PARSER OVER routeAction
// ===========================================================================
// The tempting design is `go:generate` over routeAction's AST. It is wrong here
// for a reason the file it would parse states in terms at auth_middleware.go:408:
// "THE ROUTER IS THE ORACLE, NOT A SECOND TABLE".
//
// routeAction does not decide a route by matching a pattern. It decides by
// calling seven path predicates — isMasterPath, isDraftPath, isVoucherClass,
// isPriceListPath, isMembersPath, isEmployeeChildPath, reportName — over the
// split suffix. A generator that read the switch would have to REIMPLEMENT all
// seven to know which arm a given route falls into, i.e. it would be a second
// implementation of route identity, sitting beside a control that has been
// driven and broken six times (auth_middleware.go:1128 counts them). The second
// copy is the thing that goes stale, and the failure would be silent in the
// worst direction: the registry would say a route is governed by one key while
// the PDP applied another, and the agent layer reads the registry.
//
// So: no parse, no data table, no reimplementation. Build the router, ask it
// what it mounts, ask routeAction what each one is. The cost is that this must
// run as a test in package server (routeAction is unexported, deliberately) —
// which is also why UPDATE_GOLDENS gates the write, matching the five existing
// goldens under frontend/src/api/goldens/ and internal/printing's.
//
// ⛔ AND IT NEEDS NO DATABASE. `srv := &Server{}; srv.routes()` builds the whole
// router with a nil pool (route_vocabulary_test.go:81 has relied on this since
// it was written), every derivation below calls a pure function, and the two
// artefacts are files. internal/compliance/tax_sections_golden_test.go:16 is the
// standing rule this obeys: "a golden guarded by a connection is a golden that
// silently stops being checked when the connection is down (a t.Skip exits 0)".

import (
	"context"
	"encoding/json"
	"fmt"
	"go/format"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/authz"
)

// The three artefact paths, each as ONE named constant so the cross-tree
// coupling is visible in the source rather than buried in a call.
const (
	// actionRegistryGenPath is Go source written into a SIBLING package. That is
	// unusual and it is the direction that keeps the import graph acyclic:
	// internal/actions must never import internal/server (see actions/doc.go),
	// so the generator has to live here and push, rather than there and pull.
	actionRegistryGenPath = "../actions/registry_gen.go"

	// actionRegistryJSONPath reaches out of the Go tree into the frontend's,
	// because the artefact's whole purpose is to be consumed there — the
	// frontend asserts the CONVERSE direction (every UI command lands on a
	// registered action). Same convention as templateSurfaceGoldenPath.
	actionRegistryJSONPath = "../../../frontend/src/api/goldens/action-registry.json"

	// uiCommandsManifestPath is the file the OTHER half of W0 generates: the
	// frontend's census of keystrokes, screen actions and the client calls each
	// one makes. It is an INPUT here.
	//
	// ⚠️ IT IS ALLOWED TO BE ABSENT, AND THE ABSENCE IS RECORDED RATHER THAN
	// SWALLOWED. The two halves of W0 land in different commits; a generator
	// that refused to run without the frontend's file would mean neither half
	// could be built first. So a missing manifest produces a registry whose
	// Source.Degradation says so in prose, and internal/actions' own gate
	// FAILS on that flag — the loud refusal happens where the coverage claim is
	// made, not where the file is read. What it must never do is quietly mark
	// every action ChannelNone and pass, which is house rule 3's silent default.
	uiCommandsManifestPath = "../../../frontend/src/api/goldens/ui-commands.json"
)

// companyRoutePrefix is the subtree routeAction governs. Routes above it
// (/v1/login, /v1/companies itself, /healthz) are authenticated but not
// company-scoped, so they carry no ACL key and are not agent-drivable.
const companyRoutePrefix = "/v1/companies/{companyID}"

// ---------------------------------------------------------------------------
// The probe body
// ---------------------------------------------------------------------------

// ⛔ THERE IS NO CUTOFF PROBE BODY HERE ANY MORE, AND RE-ADDING ONE WOULD
// RE-CREATE THE COLUMN IT WAS DELETED WITH. This generator used to send every
// route `{"voucher_date":..,"wage_month":..,"deposit_date":..,"bank_date":..}`
// and record whether a date reached the PDP, as ActionSpec.CutoffJudged.
//
// The probe made the answer a foregone conclusion. voucherDateFromRequest
// dispatches on route identity for three shapes (a pay run reads `wage_month`,
// a TDS challan `deposit_date`, a bank-reconciliation PUT `bank_date`) and falls
// through to the route-agnostic peekVoucherDate walk for everything else — so a
// body carrying all four spellings answers TRUE for every write, and the column
// was IsWrite under a second name on 230 of 230 rows. Worse, it answered true
// for `DELETE .../members/{userID}` and `DELETE /books-closure`, whose real
// requests have no body at all: the recorded value described the probe, not the
// product, and W2 reads these columns as controls.
//
// What survives is what can be measured from a request the product actually
// sends: DateSubject, below, which cutoffDateSubject derives from the METHOD AND
// PATH ALONE (bankReconLeg and challanAllocationTarget both ignore the body), so
// the four routes whose refusal names something other than a voucher date are
// still recorded, and recorded truthfully. See actions.ActionSpec for the field
// that is no longer there and why.

// synthRequest builds the request the derivations below are run against: the
// concrete path for a chi pattern, the RouteContext chi would have populated,
// and the probe body.
func synthRequest(method, route, companyID, body string) *http.Request {
	path, rctx := synthPath(route, companyID)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// ---------------------------------------------------------------------------
// The derivations — every one of them an ORACLE CALL
// ---------------------------------------------------------------------------

// stepUpEligible asks the PDP itself whether a WebAuthn step-up can stand
// between a caller and this action.
//
// ⛔ IT DOES NOT RE-STATE webauthnDeny's CONDITION. Writing
// `return act.IsWrite()` here would be true today and would be a second copy of
// a security rule — the exact defect doc.go describes. The day step-up grows a
// second trigger (an amount band, a resource class), a re-stated condition goes
// quietly wrong and this call does not. The level is synthetic and maximal:
// owner, WebAuthn required, caller without the AMR; if the PDP refuses with
// StepUpReason under those conditions, step-up is reachable for this action.
func stepUpEligible(act authz.Action, res authz.Resource, resName string) bool {
	d := authz.Evaluate(
		authz.Level{IsOwner: true, DefaultAccess: "full", RequiresWebAuthn: true},
		nil,
		authz.Request{Action: act, Resource: res, ResourceName: resName, HasWebAuthn: false},
	)
	return !d.Allow && d.Reason == authz.StepUpReason
}

// specID derives the stable id. See actions.ActionSpec.ID for why parameter
// segments are kept rather than dropped.
func specID(method, route string) string {
	rest := strings.TrimPrefix(route, companyRoutePrefix)
	segs := strings.Split(strings.Trim(rest, "/"), "/")
	parts := []string{strings.ToLower(method)}
	for _, s := range segs {
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			parts = append(parts, "_"+strings.ToLower(strings.Trim(s, "{}")))
			continue
		}
		parts = append(parts, s)
	}
	if len(parts) == 1 {
		// The company document itself: GET/PUT /v1/companies/{companyID}.
		parts = append(parts, "_self")
	}
	return strings.Join(parts, ".")
}

// specLabel renders the row a reviewer reads in a diff: the ACL key and the
// (method, route) it came from.
//
// ⚠️ IT IS NOT A DESCRIPTION AND THE FIELD IT FILLS IS NO LONGER CALLED ONE. It
// is a mechanical join of three columns that are already in the row, so it adds
// no word a retrieval index could use; see actions.ActionSpec.Label. It is
// derived rather than written, which is what keeps it from describing an action
// the router does not mount.
func specLabel(act authz.Action, res authz.Resource, resName, method, route string) string {
	var subject string
	switch {
	case resName != "":
		subject = resName + " report"
	case isVoucherClass(string(res)):
		subject = string(res) + " voucher"
	default:
		subject = string(res)
	}
	return fmt.Sprintf("%s %s (%s %s)", act, subject, method, route)
}

// ---------------------------------------------------------------------------
// The frontend manifest
// ---------------------------------------------------------------------------

// uiManifest is the slice of the frontend's ui-commands.json this generator
// reads. It is deliberately a SUBSET: that file also carries screen kinds, route
// keys, keymap counts and a client-route inventory, none of which this generator
// has any business interpreting. Decoding only `commands[].acl` means a field
// added there cannot break this generator.
//
// ⚠️ WHAT THE FILE CARRIES TODAY IS NOT YET THIS. As checked in it emits
// commands shaped {id, surface, scope, combo, action, label, screen_action,
// dispatch} and a separate `client_routes` list of bare CLIENT METHOD NAMES
// ("addBudgetLine", "allocateToTDSChallan", ...). None of that names an HTTP
// (verb, path), so nothing in it can be joined to a route — which the generator
// detects and records as a degradation rather than papering over. What this
// generator needs is one field: each command listing the calls it makes, as
//
//	"acl": [{"verb": "POST", "path": "/v1/companies/${companyId}/vouchers/sales"}]
//
// The path may be written with ${x}, {x} or :x placeholders and may carry a
// query string; concreteClientPath normalises all three.
type uiManifest struct {
	Commands []struct {
		ID     string `json:"id"`
		Screen string `json:"screen"`
		Combo  string `json:"combo"`
		ACL    []struct {
			Verb string `json:"verb"`
			Path string `json:"path"`
		} `json:"acl"`
	} `json:"commands"`

	// ClientCalls is the (verb, path) each method on the shipped REST client
	// actually requests, measured by the frontend driving the real client
	// against a recording transport.
	//
	// ⛔ THIS IS THE JOIN THAT EXISTS TODAY, AND commands[].acl IS THE ONE THAT
	// SHARPENS IT LATER. A client call proves a route is reachable from the
	// running product; it does not say which key presses it. Both are read here
	// and a command's own claim WINS, so the day lane E's other half lands the
	// registry gains keystroke/screen channels without a change to this file.
	//
	// `used` is whether the app's own source outside src/api mentions the
	// method at all. A method nothing calls is not a human path, so those rows
	// are deliberately NOT counted as covered — see actions.ChannelClient.
	ClientCalls []struct {
		Method string `json:"method"`
		Verb   string `json:"verb"`
		Path   string `json:"path"`
		Used   bool   `json:"used"`
	} `json:"client_calls"`
}

// clientPathParam matches the placeholder spellings a TypeScript client path can
// carry once lane E has extracted it from source: `${companyId}` from a template
// literal, `{companyID}` from a documented pattern, `:id` from a route table.
// Every one of them is replaced by a value of the right SHAPE, exactly as
// synthPath does for the Go side.
func concreteClientPath(p, companyID string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		switch {
		case strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}"):
			segs[i] = clientParamValue(strings.TrimSuffix(strings.TrimPrefix(s, "${"), "}"), companyID)
		case strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}"):
			segs[i] = clientParamValue(strings.Trim(s, "{}"), companyID)
		case strings.HasPrefix(s, ":"):
			segs[i] = clientParamValue(strings.TrimPrefix(s, ":"), companyID)
		}
	}
	return strings.Join(segs, "/")
}

// clientParamValue reuses paramValue's rule — a uuid unless the name says
// otherwise — keyed on the frontend's spelling of the parameter name.
//
// ⚠️ THE `legNo` CASE IS NOT COSMETIC and it is the same trap paramValue
// documents: bankReconLeg calls strconv.Atoi on that segment, so handing it a
// uuid makes chi's Find still match (the pattern is untyped) but makes every
// downstream date derivation read the route as "not one of mine".
func clientParamValue(name, companyID string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "company"):
		return companyID
	case strings.Contains(n, "legno"), strings.Contains(n, "leg_no"):
		return "0"
	default:
		return uuid.NewString()
	}
}

// uiChannel classifies a command by how a human reaches it. A combo is a key or
// chord (F9, Alt+E, Ctrl+A); anything else is reached by a screen control.
func uiChannel(combo string) actions.Channel {
	if strings.TrimSpace(combo) != "" {
		return actions.ChannelKeystroke
	}
	return actions.ChannelScreen
}

// uiHit is one command's (or one client method's) claim on one route.
type uiHit struct {
	channel actions.Channel
	command string
}

// uiJoin is everything the frontend manifest contributed to this build.
type uiJoin struct {
	hits map[string]uiHit
	// present is whether the manifest file existed at all.
	present bool
	// aclClaims / commandsJoined describe the COMMAND half (keystroke-level).
	aclClaims      int
	commandsJoined int
	// clientCalls / clientJoined / clientUnused describe the CLIENT half.
	clientCalls  int
	clientJoined int
	clientUnused int
	// expanded names the client calls that did not resolve to a single route
	// through chi's Find and were matched segment-wise against the walk instead
	// (the print and open-bills families, whose class or report slug is a
	// PARAMETER on the client and a LITERAL in the router).
	expanded []string
	// unresolved is every (verb, path) claim the router does not mount. The
	// caller fails on these.
	unresolved []string
}

// routeMatches reports whether a client path and a mounted chi pattern name the
// same route, treating a `{param}` on EITHER side as matching one segment.
//
// ⛔ IT IS THE FALLBACK, NEVER THE FIRST ANSWER. chi's own Find is asked first,
// with concrete values, so ordinary routes are resolved by the router itself and
// no second implementation of route identity can drift. This exists for one
// shape the router cannot answer: `printReport(companyID, slug)` builds
// `/print/reports/{slug}` because the slug is a PARAMETER on the client, while
// the router mounts thirty-three LITERAL slugs (server.go's loop over
// authz.KnownReportNames). Handing Find a uuid there matches nothing, and the
// honest reading is not "unreachable" but "reachable at all thirty-three" — the
// client method can produce every one of those URLs. Same for the thirteen print
// voucher classes and the eight open-bills classes: 54 of the 230 mounted routes
// are in exactly this family, and dropping them would have made the coverage
// number a fifth wrong.
//
// ⚠️ IT CAN ONLY EVER MATCH ROUTES OF THE SAME SEGMENT COUNT UNDER THE SAME
// LITERAL PREFIX, and every call it resolves is listed in the golden's
// `expanded` so a reviewer sees which claims came from here rather than from the
// router.
func routeMatches(clientPath, pattern string) bool {
	cs := strings.Split(strings.Trim(clientPath, "/"), "/")
	rs := strings.Split(strings.Trim(pattern, "/"), "/")
	if len(cs) != len(rs) {
		return false
	}
	isParam := func(x string) bool { return strings.HasPrefix(x, "{") && strings.HasSuffix(x, "}") }
	for i := range cs {
		if cs[i] == rs[i] || isParam(cs[i]) || isParam(rs[i]) {
			continue
		}
		return false
	}
	return true
}

// loadUICommands joins the frontend manifest onto route ids.
//
// ⛔ THE JOIN KEY IS (verb, path) RESOLVED THROUGH THE ROUTER — NEVER A SCREEN
// NAME. The screen-name shortcut is wrong on at least five screens and every one
// of them is wrong in the direction that matters: budget-variance is
// `report:budget-variance` but tds-challans is the SINGULAR bare token
// `tds-challan`, print-templates is `company` and not a report at all, and the
// five payroll screens are deliberately not `report:` frames. A registry that
// derived the key from a ScreenKind would report those six as governed by rules
// no administrator can write, and would do so while looking entirely plausible.
//
// So each acl entry's path is made concrete and handed to chi's own Find, which
// returns the mounted PATTERN — the same string chi.Walk produced. Two different
// spellings of route identity cannot arise, because there is only the router's.
//
// unresolved collects the (verb, path) pairs the router could not match, and the
// caller fails on them. A command whose call the router does not recognise is
// either a dead client method or a route this registry is blind to; both are
// findings, neither is something to drop on the floor.
func loadUICommands(t *testing.T, router chi.Router, mounted map[string][]string, companyID, manifestPath string) uiJoin {
	t.Helper()
	j := uiJoin{hits: map[string]uiHit{}}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return j
		}
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	j.present = true
	var m uiManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse %s: %v", manifestPath, err)
	}

	// claim records one (channel, source) claim on one route id, resolving
	// collisions deterministically. Without a rule here the registry's bytes
	// would depend on map iteration order and the golden would flap.
	//
	// The precedence is keystroke > screen > client, then the lower source id:
	// a route a KEY reaches is described by that key, not by whichever client
	// method happens to sort first.
	rank := map[actions.Channel]int{
		actions.ChannelKeystroke: 3,
		actions.ChannelScreen:    2,
		actions.ChannelClient:    1,
	}
	claim := func(id string, ch actions.Channel, source string) {
		if prev, ok := j.hits[id]; ok {
			if rank[prev.channel] > rank[ch] {
				return
			}
			if rank[prev.channel] == rank[ch] && prev.command <= source {
				return
			}
		}
		j.hits[id] = uiHit{channel: ch, command: source}
	}

	for _, c := range m.Commands {
		matchedAny := false
		for _, a := range c.ACL {
			j.aclClaims++
			verb := strings.ToUpper(strings.TrimSpace(a.Verb))
			path := concreteClientPath(a.Path, companyID)
			if !strings.HasPrefix(path, "/v1/companies/"+companyID) {
				// A call that is not company-scoped (login, company list) has no
				// ACL key by construction; it is not a miss.
				continue
			}
			rctx := chi.NewRouteContext()
			pattern := router.Find(rctx, verb, path)
			if pattern == "" {
				j.unresolved = append(j.unresolved, fmt.Sprintf("%s (%s %s)", c.ID, verb, a.Path))
				continue
			}
			matchedAny = true
			claim(specID(verb, pattern), uiChannel(c.Combo), c.ID)
		}
		if matchedAny {
			j.commandsJoined++
		}
	}

	for _, c := range m.ClientCalls {
		j.clientCalls++
		verb := strings.ToUpper(strings.TrimSpace(c.Verb))
		path := concreteClientPath(c.Path, companyID)
		if !strings.HasPrefix(path, "/v1/companies/"+companyID) {
			continue
		}
		var patterns []string
		rctx := chi.NewRouteContext()
		if p := router.Find(rctx, verb, path); p != "" {
			patterns = []string{p}
		} else {
			// The router could not resolve it with concrete values; try the
			// segment-wise match against what the walk mounted. See routeMatches.
			for _, p := range mounted[verb] {
				if routeMatches(c.Path, p) {
					patterns = append(patterns, p)
				}
			}
			if len(patterns) > 0 {
				j.expanded = append(j.expanded, fmt.Sprintf("%s (%s %s) -> %d routes", c.Method, verb, c.Path, len(patterns)))
			}
		}
		if len(patterns) == 0 {
			j.unresolved = append(j.unresolved, fmt.Sprintf("client.%s (%s %s)", c.Method, verb, c.Path))
			continue
		}
		j.clientJoined++
		if !c.Used {
			// ⛔ A CALL NOBODY MAKES IS NOT A HUMAN PATH. The method exists on the
			// client and nothing in the app references it, so the route it would
			// reach stays ChannelNone and has to be declared on the actions side
			// with a reason. Counting it here is how "the client CAN call it"
			// would quietly become "an operator DOES".
			j.clientUnused++
			continue
		}
		for _, p := range patterns {
			claim(specID(verb, p), actions.ChannelClient, "client."+c.Method)
		}
	}

	sort.Strings(j.unresolved)
	sort.Strings(j.expanded)
	return j
}

// ---------------------------------------------------------------------------
// The generator
// ---------------------------------------------------------------------------

// registryGolden is the JSON artefact's top-level shape.
type registryGolden struct {
	Note       string               `json:"note"`
	Regenerate string               `json:"regenerate"`
	Source     actions.Provenance   `json:"source"`
	Counts     registryCounts       `json:"counts"`
	Specs      []actions.ActionSpec `json:"specs"`
}

type registryCounts struct {
	Specs         int            `json:"specs"`
	ByAction      map[string]int `json:"by_action"`
	Writes        int            `json:"writes"`
	Reads         int            `json:"reads"`
	WithHumanPath int            `json:"with_human_path"`
	// DatedSubjects is how many rows carry a DateSubject — the count that
	// replaced `cutoff_judged`, which was Writes spelled twice. See the block
	// above synthRequest for why that column is gone.
	DatedSubjects int `json:"dated_subjects"`
	DistinctKeys  int `json:"distinct_acl_keys"`
}

func TestActionRegistryGolden(t *testing.T) {
	srv := &Server{}
	srv.routes() // the real router; no DB needed to enumerate or to resolve routes.

	companyID := uuid.NewString()

	var specs []actions.ActionSpec
	mounted := map[string][]string{}
	walked := 0
	err := chi.Walk(srv.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, companyRoutePrefix) {
			return nil
		}
		walked++
		mounted[method] = append(mounted[method], route)

		req := synthRequest(method, route, companyID, "")
		act, res, resName, mapped := routeAction(req)
		if !mapped {
			// TestEveryCompanyRouteIsMappedAndInVocabulary owns this failure and
			// says it better. Refusing to WRITE a registry row is the part that
			// belongs here: a default-denied route in the registry would be an
			// action the agent layer believes it can drive and the PDP refuses.
			t.Errorf("route %s %s is NOT mapped by routeAction; it cannot become an ActionSpec "+
				"(see TestEveryCompanyRouteIsMappedAndInVocabulary)", method, route)
			return nil
		}

		specs = append(specs, actions.ActionSpec{
			ID:             specID(method, route),
			Method:         method,
			Route:          route,
			Action:         act,
			Resource:       res,
			ResourceName:   resName,
			ACLKey:         actions.RenderACLKey(act, res, resName),
			IsWrite:        act.IsWrite(),
			StepUpEligible: stepUpEligible(act, res, resName),
			DateSubject:    cutoffDateSubject(synthRequest(method, route, companyID, "")),
			Channel:        actions.ChannelNone,
			Label:          specLabel(act, res, resName, method, route),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}

	// -----------------------------------------------------------------------
	// INVARIANTS BEFORE WRITING. The printing golden's rule: an artefact that is
	// wrong must never reach the disk, because the next run compares against it
	// and reports "no change".
	// -----------------------------------------------------------------------
	if walked == 0 {
		t.Fatalf("the router walk found NO routes under %s. Either routes() changed shape or "+
			"the prefix did; a registry generated from this walk would be empty and the "+
			"coverage gate would then be measuring nothing", companyRoutePrefix)
	}
	// The hand-derivation in the W0 brief counted 230 mounted (method, route)
	// pairs. The floor is set well below it so ordinary route churn does not trip
	// this, and well above zero so a broken walk does.
	if walked < 150 {
		t.Fatalf("the router walk found only %d routes under %s, want >= 150 (230 were mounted "+
			"when this was written)", walked, companyRoutePrefix)
	}

	sort.Slice(specs, func(i, j int) bool { return specs[i].ID < specs[j].ID })

	seenID := map[string]string{}
	for _, s := range specs {
		if prev, dup := seenID[s.ID]; dup {
			t.Fatalf("duplicate ActionSpec id %q: %s and %s %s both derive it. "+
				"ByID would return whichever came first, so an agent resolving that id would "+
				"drive one of two different routes at random — fix specID, do not dedupe",
				s.ID, prev, s.Method, s.Route)
		}
		seenID[s.ID] = s.Method + " " + s.Route

		// The join point, asserted at GENERATION time as well as in the gate: a
		// row whose key no administrator could write into a rule must never be
		// checked in.
		if err := authz.ValidateRule(authz.Rule{Effect: authz.Allow, Action: s.Action, Resource: s.RuleResource()}); err != nil {
			t.Fatalf("%s %s produced ACL key %q, which authz.ValidateRule rejects (%v). "+
				"A registry row naming a rule that cannot be written is an ungovernable action",
				s.Method, s.Route, s.ACLKey, err)
		}
		if s.ACLKey != actions.RenderACLKey(s.Action, s.Resource, s.ResourceName) {
			t.Fatalf("%s: ACLKey %q does not round-trip through RenderACLKey", s.ID, s.ACLKey)
		}
	}

	// -----------------------------------------------------------------------
	// The UI half.
	// -----------------------------------------------------------------------
	join := loadUICommands(t, srv.router, mounted, companyID, uiCommandsManifestPath)
	prov := actions.Provenance{
		RouteArms:         len(specs),
		UICommandsJoined:  join.commandsJoined,
		ClientCallsJoined: join.clientJoined,
		ClientCallsUnused: join.clientUnused,
		UIManifestPresent: join.present,
		UIManifestPath:    strings.TrimPrefix(uiCommandsManifestPath, "../../../"),
	}
	switch {
	case !join.present:
		prov.Degradation = "generated WITHOUT " + prov.UIManifestPath +
			": every Channel is \"none\" and this registry proves nothing about whether a human " +
			"can reach any of these actions. Regenerate once that manifest exists."
		t.Logf("⚠️  %s is absent. %s", uiCommandsManifestPath, prov.Degradation)

	// ⛔ THE MANIFEST EXISTS BUT CARRIES NO (verb, path) CLAIMS. This is the
	// worse of the two incomplete states, because `present: true` reads as a
	// complete build at a glance. It is recorded separately and in the manifest's
	// own vocabulary so the next reader does not go hunting for a missing file
	// that is plainly there.
	//
	// ⛔ AND THE FIX IS NOT TO JOIN ON SOMETHING ELSE. The fields the manifest
	// does carry — scope, screen_action, the gateway label — are SCREEN
	// identities, and deriving an ACL key from a screen identity is wrong on at
	// least five screens in this product (see loadUICommands' doc). A join that
	// produced a plausible-looking Channel for 200 rows and the wrong key for six
	// of them is strictly worse than no join, because the six would never be
	// looked at again.
	case join.aclClaims == 0 && join.clientCalls == 0:
		prov.Degradation = prov.UIManifestPath + " is present but carries NEITHER an " +
			"`acl: [{verb, path}]` claim on any command NOR a `client_calls` census, so there " +
			"is nothing to join on. The ACL key must come from the router, via the (verb, path) " +
			"the product actually calls; it must NEVER be derived from a screen kind or a " +
			"gateway label. Until one of the two exists, every Channel here is \"none\" and says " +
			"nothing."
		t.Logf("⚠️  %s", prov.Degradation)
	}
	if len(join.unresolved) > 0 {
		t.Errorf("%d UI/client call(s) in %s name a (verb, path) the router does not mount:\n  %s\n\n"+
			"Each one is either a client method calling a route that no longer exists, or a route "+
			"this registry is blind to. Both are findings; neither may be dropped.",
			len(join.unresolved), uiCommandsManifestPath, strings.Join(join.unresolved, "\n  "))
	}
	for i := range specs {
		if h, ok := join.hits[specs[i].ID]; ok {
			specs[i].Channel = h.channel
			specs[i].UICommand = h.command
		}
	}

	counts := registryCounts{Specs: len(specs), ByAction: map[string]int{}}
	keys := map[string]bool{}
	for _, s := range specs {
		counts.ByAction[string(s.Action)]++
		if s.IsWrite {
			counts.Writes++
		} else {
			counts.Reads++
		}
		if s.Channel != actions.ChannelNone {
			counts.WithHumanPath++
		}
		if s.DateSubject != "" {
			counts.DatedSubjects++
		}
		keys[s.ACLKey] = true
	}
	counts.DistinctKeys = len(keys)

	golden := registryGolden{
		Note: "GENERATED by internal/server's TestActionRegistryGolden by walking the real chi " +
			"router and calling the real routeAction() on each mounted route. The ACL key on " +
			"each row is VERBATIM what the PDP will evaluate for that (method, route). Do not " +
			"edit by hand. The frontend asserts the converse direction against this file: every " +
			"UI command must land on a row here.",
		Regenerate: "UPDATE_GOLDENS=1 go test ./internal/server/ -run TestActionRegistryGolden",
		Source:     prov,
		Counts:     counts,
		Specs:      specs,
	}
	gotJSON, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	gotJSON = append(gotJSON, '\n')
	gotGo := renderRegistryGo(t, specs, prov, counts)

	if os.Getenv("UPDATE_GOLDENS") != "" {
		writeArtefact(t, actionRegistryGenPath, gotGo)
		writeArtefact(t, actionRegistryJSONPath, gotJSON)
		byAction := make([]string, 0, len(counts.ByAction))
		for a, n := range counts.ByAction {
			byAction = append(byAction, fmt.Sprintf("%s=%d", a, n))
		}
		sort.Strings(byAction)
		t.Logf("regenerated: %d specs (%s), %d writes, %d reads, %d distinct ACL keys, "+
			"%d with a human path, %d naming a date subject; ui manifest present=%v commands joined=%d "+
			"client calls joined=%d (unused methods skipped=%d, expanded=%d)",
			counts.Specs, strings.Join(byAction, " "), counts.Writes, counts.Reads,
			counts.DistinctKeys, counts.WithHumanPath, counts.DatedSubjects, join.present,
			join.commandsJoined, join.clientJoined, join.clientUnused, len(join.expanded))
		for _, e := range join.expanded {
			t.Logf("  expanded: %s", e)
		}
		return
	}

	compareArtefact(t, actionRegistryGenPath, gotGo)
	compareArtefact(t, actionRegistryJSONPath, gotJSON)
}

func writeArtefact(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("wrote %s (%d bytes)", path, len(content))
}

func compareArtefact(t *testing.T, path string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\n\nIf this is the first run, generate it:\n"+
			"  UPDATE_GOLDENS=1 go test ./internal/server/ -run TestActionRegistryGolden", path, err)
	}
	if string(got) == string(want) {
		return
	}
	t.Errorf("%s is STALE.\n\n"+
		"THIS IS NOT A FAILURE TO ROUTE AROUND. This file is what internal/actions serves to "+
		"the agent layer as \"the set of things that can be driven, and under whose ACL key\". "+
		"A row that drifted from routeAction is an action the agent believes is governed by one "+
		"rule and the PDP governs by another.\n\n"+
		"Read the diff, decide whether it is what you meant, then:\n"+
		"  UPDATE_GOLDENS=1 go test ./internal/server/ -run TestActionRegistryGolden\n\n"+
		"--- got (%d bytes) ---\n%s\n--- want (%d bytes) ---\n%s",
		path, len(got), got, len(want), want)
}

// renderRegistryGo emits the checked-in Go registry. It is printf and not
// text/template on purpose: one dependency-free function whose output a reviewer
// can predict from reading it, and %q for every string so a route or a
// description containing a quote cannot produce a file that does not compile.
func renderRegistryGo(t *testing.T, specs []actions.ActionSpec, prov actions.Provenance, counts registryCounts) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteString("// Code generated by internal/server's TestActionRegistryGolden. DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// Regenerate with:\n")
	b.WriteString("//\n")
	b.WriteString("//\tUPDATE_GOLDENS=1 go test ./internal/server/ -run TestActionRegistryGolden\n")
	b.WriteString("//\n")
	b.WriteString("// Each row was produced by walking the real chi router and calling the real\n")
	b.WriteString("// routeAction() on a synthesised request for that route, so ACLKey is VERBATIM\n")
	b.WriteString("// what the PDP evaluates. Editing a row by hand breaks that guarantee silently:\n")
	b.WriteString("// the next regeneration would restore it, and in between the agent layer would\n")
	b.WriteString("// be reading a key the PDP never emits.\n")
	b.WriteString("\npackage actions\n\n")
	fmt.Fprintf(&b, "// Source records which inputs actually contributed to this build. See\n")
	fmt.Fprintf(&b, "// Provenance for why a partial build must be visible rather than silent.\n")
	fmt.Fprintf(&b, "var Source = Provenance{\n")
	fmt.Fprintf(&b, "\tRouteArms:         %d,\n", prov.RouteArms)
	fmt.Fprintf(&b, "\tUICommandsJoined:  %d,\n", prov.UICommandsJoined)
	fmt.Fprintf(&b, "\tClientCallsJoined: %d,\n", prov.ClientCallsJoined)
	fmt.Fprintf(&b, "\tClientCallsUnused: %d,\n", prov.ClientCallsUnused)
	fmt.Fprintf(&b, "\tUIManifestPresent: %t,\n", prov.UIManifestPresent)
	fmt.Fprintf(&b, "\tUIManifestPath:    %q,\n", prov.UIManifestPath)
	if prov.Degradation != "" {
		fmt.Fprintf(&b, "\tDegradation:       %q,\n", prov.Degradation)
	}
	fmt.Fprintf(&b, "}\n\n")

	byAction := make([]string, 0, len(counts.ByAction))
	for a, n := range counts.ByAction {
		byAction = append(byAction, fmt.Sprintf("%s=%d", a, n))
	}
	sort.Strings(byAction)
	fmt.Fprintf(&b, "// Specs is every governable operation the company-scoped HTTP surface mounts.\n")
	fmt.Fprintf(&b, "// %d rows, %d distinct ACL keys: %s.\n",
		counts.Specs, counts.DistinctKeys, strings.Join(byAction, ", "))
	fmt.Fprintf(&b, "var Specs = []ActionSpec{\n")
	for _, s := range specs {
		fmt.Fprintf(&b, "\t{\n")
		fmt.Fprintf(&b, "\t\tID:     %q,\n", s.ID)
		fmt.Fprintf(&b, "\t\tMethod: %q,\n", s.Method)
		fmt.Fprintf(&b, "\t\tRoute:  %q,\n", s.Route)
		fmt.Fprintf(&b, "\t\tAction:   %q,\n", string(s.Action))
		fmt.Fprintf(&b, "\t\tResource: %q,\n", string(s.Resource))
		if s.ResourceName != "" {
			fmt.Fprintf(&b, "\t\tResourceName: %q,\n", s.ResourceName)
		}
		fmt.Fprintf(&b, "\t\tACLKey:   %q,\n", s.ACLKey)
		fmt.Fprintf(&b, "\t\tIsWrite:        %t,\n", s.IsWrite)
		fmt.Fprintf(&b, "\t\tStepUpEligible: %t,\n", s.StepUpEligible)
		if s.DateSubject != "" {
			fmt.Fprintf(&b, "\t\tDateSubject:    %q,\n", s.DateSubject)
		}
		fmt.Fprintf(&b, "\t\tChannel:     %q,\n", string(s.Channel))
		if s.UICommand != "" {
			fmt.Fprintf(&b, "\t\tUICommand:   %q,\n", s.UICommand)
		}
		fmt.Fprintf(&b, "\t\tLabel:       %q,\n", s.Label)
		fmt.Fprintf(&b, "\t},\n")
	}
	fmt.Fprintf(&b, "}\n")

	// ⚠️ gofmt THE OUTPUT RATHER THAN ALIGNING IT BY HAND IN THE PRINTFS ABOVE.
	// This file is checked in and byte-compared, so a run of `gofmt -w ./...` by
	// anybody — or an editor-on-save — would rewrite it and the next comparison
	// would report a stale registry that nobody changed. Formatting it here makes
	// the generator's output a fixed point of gofmt.
	src, ferr := format.Source([]byte(b.String()))
	if ferr != nil {
		t.Fatalf("the generated registry is not valid Go (%v). This is a bug in "+
			"renderRegistryGo, not in the router:\n%s", ferr, b.String())
	}
	return src
}

// TestUICommandJoinResolvesRoutes exercises the (verb, path) -> router -> spec-id
// join against a HAND-WRITTEN manifest, because the real one does not exist yet
// and unverified join code is the part of this generator most likely to be
// silently wrong.
//
// ⛔ IT IS ALSO THE PLACE THE "NEVER DERIVE THE KEY FROM A SCREEN NAME" RULE IS
// DEMONSTRATED RATHER THAN ASSERTED. The `tds` command below points at
// /tds-challans and must land on the SINGULAR bare token `tds-challan`; the
// `budget` one at /budget-variance and must land on `report:budget-variance`.
// A join that went through a screen kind would get the first one wrong and the
// second one right, which is exactly the sort of half-working that survives
// review.
func TestUICommandJoinResolvesRoutes(t *testing.T) {
	srv := &Server{}
	srv.routes()
	companyID := uuid.NewString()

	manifest := `{
	  "schema_version": 1,
	  "commands": [
	    {"id": "voucher.sales.create", "screen": "SalesVoucher", "combo": "F8",
	     "acl": [{"verb": "POST", "path": "/v1/companies/${companyId}/vouchers/sales"}]},
	    {"id": "report.budget-variance.open", "screen": "BudgetVariance", "combo": "",
	     "acl": [{"verb": "GET", "path": "/v1/companies/{companyID}/budget-variance?from=x"}]},
	    {"id": "tds.challans.list", "screen": "TdsChallans", "combo": "",
	     "acl": [{"verb": "GET", "path": "/v1/companies/:companyId/tds-challans"}]},
	    {"id": "bank.recon.set-date", "screen": "BankReconciliation", "combo": "Enter",
	     "acl": [{"verb": "PUT", "path": "/v1/companies/${companyId}/bank-reconciliation/${ledgerId}/${voucherId}/${legNo}"}]},
	    {"id": "auth.login", "screen": "Login", "combo": "",
	     "acl": [{"verb": "POST", "path": "/v1/login"}]},
	    {"id": "dead.method", "screen": "Ghost", "combo": "",
	     "acl": [{"verb": "GET", "path": "/v1/companies/${companyId}/no-such-report"}]}
	  ]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "ui-commands.json")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// The fixture carries no client_calls; this test is about the COMMAND half,
	// and the client half has its own (TestClientCallJoinCoversTheParameterised
	// FamiliesAndNothingElse).
	join := loadUICommands(t, srv.router, mountedRoutes(t, srv.router), companyID, path)
	hits, unresolved := join.hits, join.unresolved
	if !join.present {
		t.Fatal("loadUICommands reported the fixture absent")
	}
	if join.aclClaims != 6 {
		t.Errorf("aclClaims = %d, want 6 (one per command in the fixture)", join.aclClaims)
	}
	if join.commandsJoined != 4 {
		t.Errorf("commandsJoined = %d, want 4 (login is not company-scoped and no-such-report does not exist)", join.commandsJoined)
	}
	want := map[string]uiHit{
		"post.vouchers.sales": {channel: actions.ChannelKeystroke, command: "voucher.sales.create"},
		"get.budget-variance": {channel: actions.ChannelScreen, command: "report.budget-variance.open"},
		"get.tds-challans":    {channel: actions.ChannelScreen, command: "tds.challans.list"},
		"put.bank-reconciliation._ledgerid._voucherid._legno": {channel: actions.ChannelKeystroke, command: "bank.recon.set-date"},
	}
	for id, w := range want {
		got, ok := hits[id]
		if !ok {
			t.Errorf("no join hit for %q; the manifest names a call that should resolve to it", id)
			continue
		}
		if got != w {
			t.Errorf("hit for %q = %+v, want %+v", id, got, w)
		}
	}
	if len(hits) != len(want) {
		t.Errorf("got %d hits, want %d: %+v", len(hits), len(want), hits)
	}

	// CONTROL 1: the non-company-scoped call is NOT reported as a miss. If it
	// were, every login/company-list command in the real manifest would show up
	// as a finding and the list would be ignored.
	// CONTROL 2: the call to a route the router does not mount IS reported. A
	// join that silently dropped misses would let a dead client method — or a
	// route this registry is blind to — pass unnoticed, which is the whole
	// failure the `unresolved` list exists to surface.
	if len(unresolved) != 1 || !strings.Contains(unresolved[0], "dead.method") {
		t.Errorf("unresolved = %v, want exactly the dead.method entry", unresolved)
	}

	// CONTROL 3: the joined ids must be REAL rows, not strings this test and the
	// generator happen to agree on. Resolve each against the checked-in registry.
	for id := range want {
		if _, ok := actions.ByID(id); !ok {
			t.Errorf("join produced id %q, which is not in the generated registry; specID and "+
				"the generator's walk have diverged", id)
		}
	}
}

// mountedRoutes re-walks the router into the {method: []pattern} shape
// loadUICommands needs for the segment-wise fallback.
func mountedRoutes(t *testing.T, router chi.Router) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	if err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, companyRoutePrefix) {
			out[method] = append(out[method], route)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("the walk mounted nothing; every fallback match below would be vacuous")
	}
	return out
}

// TestClientCallJoinCoversTheParameterisedFamiliesAndNothingElse pins the ONE
// place this generator is allowed to answer a route question without chi.
//
// ⛔ THE FALLBACK IS THE RISKY PART OF THE JOIN AND THIS IS WHERE IT IS HELD.
// `printReport(companyID, slug)` builds `/print/reports/{slug}` — the slug is a
// parameter on the client and one of thirty-three LITERALS in the router — so
// chi's Find, handed a uuid, matches nothing. Expanding it against the walk is
// right (the method can produce all thirty-three URLs) and it is also exactly
// the move that, applied too eagerly, would claim a human path for routes nobody
// reaches. So: the expansion must cover the three known families completely, and
// must NOT fire for an ordinary parameterised route that Find already resolves.
func TestClientCallJoinCoversTheParameterisedFamiliesAndNothingElse(t *testing.T) {
	srv := &Server{}
	srv.routes()
	companyID := uuid.NewString()
	mounted := mountedRoutes(t, srv.router)

	manifest := `{
	  "commands": [],
	  "client_calls": [
	    {"method": "printReport",  "verb": "GET", "path": "/v1/companies/{companyID}/print/reports/{slug}", "used": true},
	    {"method": "printVoucher", "verb": "GET", "path": "/v1/companies/{companyID}/print/vouchers/{voucherClass}/{voucherID}", "used": true},
	    {"method": "getPartyOpenBills", "verb": "GET", "path": "/v1/companies/{companyID}/vouchers/{voucherClass}/open-bills/{ledgerID}", "used": true},
	    {"method": "getVoucher",   "verb": "GET", "path": "/v1/companies/{companyID}/vouchers/{voucherID}", "used": true},
	    {"method": "deadMethod",   "verb": "GET", "path": "/v1/companies/{companyID}/no-such-report", "used": true},
	    {"method": "alterBudget",  "verb": "PUT", "path": "/v1/companies/{companyID}/budgets/{budgetID}", "used": false}
	  ]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "ui-commands.json")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	join := loadUICommands(t, srv.router, mounted, companyID, path)

	// The three families expand, and to the counts the router actually mounts.
	if len(join.expanded) != 3 {
		t.Errorf("%d call(s) needed the segment-wise fallback, want exactly 3: %v", len(join.expanded), join.expanded)
	}
	wantFamily := map[string]int{
		"get.print.reports.":  len(authz.KnownReportNames),
		"get.print.vouchers.": len(authz.KnownVoucherClasses),
	}
	got := map[string]int{}
	openBills := 0
	for id := range join.hits {
		for prefix := range wantFamily {
			if strings.HasPrefix(id, prefix) {
				got[prefix]++
			}
		}
		if strings.HasPrefix(id, "get.vouchers.") && strings.Contains(id, ".open-bills.") {
			openBills++
		}
	}
	for prefix, want := range wantFamily {
		if got[prefix] != want {
			t.Errorf("the %s* family joined %d route(s), want %d — the expansion is not covering "+
				"the vocabulary the router loops over", prefix, got[prefix], want)
		}
	}
	if openBills == 0 {
		t.Error("no open-bills route joined; that family is 8 of the 230 mounted routes")
	}

	// ⛔ CONTROL 1: an ordinary parameterised route resolves through chi and does
	// NOT go through the fallback — if it did, `getVoucher` would claim every
	// five-segment GET under /vouchers/ and the coverage number would be a
	// fiction.
	if h, ok := join.hits["get.vouchers._voucherid"]; !ok || h.command != "client.getVoucher" {
		t.Errorf("get.vouchers._voucherid = %+v (present=%t), want a claim from client.getVoucher", h, ok)
	}
	for _, e := range join.expanded {
		if strings.Contains(e, "getVoucher") {
			t.Errorf("getVoucher went through the segment-wise fallback (%s); chi resolved it "+
				"already, and the fallback would let one id-shaped parameter swallow a whole "+
				"subtree", e)
		}
	}

	// CONTROL 2: a call to a route the router does not mount is REPORTED, not
	// silently expanded into something plausible.
	if len(join.unresolved) != 1 || !strings.Contains(join.unresolved[0], "deadMethod") {
		t.Errorf("unresolved = %v, want exactly the deadMethod entry", join.unresolved)
	}

	// CONTROL 3: a call whose METHOD nothing in the app references is counted
	// but claims nothing. Without this, "the client can call it" would silently
	// become "an operator does".
	if join.clientUnused != 1 {
		t.Errorf("clientUnused = %d, want 1", join.clientUnused)
	}
	if h, ok := join.hits["put.budgets._budgetid"]; ok {
		t.Errorf("an UNUSED client method claimed put.budgets._budgetid (%+v). A method no "+
			"screen calls puts nobody in front of that route", h)
	}
}
