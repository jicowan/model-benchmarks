variable "region" {
  description = "AWS region to deploy into"
  type        = string
  default     = "us-east-2"
}

variable "project_name" {
  description = "Project name used as prefix for all resources"
  type        = string
  default     = "accelbench"
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "cluster_version" {
  description = "Kubernetes version for the EKS cluster"
  type        = string
  default     = "1.31"
}

variable "karpenter_version" {
  description = "Karpenter Helm chart version"
  type        = string
  default     = "1.9.0"
}

variable "aurora_min_capacity" {
  description = "Minimum ACU capacity for Aurora Serverless v2"
  type        = number
  default     = 0.5
}

variable "aurora_max_capacity" {
  description = "Maximum ACU capacity for Aurora Serverless v2"
  type        = number
  default     = 4
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}

variable "dockerhub_username" {
  description = "Docker Hub username for the ECR pull-through cache. Set via terraform.tfvars or -var."
  type        = string
  default     = ""
}

variable "dockerhub_access_token" {
  description = "Docker Hub access token for the ECR pull-through cache. Set via terraform.tfvars or -var."
  type        = string
  sensitive   = true
  default     = ""
}

# ----------------------------------------------------------------------------
# Auth (Cognito + ALB OIDC) — optional. Defaults are no-ops.
# ----------------------------------------------------------------------------

variable "auth_enabled" {
  description = "Deploy Cognito + external-dns for ALB authentication. When false the remaining auth_* variables are ignored."
  type        = bool
  default     = false
}

variable "domain_name" {
  description = "FQDN the UI will be served on (must match the ACM certificate)."
  type        = string
  default     = ""
}

variable "hosted_zone_id" {
  description = "Route53 public hosted zone ID containing domain_name. Used by external-dns."
  type        = string
  default     = ""
}

variable "acm_certificate_arn" {
  description = "Pre-existing ACM certificate ARN (us-west-2) covering domain_name. Must be passed through to Helm as ingress.certificateArn."
  type        = string
  default     = ""
}

variable "cognito_domain_prefix" {
  description = "Globally-unique prefix for the Cognito hosted-UI domain (e.g. 'accelbench-auth')."
  type        = string
  default     = ""
}

variable "mfa_configuration" {
  description = "Cognito MFA policy: OFF, OPTIONAL, or ON."
  type        = string
  default     = "OFF"
}

variable "session_timeout_seconds" {
  description = "ALB session cookie lifetime in seconds."
  type        = number
  default     = 28800
}

variable "allowed_email_domains" {
  description = "Email domains permitted to self-sign up via the Cognito Hosted UI (e.g. [\"amazon.com\"]). Empty list (default) keeps the pool admin-provisioned only — self-signup is disabled."
  type        = list(string)
  default     = []
}
