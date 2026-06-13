# aurora.tf — Amazon Aurora PostgreSQL 16, Serverless v2.
#
# This is the system of record (OLTP), the immutable Edit-Log store, the
# transactional-outbox event bus, and (at MVP) the job queue — PLAN.md A.3
# deliberately keeps ONE datastore. It lives only in the database-subnet tier,
# is reachable only from the EKS node security group, is KMS-encrypted at rest,
# and ships a read replica for the AI plane / reporting offload.
#
# Serverless v2 is acceptable for Phase 0 (PLAN.md C explicitly allows it):
# it autoscales ACUs for spiky dev/pilot load without us right-sizing
# provisioned instances before real traffic exists.

# Security group: Postgres reachable ONLY from the EKS cluster's node group.
resource "aws_security_group" "aurora" {
  name        = "${local.name_prefix}-aurora"
  description = "Aurora PostgreSQL access — ingress only from EKS nodes"
  vpc_id      = module.vpc.vpc_id

  tags = merge(local.common_tags, { Purpose = "aurora" })
}

resource "aws_security_group_rule" "aurora_ingress_from_eks" {
  type                     = "ingress"
  description              = "PostgreSQL from EKS node group"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  security_group_id        = aws_security_group.aurora.id
  source_security_group_id = module.eks.node_security_group_id
}

resource "aws_security_group_rule" "aurora_egress_all" {
  type              = "egress"
  description       = "Allow all egress (return traffic / DNS)"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.aurora.id
}

# Master credential is generated and stored in Secrets Manager (rotated by
# Vault dynamic creds in steady state — PLAN.md A.4; this is the bootstrap
# master only, never used by application code).
resource "aws_db_subnet_group" "aurora" {
  name       = "${local.name_prefix}-aurora"
  subnet_ids = module.vpc.database_subnets
  tags       = local.common_tags
}

resource "aws_rds_cluster_parameter_group" "aurora" {
  name        = "${local.name_prefix}-aurora-pg16"
  family      = "aurora-postgresql16"
  description = "Aurora PG16 cluster params: logical replication for CDC outbox drain"

  # Logical replication powers the transactional-outbox -> CDC event bus.
  parameter {
    name         = "rds.logical_replication"
    value        = "1"
    apply_method = "pending-reboot"
  }

  # Force TLS for every client connection (no plaintext to the system of record).
  parameter {
    name  = "rds.force_ssl"
    value = "1"
  }

  tags = local.common_tags
}

resource "aws_rds_cluster" "main" {
  cluster_identifier = "${local.name_prefix}-aurora"

  engine         = "aurora-postgresql"
  engine_mode    = "provisioned" # required for Serverless v2 scaling config
  engine_version = var.db_engine_version

  database_name   = var.db_name
  master_username = "tally_admin"
  # Password is managed by RDS in Secrets Manager — no plaintext in state.
  manage_master_user_password   = true
  master_user_secret_kms_key_id = aws_kms_key.aurora.arn

  db_subnet_group_name            = aws_db_subnet_group.aurora.name
  db_cluster_parameter_group_name = aws_rds_cluster_parameter_group.aurora.name
  vpc_security_group_ids          = [aws_security_group.aurora.id]

  storage_encrypted = true
  kms_key_id        = aws_kms_key.aurora.arn

  backup_retention_period      = var.db_backup_retention_days
  preferred_backup_window      = "18:00-19:00" # ~23:30 IST, low-traffic
  preferred_maintenance_window = "sun:19:30-sun:20:30"
  copy_tags_to_snapshot        = true

  deletion_protection = local.db_deletion_protection
  # In non-prod we skip the final snapshot for fast teardown; prod always keeps one.
  skip_final_snapshot       = var.environment != "prod"
  final_snapshot_identifier = var.environment == "prod" ? "${local.name_prefix}-aurora-final" : null

  serverlessv2_scaling_configuration {
    min_capacity = var.db_serverless_min_acu
    max_capacity = var.db_serverless_max_acu
  }

  # Export Postgres logs to CloudWatch for audit/forensics.
  enabled_cloudwatch_logs_exports = ["postgresql"]

  tags = local.common_tags
}

# Writer instance.
resource "aws_rds_cluster_instance" "writer" {
  identifier         = "${local.name_prefix}-aurora-writer"
  cluster_identifier = aws_rds_cluster.main.id
  instance_class     = "db.serverless"
  engine             = aws_rds_cluster.main.engine
  engine_version     = aws_rds_cluster.main.engine_version

  performance_insights_enabled    = true
  performance_insights_kms_key_id = aws_kms_key.aurora.arn

  tags = local.common_tags
}

# Reader instance — serves the AI plane (insightd) and reporting offload so
# analytics never contend with the OLTP voucher write-path (PLAN.md A.5).
resource "aws_rds_cluster_instance" "reader" {
  identifier         = "${local.name_prefix}-aurora-reader"
  cluster_identifier = aws_rds_cluster.main.id
  instance_class     = "db.serverless"
  engine             = aws_rds_cluster.main.engine
  engine_version     = aws_rds_cluster.main.engine_version

  promotion_tier = 15 # de-prioritise this reader for failover

  performance_insights_enabled    = true
  performance_insights_kms_key_id = aws_kms_key.aurora.arn

  tags = merge(local.common_tags, { Role = "read-replica-analytics" })
}
