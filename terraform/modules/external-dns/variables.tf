variable "project_name" {
  description = "Project name used as prefix"
  type        = string
}

variable "cluster_name" {
  description = "EKS cluster name (for Pod Identity association)"
  type        = string
}

variable "hosted_zone_id" {
  description = "Route53 hosted zone ID that external-dns may write to"
  type        = string
}

variable "domain_filter" {
  description = "DNS name suffix external-dns is allowed to manage"
  type        = string
}

variable "namespace" {
  description = "Kubernetes namespace for external-dns"
  type        = string
  default     = "kube-system"
}

variable "chart_version" {
  description = "external-dns Helm chart version"
  type        = string
  default     = "1.15.0"
}

variable "tags" {
  description = "Tags applied to IAM resources"
  type        = map(string)
  default     = {}
}
