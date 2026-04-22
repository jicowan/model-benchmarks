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

provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"
}

data "aws_ecrpublic_authorization_token" "token" {
  provider = aws.us_east_1
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

  registry {
    url      = "oci://public.ecr.aws"
    username = data.aws_ecrpublic_authorization_token.token.user_name
    password = data.aws_ecrpublic_authorization_token.token.password
  }
}

provider "kubectl" {
  apply_retry_count      = 15
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

resource "aws_ecr_repository" "tools" {
  name                 = "${var.project_name}-tools"
  image_tag_mutability = "MUTABLE"
  force_delete         = true
  tags                 = local.tags
}

# ---------- S3 Bucket for Benchmark Results ----------
resource "aws_s3_bucket" "results" {
  bucket        = "${var.project_name}-results-${data.aws_caller_identity.current.account_id}"
  force_destroy = true
  tags          = local.tags
}

resource "aws_s3_bucket_lifecycle_configuration" "results" {
  bucket = aws_s3_bucket.results.id

  rule {
    id     = "expire-old-results"
    status = "Enabled"

    filter {
      prefix = "results/"
    }

    expiration {
      days = 30
    }
  }
}

data "aws_caller_identity" "current" {}

# Add S3 read access to API pod role
resource "aws_iam_role_policy" "api_s3_read" {
  name = "S3ResultsRead"
  role = aws_iam_role.api_pod.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "s3:GetObject",
        "s3:ListBucket"
      ]
      Resource = [
        aws_s3_bucket.results.arn,
        "${aws_s3_bucket.results.arn}/*"
      ]
    }]
  })
}

# ---------- Loadgen Pod Identity (S3 write for results) ----------
resource "aws_iam_role" "loadgen_pod" {
  name = "${var.project_name}-loadgen"

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

resource "aws_iam_role_policy" "loadgen_s3_write" {
  name = "S3ResultsWrite"
  role = aws_iam_role.loadgen_pod.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "s3:PutObject"
      ]
      Resource = "${aws_s3_bucket.results.arn}/*"
    }]
  })
}

resource "aws_eks_pod_identity_association" "loadgen" {
  cluster_name    = module.eks.cluster_name
  namespace       = "accelbench"
  service_account = "accelbench-loadgen"
  role_arn        = aws_iam_role.loadgen_pod.arn

  tags = local.tags
}

# ---------- S3 Bucket for Model Weights ----------
resource "aws_s3_bucket" "models" {
  bucket        = "${var.project_name}-models-${data.aws_caller_identity.current.account_id}"
  force_destroy = false
  tags          = local.tags
}

# ---------- Model Pod Identity (S3 read for vLLM Run:ai Streamer) ----------
resource "aws_iam_role" "model_pod" {
  name = "${var.project_name}-model"

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

resource "aws_iam_role_policy" "model_s3_read" {
  name = "S3ModelsRead"
  role = aws_iam_role.model_pod.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "s3:GetObject",
        "s3:ListBucket"
      ]
      Resource = [
        aws_s3_bucket.models.arn,
        "${aws_s3_bucket.models.arn}/*"
      ]
    }]
  })
}

resource "aws_eks_pod_identity_association" "model" {
  cluster_name    = module.eks.cluster_name
  namespace       = "accelbench"
  service_account = "accelbench-model"
  role_arn        = aws_iam_role.model_pod.arn

  tags = local.tags
}

# ---------- ECR Repository for cache-job image ----------
resource "aws_ecr_repository" "cache_job" {
  name                 = "${var.project_name}-cache-job"
  image_tag_mutability = "MUTABLE"
  force_delete         = true
  tags                 = local.tags
}

# ---------- Cache Job Pod Identity (S3 read+write for HF-to-S3 caching) ----------
resource "aws_iam_role" "cache_job_pod" {
  name = "${var.project_name}-cache-job"

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

resource "aws_iam_role_policy" "cache_job_s3" {
  name = "S3ModelsReadWrite"
  role = aws_iam_role.cache_job_pod.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:ListBucket"
      ]
      Resource = [
        aws_s3_bucket.models.arn,
        "${aws_s3_bucket.models.arn}/*"
      ]
    }]
  })
}

resource "aws_eks_pod_identity_association" "cache_job" {
  cluster_name    = module.eks.cluster_name
  namespace       = "accelbench"
  service_account = "accelbench-cache-job"
  role_arn        = aws_iam_role.cache_job_pod.arn

  tags = local.tags
}

# Add S3 read access on models bucket to API pod role
resource "aws_iam_role_policy" "api_models_s3_read" {
  name = "S3ModelsRead"
  role = aws_iam_role.api_pod.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "s3:GetObject",
        "s3:ListBucket"
      ]
      Resource = [
        aws_s3_bucket.models.arn,
        "${aws_s3_bucket.models.arn}/*"
      ]
    }]
  })
}

# PRD-31: the API pod manages the HuggingFace + Docker Hub platform tokens
# via AWS Secrets Manager. Scoped to just the two prefixes — the pod cannot
# reach RDS, Karpenter, or any other Secrets Manager entries in the account.
resource "aws_iam_role_policy" "api_config_secrets" {
  name = "ConfigSecretsManager"
  role = aws_iam_role.api_pod.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "secretsmanager:GetSecretValue",
        "secretsmanager:PutSecretValue",
        "secretsmanager:CreateSecret",
        "secretsmanager:DescribeSecret",
        "secretsmanager:DeleteSecret",
      ]
      Resource = [
        "arn:aws:secretsmanager:${var.region}:${data.aws_caller_identity.current.account_id}:secret:accelbench/config/*",
        "arn:aws:secretsmanager:${var.region}:${data.aws_caller_identity.current.account_id}:secret:ecr-pullthroughcache/*",
      ]
    }]
  })
}

# PRD-32: Registry card on the Configuration page lists cached repos in the
# pull-through cache and their size + last-pulled timestamps. Describe-only,
# no mutation, resource="*" because ECR DescribeRepositories/DescribeImages
# don't support prefix-scoped resource ARNs.
resource "aws_iam_role_policy" "api_ecr_describe" {
  name = "ECRDescribe"
  role = aws_iam_role.api_pod.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "ecr:DescribeRepositories",
        "ecr:DescribeImages",
      ]
      Resource = "*"
    }]
  })
}

# ---------- ECR Pull-through Cache for Docker Hub (PRD-29) ----------
# Mirrors docker.io/vllm/vllm-openai:<tag> into our private ECR on first pull,
# serving subsequent pulls from AWS. Secret ARN must be under
# ecr-pullthroughcache/* prefix per AWS requirement.
resource "aws_secretsmanager_secret" "dockerhub_credential" {
  name        = "ecr-pullthroughcache/dockerhub"
  description = "Docker Hub credentials consumed by the ECR pull-through cache"
  tags        = local.tags
}

resource "aws_secretsmanager_secret_version" "dockerhub_credential" {
  secret_id = aws_secretsmanager_secret.dockerhub_credential.id
  secret_string = jsonencode({
    username    = var.dockerhub_username
    accessToken = var.dockerhub_access_token
  })
}

resource "aws_ecr_pull_through_cache_rule" "dockerhub" {
  ecr_repository_prefix = "dockerhub"
  upstream_registry_url = "registry-1.docker.io"
  credential_arn        = aws_secretsmanager_secret.dockerhub_credential.arn
}
