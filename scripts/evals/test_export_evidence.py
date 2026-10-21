from decimal import Decimal
import json
from pathlib import Path
import tempfile
import unittest
from export_evidence import export, summarize


class EvidenceExportTest(unittest.TestCase):
    def test_preserves_verdict_and_uncertain_charge_without_raw_messages(self):
        r = {k: 'fixture' for k in ('agent_prompt_version', 'source_revision', 'source_sha256',
                                  'model', 'started_at', 'shared_budget_charged_usd',
                                  'token_price_per_million_usd', 'max_usd')}
        r.update(elapsed_seconds=1, passed=0, total=1,
                 results=[{'pass': False, 'reasons': ['original failure'], 'answer': '₹25,000 24AAYFS6789S1ZJ'}],
                 calls=[{'messages': 'PRIVATE TRANSCRIPT', 'reserved_usd': '0.02', 'status': 412,
                         'error': {'code': 'PRECONDITION_FAILED', 'message': 'PRIVATE ACCOUNT'}}])
        result, cost = summarize(r, 'hash', 'fixture')
        self.assertEqual(cost, Decimal('0.02'))
        self.assertEqual(result['necessary_checks_passed'], 0)
        self.assertEqual(result['uncertain_reservations_retained'], 1)
        self.assertEqual(result['results'][0]['reasons'], ['original failure'])
        self.assertEqual(result['results'][0]['answer'], '₹25,000 [synthetic GSTIN]')
        self.assertNotIn('PRIVATE TRANSCRIPT', str(result))
        self.assertNotIn('PRIVATE ACCOUNT', str(result))
        self.assertEqual(result['provider_status_counts'], {'412': 1})
        self.assertEqual(result['provider_error_codes'], ['PRECONDITION_FAILED'])

    def test_reconciles_all_charge_and_refuses_duplicate_or_missing_runs(self):
        report = {k: 'fixture' for k in ('agent_prompt_version', 'source_revision', 'source_sha256',
                                         'model', 'started_at', 'shared_budget_charged_usd',
                                         'token_price_per_million_usd', 'max_usd')}
        report.update(elapsed_seconds=1, passed=0, total=1, results=[{'pass': False}],
                      calls=[{'reserved_usd': '0.02', 'charged_usd': '0.01', 'status': 200,
                              'usage': {'prompt_tokens': 4000, 'completion_tokens': 1000, 'total_tokens': 5000}}])
        ledger = {'charged_usd': '4.89431', 'cap_usd': '15', 'rate_per_million_usd': '2'}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'original-failure.json'
            path.write_text(json.dumps(report))
            evidence = export([path], ledger, '4.88431')
            self.assertEqual(evidence['budget']['charged_usd'], '4.89431')
            self.assertEqual(evidence['budget']['remaining_usd'], '10.10569')
            self.assertEqual(evidence['runs'][0]['validated_usage']['total_tokens'], 5000)
            self.assertFalse(evidence['runs'][0]['results'][0]['pass'])
            with self.assertRaisesRegex(ValueError, 'duplicate report'):
                export([path, path], ledger, '4.88431')
            with self.assertRaisesRegex(ValueError, 'reconciliation failed'):
                export([], ledger, '4.88431')


if __name__ == '__main__':
    unittest.main()
