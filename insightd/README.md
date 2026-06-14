# insightd

The AI insight plane — a read-only document-to-purchase-voucher **DRAFT**
pipeline, sibling to `backend/` (cored) and `frontend/`. insightd ingests an
Indian invoice (image/PDF), runs OCR → **PII redaction** → classification →
inference, and emits a `PurchaseVoucherDraft`: a ready-to-POST cored payload a
human reviews and submits via the existing frontend voucher API.

insightd has **NO ledger write path** and holds **no cored write credential** —
the human-in-the-loop boundary is structural, not a runtime check.

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

| Method | Path | Purpose |
|---|---|---|
| POST | `/companies/{id}/drafts` | run the pipeline, store + return a draft (the ONLY mutating route) |
| GET | `/companies/{id}/drafts/{draft_id}` | hydrate the F9 screen (RLS-scoped) |
| GET | `/healthz` | active adapter set per stage (fake\|real) |
| GET | `/benchmark/last` | most recent accuracy report (read-only) |

There is **no** endpoint that writes to the cored ledger.

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
