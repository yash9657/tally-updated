"""Narration retrieval scorer for the Hinglish embedding eval (W1a, d124).

The corpus (corpora/embedding) is 1,278 synthetic narrations — bank strings,
English voucher narrations, Hinglish and Devanagari Hindi — and 218 queries
with graded judgments (2 = the entry asked for, 1 = right intent / wrong party
or month, 0 = a JUDGED hard negative). It exists to pick W3's embedding model,
so the metrics are the ones that decision turns on:

  recall@k   fraction of a query's grade-2 documents in its top k, over
             min(k, #grade-2) so a query with 7 relevant entries can reach 1.0
             at k = 5 — averaged over queries
  mrr        1 / rank of the first grade-2 document, averaged (0 if none in
             the ranking at all)
  ndcg@10    graded gain 2^g - 1; unjudged documents gain 0
  triplet    over every (query, grade-2, grade-0) triple, the fraction where the
             relevant entry outscores the hard negative — the "Sharma Traders
             SE aaya" vs "KO diya" test, isolated from the rest of the ranking

Everything is computed from a RANKING (query id -> ordered doc ids) or from an
`embed(texts) -> vectors` callable; nothing here knows what model produced it.
Ties in similarity break by document id, so a ranking is deterministic.
"""

from __future__ import annotations

import hashlib
import json
import math
import random
import re
from collections import defaultdict
from pathlib import Path
from typing import Callable, Sequence

from . import CORPORA

EMBEDDING_DIR = CORPORA / "embedding"
KS = (1, 5, 10)


def load(directory: Path = EMBEDDING_DIR):
    corpus = json.loads((directory / "corpus.json").read_text(encoding="utf-8"))["documents"]
    queries = json.loads((directory / "queries.json").read_text(encoding="utf-8"))["queries"]
    rows = json.loads((directory / "qrels.json").read_text(encoding="utf-8"))["rows"]
    qrels: dict[str, dict[str, int]] = defaultdict(dict)
    for r in rows:
        qrels[r["query"]][r["doc"]] = r["grade"]
    return corpus, queries, dict(qrels)


def score_rankings(rankings: dict[str, Sequence[str]], qrels: dict[str, dict[str, int]],
                   scores: dict[str, dict[str, float]] | None = None) -> dict[str, float]:
    """Metrics over `rankings`. Every judged query must be ranked: a missing
    query is a KeyError, never a silently smaller denominator."""
    out = {f"recall@{k}": 0.0 for k in KS}
    out.update(mrr=0.0, **{"ndcg@10": 0.0})
    trip_hit = trip_n = 0
    for qid, judged in qrels.items():
        ranked = list(rankings[qid])
        rel = {d for d, g in judged.items() if g == 2}
        for k in KS:
            out[f"recall@{k}"] += len(rel & set(ranked[:k])) / min(k, len(rel)) if rel else 0.0
        rr = 0.0
        for i, d in enumerate(ranked, 1):
            if d in rel:
                rr = 1.0 / i
                break
        out["mrr"] += rr
        dcg = sum((2 ** judged.get(d, 0) - 1) / math.log2(i + 1) for i, d in enumerate(ranked[:10], 1))
        ideal = sorted(judged.values(), reverse=True)[:10]
        idcg = sum((2 ** g - 1) / math.log2(i + 1) for i, g in enumerate(ideal, 1))
        out["ndcg@10"] += dcg / idcg if idcg else 0.0
        pos = [d for d, g in judged.items() if g == 2]
        neg = [d for d, g in judged.items() if g == 0]
        if scores is not None:
            s = scores[qid]
            for p in pos:
                for n in neg:
                    trip_n += 1
                    trip_hit += s[p] > s[n]
        else:
            rank = {d: i for i, d in enumerate(ranked)}
            for p in pos:
                for n in neg:
                    trip_n += 1
                    trip_hit += rank.get(p, math.inf) < rank.get(n, math.inf)
    n = len(qrels)
    res = {k: round(v / n, 6) for k, v in out.items()}
    res["triplet"] = round(trip_hit / trip_n, 6) if trip_n else 0.0
    res["queries"] = n
    return res


def _unit(v) -> dict[int, float]:
    """A vector as a sparse unit dict. Dense sequences and sparse {index: value}
    dicts are both accepted, so a hashed lexical baseline need not materialise
    4,096 zeros per document to be compared."""
    items = v.items() if isinstance(v, dict) else ((i, x) for i, x in enumerate(v) if x)
    d = {i: float(x) for i, x in items}
    norm = math.sqrt(sum(x * x for x in d.values()))
    return {i: x / norm for i, x in d.items()} if norm else {}


def _dot(a: dict[int, float], b: dict[int, float]) -> float:
    if len(a) > len(b):
        a, b = b, a
    return sum(x * b.get(i, 0.0) for i, x in a.items())


def evaluate_embedder(embed: Callable[[list[str]], list[Sequence[float]]], directory: Path = EMBEDDING_DIR,
                      by_lang: bool = False) -> dict:
    """Embed corpus and queries with `embed`, rank by cosine, score."""
    corpus, queries, qrels = load(directory)
    dvec = [_unit(v) for v in embed([d["text"] for d in corpus])]
    qvec = [_unit(v) for v in embed([q["text"] for q in queries])]
    rankings, scores = {}, {}
    for q, qv in zip(queries, qvec):
        sims = {d["id"]: _dot(qv, dv) for d, dv in zip(corpus, dvec)}
        scores[q["id"]] = sims
        rankings[q["id"]] = sorted(sims, key=lambda d: (-sims[d], d))
    res = score_rankings(rankings, qrels, scores)
    res["xlang_recall@10"] = cross_language_recall(rankings, qrels, corpus, queries, 10)
    if by_lang:
        for lang in ("en", "hi", "hinglish"):
            ids = {q["id"] for q in queries if q["lang"] == lang}
            res[lang] = score_rankings(rankings, {k: v for k, v in qrels.items() if k in ids},
                                       {k: v for k, v in scores.items() if k in ids})
    return res


def cross_language_recall(rankings, qrels, corpus, queries, k: int = 10) -> float:
    """recall@k over only the grade-2 documents written in a DIFFERENT language
    (or register: a bank string counts as its own) from the query.

    This is the number that decides the model. Plain recall@1 is flattered by
    the corpus holding a same-script paraphrase of every query, which a lexical
    matcher finds first; what an accountant needs is that "bijli ka bill"
    finds "UPI/…/MSEDCL/…" and "मई का बिजली बिल" too.
    """
    lang = {d["id"]: (d["register"] if d["register"] == "bank" else d["lang"]) for d in corpus}
    qlang = {q["id"]: q["lang"] for q in queries}
    total, n = 0.0, 0
    for qid, judged in qrels.items():
        rel = {d for d, g in judged.items() if g == 2 and lang[d] != qlang[qid]}
        if not rel:
            continue
        n += 1
        total += len(rel & set(list(rankings[qid])[:k])) / min(k, len(rel))
    return round(total / n, 6) if n else 0.0


# ---------------------------------------------------------------------------
# reference rankers — the floor and the ceiling a real model is read between
# ---------------------------------------------------------------------------

def char_ngram_embedder(dim: int = 4096, n: tuple[int, ...] = (2, 3, 4)) -> Callable[[list[str]], list[dict[int, float]]]:
    """Hashed character n-gram TF vectors (L2 via cosine). Stdlib, deterministic.

    This is the LEXICAL floor: it matches "bijli" to "bijli" and nothing to
    "बिजली" or "electricity", and it cannot see direction. A model that does
    not beat it on the triplet metric has not learned the thing W3 needs.
    blake2b rather than hash(): Python salts str hashes per process, and a
    floor that moved between runs could not be pinned.
    """
    def embed(texts: list[str]) -> list[dict[int, float]]:
        out = []
        for t in texts:
            v: dict[int, float] = defaultdict(float)
            s = " " + re.sub(r"\s+", " ", t.casefold()) + " "
            for k in n:
                for i in range(len(s) - k + 1):
                    h = int.from_bytes(hashlib.blake2b(s[i:i + k].encode(), digest_size=4).digest(), "big")
                    v[h % dim] += 1.0
            out.append(dict(v))
        return out
    return embed


def random_rankings(corpus_ids: list[str], query_ids: list[str], seed: int = 7) -> dict[str, list[str]]:
    rng = random.Random(seed)
    out = {}
    for q in query_ids:
        ids = list(corpus_ids)
        rng.shuffle(ids)
        out[q] = ids
    return out


def oracle_rankings(corpus_ids: list[str], qrels: dict[str, dict[str, int]]) -> dict[str, list[str]]:
    out = {}
    for q, judged in qrels.items():
        out[q] = sorted(corpus_ids, key=lambda d: (-judged.get(d, -1), d))
    return out
