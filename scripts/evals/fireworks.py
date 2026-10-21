#!/usr/bin/env python3
"""Run the opt-in, state-judged Fireworks suite against disposable synthetic books."""
import argparse
import os
from pathlib import Path
import subprocess
import sys

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--env-file', type=Path, help='Read only Fireworks key/model from this dotenv file; never execute it')
parser.add_argument('--max-usd', required=True, help='Per-invocation conservative spending ceiling; account for earlier runs separately')
parser.add_argument('--token-price-per-million-usd', required=True, help='Current upper bound for BOTH input and output token pricing')
parser.add_argument('--model', help='Override FIREWORKS_MODEL')
parser.add_argument('--report', required=True, type=Path)
parser.add_argument('--tasks', help='Comma-separated fixture IDs; default is balanced 28-task suite')
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
           EVAL_SYNTHETIC_APPROVALS='1', EVAL_REPORT=str(args.report.resolve()))
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
sys.exit(subprocess.call(['go', 'test', '-tags', 'livefireworks', './internal/evals/tau',
                         '-run', '^TestFireworksLiveEvaluation$', '-count=1', '-v', '-timeout', '30m'],
                        cwd=root / 'backend', env=env))
