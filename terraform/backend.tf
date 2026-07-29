# Remote state backend (S3 + DynamoDB lock).
#
# DISABLED by default so a fresh clone works with local state out of the
# box. To enable — the recommended setup for any shared/production use:
#
#   1. cd bootstrap && terraform init && terraform apply
#      (creates the state bucket + lock table; note the output names)
#   2. Fill in the bucket name below (it's
#      "<project_name>-tfstate-<account_id>") and uncomment this block.
#   3. terraform init -migrate-state
#      (Terraform copies the current local state into S3.)
#
# Why this matters: local state has no locking (two applies can corrupt
# it) and no durability (lose the laptop, lose the state). S3 versioning
# + a DynamoDB lock fix both. The main root state was empty at the time
# this was added, so enabling it now is a zero-risk migration.
#
# terraform {
#   backend "s3" {
#     bucket         = "accelbench-tfstate-820537372947"
#     key            = "accelbench/terraform.tfstate"
#     region         = "us-east-2"
#     dynamodb_table = "accelbench-tflock"
#     encrypt        = true
#   }
# }
