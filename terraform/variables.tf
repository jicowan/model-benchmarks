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

variable "manage_accelbench_namespace" {
  description = <<-EOT
    Whether Terraform should create the `accelbench` namespace and DATABASE_URL
    secret. Default true (new installs). Set to false on an existing cluster
    where the namespace was created manually, then `terraform import` the
    resources instead to avoid re-creation conflicts.
  EOT
  type        = bool
  default     = true
}

# ---------- Public ingress (PRD-43a) ----------
# Everything below is opt-in. Default config creates no ALB, no cert, no DNS
# records — the app is reachable via kubectl port-forward only.

variable "install_alb_controller" {
  description = <<-EOT
    Install the AWS Load Balancer Controller (chart v3.2.2) via Helm. Set
    false if your cluster already has it from another source.
  EOT
  type        = bool
  default     = true
}

variable "ingress_mode" {
  description = <<-EOT
    TLS mode for the public ingress. Leave empty to skip ingress Terraform
    entirely (port-forward only). Options:
      - "acm-route53":  Terraform creates + DNS-validates an ACM cert in the
                        Route 53 hosted zone named in hosted_zone_name, and
                        writes a public A-alias for app_host to the ALB.
      - "acm-existing": You provide a pre-issued ACM cert ARN in
                        existing_certificate_arn and handle DNS yourself.
      - "none":         HTTP only. For dev/CI clusters — not for production.
  EOT
  type        = string
  default     = ""
  validation {
    condition     = contains(["", "acm-route53", "acm-existing", "none"], var.ingress_mode)
    error_message = "ingress_mode must be one of: \"\", acm-route53, acm-existing, none."
  }
}

variable "app_host" {
  description = "Public hostname for the app (e.g. accelbench.example.com). Required if ingress_mode != \"\"."
  type        = string
  default     = ""
}

variable "hosted_zone_name" {
  description = "Route 53 hosted zone name (e.g. example.com). Required only when ingress_mode = acm-route53."
  type        = string
  default     = ""
}

variable "existing_certificate_arn" {
  description = "Pre-existing ACM certificate ARN. Required only when ingress_mode = acm-existing."
  type        = string
  default     = ""
}

variable "ingress_deployed" {
  description = <<-EOT
    Set to true only after the Helm Ingress has been deployed and the ALB
    controller has provisioned the ALB. This gates the aws_lb data-source
    lookup (for the public Route 53 alias) which fails until the ALB exists.
    Flow for the initial install with ingress_mode=acm-route53:
      1. terraform apply                          (ingress_deployed=false, default)
      2. helm upgrade --set ingress.enabled=true  (ALB provisioned)
      3. Flip ingress_deployed=true in tfvars
      4. terraform apply                          (writes the A-alias)
  EOT
  type        = bool
  default     = false
}
