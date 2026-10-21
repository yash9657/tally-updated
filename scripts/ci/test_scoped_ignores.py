#!/usr/bin/env python3
"""An accepted EKS node egress finding must not hide the same bug elsewhere."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

ignore = Path(__file__).resolve().parents[2] / '.trivyignore.yaml'
with tempfile.TemporaryDirectory() as tmp:
    root = Path(tmp)
    source = root / 'infra/terraform/aurora.tf'
    source.parent.mkdir(parents=True)
    source.write_text('''resource "aws_security_group_rule" "bad_database_egress" {
  type = "egress"
  from_port = 0
  to_port = 0
  protocol = "-1"
  cidr_blocks = ["0.0.0.0/0"]
  security_group_id = "sg-fixture"
}
''')
    report = root / 'report.json'
    result = subprocess.run([os.environ.get('TRIVY_BIN', 'trivy'), 'fs', '--quiet',
        '--scanners', 'misconfig', '--severity', 'HIGH,CRITICAL', '--exit-code', '1',
        '--ignorefile', str(ignore), '--format', 'json', '--output', str(report), str(root)],
        capture_output=True, text=True)
    assert result.returncode == 1, result.stderr
    findings = [f for r in json.loads(report.read_text()).get('Results', [])
                for f in r.get('Misconfigurations', [])]
    assert any(f.get('ID') == 'AWS-0104' for f in findings), 'scoped ignore hid unrelated database egress'
    print('Scoped-ignore control detected unrestricted database egress')
