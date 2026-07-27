# WS10 — PaddleOCR feasibility spike

**Date:** 2026-07-26 · **Machine:** darwin 24.6.0, arm64 (Apple silicon), Python 3.13.5
**Plan item:** `IMPLEMENTATION_PLAN.md` WS10 / `HANDOFF.md` §6 + §10 item I — the only unshipped plan item.
**Status of this document:** spike report only. **Nothing was wired.** No file under
`insightd/insightd/`, `backend/`, or `frontend/` was touched; nothing was installed into
`insightd/.venv`. All spike artefacts live in `/tmp`.

---

## 1. The question

Wheel availability was already confirmed (`paddlepaddle 3.3.1`, `paddleocr 3.7.0` resolve on the
index). Wheel availability was **not** the question. The two open questions were:

1. Does a model actually **download** on this machine, and where does it cache?
2. Does inference then run **with no network at all** from that warm cache?

And, as a gating third question that the plan implies but does not state:

3. Is the text that comes back good enough that the insightd mapper's fields are recoverable,
   or is it noise?

---

## 2. The contract a real adapter has to satisfy

From `insightd/insightd/contracts/pipeline.py`, `adapters/ocr_paddle.py` and `adapters/fakes.py`.
The OCR stage is one method:

```
extract(doc: NormalizedDoc) -> OCRResult
```

**In —** `NormalizedDoc`: `pages: list[PageImage]` where `PageImage = {png_b64, width, height, dpi}`
(base64 PNG bytes, so the model stays JSON-safe), plus `page_count`, `source`,
`source_meta: dict[str,str]`, and an optional `tenant: TenantContext`.

**Out —** `OCRResult`:

| field | type | note |
|---|---|---|
| `pages` | `list[OCRPage]`, `OCRPage = {lines: list[OCRLine]}` | one per input page |
| `lines[i]` | `OCRLine = {text: str, bbox: list[float], confidence: float}` | `bbox` is `[x0,y0,x1,y1]`; `confidence` is 0..1 |
| `full_text` | `str` | newline-joined line texts |
| `mean_confidence` | `float` | mean of per-line confidences |
| `tenant`, `source` | carried through | downstream stages need them |

All models are pydantic with `extra="forbid"`. Everything downstream (PII boundary → classifier →
inferencer → mapper) consumes only `full_text` + the per-line `text`/`bbox`/`confidence`.
`FakeOCRExtractor` satisfies the same signature by replaying a corpus fixture's canned lines and
honouring an `ocr_quality: 'clean'|'degraded'` knob. **The seam is narrow and the contract is easy
to satisfy — the risk was never the interface, it was the runtime.**

---

## 3. Method

1. Throwaway venv at `/tmp/paddlespike-venv`; `pip install paddlepaddle paddleocr`.
2. Rendered two GST purchase bills with Pillow (`/tmp/render_bills.py`), with field values lifted
   from `insightd/insightd/benchmark/corpus/intra-state-18-1line-clean.json` and
   `inter-state-12-1line-clean.json` so what is recoverable maps 1:1 onto what the mapper wants:
   - `/tmp/bill_clean_intra18.png` — clean 1240×1754 render, MH→MH, CGST+SGST @9%+9%.
   - `/tmp/bill_hard_inter12.jpg` — **deliberately degraded**: −2.6° rotation, Gaussian blur 0.9,
     2% salt-and-pepper noise, grey/uneven-lighting composite, saved as JPEG quality 42. MH→KA, IGST @12%.
     A clean synthetic render alone would have flattered the model.
3. Ran OCR online, recording model download, cache location, and first-vs-second call latency.
4. Re-ran the **identical script** inside a macOS seatbelt jail that denies all networking, with
   the block proven from inside the jail before inference was attempted.
5. Ran a dumb regex/keyword field-recovery probe over the raw offline OCR lines.

Scripts: `/tmp/render_bills.py`, `/tmp/spike_ocr.py`, `/tmp/offline_run.sh`, `/tmp/nonet.sb`,
`/tmp/field_recovery.py`. Logs: `/tmp/paddle_install.log`, `/tmp/ocr_online.log`,
`/tmp/offline_run.log`. Raw OCR JSON: `/tmp/ocr_online.json`, `/tmp/ocr_offline.json`.

---

## 4. Raw observations

### 4.1 Install

```
Successfully installed ... opencv-contrib-python-4.10.0.84 numpy-2.3.5 pandas-3.0.5
  paddleocr-3.7.0 paddlepaddle-3.3.1 paddlex-3.7.2 pydantic-2.13.4 ... (63 packages)
EXIT=0
```

- **Wall clock: 19 s** — but **that number is not representative**: this machine already had every
  wheel in pip's HTTP cache from the 2026-07-23 probe (`Using cached …` on every line). The honest
  cold-machine figure is the download volume, measured separately:
  `paddlepaddle 104.5 MB` + `opencv-contrib-python 63.7 MB` + `paddlex 2.2 MB` + `paddleocr 0.15 MB`
  ≈ **163 MB of primary wheels**, plus the rest of a 63-package closure.
- **`du -sh /tmp/paddlespike-venv` → 933 MB.**
- 63 transitive packages, including `pandas`, `opencv-contrib-python`, `shapely`, `huggingface-hub`,
  `modelscope`, `aiohttp`, `httpx`, and its own **`pydantic 2.13.4`**.

### 4.2 Model download and cache location

First construction downloaded three models from **huggingface.co** (`PaddlePaddle/…` repos), logged verbatim:

```
Creating model: ('PP-LCNet_x1_0_textline_ori', None, None)
Checking connectivity to the model hosters, this may take a while. To bypass this check,
  set `PADDLE_PDX_DISABLE_MODEL_SOURCE_CHECK` to `True`.
Using official model (PP-LCNet_x1_0_textline_ori), the model files will be automatically
  downloaded and saved in `/Users/yashbhalgat/.paddlex/official_models/PP-LCNet_x1_0_textline_ori`.
HTTP Request: GET https://huggingface.co/api/models/PaddlePaddle/PP-LCNet_x1_0_textline_ori/... "HTTP/1.1 200 OK"
Creating model: ('PP-OCRv6_medium_det', None, None)
Creating model: ('PP-OCRv6_medium_rec', None, None)
```

- **Cache location: `~/.paddlex/official_models/<ModelName>/`** (not the venv — it is a per-user
  directory that survives venv deletion, and it is `$HOME`-relative, so a container needs it baked in
  or mounted).
- **139 MB** for the minimal set (`PP-OCRv6_medium_det`, `PP-OCRv6_medium_rec`, `PP-LCNet_x1_0_textline_ori`).
- **177 MB** once the *default* constructor's doc-preprocessor models (`PP-LCNet_x1_0_doc_ori`, `UVDoc`)
  are added — see §4.6, this distinction is load-bearing.

### 4.3 Latency

| step | cold (first ever) | warm |
|---|---|---|
| `from paddleocr import PaddleOCR` | **22.0 s** | 1.8 s |
| constructor (minimal model set, incl. download) | **18.5 s** | **0.45 s** offline |
| constructor (default set, +2 models to download) | 9.9 s | 1.66 s offline |
| `predict()` per image | 14.8 s | 14.6 s |

**Per-image inference is ~14–16 s and does not improve on the second call** — the cost is in the
per-image forward pass, not in warm-up. Re-running with `use_textline_orientation=False` changed
nothing (14.2 s / 14.0 s), so the orientation head is not the culprit. This is a **pure-CPU
`paddlepaddle` wheel**; there is no Metal/GPU acceleration in play.

### 4.4 The decisive test — offline

Block: macOS seatbelt, `/usr/bin/sandbox-exec -f /tmp/nonet.sb` with `(allow default) (deny network*)`.
The block was **proven from inside the same jail, in the same script, immediately before inference**:

```
=== PROOF A: direct-IP TCP connect (bypasses DNS) to huggingface.co IP 3.164.110.49:443
curl_direct_ip_exit=7                          # couldn't connect to host

=== PROOF B: pip download inside the jail (must fail)
WARNING: Retrying ... Failed to establish a new connection: [Errno 8] nodename nor servname provided
ERROR: Could not find a version that satisfies the requirement six (from versions: none)
pip_exit=1

=== PROOF C: python sockets inside the jail
  DNS huggingface.co        -> BLOCKED: gaierror: [Errno 8] nodename nor servname provided, or not known
  raw TCP 3.164.110.49:443  -> BLOCKED: PermissionError: [Errno 1] Operation not permitted
```

Control: the identical `curl` **outside** the jail returned `200`. Proof C is the one that matters —
`PermissionError: Operation not permitted` on a raw TCP connect to a literal IP is the **kernel**
refusing the syscall, not a DNS failure and not an env-var the library could ignore. A proxy-env
block would have been weaker (the `hf-xet` Rust downloader may not honour `http_proxy`); seatbelt
covers every library in the process.

Result, inside that jail:

```
Model files already exist. Using cached files. ... `/Users/yashbhalgat/.paddlex/official_models/PP-OCRv6_medium_det`
CTOR_SECONDS=0.45
PREDICT img=bill_clean_intra18.png run=1 seconds=15.88   run=2 seconds=14.80
PREDICT img=bill_hard_inter12.jpg  run=1 seconds=14.57   run=2 seconds=14.69
OFFLINE_INFERENCE_EXIT=0
```

**Offline inference works, and the output is byte-identical to the online run** — 47/47 and 44/44
recognised lines, same strings in the same order on both images. No "checking connectivity" line is
emitted offline: PaddleX only reaches out for a model it does not already have cached.

### 4.5 What text actually comes back (verbatim)

`/tmp/bill_clean_intra18.png` — 47 lines, mean confidence **0.9982**:

```
0.999  TAX INVOICE
1.000  Acme Components Pvt Ltd
0.991  Plot 14, MIDC Industrial Area, Pune, Maharashtra 411018
0.993  GSTIN: 27ABCDE1234F1Z5
0.997  PAN: ABCDE1234F
0.989  State: Maharashtra Code: 27
1.000  Invoice No:
1.000  INV-2025-0042
0.970  Invoice Date:
1.000  11-05-2025
1.000  Place of Supply:
1.000  Maharashtra (27)
1.000  Steel widgets
1.000  8471
1.000  2.000
1.000  500.00
1.000  1000.00
1.000  Taxable Value
1.000  1000.00
0.999  CGST 9.00%
1.000  90.00
0.992  SGST 9.00%
1.000  90.00
1.000  Invoice Total
1.000  1180.00
0.999  Bank: HDFC Bank A/c: 50200012345678 IFSC: HDFC0001234
```

`/tmp/bill_hard_inter12.jpg` (rotated + blurred + noised + JPEG-42) — 44 lines, mean confidence **0.9968**:

```
0.995  TAX INVOICE
1.000  Nandi Systems Private Limited
0.989  No. 42, 3rd Cross, Peenya Industrial Estate, Bengaluru 560058
0.981  GSTIN: 29AABCN5678M1ZK
0.997  PAN:AABCN5678M                      <-- lost the space after the colon
0.990  State: Karnataka Code: 29
0.996  Invoice No:
1.000  INV-2025-0042
1.000  Invoice Date                        <-- lost the trailing colon
1.000  11-05-2025
1.000  Karnataka (29)
1.000  Imported module
1.000  8473
1.000  4.000
1.000  250.00
1.000  1000.00
0.999  IGST 12.00%
1.000  120.00
0.999  Invoice Total
1.000  1120.00
```

The degradation cost **two punctuation characters across 44 lines**. No digit, GSTIN character,
amount or date was misread on either image. `GSTIN: 29AABCN5678M1ZK` survived rotation+blur+JPEG at
0.981 confidence — that is the single hardest string on the page and it came back exact.

`predict()` also returns `dt_polys` / `rec_polys` / `rec_boxes` (verified offline:
`poly0 [[68,63],[350,63],[350,101],[68,101]] -> 'TAX INVOICE'`) and accepts a numpy array directly,
so `OCRLine.bbox` and `confidence` are both trivially fillable from the real result.

### 4.6 The one genuine offline landmine

The **default** constructor also loads a document-preprocessor (`PP-LCNet_x1_0_doc_ori` orientation +
`UVDoc` unwarping) that the minimal path never downloaded. Constructing it offline against the
partially-warm cache failed hard:

```
PaddleOCR(use_angle_cls=True, lang='en')
  -> Exception: No available model hosting platforms detected. Please check your network connection.
```

After pre-warming those two models online (cache 139 MB → **177 MB**), the same default constructor
ran fully offline (`DEFAULT_CTOR_OFFLINE=1.66`, both images OCR'd, 47 and 44 lines). So this is not a
blocker, but it is a real deployment rule: **whatever pipeline configuration production uses must be
the exact one that was pre-warmed.** Adding a preprocessor flag later silently reintroduces a network
dependency at boot. (Side note: with UVDoc unwarping on, the harder image degraded slightly —
`Nandi Systems PrivateLimited`, a lost space — so the preprocessor is not obviously worth its 38 MB.)

### 4.7 `adapters/ocr_paddle.py` as written does not run on paddleocr 3.7.0

The existing seam was written against the 2.x API. Both of its calls fail, observed verbatim:

```
PaddleOCR(use_angle_cls=True, lang=self._lang, show_log=False)
  -> ValueError: Unknown argument: show_log

engine.ocr(arr, cls=True)
  -> TypeError: PaddleOCR.predict() got an unexpected keyword argument 'cls'
```

And the result shape changed: 3.x returns a list of dict-like `OCRResult` objects with parallel
`rec_texts` / `rec_scores` / `rec_polys` arrays, **not** the 2.x `[[[box, (text, conf)], …]]` nesting
that lines 47–55 of the adapter unpack. The adapter's unpacking loop would raise. Its *structure*
(lazy import, PIL→ndarray, per-line bbox/conf, mean confidence) is right; roughly 20 lines of the
body need rewriting.

### 4.8 Field recovery — could the mapper actually use this?

I ran a deliberately dumb regex/keyword extractor (`/tmp/field_recovery.py`) over the **raw offline
OCR lines** — no LLM, no layout model — against the 14 things the mapper needs.

**`bill_clean_intra18.png`: 14/14. `bill_hard_inter12.jpg`: 14/14.**

| field | clean | hard |
|---|---|---|
| supplier name | `Acme Components Pvt Ltd` | `Nandi Systems Private Limited` |
| supplier GSTIN | `27ABCDE1234F1Z5` | `29AABCN5678M1ZK` |
| invoice no | `INV-2025-0042` | `INV-2025-0042` |
| invoice date | `11-05-2025` | `11-05-2025` |
| place-of-supply state code | `27` | `29` |
| reverse charge | label + `No` present | label + `No` present |
| line description | `Steel widgets` | `Imported module` |
| HSN | `8471` | `8473` |
| qty / rate | `2.000` / `500.00` | `4.000` / `250.00` |
| taxable value | `1000.00` | `1000.00` |
| GST rate(s) | `['9.00','9.00']` | `['12.00']` |
| tax amounts | `[('CGST','90.00'),('SGST','90.00')]` | `[('IGST','120.00')]` |
| invoice total | `1180.00` | `1120.00` |

**Skeptical caveats on that 14/14, stated honestly:**

- **The line-item *description* is the one field my probe cheated on** — it matched against a
  hardcoded whitelist of the two known strings. The text is unambiguously present and verbatim
  (both at confidence 1.000), but a *general* description extractor cannot get it from flat line
  order; it needs row-grouping by `dt_polys` y-coordinate, or an LLM. Everything else in the table
  came from generic regexes that carry to an unseen bill.
- **Reading order scrambles on the rotated image.** Detected lines come back in detector order, and
  a −2.6° skew interleaves the two header columns and pushes `Imported module` above `Amount`:
  `Invoice Date` / `Reverse Charge:` / `No` / `Road` appear out of visual sequence. Feeding
  `full_text` to an LLM is unaffected; a positional parser would break. The bboxes are there to fix
  this, but somebody has to write that.
- **Table cells arrive as separate lines**, not as rows — `Steel widgets`, `8471`, `2.000`,
  `500.00`, `1000.00` are five independent detections. Multi-line invoices need real row assembly.
- **These are my own renders.** Uniform Arial, no logo, no stamp, no colour, no rubber-stamp overprint,
  no fold shadow, no handwriting, no Devanagari, no thermal-printer smear, no perspective from a
  phone camera held at an angle. Real Indian supplier bills are *much* worse than a −2.6° rotate and
  2% noise. **This spike proves PaddleOCR is not a blocker; it does not prove any accuracy number
  on real bills.**
- **`mean_confidence` is ~0.998 on both**, including the degraded one. That is a warning:
  the corpus's `ocr_quality: 'degraded'` fixtures pin `mean_confidence ≤ 0.55`, and real PaddleOCR
  confidence will **not** reproduce that. Any downstream gate that keys on `mean_confidence` to
  route a bill to human review is effectively dead on the real path — PaddleOCR is confidently
  wrong rather than unconfidently wrong.

---

## 5. Verdict

**Positive — with caveats. Recommendation: WIRE IT, behind the flag, as a non-core optional extra.**

Both open questions resolve yes:

- Models **do** download (177 MB into `~/.paddlex/official_models`, from huggingface.co).
- Inference **does** run with the network denied at kernel level, producing **byte-identical** output
  to the online run, with the block proven from inside the same jail.

Extraction quality is far better than needed to be worth wiring: on a deliberately degraded bill,
every GSTIN character, digit, amount and date was read exactly, and 14/14 of the mapper's fields
were recoverable from raw text.

The caveats that must be carried into the wiring, none of which are blockers:

1. **`adapters/ocr_paddle.py` does not run as written** (§4.7) — 2.x API, needs ~20 lines rewritten.
2. **~14–16 s per image on CPU** — this is a background-job latency, not a request-response latency.
3. **900+ MB venv / ~163 MB of wheels / 177 MB of models** — must never touch `requirements.txt`.
4. **The pre-warmed model set must exactly match the production pipeline config** (§4.6).
5. **Real PaddleOCR confidence is ~0.998 even on bad scans** — the `ocr_quality`/`mean_confidence`
   review-routing signal does not transfer to the real path.

---

## 6. What wiring it would actually entail

**Do not add paddle to `requirements.txt`.** The 79-test core suite passing with zero heavy deps is
load-bearing; `PaddleOCRExtractor` already lazy-imports, and that property must be preserved and
tested (an existing test should assert `paddleocr` is not importable in the core env, or at least
that the core suite never imports it).

1. **`requirements-ocr.txt`** (new, optional): `paddlepaddle==3.3.1`, `paddleocr==3.7.0`.
   Never referenced by the core suite or by `insightd/.venv`.
2. **Rewrite the body of `adapters/ocr_paddle.py`** for the 3.x API:
   - constructor: drop `show_log`; use
     `PaddleOCR(lang=…, use_doc_orientation_classify=False, use_doc_unwarping=False, use_textline_orientation=True)`
     — the explicit-minimal set, which is the 139 MB pre-warm and is *better* on the degraded image
     than the default UVDoc path.
   - inference: `engine.predict(arr)` (no `cls=`), then zip `rec_texts` / `rec_scores` / `rec_polys`
     into `OCRLine(text, bbox=[min x, min y, max x, max y], confidence)`. Keep the numpy conversion
     (verified: ndarray input is accepted).
   - construct the engine **once** per process, not per request (0.45 s warm, but 18.5 s if it ever
     has to download).
3. **Selection behind `INSIGHTD_USE_REAL_OCR=1`**, defaulting off, so every existing gate keeps using
   `FakeOCRExtractor` and stays deterministic and offline.
4. **Pre-warm as an explicit deploy step**, not lazily on first request: a `scripts/warm_ocr_models.py`
   that constructs the engine with the exact production config, run at image-build time, and a
   documented `~/.paddlex` volume/`PADDLE_PDX_MODEL_SOURCE`-aware bake. Optionally set
   `PADDLE_PDX_DISABLE_MODEL_SOURCE_CHECK=True` to skip the connectivity probe at boot.
   Without this the first user upload eats a 20 s stall *and needs egress to huggingface.co*.
5. **Treat OCR as async work.** 14–16 s/page means the Bill-Inbox upload must enqueue and poll, not
   block an HTTP request. That is a real design consequence, not a detail.
6. **Do not port `mean_confidence`-based review routing to the real path** — pick a different
   signal (field-level parse failure, totals not footing, no GSTIN match).

**Deployment cost inherited, stated plainly:**

| | |
|---|---|
| wheel download (cold) | ~163 MB primary (`paddlepaddle` 104.5 MB + `opencv-contrib-python` 63.7 MB) |
| installed venv size | **933 MB** |
| model cache | **177 MB** in `~/.paddlex/official_models` (139 MB if the doc-preprocessor is disabled, as recommended) |
| cold bootstrap (download + first ctor) | ~40 s, **requires egress to huggingface.co exactly once** |
| warm process start | 1.8 s import + 0.45 s ctor |
| steady-state per page | **~14–16 s, CPU-bound, no GPU path on this wheel** |
| extra transitive deps | 63 packages incl. `pandas`, `opencv`, `shapely`, `huggingface-hub`, `modelscope`, and its own `pydantic` |

**Remaining unknown this spike did not close:** accuracy on genuine photographed supplier bills
(logos, stamps, colour, perspective, thermal print, mixed script). Everything here was measured on
two images I rendered myself. The next honest step before trusting field values in production is a
small corpus of real bills.

---

## 8. Independent verification (second agent, same session)

The spike above was re-run adversarially by a second agent whose brief was to **refute** it, not
to agree with it. It did not reuse the first agent's jail, script, or images. Its verdict was the
same label — **WIRE_WITH_CAVEATS** — but it upheld the decisive claims on *stronger* evidence and
**refuted or downgraded three secondary ones**. Where §1–§7 above and this section disagree,
**this section is authoritative.**

### 8.1 What survived refutation (the decisive claims)

- **Model download** — independently reproduced into a *disposable* `HOME` (not the warm cache):
  139 MB, three models from huggingface.co, cold constructor 17.88 s.
- **Offline inference** — reproduced in a **fresh process**, on a bill the first agent never
  rendered, with five network probes fired **in the same PID immediately before** inference:
  DNS → `gaierror`; raw TCP to two different literal IPs → `PermissionError [Errno 1]`; UDP:53 →
  `PermissionError`; urllib → `URLError`. Outside the jail, all five connect. `PermissionError`
  on a raw `connect()` is the **kernel refusing the syscall** — not a proxy-variable placebo.
- **The negative control the first agent did not run**, and the single strongest piece of
  evidence here: **jailed + empty `HOME` → hard failure** (`No available model hosting platforms
  detected`). That proves the offline success is real cache-backed inference, and not bundled
  weights or an exception swallowed into an empty result.
- **Adapter breakage** — all three breaks reproduced by running the **actual repo
  `PaddleOCRExtractor().extract()`**, not by re-typing its calls.
- **Isolation** — `insightd/.venv` untouched (grep for paddle/opencv/shapely/huggingface/cv2
  returns nothing; 76 packages, 316 MB, unchanged); core suite **79 passed** before and after.

### 8.2 REFUTED — latency is ~3× worse at realistic resolution

§5/§6 say "~14–16 s per image regardless". That only varied the *config*; both images were
~2.2 MP. Varying the thing that actually matters for a Bill-Inbox upload:

| input | megapixels | latency |
|---|---|---|
| 1240×1754 | 2.2 MP | 13.75 s |
| 2480×3508 (A4 @ 300 dpi) | 8.7 MP | 31.88 s |
| 3024×4032 (12 MP phone photo) | 12 MP | **44.77 s** |

The real product case — a user photographing a supplier bill — is **~45 s, not 14.7 s**, so the
enqueue-and-poll requirement is *more* severe than stated. **Mitigation the first agent missed:**
downscaling to ~1240 px wide before `predict()` cost **zero** accuracy (41 lines, mean 0.9982 at
1240 px vs 41 lines, mean 0.9967 at 2480 px) for a **2.3× speedup**. Do this in the adapter.

### 8.3 REFUTED — the degraded-image quality claim does not generalize

§5's "14/14 on the degraded bill, no digit misread" was **graded on the first agent's own two
renders**. On a different bill (three line items, Times New Roman) with harder degradation
(−4.5° rotation), field recovery fell to **11/14**, and four **numeric** tokens were corrupted:

| truth | OCR read |
|---|---|
| `45.50` | `45:50` |
| `1.000` | `1-000` |
| `300.00` | `300:00` |
| `Taxable Value` | `Taxable Valie` |

The **clean** half *did* reproduce at 14/14 on an unseen bill — so the positive result is real for
clean scans. The degraded claim is not.

### 8.4 REFUTED (worse than reported) — confidence is not a usable review signal

§5 notes `mean_confidence` is useless for routing (real: 0.9982 clean / 0.9703 degraded, versus
the fake corpus's `ocr_quality: 'degraded'` pin of `<= 0.55` at `adapters/fakes.py:120`). But
**per-line confidence is useless too**: the corrupted money value `300:00` came back at **0.985**
and `Taxable Valie` at **0.950**; only `1-000` (0.880) and `45:50` (0.910) were mildly depressed.
**No confidence threshold separates the four corrupted numeric tokens from the 37 correct ones.**

This is the finding that matters most for this product: real OCR is **confidently wrong**, not
unconfidently wrong, and it corrupts *money values* specifically. Human-in-the-loop acceptance in
F9 is therefore not a nicety here — it is the only thing standing between OCR and a wrong ledger.

### 8.5 Factual correction — `requirements-ocr.txt` already exists

§6 item 1 says to create it. It is already present and already pins `paddleocr>=2.7` /
`paddlepaddle>=2.6` / `pillow>=10` / `pdf2image>=1.17`. **That loose `>=2.7` lower bound is the
root cause of §4.7's adapter breakage** — it resolves 3.7.0 against a 2.x-API adapter. The wiring
session must **edit** the file and pin `paddleocr==3.7.0` / `paddlepaddle==3.3.1`.

### 8.6 Consolidated wiring checklist (supersedes §6)

1. **Edit** (do not create) `insightd/requirements-ocr.txt`; pin `==3.7.0` / `==3.3.1`.
2. Rewrite the body of `adapters/ocr_paddle.py` for the 3.x API (drop `show_log`; `predict()` not
   `ocr(..., cls=)`; zip `rec_texts`/`rec_scores`/`rec_polys`).
3. **Downscale to ~1240 px wide before `predict()`** (§8.2) — free 2.3× speedup.
4. Gate on `INSIGHTD_USE_REAL_OCR=1`, default **off**; keep `paddle` out of `requirements.txt` and
   keep the lazy import, so the 79-test core suite stays heavy-dep-free.
5. **Enqueue-and-poll**, not a synchronous HTTP request (§8.2: up to ~45 s).
6. Pre-warm models as an explicit deploy step; `~/.paddlex` is `$HOME`-relative, so a container
   must bake or mount it.
7. **Do not route human review on OCR confidence at all** (§8.4). Use structural signals: totals
   not footing, no GSTIN regex match, tax ≠ rate × taxable, field parse failure.

**Still-open unknown, unchanged:** accuracy on genuine photographed supplier bills. Every image in
this spike — both agents' — was synthetic.
