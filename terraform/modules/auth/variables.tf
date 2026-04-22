variable "project_name" {
  description = "Project name used as prefix for Cognito resources"
  type        = string
}

variable "domain_name" {
  description = "FQDN the UI will be served on. Must match the ACM certificate's domain."
  type        = string
}

variable "cognito_domain_prefix" {
  description = "Globally unique prefix for the Cognito hosted UI domain (e.g. 'accelbench-auth')"
  type        = string
}

variable "mfa_configuration" {
  description = "MFA enforcement: OFF, OPTIONAL, or ON"
  type        = string
  default     = "OFF"

  validation {
    condition     = contains(["OFF", "OPTIONAL", "ON"], var.mfa_configuration)
    error_message = "mfa_configuration must be OFF, OPTIONAL, or ON."
  }
}

variable "session_timeout_seconds" {
  description = "ALB session cookie lifetime in seconds"
  type        = number
  default     = 28800
}

variable "tags" {
  description = "Tags applied to all auth resources"
  type        = map(string)
  default     = {}
}

variable "cluster_iam_role_name" {
  description = "Name of the EKS cluster IAM role. The Auto Mode load-balancing controller assumes this role; Cognito auth annotations require DescribeUserPoolClient + elasticloadbalancing:SetSubnets permissions on it."
  type        = string
}

variable "allowed_email_domains" {
  description = "Domains allowed to self-sign-up (e.g. [\"amazon.com\"]). Empty list keeps the pool admin-provisioned only. When non-empty, a pre-signup Lambda trigger enforces the policy and auto-confirms matching users."
  type        = list(string)
  default     = []
}
