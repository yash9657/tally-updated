# Review follow-up — 21 October 2026

Branch `codex/review-fireworks` starts at remote master
`d8c1c27071cd51d3c154658c5473274955aac81b`.
Changes are in [draft PR #5](https://github.com/yash9657/tally-updated/pull/5).
The earlier review plan is preserved without edits.

| Review concern | Delivered change and evidence |
| --- | --- |
| R1: cached tenant-card report leak | Central report vocabulary and fail-closed card authorization; agent and MCP tests inspect the whole response, with owner-positive controls |
| R2: first-30-commits secret scan | Explicit base/head merge-base scan over the full graph; deleted secret after commit 30 and merged-side-branch controls; historical synthetic findings narrowly triaged |
| R3: missing Python vulnerability coverage | Eight resolved transitive dependency profiles, deployment/CI installs from locks, complete package inventory assertion, known-vulnerable JSON/table/SARIF controls; vulnerable dependencies updated |
| R4: no actual Tier 0 isolation | Linux network namespace with loopback only; default HTTP, custom transport and raw socket probes; normal-network negative control |
| R5: skipped database tests | Separate required PostgreSQL job; missing driver/database/schema fail; disabled-RLS control fails |
| Migration delivery | Forward migration 0110 reinstalls corrected Tier B functions and ownership/grants; fresh, old-definition upgrade and repeat-application tests |
| Company integrity | Migration 0111 audits existing rows before validating five composite company-scoped foreign keys; no silent data repair |
| Insight scheduler | Migration 0112 persists retry/backoff, original business date and fenced leases; tests cover >100-company fairness, concurrent claims, lease expiry, date rollover and company-deletion locks |
| CI escape paths | `master` push trigger; removed entry-file skip guards; blocking IaC; pinned scanner/action inputs; scanner-error controls; path-scoped ignore control |
| Infrastructure defaults | EKS private API default and Aurora scoped egress controls retained; CloudFront WAF configuration added; both Terraform stacks validated with Linux and macOS provider checksums |

The original historical digest flake's cause is **not established**. Independent
scheduler failure modes were reproduced and fixed; twenty repeated digest tests
passed. This does not retrospectively prove which defect caused the old failure.

Local verification included the full Go suite, build/vet, 2,048 frontend tests
and TypeScript under Node 22, Python core (227 passed, 17 skipped), Python with
database support (243 passed, one optional skip), isolated Tier 0, corpus
validation, Terraform/Kustomize, Gitleaks, Semgrep and Trivy. Changes discovered
in paid testing additionally ran agent/server/evaluation regressions. The PR's
checks identify which pushed revision CI verified.

GitGuardian dashboard classification is pending at the user's request. Its two
new occurrences are the public disposable PostgreSQL test password, not the
Fireworks key. The Vercel backend project is paused, so its preview is blocked;
the frontend preview builds. Neither external service was reconfigured.

No production migrations, Terraform apply or merge were performed. Fireworks
evaluation uses disposable synthetic companies; its original results, spending
and remaining quality gaps are recorded in [the runbook](FIREWORKS-EVALUATION.md).
