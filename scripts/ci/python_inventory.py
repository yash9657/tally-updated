"""Require exact transitive Python locks and verify Trivy actually scanned them."""
import json
from pathlib import Path
import re
import sys

root = Path(__file__).resolve().parents[2]
destination = root / "security-python-locks"
inventory = {}
for lock in sorted((root / "insightd").glob("requirements*.txt")):
    packages = {}
    for line in lock.read_text().splitlines():
        line = line.split("#", 1)[0].strip()
        if not line:
            continue
        match = re.fullmatch(r"([A-Za-z0-9_.-]+)(?:\[[^]]+\])?==([^; ]+)(?:\s*;.*)?", line)
        if not match:
            raise SystemExit(f"{lock.name}: not an exact resolved dependency: {line}")
        packages[match[1].lower().replace("_", "-")] = match[2]
    if not {"fastapi", "starlette", "pydantic", "pydantic-core", "httpx", "httpcore"} <= packages.keys():
        raise SystemExit(f"{lock.name}: core or transitive inventory missing")
    inventory[lock.name] = packages
    target = destination / lock.stem / "requirements.txt"
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(lock.read_text())
if len(inventory) < 8:
    raise SystemExit("missing core/optional/test/deploy dependency profiles")
(root / "python-inventory.json").write_text(json.dumps(inventory, indent=2) + "\n")
print(f"Python inventory: {len(inventory)} resolved profiles")

if len(sys.argv) > 1:
    results = json.loads(Path(sys.argv[1]).read_text()).get("Results", [])
    scanned = [r for r in results if r.get("Type") == "pip"]
    if len(scanned) != len(inventory):
        raise SystemExit(f"Trivy scanned {len(scanned)} Python profiles, expected {len(inventory)}")
    profiles = [Path(r["Target"]).parent.name + ".txt" for r in scanned]
    if set(profiles) != set(inventory) or len(set(profiles)) != len(profiles):
        raise SystemExit("Trivy has duplicate or missing Python profiles")
    for result in scanned:
        profile = Path(result["Target"]).parent.name + ".txt"
        expected = inventory[profile]
        actual = {p["Name"].lower().replace("_", "-"): p["Version"] for p in result.get("Packages", [])}
        if not expected.items() <= actual.items():
            raise SystemExit(f"Trivy inventory incomplete for {profile}")
    print("Trivy Python targets contain every locked dependency")
