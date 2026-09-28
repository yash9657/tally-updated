# D123 — W5, the no-model-keys half: bank statements, validators, provenance, idempotency

Date: 2026-09-28. Branch `agentic/w5` (from `session/t32-and-close-7-3` @ `c51c0a0`).
Driver: `PLAN-AGENTIC.md` §3 W5. Migrations **0100–0101** (`PLAN-TALLY-PARITY.md` §2.5 row updated).

Every figure below was printed by a command in this session; the commands are in §7.

## 1. Scope — what this lane could and could not build

W5 as planned is PaddleOCR-VL on an India GPU, open-weight VLMs replacing Haiku/Opus, Docling,
exception-only review, and the gate "field-level F1 ≥ the current Opus pipeline". **None of the
model-backed half is buildable here: there are no provider keys and no GPU.** This lane built the
half that needs neither, and built it with gates:

| # | Deliverable | Where | Status |
|---|---|---|---|
| 1 | Bank export layouts (HDFC/ICICI/SBI/Axis/Kotak), narration parsing, **mandatory balance proof** | `backend/internal/bankstatement/{layout,summary,proof,narration,profiles}.go`, `service.go`, migration 0101 | BUILT |
| 2 | Deterministic validators deciding confidence (GSTIN checksum, HSN/SAC, totals, duplicates) | `insightd/insightd/validators/` | BUILT (HSN directory OWED, §6) |
| 3 | Per-field provenance `{value, page, bbox, confidence}` into the draft | `insightd/insightd/provenance.py`, `contracts/draft.py`, `frontend/src/api/draft.ts` | BUILT (schema + passthrough; Bill Inbox RENDERING owed) |
| 4 | Idempotency keys on voucher create — row 30's duplicate-post class | `backend/internal/ledger/idempotency.go`, `server.go`, migration 0100, `draftAccept.ts` | BUILT |
| 5 | Open-weight extraction adapter (OpenAI-compatible `json_schema`) | `insightd/insightd/adapters/inferencer_openai_compat.py` | BUILT, **not the default** |

## 2. Bank statements

### 2.1 The reader (`layout.go`)
The header row is found **by what it says** (a date, a narration and a money column, per
`propose.go`'s synonyms) within the first 40 rows; the delimiter is the one of `, \t ; |` that yields
such a header; the preamble is kept for the summary reader; separator rows under the header
(HDFC's `********`) are dropped. The footer begins only at a blank/separator row **after which no
row carries a parseable date** — a trailing row with a malformed date is refused, not swept into
the footer (it would be a dropped last row, invisible to the walk). XLS/XLSX/PDF/HTML/MT940 are
refused by magic bytes, by name. A UTF-8 BOM is stripped.

Two latent slice-2a defects surfaced and were fixed:
* `encoding/csv` with `TrimLeadingSpace` **collapses empty cells under a tab delimiter** — SBI's
  empty Debit cell shifted every later column left (found by the SBI fixture).
* A **repeated header resolved to its last occurrence** silently; Kotak's two `Dr / Cr` columns read
  the amount's direction off the balance flag. Repeated headers are now renamed `Name (2)`.
* Balance cells now read `Dr` as a **sign** (`parseBalance`): an overdrawn account's walk used to
  fail on a correct file. Amount cells keep `parseAmount` (suffix = restated direction).

### 2.2 Bank signatures (`profiles.go`) — a bounded departure from t4-slice2-model.md §8.2
§8.2 forbade built-in profiles because a column **order** recalled wrongly reads the balance as the
amount. These signatures state **header names, never positions**, match only when **every** name is
present (a wrong recollection yields "not recognised", never a mis-read), leave the **date layout**
to the file's own dates (rule 3), and sit behind the mandatory proof. ⚠️ **No real export from any
of the five banks exists in this environment**: all five ship `Verified: false`, and the proposal
says so in its notes. Flip one only with a test built from a real file's header.

### 2.3 The proof (`proof.go`, `summary.go`) — now mandatory
`ParseStatement` refuses (`ErrNoBalanceProof`) a statement with neither a running balance on every
row nor a declared opening **and** closing. Declared figures come from the file's own
preamble/footer (read by exact label; two different values for one label refuse) or from the
operator (`declared_opening_balance` / `declared_closing_balance`, decimal strings). Checks:
running walk (**forward, else reverse** — newest-first is discovered from the balances, never the
dates); `opening + deposits − withdrawals = closing`; when both exist, **the walk must start and end
on the declared figures** — the only check that sees a missing first/last row (pinned by
`TestAMissingFirstRowWalksButFailsAgainstTheDeclaredOpening`, whose control shows the walk alone
passes); the file's own debit/credit totals and counts when printed. Zero tolerance throughout.
0101 stores `balance_proof`, `balance_declared_by`, `row_order`; the read model no longer infers
"walked" from `opening_balance IS NOT NULL`, which a declared-only import now sets.

⚠️ **Behaviour change:** slice 2a imported a no-balance file with a disclosure; it is now refused
unless the operator keys the printed balances (the panel has the two inputs). Existing integration
fixtures without balances were given tautological declared figures (`declaredFor`, commented as
such) because they test matching, not proof.

### 2.4 Narration (`narration.go`)
Rail by whole-token keyword (UPI, NEFT, IMPS, RTGS, NACH/ACH/ECS, cheque clearing, ATM, card,
interest, charges); identifiers by rail format (VPA, 12-digit RRN, IFSC, UTR, 6-digit cheque no);
counterparty = first **segment** (split on `/ - *`) that reads as a name. **An annotation only** —
never an input to amount, direction, date or clearance, and excluded from `line_hash` (pinned).
Stored in 0101's four nullable columns. Known limit: a VPA handle containing `-` loses its prefix.

## 3. Validators and provenance (insightd)

**One-way rule:** a `fail` zeroes the named field's confidence and adds it to `unresolved`; `pass`
never raises anything. Outcomes: `pass | fail | unverified | not_run`, carried on
`PurchaseVoucherDraft.validations` (default `[]`, so pre-W5 rows rehydrate — pinned).

* **GSTIN** — mod-36 check character, computed on the **raw OCR before the PII boundary**, emitting
  counts only. Verified against GSTN's documented `27AAPFU0939F1ZV`. ⛔ **The benchmark corpus's
  GSTIN `27ABCDE1234F1Z5` had an invalid check character** (correct: `…Z0`); a validator passing it
  would check nothing, one failing it made every clean bill a review. The corpus (25 JSON + gen) was
  corrected (all 28 corpus JSON files, `gen_corpus.py`, one `test_api.py` assertion); the accuracy
  floor is unchanged (insightd 166 → 166 passed before the new tests were added).
* **HSN/SAC** — structure (HSN 4/6/8 digits, chapter 01–98 not 77; SAC 99 + 4 or 6 digits) and the
  tenant's own history (`TenantContext.known_hsn`). ⛔ **No HSN directory exists in the repo and none
  was invented** (FEATURE-LOCK §1.1); a valid code not in history is `unverified`, never `fail`.
  My first rule said SAC is 6 digits only and failed every services bill in the corpus — the corpus
  prints the 4-digit heading; corrected.
* **Totals** — lines sum to stated taxable; each **stated** tax component equals rate × taxable;
  taxable + tax ± round-off = total, where round-off is a **rule** (stated ≤ 0.50 making it exact,
  or implied only when the printed total is a whole rupee equal to the derived total rounded
  half-up). ⚠️ The mapper's existing exact-total gate still decides status: a rupee-rounded bill
  still goes to review, because the draft has **no round-off posting leg** and would credit the
  party 1,180.37 against a bill of 1,180.00. The leg is OWED.
* **Duplicates** — supplier (ledger id, else folded name) + post-redaction invoice number + date +
  derived total against the company's other drafts, on the create route (it needs the store).
  Numberless matches are flagged "POSSIBLE". Flags only; never merges or hides.
* **Provenance** — literal search of the **redacted** OCR: money as a number (lakh commas
  stripped; a token glued by `:`/`/`/`.`/`-` to another digit run is not a token — the first draft
  let OCR `300:00` ground `300.00`, the exact WS10 misread; the test caught it), dates in printed
  renderings of the one ISO date, text as whole tokens. A model-claimed location is carried as
  `source="model"`, ungrounded. An ungrounded **total** is a `fail` only when the document prints a
  total label; otherwise `unverified` (the synthetic corpus OCR prints no total line).
  Provenance values are the persisted (guarded) values, re-scanned for PII.

## 4. Idempotency keys — closing row 30's class

Row 30 (D15, §7.3 B) residue per `draftAccept.ts`'s "RESIDUAL WINDOW": **no draft id resolvable**
and **an ambiguous timeout** (retry with a draft id is answered 409 for a request that succeeded).
Hand-keyed double submit (D108) had no server identity either.

* `VoucherInput.IdempotencyKey`, wire `idempotency_key` **or** `Idempotency-Key` header (disagreeing
  → 400). Honoured on CREATE only.
* Fingerprint = sha256 of the input **before** post() normalises anything.
* Known key → first result replayed (`idempotent_replay: true`, header `Idempotent-Replayed`),
  **no work done**. Same key, different body → 422 `idempotency_key_reused`, never replayed.
* The key row is the **last write of the post transaction**; a concurrent delivery blocks on 0100's
  PK, trips it, rolls back everything and replays the winner. A failed post consumes no key.
* When 0077's draft index fires first (concurrent deliveries of one draft accept), the loser checks
  whether the winner holds **this key and content** and replays — found only because mutation M3
  (removing the pre-check) turned a draft retry into `DraftAlreadyAccepted`.
* 0100 is INSERT-only by trigger **and** grants; the app role's UPDATE/DELETE is refused (pinned).
* Client: `draftAccept.ts` layer 7 sends `billInboxIdempotencyKey(req)` — a **deterministic,
  synchronous** content hash, so a reload or another browser lands on the same key. A collision
  can only cost a 422 (the server compares its own sha256), never a wrong replay.

## 5. The open-weight adapter
`OpenAICompatInferencer`: `POST {base}/chat/completions` with `response_format.type=json_schema`
(`strict`, schema from `InferredInvoice`), `temperature 0`; `assert_clean` on the whole payload
first (and on each repair); bounded repair (default 1) naming field+type only; provider error bodies
never quoted; key read from the env var named in settings. Selected only by
`use_real_inferencer=true` **and** `inferencer_backend="openai_compat"`; the default stays
`opus` (and fakes), because W1b has no numbers. Tested by 3 **synthetic** cassettes
(`tests/fixtures/openweight/`, `"synthetic": true`), each replay re-deriving the request from the
corpus input and checking its recorded sha256; the census of cassettes/responses is pinned exactly
(3 cassettes / 5 responses). No new Python dependency (httpx is core).

## 6. OWED — stated, not buried
1. **Model-backed W5** — PaddleOCR-VL, the VLM image path, Docling, and the F1 gate. Needs keys/GPU.
2. **HSN/SAC directory** as versioned reference data; `known_hsn` is not yet sent by the Bill Inbox.
3. **Round-off posting leg** in the draft body, so a rupee-rounded bill can be `ready`.
4. **Bill Inbox rendering** of validations/provenance (types pass through cored's proxy byte-for-byte;
   nothing on screen draws the box or the field count yet).
5. **Hand-keyed vouchers** (VoucherScreen Ctrl+A, stock screens, offline outbox) do not yet send a
   key; the server path is ready for them. Mock client does not model replay.
6. **Retention** of `voucher_idempotency` rows (longer than any outbox retry horizon).
7. **Bank signatures unverified** against real exports; no real statement fixtures in the repo.
8. Matcher does not yet use `rail_reference`/`counterparty`; statement lines UI does not show them.
9. Every-bank-labelled "top-10" — five built; the other five need real headers first.
10. Adversarial review pass (PLAN-AGENTIC §2.8) not run by this lane.

## 7. Gates and mutation proofs (all printed in this session)
See the lane report for exact commands. Summary: `go.mod/go.sum` diff 0 lines;
frontend `package.json` / insightd `requirements.txt` diff 0 lines; vitest **122 files / 1960
tests** (baseline 1954); insightd **217 passed** (baseline 166); `make tier0` ok; full
`go test ./... -count=1` **exit 0, 39 packages ok** (baseline 39); all DB tests run
against a private `tally_w5` database via the new `TESTDB_APP_DSN` (+ existing `TESTDB_ADMIN_DSN`).

Mutations run (each observed failing, then restored):
* M1 skip the key-row insert → 5 ledger idempotency tests fail ("left 2 vouchers", "8 … left 8").
* M2 skip the fingerprint comparison → reuse test fails (`got <nil>`).
* M3 skip the pre-check → only the draft-retry test failed; fixed by the draft-conflict replay
  branch, after which M3 passes (the in-transaction key is the guarantee, the pre-check an
  optimisation).
* M5 remove the draft-conflict replay branch → concurrent draft test fails (5/6 deliveries refused).
* F1 frontend: drop `req.idempotency_key` → 2 draftAccept tests fail.
* P2 mapper ignores failed validations → GSTIN-misread and bad-HSN tests fail.
* P3 duplicate flag leaves status ready → duplicate test fails.
* P4 open-weight adapter without `assert_clean` → raw-PII test fails.
* Bank: the Kotak control (no balance flag → walk refused), the missing-first-row control (walk
  alone passes), and the lookalike narration (`NEFTY`) are in-test controls; the `300:00` grounding
  bug and the SAC and tab-delimiter bugs were found by the tests, not by review.
