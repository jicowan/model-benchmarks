module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.0"

  cluster_name    = var.cluster_name
  cluster_version = var.cluster_version

  vpc_id     = var.vpc_id
  subnet_ids = var.private_subnet_ids

  cluster_endpoint_public_access = true

  # Match how the live cluster was originally bootstrapped. Setting this is
  # a create-time-only attribute; without this line the module defaults to
  # null (AWS interprets as true) and forces replacement of the cluster.
  bootstrap_self_managed_addons = false

  # Automatically grant cluster admin to the IAM principal that creates
  # the cluster (i.e. whoever runs terraform apply). This ensures the
  # Helm and kubectl providers can authenticate during the same apply.
  enable_cluster_creator_admin_permissions = true

  cluster_addons = {
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
  version = "~> 20.0"

  name            = "system"
  cluster_name    = module.eks.cluster_name
  cluster_version = var.cluster_version
  subnet_ids      = var.private_subnet_ids

  cluster_primary_security_group_id = module.eks.cluster_primary_security_group_id
  vpc_security_group_ids            = [module.eks.node_security_group_id]
  cluster_service_cidr              = module.eks.cluster_service_cidr

  instance_types = ["m5.large"]
  min_size       = 2
  max_size       = 3
  desired_size   = 2

  labels = {
    "accelbench/node-type" = "system"
  }

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
  service_account_role_arn    = module.ebs_csi_irsa.iam_role_arn
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"

  depends_on = [module.system_node_group]
}

module "ebs_csi_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.0"

  role_name             = "${var.cluster_name}-ebs-csi"
  attach_ebs_csi_policy = true

  oidc_providers = {
    main = {
      provider_arn               = module.eks.oidc_provider_arn
      namespace_service_accounts = ["kube-system:ebs-csi-controller-sa"]
    }
  }

  tags = var.tags
}
