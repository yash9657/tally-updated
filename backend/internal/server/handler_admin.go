package server

// handler_admin.go — THE SaaS ADMIN SURFACE.
//
// Before this file, `POST /v1/companies` made its creator an Owner and that was
// the ONLY membership any HTTP route could create: adding a colleague, changing
// their role, or defining a third security level were all direct-SQL operations
// (frontend/scripts/seed-tenancy.mts shelled out to psql to do exactly that).
// The PDP has always supported custom levels; nothing could create one.
//
// The routes (all under the PDP-enforced /v1/companies/{companyID} subtree, all
// mapped to the "security" resource — see routeAction):
//
//	GET    /members                        list members + pending invites
//	POST   /members/invites                invite a SUBJECT at a level
//	DELETE /members/invites/{inviteID}     revoke a pending invite
//	PUT    /members/{userID}               change a member's security level
//	DELETE /members/{userID}               revoke a member's access
//	GET    /security-levels                levels + their ACL rules + member counts
//	POST   /security-levels                create a custom level
//	PUT    /security-levels/{levelID}      patch a level (rules replaced wholesale)
//	DELETE /security-levels/{levelID}      delete an unused custom level
//
// plus, OUTSIDE the company subtree (authn-only — a user who is not yet a member
// of anything must be able to call it):
//
//	GET    /v1/me                          who am I, and which companies can I open
//
// THREE DESIGN DECISIONS WORTH THE INK:
//
//  1. AN ADMIN NEVER WRITES ANOTHER PERSON'S IDENTITY ROW. Inviting creates a
//     company_invite addressed to a JWT subject; the invitee materialises their
//     own membership on their next GET /v1/me. So app_user rows stay authored by
//     the user they describe (0011's WITH CHECK), and an admin cannot mint an
//     identity for a subject that does not exist yet.
//
//  2. THESE ARE WRITES, SO THEY INHERIT STEP-UP. An Owner administering users
//     without a WebAuthn AMR gets 403 STEPUP_REQUIRED, exactly like posting a
//     voucher. Granting access is at least as sensitive as an invoice.
//
//  3. SERVICE PRINCIPALS ARE REFUSED. `svc:` tokens bypass the PDP by design
//     (statutoryd must pin an IRN without being a user), which would otherwise
//     hand a compromised side-plane the power to grant itself Owner. This is the
//     same boundary POST /v1/companies already draws.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/platform/db"
)

// adminStore is the subset of authz.Store this surface uses. An interface for
// the same reason authStore is one: the handlers are testable without a DB.
type adminStore interface {
	ListMembers(ctx context.Context, companyID uuid.UUID) ([]authz.Member, error)
	MemberBySubject(ctx context.Context, companyID uuid.UUID, subject string) (authz.Member, bool, error)
	SetMembershipLevel(ctx context.Context, companyID, userID, levelID uuid.UUID) error
	RemoveMembership(ctx context.Context, companyID, userID uuid.UUID) error

	CreateInvite(ctx context.Context, companyID uuid.UUID, in authz.Invite, invitedBy uuid.UUID) (uuid.UUID, error)
	ListInvites(ctx context.Context, companyID uuid.UUID, onlyPending bool) ([]authz.Invite, error)
	RevokeInvite(ctx context.Context, companyID, inviteID uuid.UUID) error
	PendingInvitesForSubject(ctx context.Context, subject string) ([]authz.Invite, error)
	AcceptInvite(ctx context.Context, inviteID, companyID, userID uuid.UUID, subject string) error

	ListLevels(ctx context.Context, companyID uuid.UUID) ([]authz.LevelDetail, error)
	LevelByName(ctx context.Context, companyID uuid.UUID, name string) (authz.Level, error)
	CreateLevel(ctx context.Context, companyID uuid.UUID, spec authz.LevelSpec) (uuid.UUID, error)
	UpdateLevel(ctx context.Context, companyID, levelID uuid.UUID, patch authz.LevelPatch) error
	DeleteLevel(ctx context.Context, companyID, levelID uuid.UUID) error

	UserCompanies(ctx context.Context, userID uuid.UUID) ([]authz.Membership, error)
}

// ---------------------------------------------------------------------------
// Wire shapes
// ---------------------------------------------------------------------------

type memberJSON struct {
	UserID      string `json:"user_id"`
	Subject     string `json:"subject"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	LevelID     string `json:"security_level_id"`
	LevelName   string `json:"security_level_name"`
	IsOwner     bool   `json:"is_owner"`
	IsSelf      bool   `json:"is_self"`
	CreatedAt   string `json:"created_at"`
}

type inviteJSON struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	LevelID     string `json:"security_level_id"`
	LevelName   string `json:"security_level_name"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	// DELIVERY state (item C): "" when nothing was enqueued, else
	// pending | sent | failed. Deliberately separate from Status: that says
	// whether they have CLAIMED access, this says whether anyone TOLD them.
	// A company that conflated the two would think it had onboarded somebody
	// who never heard about it.
	Notification    string `json:"notification_status"`
	NotifyAttempts  int    `json:"notification_attempts"`
	NotifyLastError string `json:"notification_error,omitempty"`
}

type membersResp struct {
	Members []memberJSON `json:"members"`
	Invites []inviteJSON `json:"invites"`
}

// inviteReq names the level by id OR by name ("Data Entry"), so a client that
// knows the level names does not have to resolve uuids first.
type inviteReq struct {
	Subject          string `json:"subject"`
	Email            string `json:"email"`
	DisplayName      string `json:"display_name"`
	SecurityLevelID  string `json:"security_level_id"`
	SecurityLevelNam string `json:"security_level_name"`
}

type memberLevelReq struct {
	SecurityLevelID  string `json:"security_level_id"`
	SecurityLevelNam string `json:"security_level_name"`
}

type ruleJSON struct {
	ID       string `json:"id,omitempty"`
	Effect   string `json:"effect"`
	Action   string `json:"action"`
	Resource string `json:"resource"`
}

type levelJSON struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	IsOwner          bool       `json:"is_owner"`
	DefaultAccess    string     `json:"default_access"`
	RequiresWebAuthn bool       `json:"requires_webauthn"`
	BackdatedCutoff  string     `json:"backdated_cutoff"`
	MemberCount      int        `json:"member_count"`
	Rules            []ruleJSON `json:"rules"`
}

type levelsResp struct {
	Levels []levelJSON `json:"security_levels"`
}

// levelReq is the create/update body. Pointer fields on the update path
// distinguish "absent" from "set to the zero value" — the difference between
// leaving a level's WebAuthn requirement alone and turning it off.
type levelReq struct {
	Name             string     `json:"name"`
	DefaultAccess    string     `json:"default_access"`
	RequiresWebAuthn *bool      `json:"requires_webauthn"`
	BackdatedCutoff  *string    `json:"backdated_cutoff"`
	Rules            []ruleJSON `json:"rules"`
	// RulesPresent is set by the decoder shim below; the JSON decoder alone
	// cannot tell `"rules": []` (clear every rule) from an absent key.
	rulesPresent bool
}

// ---------------------------------------------------------------------------
// Guards shared by every admin handler
// ---------------------------------------------------------------------------

// adminCtx resolves the company scope AND refuses service principals. Every
// admin handler starts here.
func (s *Server) adminCtx(w http.ResponseWriter, r *http.Request) (context.Context, uuid.UUID, bool) {
	if subject, ok := db.SubjectFromContext(r.Context()); ok &&
		strings.HasPrefix(subject, authn.ServicePrefix) {
		writeError(w, http.StatusForbidden, "service principals cannot administer users")
		return nil, uuid.Nil, false
	}
	return s.scopeCtxID(w, r)
}

// writeAdminError maps the authz sentinels onto status codes. Anything
// unrecognised is a 500 — an admin write that fails for an unmodelled reason
// must not be reported as the caller's fault.
func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, authz.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, authz.ErrConflict), errors.Is(err, authz.ErrLevelInUse):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, authz.ErrLastOwner):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, authz.ErrOwnerLevelImmutable):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// resolveLevel turns an (id, name) pair from a request body into a level id.
// Exactly one must be supplied.
func (s *Server) resolveLevel(w http.ResponseWriter, ctx context.Context, companyID uuid.UUID, idStr, name string) (uuid.UUID, bool) {
	switch {
	case idStr != "" && name != "":
		writeError(w, http.StatusBadRequest, "supply security_level_id or security_level_name, not both")
		return uuid.Nil, false
	case idStr != "":
		id, err := uuid.Parse(idStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid security_level_id")
			return uuid.Nil, false
		}
		return id, true
	case name != "":
		lv, err := s.authAdmin.LevelByName(ctx, companyID, name)
		if err != nil {
			writeAdminError(w, err)
			return uuid.Nil, false
		}
		return lv.ID, true
	default:
		writeError(w, http.StatusBadRequest, "security_level_id or security_level_name is required")
		return uuid.Nil, false
	}
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ---------------------------------------------------------------------------
// Members
// ---------------------------------------------------------------------------

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.adminCtx(w, r)
	if !ok {
		return
	}
	members, err := s.authAdmin.ListMembers(ctx, companyID)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	invites, err := s.authAdmin.ListInvites(ctx, companyID, true)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	self, _ := db.UserFromContext(r.Context())

	resp := membersResp{Members: []memberJSON{}, Invites: []inviteJSON{}}
	for _, m := range members {
		resp.Members = append(resp.Members, memberJSON{
			UserID: m.UserID.String(), Subject: m.Subject, Email: m.Email,
			DisplayName: m.DisplayName, LevelID: m.LevelID.String(),
			LevelName: m.LevelName, IsOwner: m.IsOwner,
			IsSelf: m.UserID == self, CreatedAt: iso(m.CreatedAt),
		})
	}
	for _, iv := range invites {
		resp.Invites = append(resp.Invites, inviteJSON{
			ID: iv.ID.String(), Subject: iv.Subject, Email: iv.Email,
			DisplayName: iv.DisplayName, LevelID: iv.LevelID.String(),
			LevelName: iv.LevelName, Status: iv.Status, CreatedAt: iso(iv.CreatedAt),
			Notification: iv.Notification, NotifyAttempts: iv.NotifyAttempts,
			NotifyLastError: iv.NotifyLastError,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleInviteMember(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.adminCtx(w, r)
	if !ok {
		return
	}
	var req inviteReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Subject = strings.TrimSpace(req.Subject)
	if req.Subject == "" {
		writeError(w, http.StatusBadRequest, "subject is required")
		return
	}
	// A service subject must never be grantable as a member: `svc:` tokens skip
	// the PDP entirely, so a membership for one would be meaningless AND would
	// make a compromised side-plane look like a legitimate user in the UI.
	if strings.HasPrefix(req.Subject, authn.ServicePrefix) {
		writeError(w, http.StatusUnprocessableEntity, "service principals cannot be company members")
		return
	}
	levelID, ok := s.resolveLevel(w, ctx, companyID, req.SecurityLevelID, req.SecurityLevelNam)
	if !ok {
		return
	}
	// Already a member? That is a level CHANGE (PUT /members/{userID}), not an
	// invite — say so instead of silently creating an invite that will be a no-op
	// when accepted.
	if existing, found, err := s.authAdmin.MemberBySubject(ctx, companyID, req.Subject); err != nil {
		writeAdminError(w, err)
		return
	} else if found {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "already a member of this company",
			"user_id": existing.UserID.String(),
			"hint":    "PUT /members/" + existing.UserID.String() + " to change their security level",
		})
		return
	}

	invitedBy, _ := db.UserFromContext(r.Context())
	id, err := s.authAdmin.CreateInvite(ctx, companyID, authz.Invite{
		Subject: req.Subject, Email: req.Email, DisplayName: req.DisplayName, LevelID: levelID,
	}, invitedBy)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"invite_id": id.String(),
		"subject":   req.Subject,
		"status":    "pending",
	})
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.adminCtx(w, r)
	if !ok {
		return
	}
	inviteID, err := uuid.Parse(chi.URLParam(r, "inviteID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid invite id")
		return
	}
	if err := s.authAdmin.RevokeInvite(ctx, companyID, inviteID); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"invite_id": inviteID.String(), "status": "revoked"})
}

func (s *Server) handleUpdateMember(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.adminCtx(w, r)
	if !ok {
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var req memberLevelReq
	if !decodeJSON(w, r, &req) {
		return
	}
	levelID, ok := s.resolveLevel(w, ctx, companyID, req.SecurityLevelID, req.SecurityLevelNam)
	if !ok {
		return
	}
	if err := s.authAdmin.SetMembershipLevel(ctx, companyID, userID, levelID); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"user_id":           userID.String(),
		"security_level_id": levelID.String(),
	})
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.adminCtx(w, r)
	if !ok {
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := s.authAdmin.RemoveMembership(ctx, companyID, userID); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"user_id": userID.String(), "status": "removed"})
}

// ---------------------------------------------------------------------------
// Security levels
// ---------------------------------------------------------------------------

func (s *Server) handleListSecurityLevels(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.adminCtx(w, r)
	if !ok {
		return
	}
	levels, err := s.authAdmin.ListLevels(ctx, companyID)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	resp := levelsResp{Levels: []levelJSON{}}
	for _, lv := range levels {
		j := levelJSON{
			ID: lv.ID.String(), Name: lv.Name, IsOwner: lv.IsOwner,
			DefaultAccess: lv.DefaultAccess, RequiresWebAuthn: lv.RequiresWebAuthn,
			MemberCount: lv.MemberCount, Rules: []ruleJSON{},
		}
		if lv.BackdatedCutoff != nil {
			j.BackdatedCutoff = lv.BackdatedCutoff.UTC().Format("2006-01-02")
		}
		for _, rr := range lv.Rules {
			j.Rules = append(j.Rules, ruleJSON{
				ID: rr.ID.String(), Effect: string(rr.Effect),
				Action: string(rr.Action), Resource: string(rr.Resource),
			})
		}
		resp.Levels = append(resp.Levels, j)
	}
	writeJSON(w, http.StatusOK, resp)
}

// parseRules validates the wire rules against the CLOSED PDP vocabulary. A
// misspelled resource is rejected here rather than stored as a rule that can
// never match — for a `disallow` rule, silently-never-matching is the difference
// between "forbidden" and "permitted".
func parseRules(w http.ResponseWriter, in []ruleJSON) ([]authz.Rule, bool) {
	out := make([]authz.Rule, 0, len(in))
	for _, r := range in {
		rule := authz.Rule{
			Effect:   authz.Effect(strings.ToLower(strings.TrimSpace(r.Effect))),
			Action:   authz.Action(strings.ToLower(strings.TrimSpace(r.Action))),
			Resource: authz.Resource(strings.ToLower(strings.TrimSpace(r.Resource))),
		}
		if err := authz.ValidateRule(rule); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return nil, false
		}
		out = append(out, rule)
	}
	return out, true
}

// parseCutoff turns the wire date into a *time.Time. "" means CLEAR.
func parseCutoff(w http.ResponseWriter, s string) (*time.Time, bool, bool) {
	if strings.TrimSpace(s) == "" {
		return nil, true, true // clear
	}
	d, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid backdated_cutoff (want YYYY-MM-DD)")
		return nil, false, false
	}
	return &d, false, true
}

// handleSecurityVocabulary serves the CLOSED sets an acl_rule may name.
//
// WHY THIS IS A ROUTE AND NOT A CONSTANT IN THE CLIENT: a rule whose resource is
// misspelled is not rejected by the PDP — it silently never matches, so for a
// `disallow` the administrator believes they forbade something the system
// permits (see internal/authz/vocabulary.go). The admin API therefore validates
// every rule against this vocabulary and 422s a stranger.
//
// A rule editor in the browser must offer exactly those tokens, and a hardcoded
// TypeScript copy of them would DRIFT: adding a report to KnownReportNames in Go
// would silently leave the UI unable to name it, and removing one would leave
// the UI offering a token the server now 422s. Serving the vocabulary makes the
// drift impossible by construction rather than by a test somebody has to
// remember to run.
//
// It is a GET on the security resource — the same Display/security the level
// list is — so an operator who may not administer users cannot enumerate the
// vocabulary either. Read-only, so it never inherits step-up or the cutoff.
func (s *Server) handleSecurityVocabulary(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.adminCtx(w, r); !ok {
		return
	}
	effects := make([]string, 0, 2)
	for _, e := range []authz.Effect{authz.Allow, authz.Disallow} {
		effects = append(effects, string(e))
	}
	actions := make([]string, 0, len(authz.KnownActions)+1)
	for _, a := range authz.KnownActions {
		actions = append(actions, string(a))
	}
	actions = append(actions, authz.Wildcard)

	// Bare resources plus the "report:NAME" forms, already expanded, so the
	// client renders one flat list and never has to know the ":" grammar. The
	// wildcard is included on both axes: it is a legal rule token.
	resources := make([]string, 0, len(authz.KnownResources)+len(authz.KnownReportNames)+1)
	for _, res := range authz.KnownResources {
		resources = append(resources, string(res))
	}
	for _, n := range authz.KnownReportNames {
		resources = append(resources, "report:"+n)
	}
	resources = append(resources, authz.Wildcard)

	writeJSON(w, http.StatusOK, map[string]any{
		"effects":          effects,
		"actions":          actions,
		"resources":        resources,
		"default_accesses": authz.KnownDefaultAccesses,
	})
}

func (s *Server) handleCreateSecurityLevel(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.adminCtx(w, r)
	if !ok {
		return
	}
	var req levelReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.DefaultAccess == "" {
		// Default-DENY is the safe default for a new level: a level created with
		// no explicit default must not silently grant everything.
		req.DefaultAccess = "none"
	}
	if !authz.ValidDefaultAccess(req.DefaultAccess) {
		writeError(w, http.StatusUnprocessableEntity, "default_access must be full or none")
		return
	}
	rules, ok := parseRules(w, req.Rules)
	if !ok {
		return
	}
	spec := authz.LevelSpec{
		Name: req.Name, DefaultAccess: req.DefaultAccess, Rules: rules,
	}
	if req.RequiresWebAuthn != nil {
		spec.RequiresWebAuthn = *req.RequiresWebAuthn
	}
	if req.BackdatedCutoff != nil {
		cutoff, _, valid := parseCutoff(w, *req.BackdatedCutoff)
		if !valid {
			return
		}
		spec.BackdatedCutoff = cutoff
	}

	id, err := s.authAdmin.CreateLevel(ctx, companyID, spec)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"security_level_id": id.String(),
		"name":              spec.Name,
	})
}

func (s *Server) handleUpdateSecurityLevel(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.adminCtx(w, r)
	if !ok {
		return
	}
	levelID, err := uuid.Parse(chi.URLParam(r, "levelID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid security level id")
		return
	}
	// Decode twice: once into the typed body, once into a raw map so an ABSENT
	// "rules" key is distinguishable from `"rules": []` (clear all rules).
	body := drainBody(r)
	var raw map[string]any
	var req levelReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &raw)
	}
	if _, present := raw["rules"]; present {
		req.rulesPresent = true
	}

	patch := authz.LevelPatch{}
	if name := strings.TrimSpace(req.Name); name != "" {
		patch.Name = &name
	}
	if req.DefaultAccess != "" {
		if !authz.ValidDefaultAccess(req.DefaultAccess) {
			writeError(w, http.StatusUnprocessableEntity, "default_access must be full or none")
			return
		}
		da := req.DefaultAccess
		patch.DefaultAccess = &da
	}
	patch.RequiresWebAuthn = req.RequiresWebAuthn
	if req.BackdatedCutoff != nil {
		cutoff, clear, valid := parseCutoff(w, *req.BackdatedCutoff)
		if !valid {
			return
		}
		patch.BackdatedCutoff = cutoff
		patch.ClearCutoff = clear
	}
	if req.rulesPresent {
		rules, ok := parseRules(w, req.Rules)
		if !ok {
			return
		}
		patch.Rules = &rules
	}

	if err := s.authAdmin.UpdateLevel(ctx, companyID, levelID, patch); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"security_level_id": levelID.String()})
}

func (s *Server) handleDeleteSecurityLevel(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.adminCtx(w, r)
	if !ok {
		return
	}
	levelID, err := uuid.Parse(chi.URLParam(r, "levelID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid security level id")
		return
	}
	if err := s.authAdmin.DeleteLevel(ctx, companyID, levelID); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"security_level_id": levelID.String(), "status": "deleted",
	})
}

// ---------------------------------------------------------------------------
// GET /v1/me — identity + the companies this user may open
// ---------------------------------------------------------------------------

// handleMe is the client's session bootstrap AND the invite-claim seam. It runs
// under the authn-ONLY middleware (verify JWT + create-on-first-login), because
// an invited user is by definition not yet a member of any company — the
// {companyID} subtree would 403 them before they could ever claim their invite.
//
// Order matters: claim first, then list, so a user who was invited a moment ago
// sees the company on the SAME call rather than having to reload.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	subject, _ := db.SubjectFromContext(r.Context())
	userID, ok := db.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "no user in context")
		return
	}

	claimed := []map[string]string{}
	pending, err := s.authAdmin.PendingInvitesForSubject(r.Context(), subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, iv := range pending {
		if err := s.authAdmin.AcceptInvite(r.Context(), iv.ID, iv.CompanyID, userID, subject); err != nil {
			// A revoked-in-flight invite loses the race; that is not an error for
			// the caller's session, so skip it and keep going.
			if errors.Is(err, authz.ErrNotFound) {
				continue
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		claimed = append(claimed, map[string]string{
			"invite_id":  iv.ID.String(),
			"company_id": iv.CompanyID.String(),
		})
	}

	companies, err := s.authAdmin.UserCompanies(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(companies))
	for _, c := range companies {
		out = append(out, map[string]any{
			"company_id":          c.CompanyID.String(),
			"name":                c.CompanyName,
			"security_level_id":   c.LevelID.String(),
			"security_level_name": c.LevelName,
			"is_owner":            c.IsOwner,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":         userID.String(),
		"subject":         subject,
		"companies":       out,
		"claimed_invites": claimed,
	})
}
