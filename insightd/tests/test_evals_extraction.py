"""W1a extraction set + field-level F1 scorer (d124).

The scorer is proven against four inputs whose scores are KNOWN: the labels
themselves (1.0 exactly), nothing (0.0 exactly), the labels with one named
defect (a computable drop), and a deterministic regex floor whose figures are
PINNED — so a scorer change that moves any number is a visible diff, not a
silent re-baselining of every model measured afterwards.
"""

from __future__ import annotations

import copy
import random
from collections import Counter

import pytest

from insightd.evals import baseline_extract, extraction as ex

DOCS = ex.load_documents()


def oracle(docs):
    by_text = {d["document"]["text"]: d["labels"] for d in docs}
    return lambda text, kind: copy.deepcopy(by_text[text])


def test_set_composition_is_the_one_w1_names():
    kinds = Counter(d["kind"] for d in DOCS)
    assert kinds == {"invoice": 100, "bank_statement": 50}
    banks = Counter(d["labels"]["fields"]["bank"] for d in DOCS if d["kind"] == "bank_statement")
    assert banks == {"HDFC Bank": 10, "ICICI Bank": 10, "State Bank of India": 10, "Axis Bank": 10,
                     "Kotak Mahindra Bank": 10}
    for d in DOCS:
        assert d["synthetic"] is True and "SYNTHETIC" in d["provenance"]
        assert "SYNTHETIC" in d["document"]["text"].splitlines()[0], d["id"]


def test_oracle_scores_exactly_one():
    rep = ex.evaluate(oracle(DOCS), DOCS)
    assert rep.overall.fp == 0 and rep.overall.fn == 0
    assert rep.summary()["micro_f1"] == 1.0


def test_nothing_scores_exactly_zero():
    rep = ex.evaluate(lambda text, kind: None, DOCS)
    assert rep.overall.tp == 0 and rep.overall.fp == 0
    assert rep.summary()["micro_f1"] == 0.0


def test_regex_floor_is_pinned():
    # Measured 2026-09-28. A change here is a scorer or fixture change and must
    # say which in d124 — never a silent re-pin.
    got = ex.evaluate(baseline_extract.extract, DOCS).summary()
    assert got == {"documents": 150, "micro_f1": 0.877864, "header_f1": 0.80865, "line_f1": 0.895871,
                   "precision": 0.958433, "recall": 0.80979, "bank_statement_f1": 0.954576,
                   "invoice_f1": 0.748015}


def test_a_wrong_value_costs_an_fp_and_an_fn():
    doc = next(d for d in DOCS if d["kind"] == "invoice")
    pred = copy.deepcopy(doc["labels"])
    pred["fields"]["invoice_total"] = "1.00"
    rep = ex.Report()
    ex.score_document(doc, pred, rep)
    c = rep.by_field["invoice_total"]
    assert (c.tp, c.fp, c.fn) == (0, 1, 1)


def test_zero_amount_is_empty_and_inventing_one_is_an_fp():
    doc = next(d for d in DOCS if d["kind"] == "invoice" and d["labels"]["fields"]["igst"] == "0.00")
    pred = copy.deepcopy(doc["labels"])
    pred["fields"].pop("igst")
    rep = ex.Report()
    ex.score_document(doc, pred, rep)
    assert (rep.overall.fp, rep.overall.fn) == (0, 0), "leaving a zero head blank is not a miss"
    pred["fields"]["igst"] = "12.00"
    rep = ex.Report()
    ex.score_document(doc, pred, rep)
    assert rep.by_field["igst"].fp == 1, "stating an IGST on an intra-State bill is an invented figure"


def test_line_order_does_not_matter_but_splitting_a_line_does():
    doc = next(d for d in DOCS if d["kind"] == "invoice" and len(d["labels"]["lines"]) >= 3)
    pred = copy.deepcopy(doc["labels"])
    random.Random(1).shuffle(pred["lines"])
    rep = ex.Report()
    ex.score_document(doc, pred, rep)
    assert rep.lines.fp == 0 and rep.lines.fn == 0
    split = copy.deepcopy(doc["labels"])
    first = split["lines"][0]
    half = dict(first, qty="0", amount="0")
    split["lines"].append(half)
    rep = ex.Report()
    ex.score_document(doc, split, rep)
    assert rep.lines.fp > 0


@pytest.mark.parametrize("kind,a,b", [
    ("date", "12/06/2025", "2025-06-12"), ("date", "12-Jun-2025", "2025-06-12"),
    ("date", "June 12, 2025", "2025-06-12"), ("date", "12/06/25", "2025-06-12"),
    ("date", "5 Jun 2025", "2025-06-05"),
    ("money", "₹ 1,18,000.00", "118000"), ("money", "Rs. 118,000/-", "118000.00"),
    ("money", "118000.004", "118000.00"),
    ("code", "27aabca1234b1z2 ", "27AABCA1234B1Z2"), ("state", "27-Maharashtra", "27"),
    ("state", "Karnataka", "29"), ("text", "Iyer & Sons", "IYER AND SONS"),
])
def test_normalisers_equate_the_same_fact(kind, a, b):
    assert ex.normalise(kind, a) == ex.normalise(kind, b)


@pytest.mark.parametrize("kind,a,b", [
    ("date", "05/06/2025", "2025-05-06"),        # day-first: this is 5 June, not 6 May
    ("text", "Acme Traders", "Acme Trading Co"),  # different parties stay different
    ("money", "1,18,000.00", "1,18,000.50"),
    ("code", "SI/2025/0339", "SI/2O25/0339"),     # no OCR repair in the scorer
])
def test_normalisers_keep_different_facts_different(kind, a, b):
    assert ex.normalise(kind, a) != ex.normalise(kind, b)


def test_predict_sees_only_text_and_kind():
    seen = []

    def spy(text, kind):
        seen.append((type(text), kind))
        return None

    ex.evaluate(spy, DOCS[:3])
    assert seen == [(str, DOCS[i]["kind"]) for i in range(3)]
