"""Build and (re)record the open-weight adapter's cassettes.

A cassette is {request_sha256, request, response}: the request the adapter
builds for one corpus bill, keyed by its hash, and the provider response served
back for it. ⚠️ THE RESPONSES ARE SYNTHETIC — shaped exactly like a Fireworks /
DeepInfra chat-completions answer (id, object, model, choices[].message.content,
usage), with ``content`` built from the corpus fixture's own ground-truth
inference. There are no provider keys in this environment; a real recording
replaces them by running this with a key once W1b has one, and the replay test
then pins the real bytes.

Run:  PYTHONPATH=. python tests/openweight_cassettes.py   (from insightd/)
"""

from __future__ import annotations

import hashlib
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parent))

CASSETTE_DIR = HERE / "fixtures" / "openweight"
FIXTURE_ID = "intra-state-18-1line-clean"
MODEL = "accounts/fireworks/models/qwen3p8-27b-instruct"
BASE_URL = "https://api.fireworks.ai/inference/v1"


def request_sha(req: dict) -> str:
    return hashlib.sha256(json.dumps(req, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def build_request_for_fixture():
    """Re-derive the adapter's FIRST request for the fixture, from the input."""
    from conftest import load_fixture, normalized_from_fixture
    from insightd.adapters.fakes import FakeClassifier, FakeOCRExtractor, FakePIIRedactor, stash_fixture
    from insightd.adapters.inferencer_openai_compat import OpenAICompatInferencer
    from insightd.contracts.pipeline import TenantContext
    from insightd.redaction.boundary import Boundary

    fx = load_fixture(FIXTURE_ID)
    ctx = TenantContext(**fx["input"]["tenant"])
    doc = normalized_from_fixture(fx, ctx)
    ocr = FakeOCRExtractor().extract(doc)
    redacted, _ = FakePIIRedactor().redact(ocr)
    # A FIXED salt so the <<TYPE:hash>> tags — and therefore the request bytes —
    # are reproducible. The production boundary salts per request.
    boundary = Boundary(salt="cassette-salt")
    redacted, _ = boundary.redact_for_llm(ocr)
    stash_fixture(redacted, fx)
    cls = FakeClassifier(boundary=boundary).classify(redacted, ctx)
    ad = OpenAICompatInferencer(boundary, BASE_URL, MODEL, "INSIGHTD_OPENWEIGHT_API_KEY")
    req = ad.build_request(ad.build_messages(redacted, cls, ctx))
    return fx, ctx, redacted, cls, boundary, req


def completion(content: str) -> dict:
    return {
        "id": "cmpl-synthetic-w5",
        "object": "chat.completion",
        "model": MODEL,
        "choices": [{"index": 0, "finish_reason": "stop",
                     "message": {"role": "assistant", "content": content}}],
        "usage": {"prompt_tokens": 812, "completion_tokens": 214, "total_tokens": 1026},
    }


def inferred_from_fixture(fx: dict) -> dict:
    inf = fx["input"]["inferred"]
    return {
        "invoice_no": inf["invoice_no"],
        "invoice_date": inf["invoice_date"],
        "supplier_gstin_masked": None,
        "place_of_supply_state_code": inf["place_of_supply_state_code"],
        "reverse_charge": inf["reverse_charge"],
        "exempt": inf["exempt"],
        "line_items": [
            {
                "description": li["description"], "hsn": li["hsn"], "qty": li["qty"], "unit": None,
                "rate": li["rate"], "taxable_value": li["taxable_value"], "gst_rate_pct": li["gst_rate_pct"],
                "ledger_guess": {"ledger_id": li["ledger_id"], "name": li["ledger_name"],
                                 "confidence": li["ledger_confidence"]},
            }
            for li in inf["line_items"]
        ],
        "totals": {"taxable": inf["totals"]["taxable"], "invoice_total": inf["totals"]["invoice_total"]},
        "per_field_confidence": {"invoice_no": 0.97, "invoice_date": 0.96, "place_of_supply_state_code": 0.95},
        "field_evidence": {"totals": {"page": 0, "bbox": [400, 620, 560, 640]}},
    }


def record() -> None:
    fx, _, _, _, _, req = build_request_for_fixture()
    CASSETTE_DIR.mkdir(parents=True, exist_ok=True)
    good = json.dumps(inferred_from_fixture(fx))
    cassettes = {
        "success": [completion(good)],
        # One schema violation (a string where a list belongs), then the repair.
        "repair": [completion(json.dumps({**json.loads(good), "line_items": "see attached"})), completion(good)],
        "unrepairable": [completion("{\"invoice_no\": 42"), completion("not json either")],
    }
    for name, responses in cassettes.items():
        (CASSETTE_DIR / f"{name}.json").write_text(json.dumps({
            "fixture": FIXTURE_ID,
            "synthetic": True,
            "request_sha256": request_sha(req),
            "request": req,
            "responses": responses,
        }, indent=1, sort_keys=True) + "\n")
    print("recorded", len(cassettes), "cassettes; request", request_sha(req)[:12])


if __name__ == "__main__":
    record()
