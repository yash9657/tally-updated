# Tally `cored` on AWS:
#   CloudFront -> Lambda Function URL -> cored (Go) via the Lambda Web Adapter
#   -> shared Aurora Serverless v2 (platform stack, ~/code/aws-platform).
#   EventBridge Scheduler replaces the Vercel crons.
#
# Build the zip first: ./build.sh   (writes build/cored.zip)
# Runbook: README.md

terraform {
  required_version = ">= 1.10"
  required_providers {
    aws    = { source = "hashicorp/aws", version = "~> 6.0" }
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
  backend "s3" {
    bucket       = "tfstate-481912007673-use1"
    key          = "tally/terraform.tfstate"
    region       = "us-east-1"
    use_lockfile = true
    encrypt      = true
  }
}

provider "aws" {
  region = "us-east-1"
  default_tags {
    tags = { project = "tally", managed_by = "terraform" }
  }
}

data "terraform_remote_state" "platform" {
  backend = "s3"
  config = {
    bucket = "tfstate-481912007673-use1"
    key    = "platform/terraform.tfstate"
    region = "us-east-1"
  }
}

locals {
  name     = "tally-cored"
  platform = data.terraform_remote_state.platform.outputs
  db_name  = "tally"
  # Set once the ACM certificate is validated (see README "Cutover").
  api_domain = var.api_domain
}

variable "api_domain" {
  description = "Custom domain for CloudFront, e.g. api.tallyweb.world. Empty until the certificate is issued."
  type        = string
  default     = ""
}

variable "web_origin" {
  type    = string
  default = "https://tallyweb.world"
}

# ---------------------------------------------------------------- secrets

resource "random_password" "app_role" {
  length  = 40
  special = false
}

resource "random_password" "statutory_role" {
  length  = 40
  special = false
}

# ed25519 private key (base64url, 64 bytes). Terraform's random provider
# cannot produce one, so it is generated once and stored by hand:
#   go run keygen.go key.txt && aws secretsmanager create-secret \
#     --name tally/auth-dev-signing-key --secret-string file://key.txt
data "aws_secretsmanager_secret_version" "dev_signing_key" {
  secret_id = "tally/auth-dev-signing-key"
}

resource "random_password" "cron_secret" {
  length  = 48
  special = false
}

locals {
  app_dsn = "postgres://tally_app:${random_password.app_role.result}@${local.platform.db_endpoint}:5432/${local.db_name}?sslmode=require&pool_max_conns=4&connect_timeout=30"
}

# Source of truth for humans and CI (migrations read it); the function gets
# the same values as environment variables.
resource "aws_secretsmanager_secret" "cored" {
  name                    = "tally/cored"
  recovery_window_in_days = 7
}

resource "aws_secretsmanager_secret_version" "cored" {
  secret_id = aws_secretsmanager_secret.cored.id
  secret_string = jsonencode({
    APP_DSN                       = local.app_dsn
    TALLY_APP_ROLE_PASSWORD       = random_password.app_role.result
    TALLY_STATUTORY_ROLE_PASSWORD = random_password.statutory_role.result
    CRON_SECRET                   = random_password.cron_secret.result
  })
}

# ---------------------------------------------------------------- function

resource "aws_iam_role" "fn" {
  name = local.name
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "fn_vpc" {
  role       = aws_iam_role.fn.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

resource "aws_cloudwatch_log_group" "fn" {
  name              = "/aws/lambda/${local.name}"
  retention_in_days = 14
}

resource "aws_lambda_function" "cored" {
  function_name = local.name
  role          = aws_iam_role.fn.arn
  runtime       = "provided.al2023"
  architectures = ["arm64"]
  handler       = "bootstrap"
  memory_size   = 1024
  # The nightly insight pass behind /internal/cron/tick can run long.
  timeout = 900

  filename         = "${path.module}/build/cored.zip"
  source_code_hash = filebase64sha256("${path.module}/build/cored.zip")

  # Lambda Web Adapter: runs as an extension, turns invocations into HTTP
  # requests against cored on :8080.
  layers = ["arn:aws:lambda:us-east-1:753240598075:layer:LambdaAdapterLayerArm64:30"]

  vpc_config {
    subnet_ids         = local.platform.private_subnet_ids
    security_group_ids = [local.platform.db_client_security_group_id]
  }

  environment {
    variables = {
      AWS_LWA_PORT                 = "8080"
      AWS_LWA_READINESS_CHECK_PATH = "/healthz"
      # Aurora resumes from 0 ACU in ~15 s; cored pings the DB at boot.
      AWS_LWA_ASYNC_INIT = "true"

      PORT                          = "8080"
      APP_DSN                       = local.app_dsn
      AUTH_DEV_TOKEN_ENABLED        = "true"
      AUTH_DEV_TOKEN_ROUTE_DISABLED = "true"
      AUTH_DEV_SIGNING_KEY          = data.aws_secretsmanager_secret_version.dev_signing_key.secret_string
      AUTH_WEBAUTHN_RPID            = trimprefix(var.web_origin, "https://")
      AUTH_WEBAUTHN_ORIGIN          = var.web_origin
      CRON_SECRET                   = random_password.cron_secret.result
    }
  }

  depends_on = [aws_cloudwatch_log_group.fn, aws_iam_role_policy_attachment.fn_vpc]
}

# Public URL; CloudFront sits in front for the custom domain and TLS. OAC is
# not used because it requires clients to sign POST bodies.
resource "aws_lambda_function_url" "cored" {
  function_name      = aws_lambda_function.cored.function_name
  authorization_type = "NONE"
  invoke_mode        = "BUFFERED"
}

# ---------------------------------------------------------------- edge

resource "aws_acm_certificate" "api" {
  count             = local.api_domain == "" ? 0 : 1
  domain_name       = local.api_domain
  validation_method = "DNS"
  lifecycle { create_before_destroy = true }
}

resource "aws_cloudfront_distribution" "api" {
  enabled         = true
  comment         = "Tally API"
  price_class     = "PriceClass_100"
  http_version    = "http2and3"
  is_ipv6_enabled = true
  aliases         = local.api_domain == "" ? [] : [local.api_domain]

  origin {
    origin_id   = "cored"
    domain_name = trimsuffix(trimprefix(aws_lambda_function_url.cored.function_url, "https://"), "/")
    custom_origin_config {
      http_port                = 80
      https_port               = 443
      origin_protocol_policy   = "https-only"
      origin_ssl_protocols     = ["TLSv1.2"]
      origin_read_timeout      = 60
      origin_keepalive_timeout = 60
    }
  }

  default_cache_behavior {
    target_origin_id       = "cored"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"]
    cached_methods         = ["GET", "HEAD"]
    compress               = true
    # Managed policies: CachingDisabled, AllViewerExceptHostHeader.
    cache_policy_id          = "4135ea2d-6df8-44a3-9df3-4b5a84be39ad"
    origin_request_policy_id = "b689b0a8-53d0-40ab-baf2-68738e2966ac"
  }

  restrictions {
    geo_restriction { restriction_type = "none" }
  }

  viewer_certificate {
    cloudfront_default_certificate = local.api_domain == ""
    acm_certificate_arn            = local.api_domain == "" ? null : aws_acm_certificate.api[0].arn
    ssl_support_method             = local.api_domain == "" ? null : "sni-only"
    minimum_protocol_version       = local.api_domain == "" ? null : "TLSv1.2_2021"
  }
}

# ---------------------------------------------------------------- crons

# Scheduler invokes the function directly with a Function-URL-shaped event,
# which the web adapter turns into GET <path> with the cron bearer token.
# This skips CloudFront's 60 s origin timeout.
locals {
  crons = {
    tick   = { path = "/internal/cron/tick", schedule = "cron(0 * * * ? *)" }
    anchor = { path = "/internal/cron/anchor", schedule = "cron(30 21 * * ? *)" }
  }
}

resource "aws_iam_role" "scheduler" {
  name = "${local.name}-scheduler"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "scheduler.amazonaws.com" }
      Action    = "sts:AssumeRole"
      Condition = { StringEquals = { "aws:SourceAccount" = "481912007673" } }
    }]
  })
}

resource "aws_iam_role_policy" "scheduler" {
  role = aws_iam_role.scheduler.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "lambda:InvokeFunction"
      Resource = aws_lambda_function.cored.arn
    }]
  })
}

resource "aws_scheduler_schedule" "cron" {
  for_each                     = local.crons
  name                         = "${local.name}-${each.key}"
  schedule_expression          = each.value.schedule
  schedule_expression_timezone = "UTC"
  flexible_time_window { mode = "OFF" }

  target {
    arn      = aws_lambda_function.cored.arn
    role_arn = aws_iam_role.scheduler.arn
    retry_policy { maximum_retry_attempts = 0 }
    input = jsonencode({
      version        = "2.0"
      routeKey       = "$default"
      rawPath        = each.value.path
      rawQueryString = ""
      headers = {
        authorization = "Bearer ${random_password.cron_secret.result}"
        host          = "scheduler.internal"
        "user-agent"  = "eventbridge-scheduler"
      }
      requestContext = {
        accountId    = "481912007673"
        apiId        = "scheduler"
        domainName   = "scheduler.internal"
        domainPrefix = "scheduler"
        requestId    = "cron-${each.key}"
        routeKey     = "$default"
        stage        = "$default"
        time         = "01/Jan/2026:00:00:00 +0000"
        timeEpoch    = 0
        http = {
          method    = "GET"
          path      = each.value.path
          protocol  = "HTTP/1.1"
          sourceIp  = "127.0.0.1"
          userAgent = "eventbridge-scheduler"
        }
      }
      isBase64Encoded = false
    })
  }
}

# ---------------------------------------------------------------- alarms

resource "aws_cloudwatch_metric_alarm" "errors" {
  alarm_name          = "${local.name}-errors"
  namespace           = "AWS/Lambda"
  metric_name         = "Errors"
  dimensions          = { FunctionName = aws_lambda_function.cored.function_name }
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 5
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"
}

# ---------------------------------------------------------------- outputs

output "function_url" { value = aws_lambda_function_url.cored.function_url }
output "cloudfront_domain" { value = aws_cloudfront_distribution.api.domain_name }
output "acm_validation" {
  value = local.api_domain == "" ? [] : [
    for o in aws_acm_certificate.api[0].domain_validation_options : { name = o.resource_record_name, value = o.resource_record_value }
  ]
}
