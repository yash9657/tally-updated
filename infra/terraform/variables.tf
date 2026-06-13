# variables.tf — inputs for the Phase-0 ap-south-1 baseline.
#
# Every variable is typed and (where it constrains a real invariant) validated,
# so a bad value fails at plan time rather than half-applying. Defaults reflect
# the architecture in PLAN.md A.2/A.3; nothing here provisions resources by
# itself — the root module composes the child modules.

variable "region" {
  description = "Primary AWS region. India data-residency requires an India region."
  type        = string
  default     = "ap-south-1"

  validation {
    condition     = can(regex("^ap-south-[12]$", var.region))
    error_message = "Data residency: region must be an India region (ap-south-1 or ap-south-2)."
  }
}

variable "dr_region" {
  description = "Disaster-recovery region (Hyderabad). Must differ from the primary region."
  type        = string
  default     = "ap-south-2"

  validation {
    condition     = can(regex("^ap-south-[12]$", var.dr_region))
    error_message = "Data residency: dr_region must be an India region (ap-south-1 or ap-south-2)."
  }
}

variable "allowed_account_ids" {
  description = "AWS account IDs this stack is permitted to apply against (safety guardrail). Empty list disables the check."
  type        = list(string)
  default     = []
}

variable "environment" {
  description = "Deployment environment slug (dev | staging | prod). Drives sizing and tags."
  type        = string
  default     = "dev"

  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "environment must be one of: dev, staging, prod."
  }
}

variable "project" {
  description = "Project/product slug used as a name prefix and tag."
  type        = string
  default     = "tally"
}

# --- Networking -------------------------------------------------------------

variable "vpc_cidr" {
  description = "CIDR block for the VPC. /16 leaves room for EKS pod-per-ENI density."
  type        = string
  default     = "10.40.0.0/16"

  validation {
    condition     = can(cidrhost(var.vpc_cidr, 0))
    error_message = "vpc_cidr must be a valid IPv4 CIDR block."
  }
}

variable "az_count" {
  description = "Number of Availability Zones to span (>=2 for HA; 3 recommended for Aurora + EKS)."
  type        = number
  default     = 3

  validation {
    condition     = var.az_count >= 2 && var.az_count <= 3
    error_message = "az_count must be 2 or 3 (ap-south-1 exposes 3 AZs)."
  }
}

variable "single_nat_gateway" {
  description = "Use one NAT gateway (cheaper, dev) instead of one-per-AZ (HA, prod)."
  type        = bool
  default     = true
}

# --- EKS --------------------------------------------------------------------

variable "kubernetes_version" {
  description = "EKS control-plane Kubernetes version (PLAN.md A.3 targets 1.31+)."
  type        = string
  default     = "1.31"
}

variable "eks_node_instance_types" {
  description = "Graviton (arm64) instance types for the managed node group. Bottlerocket AMI set on the node group."
  type        = list(string)
  default     = ["m7g.large"]
}

variable "eks_node_desired_size" {
  description = "Desired size of the baseline managed node group (Karpenter scales the rest)."
  type        = number
  default     = 2
}

variable "eks_node_min_size" {
  description = "Minimum size of the baseline managed node group."
  type        = number
  default     = 2
}

variable "eks_node_max_size" {
  description = "Maximum size of the baseline managed node group."
  type        = number
  default     = 6
}

# --- Aurora PostgreSQL ------------------------------------------------------

variable "db_name" {
  description = "Initial database name created on the Aurora cluster."
  type        = string
  default     = "tally"
}

variable "db_engine_version" {
  description = "Aurora PostgreSQL engine version. Must be a 16.x line (system of record is Postgres 16)."
  type        = string
  default     = "16.4"

  validation {
    condition     = can(regex("^16\\.", var.db_engine_version))
    error_message = "System of record is PostgreSQL 16; db_engine_version must be a 16.x release."
  }
}

variable "db_serverless_min_acu" {
  description = "Aurora Serverless v2 minimum capacity (ACUs)."
  type        = number
  default     = 0.5
}

variable "db_serverless_max_acu" {
  description = "Aurora Serverless v2 maximum capacity (ACUs)."
  type        = number
  default     = 8

  validation {
    condition     = var.db_serverless_max_acu >= var.db_serverless_min_acu
    error_message = "db_serverless_max_acu must be >= db_serverless_min_acu."
  }
}

variable "db_backup_retention_days" {
  description = "Automated backup retention. Section 128(5) demands long retention; WORM archive handles the 8-year obligation, this is the operational window."
  type        = number
  default     = 14

  validation {
    condition     = var.db_backup_retention_days >= 7
    error_message = "db_backup_retention_days must be at least 7."
  }
}

variable "db_deletion_protection" {
  description = "Protect the Aurora cluster from accidental deletion (force-enabled in prod by locals)."
  type        = bool
  default     = false
}
