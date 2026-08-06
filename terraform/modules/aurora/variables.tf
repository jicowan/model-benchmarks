variable "name" {
  description = "Name for the Aurora cluster"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID where Aurora will be created"
  type        = string
}

variable "private_subnet_ids" {
  description = "List of private subnet IDs for the DB subnet group"
  type        = list(string)
}

variable "eks_node_security_group_id" {
  description = "Security group ID of EKS nodes (for ingress rule)"
  type        = string
}

variable "min_capacity" {
  description = "Minimum ACU capacity for Serverless v2"
  type        = number
  default     = 0.5
}

variable "max_capacity" {
  description = "Maximum ACU capacity for Serverless v2"
  type        = number
  default     = 4
}

variable "skip_final_snapshot" {
  description = "Skip final snapshot when destroying"
  type        = bool
  default     = true
}

# Master-user-password rotation control.
#
# RDS manages the master password in Secrets Manager (manage_master_user_password
# is hard-set true below). RDS ALSO turns on automatic 7-day rotation by default,
# which we do NOT want: nothing syncs the rotated password into the K8s
# `accelbench-db` secret, so a rotation silently breaks DB auth cluster-wide.
#
# AWS/module limitation: rotation cannot be disabled in a single apply. To turn
# it off on a cluster where RDS already enabled it, apply ONCE with
# manage_master_user_password_rotation = true (Terraform adopts the rotation
# resource; rotate_immediately = false so it doesn't fire), THEN apply again with
# it = false (which disables rotation). The committed default is false — the
# steady state we want — and the transition is done via a one-time
# `-var manage_master_user_password_rotation=true` apply (see terraform/README).
variable "manage_master_user_password_rotation" {
  description = "Whether Terraform manages master-user-password rotation. Keep false (rotation OFF). See note above for the one-time true->false transition needed to disable RDS's default rotation."
  type        = bool
  default     = false
}

variable "master_user_password_rotate_immediately" {
  description = "Whether to rotate the secret immediately when rotation management is (re)configured. Kept false so adopting the rotation resource during the disable transition does not trigger an extra rotation."
  type        = bool
  default     = false
}

variable "master_user_password_rotation_days" {
  description = "Days between scheduled rotations while rotation is managed. Only relevant during the transition apply; irrelevant once rotation is disabled."
  type        = number
  default     = 7
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
