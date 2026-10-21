import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class RunnerPreflightTest(unittest.TestCase):
    def invoke(self, ledger, report):
        runner = Path(__file__).with_name('fireworks.py')
        env = os.environ | {'FIREWORKS_API_KEY': 'invalid-test-key',
                            'FIREWORKS_MODEL': 'unused', 'ADMIN_DATABASE_URL': 'unused'}
        return subprocess.run([sys.executable, str(runner), '--max-usd', '15',
                               '--token-price-per-million-usd', '2',
                               '--budget-ledger', str(ledger), '--report', str(report)],
                              capture_output=True, text=True, env=env)

    def test_missing_shared_ledger_fails_before_any_provider_call(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d)
            result = self.invoke(p / 'missing.json', p / 'new-report.json')
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('shared ledger is missing', result.stderr)
            self.assertFalse((p / 'missing.json').exists())
            self.assertFalse((p / 'new-report.json').exists())

    def test_historical_report_cannot_be_overwritten(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d)
            report = p / 'historic.json'
            report.write_text('preserved failure')
            result = self.invoke(p / 'ledger.json', report)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('report already exists', result.stderr)
            self.assertEqual(report.read_text(), 'preserved failure')


if __name__ == '__main__':
    unittest.main()
