# infra/terraform — AWS ap-south-1 baseline (Phase 0)

A clean, idiomatic Terraform baseline for the architecture in `PLAN.md` Part A.
**Primary region: `ap-south-1` (Mumbai)** for India data residency; **DR region:
`ap-south-2` (Hyderabad)** (RPO ≤ 5 min / RTO ≤ 1 hr).

> **Status:** Phase 0 authors valid HCL. The `terraform` binary is **not**
> installed in the build environment, so this is validated by structure and
> parsed in CI (`iac` job, `continue-on-error` until the cloud toolchain is
> provisioned). Nothing here is applied.

## Files

| File | Owns |
|---|---|
| `versions.tf` | Terraform + provider version pins (AWS `~> 5.60`). |
| `providers.tf` | AWS provider (primary + `dr` alias) and the S3 remote-state backend (native S3 locking). |
| `variables.tf` | All typed, validated inputs (region is constrained to India regions). |
| `main.tf` | Composition: VPC (3-AZ) + EKS (Graviton/Bottlerocket) via the community modules. |
| `kms.tf` | Root CMKs; **per-tenant data-key envelope story is documented at the top**. |
| `aurora.tf` | Aurora PostgreSQL 16 Serverless v2 (writer + analytics reader), logical replication on for CDC. |
| `outputs.tf` | Endpoints/ARNs for kubeconfig, Argo CD, and app wiring (no secrets in plaintext). |

## What the baseline provisions

- **VPC** — `/16`, 3 AZs, public/private/database subnet tiers, NAT, VPC flow logs.
- **EKS** — K8s 1.31, Graviton (arm64) Bottlerocket managed node group, IRSA,
  secrets encrypted with our own KMS CMK. Hosts the three planes (`cored`,
  `statutoryd`, `insightd`) — manifests live in `infra/k8s`.
- **Aurora PostgreSQL 16 (Serverless v2)** — writer + analytics reader,
  KMS-encrypted, TLS-forced, `rds.logical_replication=1` for the outbox→CDC bus,
  reachable only from EKS nodes.
- **KMS** — root CMKs for tenant-data envelope encryption, EKS secrets, Aurora
  storage, and RuleSet/Edit-Log signing. Per-tenant AES-256-GCM data keys are an
  application concern (see the comment block in `kms.tf`); crypto-shredding =
  deleting a tenant's wrapped data key.

## Per-tenant data-key story (summary)

KMS holds the **root** key. `cored` calls `GenerateDataKey` with an encryption
context of `{tenant_id}` per tenant; only the **wrapped** key is stored. Plaintext
data keys are re-derived per request via `Decrypt` with the same context (so a
tenant-A key can't be unwrapped as tenant B). DPDP offboarding deletes the wrapped
key — the tenant's data becomes unrecoverable without touching other tenants. Full
detail in `kms.tf`.

## Usage (when tooling is available)

```bash
cd infra/terraform
terraform init \
  -backend-config="bucket=<your-tfstate-bucket>" \
  -backend-config="key=phase0/baseline.tfstate"
terraform fmt -check
terraform validate
terraform plan -var environment=dev
```

## DR (ap-south-2)

A sibling root module instantiates the same child modules with `region =
var.dr_region` and consumes the cross-region KMS multi-region replica
(`aws_kms_key.tally_data` is `multi_region = true`). Aurora Global Database
(writer in ap-south-1, headless secondary in ap-south-2) is the Phase-1 step;
Phase 0 establishes the key + region wiring it depends on.
