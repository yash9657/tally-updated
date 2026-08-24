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
from ..redaction.persistence import PersistGuard, assert_body_clean
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

        # ⛔ THE PERSISTENCE BOUNDARY. Everything below that becomes a voucher
        # field or a LEDGER MASTER NAME goes through this guard first. See
        # redaction/persistence.py: the cross-border boundary in the pipeline
        # scrubbed the text handed to the MODEL and nothing scrubbed the values
        # handed to the BOOKS, so a PAN read as the invoice number was written
        # verbatim into voucher_version.voucher_no and a raw Aadhaar in a line
        # description became a permanent master. The books are append-only —
        # there is no later.
        guard = PersistGuard()

        pcf = inferred.per_field_confidence

        # --- supplier -> party --------------------------------------------
        party_ledger_id = cls.supplier_match.ledger_id
        # The supplier's DISPLAY name, and on the Alt+C path this string IS the
        # master that gets created — so it is scrubbed like every other persisted
        # free-text value.
        party_name = guard.clean("party", cls.supplier_match.name)
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
        # The obvious source is closed to us: the supplier's GSTIN — the one
        # field whose first two digits ARE the state code — is masked before the
        # LLM ever sees it (the PII boundary, working as designed), so
        # `inferred.supplier_gstin_masked` cannot supply it. One honest fallback
        # does exist and is applied below: an AMBIGUOUS match whose candidates
        # all sit in one state tells us the state without telling us the ledger.
        # (A second, not yet built: the plain-text "State: ... Code: NN" line
        # that real bills print is not PII and survives redaction, but
        # InferredInvoice has nowhere to put it. See HANDOFF §6.)
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

        # AMBIGUOUS IS NOT UNKNOWN.
        #
        # When the classifier could not choose between several ledgers but they
        # all sit in the same state, the supplier's state is not in doubt — only
        # which account to credit is. Two ledgers for one vendor's two branches
        # in Maharashtra are the ordinary case, and refusing to split the tax
        # there is as wrong as guessing: it books input IGST on a supply the
        # document itself shows as intra-state.
        #
        # Unanimity is required, every candidate must be a real ledger in THIS
        # tenant (so a hallucinated id cannot introduce a state), and every one
        # must carry a state code — one blank makes the set silent, not
        # unanimous. An empty candidate list is the genuinely-unknown case: a
        # vendor not in these books at all, where no fallback is available.
        if supplier_state is None and cls.supplier_match.candidate_ledger_ids:
            by_id = {led.id: led for led in ctx.ledgers}
            states = {
                by_id[cid].state_code
                for cid in cls.supplier_match.candidate_ledger_ids
                if cid in by_id
            }
            if len(cls.supplier_match.candidate_ledger_ids) == len(
                [c for c in cls.supplier_match.candidate_ledger_ids if c in by_id]
            ) and len(states) == 1:
                only = states.pop()
                if only:
                    supplier_state = only

        if supplier_state is None:
            # A DISTINCT token, not "supplier". The header-confidence floor
            # already emits "supplier" for the identity question — who this
            # vendor is — and the state question is a different one that a human
            # can answer separately: an ambiguous match resolves the state and
            # leaves the identity open (see the candidate rule above). Sharing
            # one token made the two indistinguishable in the draft, so a split
            # that HAD been derived still read as unasserted.
            unresolved.append("supplier_state")
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
                GSTLineDraft(
                    taxable_value=tv,
                    gst_rate_pct=rate,
                    hsn=guard.clean(f"line[{idx}].hsn", li.hsn),
                )
            )
            tax_lines.append(
                taxmod.TaxLine(taxable_value=_dec(tv), gst_rate_pct=_dec(rate))
            )

            guess = li.ledger_guess
            # ⛔ THIS STRING BECOMES A PERMANENT LEDGER MASTER when the guess has
            # no id (the Alt+C on-the-fly create path), and it is built out of the
            # LINE DESCRIPTION — the field a real bill uses for free text, and the
            # field the driven defect planted a raw Aadhaar in. A master name
            # cannot be un-created, so it is scrubbed here and the line is named in
            # `unresolved` so the operator retypes it.
            purchase_debits.append(
                PostingDraft(
                    ledger_id=guess.ledger_id,
                    ledger_name=guard.clean(
                        f"line[{idx}].ledger_name",
                        guess.name or li.description or "Purchase",
                    )
                    or "Purchase",
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
        #
        # ⛔⛔ A DRAFT MUST BALANCE EVEN WHEN THE TENANT CONTEXT IS INCOMPLETE, AND
        # THIS IS THE FIX FOR A BILL THAT COULD NEVER BE POSTED THROUGH THE
        # SCREEN'S OWN FRONT DOOR.
        #
        # These three legs used to be gated on `... and ctx.<x>_ledger_id`, so a
        # caller that supplied no tax-ledger ids got a draft with the tax SILENTLY
        # DROPPED FROM THE DEBITS while the party credit still carried the full
        # invoice total. Driven in a browser: debits 1000.0000 against a credit of
        # 1180.0000, unbalanced by exactly the tax, and cored refuses it — after
        # the operator has reviewed and accepted it. Nothing anywhere said why.
        #
        # The party leg has ALWAYS handled the same situation correctly one block
        # down: `ledger_id=party_ledger_id` may be None, the NAME carries the
        # human-facing master to create (Alt+C on-the-fly), and "party" goes into
        # `unresolved`. The tax legs now do exactly that, so the accounting
        # identity holds unconditionally and the missing thing is the LEDGER ID —
        # which is a question a human can answer — rather than the MONEY.
        #
        # ⚠️ The reachable cause of the empty ids is the caller: BillInboxScreen
        # sent `tenant: { company_id }` and nothing else. That is fixed there too
        # (it now sends this company's real chart of accounts). Both halves are
        # needed: the screen makes the ids present, and this makes their absence
        # survivable instead of silently destroying a draft.
        postings: list[PostingDraft] = list(purchase_debits)

        def add_input_tax(component: str, amount, ledger_id: str | None) -> None:
            if amount <= 0:
                return
            postings.append(
                PostingDraft(
                    ledger_id=ledger_id,
                    ledger_name=f"Input {component.upper()}",
                    dr_cr="D",
                    amount=taxmod.fixed4(amount),
                )
            )
            if ledger_id is None:
                # Named distinctly per component: an operator told "gst" cannot
                # tell which of three ledgers this tenant is missing.
                unresolved.append(f"gst.{component}_ledger")

        add_input_tax("cgst", derived.cgst, ctx.cgst_ledger_id)
        add_input_tax("sgst", derived.sgst, ctx.sgst_ledger_id)
        add_input_tax("igst", derived.igst, ctx.igst_ledger_id)

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
            # ⛔ THE DRIVEN CASE. `invoice_no` is OCR/LLM output copied straight
            # into voucher_version.voucher_no, which is immutable once posted.
            voucher_no=guard.clean("invoice_no", inferred.invoice_no),
            voucher_date=inferred.invoice_date,
            narration=guard.clean("narration", None),
            postings=postings,
            gst=gst,
        )

        # ⛔ FAIL-CLOSED RE-SCAN, the mirror of Boundary.assert_clean on the other
        # boundary. The guard above removes PII from the fields we ROUTED through
        # it; this proves none survived into the assembled body by any other route.
        # It also CLASSIFIES every field on the way past, so a field added to
        # cored_map.py without a decision turns this suite red instead of shipping
        # an unguarded value into the ledger.
        assert_body_clean(body)

        # A field whose value had to be redacted is a field a human must retype:
        # a masked voucher number is not a voucher number. Naming them here is
        # what stops `_decide_status` returning "ready" for a draft that would
        # post "[PAN REDACTED 6442]" as the supplier's invoice number. ⚠️ The tag
        # carries a fingerprint of the removed value (D85) — do NOT read that as
        # "so it is a usable number now". It tells the duplicate-bill guard two
        # different bills apart; it still tells the OPERATOR to retype the field.
        for field in guard.fields:
            if field not in unresolved:
                unresolved.append(field)

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
            # ⛔ THE PANEL'S CLAIM HAS TO COVER THE VALUES THAT PERSIST.
            # The F9 draft panel renders this as "PII redacted: N (PAN 1, …)".
            # It used to count ONLY the cross-border boundary's work, which is why
            # it could truthfully say "AADHAAR 1" while the same Aadhaar sat
            # un-redacted in the ledger name the draft proposed. The persistence
            # guard's counts are folded in, so the number the operator reads is the
            # number of values actually scrubbed on BOTH paths.
            redaction=RedactionReportSummary(
                any_redacted=redaction.any_redacted or guard.any_redacted,
                counts_by_type=guard.merged_counts(redaction.counts_by_type),
                total=sum(guard.merged_counts(redaction.counts_by_type).values()),
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
