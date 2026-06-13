# infra/security — scanner baselines

Configs for the `security` CI job (`PLAN.md` A.3: Trivy / Semgrep / gitleaks /
Falco).

## Files

- `semgrep.yml` — SAST rules. Beyond the registry packs CI also pulls
  (`p/default`, `p/secrets`, etc.), this file encodes **project invariants** a
  generic scanner can't know:
  - `no-bypassrls-role-in-app` — app code never connects as a superuser/BYPASSRLS role.
  - `money-must-not-be-float` — money is NUMERIC(19,4)/paise/decimal, never float.
  - `ai-must-not-emit-raw-sql` — the AI plane uses a typed DSL on a read replica.
  - `no-hardcoded-dsn-secret` — flags embedded DB passwords.
- `.gitleaks.toml` — secret scanning. Extends the gitleaks default ruleset, adds
  India PII detectors (PAN / GSTIN / Aadhaar — DPDP), and allowlists the known
  dev-only local Postgres credentials so they don't create alert fatigue.

## Local use

```bash
semgrep scan --config infra/security/semgrep.yml --error
gitleaks detect --config infra/security/.gitleaks.toml --redact --no-banner
trivy fs --scanners vuln,secret,misconfig .
```

Falco (runtime threat detection) is a cluster DaemonSet deployed via Argo CD in
a later phase; it has no static config in this repo.
