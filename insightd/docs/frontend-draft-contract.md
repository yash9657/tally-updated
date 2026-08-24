# insightd → frontend F9-prefill draft contract (handshake)

This is the **contract** the frontend will consume to hydrate the F9 Purchase
screen from an insightd draft. Wiring the actual F9 prefill is the explicit
follow-up; this document freezes the shape so both sides can build in parallel.

## The boundary

insightd returns a `PurchaseVoucherDraft` and **stops**. It NEVER POSTs to
cored. The operator reviews the prefilled F9 form and presses **Ctrl+A**; the
EXISTING frontend voucher API (`frontend/src/api/rest.ts` → `POST
/v1/companies/{id}/vouchers/purchase`) submits under the human's identity, and
cored re-derives GST authoritatively and writes the immutable ledger entry.

Human-in-the-loop is therefore **structural**: there is no insightd endpoint
that writes the ledger.

## Fetching a draft

⛔ **THESE ARE CORED ROUTES, UNDER `/v1`** (register row D49, closed 2026-08-24).
The frontend never addresses insightd. It calls cored, which authenticates the
caller, checks membership, evaluates `(Display|Create, "purchase")` and forwards to
insightd with the company its PDP authorized:

```
GET  /v1/companies/{company_id}/drafts                      ->  DraftSummary[]
GET  /v1/companies/{company_id}/drafts/{draft_id}           ->  PurchaseVoucherDraft
POST /v1/companies/{company_id}/drafts                      ->  PurchaseVoucherDraft (201)
POST /v1/companies/{company_id}/drafts/{draft_id}/claim           (the accept lifecycle)
POST /v1/companies/{company_id}/drafts/{draft_id}/claim/release
POST /v1/companies/{company_id}/drafts/{draft_id}/accepted
```

⚠️ **NOT `/companies/…`, and the difference is not cosmetic.** vite used to proxy
that whole prefix to insightd, so a cored path written without `/v1` was delivered
to the wrong process — a mistake that looks like nothing happening in the browser
while every unit test passes. That proxy rule is gone; `/v1` is the only prefix
cored answers.

✅ **THE BEARER ARRIVES ON ITS OWN.** `createInsightdClient()` with no arguments
resolves cored's base URL and picks up the session transport the auth gate
installed (`api/http.ts`'s `setSessionFetch`, resolved per request). A caller does
not pass a credential, and `VITE_INSIGHTD_BASE_URL` no longer exists.

⛔ **AND A 409 MUST REACH THE CLIENT UNCHANGED.** cored passes insightd's answer
back byte for byte — status, content type and bytes — because `draftAccept.ts`'s
`conflictCode` reads `err.body.detail.code` and **only** for status 409, and every
insightd-backed layer of the duplicate-bill guard fails OPEN on anything else. A
proxy that reshaped that body into cored's own `{"error": …}` form would turn three
specific refusals into the fail-open branch, and the accept would still succeed.

Tenant scoping: a `draft_id` from another `company_id` returns 404, and a caller
who is not a member of `{company_id}` never reaches insightd at all (cored answers
403).

## What the frontend reads

### 1. `body` — the ready-to-POST payload

`body` is byte-compatible with cored's `VoucherRequest` (mirrors
`frontend/src/api/types.ts`). The F9 form prefills directly from it:

| F9 field | draft path | notes |
|---|---|---|
| Party ledger | `body.party_ledger_id` | `null` ⇒ unmatched supplier — open Alt+C on-the-fly create using `body.party` as the proposed name |
| Supplier ref (voucher no) | `body.voucher_no` | supplier's invoice number (Purchase is not auto-numbered) |
| Date | `body.voucher_date` | |
| Narration | `body.narration` | |
| Postings | `body.postings[]` | `{ledger_id, ledger_name, dr_cr: "D"\|"C", amount}` — a `ledger_id` of `null` means a proposed new master (Alt+C); `ledger_name` carries it |
| GST block | `body.gst` | `{ruleset_version, supplier_state_code, place_of_supply_state_code, reverse_charge, exempt, lines[], cgst/sgst/igst_ledger_id}` |
| GST lines | `body.gst.lines[]` | `{taxable_value, gst_rate_pct, hsn}` — the LLM supplied taxable_value + rate only; cored re-derives the tax amounts on submit |

Money / quantities / rates are **fixed-4 decimal STRINGS** (e.g. `"1180.0000"`),
never JS numbers. State codes are 2-digit GST strings.

### 2. `confidence` — amber rendering

```ts
confidence: {
  header: { [field: string]: { value_present, confidence (0..1), source } }
  line_items: Array<{ [field: string]: FieldConfidence }>
  overall_header: number
  overall_line_items: number
  overall: number   // == min(overall_header, overall_line_items)
}
```

Render any field whose `confidence` is below the amber threshold (suggested
**0.80**, matching insightd's per-field floor) in amber. `source` ∈
`ocr | classifier | inferencer | derived | default` is available for tooltips.

### 3. `unresolved` — focus order

`unresolved: string[]` lists the fields needing human attention, **in the order
the form should focus them**. Header keys are bare (`"party"`,
`"place_of_supply"`); line fields are `"line[<i>].<field>"` (e.g.
`"line[0].ledger"`). Drive the F9 tab/focus order from this list.

### 4. `status` — form mode

| `status` | frontend behavior |
|---|---|
| `"ready"` | every field cleared its floor; open accept-ready, Ctrl+A is a single keystroke |
| `"low_confidence_manual"` | open the form blank-but-prefilled for full manual review — do NOT present as accept-ready |

### 5. `derived_gst` — display only

```ts
derived_gst: { cgst, sgst, igst, total_tax, invoice_total }  // fixed-4 strings
```

insightd re-derives these against cored's `gst-2024-09` ruleset for **display
only**. Show them in the GST preview, but the human's POST re-derives them
server-side authoritatively — never treat `derived_gst` as the value to submit.

### 6. `redaction` — proof, counts only

```ts
redaction: { any_redacted: boolean, counts_by_type: {GSTIN, PAN, ...}, total }
```

Counts only — proves PII was scrubbed before any cross-border call. No raw
values ever appear in the draft.

## Submit (unchanged, existing path)

The frontend does NOT call insightd to submit. On Ctrl+A it POSTs `body`
(possibly edited by the operator) via the existing `rest.ts` →
`POST /v1/companies/{id}/vouchers/purchase`. cored re-derives GST and writes the
ledger entry attributed to the human.
