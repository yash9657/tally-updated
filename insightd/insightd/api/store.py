"""The draft store — append-only proposals, never ledger rows.

Drafts are per-company scoped (RLS-style): a draft_id from another company_id is
invisible (returns None). In-memory by default; a Postgres backend can implement
the same tiny interface. The store NEVER calls cored and holds no write client.
"""

from __future__ import annotations

import threading

from ..contracts.draft import PurchaseVoucherDraft


class DraftStore:
    """In-memory, per-company-scoped, append-only."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        # company_id -> draft_id -> draft
        self._by_company: dict[str, dict[str, PurchaseVoucherDraft]] = {}

    def put(self, draft: PurchaseVoucherDraft) -> None:
        with self._lock:
            self._by_company.setdefault(draft.company_id, {})[draft.draft_id] = draft

    def get(self, company_id: str, draft_id: str) -> PurchaseVoucherDraft | None:
        with self._lock:
            return self._by_company.get(company_id, {}).get(draft_id)

    def list_for_company(self, company_id: str) -> list[PurchaseVoucherDraft]:
        with self._lock:
            return list(self._by_company.get(company_id, {}).values())


_store: DraftStore | None = None


def get_store() -> DraftStore:
    global _store
    if _store is None:
        _store = DraftStore()
    return _store
