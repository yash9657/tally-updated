#!/usr/bin/env python3
"""Offline scope/cost forecast. Reads evidence; never loads a key or writes a ledger."""
import argparse
import json
from decimal import Decimal
from pathlib import Path


def forecast(evidence, ledger):
    rounds = {r['name']: r for r in evidence['follow_up']['rounds']}
    charge = lambda name: Decimal(rounds[name]['shared_conservative_charge_after_run'])
    task_cost = charge('v6-balanced') - charge('v6-focused')
    insight_per_task = (charge('v8-insights') - charge('v8-repairs')) / 5
    holdout_cost = charge('v7-insights') - charge('v6-balanced')
    initial = task_cost + insight_per_task * 12 + holdout_cost
    all_tiers = initial + insight_per_task * 24
    cap, used = Decimal(ledger['cap_usd']), Decimal(ledger['charged_usd'])
    return {
        'kind': 'forecast_not_provider_invoice', 'agent_prompt': 'agent-decide-v9',
        'fixture_version': 'scale-fixture-v1',
        'shared_ledger_cap_usd': str(cap), 'shared_ledger_charge_usd': str(used),
        'shared_ledger_remaining_usd': str(cap - used),
        'conservative_rate_per_million_tokens_usd': ledger['rate_per_million_usd'],
        'initial_suite': {'original_tasks': 28, 'original_business_cases_and_holdouts': 9,
                          'expanded_1000_voucher_cases': 12, 'total': 49},
        'later_tiers_after_offline_validation': {'10000_vouchers': 12, '100000_vouchers': 12},
        'total_if_all_tiers_validated': 73,
        'languages': ['English', 'Devanagari Hindi', 'Hinglish'],
        'industries': ['retail', 'wholesale', 'services', 'manufacturing'],
        'empirical_initial_conservative_usd': str(initial),
        'empirical_all_tiers_conservative_usd': str(all_tiers),
        'planning_allowance_at_2x_all_tiers_usd': str(all_tiers * 2),
        'requested_additional_authorization_usd': '10',
        'proposed_total_cap_usd_only_if_authorized': str(cap + 10),
        'paid_runs_authorized': False,
        'caveats': ['Forecast uses historical observed token usage, not a guaranteed maximum.',
                    'Larger context, Hindi, repairs and complex reads may cost more; shared cap stops spending.',
                    'Business insight cases are advice-only and require unchanged books; not completed collections or sends.',
                    '10k/100k runs require independent fixture validation and resource inspection first.',
                    'No ledger mutation: a later approved increase must retain charge/history and audit old/new cap.',
                    'No human UI timing or customer ROI is established.'],
    }


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--budget-ledger', required=True, type=Path)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    evidence = json.loads((root / 'docs/evals/fireworks-2026-10-21.json').read_text())
    ledger = json.loads(args.budget_ledger.read_text())
    print(json.dumps(forecast(evidence, ledger), indent=2))
