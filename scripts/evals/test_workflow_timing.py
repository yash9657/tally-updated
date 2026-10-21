import unittest
from workflow_timing import summarize


class TimingControls(unittest.TestCase):
    def rows(self):
        base = dict(industry='services', operator='fixture', task='receipt', bookFingerprint='seed',
                    completed=True, qualityVerified=True, finalFingerprint='books',
                    deliverable='state_change', elapsedMs=120000, source='operator_pilot')
        return [dict(base, path='manual_ui', activeHumanMs=60000), dict(base, path='agent_ui', activeHumanMs=30000)]

    def test_matched_positive_control(self):
        row = summarize(self.rows())['industries'][2]
        self.assertEqual(row['median_active_minutes_saved'], 0.5)
        self.assertEqual(row['reduction_of_medians_fraction'], 0.5)

    def test_advice_automation_missing_wrong_state_and_failed_do_not_claim_savings(self):
        for change in [dict(deliverable='advice_or_draft'), dict(completed=False), dict(qualityVerified=False),
                       dict(deliverable=None), dict(completed='false'), dict(qualityVerified='false'),
                       dict(source='browser_automation'), dict(finalFingerprint='wrong'), dict(activeHumanMs=None)]:
            rows = self.rows()
            rows[1].update(change)
            row = summarize(rows)['industries'][2]
            self.assertIsNone(row['median_active_minutes_saved'], change)
            self.assertEqual(row['attempted_pairs'], 1)
        self.assertIsNone(summarize(self.rows()[:1])['industries'][2]['median_active_minutes_saved'])

    def test_duplicate_trial_is_not_more_evidence(self):
        with self.assertRaises(ValueError):
            summarize(self.rows() * 2)

    def test_invalid_elapsed_time_is_excluded(self):
        for invalid in (float('inf'), float('nan'), None, '120000'):
            rows = self.rows()
            rows[1]['elapsedMs'] = invalid
            self.assertIsNone(summarize(rows)['industries'][2]['median_active_minutes_saved'])

    def test_invalid_matching_metadata_cannot_hide_a_trial(self):
        for change in [dict(industry='unknown'), dict(path='draft'), dict(operator=''), dict(bookFingerprint=None)]:
            rows = self.rows()
            rows[1].update(change)
            with self.assertRaises(ValueError):
                summarize(rows)

if __name__ == '__main__':
    unittest.main()
