"""Prove the scanner sees deleted secrets after commit 30 and on merged branches."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("scan-secrets.sh").resolve()


class SecretScanTest(unittest.TestCase):
    def test_history_and_scanner_errors(self):
        with tempfile.TemporaryDirectory(prefix="tally-secret-gate-") as tmp:
            root = Path(tmp)
            repo = root / "repo"
            repo.mkdir()

            def git(*args):
                return subprocess.check_output(["git", "-C", str(repo), *args], text=True, stderr=subprocess.DEVNULL).strip()

            git("init", "-b", "main")
            git("config", "user.email", "fixture@example.invalid")
            git("config", "user.name", "Scanner fixture")
            git("commit", "--allow-empty", "-m", "base")
            base = git("rev-parse", "HEAD")
            config = root / "sentinel.toml"
            config.write_text('title="test"\n[[rules]]\nid="sentinel"\ndescription="synthetic scanner control"\nregex="TALLY_SCAN_SENTINEL_[A-Z]+"\n')
            report = root / "report.json"

            def scan(**extra):
                env = {**os.environ, "SCAN_BASE_SHA": base, "SCAN_HEAD_SHA": git("rev-parse", "HEAD"),
                       "GITLEAKS_CONFIG": str(config), "GITLEAKS_REPORT": str(report), **extra}
                return subprocess.run(["bash", str(SCRIPT), str(repo)], env=env, capture_output=True, text=True)

            for i in range(34):
                git("commit", "--allow-empty", "-m", f"clean {i}")
            self.assertEqual(scan().returncode, 0)
            git("checkout", "-b", "side")
            (repo / "side.txt").write_text("TALLY_SCAN_SENTINEL_SIDE\n")
            git("add", "side.txt")
            git("commit", "-m", "side sentinel")
            git("rm", "side.txt")
            git("commit", "-m", "remove side sentinel")
            git("checkout", "main")
            (repo / "late.txt").write_text("TALLY_SCAN_SENTINEL_LATE\n")
            git("add", "late.txt")
            git("commit", "-m", "sentinel after commit thirty")
            git("rm", "late.txt")
            git("commit", "-m", "remove late sentinel")
            git("merge", "--no-ff", "side", "-m", "merge side history")
            result = scan()
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertEqual({x["File"] for x in json.loads(report.read_text())}, {"late.txt", "side.txt"})
            self.assertNotEqual(scan(GITLEAKS_BIN="/does/not/exist").returncode, 0)
            self.assertNotEqual(scan(SCAN_BASE_SHA="--all").returncode, 0)


if __name__ == "__main__":
    unittest.main()
