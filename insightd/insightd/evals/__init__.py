"""W1a evaluation scorers (d124): field-level F1 for extraction, and recall@k /
MRR / nDCG for narration retrieval.

Stdlib only, deterministic, no model and no network. The fixtures they score
against live in the repo's corpora/ (corpora/extraction, corpora/embedding) —
language-neutral JSON, validated independently by corpora/validate.py — so the
Go side and any future harness read the same ground truth.
"""

from __future__ import annotations

from pathlib import Path

# insightd/insightd/evals/__init__.py -> repo root is three parents up.
REPO_ROOT = Path(__file__).resolve().parents[3]
CORPORA = REPO_ROOT / "corpora"
