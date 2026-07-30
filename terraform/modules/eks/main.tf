module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 21.0"

  # v21: `cluster_name` → `name`, `cluster_version` → `kubernetes_version`,
  # `cluster_endpoint_public_access` → `endpoint_public_access`,
  # `cluster_addons` → `addons`. `bootstrap_self_managed_addons` is now
  # ignored by the module (hardcoded), so it's removed.
  name               = var.cluster_name
  kubernetes_version = var.cluster_version

  vpc_id     = var.vpc_id
  subnet_ids = var.private_subnet_ids

  endpoint_public_access = true

  # Automatically grant cluster admin to the IAM principal that creates
  # the cluster (i.e. whoever runs terraform apply). This ensures the
  # Helm and kubectl providers can authenticate during the same apply.
  # Set to false on clusters where an access entry for the apply principal
  # already exists outside Terraform.
  enable_cluster_creator_admin_permissions = var.enable_cluster_creator_admin_permissions

  # Operator-supplied cluster-admin access entries. Codifies the manual
  # `aws eks create-access-entry` + `associate-access-policy` step that
  # unblocked greenfield builds when enable_cluster_creator_admin_permissions
  # alone left the apply principal without an entry. One entry per ARN,
  # each with a cluster-scoped AmazonEKSClusterAdminPolicy association.
  access_entries = {
    for arn in var.cluster_admin_principal_arns : arn => {
      principal_arn = arn
      policy_associations = {
        admin = {
          policy_arn   = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
          access_scope = { type = "cluster" }
        }
      }
    }
  }

  addons = {
    # vpc-cni and kube-proxy are DaemonSets — they register immediately and
    # run once nodes exist. Safe to wait on these.
    kube-proxy = {
      most_recent = true
    }
    vpc-cni = {
      most_recent = true
    }
    eks-pod-identity-agent = {
      most_recent = true
    }
    # CoreDNS and EBS CSI need nodes to schedule pods. We set
    # resolve_conflicts but move them to a separate resource so the node
    # group doesn't have to wait for them.
  }

  # Node groups are created separately (below) so they wait for addons.
  eks_managed_node_groups = {}

  node_security_group_tags = {
    "karpenter.sh/discovery" = var.cluster_name
  }

  tags = var.tags
}

# ---------- Managed Node Group (depends on addons) ----------
# Created separately so it waits for vpc-cni and other addons to be active.
# Without this, nodes launch with no pod networking and fail health checks.
module "system_node_group" {
  source  = "terraform-aws-modules/eks/aws//modules/eks-managed-node-group"
  version = "~> 21.0"

  # v21: `cluster_version` → `kubernetes_version`.
  name               = "system"
  cluster_name       = module.eks.cluster_name
  kubernetes_version = var.cluster_version
  subnet_ids         = var.private_subnet_ids

  cluster_primary_security_group_id = module.eks.cluster_primary_security_group_id
  vpc_security_group_ids            = [module.eks.node_security_group_id]
  cluster_service_cidr              = module.eks.cluster_service_cidr

  # v21 default is AL2023_x86_64_STANDARD; set explicitly for clarity.
  ami_type       = "AL2023_x86_64_STANDARD"
  instance_types = ["m5.large"]
  min_size       = 2
  max_size       = 3
  desired_size   = 2

  labels = {
    "accelbench/node-type" = "system"
  }

  # Pin the launch-template name_prefix so in-place state migrations from
  # the bundled eks_managed_node_groups block (which used "<key>-", i.e.
  # "system-") don't trigger a destroy/replace of the live LT. New installs
  # are unaffected — a fresh LT is created either way.
  launch_template_name = "system"

  # Wait for DaemonSet addons (especially vpc-cni) before launching nodes
  depends_on = [module.eks.cluster_addons]

  tags = var.tags
}

# ---------- Addons that need nodes (Deployments) ----------
# These are created AFTER the node group so pods can be scheduled.
resource "aws_eks_addon" "coredns" {
  cluster_name                = module.eks.cluster_name
  addon_name                  = "coredns"
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"

  depends_on = [module.system_node_group]
}

resource "aws_eks_addon" "ebs_csi_driver" {
  cluster_name                = module.eks.cluster_name
  addon_name                  = "aws-ebs-csi-driver"
  service_account_role_arn    = module.ebs_csi_irsa.arn
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"

  depends_on = [module.system_node_group]
}

module "ebs_csi_irsa" {
  # v6 renamed this submodule (dropped the `-eks` suffix) and `role_name`
  # → `name`. `oidc_providers` + `attach_ebs_csi_policy` are unchanged.
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts"
  version = "~> 6.0"

  name                  = "${var.cluster_name}-ebs-csi"
  attach_ebs_csi_policy = true

  oidc_providers = {
    main = {
      provider_arn               = module.eks.oidc_provider_arn
      namespace_service_accounts = ["kube-system:ebs-csi-controller-sa"]
    }
  }

  tags = var.tags
}
