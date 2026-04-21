# ============================================================================
# Cognito + ALB Auth (optional)
# ============================================================================
# Enabled when var.auth_enabled = true and the domain/cert/zone vars are set.
# Creates a Cognito User Pool + app client + hosted-UI domain, and deploys
# external-dns so the ALB Ingress can register its A-alias record with Route53.
# ============================================================================

module "auth" {
  count  = var.auth_enabled ? 1 : 0
  source = "./modules/auth"

  project_name            = var.project_name
  domain_name             = var.domain_name
  cognito_domain_prefix   = var.cognito_domain_prefix
  mfa_configuration       = var.mfa_configuration
  session_timeout_seconds = var.session_timeout_seconds
  allowed_email_domains   = var.allowed_email_domains
  cluster_iam_role_name   = module.eks.cluster_iam_role_name

  tags = local.tags
}

module "external_dns" {
  count  = var.auth_enabled ? 1 : 0
  source = "./modules/external-dns"

  project_name   = var.project_name
  cluster_name   = module.eks.cluster_name
  hosted_zone_id = var.hosted_zone_id
  domain_filter  = var.domain_name

  tags = local.tags
}
