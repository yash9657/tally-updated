# providers.tf — AWS provider + S3/DynamoDB remote backend.
#
# DATA RESIDENCY: the primary region is ap-south-1 (Mumbai). All financial
# data, audit logs, backups, and KMS keys are pinned here. ap-south-2
# (Hyderabad) is the disaster-recovery region (RPO <= 5min / RTO <= 1hr, see
# PLAN.md A.3). DR resources live in a separate root module / workspace that
# instantiates the same child modules with `region = var.dr_region`; this root
# wires up the primary only.

provider "aws" {
  region = var.region

  # Guardrail: refuse to apply against an account that is not the intended one.
  # Prevents an operator from accidentally pointing prod creds at the wrong org.
  allowed_account_ids = var.allowed_account_ids

  default_tags {
    tags = local.common_tags
  }
}

# Secondary provider alias for DR-region resources that a primary stack must
# create directly (e.g. a cross-region KMS replica key or backup bucket).
provider "aws" {
  alias  = "dr"
  region = var.dr_region

  allowed_account_ids = var.allowed_account_ids

  default_tags {
    tags = merge(local.common_tags, { Role = "dr" })
  }
}

# ---------------------------------------------------------------------------
# Remote state backend.
#
# State is stored in an S3 bucket in ap-south-1 with native S3 state locking
# (use_lockfile, GA in AWS provider/terraform 1.10+) and server-side
# encryption. The bucket + its versioning/Object-Lock are bootstrapped once,
# out-of-band, by `infra/terraform/bootstrap/` (not included in Phase 0) so the
# backend exists before the first `terraform init` of this root.
#
# Backend blocks cannot use variables; values below are placeholders that an
# operator overrides via `-backend-config=` at init time (or a partial
# backend config file checked in per environment).
# ---------------------------------------------------------------------------
terraform {
  backend "s3" {
    bucket       = "tally-tfstate-ap-south-1" # override per account via -backend-config
    key          = "phase0/baseline.tfstate"
    region       = "ap-south-1"
    encrypt      = true
    use_lockfile = true # native S3 lock; no DynamoDB table required
  }
}
