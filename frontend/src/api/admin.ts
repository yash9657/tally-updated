/**
 * The SaaS ADMIN contract — members, invites, security levels, and `/v1/me`.
 *
 * Mirrors backend/internal/server/handler_admin.go field for field. Two shapes
 * are worth understanding before reading the screens:
 *
 *  - **An invite is addressed to a SUBJECT, not to a user.** The server never
 *    writes another person's identity row; the invitee's own next call to
 *    `GET /v1/me` claims the invite and creates their membership. So a fresh
 *    invite appears under `invites` (pending), and only MOVES to `members` once
 *    that person signs in. A UI that shows only `members` would look broken.
 *
 *  - **`/v1/me` is the company switcher's data source.** It is authn-only (no
 *    company in the path), and returns every company this user may open with the
 *    security level they hold in each — which is exactly what F3 needs.
 */

/** One company a user may open, as returned by GET /v1/me. */
export interface MeCompany {
  company_id: string;
  name: string;
  security_level_id: string;
  security_level_name: string;
  is_owner: boolean;
}

/** An invite claimed during this /v1/me call (empty on subsequent calls). */
export interface ClaimedInvite {
  invite_id: string;
  company_id: string;
}

export interface MeResponse {
  user_id: string;
  subject: string;
  companies: MeCompany[];
  claimed_invites: ClaimedInvite[];
}

/** A member of a company. `is_self` marks the caller's own row. */
export interface MemberView {
  user_id: string;
  subject: string;
  email: string;
  display_name: string;
  security_level_id: string;
  security_level_name: string;
  is_owner: boolean;
  is_self: boolean;
  created_at: string;
}

/** A pending (or historic) invite. */
export interface InviteView {
  id: string;
  subject: string;
  email: string;
  display_name: string;
  security_level_id: string;
  security_level_name: string;
  status: string;
  created_at: string;
  /**
   * DELIVERY state of the invitation: "" when nothing was enqueued, else
   * pending | sending | sent | failed.
   *
   * DELIBERATELY SEPARATE FROM `status`. That says whether the invitee has
   * CLAIMED their access; this says whether anyone TOLD them. A company that
   * conflated the two would believe it had onboarded somebody who never heard
   * about it — which was the actual behaviour before the outbox existed, when
   * an invite was a database row that notified nobody.
   */
  notification_status: string;
  notification_attempts: number;
  /** The transport's own words when delivery was abandoned. */
  notification_error?: string;
}

export interface MembersResponse {
  members: MemberView[];
  /** PENDING invites only — people who have been granted access but not signed in yet. */
  invites: InviteView[];
}

/**
 * Invite a subject. Supply the level by id OR by name ("Data Entry") — never
 * both; the server 400s on that, deliberately, rather than picking one.
 */
export interface InviteMemberRequest {
  subject: string;
  email?: string;
  display_name?: string;
  security_level_id?: string;
  security_level_name?: string;
}

export interface InviteMemberResponse {
  invite_id: string;
  subject: string;
  status: string;
}

/** One Disallow/Allow rule of a security level. */
export interface SecurityLevelRule {
  id?: string;
  effect: string;
  action: string;
  resource: string;
}

export interface SecurityLevelView {
  id: string;
  name: string;
  is_owner: boolean;
  default_access: string;
  requires_webauthn: boolean;
  /** "" when the level has no back-dated cutoff. */
  backdated_cutoff: string;
  member_count: number;
  rules: SecurityLevelRule[];
}

export interface SecurityLevelsResponse {
  security_levels: SecurityLevelView[];
}

/**
 * The CLOSED sets an ACL rule may name (GET .../security-levels/vocabulary).
 *
 * SERVED, NOT HARDCODED, ON PURPOSE. A misspelled rule is not rejected by the
 * PDP — it silently never matches, so for a `disallow` the administrator
 * believes they forbade something the system permits. The server validates every
 * rule against internal/authz's closed vocabulary and 422s a stranger; a
 * TypeScript copy of those sets would drift the moment either side changed
 * (a report added in Go would be un-nameable in the UI; one removed would be
 * offered and then 422'd). Fetching it makes the rule editor incapable of
 * composing a rule the server will reject.
 *
 * `resources` arrives FLAT — bare resources and the `report:NAME` forms already
 * expanded — so the client never has to know the ":" grammar.
 */
export interface SecurityVocabulary {
  effects: string[];
  actions: string[];
  resources: string[];
  default_accesses: string[];
}

/** Create/replace a security level. On update, an omitted field is left alone. */
export interface SecurityLevelRequest {
  name?: string;
  default_access?: string;
  requires_webauthn?: boolean;
  /** "" CLEARS the cutoff; a YYYY-MM-DD date sets it. Omit to leave unchanged. */
  backdated_cutoff?: string;
  /**
   * Rules are replaced WHOLESALE when present — this is a PUT of the whole rule
   * set, not a patch of individual rules. Omit the key to leave them alone;
   * send `[]` to clear every rule.
   */
  rules?: SecurityLevelRule[];
}

export interface CreateSecurityLevelResponse {
  security_level_id: string;
  name: string;
}

export interface UpdateSecurityLevelResponse {
  security_level_id: string;
}

export interface DeleteSecurityLevelResponse {
  security_level_id: string;
  status: string;
}

/** Change a member's level. Supply id OR name, never both (the server 400s). */
export interface UpdateMemberRequest {
  security_level_id?: string;
  security_level_name?: string;
}

export interface UpdateMemberResponse {
  user_id: string;
  security_level_id: string;
}

export interface RemoveMemberResponse {
  user_id: string;
  status: string;
}

export interface RevokeInviteResponse {
  invite_id: string;
  status: string;
}
