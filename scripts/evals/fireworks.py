#!/usr/bin/env python3
"""Run the opt-in, state-judged Fireworks suite against disposable synthetic books."""
import argparse
from decimal import Decimal
import fcntl
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--env-file', type=Path, help='Read only Fireworks key/model from this dotenv file; never execute it')
parser.add_argument('--max-usd', required=True, help='Shared spending ceiling, including all earlier runs in the ledger')
parser.add_argument('--budget-ledger', required=True, type=Path)
parser.add_argument('--prior-report', action='append', default=[], type=Path, help='Import completed prior runs once when creating a new ledger')
parser.add_argument('--token-price-per-million-usd', required=True, help='Current upper bound for BOTH input and output token pricing')
parser.add_argument('--model', help='Override FIREWORKS_MODEL')
parser.add_argument('--report', required=True, type=Path)
parser.add_argument('--tasks', help='Comma-separated fixture IDs; default is balanced 28-task suite')
parser.add_argument('--suite', choices=['tasks', 'insights'], default='tasks')
args = parser.parse_args()
env = os.environ.copy()
if args.env_file:
    for line in args.env_file.read_text().splitlines():
        name, sep, value = line.removeprefix('export ').partition('=')
        if sep and name.strip() in {'FIREWORKS_API_KEY', 'FIREWORKS_MODEL'}:
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                value = value[1:-1]
            env[name.strip()] = value
if args.model:
    env['FIREWORKS_MODEL'] = args.model
for name in ('FIREWORKS_API_KEY', 'FIREWORKS_MODEL', 'ADMIN_DATABASE_URL'):
    if not env.get(name):
        parser.error(f'{name} is required')
env.update(EVAL_MAX_USD=args.max_usd,
           EVAL_TOKEN_PRICE_PER_MILLION_USD=args.token_price_per_million_usd,
           EVAL_SYNTHETIC_APPROVALS='1', EVAL_REPORT=str(args.report.resolve()),
           EVAL_BUDGET_LEDGER=str(args.budget_ledger.resolve()), EVAL_SUITE=args.suite)
env.pop('EVAL_TASK_IDS', None)
if args.tasks:
    env['EVAL_TASK_IDS'] = args.tasks
# Fail closed instead of allowing the shared DB-test skip switch to turn this
# paid evaluation into a successful no-op.
if env.get('CORED_SKIP_DB'):
    parser.error('unset CORED_SKIP_DB for a live evaluation')
root = Path(__file__).resolve().parents[2]
if not args.report.resolve().parent.is_dir():
    parser.error('report parent directory does not exist')
revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
dirty = subprocess.check_output(['git', 'diff', '--name-only'], cwd=root, text=True).strip()
env['EVAL_SOURCE_REVISION'] = revision + ('+dirty' if dirty else '')
fingerprint = hashlib.sha256()
for path in sorted((root / 'backend').rglob('*.go')):
    fingerprint.update(str(path.relative_to(root)).encode())
    fingerprint.update(b'\0')
    fingerprint.update(path.read_bytes())
env['EVAL_SOURCE_SHA256'] = fingerprint.hexdigest()
# Initialize once; the Go transport locks this same file for the full run.
# Validated usage settles previous conservative byte reservations. Uncertain
# requests retain a reservation repriced at the current upper token rate.
cap, rate = Decimal(args.max_usd), Decimal(args.token_price_per_million_usd)
if not cap.is_finite() or not rate.is_finite() or cap <= 0 or rate <= 0:
    parser.error('cap and price must be finite positive decimals')
lock_path = str(args.budget_ledger) + '.lock'
with open(lock_path, 'a') as lock:
    os.chmod(lock_path, 0o600)
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    if args.budget_ledger.exists():
        ledger = json.loads(args.budget_ledger.read_text())
        if Decimal(ledger['cap_usd']) != cap or Decimal(ledger['rate_per_million_usd']) != rate:
            parser.error('existing ledger cap/rate differ; refusing to reset budget')
        if args.prior_report:
            parser.error('prior reports can only initialize a new ledger')
    else:
        charge, hashes = Decimal(0), []
        for path in args.prior_report:
            raw = path.read_bytes()
            digest = hashlib.sha256(raw).hexdigest()
            if digest in hashes:
                parser.error('duplicate prior report')
            hashes.append(digest)
            report = json.loads(raw)
            if report['model'] != env['FIREWORKS_MODEL']:
                parser.error('prior report model differs from selected model')
            for call in report['calls']:
                usage = call.get('usage') or {}
                p, c, total = (usage.get(k) for k in ('prompt_tokens', 'completion_tokens', 'total_tokens'))
                if call.get('status') == 200 and all(type(n) is int for n in (p, c, total)) and p > 0 and c >= 0 and total == p + c:
                    charge += Decimal(total) * rate / 1000000
                else:
                    charge += Decimal(call['reserved_usd']) * rate / Decimal(report['token_price_per_million_usd'])
        if charge > cap:
            parser.error('prior runs already exceed the shared ceiling at this price')
        fd = os.open(args.budget_ledger, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as out:
            json.dump({'cap_usd': str(cap), 'rate_per_million_usd': str(rate), 'charged_usd': str(charge), 'prior_reports': hashes}, out, indent=2)
            out.flush()
            os.fsync(out.fileno())
sys.exit(subprocess.call(['go', 'test', '-tags', 'livefireworks', './internal/evals/tau',
                         '-run', '^TestFireworksLiveEvaluation$', '-count=1', '-v', '-timeout', '30m'],
                        cwd=root / 'backend', env=env))
