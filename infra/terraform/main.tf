# main.tf — Phase-0 ap-south-1 baseline composition.
#
# This root module wires together the four building blocks of the baseline:
#   * VPC (3-AZ, public/private/database subnet tiers)
#   * EKS (Graviton managed node group; hosts cored / statutoryd / insightd)
#   * Aurora PostgreSQL 16 Serverless v2 (system of record)
#   * KMS (envelope-encryption root key; per-tenant data-key story documented)
#
# Module *implementations* are intentionally not vendored into Phase 0 — we
# stand on the community-maintained, current-stable terraform-aws-modules so we
# do not hand-roll a VPC/EKS. The `source`/`version` pins below are the boring,
# proven choices. Resources we own directly (KMS, Aurora) are declared in their
# own files (kms.tf, aurora.tf) for clarity.

locals {
  name_prefix = "${var.project}-${var.environment}"

  # Force deletion protection on in prod regardless of the input default.
  db_deletion_protection = var.environment == "prod" ? true : var.db_deletion_protection

  # One-NAT-per-AZ in prod for HA; single NAT elsewhere to save cost.
  single_nat_gateway = var.environment == "prod" ? false : var.single_nat_gateway

  common_tags = {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "terraform"
    DataClass   = "regulated-financial-in" # DPDP / India residency marker
    Component   = "phase0-baseline"
  }
}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, var.az_count)

  # Deterministic /20 subnet carve-out per tier per AZ from the VPC /16.
  # Tiers: public (NAT/ALB ingress), private (EKS nodes/pods), database (Aurora).
  public_subnets   = [for i in range(var.az_count) : cidrsubnet(var.vpc_cidr, 4, i)]
  private_subnets  = [for i in range(var.az_count) : cidrsubnet(var.vpc_cidr, 4, i + 4)]
  database_subnets = [for i in range(var.az_count) : cidrsubnet(var.vpc_cidr, 4, i + 8)]
}

# ---------------------------------------------------------------------------
# VPC — terraform-aws-modules/vpc (current stable v5 line).
# ---------------------------------------------------------------------------
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.13"

  name = "${local.name_prefix}-vpc"
  cidr = var.vpc_cidr

  azs              = local.azs
  public_subnets   = local.public_subnets
  private_subnets  = local.private_subnets
  database_subnets = local.database_subnets

  create_database_subnet_group = true

  enable_nat_gateway     = true
  single_nat_gateway     = local.single_nat_gateway
  one_nat_gateway_per_az = !local.single_nat_gateway

  enable_dns_hostnames = true
  enable_dns_support   = true

  # VPC flow logs to CloudWatch for network forensics (security baseline).
  enable_flow_log                      = true
  create_flow_log_cloudwatch_iam_role  = true
  create_flow_log_cloudwatch_log_group = true

  # Tags EKS uses for subnet auto-discovery (load balancers / Karpenter).
  public_subnet_tags = {
    "kubernetes.io/role/elb" = "1"
  }
  private_subnet_tags = {
    "kubernetes.io/role/internal-elb" = "1"
    "karpenter.sh/discovery"          = "${local.name_prefix}-eks"
  }

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# EKS — terraform-aws-modules/eks (current stable v20 line).
#
# Hosts the three deployable planes (cored, statutoryd, insightd). Karpenter is
# installed in-cluster (Helm/Argo, see infra/k8s & Argo CD) and provisions the
# bulk of compute; this managed node group is the small always-on baseline that
# runs system add-ons and Karpenter itself.
# ---------------------------------------------------------------------------
module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.24"

  cluster_name    = "${local.name_prefix}-eks"
  cluster_version = var.kubernetes_version

  # PRIVATE API endpoint by default, in every environment. It used to be public
  # in all non-prod environments to 0.0.0.0/0 (the module's default CIDRs),
  # which trivy flags CRITICAL (AVD-AWS-0040/0041). A staging cluster holding
  # real pilot books is no less sensitive than prod. Public access is now an
  # explicit opt-in that must name the admin CIDRs, and the variable refuses
  # 0.0.0.0/0.
  cluster_endpoint_public_access       = var.cluster_endpoint_public_access
  cluster_endpoint_public_access_cidrs = var.cluster_endpoint_public_access_cidrs
  cluster_endpoint_private_access      = true

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  # Encrypt Kubernetes secrets at rest with our own KMS CMK (envelope), not the
  # default AWS-managed key — see kms.tf.
  cluster_encryption_config = {
    provider_key_arn = aws_kms_key.eks_secrets.arn
    resources        = ["secrets"]
  }

  enable_irsa = true # IAM Roles for Service Accounts (least-privilege pod IAM)

  cluster_addons = {
    coredns                = {}
    kube-proxy             = {}
    vpc-cni                = {}
    aws-ebs-csi-driver     = {}
    eks-pod-identity-agent = {}
  }

  eks_managed_node_groups = {
    baseline = {
      ami_type       = "BOTTLEROCKET_ARM_64" # Bottlerocket + Graviton (PLAN.md A.3)
      instance_types = var.eks_node_instance_types

      min_size     = var.eks_node_min_size
      max_size     = var.eks_node_max_size
      desired_size = var.eks_node_desired_size

      # Bottlerocket: keep host immutable; workloads use IRSA, not node IAM.
      labels = {
        "workload-class" = "system"
      }
    }
  }

  tags = local.common_tags
}
