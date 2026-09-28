#!/usr/bin/env python3
"""Write corpora/embedding/_poison/: qrels files wrong in one named way each.

check_poison() requires validate_embedding to reject every one. They reuse the
real corpus and queries (the validator falls back to corpora/embedding when a
poison directory has none), so each differs from the real judgments by exactly
its named defect.

    python3 corpora/embedding/make_poison.py
"""

from __future__ import annotations

import copy
import importlib.util
import json
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("emb_generate", HERE / "generate.py")
gen = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gen)
QRELS = gen.files()["qrels.json"]


def main() -> int:
    out = HERE / "_poison"
    out.mkdir(exist_ok=True)
    for p in out.glob("*.json"):
        p.unlink()

    # 1. A hard negative promoted to relevant: "Sharma Traders KO diya" labelled
    #    as the answer to "Sharma Traders SE aaya".
    a = copy.deepcopy(QRELS)
    r = next(x for x in a["rows"] if x["grade"] == 0)
    r["grade"] = 2
    # 2. A relevant document dropped from the judgments (unjudged relevant).
    b = copy.deepcopy(QRELS)
    i = next(k for k, x in enumerate(b["rows"]) if x["grade"] == 2)
    del b["rows"][i]
    # 3. A wrong-month document labelled as the answer.
    c = copy.deepcopy(QRELS)
    r = next(x for x in c["rows"] if x["grade"] == 1)
    r["grade"] = 2
    for name, obj in (("qrels-hard-negative-as-relevant", a), ("qrels-unjudged-relevant", b),
                      ("qrels-wrong-key-as-relevant", c)):
        (out / f"{name}.json").write_text(json.dumps(obj, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    print("wrote 3 poison qrels")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
