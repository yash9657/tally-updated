"""The open-weight (OpenAI-compatible json_schema) inferencer — replayed from
cassettes, never against a network. See adapters/inferencer_openai_compat.py.

EVERY TEST STARTS FROM THE CORPUS INPUT AND RE-DERIVES THE REQUEST, then checks
it against the cassette's recorded request hash before serving the recorded
response. A change to the prompt, the schema or the redaction that moves the
request bytes fails here by name — the replay would otherwise keep serving an
answer to a question the adapter no longer asks.
"""

from __future__ import annotations

import json
import pathlib

import httpx
import pytest

from insightd.adapters.inferencer_openai_compat import OpenAICompatInferencer, response_format
from insightd.config import Settings, StageFactory, active_adapter_set
from insightd.contracts.pipeline import InferredInvoice
from insightd.interfaces.base import StageError
from insightd.redaction import recognizers
from insightd.redaction.boundary import PIIBoundaryViolation

import openweight_cassettes as oc

CASSETTES = pathlib.Path(__file__).resolve().parent / "fixtures" / "openweight"
# ⛔ PINNED EXACTLY, like the Go tier-0 census: adding or dropping a cassette
# must be a deliberate edit here, not a silent change in coverage.
EXPECTED_CASSETTES = {"success.json": 1, "repair.json": 2, "unrepairable.json": 2}


def _load(name: str) -> dict:
    return json.loads((CASSETTES / name).read_text())


class Recorder:
    """An httpx transport that serves a cassette's responses in order and keeps
    every request it was sent."""

    def __init__(self, responses: list[dict], status: int = 200) -> None:
        self.responses = list(responses)
        self.status = status
        self.requests: list[dict] = []
        self.headers: list[dict] = []

    def __call__(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(json.loads(request.content))
        self.headers.append(dict(request.headers))
        if self.status != 200:
            return httpx.Response(self.status, json={"error": "echo of your prompt would be here"})
        return httpx.Response(200, json=self.responses.pop(0))


def _adapter(rec: Recorder, boundary, **kw) -> OpenAICompatInferencer:
    return OpenAICompatInferencer(
        boundary, oc.BASE_URL, oc.MODEL, "INSIGHTD_OPENWEIGHT_API_KEY",
        transport=httpx.MockTransport(rec), **kw,
    )


def test_the_cassette_census_is_exact():
    got = {p.name: len(json.loads(p.read_text())["responses"]) for p in CASSETTES.glob("*.json")}
    assert got == EXPECTED_CASSETTES


def test_the_request_rederived_from_the_input_matches_the_recording():
    _, _, _, _, _, req = oc.build_request_for_fixture()
    for name in EXPECTED_CASSETTES:
        cas = _load(name)
        assert oc.request_sha(req) == cas["request_sha256"], (
            f"{name}: the adapter now builds a different request than the one recorded — "
            "re-record with tests/openweight_cassettes.py and review the diff")


def test_the_wire_shape_is_json_schema_chat_completions():
    _, _, _, _, _, req = oc.build_request_for_fixture()
    assert req["model"] == oc.MODEL
    assert req["temperature"] == 0
    assert [m["role"] for m in req["messages"]] == ["system", "user"]
    rf = req["response_format"]
    assert rf["type"] == "json_schema"
    assert rf["json_schema"]["name"] == "inferred_invoice"
    assert rf["json_schema"]["strict"] is True
    # The schema IS the contract, so a field added to InferredInvoice is asked for.
    assert rf == response_format()
    assert "round_off" in json.dumps(rf["json_schema"]["schema"])
    assert "field_evidence" in rf["json_schema"]["schema"]["properties"]


def test_replay_success_yields_the_invoice_and_sends_no_raw_pii(monkeypatch):
    monkeypatch.setenv("INSIGHTD_OPENWEIGHT_API_KEY", "sk-test-not-a-real-key")
    fx, ctx, redacted, cls, boundary, _ = oc.build_request_for_fixture()
    rec = Recorder(_load("success.json")["responses"])
    inv = _adapter(rec, boundary).infer(redacted, cls, ctx)
    assert isinstance(inv, InferredInvoice)
    assert inv.invoice_no == fx["input"]["inferred"]["invoice_no"]
    assert inv.totals.invoice_total == fx["input"]["inferred"]["totals"]["invoice_total"]
    assert inv.field_evidence["totals"].page == 0
    assert len(rec.requests) == 1
    # THE PII BOUNDARY, on what actually left: nothing detectable in the body.
    assert recognizers.detect_all(rec.requests[0]) == {}
    assert rec.headers[0]["authorization"] == "Bearer sk-test-not-a-real-key"


def test_one_schema_violation_is_repaired_and_the_repair_is_rescanned():
    _, ctx, redacted, cls, boundary, _ = oc.build_request_for_fixture()
    rec = Recorder(_load("repair.json")["responses"])
    inv = _adapter(rec, boundary).infer(redacted, cls, ctx)
    assert inv.line_items and len(rec.requests) == 2
    repair = rec.requests[1]["messages"]
    assert repair[-1]["role"] == "user" and "did not match the schema" in repair[-1]["content"]
    # The repair names the FIELD and error TYPE, not the model's values.
    assert "line_items" in repair[-1]["content"]
    assert recognizers.detect_all(rec.requests[1]) == {}


def test_an_unrepairable_answer_is_a_stage_error_not_a_half_invoice():
    _, ctx, redacted, cls, boundary, _ = oc.build_request_for_fixture()
    rec = Recorder(_load("unrepairable.json")["responses"])
    with pytest.raises(StageError) as ei:
        _adapter(rec, boundary, max_repairs=1).infer(redacted, cls, ctx)
    assert "after 1 repair" in str(ei.value)
    assert len(rec.requests) == 2  # bounded: the original + exactly one repair


def test_a_provider_error_does_not_quote_the_provider_body():
    _, ctx, redacted, cls, boundary, _ = oc.build_request_for_fixture()
    rec = Recorder([], status=500)
    with pytest.raises(StageError) as ei:
        _adapter(rec, boundary).infer(redacted, cls, ctx)
    assert "HTTP 500" in str(ei.value)
    assert "prompt" not in str(ei.value)


def test_raw_pii_is_refused_before_any_request_is_sent():
    """⛔ FAIL-CLOSED FIRST: a raw GSTIN downstream of redaction never leaves."""
    _, ctx, redacted, cls, boundary, _ = oc.build_request_for_fixture()
    leaked = redacted.model_copy(update={"full_text": redacted.full_text + "\nGSTIN 27ABCDE1234F1Z0"})
    rec = Recorder(_load("success.json")["responses"])
    with pytest.raises(PIIBoundaryViolation):
        _adapter(rec, boundary).infer(leaked, cls, ctx)
    assert rec.requests == []


def test_the_request_goes_to_chat_completions_under_the_base_url():
    _, ctx, redacted, cls, boundary, _ = oc.build_request_for_fixture()
    seen = []

    def handler(req: httpx.Request) -> httpx.Response:
        seen.append(str(req.url))
        return httpx.Response(200, json=_load("success.json")["responses"][0])

    OpenAICompatInferencer(boundary, oc.BASE_URL + "/", oc.MODEL, "X",
                           transport=httpx.MockTransport(handler)).infer(redacted, cls, ctx)
    assert seen == [oc.BASE_URL + "/chat/completions"]


def test_it_is_not_the_default_and_is_selected_only_explicitly():
    """⛔ Opus stays the real default until W1b has numbers."""
    assert Settings().inferencer_backend == "opus"
    assert active_adapter_set(Settings())["inferencer"] == "fake"
    s = Settings(use_real_inferencer=True, inferencer_backend="openai_compat",
                 openweight_base_url=oc.BASE_URL, openweight_model=oc.MODEL)
    assert active_adapter_set(s)["inferencer"] == "real:openai_compat"
    from insightd.redaction.boundary import Boundary
    inf = StageFactory(s).inferencer(Boundary())
    assert isinstance(inf, OpenAICompatInferencer)
    assert inf.base_url == oc.BASE_URL and inf.model == oc.MODEL
