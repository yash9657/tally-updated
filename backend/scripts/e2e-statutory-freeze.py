#!/usr/bin/env python3
"""LIVE two-binary e2e for Task B — statutoryd IRN + cored freeze-on-external-reference.

Drives a RUNNING cored (:8087) AND statutoryd (:8091) over HTTP, proving the whole
IRN-and-freeze loop end to end (stdlib only, no deps):

  1. Post a GST sales voucher in cored.
  2. statutoryd mints a deterministic IRN (fetching the voucher amounts from cored,
     validating the NIC e-invoice schema, then calling cored's freeze hook), which
     PINS the voucher's display number.
  3. The freeze hook is idempotent (re-register the same IRN -> same pinned number).
  4. ALTER and DELETE of the now-frozen voucher are HARD-BLOCKED with HTTP 409,
     carrying the blocking reference, and each raises a reconciliation-queue row.
  5. The reconciliation-queue surfaces both refused attempts.
  6. The voucher is left intact (version 1, not deleted) — the block rolled back.

With cored auth on (Task D), every cored call needs a Bearer token: this script
mints an Owner token (POST /auth/token) for its direct calls and asserts the auth
gate (no-token -> 401, cross-tenant -> 403). statutoryd needs a SERVICE token
(subject "svc:statutoryd") at boot via CORED_SERVICE_TOKEN.

OPT-IN PHASE 7 — DURABLE StepStore across a statutoryd PROCESS RESTART (WS7a).
Set STATUTORYD_BIN (path to a built statutoryd) and STATUTORYD_DSN to enable it.
This phase launches, kills and relaunches statutoryd ITSELF on its own port, and
verifies durability with an INDEPENDENT oracle — it reads `statutory_step`
straight out of Postgres via `docker exec <container> psql`, because asserting
"same IRN after restart" alone would be VACUOUS: FakeProvider is deterministic,
so an in-memory store would mint the identical IRN too. The real assertions are
(i) 4 step rows exist for the workflow while statutoryd is DOWN (an in-memory
store writes 0), and (ii) the `generate` row's created_at is UNCHANGED by the
post-restart re-POST, proving the external IRP call was not re-executed.
When STATUTORYD_BIN is unset the phase prints an explicit SKIP.

Boot:
  cd backend && go build -o /tmp/cored ./cmd/cored && go build -o /tmp/statutoryd ./cmd/statutoryd
  ADMIN_DSN=... go run ./cmd/cored migrate
  # auth is fail-closed; enable the in-process dev issuer so /auth/token serves:
  AUTH_DEV_TOKEN_ENABLED=true PORT=8087 APP_DSN=... /tmp/cored serve &
  # mint the service token, then boot statutoryd with it:
  SVC=$(curl -s -XPOST http://localhost:8087/auth/token -d '{"subject":"svc:statutoryd"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
  CORED_BASE_URL=http://localhost:8087 CORED_SERVICE_TOKEN=$SVC PORT=8091 /tmp/statutoryd &
Run:
  python3 backend/scripts/e2e-statutory-freeze.py
  # with the durable-restart phase:
  STATUTORYD_BIN=/tmp/statutoryd \
  STATUTORYD_DSN="postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable" \
    python3 backend/scripts/e2e-statutory-freeze.py

ENV: CORED_BASE_URL, STATUTORYD_BASE_URL, STATUTORYD_BIN, STATUTORYD_DSN,
     STATUTORYD_RESTART_PORT (default 8107), PG_CONTAINER (default tally_pg).

Exit 0 + "STATUTORY E2E OK" on success; an AssertionError (non-zero exit) on any mismatch.
"""
import json, os, subprocess, time, urllib.request, urllib.error, uuid

CORED = os.environ.get("CORED_BASE_URL", "http://localhost:8087")
STAT = os.environ.get("STATUTORYD_BASE_URL", "http://localhost:8091")
STATUTORYD_BIN = os.environ.get("STATUTORYD_BIN", "")
STATUTORYD_DSN = os.environ.get("STATUTORYD_DSN", "")
RESTART_PORT = os.environ.get("STATUTORYD_RESTART_PORT", "8107")
PG_CONTAINER = os.environ.get("PG_CONTAINER", "tally_pg")


def req(url, data=None, method=None, token=None):
    body = json.dumps(data).encode() if data is not None else None
    r = urllib.request.Request(url, data=body, method=method or ("POST" if data is not None else "GET"))
    if body is not None:
        r.add_header("Content-Type", "application/json")
    if token:
        r.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(r) as resp:
            t = resp.read().decode()
            return resp.status, (json.loads(t) if t else None)
    except urllib.error.HTTPError as e:
        t = e.read().decode()
        return e.code, (json.loads(t) if t else None)


def psql(sql):
    """INDEPENDENT oracle: read Postgres directly, bypassing every service under test."""
    p = subprocess.run(["docker", "exec", "-i", PG_CONTAINER, "psql", "-U", "postgres",
                        "-d", "tally", "-tAc", sql], capture_output=True, text=True)
    assert p.returncode == 0, ("psql failed", p.returncode, p.stderr.strip())
    return p.stdout.strip()


def boot_statutoryd(svc_token, dsn=None):
    """Launch OUR OWN statutoryd and wait for /healthz. dsn="" => in-memory store."""
    envd = dict(os.environ)
    envd.update({"CORED_BASE_URL": CORED, "CORED_SERVICE_TOKEN": svc_token,
                 "PORT": RESTART_PORT,
                 "STATUTORYD_DSN": STATUTORYD_DSN if dsn is None else dsn})
    proc = subprocess.Popen([STATUTORYD_BIN], env=envd,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    base = f"http://localhost:{RESTART_PORT}"
    for _ in range(100):
        if proc.poll() is not None:
            raise AssertionError(("statutoryd exited during boot", proc.returncode,
                                  proc.stdout.read() if proc.stdout else ""))
        try:
            s, _r = req(f"{base}/healthz")
            if s == 200:
                return proc, base
        except Exception:
            pass
        time.sleep(0.1)
    proc.kill()
    raise AssertionError("statutoryd did not become healthy on port " + RESTART_PORT)


def stop(proc):
    proc.kill()
    proc.wait(timeout=10)


EINVOICE_BODY = {
    "seller_gstin": "29ABCDE1234F1Z5", "seller_legal_name": "Stat E2E Co", "seller_state_code": "29",
    "buyer_gstin": "29ZYXWV9876G1Z2", "buyer_legal_name": "Bharat Buyer", "buyer_state_code": "29",
    "lines": [{"desc": "Steel widgets", "hsn": "8471", "qty": "2.00", "unit": "NOS",
               "unit_price": "500.00", "taxable": "1000.00", "gst_rate_pct": "18.00",
               "cgst": "90.00", "sgst": "90.00", "igst": "0.00", "line_total": "1180.00"}]}


def post_sale(cid, owner, postings, gst, date):
    s, v = req(f"{CORED}/v1/companies/{cid}/vouchers/sales",
               {"voucher_date": date, "party": "Bharat Buyer",
                "party_ledger_id": postings[0]["ledger_id"], "postings": postings, "gst": gst}, token=owner)
    assert s == 201, (s, v)
    return v["voucher_id"], v["voucher_no"]


def restart_phase(cid, owner, svc, postings, gst):
    """Prove IRN workflow state SURVIVES a statutoryd process restart (WS7a)."""
    if not STATUTORYD_BIN or not STATUTORYD_DSN:
        print("SKIP durable-restart phase: set STATUTORYD_BIN and STATUTORYD_DSN to run it "
              "(this run proved nothing about statutoryd restart durability)")
        return

    # --- CONTROL: the SAME binary WITHOUT a DSN persists NOTHING -------------
    # Without this, "4 rows exist after the kill" could be true for reasons
    # unrelated to WS7a. Run it first so the durable assertions below are known
    # to discriminate.
    cvid, _cvno = post_sale(cid, owner, postings, gst, "2026-05-12")
    cwf = f"irn:{cid}:{cvid}"
    proc, base = boot_statutoryd(svc, dsn="")
    try:
        s, ir = req(f"{base}/companies/{cid}/vouchers/{cvid}/e-invoice", EINVOICE_BODY)
        assert s == 200, (s, ir)
    finally:
        stop(proc)
    ctrl = psql(f"SELECT count(*) FROM statutory_step WHERE workflow_id = '{cwf}'")
    assert ctrl == "0", ("control is broken: the in-memory store must persist NOTHING", ctrl)
    print("control (no STATUTORYD_DSN): IRN minted, statutory_step rows = 0")

    # --- A second voucher, exclusively for the durable phase ------------------
    vid, vno = post_sale(cid, owner, postings, gst, "2026-05-11")
    wf = f"irn:{cid}:{vid}"  # mirrors GenerateIRNWorkflow's workflow-id derivation

    # No prior state for this workflow.
    assert psql(f"SELECT count(*) FROM statutory_step WHERE workflow_id = '{wf}'") == "0", wf

    einv = EINVOICE_BODY

    proc, base = boot_statutoryd(svc)
    try:
        s, ir = req(f"{base}/companies/{cid}/vouchers/{vid}/e-invoice", einv)
        assert s == 200, (s, ir)
        irn = ir["irn"]
        assert ir["pinned_voucher_no"] == vno, (ir["pinned_voucher_no"], vno)
        print(f"durable statutoryd minted IRN {irn[:16]}... for {vno}")
    finally:
        stop(proc)
    print("statutoryd KILLED")

    # ORACLE 1 — with statutoryd DOWN, all four steps are on disk. An in-memory
    # StepStore writes NOTHING here, so this count would be 0.
    n = psql(f"SELECT count(*) FROM statutory_step WHERE workflow_id = '{wf}'")
    assert n == "4", ("workflow state did not survive the kill (in-memory store?)", n)
    names = psql(f"SELECT string_agg(step_name, ',' ORDER BY seq) FROM statutory_step WHERE workflow_id = '{wf}'")
    assert names == "build,validate,generate,freeze", names
    gen_at = psql(f"SELECT created_at FROM statutory_step WHERE workflow_id = '{wf}' AND step_name = 'generate'")
    assert gen_at, "no generate row"
    print(f"while DOWN: statutory_step holds [{names}] (generate at {gen_at})")

    # Relaunch against the SAME DSN and re-POST.
    proc, base = boot_statutoryd(svc)
    try:
        s, ir2 = req(f"{base}/companies/{cid}/vouchers/{vid}/e-invoice", einv)
        assert s == 200, (s, ir2)
        assert ir2["irn"] == irn, ("IRN changed across restart", irn, ir2["irn"])
    finally:
        stop(proc)

    # ORACLE 2 — the `generate` row was REUSED, not rewritten: its created_at is
    # untouched and no row was duplicated. That is what proves the external IRP
    # call was NOT re-executed (the IRN alone cannot prove it: FakeProvider is
    # deterministic and would mint the same value from scratch).
    n2 = psql(f"SELECT count(*) FROM statutory_step WHERE workflow_id = '{wf}'")
    assert n2 == "4", ("restart re-executed steps", n2)
    gen_at2 = psql(f"SELECT created_at FROM statutory_step WHERE workflow_id = '{wf}' AND step_name = 'generate'")
    assert gen_at2 == gen_at, ("generate step was RE-EXECUTED after restart", gen_at, gen_at2)
    print(f"after restart: same IRN, generate row untouched ({gen_at2}) -> IRP called exactly once")

    # Cleanup this phase's rows so repeated runs stay clean.
    psql(f"DELETE FROM statutory_step WHERE workflow_id = '{wf}'")


def mint_token(subject, amr=None):
    """Obtain a dev JWT from cored's POST /auth/token (no auth required)."""
    s, r = req(f"{CORED}/auth/token", {"subject": subject, "amr": amr or []})
    assert s == 200, ("mint token", s, r)
    return r["token"]


def main():
    # An Owner token for the direct cored calls. AMR webauthn satisfies the Owner
    # level's requires_webauthn step-up on writes.
    owner = mint_token("owner-" + str(uuid.uuid4()), amr=["webauthn"])
    # A service token for statutoryd's freeze-hook calls (svc:* -> full access).
    svc = mint_token("svc:statutoryd")

    cid = str(uuid.uuid4())
    s, _ = req(f"{CORED}/v1/companies", {"id": cid, "name": "Stat E2E Co",
                                         "fy_start_date": "2026-04-01", "base_currency": "INR"}, token=owner)
    assert s == 201, s

    # NEGATIVE: a request with NO token -> 401, a cross-tenant token -> 403.
    s, _ = req(f"{CORED}/v1/companies/{cid}/reconciliation-queue")
    assert s == 401, ("no-token must be 401", s)
    other = mint_token("intruder-" + str(uuid.uuid4()), amr=["webauthn"])
    s, _ = req(f"{CORED}/v1/companies/{cid}/reconciliation-queue", token=other)
    assert s == 403, ("cross-tenant must be 403", s)
    print("auth gate: no-token -> 401, cross-tenant -> 403")

    def ledger(name, group, **kw):
        s, r = req(f"{CORED}/v1/companies/{cid}/ledgers", {"name": name, "group_name": group, **kw}, token=owner)
        assert s == 201, (s, r)
        return r["ledger_id"]

    debtor = ledger("Bharat Buyer", "Sundry Debtors", state_code="29")
    sales = ledger("Domestic Sales", "Sales Accounts")
    ocg = ledger("Output CGST", "Duties & Taxes")
    osg = ledger("Output SGST", "Duties & Taxes")
    oig = ledger("Output IGST", "Duties & Taxes")

    # intra @18 sale: taxable 1000 -> CGST 90 + SGST 90, party Dr 1180.
    gst = {"supplier_state_code": "29", "place_of_supply_state_code": "29",
           "lines": [{"taxable_value": "1000.0000", "gst_rate_pct": "18.0000", "hsn": "8471"}],
           "cgst_ledger_id": ocg, "sgst_ledger_id": osg, "igst_ledger_id": oig}
    postings = [{"ledger_id": debtor, "dr_cr": "D", "amount": "1180.0000"},
                {"ledger_id": sales, "dr_cr": "C", "amount": "1000.0000"},
                {"ledger_id": ocg, "dr_cr": "C", "amount": "90.0000"},
                {"ledger_id": osg, "dr_cr": "C", "amount": "90.0000"}]
    s, v = req(f"{CORED}/v1/companies/{cid}/vouchers/sales",
               {"voucher_date": "2026-05-10", "party": "Bharat Buyer",
                "party_ledger_id": debtor, "postings": postings, "gst": gst}, token=owner)
    assert s == 201, (s, v)
    vid, vno = v["voucher_id"], v["voucher_no"]
    print(f"sales voucher posted: no={vno} cgst/sgst={v['cgst']}/{v['sgst']}")

    s, rq = req(f"{CORED}/v1/companies/{cid}/reconciliation-queue", token=owner)
    assert s == 200 and len(rq["entries"]) == 0, rq
    print("recon queue empty before freeze")

    # statutoryd: mint the IRN (fetches amounts from cored, calls the freeze hook).
    einv = {"seller_gstin": "29ABCDE1234F1Z5", "seller_legal_name": "Stat E2E Co", "seller_state_code": "29",
            "buyer_gstin": "29ZYXWV9876G1Z2", "buyer_legal_name": "Bharat Buyer", "buyer_state_code": "29",
            "lines": [{"desc": "Steel widgets", "hsn": "8471", "qty": "2.00", "unit": "NOS",
                       "unit_price": "500.00", "taxable": "1000.00", "gst_rate_pct": "18.00",
                       "cgst": "90.00", "sgst": "90.00", "igst": "0.00", "line_total": "1180.00"}]}
    s, ir = req(f"{STAT}/companies/{cid}/vouchers/{vid}/e-invoice", einv)
    assert s == 200, (s, ir)
    irn = ir["irn"]
    assert len(irn) == 64, ("IRN not 64-hex", irn)
    assert ir["pinned_voucher_no"] == vno, (ir["pinned_voucher_no"], vno)
    print(f"IRN minted: {irn[:16]}... status={ir['status']} pinned_no={ir['pinned_voucher_no']}")

    # Idempotent freeze hook.
    s, p1 = req(f"{CORED}/v1/companies/{cid}/vouchers/{vid}/external-reference",
                {"kind": "IRN", "ref_value": irn}, token=owner)
    assert s in (200, 201) and p1["pinned_voucher_no"] == vno, (s, p1)
    print("freeze-hook idempotent re-register ok")

    # ALTER + DELETE of the frozen voucher -> 409.
    s, a = req(f"{CORED}/v1/companies/{cid}/vouchers/sales/{vid}",
               {"voucher_date": "2026-05-10", "party": "Bharat Buyer",
                "party_ledger_id": debtor, "postings": postings, "gst": gst}, method="PUT", token=owner)
    assert s == 409, ("alter not blocked", s, a)
    assert a["frozen"] == {"kind": "IRN", "ref_value": irn, "pinned_voucher_no": vno}, a
    print("ALTER frozen voucher -> 409:", a["frozen"]["kind"])

    s, dl = req(f"{CORED}/v1/companies/{cid}/vouchers/{vid}", method="DELETE", token=owner)
    assert s == 409, ("delete not blocked", s, dl)
    print("DELETE frozen voucher -> 409")

    # Reconciliation-queue surfaces both refused attempts.
    s, rq = req(f"{CORED}/v1/companies/{cid}/reconciliation-queue", token=owner)
    assert s == 200
    ops = sorted(e["attempted_op"] for e in rq["entries"])
    assert ops == ["ALTER", "DELETE"], ("recon entries", rq)
    for e in rq["entries"]:
        assert e["voucher_id"] == vid and e["kind"] == "IRN" and e["frozen_voucher_no"] == vno, e
    print("recon queue after blocks:", ops)

    # Voucher intact.
    s, gv = req(f"{CORED}/v1/companies/{cid}/vouchers/{vid}", token=owner)
    assert s == 200 and gv["version_no"] == 1 and gv["voucher_no"] == vno, gv
    print("voucher intact after blocked edits: version", gv["version_no"])

    # WS7a: durable StepStore across a statutoryd process restart (opt-in).
    restart_phase(cid, owner, svc, postings, gst)

    print("\nSTATUTORY E2E OK — IRN minted, number frozen, alter+delete hard-blocked, recon queue surfaced")


if __name__ == "__main__":
    main()
