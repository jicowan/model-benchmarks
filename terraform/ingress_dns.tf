# PRD-43a: Public A-record pointing app_host at the ALB.
#
# Only active when var.ingress_mode == "acm-route53". The ALB is provisioned
# by the load balancer controller when the Ingress resource (in Helm) is
# reconciled — this file runs on a *second* terraform apply, once the
# Ingress has been deployed and the ALB exists.
#
# The ALB controller tags each ALB it creates with
#   ingress.k8s.aws/stack = "<namespace>/<ingress-name>"
# so we look it up by that tag here.
#
# First-install ordering:
#   1. terraform apply              # ALB controller + ACM cert
#   2. helm upgrade ...              # creates Ingress; controller provisions ALB
#   3. terraform apply              # picks up ALB, writes this A record
#
# Subsequent changes are single-step.

locals {
  manage_dns_record = local.manage_cert && var.ingress_deployed
}

data "aws_lb" "ingress" {
  count = local.manage_dns_record ? 1 : 0
  tags = {
    "ingress.k8s.aws/stack" = "accelbench/accelbench"
  }
}

resource "aws_route53_record" "app" {
  count   = local.manage_dns_record ? 1 : 0
  zone_id = data.aws_route53_zone.app[0].zone_id
  name    = var.app_host
  type    = "A"

  alias {
    name                   = data.aws_lb.ingress[0].dns_name
    zone_id                = data.aws_lb.ingress[0].zone_id
    evaluate_target_health = true
  }
}
