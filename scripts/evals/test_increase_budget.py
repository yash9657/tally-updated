import fcntl
import json
from pathlib import Path
import tempfile
import unittest

from increase_budget import increase


class IncreaseBudgetTest(unittest.TestCase):
    def test_preserves_spend_history_and_backup_and_refuses_repeat(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d) / 'ledger.json'
            before = {'cap_usd': '5', 'charged_usd': '4.88431',
                      'rate_per_million_usd': '2', 'prior_reports': ['historical-failure'],
                      'other_history': {'unchanged': True}}
            raw = json.dumps(before).encode()
            p.write_bytes(raw)
            kwargs = dict(expected_cap='5', expected_charge='4.88431', new_cap='15',
                          authorization='Authorize an additional $10')
            with open(str(p) + '.lock', 'a') as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                with self.assertRaises(BlockingIOError):
                    increase(p, **kwargs)
            audit = increase(p, **kwargs)
            after = json.loads(p.read_text())
            self.assertEqual(after.pop('cap_increases'), [audit])
            self.assertEqual(after.pop('cap_usd'), '15')
            before.pop('cap_usd')
            self.assertEqual(after, before)
            self.assertEqual(Path(audit['backup_path']).read_bytes(), raw)
            self.assertEqual(p.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(ValueError):
                increase(p, **kwargs)

    def test_missing_or_changed_ledger_cannot_initialize(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d) / 'ledger.json'
            kwargs = dict(expected_cap='5', expected_charge='4.88431', new_cap='15', authorization='Approved')
            with self.assertRaises(FileNotFoundError):
                increase(p, **kwargs)
            p.write_text('{"cap_usd":"5","charged_usd":"4.9"}')
            with self.assertRaises(ValueError):
                increase(p, **kwargs)
            self.assertEqual(json.loads(p.read_text())['cap_usd'], '5')


if __name__ == '__main__':
    unittest.main()
