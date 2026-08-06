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

variable "force_destroy_buckets" {
  description = <<-EOT
    Allow `terraform destroy` to delete S3 buckets that still contain
    objects. Defaults to false so a normal destroy can't silently wipe
    cached model weights or results. Set to true for an intentional
    teardown (avoids the manual `aws s3 rm --recursive` step that a
    non-empty models bucket otherwise forces).
  EOT
  type        = bool
  default     = false
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "cluster_version" {
  description = "Kubernetes version for the EKS cluster"
  type        = string
  default     = "1.36"
}

variable "karpenter_version" {
  description = "Karpenter Helm chart version"
  type        = string
  default     = "1.14.0"
}

# ---------- Multi-node / distributed inference (PRD-55) ----------

variable "enable_multinode" {
  description = <<-EOT
    Provision the distributed-inference foundations: one EC2 cluster
    placement group + one static EFA GPU Karpenter NodePool per AZ, and
    the DRA drivers (NVIDIA GPU + AWS DRANET/EFA). Greenfield only. Off by
    default — the single-instance platform is unaffected. Requires EKS
    1.34+ and Karpenter >= v1.11.
  EOT
  type        = bool
  default     = false
}

# Serving-stack chart/CRD versions (gateway.tf). Pinned; verified against
# each project's GitHub releases. LWS chart version has NO `v` prefix.
variable "lws_version" {
  description = "LeaderWorkerSet Helm chart version (oci://registry.k8s.io/lws/charts/lws)."
  type        = string
  default     = "0.9.0"
}

variable "gateway_api_version" {
  description = "Gateway API release tag for standard-install.yaml CRDs."
  type        = string
  default     = "v1.6.1"
}

variable "inference_extension_version" {
  description = "Gateway API Inference Extension release tag for manifests.yaml (InferencePool v1 GA)."
  type        = string
  default     = "v1.5.0"
}

variable "envoy_gateway_version" {
  description = "Envoy Gateway Helm chart version (oci://docker.io/envoyproxy/gateway-helm)."
  type        = string
  default     = "v1.8.3"
}

variable "envoy_ai_gateway_version" {
  description = "Envoy AI Gateway version — shared by ai-gateway-crds-helm + ai-gateway-helm, and the envoy-gateway-values.yaml ref."
  type        = string
  default     = "v1.0.0"
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

# Master-user-password rotation. Default false = rotation OFF (desired steady
# state — nothing syncs a rotated RDS password into the K8s accelbench-db secret,
# so rotation breaks DB auth). RDS enables rotation by default when it manages the
# password; disabling requires a ONE-TIME transition apply with this set to true
# (Terraform adopts the rotation resource, rotate_immediately=false), then a second
# apply back to false which disables it. Steady state stays false.
variable "manage_master_user_password_rotation" {
  description = "One-time escape hatch to disable RDS's default master-password rotation via a true->false apply. Keep false in steady state."
  type        = bool
  default     = false
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

# PRD-66 Part 2a: GHCR pull-through for the co-located PP image (llm-d-aws).
# GHCR requires auth even for public images. The token is a GitHub PAT with the
# read:packages scope; secret → gitignored tfvars like dockerhub_access_token.
variable "github_username" {
  description = "GitHub username for the GHCR ECR pull-through cache. Set via terraform.tfvars or -var."
  type        = string
  default     = ""
}

variable "github_token" {
  description = "GitHub PAT (read:packages scope) for the GHCR ECR pull-through cache. Set via terraform.tfvars or -var."
  type        = string
  sensitive   = true
  default     = ""
}

variable "enable_cluster_creator_admin_permissions" {
  description = <<-EOT
    Whether the EKS module creates a cluster-admin access entry for the IAM
    principal running `terraform apply`. Default true (new installs). Set to
    false on a cluster where that principal already has an access entry
    outside Terraform — otherwise the apply fails with "access entry already
    exists".
  EOT
  type        = bool
  default     = true
}

variable "cluster_admin_principal_arns" {
  description = <<-EOT
    IAM principal ARNs (users/roles) to grant cluster-admin via an EKS
    access entry + AmazonEKSClusterAdminPolicy association. The operator
    passes in the principal that runs `terraform apply` (and any others
    that need kubectl/Helm access), e.g.
      cluster_admin_principal_arns = ["arn:aws:iam::<acct>:user/kubernetes"]
    Observed: enable_cluster_creator_admin_permissions alone did NOT create
    an entry for the apply principal on greenfield builds, so the
    kube/helm/kubectl providers failed to authenticate mid-apply. Setting
    this codifies the manual access-entry step that unblocked those builds.
    Empty (default) creates none — relies on the module's creator-admin.
  EOT
  type        = list(string)
  default     = []
}

variable "manage_cluster" {
  description = <<-EOT
    When true (default, greenfield), Terraform provisions the VPC, EKS
    cluster, node IAM, and the cluster-level addons listed under the
    install_* variables below.

    When false (brownfield), AccelBench is installed into an EXISTING
    EKS cluster. cluster_name, vpc_id, and private_subnet_ids become
    required. Per-addon install_* flags let the operator skip
    components their cluster already runs (Karpenter, ALB controller,
    NVIDIA device plugin, Pod Identity agent, EBS CSI driver).

    See docs/brownfield.md for a worked example.
  EOT
  type        = bool
  default     = true
}

variable "cluster_name" {
  description = "Existing EKS cluster name. Required when manage_cluster=false. Ignored otherwise (greenfield derives it from project_name)."
  type        = string
  default     = ""
}

variable "vpc_id" {
  description = "VPC holding the existing cluster. Required when manage_cluster=false."
  type        = string
  default     = ""
}

variable "private_subnet_ids" {
  description = "Private subnets where AccelBench data-plane resources land (RDS, optional ALB, and the subnets Karpenter provisions nodes into). Required when manage_cluster=false."
  type        = list(string)
  default     = []
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

variable "auth_enabled" {
  description = <<-EOT
    When true (default), provisions Cognito + ACM/public-ingress resources
    for in-app user authentication. Helm chart must also be deployed with
    default cognito.authDisabled=false.

    Set to false for lab / bring-up deployments where access control is
    handled upstream (VPN, Kubernetes RBAC, port-forward). Implications:
      - Cognito user pool + app client are NOT created.
      - ACM cert + public DNS records are NOT created (ingress_mode is
        forced to empty regardless of the user-supplied value).
      - The Helm chart must be installed with cognito.authDisabled=true
        so the api pod starts in AUTH_DISABLED=1 mode.
      - The backend's startup log prints a loud "AUTH DISABLED" banner.

    Never combine auth_enabled=false with a publicly-reachable ingress.
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

variable "install_karpenter_controller" {
  description = <<-EOT
    PRD-53: install the Karpenter controller + CRDs via Helm. Set false
    on brownfield clusters where Karpenter is already running (>= v1.9
    required). AccelBench's NodePools + EC2NodeClasses are applied
    regardless — they layer on top of an existing controller.
  EOT
  type        = bool
  default     = true
}

variable "karpenter_namespace" {
  description = <<-EOT
    Namespace where the existing Karpenter controller Deployment lives.
    Only consulted in brownfield mode (manage_cluster=false +
    install_karpenter_controller=false) to look up the controller's
    image and assert version >= v1.9. Default "kube-system" matches the
    upstream chart's default; some operators install Karpenter in a
    namespace named "karpenter" or in a per-tenant namespace, in which
    case set this accordingly.
  EOT
  type        = string
  default     = "kube-system"
}

variable "karpenter_release_name" {
  description = <<-EOT
    Name of the Karpenter controller Deployment in karpenter_namespace.
    Only consulted in brownfield mode (manage_cluster=false +
    install_karpenter_controller=false). Default "karpenter" matches
    the upstream chart; if the operator installed under a different
    Helm release name, set this to that name. Confirm with:
      kubectl get deploy -n <karpenter_namespace>
  EOT
  type        = string
  default     = "karpenter"
}

variable "install_nvidia_device_plugin" {
  description = <<-EOT
    PRD-53: install the NVIDIA device plugin DaemonSet. Set false if the
    cluster already runs it (check: kubectl get ds -n kube-system |
    grep nvidia-device-plugin).
  EOT
  type        = bool
  default     = true
}

variable "manage_pull_through_cache" {
  description = <<-EOT
    PRD-53: create an ECR pull-through cache rule for Docker Hub + grant
    the node IAM role ECR pull-through permissions. Set false to skip
    — operators who set image.vllm.repository in Helm values to a
    public-ECR vLLM mirror (see docs/deployment.md) don't need the
    cache.
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

variable "admin_email" {
  description = <<-EOT
    Email address for the Cognito bootstrap admin (PRD-44). A temporary
    password is emailed to this address on `terraform apply` and must be
    changed on first login. Required for fresh installs so the cluster
    ships with a working admin on day one. Leave empty to skip — useful
    when the admin was created manually via the AWS console.
  EOT
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
