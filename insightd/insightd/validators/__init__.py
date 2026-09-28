"""Deterministic validators that DECIDE extraction confidence (W5).

Design memo: docs/design/d123-w5-documents-bank.md §3.

WHY CONFIDENCE IS DECIDED HERE AND NOT BY THE MODEL
---------------------------------------------------
The WS10 spike (docs/spikes/ws10-paddleocr-spike.md §8.4) measured the thing
this package exists for: OCR returned a corrupted ``300.00`` as ``300:00`` at
0.985 confidence. A model's self-reported confidence is an opinion about its
own output, and it is most confident exactly where it is most wrong about
digits. What does not care how sure the model was is ARITHMETIC and CHECKSUMS:

  * a GSTIN carries a mod-36 check character — one misread character in fifteen
    fails it (``gstin``);
  * an HSN/SAC code has a fixed structure and a tenant has a history of the
    codes it actually buys (``hsn``);
  * taxable + taxes ± round-off = total, and the lines add up to the taxable
    value (``totals``);
  * the same supplier bill arriving twice is two drafts of ONE payable
    (``duplicates``).

THE ONE-WAY RULE. A validator may only LOWER a field's confidence (to zero, or
to "unresolved") or CONFIRM it. It never raises one. A passing checksum is not
evidence the model read the right GSTIN for the right party — only that it read
fifteen characters that could be one — so "pass" leaves the model's number
alone and "fail" overrides it. That asymmetry is the whole design: the costly
direction (a wrong value accepted without review) is the one these checks can
close, and the cheap direction (an extra review) is the one they are allowed to
cost.

Pure python, no optional deps, no network, no model.
"""

from __future__ import annotations

from .result import Validation  # noqa: F401
