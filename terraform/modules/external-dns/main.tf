# IAM role for external-dns, assumed via EKS Pod Identity.
# Scoped to a single hosted zone so it cannot touch unrelated Route53 records.
resource "aws_iam_role" "this" {
  name = "${var.project_name}-external-dns"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "pods.eks.amazonaws.com" }
      Action    = ["sts:AssumeRole", "sts:TagSession"]
    }]
  })

  tags = var.tags
}

resource "aws_iam_role_policy" "this" {
  name = "Route53Write"
  role = aws_iam_role.this.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        # ChangeResourceRecordSets must be scoped to hostedzone/* (not a
        # specific zone ARN) — external-dns enumerates all zones at startup
        # and only filters down to ZoneIDFilter after the list call.
        Effect = "Allow"
        Action = [
          "route53:ChangeResourceRecordSets",
          "route53:ListResourceRecordSets",
        ]
        Resource = "arn:aws:route53:::hostedzone/*"
      },
      {
        Effect = "Allow"
        Action = [
          "route53:ListHostedZones",
          "route53:ListHostedZonesByName",
          "route53:ListTagsForResource",
          "route53:GetChange",
        ]
        Resource = "*"
      },
    ]
  })
}

resource "aws_eks_pod_identity_association" "this" {
  cluster_name    = var.cluster_name
  namespace       = var.namespace
  service_account = "external-dns"
  role_arn        = aws_iam_role.this.arn

  tags = var.tags
}

resource "helm_release" "this" {
  name       = "external-dns"
  repository = "https://kubernetes-sigs.github.io/external-dns"
  chart      = "external-dns"
  version    = var.chart_version
  namespace  = var.namespace

  values = [yamlencode({
    provider      = "aws"
    policy        = "upsert-only" # never delete records we didn't create
    txtOwnerId    = var.cluster_name
    domainFilters = [var.domain_filter]
    sources       = ["ingress"]
    # Poll Route53 every 30s (default is 1m). Combined with --events below
    # this makes records propagate within seconds of an ingress change.
    interval = "30s"
    logLevel = "info"
    serviceAccount = {
      create = true
      name   = "external-dns"
    }
    aws = {
      zoneType = "public"
      region   = data.aws_region.current.name
    }
    extraArgs = [
      "--aws-zone-type=public",
      "--zone-id-filter=${var.hosted_zone_id}",
      # Trigger reconcile immediately on every ingress event instead of
      # waiting for the next poll interval.
      "--events",
    ]
  })]

  depends_on = [aws_eks_pod_identity_association.this]
}

data "aws_region" "current" {}
