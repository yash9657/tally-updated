# Tenancy and Access Control — every flow, every allow/disallow path

**Companion to `TALLY-FOR-FOUNDERS.md`.** That document explains the accounting.
This one explains *who is allowed to do what, and why* — the SaaS layer.

Every status code below was observed on the running stack. The whole document is
gated by `pnpm verify:tenancy`, which exercises **27 paths** and fails if any
branch changes behaviour.

```bash
cd frontend
pnpm seed:demo -- --insightd        # company A (Demo Traders) — prints its id
pnpm seed:tenancy <A>               # adds a 2nd user to A, builds company B
pnpm verify:tenancy <A> <B>         # 27 access-control checks
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
| `POST .../ledgers|groups|units|stock-items` | `create` / `master` |
| `GET .../ledgers` | `display` / `master` |
| `PUT .../ledgers/{id}/opening` | `alter` / `master` |
| `GET .../trial-balance` etc. | `display` / `report:NAME` |
| `GET .../` (company master) | `display` / `company` |
| `POST .../import/tally-xml` | `create` / `import` |
| `POST .../sync/commit` | `create` / `sync` |
| `POST .../query` | `display` / `query` |

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
| Create a **Credit Note** | `201` | matched `allow create credit-note` (see §6) |
| Display Trial Balance | `200` | matched `allow display report` |
| **Delete a voucher** | `403 "default access: none"` | no `delete` rule anywhere |
| **Create a ledger master** | `403 "default access: none"` | no `master` rule |
| **`POST /query`** (ask your books) | `403 "default access: none"` | no `query` rule |
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

---

## 6. A gap this exercise found

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

## 7. What is missing for this to be a real SaaS

Honest list. The engine is multi-tenant; the *product* around it is not yet.

| Missing | Impact |
|---|---|
| **No admin API for users/roles** | `POST /v1/companies` makes its creator an Owner, and that is the only membership any HTTP route can create. Inviting a colleague or assigning a role is **SQL only** — which is why `seed-tenancy.mts` shells out to `psql`. A SaaS needs this as a first-class flow. |
| **No custom security levels** | The engine fully supports them (`security_level` + `acl_rule` are per-company data), but only the two seeded levels exist and nothing can create a third. |
| **No login** | `POST /auth/token` mints a token for *any* subject and exists only when a dev signer is configured. There is no password, no SSO, no IdP integration. |
| **No company switching** | `F3` is bound and answers "one company per session". A user with two memberships cannot reach the second in the UI. |
| **UI is single-identity** | The web client authenticates as one hardcoded dev subject. The whole allow/disallow story above is reachable by API but invisible in the browser. |
| **Not deployed** | `infra/` holds k8s + terraform for EKS `ap-south-1`; none of it has been applied. |

The ordering I would suggest: an admin API for membership and roles first (it
unblocks everything else and makes §4 demonstrable in the UI), then a real login,
then company switching.

---

## Appendix — reproducing every line

```bash
cd frontend
pnpm seed:demo -- --insightd     # -> company A
pnpm seed:tenancy <A>            # -> company B + the Data Entry user
pnpm verify:tenancy <A> <B>      # 27 checks; exits non-zero on any drift
pnpm verify:demo <A>             # 18 accounting checks; unaffected by the above
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
