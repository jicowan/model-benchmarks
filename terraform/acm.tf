# PRD-43a: ACM certificate for the public ingress.
#
# Only active when var.ingress_mode == "acm-route53". In that mode Terraform
# requests an ACM cert for var.app_host and validates it via DNS records
# written to the Route 53 hosted zone named in var.hosted_zone_name (which
# must live in this same AWS account).
#
# For acm-existing or none modes, this file creates nothing — the operator
# supplies a pre-issued cert ARN (or none at all) and this zone lookup is
# skipped.

locals {
  manage_cert = var.ingress_mode == "acm-route53"
}

data "aws_route53_zone" "app" {
  count        = local.manage_cert ? 1 : 0
  name         = var.hosted_zone_name
  private_zone = false
}

resource "aws_acm_certificate" "app" {
  count             = local.manage_cert ? 1 : 0
  domain_name       = var.app_host
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }

  tags = local.tags
}

resource "aws_route53_record" "cert_validation" {
  for_each = local.manage_cert ? {
    for dvo in aws_acm_certificate.app[0].domain_validation_options :
    dvo.domain_name => {
      name  = dvo.resource_record_name
      type  = dvo.resource_record_type
      value = dvo.resource_record_value
    }
  } : {}

  zone_id         = data.aws_route53_zone.app[0].zone_id
  name            = each.value.name
  type            = each.value.type
  records         = [each.value.value]
  ttl             = 60
  allow_overwrite = true
}

resource "aws_acm_certificate_validation" "app" {
  count                   = local.manage_cert ? 1 : 0
  certificate_arn         = aws_acm_certificate.app[0].arn
  validation_record_fqdns = [for r in aws_route53_record.cert_validation : r.fqdn]

  timeouts {
    create = "10m"
  }
}
