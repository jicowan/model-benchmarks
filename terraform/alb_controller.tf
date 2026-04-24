# PRD-43a: AWS Load Balancer Controller
#
# Watches for Ingress resources with ingressClassName=alb and provisions AWS
# ALBs accordingly. Installed via Helm with Pod Identity (matching the
# existing `api_pod`/`loadgen`/`cache-job` pattern in main.tf).
#
# All resources gated on var.install_alb_controller. Operators whose clusters
# already have the controller installed via another mechanism should set
# install_alb_controller=false.
#
# Chart + controller app version pinned to v3.2.2. IAM policy JSON is fetched
# from the matching git tag so the policy stays in sync with the controller.
# Upgrading: bump both version strings together and re-apply.
#
# Reference: https://kubernetes-sigs.github.io/aws-load-balancer-controller/latest/deploy/installation/

locals {
  alb_controller_version = "3.2.2"
  alb_controller_enabled = var.install_alb_controller
}

data "http" "alb_controller_policy" {
  count = local.alb_controller_enabled ? 1 : 0
  url   = "https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/v${local.alb_controller_version}/docs/install/iam_policy.json"

  request_headers = {
    Accept = "application/json"
  }

  # Fail loudly if the upstream policy disappears or returns a non-200.
  lifecycle {
    postcondition {
      condition     = self.status_code == 200
      error_message = "Failed to fetch ALB controller IAM policy from upstream (HTTP ${self.status_code})."
    }
  }
}

resource "aws_iam_role" "alb_controller" {
  count = local.alb_controller_enabled ? 1 : 0
  name  = "${var.project_name}-alb-controller"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "pods.eks.amazonaws.com"
      }
      Action = ["sts:AssumeRole", "sts:TagSession"]
    }]
  })

  tags = local.tags
}

resource "aws_iam_role_policy" "alb_controller" {
  count  = local.alb_controller_enabled ? 1 : 0
  name   = "LoadBalancerControllerPolicy"
  role   = aws_iam_role.alb_controller[0].id
  policy = data.http.alb_controller_policy[0].response_body
}

# Service account managed by Terraform (rather than the chart) so Pod Identity
# binds to a resource we control declaratively.
resource "kubernetes_service_account" "alb_controller" {
  count = local.alb_controller_enabled ? 1 : 0
  metadata {
    name      = "aws-load-balancer-controller"
    namespace = "kube-system"
    labels = {
      "app.kubernetes.io/name"       = "aws-load-balancer-controller"
      "app.kubernetes.io/component"  = "controller"
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }

  depends_on = [module.eks]
}

resource "aws_eks_pod_identity_association" "alb_controller" {
  count           = local.alb_controller_enabled ? 1 : 0
  cluster_name    = module.eks.cluster_name
  namespace       = "kube-system"
  service_account = "aws-load-balancer-controller"
  role_arn        = aws_iam_role.alb_controller[0].arn

  tags = local.tags
}

resource "helm_release" "alb_controller" {
  count      = local.alb_controller_enabled ? 1 : 0
  name       = "aws-load-balancer-controller"
  repository = "https://aws.github.io/eks-charts"
  chart      = "aws-load-balancer-controller"
  version    = local.alb_controller_version
  namespace  = "kube-system"

  set {
    name  = "clusterName"
    value = module.eks.cluster_name
  }
  set {
    name  = "region"
    value = var.region
  }
  set {
    name  = "vpcId"
    value = module.vpc.vpc_id
  }
  set {
    name  = "serviceAccount.create"
    value = "false"
  }
  set {
    name  = "serviceAccount.name"
    value = "aws-load-balancer-controller"
  }

  # Controller needs permissions attached before it starts reconciling.
  depends_on = [
    aws_eks_pod_identity_association.alb_controller,
    kubernetes_service_account.alb_controller,
  ]
}
