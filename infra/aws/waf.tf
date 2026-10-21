# CloudFront's global WAF is managed in us-east-1, like this stack's provider.
# Edge throttling complements cored's authenticated limits. This is approximate
# per-IP flood protection, not the user's billing or accounting quota.
variable "api_requests_per_five_minutes" {
  type        = number
  description = "CloudFront WAF request threshold per source IP over five minutes."
  default     = 2000
  validation {
    condition     = var.api_requests_per_five_minutes >= 100 && floor(var.api_requests_per_five_minutes) == var.api_requests_per_five_minutes
    error_message = "Use an integer threshold of at least 100 requests."
  }
}

resource "aws_wafv2_web_acl" "api" {
  name        = "${local.name}-edge"
  description = "Block abusive request volume and known malicious source IPs."
  scope       = "CLOUDFRONT"

  default_action {
    allow {}
  }

  rule {
    name     = "ip-rate-limit"
    priority = 0
    action {
      block {}
    }
    statement {
      rate_based_statement {
        limit                 = var.api_requests_per_five_minutes
        aggregate_key_type    = "IP"
        evaluation_window_sec = 300
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${local.name}-ip-rate-limit"
      sampled_requests_enabled   = false
    }
  }

  rule {
    name     = "known-malicious-ips"
    priority = 1
    override_action {
      none {}
    }
    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesAmazonIpReputationList"
        vendor_name = "AWS"
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${local.name}-ip-reputation"
      sampled_requests_enabled   = false
    }
  }

  # Counts are visible without sampling request headers or financial payloads.
  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = "${local.name}-edge"
    sampled_requests_enabled   = false
  }
}
