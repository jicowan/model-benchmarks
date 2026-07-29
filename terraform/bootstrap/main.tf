# Remote-state bootstrap (run ONCE, before enabling the S3 backend on the
# main root in ../).
#
# Chicken-and-egg: the main Terraform root wants to store its state in S3
# with a DynamoDB lock, but those resources must exist first and can't
# store THEIR own state in themselves. This tiny root creates them with
# local state (checked in here, it's just two resource IDs, no secrets).
#
# Usage:
#   cd terraform/bootstrap
#   terraform init && terraform apply
#   # then uncomment the backend "s3" block in ../backend.tf and run
#   # `terraform init -migrate-state` in ../
#
# Idempotent and safe to re-run. Destroying this is destructive to remote
# state for the whole project — don't, unless you're tearing everything
# down for good.

terraform {
  required_version = ">= 1.7.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.region
}

variable "region" {
  type    = string
  default = "us-east-2"
}

variable "project_name" {
  type    = string
  default = "accelbench"
}

data "aws_caller_identity" "current" {}

# State bucket: versioned (so a corrupt/lost state can be recovered from a
# prior version) and encrypted. force_destroy stays false — you never want
# an accidental teardown to nuke state history.
resource "aws_s3_bucket" "tfstate" {
  bucket = "${var.project_name}-tfstate-${data.aws_caller_identity.current.account_id}"
}

resource "aws_s3_bucket_versioning" "tfstate" {
  bucket = aws_s3_bucket.tfstate.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "tfstate" {
  bucket = aws_s3_bucket.tfstate.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "aws:kms"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "tfstate" {
  bucket                  = aws_s3_bucket.tfstate.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Lock table: prevents two `terraform apply`s from corrupting state by
# writing concurrently. PAY_PER_REQUEST so it costs ~nothing at rest.
resource "aws_dynamodb_table" "tflock" {
  name         = "${var.project_name}-tflock"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"
  attribute {
    name = "LockID"
    type = "S"
  }
}

output "state_bucket" {
  value = aws_s3_bucket.tfstate.id
}

output "lock_table" {
  value = aws_dynamodb_table.tflock.name
}
