data "aws_availability_zones" "available" {
  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, 3)
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 6.0"

  name = var.name
  cidr = var.cidr

  azs             = local.azs
  private_subnets = [for k, v in local.azs : cidrsubnet(var.cidr, 4, k)]
  public_subnets  = [for k, v in local.azs : cidrsubnet(var.cidr, 8, k + 48)]
  intra_subnets   = [for k, v in local.azs : cidrsubnet(var.cidr, 8, k + 52)]

  enable_nat_gateway = true
  single_nat_gateway = var.single_nat_gateway

  # The default route table has no functional use (the private/public/intra
  # RTs carry the real routes), and aws_default_route_table has a known
  # import bug in the AWS provider for RTs with only a local route.
  manage_default_route_table = false

  public_subnet_tags = {
    "kubernetes.io/role/elb" = 1
  }

  private_subnet_tags = {
    "kubernetes.io/role/internal-elb" = 1
    "karpenter.sh/discovery"          = var.cluster_name
  }

  tags = var.tags
}
