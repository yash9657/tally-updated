#!/usr/bin/env python3
"""Export additive synthetic evidence without provider messages or raw tool transcripts."""
import argparse
from collections import Counter
from decimal import Decimal
import hashlib
import json
from pathlib import Path
import re


def redact(value):
    if isinstance(value, dict):
        return {k: redact(v) for k, v in value.items()}
    if isinstance(value, list):
        return [redact(v) for v in value]
    if isinstance(value, str):
        value = re.sub(r'(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b', '[internal ID]', value)
        return re.sub(r'\b[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][A-Z0-9]Z[A-Z0-9]\b', '[synthetic GSTIN]', value)
    return value


def summarize(report, raw_hash, name):
    calls = report['calls']
    charged = sum((Decimal(c.get('charged_usd', c['reserved_usd'])) for c in calls), Decimal(0))
    tokens = {'prompt_tokens': 0, 'completion_tokens': 0, 'total_tokens': 0}
    uncertain = 0
    for c in calls:
        usage = c.get('usage') or {}
        if 'charged_usd' not in c:
            uncertain += 1
        if (c.get('status') == 200 and all(type(usage.get(k)) is int for k in tokens)
                and usage['prompt_tokens'] > 0 and usage['completion_tokens'] >= 0
                and usage['total_tokens'] == usage['prompt_tokens'] + usage['completion_tokens']):
            for k in tokens:
                tokens[k] += usage[k]
    result = {k: report[k] for k in ('agent_prompt_version', 'source_revision', 'source_sha256',
                                      'model', 'started_at', 'elapsed_seconds', 'shared_budget_charged_usd',
                                      'token_price_per_million_usd', 'max_usd')}
    result.update(name=name, raw_report_sha256=raw_hash, provider_calls=len(calls),
                  validated_usage=tokens, conservative_charge_usd=str(charged),
                  uncertain_reservations_retained=uncertain,
                  provider_status_counts=dict(Counter(str(c.get('status', 'transport_error')) for c in calls)),
                  provider_error_codes=sorted({c['error']['code'] for c in calls
                                               if isinstance(c.get('error'), dict) and isinstance(c['error'].get('code'), str)}))
    if 'report' in report:
        result.update(suite='original_tasks', state_results=report['report'],
                      state_passed=sum(t['Successes'] for t in report['report']['Tasks']),
                      total=len(report['report']['Tasks']), language_checks=report.get('language_checks'))
    else:
        result.update(suite='business_advice', necessary_checks_passed=report['passed'],
                      total=report['total'], results=report['results'])
    if report.get('harness_error'):
        result['harness_error'] = report['harness_error']
    return redact(result), charged


def export(paths, ledger, initial_charge):
    seen, runs, charged = set(), [], Decimal(initial_charge)
    for path in paths:
        raw = Path(path).read_bytes()
        digest = hashlib.sha256(raw).hexdigest()
        if digest in seen:
            raise ValueError('duplicate report would double-count spending')
        seen.add(digest)
        run, cost = summarize(json.loads(raw), digest, Path(path).stem)
        runs.append(run)
        charged += cost
    if charged != Decimal(ledger['charged_usd']):
        raise ValueError(f'ledger reconciliation failed: reports + prior charge = {charged}, ledger = {ledger["charged_usd"]}')
    audits = [{k: a[k] for k in ('at', 'old_cap_usd', 'new_cap_usd', 'additional_usd', 'charged_usd', 'authorization', 'previous_sha256')} for a in ledger.get('cap_increases', [])]
    return {'kind': 'synthetic_agent_parity_evaluation', 'historical_verdicts_replaced': False,
            'limitations': ['State checks, language heuristics and business necessary checks are separate measures.',
                            'Advice/drafts are not completed execution. Human review may find defects in a passing answer.',
                            'No human time savings or customer ROI established. Costs are conservative accounting, not an invoice.'],
            'budget': {'initial_charge_usd': initial_charge, 'cap_usd': ledger['cap_usd'],
                       'charged_usd': str(charged), 'remaining_usd': str(Decimal(ledger['cap_usd']) - charged),
                       'rate_per_million_usd': ledger['rate_per_million_usd'], 'cap_increases': audits,
                       'prior_report_hashes': ledger.get('prior_reports', [])},
            'runs': sorted(runs, key=lambda r: r['started_at'])}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ledger', type=Path, required=True)
    parser.add_argument('--initial-charge', required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('reports', nargs='+', type=Path)
    args = parser.parse_args()
    result = export(args.reports, json.loads(args.ledger.read_text()), args.initial_charge)
    with args.output.open('x') as out:  # Never overwrite an original verdict.
        json.dump(result, out, indent=2, ensure_ascii=False)
        out.write('\n')
