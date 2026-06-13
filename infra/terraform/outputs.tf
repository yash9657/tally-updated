# outputs.tf — values downstream tooling (Argo CD, kubeconfig, app secrets) needs.
#
# Connection-string outputs deliberately expose only host/port/dbname — never a
# password. Application credentials are short-TTL Vault dynamic creds (PLAN.md
# A.4); the bootstrap master password lives in Secrets Manager and is surfaced
# by ARN only.

output "region" {
  description = "Primary region this stack deployed into."
  value       = var.region
}

output "vpc_id" {
  description = "VPC ID."
  value       = module.vpc.vpc_id
}

output "private_subnet_ids" {
  description = "Private subnet IDs (EKS nodes/pods)."
  value       = module.vpc.private_subnets
}

output "database_subnet_ids" {
  description = "Database-tier subnet IDs (Aurora)."
  value       = module.vpc.database_subnets
}

output "eks_cluster_name" {
  description = "EKS cluster name (feed to `aws eks update-kubeconfig`)."
  value       = module.eks.cluster_name
}

output "eks_cluster_endpoint" {
  description = "EKS API server endpoint."
  value       = module.eks.cluster_endpoint
}

output "eks_cluster_certificate_authority_data" {
  description = "Base64 cluster CA cert for kubeconfig."
  value       = module.eks.cluster_certificate_authority_data
  sensitive   = true
}

output "eks_oidc_provider_arn" {
  description = "IRSA OIDC provider ARN (bind to pod service accounts for least-privilege IAM)."
  value       = module.eks.oidc_provider_arn
}

output "aurora_writer_endpoint" {
  description = "Aurora writer endpoint (OLTP write-path: cored)."
  value       = aws_rds_cluster.main.endpoint
}

output "aurora_reader_endpoint" {
  description = "Aurora reader endpoint (analytics/AI read-path: insightd, reporting)."
  value       = aws_rds_cluster.main.reader_endpoint
}

output "aurora_master_secret_arn" {
  description = "ARN of the RDS-managed master-user secret in Secrets Manager (bootstrap only)."
  value       = aws_rds_cluster.main.master_user_secret[0].secret_arn
  sensitive   = true
}

output "kms_tenant_data_key_arn" {
  description = "Root CMK ARN for per-tenant data-key envelope encryption."
  value       = aws_kms_key.tally_data.arn
}

output "kms_ruleset_signing_key_arn" {
  description = "CMK ARN for RuleSet bundle + Edit-Log Merkle signing."
  value       = aws_kms_key.ruleset_signing.arn
}
