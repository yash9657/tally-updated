# insightd

The AI insight plane — a read-only document-to-purchase-voucher **DRAFT**
pipeline, sibling to `backend/` (cored) and `frontend/`. insightd ingests an
Indian invoice (image/PDF), runs OCR → **PII redaction** → classification →
inference, and emits a `PurchaseVoucherDraft`: a ready-to-POST cored payload a
human reviews and submits via the existing frontend voucher API.

insightd has **NO ledger write path** and holds **no cored write credential** —
the human-in-the-loop boundary is structural, not a runtime check.

## ⛔ HOW insightd IS REACHED: CORED IS THE ONLY DOOR

**Nothing but cored talks to this service** (register row D49, closed 2026-08-24).
A browser calls `/v1/companies/{id}/drafts…` on **cored**, which verifies the
caller's JWT, rejects a revoked session, requires membership in that company and
evaluates `(Display|Create, "purchase")` against the caller's security level —
then forwards the request here, presenting:

| Header | What it is |
|---|---|
| `X-Insightd-Service-Token` | a credential shared by **exactly two server processes**, cored and this one. It says "this came through the proxy" and nothing else. |
| `X-Insightd-Company` | the company **cored's PDP already authorized** for that caller. Every company-scoped route requires it to equal the `company_id` in the path. |

So the path `company_id` still scopes the store and the RLS GUC — but a caller can
only NAME a company somebody vouched for. Before this, the GUC was set from a value
an anonymous caller typed, which is why the Postgres RLS was never a second layer.

`INSIGHTD_SERVICE_TOKEN` **must be set on both processes** (the repo `Makefile`
exports a dev value to `run-cored` and `run-insightd`). With it unset, cored
refuses to forward and this service answers **503 "unconfigured"** on every
company-scoped route — it never serves them open.

⚠️ **This is not "a shared secret bolted on".** A secret the *browser* holds is not
a credential, and it would say nothing about which company the caller may read;
neither clause applies to a credential held only by two server processes for a
request whose company the PDP has already decided. `api/authn.py` carries that
argument in full, and `/healthz` states the posture on the wire.

⚠️ **The auto-docs are NOT mounted** (`openapi_url=None`): `/docs`, `/redoc`,
`/openapi.json` and `/docs/oauth2-redirect` all 404. They published the entire
schema to anyone who could open a socket, they never appear in `openapi.paths`
(which is why every count of this service's exposure missed them), and no runtime
client reads them. `create_app().openapi()` still builds the schema in-process.

## Pipeline

```
RawUpload → Ingest → OCR → PIIRedactor → (Classifier Haiku | Inferencer Opus) → VoucherDraftMapper → PurchaseVoucherDraft
                              └── the cross-border chokepoint: only redacted text crosses the border ──┘
```

Every stage is a Protocol with a deterministic **fake** (used by tests, zero
network/model/key) and a **real** adapter that lazy-imports its heavy dep
(`anthropic` / `paddleocr` / `presidio`) inside the method. `import insightd`
needs only `pydantic + fastapi + httpx + pypdf`.

The `VoucherDraftMapper` is **pure** and shared identically by the fake and real
pipelines — a green fake test proves the real mapping. GST amounts are NEVER
trusted from the LLM; they are re-derived against cored's `gst-2024-09` ruleset
for display, and cored re-derives them authoritatively on the human's POST.

## Layout

```
insightd/
  contracts/   draft.py (pinned wire contract), cored_map.py, pipeline.py
  interfaces/  base.py (6 Protocols + StageError)
  redaction/   recognizers.py (PAN/GSTIN/Aadhaar-Verhoeff/IFSC), boundary.py (fail-closed)
  adapters/    mapper.py + fakes.py (pure core); *_real/_paddle/_haiku/_opus/_presidio (lazy)
  pipeline.py  orchestrator (hard-wires PII-before-LLM ordering)
  api/         app.py, routes_drafts.py, store.py, deps.py
  benchmark/   corpus/, gen_corpus.py, validate.py, harness.py, report.py
  tests/
```

## Endpoints

**EIGHT**, enumerated rather than summarised — the count of this service's surface
has been written wrong three times. The first six are company-scoped and require
both proxy headers; `/benchmark/last` requires the service token alone (it names no
tenant, so there is no company to assert); `/healthz` is the **only** anonymous
route, and says so in its own `unauthenticated_routes` field.

| Method | Path | Purpose | Guard |
|---|---|---|---|
| POST | `/companies/{id}/drafts` | run the pipeline, store + return a draft (the only route that writes the DRAFT store) | `require_proxy` |
| GET | `/companies/{id}/drafts` | slim list for the F9 bill inbox | `require_proxy` |
| GET | `/companies/{id}/drafts/{draft_id}` | hydrate the F9 screen (RLS-scoped) | `require_proxy` |
| POST | `/companies/{id}/drafts/{draft_id}/claim` | RESERVE the draft for one poster, before any voucher exists | `require_proxy` |
| POST | `/companies/{id}/drafts/{draft_id}/claim/release` | give the claim back when the voucher POST failed | `require_proxy` |
| POST | `/companies/{id}/drafts/{draft_id}/accepted` | record WHICH cored voucher a human posted from this draft | `require_proxy` |
| GET | `/benchmark/last` | most recent accuracy report (read-only) | service token |
| GET | `/healthz` | adapter set, PII posture, and what a token is checked against | **none, deliberately** |

There is **no** endpoint that writes to the cored ledger.

⚠️ **The guard is on the drafts ROUTER, not on six decorators**, so a route added
to `routes_drafts.py` inherits it. And it is a route dependency and **not
middleware**, on purpose: routing must happen first, so a path this service does
not serve still answers **404** rather than 401 — `tests/test_api.py`'s
ledger-write-endpoint proof POSTs to a cored `/vouchers` path and observes that
nothing answers, and 401-before-routing would turn that proof into a statement
about headers.

⛔ **A claim token is not a credential.** It is an opaque concurrency value whose
only power is to release or renew the claim it already holds; these routes would be
exactly as safe if it were printed on a billboard.

## Run the tests (core only — no models/keys)

```sh
cd insightd
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt pytest
python -m insightd.benchmark.gen_corpus     # regenerate the labeled corpus
python -m pytest -q
```

CI installs `requirements.txt` (core only); the optional extras are pinned
separately in `requirements-ocr.txt` / `-llm.txt` / `-pii.txt`.

## Accuracy floor (gated in CI, runs on the deterministic fakes)

```
header_accuracy ≥ 0.96 · line_item_micro_f1 ≥ 0.92 · line_count ≥ 0.95
gst_reconciliation = 1.0 · pii_recall = 1.0 · degrade_recall = 1.0
```

`degrade_recall = 1.0` is the cardinal-sin gate: a degraded/adversarial fixture
that silently produces `status: "ready"` fails the build. A manual `--real`
calibration mode (paddle + anthropic) does NOT gate merges.

## Frontend handshake

The draft `body + confidence + unresolved + status` shape the frontend consumes
to prefill the F9 Purchase screen is frozen in
[`docs/frontend-draft-contract.md`](docs/frontend-draft-contract.md). Wiring the
actual F9 prefill is the explicit follow-up.
