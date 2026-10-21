#!/usr/bin/env python3
"""Summarize matched opt-in UI measurements; refuse to infer human time from automation."""
import argparse
import json
import math
from collections import defaultdict
from statistics import median


def summarize(rows):
    pairs = defaultdict(dict)
    industries = ('retail', 'wholesale', 'services', 'manufacturing')
    for row in rows:
        if row.get('industry') not in industries or row.get('path') not in ('manual_ui', 'agent_ui'):
            raise ValueError('unknown industry or workflow path would hide a trial from the denominator')
        if not all(isinstance(row.get(k), str) and row[k].strip() for k in ('operator', 'task', 'bookFingerprint')):
            raise ValueError('matched trials require nonempty operator, task and book identifiers')
        key = (row['industry'], row['operator'], row['task'], row['bookFingerprint'])
        if row['path'] in pairs[key]:
            raise ValueError('duplicate path for matched trial; use unique task/variant IDs')
        pairs[key][row['path']] = row
    result = []
    for industry in industries:
        saved, reductions, manual_times, agent_times = [], [], [], []
        attempted = matched = 0
        exclusions = defaultdict(int)
        for key, pair in pairs.items():
            if key[0] != industry:
                continue
            attempted += 1
            if set(pair) != {'manual_ui', 'agent_ui'}:
                exclusions['missing_matched_path'] += 1
                continue
            manual, agent = pair['manual_ui'], pair['agent_ui']
            if not all(r.get('completed') is True and r.get('qualityVerified') is True and r.get('deliverable') in ('state_change', 'downloaded_file') for r in (manual, agent)):
                exclusions['incomplete_or_unverified'] += 1
                continue
            if not manual.get('finalFingerprint') or manual['finalFingerprint'] != agent.get('finalFingerprint'):
                exclusions['different_final_state'] += 1
                continue
            matched += 1
            if any(r.get('source') != 'operator_pilot' for r in (manual, agent)):
                exclusions['automation_not_human_time'] += 1
                continue
            m, a = manual.get('activeHumanMs'), agent.get('activeHumanMs')
            times = (m, a, manual.get('elapsedMs'), agent.get('elapsedMs'))
            if not all(type(n) in (int, float) and math.isfinite(n) for n in times) or not 0 <= a <= agent['elapsedMs'] or not 0 < m <= manual['elapsedMs']:
                exclusions['invalid_active_time'] += 1
                continue
            saved.append((m - a) / 60000)
            reductions.append((m - a) / m)
            manual_times.append(m / 60000)
            agent_times.append(a / 60000)
        result.append({'industry': industry, 'attempted_pairs': attempted,
                       'correct_completed_pairs': matched, 'human_timed_pairs': len(saved),
                       'median_active_minutes_saved': median(saved) if saved else None,
                       'median_paired_reduction_fraction': median(reductions) if reductions else None,
                       'median_manual_active_minutes': median(manual_times) if manual_times else None,
                       'median_agent_active_minutes': median(agent_times) if agent_times else None,
                       'reduction_of_medians_fraction': 1 - median(agent_times) / median(manual_times) if manual_times else None,
                       'paired_active_minutes_saved_distribution': saved,
                       'excluded_from_human_savings': dict(exclusions)})
    return {'kind': 'matched_operator_pilot_summary', 'customer_roi': None,
            'limitation': 'Internal pilot timing does not establish representative customer savings. All failures remain in the denominator.',
            'industries': result}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('trace', help='JSON array from measureWorkflow; keep original trials immutable')
    args = parser.parse_args()
    with open(args.trace) as source:
        print(json.dumps(summarize(json.load(source)), indent=2))
