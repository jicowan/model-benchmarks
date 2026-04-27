variable "cluster_name" {
  description = "Name of the EKS cluster"
  type        = string
}

variable "cluster_version" {
  description = "Kubernetes version for the EKS cluster"
  type        = string
  default     = "1.31"
}

variable "vpc_id" {
  description = "VPC ID where the cluster will be created"
  type        = string
}

variable "private_subnet_ids" {
  description = "List of private subnet IDs for the cluster"
  type        = list(string)
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}

# When true (default), the EKS module creates an access entry for the IAM
# principal running `terraform apply`. Set to false on clusters where an
# access entry for that principal already exists outside Terraform — e.g.
# the original AccelBench cluster where `user/kubernetes` has a manually
# created entry. Otherwise the apply fails with "access entry already exists".
variable "enable_cluster_creator_admin_permissions" {
  description = "Grant cluster-admin to the IAM principal that runs terraform apply. Disable on pre-existing clusters where the principal already has an access entry."
  type        = bool
  default     = true
}
