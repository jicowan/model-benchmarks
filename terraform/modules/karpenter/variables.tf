variable "cluster_name" {
  description = "Name of the EKS cluster"
  type        = string
}

variable "cluster_endpoint" {
  description = "Endpoint for the EKS cluster API server"
  type        = string
}

variable "karpenter_version" {
  description = "Karpenter Helm chart version"
  type        = string
  default     = "1.9.0"
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}

# PRD-53: brownfield install toggles. NodePools + EC2NodeClasses + the
# IAM side of the Karpenter module are always applied — only the
# controller Helm release and the NVIDIA device plugin are gated.

variable "install_controller" {
  description = "Install the Karpenter controller + CRDs via Helm. Set false on clusters where Karpenter is already running (>= v1.9 required)."
  type        = bool
  default     = true
}

variable "install_nvidia_device_plugin" {
  description = "Install the NVIDIA device plugin DaemonSet."
  type        = bool
  default     = true
}

variable "manage_pull_through_cache" {
  description = "Create the ECR pull-through permissions policy on the Karpenter node role. When false, skips the policy and assumes pods pull from public registries or a pre-existing cache."
  type        = bool
  default     = true
}

variable "cluster_oidc_issuer_url" {
  description = "The existing cluster's OIDC issuer URL. Required when install_controller=false so the module.karpenter IAM role trust policy can target the existing OIDC provider instead of expecting the EKS module to provide it."
  type        = string
  default     = ""
}
