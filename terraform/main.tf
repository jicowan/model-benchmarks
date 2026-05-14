locals {
  # PRD-53: cluster name resolves differently in greenfield vs
  # brownfield. Greenfield derives it from project_name; brownfield
  # uses the operator-supplied existing cluster name.
  cluster_name = var.manage_cluster ? "${var.project_name}-eks" : var.cluster_name

  tags = merge(var.tags, {
    Project   = var.project_name
    ManagedBy = "terraform"
  })

  # PRD-52: when auth is disabled, force port-forward-only mode. No
  # ACM cert, no DNS, no ALB provisioning — prevents accidentally
  # exposing an auth-less control plane. If the user supplied
  # ingress_mode explicitly, Terraform silently overrides it.
  effective_ingress_mode = var.auth_enabled ? var.ingress_mode : ""

  # PRD-53: cluster attributes abstracted so downstream modules don't
  # need to know whether they came from module.eks or a data source.
  cluster_endpoint = var.manage_cluster ? module.eks[0].cluster_endpoint : data.aws_eks_cluster.existing[0].endpoint
  cluster_ca_data  = var.manage_cluster ? module.eks[0].cluster_certificate_authority_data : data.aws_eks_cluster.existing[0].certificate_authority[0].data
  oidc_issuer_url  = var.manage_cluster ? module.eks[0].cluster_oidc_issuer_url : data.aws_eks_cluster.existing[0].identity[0].oidc[0].issuer
  oidc_provider_arn = var.manage_cluster ? module.eks[0].oidc_provider_arn : data.aws_iam_openid_connect_provider.existing[0].arn
  node_security_group_id = var.manage_cluster ? module.eks[0].node_security_group_id : data.aws_eks_cluster.existing[0].vpc_config[0].cluster_security_group_id
  vpc_id          = var.manage_cluster ? module.vpc[0].vpc_id : var.vpc_id
  private_subnets = var.manage_cluster ? module.vpc[0].private_subnets : var.private_subnet_ids
}

# PRD-53: brownfield precondition. manage_cluster=false requires the
# operator to hand us the existing cluster's identity + network info.
check "brownfield_inputs" {
  assert {
    condition = var.manage_cluster || (
      var.cluster_name != "" &&
      var.vpc_id != "" &&
      length(var.private_subnet_ids) > 0
    )
    error_message = "manage_cluster = false requires cluster_name, vpc_id, and private_subnet_ids."
  }
}

# PRD-53: read existing cluster details (brownfield) so downstream
# modules can consume the same locals as greenfield. Count=0 in
# greenfield mode skips the lookup entirely.
data "aws_eks_cluster" "existing" {
  count = var.manage_cluster ? 0 : 1
  name  = var.cluster_name
}

data "aws_iam_openid_connect_provider" "existing" {
  count = var.manage_cluster ? 0 : 1
  # OIDC issuer URL → provider URL (drop https:// prefix). AWS
  # requires matching the URL format the provider was registered
  # with, which is always https://oidc.eks.<region>.amazonaws.com/id/<id>.
  url = data.aws_eks_cluster.existing[0].identity[0].oidc[0].issuer
}

# PRD-53: in brownfield mode, tag the operator-supplied private
# subnets + the cluster security group with karpenter.sh/discovery =
# <cluster_name> so our EC2NodeClasses' subnetSelectorTerms +
# securityGroupSelectorTerms resolve. In greenfield mode our VPC
# module (modules/vpc/main.tf) already applies this tag.
resource "aws_ec2_tag" "subnet_discovery" {
  for_each    = var.manage_cluster ? toset([]) : toset(var.private_subnet_ids)
  resource_id = each.key
  key         = "karpenter.sh/discovery"
  value       = local.cluster_name
}

resource "aws_ec2_tag" "cluster_sg_discovery" {
  count       = var.manage_cluster ? 0 : 1
  resource_id = local.node_security_group_id
  key         = "karpenter.sh/discovery"
  value       = local.cluster_name
}

# PRD-53: when reusing an operator-managed Karpenter controller, the
# installed version must be at least v1.9 — our NodePools use fields
# (disruption.budgets, consolidationPolicy, consolidateAfter) that
# landed in v1.0 and were polished through v1.9. The NodePool YAML
# will render but silently degrade on older versions.
data "kubernetes_resource" "karpenter_deployment" {
  count        = !var.manage_cluster && !var.install_karpenter_controller ? 1 : 0
  api_version  = "apps/v1"
  kind         = "Deployment"
  metadata {
    name      = "karpenter"
    namespace = "kube-system"
  }
}

check "karpenter_version" {
  assert {
    condition = (
      var.manage_cluster ||
      var.install_karpenter_controller ||
      length(data.kubernetes_resource.karpenter_deployment) == 0 ||
      can(regex(":(1\\.(9|[1-9][0-9])|[2-9]\\.)",
        try(data.kubernetes_resource.karpenter_deployment[0].object.spec.template.spec.containers[0].image, "")))
    )
    error_message = "AccelBench requires Karpenter >= v1.9 when reusing an existing controller (install_karpenter_controller=false). Upgrade the operator's Karpenter, or set install_karpenter_controller=true to have Terraform install a compatible version."
  }
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
    host                   = local.cluster_endpoint
    cluster_ca_certificate = base64decode(local.cluster_ca_data)

    exec {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", local.cluster_name, "--region", var.region]
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
  host                   = local.cluster_endpoint
  cluster_ca_certificate = base64decode(local.cluster_ca_data)
  load_config_file       = false

  exec {
    api_version = "client.authentication.k8s.io/v1beta1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", local.cluster_name, "--region", var.region]
  }
}

provider "kubernetes" {
  host                   = local.cluster_endpoint
  cluster_ca_certificate = base64decode(local.cluster_ca_data)

  exec {
    api_version = "client.authentication.k8s.io/v1beta1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", local.cluster_name, "--region", var.region]
  }
}

# PRD-53: state migration — several resources moved from unkeyed
# to counted (count = var.flag ? 1 : 0). `moved` blocks tell
# Terraform these addresses are the same resource, preventing a
# destroy+recreate on existing greenfield state.
moved {
  from = module.vpc
  to   = module.vpc[0]
}

moved {
  from = module.eks
  to   = module.eks[0]
}

moved {
  from = aws_secretsmanager_secret.dockerhub_credential
  to   = aws_secretsmanager_secret.dockerhub_credential[0]
}

moved {
  from = aws_secretsmanager_secret_version.dockerhub_credential
  to   = aws_secretsmanager_secret_version.dockerhub_credential[0]
}

moved {
  from = aws_ecr_pull_through_cache_rule.dockerhub
  to   = aws_ecr_pull_through_cache_rule.dockerhub[0]
}

# ---------- VPC ----------
# PRD-53: skipped in brownfield mode. Operator provides vpc_id +
# private_subnet_ids via variables.
module "vpc" {
  count  = var.manage_cluster ? 1 : 0
  source = "./modules/vpc"

  name         = "${var.project_name}-vpc"
  cidr         = var.vpc_cidr
  cluster_name = local.cluster_name

  tags = local.tags
}

# ---------- EKS ----------
# PRD-53: skipped in brownfield mode. Cluster attributes come from
# the data sources computed in local.cluster_* instead.
module "eks" {
  count  = var.manage_cluster ? 1 : 0
  source = "./modules/eks"

  cluster_name       = local.cluster_name
  cluster_version    = var.cluster_version
  vpc_id             = local.vpc_id
  private_subnet_ids = local.private_subnets

  # Our original cluster was bootstrapped with an access entry for
  # user/kubernetes created outside Terraform; enabling the module's
  # cluster-creator admin would try to create a duplicate. New installs
  # leave this at its default (true).
  enable_cluster_creator_admin_permissions = var.enable_cluster_creator_admin_permissions

  tags = local.tags
}

# ---------- Karpenter ----------
# PRD-53: install_controller splits the module so the controller
# Helm release is skipped on brownfield clusters that already run
# Karpenter, while the IAM role, NodePools, and EC2NodeClasses are
# always applied. manage_pull_through_cache gates the extra
# pull-through-cache IAM policy on the node role.
module "karpenter" {
  source = "./modules/karpenter"

  cluster_name      = local.cluster_name
  cluster_endpoint  = local.cluster_endpoint
  karpenter_version = var.karpenter_version

  install_controller           = var.install_karpenter_controller
  install_nvidia_device_plugin = var.install_nvidia_device_plugin
  manage_pull_through_cache    = var.manage_pull_through_cache
  cluster_oidc_issuer_url      = local.oidc_issuer_url

  tags = local.tags
}

# ---------- Aurora PostgreSQL ----------
module "aurora" {
  source = "./modules/aurora"

  name                       = "${var.project_name}-db"
  vpc_id                     = local.vpc_id
  private_subnet_ids         = local.private_subnets
  eks_node_security_group_id = local.node_security_group_id
  min_capacity               = var.aurora_min_capacity
  max_capacity               = var.aurora_max_capacity

  tags = local.tags
}

# ---------- Kubernetes namespace + DB secret (Helm-owned) ----------
# Creates the accelbench namespace with Helm ownership metadata AND the
# DATABASE_URL secret the API + migration jobs read at runtime. Reads the
# live Aurora password from Secrets Manager (populated by the RDS-managed
# master user secret) and URL-encodes it before building the Postgres URI.
#
# If Aurora ever rotates the password, run `terraform apply` to refresh.

data "aws_secretsmanager_secret_version" "aurora_master" {
  secret_id = module.aurora.cluster_master_user_secret[0].secret_arn
}

locals {
  aurora_creds    = jsondecode(data.aws_secretsmanager_secret_version.aurora_master.secret_string)
  aurora_password = local.aurora_creds.password
  aurora_username = local.aurora_creds.username
  # urlencode() percent-encodes every char except the RFC 3986 unreserved
  # set, which matches what we need for the password in a Postgres URI.
  database_url = format(
    "postgres://%s:%s@%s:%d/accelbench?sslmode=require",
    local.aurora_username,
    urlencode(local.aurora_password),
    module.aurora.cluster_endpoint,
    module.aurora.cluster_port,
  )
}

resource "kubernetes_namespace" "accelbench" {
  count = var.manage_accelbench_namespace ? 1 : 0

  metadata {
    name = "accelbench"
    labels = {
      "app.kubernetes.io/managed-by" = "Helm"
    }
    annotations = {
      "meta.helm.sh/release-name"      = "accelbench"
      "meta.helm.sh/release-namespace" = "accelbench"
    }
  }

  # Helm adds its own labels (app.kubernetes.io/version, helm.sh/chart)
  # and hook annotations on every install/upgrade. Terraform owns the
  # namespace's existence; Helm owns its per-release metadata.
  lifecycle {
    ignore_changes = [
      metadata[0].labels,
      metadata[0].annotations,
    ]
  }

  depends_on = [module.eks]
}

resource "kubernetes_secret" "accelbench_db" {
  count = var.manage_accelbench_namespace ? 1 : 0

  metadata {
    name      = "accelbench-db"
    namespace = kubernetes_namespace.accelbench[0].metadata[0].name
  }

  data = {
    DATABASE_URL = local.database_url
  }

  type = "Opaque"
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
  cluster_name    = local.cluster_name
  namespace       = "accelbench"
  service_account = "accelbench-api"
  role_arn        = aws_iam_role.api_pod.arn

  tags = local.tags
}

# ---------- ECR Repositories ----------
# scan_on_push enables ECR basic scanning. Findings show up under
# Amazon Inspector / ECR console and are free for basic scans.
resource "aws_ecr_repository" "api" {
  name                 = "${var.project_name}-api"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = local.tags
}

resource "aws_ecr_repository" "web" {
  name                 = "${var.project_name}-web"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = local.tags
}

resource "aws_ecr_repository" "migration" {
  name                 = "${var.project_name}-migration"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = local.tags
}

resource "aws_ecr_repository" "loadgen" {
  name                 = "${var.project_name}-loadgen"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = local.tags
}

resource "aws_ecr_repository" "tools" {
  name                 = "${var.project_name}-tools"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = local.tags
}

# ---------- S3 Bucket for Benchmark Results ----------
resource "aws_s3_bucket" "results" {
  bucket        = "${var.project_name}-results-${data.aws_caller_identity.current.account_id}"
  force_destroy = true
  tags          = local.tags
}

# Block all forms of public access. Benchmark results are internal only;
# pods read via IAM. Overrides any bucket policy or ACL that might
# otherwise grant public access.
resource "aws_s3_bucket_public_access_block" "results" {
  bucket                  = aws_s3_bucket.results.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
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
  cluster_name    = local.cluster_name
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

# Model weights stay private; streamer pods read via IAM. Public access
# is always off.
resource "aws_s3_bucket_public_access_block" "models" {
  bucket                  = aws_s3_bucket.models.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
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
  cluster_name    = local.cluster_name
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

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = local.tags
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
  cluster_name    = local.cluster_name
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
#
# PRD-33: ec2:DescribeCapacityReservations lets the Capacity Reservations
# card validate attached ODCRs/CBRs against live EC2 state (instance type,
# AZ, state, available count). Also resource="*" — no ARN scoping support.
resource "aws_iam_role_policy" "api_ecr_describe" {
  name = "DescribeReadOnly"
  role = aws_iam_role.api_pod.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "ecr:DescribeRepositories",
        "ecr:DescribeImages",
        "ec2:DescribeCapacityReservations",
      ]
      Resource = "*"
    }]
  })
}

# ---------- ECR Pull-through Cache for Docker Hub (PRD-29) ----------
# Mirrors docker.io/vllm/vllm-openai:<tag> into our private ECR on first pull,
# serving subsequent pulls from AWS. Secret ARN must be under
# ecr-pullthroughcache/* prefix per AWS requirement.
#
# PRD-53: skipped when manage_pull_through_cache=false. Operators on
# brownfield clusters that already have a pull-through rule, or who
# prefer a public-ECR vLLM image via image.vllm.repository (see
# docs/deployment.md), don't need this.
resource "aws_secretsmanager_secret" "dockerhub_credential" {
  count       = var.manage_pull_through_cache ? 1 : 0
  name        = "ecr-pullthroughcache/dockerhub"
  description = "Docker Hub credentials consumed by the ECR pull-through cache"
  tags        = local.tags
}

resource "aws_secretsmanager_secret_version" "dockerhub_credential" {
  count     = var.manage_pull_through_cache ? 1 : 0
  secret_id = aws_secretsmanager_secret.dockerhub_credential[0].id
  secret_string = jsonencode({
    username    = var.dockerhub_username
    accessToken = var.dockerhub_access_token
  })
}

resource "aws_ecr_pull_through_cache_rule" "dockerhub" {
  count                 = var.manage_pull_through_cache ? 1 : 0
  ecr_repository_prefix = "dockerhub"
  upstream_registry_url = "registry-1.docker.io"
  credential_arn        = aws_secretsmanager_secret.dockerhub_credential[0].arn
}
