# versions.tf — Terraform & provider version pins.
#
# Pins are intentionally conservative (boring, proven, current-stable). The AWS
# provider v5 line is the long-lived stable series; Terraform >= 1.9 gives us
# the `removed` block and input-variable validation used below.
#
# NOTE: `terraform` is not installed in the Phase-0 build environment, so this
# HCL is authored for structural correctness and validated by parse/lint where
# tooling permits. `terraform fmt -check` / `terraform validate` run in CI
# (the `iac` job) and are `continue-on-error` until the cloud toolchain is
# provisioned.

terraform {
  required_version = ">= 1.9.0, < 2.0.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}
