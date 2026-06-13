# kms.tf — KMS root keys for envelope encryption.
#
# CRYPTO MODEL (PLAN.md A.3 "At-rest crypto"):
#   AWS KMS (HSM-backed) holds the *root* CMKs. Application-level at-rest
#   encryption uses ENVELOPE encryption with PER-TENANT AES-256-GCM data keys:
#
#     1. On tenant creation, `cored` calls KMS:GenerateDataKey under the
#        `tally_data` CMK below, scoped by an encryption-context of
#        { "tenant_id": "<company_id>" }. KMS returns a plaintext data key
#        (used immediately in-memory) + a ciphertext blob (the wrapped key).
#     2. Only the wrapped blob is persisted (in `tenant_key` rows). The
#        plaintext key never lands on disk and is re-derived per request via
#        KMS:Decrypt with the SAME encryption context (so a wrapped key for
#        tenant A cannot be decrypted while claiming to be tenant B).
#     3. DPDP offboarding == "crypto-shredding": delete the tenant's wrapped
#        data key and the tenant's ciphertext is permanently unrecoverable,
#        without touching the shared KMS CMK or other tenants.
#
# The root CMK itself is region-bound (ap-south-1) for data residency; a DR
# replica key in ap-south-2 is created in the DR stack so wrapped blobs decrypt
# after a regional failover. Key rotation is enabled (annual, AWS-managed).

# Root CMK for tenant data-key envelope encryption (the per-tenant story above).
resource "aws_kms_key" "tally_data" {
  description             = "${local.name_prefix} per-tenant data-key envelope root (HSM-backed)"
  deletion_window_in_days = 30
  enable_key_rotation     = true
  multi_region            = true # enables an ap-south-2 replica for DR

  tags = merge(local.common_tags, { Purpose = "tenant-data-envelope" })
}

resource "aws_kms_alias" "tally_data" {
  name          = "alias/${local.name_prefix}-tally-data"
  target_key_id = aws_kms_key.tally_data.key_id
}

# CMK that encrypts Kubernetes Secrets at rest in the EKS etcd store.
resource "aws_kms_key" "eks_secrets" {
  description             = "${local.name_prefix} EKS secret-envelope CMK"
  deletion_window_in_days = 30
  enable_key_rotation     = true

  tags = merge(local.common_tags, { Purpose = "eks-secrets" })
}

resource "aws_kms_alias" "eks_secrets" {
  name          = "alias/${local.name_prefix}-eks-secrets"
  target_key_id = aws_kms_key.eks_secrets.key_id
}

# CMK that encrypts the Aurora cluster storage at rest.
resource "aws_kms_key" "aurora" {
  description             = "${local.name_prefix} Aurora PostgreSQL storage CMK"
  deletion_window_in_days = 30
  enable_key_rotation     = true

  tags = merge(local.common_tags, { Purpose = "aurora-storage" })
}

resource "aws_kms_alias" "aurora" {
  name          = "alias/${local.name_prefix}-aurora"
  target_key_id = aws_kms_key.aurora.key_id
}

# CMK for signing RuleSet bundles + anchoring the Edit-Log Merkle roots.
# Ed25519 (Sigstore-style detached signatures, PLAN.md B.5). KMS sign/verify so
# the private key never leaves the HSM.
resource "aws_kms_key" "ruleset_signing" {
  description              = "${local.name_prefix} RuleSet bundle + Edit-Log Merkle signing (Ed25519)"
  key_usage                = "SIGN_VERIFY"
  customer_master_key_spec = "ECC_NIST_P256" # Ed25519 not yet GA in KMS; P-256 placeholder, swap when available
  deletion_window_in_days  = 30

  tags = merge(local.common_tags, { Purpose = "ruleset-signing" })
}

resource "aws_kms_alias" "ruleset_signing" {
  name          = "alias/${local.name_prefix}-ruleset-signing"
  target_key_id = aws_kms_key.ruleset_signing.key_id
}
