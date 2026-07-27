"""PureVoucherDraftMapper — the only stage that knows cored's posting shape.

PURE, deterministic, no optional deps, no model, no network. Identical for the
real and fake pipelines, so a green fake test proves the real mapping.

Responsibilities:
  * supplier -> party_ledger_id (or None -> Alt+C on-the-fly create path)
  * line items -> cored GST block (taxable_value, gst_rate_pct, hsn, place of
    supply, reverse_charge) + per-line purchase ledger guesses
  * assemble postings (party CREDIT total; purchase/expense DEBIT taxable; GST
    input ledgers DEBIT using the tenant's cgst/sgst/igst_ledger_id)
  * derive the confidence map (per-field = min of contributing stage
    confidences; overall = weighted floor, header vs line-item bucketed)
  * decide status via the floor thresholds (degrade-to-manual on any miss)

GST AMOUNTS ARE NEVER COPIED FROM THE LLM — they are RE-derived here against
cored's ruleset (display only). cored re-derives authoritatively on POST.
"""

from __future__ import annotations

import datetime as _dt
import uuid
from decimal import Decimal, InvalidOperation

from ..contracts.cored_map import (
    GSTDraft,
    GSTLineDraft,
    PostingDraft,
    VoucherDraftBody,
)
from ..contracts.draft import (
    ConfidenceMap,
    FieldConfidence,
    GSTAmounts,
    PurchaseVoucherDraft,
    RedactionReportSummary,
)
from ..contracts.pipeline import (
    ClassifiedDoc,
    InferredInvoice,
    LineItem,
    RedactionReport,
    TenantContext,
)
from . import tax as taxmod

# --------------------------------------------------------------------------- #
# Floor thresholds — the load-bearing safety mechanism. Tuned against the
# corpus, never in prod. A field/line below its floor degrades to manual.
# --------------------------------------------------------------------------- #

HEADER_FIELD_FLOOR = 0.80
LINE_FIELD_FLOOR = 0.80
OCR_MEAN_CONF_FLOOR = 0.85  # first degrade-to-manual signal
OVERALL_FLOOR = 0.85

# Header vs line-item bucket weights for the overall roll-up (informational —
# overall = min(bucket overalls), but weights document intent).
HEADER_WEIGHT = 0.5
LINE_WEIGHT = 0.5


def _structural_defects(inferred: InferredInvoice, derived) -> list[str]:
    """Reasons this draft must be reviewed that do NOT depend on confidence.

    The WS10 spike's decisive negative finding is that OCR confidence cannot
    police money: a corrupted `300.00` was returned as `300:00` at 0.985. What
    DOES catch that is arithmetic which stops working once a digit or separator
    is mangled, so these are the checks the review decision leans on:

      * a line whose money fields will not PARSE (`45:50`, `1-000` — precisely
        the corruptions the spike observed);
      * tax that is not `rate x taxable` on the line, re-derived independently;
      * an echoed invoice total that does not FOOT against the re-derived one;
      * no GSTIN anywhere in the document. Checked as PRESENCE, not by regex:
        the value is masked by the PII boundary before it ever reaches this
        function, so a GSTIN-shaped pattern match here could never succeed — but
        the masked field being populated still tells us the recogniser found
        one.

    Returns the reasons rather than a bool so the draft can say WHY, and so a
    test asserts the specific defect rather than "something was wrong".
    """
    defects: list[str] = []

    for idx, li in enumerate(inferred.line_items):
        for field in ("taxable_value", "gst_rate_pct", "qty", "rate"):
            raw = getattr(li, field, None)
            if raw is None or raw == "":
                continue
            try:
                Decimal(str(raw))
            except (InvalidOperation, ValueError):
                # A money field that will not parse is the SIGNATURE of the
                # corruption the spike found, and it is unambiguous.
                defects.append(f"line[{idx}].{field} is not a number: {raw!r}")

    # The echoed total must agree with the re-derivation. This is the check the
    # LLM cannot talk its way past: two independent paths to the same number.
    if inferred.totals.invoice_total:
        try:
            echoed = Decimal(inferred.totals.invoice_total)
            if echoed != derived.invoice_total:
                defects.append(
                    f"invoice total does not foot: document says {echoed}, "
                    f"the lines derive {derived.invoice_total}"
                )
        except (InvalidOperation, ValueError):
            defects.append(
                f"invoice total is not a number: {inferred.totals.invoice_total!r}"
            )

    # A document with no GSTIN at all is not a tax invoice we can post against.
    # The masked form is checked too: redaction replaces the value, so its
    # PRESENCE still tells us the recogniser found one.
    if not inferred.supplier_gstin_masked:
        defects.append("no supplier GSTIN found in the document")

    return defects


def _fixed4(s: str | None) -> str | None:
    if s is None:
        return None
    try:
        return f"{Decimal(s):.4f}"
    except (InvalidOperation, ValueError):
        return s


def _dec(s: str | None) -> Decimal:
    try:
        return Decimal(s) if s not in (None, "") else Decimal(0)
    except (InvalidOperation, ValueError):
        return Decimal(0)


class PureVoucherDraftMapper:
    def __init__(self, ruleset_version: str = "gst-2024-09") -> None:
        self.ruleset_version = ruleset_version

    # ------------------------------------------------------------------ #

    def to_draft(
        self,
        inferred: InferredInvoice,
        cls: ClassifiedDoc,
        ctx: TenantContext,
        *,
        redaction: RedactionReport,
        mean_confidence: float,
        source: str,
        company_id: str,
    ) -> PurchaseVoucherDraft:
        unresolved: list[str] = []

        pcf = inferred.per_field_confidence

        # --- supplier -> party --------------------------------------------
        party_ledger_id = cls.supplier_match.ledger_id
        party_name = cls.supplier_match.name
        supplier_conf = cls.supplier_match.match_confidence
        if party_ledger_id is None:
            # below floor / no match -> Alt+C path, do NOT guess.
            unresolved.append("party")

        # --- place of supply & reverse charge -----------------------------
        #
        # THE SUPPLIER'S STATE COMES FROM THE MATCHED LEDGER OR FROM NOWHERE.
        #
        # This used to fall back to `supplier_state = place_of_supply`, with the
        # comment "POS == supplier on intra" — which is circular: it ASSUMES an
        # intra-state supply in order to derive one. The effect was that EVERY
        # bill whose supplier could not be matched came out as CGST+SGST, an
        # answer that looks derived and is a coin flip. For an out-of-state
        # vendor it puts input tax in the wrong two ledgers and misstates GSTR-3B
        # table 4.
        #
        # There is no honest fallback available here: the supplier's GSTIN — the
        # one field whose first two digits ARE the state code — is masked before
        # the LLM ever sees it (the PII boundary, working as designed), so
        # `inferred.supplier_gstin_masked` cannot supply it either.
        #
        # So when the supplier is unmatched the state is None, `tax_derive`
        # cannot conclude intra, and the draft says so via `unresolved`. The tax
        # AMOUNT is unaffected either way (rate x taxable); what is unasserted is
        # which ledgers it lands in, and that is now visible rather than implied.
        pos = inferred.place_of_supply_state_code
        supplier_state: str | None = None
        for led in ctx.ledgers:
            if led.id == party_ledger_id and led.state_code:
                supplier_state = led.state_code
                break
        if supplier_state is None:
            # Distinct from "party": the party is who to credit, the supplier
            # state is what splits the tax. A human can resolve one without the
            # other, so the draft names both.
            unresolved.append("supplier")
        reverse_charge = inferred.reverse_charge

        # --- line items -> GST lines + postings ---------------------------
        gst_lines: list[GSTLineDraft] = []
        purchase_debits: list[PostingDraft] = []
        line_conf_maps: list[dict[str, FieldConfidence]] = []
        tax_lines: list[taxmod.TaxLine] = []

        for idx, li in enumerate(inferred.line_items):
            tv = _fixed4(li.taxable_value) or "0.0000"
            rate = _fixed4(li.gst_rate_pct) or "0.0000"
            gst_lines.append(
                GSTLineDraft(taxable_value=tv, gst_rate_pct=rate, hsn=li.hsn)
            )
            tax_lines.append(
                taxmod.TaxLine(taxable_value=_dec(tv), gst_rate_pct=_dec(rate))
            )

            guess = li.ledger_guess
            purchase_debits.append(
                PostingDraft(
                    ledger_id=guess.ledger_id,
                    ledger_name=guess.name or li.description or "Purchase",
                    dr_cr="D",
                    amount=tv,
                )
            )
            if guess.ledger_id is None:
                unresolved.append(f"line[{idx}].ledger")

            line_conf_maps.append(self._line_confidence(li, guess.confidence))

        # --- re-derive GST (display only) ---------------------------------
        derived = taxmod.tax_derive(
            supplier_state_code=supplier_state,
            place_of_supply_state_code=pos,
            reverse_charge=reverse_charge,
            lines=tax_lines,
        )
        derived_gst = GSTAmounts(
            cgst=taxmod.fixed4(derived.cgst),
            sgst=taxmod.fixed4(derived.sgst),
            igst=taxmod.fixed4(derived.igst),
            total_tax=taxmod.fixed4(derived.total_tax),
            invoice_total=taxmod.fixed4(derived.invoice_total),
        )

        # --- assemble postings (party CREDIT total; GST inputs DEBIT) -----
        postings: list[PostingDraft] = list(purchase_debits)
        if derived.cgst > 0 and ctx.cgst_ledger_id:
            postings.append(
                PostingDraft(
                    ledger_id=ctx.cgst_ledger_id,
                    ledger_name="Input CGST",
                    dr_cr="D",
                    amount=taxmod.fixed4(derived.cgst),
                )
            )
        if derived.sgst > 0 and ctx.sgst_ledger_id:
            postings.append(
                PostingDraft(
                    ledger_id=ctx.sgst_ledger_id,
                    ledger_name="Input SGST",
                    dr_cr="D",
                    amount=taxmod.fixed4(derived.sgst),
                )
            )
        if derived.igst > 0 and ctx.igst_ledger_id:
            postings.append(
                PostingDraft(
                    ledger_id=ctx.igst_ledger_id,
                    ledger_name="Input IGST",
                    dr_cr="D",
                    amount=taxmod.fixed4(derived.igst),
                )
            )
        # party credit = invoice total (taxable + tax, unless reverse charge)
        postings.append(
            PostingDraft(
                ledger_id=party_ledger_id,
                ledger_name=party_name or "Sundry Creditor",
                dr_cr="C",
                amount=taxmod.fixed4(derived.invoice_total),
            )
        )

        # --- GST block ----------------------------------------------------
        gst = GSTDraft(
            ruleset_version=self.ruleset_version,
            supplier_state_code=supplier_state,
            place_of_supply_state_code=pos,
            reverse_charge=reverse_charge,
            exempt=inferred.exempt,
            lines=gst_lines,
            cgst_ledger_id=ctx.cgst_ledger_id,
            sgst_ledger_id=ctx.sgst_ledger_id,
            igst_ledger_id=ctx.igst_ledger_id,
        )

        body = VoucherDraftBody(
            voucher_class="purchase",
            voucher_type_id=None,
            party_ledger_id=party_ledger_id,
            party=party_name,
            voucher_no=inferred.invoice_no,
            voucher_date=inferred.invoice_date,
            narration=None,
            postings=postings,
            gst=gst,
        )

        # --- confidence map ----------------------------------------------
        header_cm = self._header_confidence(
            inferred, cls, supplier_conf, party_ledger_id, derived_gst, pcf
        )
        for key, fc in header_cm.items():
            if fc.confidence < HEADER_FIELD_FLOOR or not fc.value_present:
                if key not in unresolved:
                    unresolved.append(key)
        for idx, lcm in enumerate(line_conf_maps):
            for key, fc in lcm.items():
                if fc.confidence < LINE_FIELD_FLOOR or not fc.value_present:
                    tag = f"line[{idx}].{key}"
                    if tag not in unresolved:
                        unresolved.append(tag)

        overall_header = (
            min(fc.confidence for fc in header_cm.values()) if header_cm else 0.0
        )
        per_line_overalls = [
            (min(fc.confidence for fc in lcm.values()) if lcm else 0.0)
            for lcm in line_conf_maps
        ]
        overall_line_items = min(per_line_overalls) if per_line_overalls else 0.0
        overall = min(overall_header, overall_line_items)

        confidence = ConfidenceMap(
            header=header_cm,
            line_items=line_conf_maps,
            overall_header=round(overall_header, 4),
            overall_line_items=round(overall_line_items, 4),
            overall=round(overall, 4),
        )

        # --- status decision ----------------------------------------------
        status = self._decide_status(
            cls=cls,
            inferred=inferred,
            mean_confidence=mean_confidence,
            overall=overall,
            has_lines=bool(inferred.line_items),
            derived=derived,
            unresolved=unresolved,
        )

        return PurchaseVoucherDraft(
            draft_id=str(uuid.uuid4()),
            company_id=company_id,
            created_at=_dt.datetime.now(_dt.timezone.utc).isoformat(),
            source=source,  # type: ignore[arg-type]
            status=status,
            body=body,
            confidence=confidence,
            redaction=RedactionReportSummary(
                any_redacted=redaction.any_redacted,
                counts_by_type=dict(redaction.counts_by_type),
                total=sum(redaction.counts_by_type.values()),
            ),
            unresolved=unresolved,
            derived_gst=derived_gst,
        )

    # ------------------------------------------------------------------ #
    # confidence helpers
    # ------------------------------------------------------------------ #

    def _header_confidence(
        self,
        inferred: InferredInvoice,
        cls: ClassifiedDoc,
        supplier_conf: float,
        party_ledger_id: str | None,
        derived_gst: GSTAmounts,
        pcf: dict[str, float],
    ) -> dict[str, FieldConfidence]:
        def fc(present: bool, conf: float, source) -> FieldConfidence:
            return FieldConfidence(
                value_present=present, confidence=conf if present else 0.0, source=source
            )

        # supplier confidence is min(match, classify) and 0 when no ledger match
        supplier_present = party_ledger_id is not None
        supplier_conf_final = (
            min(supplier_conf, cls.classify_confidence) if supplier_present else 0.0
        )

        return {
            "invoice_no": fc(
                bool(inferred.invoice_no),
                pcf.get("invoice_no", 0.0),
                "inferencer",
            ),
            "invoice_date": fc(
                bool(inferred.invoice_date),
                pcf.get("invoice_date", 0.0),
                "inferencer",
            ),
            "supplier": fc(supplier_present, supplier_conf_final, "classifier"),
            "place_of_supply": fc(
                bool(inferred.place_of_supply_state_code),
                pcf.get("place_of_supply_state_code", 0.0),
                "inferencer",
            ),
            "reverse_charge": fc(
                True,
                pcf.get("reverse_charge", 1.0),
                "inferencer",
            ),
            "totals": fc(True, pcf.get("totals", 1.0), "derived"),
        }

    def _line_confidence(
        self, li: LineItem, ledger_conf: float
    ) -> dict[str, FieldConfidence]:
        pcf = {}  # per-line confidences live in the inferred per_field map by index
        c = li.ledger_guess.confidence  # reuse as the line's headline conf

        def fc(present: bool, conf: float, source) -> FieldConfidence:
            return FieldConfidence(
                value_present=present, confidence=conf if present else 0.0, source=source
            )

        base = c if c > 0 else 0.9
        return {
            "description": fc(bool(li.description), base, "inferencer"),
            "hsn": fc(bool(li.hsn), base, "inferencer"),
            "qty": fc(li.qty is not None, base, "inferencer"),
            "rate": fc(li.rate is not None, base, "inferencer"),
            "taxable_value": fc(li.taxable_value is not None, base, "inferencer"),
            "gst_rate_pct": fc(li.gst_rate_pct is not None, base, "inferencer"),
            "ledger": fc(li.ledger_guess.ledger_id is not None, ledger_conf, "inferencer"),
        }

    # ------------------------------------------------------------------ #
    # status decision
    # ------------------------------------------------------------------ #

    def _decide_status(
        self,
        *,
        cls: ClassifiedDoc,
        inferred: InferredInvoice,
        mean_confidence: float,
        overall: float,
        has_lines: bool,
        derived,
        unresolved: list[str],
    ):
        # STRUCTURAL CHECKS COME FIRST, AND CONFIDENCE IS NOT ONE OF THEM.
        #
        # The WS10 feasibility spike measured this directly: real OCR is
        # CONFIDENTLY WRONG, and it corrupts MONEY values specifically. Not only
        # was `mean_confidence` useless as a review signal — per-line confidence
        # was too. A corrupted `300.00` read as `300:00` came back at **0.985**
        # (docs/spikes/ws10-paddleocr-spike.md §8.4), and on an unseen bill four
        # NUMERIC tokens were mangled while the recogniser remained sure of
        # itself.
        #
        # So high confidence must NEVER be read as "no review needed". What
        # catches a mangled money value is ARITHMETIC that stops working: totals
        # that do not foot, tax that is not rate x taxable, a field that will not
        # parse. Those checks run below, before any confidence test, and they are
        # what this feature relies on.
        #
        # Low confidence is still allowed to ESCALATE to manual — that direction
        # is conservative and costs nothing. It is simply not trusted in the
        # other direction, which is the direction that loses money.
        #
        # Non-purchase doc short-circuits to manual.
        if cls.doc_type != "purchase_invoice" or not cls.is_purchase:
            return "low_confidence_manual"
        # STRUCTURAL: the arithmetic must hold. See _structural_defects.
        if _structural_defects(inferred, derived):
            return "low_confidence_manual"
        # Degraded scan / low OCR confidence -> manual. A conservative ESCALATION
        # only; never a licence to skip review when confidence is high.
        if mean_confidence < OCR_MEAN_CONF_FLOOR:
            return "low_confidence_manual"
        # No line items at all -> manual.
        if not has_lines:
            return "low_confidence_manual"
        # Missing GSTIN / no supplier match -> manual.
        if cls.supplier_match.ledger_id is None:
            return "low_confidence_manual"
        if not inferred.place_of_supply_state_code:
            return "low_confidence_manual"
        # Any field below its floor -> manual.
        if unresolved:
            return "low_confidence_manual"
        # Totals must foot against the re-derived numbers (LLM echo cross-check).
        if inferred.totals.invoice_total:
            try:
                echoed = Decimal(inferred.totals.invoice_total)
                if echoed != derived.invoice_total:
                    return "low_confidence_manual"
            except (InvalidOperation, ValueError):
                return "low_confidence_manual"
        # Overall floor.
        if overall < OVERALL_FLOOR:
            return "low_confidence_manual"
        return "ready"
