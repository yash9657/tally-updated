"""W1a Hinglish narration retrieval eval + scorer (d124).

Proven between a known ceiling (the judgments themselves: 1.0 on every metric),
a known floor (a seeded random ranking), and a deterministic lexical baseline
whose figures are pinned. The baseline's shape is the finding: it finds the
same-script paraphrase first (recall@1 ~0.93) and almost nothing across
scripts (xlang_recall@10 ~0.12), and its direction test ("se aaya" vs "ko
diya") is barely above a coin toss — which is what W3's model must fix.
"""

from __future__ import annotations

import pytest

from insightd.evals import retrieval as rt

CORPUS, QUERIES, QRELS = rt.load()
IDS = [d["id"] for d in CORPUS]


def test_sizes_are_pinned():
    assert (len(CORPUS), len(QUERIES), sum(len(v) for v in QRELS.values())) == (1278, 218, 2342)
    langs = {q["lang"] for q in QUERIES}
    assert langs == {"en", "hi", "hinglish"}


def test_oracle_is_one_everywhere():
    got = rt.score_rankings(rt.oracle_rankings(IDS, QRELS), QRELS)
    assert all(v == 1.0 for k, v in got.items() if k != "queries"), got


def test_reversed_oracle_fails_the_triplet_test_completely():
    rev = {q: list(reversed(r)) for q, r in rt.oracle_rankings(IDS, QRELS).items()}
    got = rt.score_rankings(rev, QRELS)
    assert got["triplet"] == 0.0 and got["recall@10"] == 0.0


def test_random_floor_is_pinned():
    got = rt.score_rankings(rt.random_rankings(IDS, [q["id"] for q in QUERIES]), QRELS)
    assert got == {"recall@1": 0.0, "recall@5": 0.003899, "recall@10": 0.007487, "mrr": 0.020567,
                   "ndcg@10": 0.004828, "triplet": 0.519728, "queries": 218}


def test_lexical_baseline_is_pinned():
    got = rt.evaluate_embedder(rt.char_ngram_embedder(), by_lang=True)
    assert {k: got[k] for k in ("recall@1", "recall@5", "recall@10", "mrr", "ndcg@10", "triplet",
                                "xlang_recall@10")} == {
        "recall@1": 0.931193, "recall@5": 0.299312, "recall@10": 0.293968, "mrr": 0.964832,
        "ndcg@10": 0.375904, "triplet": 0.558503, "xlang_recall@10": 0.124792}
    assert got["hi"]["recall@10"] == 0.161725 and got["hinglish"]["recall@10"] == 0.327876


def test_a_missing_query_is_an_error_not_a_smaller_denominator():
    ranks = rt.oracle_rankings(IDS, QRELS)
    ranks.pop(next(iter(ranks)))
    with pytest.raises(KeyError):
        rt.score_rankings(ranks, QRELS)


def test_dense_and_sparse_vectors_rank_identically():
    sparse = rt.char_ngram_embedder(dim=64)

    def dense(texts):
        return [[v.get(i, 0.0) for i in range(64)] for v in sparse(texts)]

    a = rt.evaluate_embedder(sparse)
    b = rt.evaluate_embedder(dense)
    assert a == b
