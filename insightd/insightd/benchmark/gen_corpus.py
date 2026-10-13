"""Generate the labeled synthetic Indian-invoice corpus.

Produces one JSON per invoice under benchmark/corpus/. All synthetic — PAN /
GSTIN / Aadhaar / IFSC are valid-FORMAT but fictitious (Aadhaar passes the
Verhoeff checksum so PII recall is measurable; GSTIN embeds a valid-format PAN).
Money is fixed-4 decimal STRINGS, state codes 2-digit, mirroring corpora/tax.

Run:  python -m insightd.benchmark.gen_corpus
"""

from __future__ import annotations

import json
from decimal import ROUND_HALF_UP, Decimal
from pathlib import Path

from ..adapters import tax as taxmod
from ..redaction.recognizers import verhoeff_checksum, verhoeff_valid

CORPUS_DIR = Path(__file__).resolve().parent / "corpus"
RULESET = "gst-2024-09"


def f4(v) -> str:
    return f"{Decimal(str(v)).quantize(Decimal('0.0001'), rounding=ROUND_HALF_UP):.4f}"


def aadhaar(prefix11: str) -> str:
    """Mint a valid-format synthetic Aadhaar (11-digit prefix + Verhoeff digit)."""
    assert len(prefix11) == 11 and prefix11.isdigit()
    a = prefix11 + str(verhoeff_checksum(prefix11))
    assert verhoeff_valid(a), a
    return a


def tenant(home_state: str = "27", extra_party: bool = False) -> dict:
    ledgers = [
        {"id": "L-PARTY-1", "name": "Acme Components Pvt Ltd", "gstin_masked": "27ABC…1Z0", "state_code": "27", "kind": "party"},
        {"id": "L-PUR-RM", "name": "Purchase - Raw Material", "kind": "purchase"},
        {"id": "L-PUR-CONS", "name": "Purchase - Consumables", "kind": "purchase"},
        {"id": "L-PUR-SVC", "name": "Purchase - Services", "kind": "purchase"},
        {"id": "L-CGST", "name": "Input CGST", "kind": "tax"},
        {"id": "L-SGST", "name": "Input SGST", "kind": "tax"},
        {"id": "L-IGST", "name": "Input IGST", "kind": "tax"},
    ]
    if extra_party:
        ledgers.append(
            {"id": "L-PARTY-2", "name": "Acme Components (Mumbai)", "gstin_masked": "27ABC…1Z0", "state_code": "27", "kind": "party"}
        )
    return {
        "company_id": "CO-DEMO",
        "home_state_code": home_state,
        "ledgers": ledgers,
        "cgst_ledger_id": "L-CGST",
        "sgst_ledger_id": "L-SGST",
        "igst_ledger_id": "L-IGST",
    }


def derived(lines, supplier_state, pos, rcm) -> dict:
    res = taxmod.tax_derive(
        supplier_state_code=supplier_state,
        place_of_supply_state_code=pos,
        reverse_charge=rcm,
        lines=[taxmod.TaxLine(Decimal(l["taxable_value"]), Decimal(l["gst_rate_pct"])) for l in lines],
    )
    return {
        "cgst": taxmod.fixed4(res.cgst),
        "sgst": taxmod.fixed4(res.sgst),
        "igst": taxmod.fixed4(res.igst),
        "total_tax": taxmod.fixed4(res.total_tax),
        "invoice_total": taxmod.fixed4(res.invoice_total),
        "taxable": taxmod.fixed4(res.taxable_total),
    }


def make(
    fid,
    description,
    *,
    supplier_state,
    pos,
    rcm,
    exempt,
    lines,
    inv_no="INV-2025-0042",
    inv_date="2025-05-11",
    ocr_quality="clean",
    classify=None,
    expected_status="ready",
    plant_pii=True,
    extra_ocr_lines=None,
    tenant_kwargs=None,
):
    d = derived(lines, supplier_state, pos, rcm)
    tn = tenant(**(tenant_kwargs or {}))
    if classify is None:
        classify = {"doc_type": "purchase_invoice", "is_purchase": True, "supplier_ledger_id": "L-PARTY-1"}

    planted = {
        "GSTIN": ["27ABCDE1234F1Z0"],
        "PAN": ["ABCDE1234F"],
        "AADHAAR": [aadhaar("12345678901")],
        "IFSC": ["HDFC0001234"],
    } if plant_pii else {}

    ocr_lines = [
        {"text": "TAX INVOICE", "bbox": [10, 10, 200, 30], "confidence": 0.99},
        {"text": f"Invoice No: {inv_no}", "bbox": [10, 40, 250, 60], "confidence": 0.98},
        {"text": f"Date: {inv_date}", "bbox": [10, 70, 200, 90], "confidence": 0.98},
        {"text": "Supplier: Acme Components Pvt Ltd", "bbox": [10, 100, 350, 120], "confidence": 0.97},
    ]
    if plant_pii:
        ocr_lines += [
            {"text": "GSTIN: 27ABCDE1234F1Z0", "bbox": [10, 130, 300, 150], "confidence": 0.96},
            {"text": "PAN: ABCDE1234F", "bbox": [10, 160, 250, 180], "confidence": 0.96},
            {"text": f"Aadhaar: {planted['AADHAAR'][0]}", "bbox": [10, 190, 320, 210], "confidence": 0.95},
            {"text": "Bank IFSC: HDFC0001234", "bbox": [10, 220, 320, 240], "confidence": 0.95},
        ]
    ocr_lines.append({"text": f"Place of Supply: {pos}", "bbox": [10, 250, 300, 270], "confidence": 0.97})
    for i, l in enumerate(lines):
        ocr_lines.append({
            "text": f"{l['description']} HSN {l.get('hsn','')} qty {l.get('qty','')} rate {l.get('rate','')} {l['taxable_value']} @ {l['gst_rate_pct']}%",
            "bbox": [10, 300 + i * 30, 500, 320 + i * 30],
            "confidence": 0.96,
        })
    if extra_ocr_lines:
        ocr_lines += extra_ocr_lines

    mean_conf = 0.55 if ocr_quality == "degraded" else round(
        sum(l["confidence"] for l in ocr_lines) / len(ocr_lines), 4
    )

    inferred = {
        "invoice_no": inv_no,
        "invoice_date": inv_date,
        "place_of_supply_state_code": pos,
        "reverse_charge": rcm,
        "exempt": exempt,
        "line_items": [
            {**l, "ledger_id": l.get("ledger_id"), "ledger_name": l["description"],
             "ledger_confidence": l.get("ledger_confidence", 0.95)}
            for l in lines
        ],
        "totals": {"taxable": d["taxable"], "invoice_total": d["invoice_total"]},
    }

    gt_lines = [
        {
            "description": l["description"], "hsn": l.get("hsn"),
            "qty": l.get("qty"), "rate": l.get("rate"),
            "taxable_value": l["taxable_value"], "gst_rate_pct": l["gst_rate_pct"],
            "ledger_id": l.get("ledger_id"),
        }
        for l in lines
    ]

    return {
        "id": fid,
        "description": description,
        "rulesetVersion": RULESET,
        "ocr_quality": ocr_quality,
        "input": {
            "pages": [{"png_b64": "", "width": 1240, "height": 1754, "dpi": 200}],
            "ocr": {"lines": ocr_lines, "mean_confidence": mean_conf},
            "inferred": inferred,
            "tenant": tn,
            "planted_pii": planted,
        },
        "classify": classify,
        "ground_truth": {
            "header": {
                "invoice_no": inv_no,
                "invoice_date": inv_date,
                "place_of_supply_state_code": pos,
                "reverse_charge": rcm,
                "party_ledger_id": classify.get("supplier_ledger_id"),
                "totals": {"taxable": d["taxable"], "invoice_total": d["invoice_total"]},
            },
            "line_items": gt_lines,
            "expected_status": expected_status,
            "derived_gst": {"cgst": d["cgst"], "sgst": d["sgst"], "igst": d["igst"]},
        },
    }


def line(desc, hsn, qty, rate, gst, ledger_id="L-PUR-RM"):
    taxable = f4(Decimal(str(qty)) * Decimal(str(rate)))
    return {
        "description": desc, "hsn": hsn, "qty": f4(qty), "rate": f4(rate),
        "taxable_value": taxable, "gst_rate_pct": f4(gst), "ledger_id": ledger_id,
    }


def build_all() -> list[dict]:
    fx: list[dict] = []

    # CLEAN: intra-state across slabs.
    for rate in ("5.00", "12.00", "18.00", "28.00"):
        fx.append(make(
            f"intra-state-{rate.split('.')[0]}-1line-clean",
            f"MH->MH purchase @{rate} -> CGST+SGST; clean scan",
            supplier_state="27", pos="27", rcm=False, exempt=False,
            lines=[line("Steel widgets", "8471", 2, 500, rate)],
        ))
    # CLEAN: inter-state IGST across slabs.
    for rate in ("5.00", "12.00", "18.00", "28.00"):
        fx.append(make(
            f"inter-state-{rate.split('.')[0]}-1line-clean",
            f"MH->KA purchase @{rate} -> full IGST; clean scan",
            supplier_state="27", pos="29", rcm=False, exempt=False,
            lines=[line("Imported module", "8473", 4, 250, rate)],
        ))
    # CLEAN: intra multi-line, multi-HSN.
    fx.append(make(
        "intra-state-multiline-multihsn-clean",
        "MH->MH 3 lines mixing 18/12/5; multi-HSN",
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[
            line("CPU board", "8471", 2, 500, "18.00", "L-PUR-RM"),
            line("Plastic casing", "3917", 5, 100, "12.00", "L-PUR-CONS"),
            line("Rice", "1001", 10, 200, "5.00", "L-PUR-CONS"),
        ],
    ))
    # CLEAN: inter-state multi-line.
    fx.append(make(
        "inter-state-multiline-clean",
        "MH->KA 2 lines @18 -> IGST",
        supplier_state="27", pos="29", rcm=False, exempt=False,
        lines=[
            line("Sensor array", "9027", 3, 400, "18.00", "L-PUR-RM"),
            line("Cabling", "8544", 20, 30, "18.00", "L-PUR-CONS"),
        ],
    ))
    # CLEAN: reverse charge.
    fx.append(make(
        "reverse-charge-18-clean",
        "RCM intra @18 -> tax computed, invoice_total excludes GST",
        supplier_state="27", pos="27", rcm=True, exempt=False,
        lines=[line("Legal services", "9982", 1, 1000, "18.00", "L-PUR-SVC")],
    ))
    # CLEAN: exempt / nil-rated.
    fx.append(make(
        "exempt-nil-rated-clean",
        "Exempt supply @0 -> no tax",
        supplier_state="27", pos="27", rcm=False, exempt=True,
        lines=[line("Fresh produce", "0701", 50, 20, "0.00", "L-PUR-CONS")],
    ))
    # CLEAN: registered dealer stationery.
    fx.append(make(
        "registered-dealer-intra-clean",
        "Intra @18 from a registered supplier; clean",
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[line("Stationery", "4820", 10, 50, "18.00", "L-PUR-CONS")],
    ))
    # CLEAN: rounding edge.
    fx.append(make(
        "intra-state-rounding-edge-clean",
        "Intra @18 exercising per-line half-up rounding",
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[line("Calibrated part", "9026", 1, 333.33, "18.00", "L-PUR-RM")],
    ))
    # CLEAN: services @18.
    fx.append(make(
        "intra-state-services-18-clean",
        "Intra services @18",
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[line("Maintenance contract", "9987", 1, 5000, "18.00", "L-PUR-SVC")],
    ))
    # CLEAN: 5-line mixed.
    fx.append(make(
        "intra-state-5line-clean",
        "Intra 5-line mixed",
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[
            line("Item A", "8471", 1, 100, "18.00", "L-PUR-RM"),
            line("Item B", "8472", 2, 150, "18.00", "L-PUR-RM"),
            line("Item C", "8473", 3, 200, "12.00", "L-PUR-CONS"),
            line("Item D", "8474", 4, 250, "12.00", "L-PUR-CONS"),
            line("Item E", "8475", 5, 300, "5.00", "L-PUR-CONS"),
        ],
    ))
    fx.append(make("inter-state-services-18-clean", "Inter services @18 -> IGST",
                   supplier_state="27", pos="29", rcm=False, exempt=False,
                   lines=[line("Consulting", "9983", 1, 8000, "18.00", "L-PUR-SVC")]))
    fx.append(make("intra-state-28-2line-clean", "Intra @28 two lines",
                   supplier_state="27", pos="27", rcm=False, exempt=False,
                   lines=[line("Luxury part", "8703", 1, 1000, "28.00", "L-PUR-RM"),
                          line("Accessory", "8708", 2, 500, "28.00", "L-PUR-CONS")]))
    fx.append(make("intra-state-12-2line-clean", "Intra @12 two lines",
                   supplier_state="27", pos="27", rcm=False, exempt=False,
                   lines=[line("Pipe fitting", "3917", 4, 125, "12.00", "L-PUR-RM"),
                          line("Gasket", "4016", 8, 40, "12.00", "L-PUR-CONS")]))
    fx.append(make("inter-state-5-2line-clean", "Inter @5 two lines -> IGST",
                   supplier_state="27", pos="32", rcm=False, exempt=False,
                   lines=[line("Grain", "1006", 100, 30, "5.00", "L-PUR-CONS"),
                          line("Pulses", "0713", 50, 60, "5.00", "L-PUR-CONS")]))

    # ===================== ADVERSARIAL (>=8) =====================

    fx.append(make(
        "adv-degraded-scan",
        "Degraded scan; low mean OCR confidence -> manual",
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[line("Blurry item", "8471", 2, 500, "18.00")],
        ocr_quality="degraded", expected_status="low_confidence_manual",
    ))
    fx.append(make(
        "adv-missing-gstin",
        (
        "No GSTIN present; no candidate supplier ledger at all -> the supplier's state is genuinely unknowable from this document, so the derivation reaches no intra conclusion and the whole tax sits in IGST. That is the no-conclusion DEFAULT, not a derived claim about this vendor: the amount is 180 either way and only the split is unasserted, which the draft says via unresolved 'supplier_state'. Contrast adv-ambiguous-supplier: candidates exist there and agree on a state, so it is resolved despite the ledger being unresolved. Ambiguous is not unknown."
        ),
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[line("Item", "8471", 2, 500, "18.00")],
        plant_pii=False,
        classify={"doc_type": "purchase_invoice", "is_purchase": True, "supplier": "none"},
        expected_status="low_confidence_manual",
    ))
    fx.append(make(
        "adv-ambiguous-supplier",
        (
        "Two party ledgers match -> no auto-select -> manual. Both candidates are the SAME vendor's ledgers and both sit in state 27, so which account to credit is unresolved but the supplier's STATE is not: the document's own place of supply is 27 too, and the split is the ordinary intra-state cgst 90 / sgst 90. This fixture previously expected igst 180 on the reasoning that an unmatched supplier has an unknown state — true when there is NO candidate (see adv-missing-gstin), false here, where every candidate agrees. 'supplier' is therefore NOT unresolved; only 'party' is."
        ),
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[line("Item", "8471", 2, 500, "18.00")],
        classify={"doc_type": "purchase_invoice", "is_purchase": True, "supplier": "ambiguous"},
        expected_status="low_confidence_manual",
        tenant_kwargs={"extra_party": True},
    ))
    bad = make(
        "adv-totals-dont-foot",
        "Echoed invoice_total disagrees with re-derived total -> manual",
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[line("Item", "8471", 2, 500, "18.00")],
        expected_status="low_confidence_manual",
    )
    bad["input"]["inferred"]["totals"]["invoice_total"] = "9999.0000"
    fx.append(bad)
    fx.append(make(
        "adv-non-purchase-doc",
        (
        "Delivery challan / quotation -> not a purchase -> manual, and NOTHING is posted. The derived_gst block below is therefore not a claim about this document's tax at all — a challan has none to claim. It records the no-conclusion default the pipeline reaches when there is no candidate supplier ledger (contrast adv-ambiguous-supplier, where candidates exist and agree, so the state IS known). The value is retained only so the oracle has something to check; a challan that carried no derived_gst at all would be more honest, and is tracked in HANDOFF §6."
        ),
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[line("Item", "8471", 2, 500, "18.00")],
        classify={"doc_type": "other", "is_purchase": False},
        expected_status="low_confidence_manual",
        extra_ocr_lines=[{"text": "DELIVERY CHALLAN (not for accounting)", "bbox": [10, 500, 400, 520], "confidence": 0.9}],
    ))
    partial = make(
        "adv-torn-partial-page",
        "Torn page: place of supply missing -> manual",
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[line("Item", "8471", 2, 500, "18.00")],
        expected_status="low_confidence_manual",
    )
    partial["input"]["inferred"]["place_of_supply_state_code"] = None
    fx.append(partial)
    fx.append(make(
        "adv-degraded-multiline",
        "Degraded multi-line -> manual",
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[line("A", "8471", 1, 100, "18.00"), line("B", "8472", 2, 200, "18.00")],
        ocr_quality="degraded", expected_status="low_confidence_manual",
    ))
    empty = make(
        "adv-no-line-items",
        "No line items extracted -> manual",
        supplier_state="27", pos="27", rcm=False, exempt=False,
        lines=[line("placeholder", "8471", 1, 100, "18.00")],
        expected_status="low_confidence_manual",
    )
    empty["input"]["inferred"]["line_items"] = []
    empty["ground_truth"]["line_items"] = []
    empty["ground_truth"]["derived_gst"] = {"cgst": "0.0000", "sgst": "0.0000", "igst": "0.0000"}
    fx.append(empty)

    # The no-conclusion GST default (c3aca5d): when there is NO candidate
    # supplier ledger the supplier's state is unknowable from the document, so
    # the derivation reaches no intra conclusion and the whole tax sits in IGST,
    # with the unasserted split named in expected_unresolved. Ambiguous is not
    # unknown: adv-ambiguous-supplier's candidates agree on state 27, so it
    # stays intra-state and only the party is unresolved. These three fixtures
    # were corrected by hand in July and this generator was not, so every CI
    # run regenerated the old expectations and failed validation against the
    # oracle; the generator now states what the fixtures state.
    by_id = {f["id"]: f for f in fx}
    no_candidate_default = {"cgst": "0.0000", "sgst": "0.0000", "igst": "180.0000"}
    for fid in ("adv-missing-gstin", "adv-non-purchase-doc"):
        by_id[fid]["ground_truth"]["derived_gst"] = dict(no_candidate_default)
        by_id[fid]["ground_truth"]["expected_unresolved"] = ["party", "supplier"]
    amb = by_id["adv-ambiguous-supplier"]
    amb["classify"]["supplier_candidates"] = ["L-PARTY-1", "L-PARTY-2"]
    amb["ground_truth"]["expected_unresolved"] = ["party"]

    return fx


def main() -> None:
    CORPUS_DIR.mkdir(parents=True, exist_ok=True)
    fixtures = build_all()
    for f in fixtures:
        (CORPUS_DIR / f"{f['id']}.json").write_text(json.dumps(f, indent=2) + "\n")
    clean = sum(1 for f in fixtures if f["ground_truth"]["expected_status"] == "ready")
    adv = len(fixtures) - clean
    print(f"wrote {len(fixtures)} fixtures to {CORPUS_DIR} ({clean} clean, {adv} adversarial)")


if __name__ == "__main__":
    main()
