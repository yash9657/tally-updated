#!/usr/bin/env bash
# pyproject.toml owns constraints; these exact resolutions are installed and scanned.
set -euo pipefail
cd "$(dirname "$0")/../../insightd"
common=(pyproject.toml --python-version 3.13 --python-platform linux --no-emit-index-url)
uv pip compile "${common[@]}" -o requirements.txt
for extra in db llm ocr pii test; do
  uv pip compile "${common[@]}" --extra "$extra" -c requirements.txt -o "requirements-$extra.txt"
done
uv pip compile "${common[@]}" --extra db --extra llm -c requirements.txt -o requirements-deploy.txt
uv pip compile "${common[@]}" --extra db --extra test -c requirements.txt -o requirements-db-test.txt
