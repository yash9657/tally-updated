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
