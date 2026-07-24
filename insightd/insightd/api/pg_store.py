"""Postgres-backed DraftStore — drafts survive an insightd restart.

Same three-method surface as the in-memory ``DraftStore`` (``put`` /
``get`` / ``list_for_company``), so it drops straight into
``deps.get_draft_store`` with no route change. Still append-only proposals:
this module holds NO cored client and can never write a ledger row.

TENANT SCOPING IS STRUCTURAL, NOT ADVISORY
------------------------------------------
Every operation runs inside ONE transaction whose FIRST statement sets the
company GUC::

    SELECT set_config('app.current_company_id', %s, true)

The third argument ``true`` means *transaction-local* — this is the
parameterizable equivalent of ``SET LOCAL app.current_company_id = ...``
(``SET`` itself cannot take bind parameters), and it is exactly what cored
does in ``backend/internal/platform/db/db.go:153``. It is passed as a BOUND
PARAMETER, never string-concatenated: a company id is user-supplied input and
user input never becomes SQL text.

Migration ``0014_insightd_draft.sql`` then puts FORCE ROW LEVEL SECURITY on
``insightd_draft`` with ``USING``/``WITH CHECK`` pinned to that GUC, so the
database — not this file — is what makes another company's draft invisible.
The WHERE clauses below are belt-and-braces; deleting them would not leak a
row, and the test suite proves that by querying with no WHERE at all.

CONCURRENCY
-----------
The draft routes are sync ``def``s, so Starlette runs them in a worker
threadpool and concurrent requests land on this object from different threads.
A single shared psycopg connection is not thread-safe across concurrent
transactions, so each operation opens (and closes) its own short-lived
connection. That keeps the dependency set to ``psycopg[binary]`` alone — no
connection-pool extra.
"""

from __future__ import annotations

from typing import Any

from ..contracts.draft import PurchaseVoucherDraft

# The company GUC read by 0014's tenant_isolation policy. Same name cored uses.
_COMPANY_GUC = "app.current_company_id"

# ``true`` => transaction-local (== SET LOCAL): the setting is reverted when the
# transaction ends, so a recycled connection can never inherit another tenant's
# scope.
_SET_COMPANY_SQL = f"SELECT set_config('{_COMPANY_GUC}', %s, true)"

_INSERT_SQL = """
INSERT INTO insightd_draft (company_id, draft_id, body)
VALUES (%s, %s, %s)
ON CONFLICT (company_id, draft_id) DO UPDATE SET body = EXCLUDED.body
"""

_SELECT_ONE_SQL = """
SELECT body FROM insightd_draft
WHERE company_id = %s AND draft_id = %s
"""

# PG has no implicit row order; the in-memory store returns insertion order, so
# pin an explicit one. (company_id, draft_id) is the PK, so this needs no extra
# index beyond a small sort.
_SELECT_LIST_SQL = """
SELECT body FROM insightd_draft
WHERE company_id = %s
ORDER BY created_at, draft_id
"""

_MISSING_TABLE_HINT = (
    "insightd_draft table is missing — INSIGHTD_DATABASE_URL is set but the "
    "schema has not been migrated. Run: cd backend && go run ./cmd/cored migrate"
)


class PGDraftStore:
    """RLS-scoped, durable, append-only draft store.

    Duck-typed against ``store.DraftStore`` (FastAPI does not validate what a
    ``Depends`` returns), so it deliberately does not inherit from it — the
    in-memory class owns a ``threading.Lock`` and dicts this one must not.
    """

    def __init__(self, dsn: str) -> None:
        if not dsn:
            raise ValueError("PGDraftStore requires a non-empty DSN")
        self._dsn = dsn

    # -- plumbing -----------------------------------------------------------

    def _connect(self):
        """Open a fresh connection. psycopg is imported HERE, at call time, so
        the module can be imported (and AST-linted) with no db deps installed."""
        import psycopg  # noqa: PLC0415 - lazy by design; see module docstring

        return psycopg.connect(self._dsn)

    def verify_ready(self) -> None:
        """Fail LOUD at construction rather than silently losing durability.

        A configured DSN whose schema is not migrated must not degrade to the
        in-memory store — that would look healthy and drop every draft on
        restart. Raises RuntimeError with the exact fix.
        """
        with self._connect() as conn:
            with conn.cursor() as cur:
                cur.execute("SELECT to_regclass('public.insightd_draft')")
                row = cur.fetchone()
                if row is None or row[0] is None:
                    raise RuntimeError(_MISSING_TABLE_HINT)

    # -- the DraftStore surface ---------------------------------------------

    def put(self, draft: PurchaseVoucherDraft) -> None:
        """Upsert a draft. RLS WITH CHECK refuses a row whose company_id is not
        the GUC, so a mis-scoped write raises rather than landing cross-tenant."""
        body_json = draft.model_dump_json()  # jsonb accepts the JSON text
        with self._connect() as conn:
            with conn.cursor() as cur:
                cur.execute(_SET_COMPANY_SQL, (draft.company_id,))
                cur.execute(
                    _INSERT_SQL, (draft.company_id, draft.draft_id, body_json)
                )

    def get(self, company_id: str, draft_id: str) -> PurchaseVoucherDraft | None:
        """Return the draft, or None when it does not exist *for this company*.

        Cross-company is indistinguishable from not-found (the RLS policy makes
        the row invisible), which is exactly the in-memory contract the 404 in
        routes_drafts.py depends on.
        """
        with self._connect() as conn:
            with conn.cursor() as cur:
                cur.execute(_SET_COMPANY_SQL, (company_id,))
                cur.execute(_SELECT_ONE_SQL, (company_id, draft_id))
                row = cur.fetchone()
        if row is None:
            return None
        return _to_draft(row[0])

    def list_for_company(self, company_id: str) -> list[PurchaseVoucherDraft]:
        """All drafts visible to this company, oldest first. Never a leak: an
        unknown company yields [] (an empty collection, not an error)."""
        with self._connect() as conn:
            with conn.cursor() as cur:
                cur.execute(_SET_COMPANY_SQL, (company_id,))
                cur.execute(_SELECT_LIST_SQL, (company_id,))
                rows = cur.fetchall()
        return [_to_draft(r[0]) for r in rows]


def _to_draft(body: Any) -> PurchaseVoucherDraft:
    """Rehydrate a stored body.

    psycopg auto-decodes ``jsonb`` to a Python dict, so this is
    ``model_validate`` (dict) rather than ``model_validate_json`` (str). The
    model is ``extra='forbid'``, so a schema drift fails here loudly instead of
    silently dropping fields.
    """
    if isinstance(body, (str, bytes, bytearray)):  # defensive: text-mode driver
        return PurchaseVoucherDraft.model_validate_json(body)
    return PurchaseVoucherDraft.model_validate(body)
