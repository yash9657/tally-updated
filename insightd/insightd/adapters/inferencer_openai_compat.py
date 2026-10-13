"""Open-weight Inferencer over an OpenAI-compatible ``json_schema`` endpoint.

W5 (PLAN-AGENTIC.md §3): "GLM-5.3-Flash / Qwen3.8-27B with json_schema
replacing the Haiku/Opus adapters". This is the ADAPTER half of that sentence,
and deliberately not the REPLACEMENT half:

  ⛔ IT IS NOT THE DEFAULT. ``Settings.inferencer_backend`` stays ``"opus"``
  and the fakes stay the default of the default. Replacing Opus needs W1b's
  numbers — field-level F1 on the extraction set at or above the current
  pipeline — and there are no provider keys in this environment to produce
  them. An adapter that is built, tested on recorded responses and switched off
  is honest; one switched on by assertion is not.

THE WIRE SHAPE is the one Fireworks, DeepInfra, Together and a self-hosted vLLM
all accept on ``POST {base_url}/chat/completions``:

    {"model": ..., "temperature": 0,
     "messages": [{"role": "system", ...}, {"role": "user", ...}],
     "response_format": {"type": "json_schema",
                         "json_schema": {"name": "inferred_invoice",
                                         "strict": true,
                                         "schema": <InferredInvoice schema>}}}

and the answer is read from ``choices[0].message.content`` as JSON and
validated against ``InferredInvoice`` (extra='forbid'). This mirrors the Go
side's internal/llm ``json_schema`` mode; insightd is Python, so it has its own
small client rather than a cross-language call.

THE SAME FOUR GUARANTEES AS THE OPUS ADAPTER, EACH PINNED BY A TEST
-------------------------------------------------------------------
  1. ``boundary.assert_clean`` over the WHOLE outgoing payload is the first
     thing that happens — before the HTTP client is even constructed — and a
     repair request is re-scanned too (it quotes the model's own output).
  2. GST AMOUNTS ARE NOT TRUSTED: the model supplies taxable value, rate and
     place of supply; the mapper re-derives tax (unchanged).
  3. A response that does not validate is repaired AT MOST ``max_repairs``
     times (default 1) by sending the validation error back; after that it is
     a ``StageError``, never a half-parsed invoice.
  4. The API key is read from the environment variable NAMED in settings, at
     call time, and never logged or put in an exception message.

Transport is injectable (``httpx.MockTransport`` in tests), so no test makes a
network call. httpx is a CORE dependency already (requirements.txt), so this
adds nothing to the install.
"""

from __future__ import annotations

import json
import os

from pydantic import ValidationError

from ..contracts.pipeline import (
    ClassifiedDoc,
    InferredInvoice,
    RedactedOCR,
    TenantContext,
)
from ..interfaces.base import StageError
from ..redaction.boundary import Boundary

DEFAULT_TIMEOUT_S = 60.0


def response_format() -> dict:
    """The json_schema response_format for InferredInvoice. Computed from the
    pydantic model, so a field added to the contract is in the request."""
    return {
        "type": "json_schema",
        "json_schema": {
            "name": "inferred_invoice",
            "strict": True,
            "schema": InferredInvoice.model_json_schema(),
        },
    }


SYSTEM_PROMPT = (
    "You extract a structured purchase invoice from REDACTED Indian-invoice OCR "
    "text (PAN/GSTIN/Aadhaar/IFSC appear as <<TYPE:hash>> tags; never emit a raw "
    "one). Return ONLY JSON matching the schema. Extract line items, HSN/SAC, "
    "taxable values, GST rate %, place of supply, reverse charge, the printed "
    "totals and any printed round-off. DO NOT compute CGST/SGST/IGST — they are "
    "re-derived downstream. Copy numbers exactly as printed; do not correct a "
    "number you cannot read — leave it null. Map each line to a ledger from the "
    "tenant chart of accounts by id."
)


class OpenAICompatInferencer:
    def __init__(
        self,
        boundary: Boundary,
        base_url: str,
        model: str,
        api_key_env: str,
        *,
        transport=None,
        max_repairs: int = 1,
        timeout_s: float = DEFAULT_TIMEOUT_S,
    ) -> None:
        if not base_url or not model:
            raise ValueError("OpenAICompatInferencer needs a base_url and a model id")
        self.boundary = boundary
        self.base_url = base_url.rstrip("/")
        self.model = model
        self.api_key_env = api_key_env
        self.transport = transport
        self.max_repairs = max_repairs
        self.timeout_s = timeout_s

    # -- request building (pure) ------------------------------------------

    def build_messages(self, redacted_ocr: RedactedOCR, cls: ClassifiedDoc, ctx: TenantContext) -> list[dict]:
        chart = [{"ledger_id": led.id, "name": led.name, "kind": led.kind} for led in ctx.ledgers]
        user = (
            f"OCR (redacted):\n{redacted_ocr.full_text}\n\n"
            f"Tenant chart of accounts (masked): {json.dumps(chart)}\n\n"
            f"Classifier supplier match: {json.dumps(cls.supplier_match.model_dump())}"
        )
        return [
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": user},
        ]

    def build_request(self, messages: list[dict]) -> dict:
        return {
            "model": self.model,
            "temperature": 0,
            "messages": messages,
            "response_format": response_format(),
        }

    # -- the call -------------------------------------------------------------

    def infer(self, redacted_ocr: RedactedOCR, cls: ClassifiedDoc, ctx: TenantContext) -> InferredInvoice:
        messages = self.build_messages(redacted_ocr, cls, ctx)
        # ⛔ FAIL-CLOSED, FIRST. Nothing below runs for a payload with raw PII.
        self.boundary.assert_clean(messages)

        import httpx  # core dep; imported here so the module stays cheap to load

        key = os.environ.get(self.api_key_env, "")
        headers = {"Content-Type": "application/json"}
        if key:
            headers["Authorization"] = f"Bearer {key}"
        with httpx.Client(transport=self.transport, timeout=self.timeout_s) as client:
            last_error = ""
            for attempt in range(self.max_repairs + 1):
                body = self.build_request(messages)
                try:
                    resp = client.post(f"{self.base_url}/chat/completions", json=body, headers=headers)
                except httpx.HTTPError as exc:
                    raise StageError("inferencer", f"open-weight endpoint unreachable: {type(exc).__name__}") from None
                if resp.status_code != 200:
                    # The body is NOT quoted: a provider error page can echo the
                    # request, and the request is the tenant's (redacted) bill.
                    raise StageError("inferencer", f"open-weight endpoint answered HTTP {resp.status_code}")
                content = _content(resp)
                try:
                    return InferredInvoice.model_validate(json.loads(content))
                except (ValueError, ValidationError) as exc:
                    last_error = _short(exc)
                    if attempt == self.max_repairs:
                        break
                    # BOUNDED REPAIR: send the model its own output and the error.
                    messages = messages + [
                        {"role": "assistant", "content": content},
                        {"role": "user", "content": f"That JSON did not match the schema: {last_error}. "
                                                    "Return corrected JSON only."},
                    ]
                    self.boundary.assert_clean(messages)
        raise StageError("inferencer", f"response did not match the schema after {self.max_repairs} repair(s): {last_error}")


def _content(resp) -> str:
    try:
        data = resp.json()
        return data["choices"][0]["message"]["content"] or ""
    except (ValueError, KeyError, IndexError, TypeError):
        raise StageError("inferencer", "open-weight response had no choices[0].message.content") from None


def _short(exc: Exception) -> str:
    """A validation error summarised by FIELD and TYPE, not by input value."""
    if isinstance(exc, ValidationError):
        parts = [f"{'.'.join(str(x) for x in e['loc'])}: {e['type']}" for e in exc.errors()[:5]]
        return "; ".join(parts)
    return "not valid JSON"
