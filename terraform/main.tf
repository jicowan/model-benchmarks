locals {
  cluster_name = "${var.project_name}-eks"

  tags = merge(var.tags, {
    Project   = var.project_name
    ManagedBy = "terraform"
  })
}

provider "aws" {
  region = var.region
}

provider "helm" {
  kubernetes {
    host                   = module.eks.cluster_endpoint
    cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)

    exec {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", local.cluster_name]
    }
  }
}

provider "kubectl" {
  apply_retry_count      = 5
  host                   = module.eks.cluster_endpoint
  cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)
  load_config_file       = false

  exec {
    api_version = "client.authentication.k8s.io/v1beta1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", local.cluster_name]
  }
}

# ---------- VPC ----------
module "vpc" {
  source = "./modules/vpc"

  name         = "${var.project_name}-vpc"
  cidr         = var.vpc_cidr
  cluster_name = local.cluster_name

  tags = local.tags
}

# ---------- EKS ----------
module "eks" {
  source = "./modules/eks"

  cluster_name       = local.cluster_name
  cluster_version    = var.cluster_version
  vpc_id             = module.vpc.vpc_id
  private_subnet_ids = module.vpc.private_subnets

  tags = local.tags
}

# ---------- Karpenter ----------
module "karpenter" {
  source = "./modules/karpenter"

  cluster_name      = module.eks.cluster_name
  cluster_endpoint  = module.eks.cluster_endpoint
  karpenter_version = var.karpenter_version

  tags = local.tags
}

# ---------- Aurora PostgreSQL ----------
module "aurora" {
  source = "./modules/aurora"

  name                       = "${var.project_name}-db"
  vpc_id                     = module.vpc.vpc_id
  private_subnet_ids         = module.vpc.private_subnets
  eks_node_security_group_id = module.eks.node_security_group_id
  min_capacity               = var.aurora_min_capacity
  max_capacity               = var.aurora_max_capacity

  tags = local.tags
}

# ---------- API Pod Identity (pricing:GetProducts for CronJob) ----------
resource "aws_iam_role" "api_pod" {
  name = "${var.project_name}-api"

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

resource "aws_iam_role_policy" "api_pricing" {
  name = "PricingReadOnly"
  role = aws_iam_role.api_pod.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "pricing:GetProducts"
      Resource = "*"
    }]
  })
}

resource "aws_eks_pod_identity_association" "api" {
  cluster_name    = module.eks.cluster_name
  namespace       = "accelbench"
  service_account = "accelbench-api"
  role_arn        = aws_iam_role.api_pod.arn

  tags = local.tags
}

# ---------- ECR Repositories ----------
resource "aws_ecr_repository" "api" {
  name                 = "${var.project_name}-api"
  image_tag_mutability = "MUTABLE"
  force_delete         = true
  tags                 = local.tags
}

resource "aws_ecr_repository" "web" {
  name                 = "${var.project_name}-web"
  image_tag_mutability = "MUTABLE"
  force_delete         = true
  tags                 = local.tags
}

resource "aws_ecr_repository" "migration" {
  name                 = "${var.project_name}-migration"
  image_tag_mutability = "MUTABLE"
  force_delete         = true
  tags                 = local.tags
}

resource "aws_ecr_repository" "loadgen" {
  name                 = "${var.project_name}-loadgen"
  image_tag_mutability = "MUTABLE"
  force_delete         = true
  tags                 = local.tags
}
