"""PGDraftStore — durability + STRUCTURAL tenant isolation (RLS), plus the
import-lint that keeps psycopg optional.

Two independent groups:

1. **Import-lint** (always runs, needs no db, no psycopg): proves psycopg is
   never imported at module top level anywhere in the package, and that
   importing the app + ``pg_store`` itself leaves ``psycopg`` out of
   ``sys.modules``. These stay non-vacuous even on a machine where psycopg IS
   installed — an "import succeeds" test would not.

2. **Live PG tests** (skip cleanly when psycopg is absent, PG is unreachable,
   the table is unmigrated, or ``CORED_SKIP_DB`` is set — mirroring the Go
   connect-or-skip discipline, e.g. ``backend/internal/authz/store_test.go``).

The isolation oracle is INDEPENDENT of the store: cross-company assertions are
made with raw psycopg queries carrying **no WHERE clause at all**, so they fail
if and only if the ``tenant_isolation`` policy in
``backend/migrations/0014_insightd_draft.sql`` is missing or wrong. Asserting
only "company B's get() returns None" would be vacuous — the in-memory store
does that too, with no database involved.
"""

from __future__ import annotations

import ast
import os
import pathlib
import subprocess
import sys
import threading
import uuid

import pytest
from fastapi.testclient import TestClient

from insightd.api.app import create_app
from insightd.api.deps import get_draft_store
from insightd.contracts.cored_map import VoucherDraftBody
from insightd.contracts.draft import (
    ConfidenceMap,
    FieldConfidence,
    PurchaseVoucherDraft,
    RedactionReportSummary,
)

from conftest import load_fixture

PKG = pathlib.Path(__file__).resolve().parent.parent / "insightd"

# Same app-role DSN every Go PG test hard-codes (NOBYPASSRLS role — RLS must
# actually apply). Overridable for a different environment.
DEFAULT_DSN = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"

COMPANY_GUC_SQL = "SELECT set_config('app.current_company_id', %s, true)"


# ---------------------------------------------------------------------------
# 1. Import-lint — psycopg must stay OPTIONAL (runs with or without it)
# ---------------------------------------------------------------------------


def _module_sources():
    for p in PKG.rglob("*.py"):
        yield p, p.read_text()


def test_no_top_level_psycopg_import():
    """psycopg must never be imported at module top level: the core package has
    to import cleanly with only requirements.txt installed (which is exactly
    what CI does). Mirrors tests/test_hitl_invariant.py's heavy-dep lint."""
    db_deps = {"psycopg", "psycopg2", "psycopg_pool", "sqlalchemy", "asyncpg"}
    offenders = []
    for path, src in _module_sources():
        tree = ast.parse(src, filename=str(path))
        for node in tree.body:  # module-level statements only
            names = []
            if isinstance(node, ast.Import):
                names = [a.name.split(".")[0] for a in node.names]
            elif isinstance(node, ast.ImportFrom) and node.module:
                names = [node.module.split(".")[0]]
            for n in names:
                if n in db_deps:
                    offenders.append(f"{path.name}: top-level import of {n}")
    assert not offenders, offenders


def test_importing_app_and_pg_store_does_not_load_psycopg():
    """Environment-independent proof that the lazy import is real: in a FRESH
    interpreter, importing the API app *and* pg_store must leave psycopg absent
    from sys.modules. Non-vacuous even with psycopg installed."""
    code = (
        "import sys;"
        "import insightd.api.app, insightd.api.deps, insightd.api.pg_store;"
        "assert 'psycopg' not in sys.modules, sorted(m for m in sys.modules"
        " if 'psycopg' in m);"
        "print('clean')"
    )
    proc = subprocess.run(
        [sys.executable, "-c", code],
        cwd=str(PKG.parent),
        capture_output=True,
        text=True,
    )
    assert proc.returncode == 0, proc.stderr
    assert "clean" in proc.stdout


def test_default_settings_select_the_in_memory_store():
    """With no INSIGHTD_DATABASE_URL, the dependency must return the in-memory
    singleton — the zero-config path CI relies on."""
    import insightd.api.deps as deps_mod
    import insightd.config as config_mod
    from insightd.api.store import DraftStore, get_store

    monkey = pytest.MonkeyPatch()
    try:
        monkey.setattr(config_mod, "_settings", config_mod.Settings(database_url=None))
        monkey.setattr(deps_mod, "_pg_store", None)
        store = deps_mod.get_draft_store()
    finally:
        monkey.undo()
    assert isinstance(store, DraftStore)
    assert store is get_store()


# ---------------------------------------------------------------------------
# 2. Live PG tests — connect-or-skip
# ---------------------------------------------------------------------------


@pytest.fixture(scope="module")
def dsn() -> str:
    if os.environ.get("CORED_SKIP_DB") or os.environ.get("INSIGHTD_SKIP_DB"):
        pytest.skip("CORED_SKIP_DB/INSIGHTD_SKIP_DB set")
    psycopg = pytest.importorskip("psycopg", reason="psycopg not installed (requirements-db.txt)")
    value = os.environ.get("INSIGHTD_DATABASE_URL") or DEFAULT_DSN
    try:
        with psycopg.connect(value, connect_timeout=3) as conn:
            with conn.cursor() as cur:
                cur.execute("SELECT to_regclass('public.insightd_draft')")
                if cur.fetchone()[0] is None:
                    pytest.skip(
                        "insightd_draft missing — run: cd backend && "
                        "go run ./cmd/cored migrate"
                    )
    except pytest.skip.Exception:
        raise
    except Exception as exc:  # unreachable PG -> skip, never fail (Go idiom)
        pytest.skip(f"postgres unreachable at {value}: {exc}")
    return value


def _company() -> str:
    """A fresh tenant per test, so the no-WHERE oracles below only ever see rows
    this test wrote (and the suite is safe to re-run / run in parallel)."""
    return f"CO-TEST-{uuid.uuid4()}"


def _store(dsn: str):
    from insightd.api.pg_store import PGDraftStore

    return PGDraftStore(dsn)


def _draft(draft_id: str, company_id: str, party: str = "Acme Traders"):
    """A minimal hand-built draft (copied from tests/test_drafts_list.py) — no
    pipeline needed, and every field is pinned so a round-trip is checkable."""
    return PurchaseVoucherDraft(
        draft_id=draft_id,
        company_id=company_id,
        created_at="2026-06-14T00:00:00Z",
        source="upload",
        status="low_confidence_manual",
        body=VoucherDraftBody(party=party, postings=[]),
        confidence=ConfidenceMap(
            # a NESTED model, so the round-trip test also proves nested
            # structures survive the jsonb hop intact
            header={
                "supplier": FieldConfidence(
                    value_present=True, confidence=0.42, source="ocr"
                )
            },
            line_items=[],
            overall_header=0.4,
            overall_line_items=0.3,
            overall=0.3,
        ),
        redaction=RedactionReportSummary(any_redacted=False, counts_by_type={}, total=0),
        unresolved=["supplier"],
        derived_gst=None,
    )


# -- independent RLS oracles (raw psycopg, deliberately no WHERE clause) -----


def _raw_visible_count(dsn: str, company_id: str | None) -> int:
    """Rows the DATABASE lets this tenant see, with NO WHERE clause: purely the
    RLS policy's answer. company_id=None means "GUC never set"."""
    import psycopg

    with psycopg.connect(dsn) as conn:
        with conn.cursor() as cur:
            if company_id is not None:
                cur.execute(COMPANY_GUC_SQL, (company_id,))
            cur.execute("SELECT count(*) FROM insightd_draft")
            return cur.fetchone()[0]


def test_rls_hides_other_companies_rows_without_any_where_clause(dsn):
    """THE isolation gate. Two tenants each hold one draft; an unfiltered
    count must return exactly 1 for each, 0 for a third tenant, and 0 with no
    GUC set. Drop the tenant_isolation policy and every one of these fails."""
    a, b, c = _company(), _company(), _company()
    store = _store(dsn)
    store.put(_draft("D-A", a))
    store.put(_draft("D-B", b))

    assert _raw_visible_count(dsn, a) == 1
    assert _raw_visible_count(dsn, b) == 1
    assert _raw_visible_count(dsn, c) == 0
    # unset GUC and empty GUC both deny (nullif('') -> NULL, and the column has
    # CHECK (company_id <> '') so no stored row can ever match it)
    assert _raw_visible_count(dsn, None) == 0
    assert _raw_visible_count(dsn, "") == 0


def test_rls_with_check_refuses_a_cross_tenant_insert(dsn):
    """WITH CHECK is pinned, not implicit: writing company B's row while scoped
    to A must be REFUSED by the database, not silently accepted."""
    import psycopg

    a, b = _company(), _company()
    with pytest.raises(psycopg.errors.InsufficientPrivilege):
        with psycopg.connect(dsn) as conn:
            with conn.cursor() as cur:
                cur.execute(COMPANY_GUC_SQL, (a,))
                cur.execute(
                    "INSERT INTO insightd_draft (company_id, draft_id, body) "
                    "VALUES (%s, %s, %s)",
                    (b, "D-EVIL", "{}"),
                )
    # ...and nothing landed
    assert _raw_visible_count(dsn, b) == 0


def test_cross_company_get_and_list_are_invisible_via_the_store(dsn):
    a, b = _company(), _company()
    store = _store(dsn)
    store.put(_draft("D-1", a))

    assert store.get(a, "D-1") is not None
    assert store.get(b, "D-1") is None  # not-found is indistinguishable
    assert store.list_for_company(b) == []
    assert [d.draft_id for d in store.list_for_company(a)] == ["D-1"]


def test_round_trip_is_field_exact(dsn):
    """extra='forbid' on PurchaseVoucherDraft makes this genuinely non-vacuous:
    any dropped/renamed/retyped field fails model_validate on the way out."""
    a = _company()
    store = _store(dsn)
    original = _draft("D-RT", a, party="Round Trip Pvt Ltd")
    store.put(original)

    loaded = store.get(a, "D-RT")
    assert loaded is not None
    assert loaded.model_dump() == original.model_dump()
    assert loaded == original


def test_put_is_an_upsert_not_a_duplicate(dsn):
    a = _company()
    store = _store(dsn)
    store.put(_draft("D-U", a, party="First"))
    store.put(_draft("D-U", a, party="Second"))
    listed = store.list_for_company(a)
    assert len(listed) == 1
    assert listed[0].body.party == "Second"
    assert _raw_visible_count(dsn, a) == 1


def test_drafts_survive_a_store_restart(dsn):
    """Durability: a BRAND NEW store object (as after an insightd restart) still
    sees the draft. The in-memory store fails this by construction."""
    a = _company()
    writer = _store(dsn)
    writer.put(_draft("D-BEFORE", a, party="Persisted Ltd"))
    del writer

    reader = _store(dsn)  # fresh instance, fresh connection, same DSN
    listed = reader.list_for_company(a)
    assert [d.draft_id for d in listed] == ["D-BEFORE"]
    assert listed[0].body.party == "Persisted Ltd"
    assert reader.get(a, "D-BEFORE") is not None


def test_list_order_is_deterministic(dsn):
    a = _company()
    store = _store(dsn)
    for i in range(4):
        store.put(_draft(f"D-{i}", a))
    assert [d.draft_id for d in store.list_for_company(a)] == [
        "D-0",
        "D-1",
        "D-2",
        "D-3",
    ]


def test_concurrent_operations_from_threads(dsn):
    """The draft routes are sync defs served from Starlette's threadpool, so the
    store is hit from several threads at once. A shared bare connection would
    blow up here; one connection per operation does not."""
    a = _company()
    store = _store(dsn)
    errors: list[BaseException] = []

    def work(i: int) -> None:
        try:
            store.put(_draft(f"D-T{i}", a))
            assert store.get(a, f"D-T{i}") is not None
            store.list_for_company(a)
        except BaseException as exc:  # noqa: BLE001 - re-raised in the assert
            errors.append(exc)

    threads = [threading.Thread(target=work, args=(i,)) for i in range(8)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    assert not errors, errors
    assert _raw_visible_count(dsn, a) == 8


def _schemaless_dsn(dsn: str) -> str:
    """A reachable DSN for a database with no insightd_draft table (template1 is
    always present and always empty of app schema). Skips rather than silently
    returning the migrated DSN, which would turn the fail-loud assertions below
    into false reds/greens."""
    import psycopg

    bare = dsn.replace("/tally?", "/template1?")
    if bare == dsn:
        pytest.skip(f"cannot derive a schema-less database from DSN {dsn!r}")
    try:
        with psycopg.connect(bare, connect_timeout=3):
            pass
    except Exception as exc:  # no rights on template1 -> nothing to prove here
        pytest.skip(f"template1 not reachable as the app role: {exc}")
    return bare


def test_verify_ready_rejects_a_dsn_with_no_schema(dsn):
    """Fail-loud rather than silent in-memory fallback."""
    store = _store(dsn)
    store.verify_ready()  # the migrated database is fine

    with pytest.raises(RuntimeError, match="cored migrate"):
        _store(_schemaless_dsn(dsn)).verify_ready()


# -- the production wiring itself (deps.get_draft_store) ---------------------


def test_a_configured_dsn_actually_selects_the_pg_store(dsn):
    """THE wiring gate. Every other PG test injects the store by hand
    (``dependency_overrides``) or constructs it directly, so without this one
    the whole PG branch could be deleted from ``deps.get_draft_store`` and all
    of them would still pass while insightd silently ran in memory."""
    import insightd.api.deps as deps_mod
    import insightd.config as config_mod
    from insightd.api.pg_store import PGDraftStore
    from insightd.api.store import DraftStore

    monkey = pytest.MonkeyPatch()
    try:
        monkey.setattr(config_mod, "_settings", config_mod.Settings(database_url=dsn))
        monkey.setattr(deps_mod, "_pg_store", None)
        store = deps_mod.get_draft_store()
        assert isinstance(store, PGDraftStore)
        assert not isinstance(store, DraftStore)  # NOT the in-memory singleton
        assert deps_mod.get_draft_store() is store  # built once, then reused
    finally:
        monkey.undo()
    assert deps_mod._pg_store is None  # no leakage into other tests


def test_configured_but_unmigrated_dsn_fails_loud_instead_of_falling_back(dsn):
    """A set-but-unmigrated INSIGHTD_DATABASE_URL must RAISE at store selection.
    Degrading to the in-memory store would look healthy while dropping every
    draft on restart — exactly the failure this workstream exists to remove."""
    import insightd.api.deps as deps_mod
    import insightd.config as config_mod

    bare = _schemaless_dsn(dsn)
    monkey = pytest.MonkeyPatch()
    try:
        monkey.setattr(config_mod, "_settings", config_mod.Settings(database_url=bare))
        monkey.setattr(deps_mod, "_pg_store", None)
        with pytest.raises(RuntimeError, match="cored migrate"):
            deps_mod.get_draft_store()
    finally:
        monkey.undo()


# -- HTTP layer: same isolation contract through the real FastAPI app --------


def _pg_client(dsn: str):
    app = create_app()
    store = _store(dsn)
    app.dependency_overrides[get_draft_store] = lambda: store
    return TestClient(app), store


def test_http_routes_are_company_scoped_against_the_pg_store(dsn):
    """Cross-company invisibility at the HTTP layer, with the PG store wired in
    (the routes need no change — PGDraftStore duck-types DraftStore)."""
    a, b = _company(), _company()
    client, _ = _pg_client(dsn)

    fx = load_fixture("intra-state-18-1line-clean")
    fx["input"]["tenant"]["company_id"] = a
    ctx = fx["input"]["tenant"]
    r = client.post(
        f"/companies/{a}/drafts",
        json={"source": "upload", "tenant": ctx, "fixture": fx},
    )
    assert r.status_code == 201, r.text
    created = r.json()
    assert created["company_id"] == a
    did = created["draft_id"]

    # owner sees it
    assert client.get(f"/companies/{a}/drafts/{did}").status_code == 200
    listed = client.get(f"/companies/{a}/drafts").json()
    assert [s["draft_id"] for s in listed] == [did]

    # another company sees nothing: 404 on the item, [] on the collection
    assert client.get(f"/companies/{b}/drafts/{did}").status_code == 404
    assert client.get(f"/companies/{b}/drafts").json() == []

    # ...and the DATABASE agrees, with no WHERE clause (this is the assertion
    # that fails if RLS is dropped; the 404 above would still pass)
    assert _raw_visible_count(dsn, b) == 0
    assert _raw_visible_count(dsn, a) == 1


def test_http_created_draft_survives_a_store_restart(dsn):
    """POST through the app, then serve the GET from a brand-new store object."""
    a = _company()
    client, _ = _pg_client(dsn)
    fx = load_fixture("intra-state-18-1line-clean")
    fx["input"]["tenant"]["company_id"] = a
    r = client.post(
        f"/companies/{a}/drafts",
        json={"source": "upload", "tenant": fx["input"]["tenant"], "fixture": fx},
    )
    assert r.status_code == 201, r.text
    posted = r.json()

    app2 = create_app()
    app2.dependency_overrides[get_draft_store] = lambda: _store(dsn)
    client2 = TestClient(app2)
    g = client2.get(f"/companies/{a}/drafts/{posted['draft_id']}")
    assert g.status_code == 200, g.text
    assert g.json() == posted  # byte-for-byte the same draft after "restart"
