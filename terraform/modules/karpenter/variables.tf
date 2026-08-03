variable "cluster_name" {
  description = "Name of the EKS cluster"
  type        = string
}

variable "cluster_endpoint" {
  description = "Endpoint for the EKS cluster API server"
  type        = string
}

variable "kubernetes_version" {
  description = <<-EOT
    Kubernetes minor version (e.g. "1.36") of the cluster. Used to build
    the SSM parameter path that resolves the EKS-optimized AMI ids for the
    GPU / accelerated node classes, so benchmark hardware is reproducible
    (pinned to a concrete AMI id in the plan) rather than drifting with
    Karpenter's `al2023@latest` alias.
  EOT
  type        = string
}

variable "karpenter_version" {
  description = "Karpenter Helm chart version"
  type        = string
  default     = "1.14.0"
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

# PRD-55: DRA drivers for the multi-node pool. Both default OFF so the
# single-instance install is unchanged; the multi-node NodePool slice
# turns them on. They target ONLY nodes labeled accelbench.io/dra=true
# (the classic device plugin is excluded from those nodes — KEP-5004).

variable "install_dra_drivers" {
  description = <<-EOT
    Install the NVIDIA DRA driver (GPU allocation) + the AWS EFA DRA
    driver (DRANET) for the multi-node/distributed-inference pool. Both
    run only on nodes labeled accelbench.io/dra=true. Requires EKS 1.34+
    (DRANET floor) and that the multi-node static NodePool exists.
  EOT
  type        = bool
  default     = false
}

variable "enable_multinode" {
  description = "Create the per-AZ static EFA GPU EC2NodeClasses + NodePools for distributed inference."
  type        = bool
  default     = false
}

variable "multinode_placement_groups" {
  description = <<-EOT
    Map of AZ name -> EC2 cluster placement-group name. One static
    NodePool + EC2NodeClass is created per entry, pinned to that AZ and
    selecting that placement group via spec.placementGroupSelector.
    Empty (default) creates no multi-node pools.
  EOT
  type        = map(string)
  default     = {}
}

variable "multinode_subnets" {
  description = <<-EOT
    Map of AZ name -> private subnet ID. Each multi-node NodeClass selects
    its subnet by id (subnetSelectorTerms has no AZ field and the subnets
    aren't zone-tagged), which pins the pool to that AZ. Keys must match
    multinode_placement_groups.
  EOT
  type        = map(string)
  default     = {}
}

variable "nvidia_dra_driver_version" {
  description = "nvidia-dra-driver-gpu Helm chart version (NGC: https://helm.ngc.nvidia.com/nvidia)."
  type        = string
  default     = "25.12.0"
}

variable "aws_dranet_version" {
  description = "aws-dranet (EFA DRA / DRANET) Helm chart version (eks/aws-dranet). Verify with `helm search repo eks/aws-dranet --versions` before pinning."
  type        = string
  default     = "1.0.0"
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
