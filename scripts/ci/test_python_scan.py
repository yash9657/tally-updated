#!/usr/bin/env python3
"""Prove the installed scanner fails for a vulnerable Python dependency."""
import json
import os
import pathlib
import subprocess
import tempfile
import unittest


class PythonScanControl(unittest.TestCase):
    def test_known_vulnerability_is_a_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / 'requirements.txt').write_text('starlette==0.27.0\n')
            report = root / 'result.json'
            result = subprocess.run([
                os.environ.get('TRIVY_BIN', 'trivy'), 'fs', '--quiet',
                '--scanners', 'vuln', '--format', 'json', '--severity', 'HIGH,CRITICAL',
                '--ignore-unfixed', '--exit-code', '1', '--output', str(report), str(root),
            ], capture_output=True, text=True)
            self.assertEqual(result.returncode, 1, result.stderr)
            findings = [v for r in json.loads(report.read_text()).get('Results', [])
                        if r.get('Type') == 'pip' for v in r.get('Vulnerabilities', [])]
            self.assertTrue(any(v['PkgName'] == 'starlette' and v.get('FixedVersion')
                                for v in findings), 'scanner failed without detecting fixture')
            for output_format in ('table', 'sarif'):
                control = subprocess.run([
                    os.environ.get('TRIVY_BIN', 'trivy'), 'fs', '--quiet',
                    '--scanners', 'vuln', '--format', output_format,
                    '--severity', 'HIGH,CRITICAL', '--ignore-unfixed',
                    '--exit-code', '1', str(root),
                ], capture_output=True, text=True)
                self.assertEqual(control.returncode, 1, control.stderr)
                self.assertIn('starlette', control.stdout)


if __name__ == '__main__':
    unittest.main()
