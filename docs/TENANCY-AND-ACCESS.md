# Tenancy and Access Control — every flow, every allow/disallow path

**Companion to `TALLY-FOR-FOUNDERS.md`.** That document explains the accounting.
This one explains *who is allowed to do what, and why* — the SaaS layer.

Every status code below was observed on the running stack. The whole document is
gated by `pnpm verify:tenancy`, which exercises **113 paths** and fails if any
branch changes behaviour.

```bash
cd frontend
pnpm seed:demo -- --insightd        # company A (Demo Traders) — prints its id
pnpm seed:tenancy <A>               # adds a 2nd user to A, builds company B
pnpm verify:tenancy <A> <B>         # 113 access-control checks
```

The verifier is **self-cleaning**: it must write in order to test write
permission, so it tracks every voucher it creates and tombstones them as Owner at
the end. Company A's Trial Balance is byte-identical before and after — proven by
running it twice and asserting `total_debit` returns to the seed's `774970.0000`.

---

## 1. The three layers a request passes through

A request is refused at the **first** layer that says no. Getting the layers in
the right order is what makes the status codes meaningful.

```
   ┌─ 1. AUTHENTICATION ───────────────────────────────┐
   │  Is the bearer token real?                        │  no  -> 401
   └───────────────────────────────────────────────────┘
   ┌─ 2. MEMBERSHIP ───────────────────────────────────┐
   │  Is this user a member of THIS company?           │  no  -> 403
   └───────────────────────────────────────────────────┘
   ┌─ 3. POLICY (PDP) ─────────────────────────────────┐
   │  Does their security level permit this            │  no  -> 403
   │  (action, resource)? Plus cutoff and step-up.     │        (or 403 STEPUP_REQUIRED)
   └───────────────────────────────────────────────────┘
```

**401 is reserved for token problems.** An authenticated caller who is not
permitted gets **403**, never 401. That distinction matters to a client: 401 means
"log in again", 403 means "you are logged in, but no". Observed:

| Request | Result |
|---|---|
| No `Authorization` header | `401 {"error":"missing bearer token"}` |
| `Authorization: Bearer not-a-jwt` | `401 {"error":"invalid token"}` |
| Valid token, subject has no membership | `403 {"error":"not authorized for this company"}` |
| Valid token, member of a *different* company | `403 {"error":"not a member of this company"}` |

Note the last two differ. An unknown subject and a known-but-not-a-member subject
both get 403, and neither reveals whether the company exists.

---

## 2. The policy engine, in precedence order

`backend/internal/authz/authz.go`. Evaluated on **every** request after
authentication and membership:

| # | Rule | Effect |
|---|---|---|
| 1 | Subject starts with `svc:` | Full access to the targeted company. Cutoff and step-up **do not** apply. |
| 2 | Level `is_owner` | Allow — **but cutoff and step-up still apply.** |
| 3 | Most-specific matching ACL rule | `disallow` beats `allow` at equal specificity. |
| 4 | No rule matched | `default_access`: `full` → allow, `none` → **deny**. |
| 5 | Back-dated cutoff (writes only) | `voucher_date < cutoff` → deny. |
| 6 | `requires_webauthn` (writes only) | No WebAuthn in the token's AMR → `STEPUP_REQUIRED`. |

**Specificity** is scored so a precise rule beats a broad one:

```
exact action + exact resource:name   = 3    e.g. allow display report:trial-balance
exact action + exact resource        = 2    e.g. allow create  sales
exact action + "*"                   = 1
"*"          + "*"                   = 0
```

Ties break toward **disallow**. That is the safe direction: a rule that says "no"
is never overridden by an equally-specific "yes".

### How a route becomes an (action, resource)

`routeAction()` in `auth_middleware.go` maps the matched route to the pair the PDP
evaluates. The mapping is a **closed list** — and an unmapped route is denied:

| Route | Action / Resource |
|---|---|
| `POST .../vouchers/{class}` | `create` / `{class}` |
| `PUT .../vouchers/{class}/{id}` | `alter` / `{class}` |
| `DELETE .../vouchers/{id}` | `delete` / `voucher` |
| `GET .../vouchers/{id}` | `display` / `voucher` |
| `GET .../vouchers/{id}/history` (edit log) | `display` / `voucher` — the SAME pair, deliberately: whoever may see a voucher may see how it came to look that way, and a level that cannot see it cannot see its history either |
| `POST .../ledgers|groups|units|stock-items` | `create` / `master` |
| `GET .../ledgers` | `display` / `master` |
| `PUT .../ledgers/{id}/opening` | `alter` / `master` |
| `GET .../trial-balance` etc. | `display` / `report:NAME` |
| `GET .../` (company master) | `display` / `company` |
| `POST .../import/tally-xml` | `create` / `import` |
| `POST .../sync/commit` | `create` / `sync` |
| `POST .../query` | `display` / `query` |
| `GET .../members`, `GET .../security-levels` | `display` / `security` |
| `POST .../members/invites`, `POST .../security-levels` | `create` / `security` |
| `PUT .../members/{userID}`, `PUT .../security-levels/{id}` | `alter` / `security` |
| `DELETE .../members/…`, `DELETE .../security-levels/{id}` | `delete` / `security` |

> **Observed:** `GET /v1/companies/{A}/not-a-real-route` as an **Owner** →
> `403 {"error":"unmapped route: access denied"}`.
>
> This is deliberate and worth understanding: adding a route without adding its
> `routeAction` entry makes the route **inaccessible**, not **unprotected**. The
> failure mode of forgetting is a 403, never an open door.

---

## 3. The two seeded security levels

Every company gets these at creation (`seed_company_auth_defaults`):

| | Owner | Data Entry |
|---|---|---|
| `is_owner` | **true** | false |
| `default_access` | `full` | **`none`** |
| `requires_webauthn` | **true** | false |
| `backdated_cutoff` | none | none |
| ACL rules | none needed | 25 explicit `allow` rules |

**Data Entry's rules**, and nothing else:

```
allow create  | alter | display   sales, purchase, receipt, payment,
                                  contra, journal, credit-note, debit-note
allow display                     report
allow display                     company, master   (migration 0022)
```

Everything absent from that list is denied, because `default_access` is `none`.
That is the whole design: **a data-entry operator can key documents and read
reports, and can do nothing else.**

---

## 4. Every allow/disallow path, as observed

### Owner

| Attempt | Result | Why |
|---|---|---|
| Read Trial Balance (with WebAuthn) | `200` | rule 2 — owner |
| Post a voucher (with WebAuthn) | `201` | rule 2 |
| Create a ledger master | `201` | rule 2 |
| Delete a voucher | `200` | rule 2 |
| **Post a voucher WITHOUT WebAuthn** | **`403 STEPUP_REQUIRED`** | rule 6 — owner rights do not bypass step-up |
| **Read a report WITHOUT WebAuthn** | **`200`** | step-up gates **writes only** |

The last two are the same user with the same rights and the same level. The only
difference is the token's AMR. That is the step-up mechanism doing its job:
reading your own books never demands a second factor; changing them does.

```json
403 {"code":"STEPUP_REQUIRED","error":"webauthn step-up required"}
```

The distinguishable `code` is the contract — a client uses it to trigger a
WebAuthn ceremony rather than bouncing the user to a login screen.

### Data Entry

| Attempt | Result | Why |
|---|---|---|
| Create a Journal voucher | `201` | matched `allow create journal` |
| Create a **Credit Note** | `201` | matched `allow create credit-note` (see §7) |
| Display Trial Balance | `200` | matched `allow display report` |
| **Delete a voucher** | `403 "default access: none"` | no `delete` rule anywhere |
| **Create a ledger master** | `403 "default access: none"` | no `master` rule |
| **`POST /query`** (ask your books) | `403 "default access: none"` | no `query` rule |
| Read the **company master** | `200` | `allow display company` (migration 0022) — the client hydrates the home GST state from it, so without this an operator cannot key GST |
| Read the **chart of accounts** | `200` | `allow display master` — picking an account IS data entry |
| **ALTER** a master (set an opening balance) | `403` | display is not alter: an operator picks from the accounts a supervisor defined |
| **Import Tally XML** | `403 "default access: none"` | no `import` rule |
| **Read the company master** | `403 "default access: none"` | no `company` rule |

Every denial reports `default access: none` — i.e. *nothing matched*, rather than
*something forbade it*. That is the signature of default-deny, and it is the
correct message: the operator was never granted the right in the first place.

> **A consequence worth flagging, not defending.** The last row means a Data
> Entry user **cannot read the company master** (`GET /v1/companies/{id}`). The
> web client hydrates the company's home `state_code` from exactly that endpoint
> in order to compute the intra/inter-state GST split in the browser. So the UI
> is not currently usable end-to-end as a Data Entry operator. The *server* is
> right — nobody granted `display company` — but the seeded rule set is
> incomplete for the client that exists. Fix is one ACL row; I have not added it
> because it is a product decision about what a data-entry operator should see,
> not a bug to silently patch.

### Service principal

| Attempt | Result | Why |
|---|---|---|
| Read company A with `svc:statutoryd` | `200` | rule 1 — no membership required |
| **Create a company** as `svc:` | `403 "service principals cannot create companies"` | explicit guard in the authn-only middleware |

Service principals exist so `statutoryd` can pin an IRN to a voucher without
being a "user". They are powerful *within* a company and deliberately powerless
at the tenant-lifecycle boundary.

### Back-dated cutoff

Set `backdated_cutoff = 2026-07-01` on the Data Entry level, then:

| Voucher date | Result |
|---|---|
| `2026-06-15` (before) | `403 {"error":"voucher date is before the back-dated cutoff"}` |
| `2026-07-15` (on/after) | `201` |

This is how a business stops staff back-dating entries into a period already
filed with the government. It applies to **writes only**, and it applies to Owners
too — an Owner cannot quietly back-date past their own cutoff.

---

## 5. Tenant isolation

Two companies, two owners, no overlap:

| Caller | Target | Result |
|---|---|---|
| Owner of A | company **B** | `403 "not a member of this company"` |
| Owner of B | company **A** | `403 "not a member of this company"` |
| Owner of B | company B | `200` |

And they genuinely hold different books, so a leak would be *visible* rather than
coincidentally identical:

```
A total_debit = 774970.0000     (Demo Traders,        Karnataka / 29)
B total_debit = 247200.0000     (Sharma Distributors, Maharashtra / 27)
```

**Isolation is enforced in three independent places**, which is why it is a
claim rather than a hope:

1. **Membership** — the middleware refuses a non-member before any query runs.
2. **Row-level security** — every tenant table is `ENABLE` + **`FORCE`** RLS with
   a default-deny policy on the `app.current_company_id` GUC. The app role is
   `NOBYPASSRLS`, so even a query that forgot its `WHERE` returns zero rows.
3. **Architecture tests** — CI asserts an RLS policy exists on *every* tenant
   table, so a new table cannot ship unprotected.

The corpora carry 11 isolation cases and `internal/security` adds dedicated
fuzzing, including "GUC unset", "GUC reset mid-transaction", and "malformed GUC"
— all of which must return **zero rows**, never all rows.

### 5b. ⛔ THE SIDE PLANE THAT WAS OUTSIDE ALL THREE LAYERS — AND IS NOT ANY MORE

Everything above is about **cored**. Until 2026-08-24 the AI insight plane
(`insightd`, the F9 Bill Inbox) sat entirely outside it, and that was a real
tenancy hole rather than a theoretical one. Measured on the wire:

```
GET :8088/companies/<any-company-uuid>/drafts  ->  200 · 15 drafts, hydrated in full
                                                   supplier names · invoice numbers · dates
                                                   posting ledger names · amounts · GST state codes
GET :8087/v1/companies/<the same uuid>         ->  401
```

Twelve routes answered anonymously — six draft routes, `/healthz`,
`/benchmark/last`, and `/docs` `/redoc` `/openapi.json` `/docs/oauth2-redirect`,
which published the whole schema and which no count of the exposure had ever
included because they do not appear in `openapi.paths`. And the browser reached
them **same-origin**: the dev server proxied every `/companies/*` request there.

⛔ **THE POSTGRES RLS UNDERNEATH WAS NEVER A SECOND LAYER.** insightd set
`app.current_company_id` from *the company_id in the path* — the same value the
caller typed — so RLS faithfully enforced whichever scope the caller chose. This is
worth stating plainly because it is the general trap: **RLS is only a second layer
when the GUC comes from somewhere the caller does not control.**

**The fix (register row D49) puts the drafts inside the same three layers as the
day book.** They are cored routes now — `/v1/companies/{id}/drafts…` — mapped by
`routeAction` onto the **existing `purchase` voucher-class token** (GET →
`Display`, POST → `Create`), so §2's precedence, §3's seeded levels and §4's
allow/disallow paths all apply to them unchanged. cored then forwards to insightd
presenting a service credential shared by exactly those two processes, plus the
company **its PDP just authorized**; insightd refuses anything else.

| Caller | Request | Result |
|---|---|---|
| anonymous | `GET :8088/companies/{B}/drafts` | `401` — and the supplier name is absent from the body, not merely behind a status code |
| anonymous | `GET /v1/companies/{B}/drafts` | `401 "missing bearer token"` |
| Owner of A | `GET /v1/companies/{B}/drafts` | `403 "not a member of this company"`, B's supplier name nowhere in the answer |
| Owner of B | `GET /v1/companies/{B}/drafts` | `200`, the draft in full |
| **Data Entry** of B | `GET /v1/companies/{B}/drafts` | `200` — migration 0011 already grants that level `display purchase`, so the inbox needed **no new ACL row** |
| Data Entry with `disallow display purchase` | `GET /v1/companies/{B}/drafts` | `403` — while the same clerk still reads the Trial Balance |
| Data Entry with `disallow create purchase` | `POST …/drafts/{id}/claim` | `403`, **nothing reserved** — while the inbox stays readable |
| holds the service credential, names a company the PDP did not authorize | `GET :8088/companies/{B}/drafts` | `403` — holding the credential is not holding a tenant |

⚠️ **THE TOKEN CHOICE IS THE PART WORTH COPYING.** A new `draft` resource would
have arrived **denied** for Data Entry (`default_access: none`) and forced a
migration granting a power that level already held — §7's lesson in reverse. Reusing
the class token means one rule governs "may this level see supplier purchases" for
the day book and the AI inbox alike, and the two cannot drift apart.

---

## 6. Administering users, roles and levels — over HTTP, no SQL

Until this pass, `POST /v1/companies` made its creator an Owner and that was the
**only** membership any HTTP route could create. Adding a colleague was a psql
statement. There is now a first-class admin surface, and `seed-tenancy.mts` uses
it — the seeder no longer shells out to `psql` at all.

### The invite flow

An administrator never writes another person's identity row. They record an
**invite addressed to a JWT subject**; the invitee materialises their own
membership the next time they authenticate.

```
Owner                                     Invitee
  │                                          │
  ├─ POST /v1/companies/{A}/members/invites  │
  │    {subject, security_level_name}        │
  │      -> 201 {invite_id, status:pending}  │
  │                                          │
  │                              GET /v1/me ─┤
  │                    claims pending invites│
  │                    -> membership created │
  │                                          │
  └─ GET /members  -> the colleague is there ┘
```

Why this shape and not "create the user, then grant them a role":

* `app_user`'s RLS `WITH CHECK` has always been self-only (`subject =
  app_current_subject()`). Keeping it that way means **no API can mint an
  identity for a subject that has never logged in** — the invitee's first
  authenticated call is what creates their row.
* An invite is revocable and auditable. A revoked invite is inert: the invitee's
  `/v1/me` claims nothing and they stay locked out (observed `403`).
* An unclaimed invite grants **nothing**. Between the invite and the claim, the
  invitee is `403 not authorized for this company`, exactly like a stranger.

### The endpoints

| Route | What it does |
|---|---|
| `GET .../members` | members (subject, email, level, `is_self`) + pending invites |
| `POST .../members/invites` | invite a subject at a level (by id **or** name) |
| `DELETE .../members/invites/{id}` | revoke a pending invite |
| `PUT .../members/{userID}` | change a member's security level |
| `DELETE .../members/{userID}` | revoke a member's access |
| `GET .../security-levels` | levels, their ACL rules, and member counts |
| `GET .../security-levels/vocabulary` | the **closed sets** a rule may name (effects, actions, resources, default accesses) |
| `POST .../security-levels` | create a **custom** level |
| `PUT .../security-levels/{id}` | patch a level; `rules` replaces the whole set |
| `DELETE .../security-levels/{id}` | delete an unused custom level |
| `GET /v1/me` | who am I + **which companies I may open** (authn-only) |

### The guards, and why each exists

| Guard | Observed | Why |
|---|---|---|
| The whole surface is `(action, "security")` | Data Entry → `403 default access: none` | Administering users is not data entry. One ACL rule governs the surface. |
| Writes inherit **step-up** | Owner without WebAuthn → `403 STEPUP_REQUIRED` | Granting access is at least as sensitive as posting an invoice. |
| **Service principals refused** | `svc:statutoryd` → `403 service principals cannot administer users` | `svc:` tokens bypass the PDP by design; without this, a compromised side-plane could grant itself Owner. |
| A `svc:` subject cannot be invited | `422` | A membership for a principal that skips the PDP would be meaningless *and* misleading in the UI. |
| **Last-Owner guard** | demote/remove the only Owner → `409` | A company must never be left with nobody able to administer it — and since the admin routes are themselves PDP-gated, that state would be permanent. |
| The **Owner level is immutable** | rename / `default_access` / `requires_webauthn` / rules → `422` | Turning off the Owner level's step-up would silently rewrite the posture every other check assumes. Its **back-dated cutoff IS settable** (Tally behaviour; the cutoff binds Owners too). |
| A level **in use** cannot be deleted | `409` | Members would point at nothing. Historic (accepted/revoked) invites do **not** block it: they keep the level *name* and their FK is nulled, so the audit trail survives the deletion and the deletion survives the audit trail. |
| Rules validated against a **closed vocabulary** | `report:trail-balance` → `422 unknown resource` | A misspelled rule is not rejected by the PDP — it simply never matches. For a `disallow` rule that is the difference between "forbidden" and "permitted". |

### A custom level, end to end

```bash
POST /v1/companies/{A}/security-levels
  {"name":"Auditor","default_access":"none",
   "rules":[{"effect":"allow","action":"display","resource":"report:trial-balance"}]}
POST /v1/companies/{A}/members/invites  {"subject":"auditor@firm","security_level_name":"Auditor"}
GET  /v1/me                              # as auditor@firm -> claims it
```

Observed for `auditor@firm` afterwards:

| Attempt | Result |
|---|---|
| `GET /trial-balance` (the granted report) | `200` |
| `GET /balance-sheet` | `403 default access: none` |
| `POST /vouchers/journal` | `403 default access: none` |
| `GET /members` | `403 default access: none` |

The engine always supported custom levels. This is the first time anything could
create one.

### RLS beneath it (migration 0020)

Three changes, each minimal:

1. **`company_invite`** — new table, `ENABLE`+`FORCE` RLS. Readable by the
   administering company (company GUC) **or** by the addressee (subject GUC —
   the one read that must work before any membership exists). Writable **only**
   under the company GUC: were `subject = me` in the `WITH CHECK`, a caller
   could mint themselves an invite into an arbitrary company and accept it.
2. **`company_membership`** — read widened to the company GUC (an Owner could
   not previously list their own company's members), and the **write side
   NARROWED** to the company GUC at the same time: 0011 allowed a user-GUC-only
   self-grant into *any* company. Nothing did that; now nothing can.
3. **`app_user`** — reads now also admit "this row belongs to a member of the
   company in scope", so a member list can show a colleague's email. The added
   clause is itself evaluated through `company_membership`'s policy, so there is
   no path to enumerate the global directory. `WITH CHECK` is unchanged
   (self-only).

Proven at the store level (`internal/authz/admin_test.go`), including: an invite
in company A is invisible under company B's GUC even when the query's own
`WHERE company_id` is removed (RLS, not the SQL text, is what isolates it), a
membership INSERT naming company A while scoped to B is refused, and an invite
cannot be claimed by a subject it was not addressed to.

---

## 7. A gap this exercise found

Migration 0018 (this pass) added `credit-note` and `debit-note` as voucher
classes. The Data Entry ACL, seeded back in 0011, listed exactly six classes.
Because the level is `default_access: none`, **the new classes were denied** — a
data-entry operator pressing `Alt+F6` got a bare 403 with nothing to indicate the
cause was a missing ACL row rather than a broken screen.

Migration **0019** adds `create`/`alter`/`display` rules for both note
classes and backfills every existing company. Deliberately **no `delete` rule** —
consistent with the other six classes, reversing a posted document stays an Owner
action.

The lesson generalises: **adding a voucher class is a two-file change** — the
class, and the levels permitted to use it. Default-deny made the omission a
harmless 403 instead of an accidental grant, which is exactly the failure mode
you want, but it is still a failure.

---

## 8. What is missing for this to be a real SaaS

Honest list. The engine is multi-tenant; the *product* around it is not yet.

| Missing | Impact |
|---|---|
| ~~No admin API for users/roles~~ | **DONE** — §6. Invite, claim, change level, revoke, and custom levels, all over HTTP; `seed-tenancy.mts` no longer touches psql. |
| ~~No custom security levels~~ | **DONE** — §6. `POST /security-levels` with a vocabulary-validated rule set. |
| **Login: PARTIAL** | **First-party password login SHIPS** (migration 0021 `user_credential`, PBKDF2-HMAC-SHA256 @210k, `POST /auth/login` + `POST /auth/password`, a login screen with logout and a 401 bounce) — see §10. **But it is mounted only where the in-process dev signer is:** `authLoginRoutes()` registers `/auth/login` inside `if s.signer != nil`, and the only production posture `buildAuth()` accepts besides refusing to start is `AUTH_JWKS_URL`+`AUTH_ISSUER`, where the signer is nil. **In that posture `POST /auth/login` is a 404** and identity is the IdP's job. So this closes the gap for the product as it runs today, and SSO/Keycloak remains the production answer — a later swap onto the already-built `JWKSVerifier`. Two residual holes, both tracked: there is **no way for an invited user to set a FIRST password** without a Bearer they cannot yet obtain (an admin-issued one-time set-password token is the required follow-up), and **no password reset**. |
| ~~No company switching in the UI~~ | **DONE** — `F3` opens the list of companies the signed-in user may open (from `GET /v1/me`), with the level held in each, and re-scopes the session. See §9. |
| ~~UI is single-identity~~ | **DONE** — the hardcoded `browser-dev-owner` mint is deleted. The browser signs in as whoever types their user name and password, holds the token in `sessionStorage` (per-tab, so a closed tab is a logged-out tab), shows who is signed in, and offers Sign Out. Every allow/disallow path in this document is now reachable *as the right person*. |
| ~~Admin screens are read-mostly~~ | **DONE** — Gateway > **Users & Roles** now performs every write the API offers: invite, change a member's level, revoke access, revoke a pending invite, and create / edit / delete a custom security level. The rule editor's tokens are SERVED by the PDP (`GET .../security-levels/vocabulary`) rather than hardcoded, so a rule the PDP could never match is unrepresentable. See §9. |
| ~~No email delivery~~ | **DONE** — a transactional outbox (migration 0023): the notification is enqueued in the SAME transaction as the invite and delivered by a dispatcher, with retry and dead-lettering. Users & Roles reports delivery separately from acceptance. The default Sender LOGS — real SMTP is configuration, not code — so nothing yet claims a message left the building. |
| ~~The AI plane was outside the engine~~ | **DONE** — §5b. `insightd` answered twelve routes anonymously and scoped drafts by the caller's own path value; it is now reached only through cored, inside the same three layers. |
| **Not deployed** | `infra/` holds k8s + terraform for EKS `ap-south-1`; none of it has been applied. ⚠️ **And `infra/k8s/base/networkpolicy-default-deny.yaml` claimed "only cored may call insightd" while no proxy existed.** The proxy exists now, so the policy has become a second control instead of the only (unimplemented) one — but it still needs `INSIGHTD_SERVICE_TOKEN` delivered as a secret to both pods, which nothing in `infra/` yet does. |

Suggested ordering from here: invite delivery (an outbox table plus a
logged/stubbed sender is honest and testable offline; real SMTP is config, not
code), then an admin-issued one-time set-password token (the missing half of
first-party login, and a natural pair with delivery), and finally SSO — a live
Keycloak against the built `JWKSVerifier`, plus `aud` validation, which is still
not performed anywhere.

---

## 8b. Signing in — first-party password login

Everything above answers *what is this subject allowed to do*. This section
answers the question that used to have no answer at all: **who says you are that
subject?**

Until migration 0021 the honest answer was "nobody". `POST /auth/token` minted a
validly-signed token for any subject you cared to name, and the web client simply
asked for `browser-dev-owner` on load. Every browser session was the same person,
so every path in §4 was reachable but never *as the right user*.

### The surface

| Route | Auth | Purpose |
|---|---|---|
| `POST /auth/login` | pre-auth | `{subject, password}` → a signed Bearer + the user block |
| `POST /auth/password` | **Bearer required** | `{current_password, new_password}` → `204`. The **only** writer of a password anywhere. |
| `POST /auth/token` | none, **dev-gated** | unchanged; still mints for any subject. Scripts and e2e use it. |

Credentials live in **`user_credential`** (migration 0021), keyed by `user_id`,
storing `algo` / `iterations` / `salt` / `hash` — **PBKDF2-HMAC-SHA256, 210 000
iterations** (`crypto/pbkdf2`, Go standard library; no new dependency).

**Why a separate table and not columns on `app_user`.** Migration 0020 *widened*
`app_user`'s RLS `USING` clause so co-members of the company in scope can read one
another's rows — that is what makes Users & Roles work. A password hash on
`app_user` would therefore have been readable by every colleague. `user_credential`
carries a strictly self-only policy (`user_id = app_current_user_id()`, with no
company clause at all) and is registered in `internal/security`'s
`authCatalogTables`, so ENABLE+FORCE RLS and an explicit `WITH CHECK` are gated,
not merely intended.

### Observed

| Attempt | Result |
|---|---|
| Correct password | `200 {token, token_type, expires_in, user:{id,subject,display_name,email}}` |
| Wrong password | `401 {"error":"invalid credentials"}` |
| Subject that does not exist | `401` — **byte-identical to the line above** |
| User who has never set a password | `401` — byte-identical |
| `is_active = false` | `401` — byte-identical |
| 5 wrong in 15 min | `423 {"error":"account locked","code":"LOCKED","until":…}` |
| …then the **correct** password | `423` — the lock is real, not a counter |
| `POST /auth/password`, no Bearer | `401 {"error":"missing bearer token"}` |
| `new_password` under 10 characters | `400` with the policy reason |
| `subject`/`user_id` smuggled into the `/auth/password` body | **inert** — the victim's password still works |

**No user enumeration, by construction.** All four failure modes funnel through a
single `invalidCredentials` constant and one `writeError` call, so the bodies are
necessarily equal rather than coincidentally equal. Timing is levelled too: when
there is no stored credential the handler still runs one PBKDF2 derivation against
a **decoy credential** over 32 random bytes, so "unknown user" costs the same as
"wrong password".

**A login never mints an identity.** The old dev login called
`CreateUserOnFirstLogin` so lockout state had somewhere to live. The real one does
not — a login must never create the account it is failing to authenticate.

### Two things adversarial review changed here

Both were found by trying to *refute* the implementation, and both are now gated:

1. **The `423` used to be an enumeration oracle.** `RecordFailure` decided
   "locked" purely from the `auth_login_attempt` counter — a table with no RLS,
   written for any subject — and ignored whether `SetLock` had actually updated a
   row. A real account's `until` is the *stored* `locked_until` and is identical on
   every later request; a ghost account's was recomputed as `now+15m` and
   **advanced** each time. Six wrong passwords and a clock told you whether an
   account existed. `SetLock` now reports rows-affected and only a persisted lock
   is ever reported.
2. **A correct password did not reset the failure counter.** `ClearLock` zeroed
   `app_user.failed_attempts`, but the number that decides locking is a `count(*)`
   over `auth_login_attempt`, which it never touched. Four typos, a successful
   sign-in, then one more typo locked a legitimate user out for 15 minutes.
   `FailuresInWindow` now counts only failures *since the last success*. The
   in-memory test fake had always modelled the correct behaviour, which is exactly
   why no hermetic test caught it.

### Still missing

- **No way to set a FIRST password without a Bearer.** `POST /auth/password`
  requires one, and the only in-process minter is the dev-gated `/auth/token`. An
  invited user in a production deployment can never bootstrap. An admin-issued
  one-time set-password token is the required follow-up.
- **No password reset**, no email verification, no rate limit beyond lockout.
- **`AUTH_DEV_TOKEN_ENABLED=true` is now more dangerous than it was.** Combined
  with `/auth/password`, any caller who can reach the port can *permanently* set
  any user's password. It was always a full-impersonation oracle; it is now a
  **persistent** one. Never set it on a reachable host.

---

## 9. In the browser

Two screens make the story above clickable rather than curl-only.

**F3 — Select Company.** Lists the companies *this user* is a member of, from
`GET /v1/me`, with the security level held in each (the same person is routinely
an Owner of their own books and a Data Entry operator on a client's). Choosing
one re-reads that company's master so the browser's home GST state comes from the
server, re-scopes the session, and **unwinds the screen stack to the Gateway** —
the frames underneath were rendered against the previous company's books, and
silently re-pointing them is how an entry lands in the wrong company. A company
that cannot be hydrated is **not** switched to; the error says so.

**Gateway > Users & Roles.** Members (with the level each holds), **pending
invites listed separately** — an invite is not a membership until the invitee
signs in and claims it, and hiding that distinction makes a successful invite
look like a failure — and the security levels with their Allow/Disallow rules.

**Every write the API offers is now performed here**, not just inviting: change a
member's security level, revoke a member's access, revoke a pending invite, and
create / edit / delete a custom security level. Four details that are decisions,
not incidentals:

- **The rule editor's dropdowns come from the SERVER**
  (`GET .../security-levels/vocabulary`), never from a list hardcoded in the
  client. A misspelled rule is not rejected by the PDP — it silently never
  matches, so a `disallow` an administrator believes they wrote would *permit*
  the thing instead. Serving the closed sets makes such a rule unrepresentable
  in the UI rather than merely 422'd, and it cannot drift from `internal/authz`
  the way a TypeScript copy would. Gated both ways by
  `TestAdminVocabularyIsExactlyWhatTheServerAccepts` (every served token
  survives a real write; nothing writable is missing) and live by
  `verify:tenancy` §8b.
- **The guards are explained, not swallowed.** The last Owner's level select is
  disabled with the reason shown *before* the click, rather than offering an
  action the server answers `409`; the Owner level's editor locks every field
  but the back-dated cutoff and sends **only** that field, because a PUT
  carrying anything else is a `422`; a level still used by a member or a pending
  invite reports the server's own `409` sentence.
- **The level offered by default is the least-privileged one**, never Owner, and
  a newly created level starts at `default_access: none`.
- **Destructive actions confirm INLINE, never with `window.confirm`.** A native
  modal blocks the CDP channel the browser e2e drives, so a `confirm()` would
  make revoke and delete permanently untestable — the wrong trade on an
  access-control screen.

Both screens treat a **403 as the product working**: a Data Entry operator opening
Users & Roles is told their level does not permit it, and an Owner without a
WebAuthn AMR is told the write needs step-up — rather than being shown an empty
page that reads as broken.

Verified in a real browser by `pnpm e2e:ui` (headless Chromium, keys sent to the
document exactly as a keyboard user's are): **34/34 flows**, including create →
invite → revoke-invite → delete on a real custom level, each write running the
real WebAuthn step-up ceremony through a CDP virtual authenticator.

**Still API-only:** nothing on this surface. **Still missing:** a *successful*
member level change is not driven in the browser test, because the company that
file bootstraps has exactly one member (so it is the last-Owner case by
construction); that path is gated live by `verify:tenancy` §8b instead, with the
PDP — not the admin API reading its own write — as the oracle.

---

## Appendix — reproducing every line

```bash
cd frontend
pnpm seed:demo -- --insightd     # -> company A
pnpm seed:tenancy <A>            # -> company B + the Data Entry user
pnpm verify:tenancy <A> <B>      # 113 checks; exits non-zero on any drift
pnpm verify:demo <A>             # 24 accounting checks; unaffected by the above
```

Identities the seeder creates:

| Subject | Level | Company |
|---|---|---|
| `browser-dev-owner` | Owner | A |
| `demo-data-entry` | Data Entry | A |
| `sharma-owner` | Owner | B |

Mint a token for any of them yourself — add or omit `"webauthn"` in `amr` to
watch the step-up branch flip:

```bash
curl -s -X POST http://localhost:8087/auth/token \
  -H 'Content-Type: application/json' \
  -d '{"subject":"demo-data-entry","amr":["pwd"]}'
```
