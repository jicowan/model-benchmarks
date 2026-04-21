# Cognito User Pool — authoritative identity store.
# Users are admin-created via the AWS CLI (see README). Passwords never live in
# Terraform state. When allowed_email_domains is non-empty, self-signup is
# opened up but restricted to those domains by a pre-signup Lambda trigger.
resource "aws_cognito_user_pool" "this" {
  name              = "${var.project_name}-users"
  mfa_configuration = var.mfa_configuration

  username_attributes      = ["email"]
  auto_verified_attributes = ["email"]

  password_policy {
    minimum_length                   = 12
    require_lowercase                = true
    require_uppercase                = true
    require_numbers                  = true
    require_symbols                  = true
    temporary_password_validity_days = 7
  }

  admin_create_user_config {
    # When an allowed_email_domains list is provided, self-signup is enabled
    # (the pre-signup Lambda trigger enforces the domain policy). Otherwise
    # only admins can create users via the CLI.
    allow_admin_create_user_only = length(var.allowed_email_domains) == 0
  }

  account_recovery_setting {
    recovery_mechanism {
      name     = "verified_email"
      priority = 1
    }
  }

  dynamic "software_token_mfa_configuration" {
    for_each = var.mfa_configuration == "OFF" ? [] : [1]
    content {
      enabled = true
    }
  }

  dynamic "lambda_config" {
    for_each = length(var.allowed_email_domains) > 0 ? [1] : []
    content {
      pre_sign_up = aws_lambda_function.pre_signup[0].arn
    }
  }

  tags = var.tags
}

resource "aws_cognito_user_pool_domain" "this" {
  domain       = var.cognito_domain_prefix
  user_pool_id = aws_cognito_user_pool.this.id
}

resource "aws_cognito_user_pool_client" "alb" {
  name         = "${var.project_name}-alb"
  user_pool_id = aws_cognito_user_pool.this.id

  generate_secret                      = true
  allowed_oauth_flows_user_pool_client = true
  allowed_oauth_flows                  = ["code"]
  allowed_oauth_scopes                 = ["openid", "email", "profile"]
  supported_identity_providers         = ["COGNITO"]

  callback_urls = ["https://${var.domain_name}/oauth2/idpresponse"]
  logout_urls   = ["https://${var.domain_name}"]

  explicit_auth_flows = [
    "ALLOW_USER_PASSWORD_AUTH",
    "ALLOW_REFRESH_TOKEN_AUTH",
  ]

  prevent_user_existence_errors = "ENABLED"
  enable_token_revocation       = true

  access_token_validity  = 60
  id_token_validity      = 60
  refresh_token_validity = 30

  token_validity_units {
    access_token  = "minutes"
    id_token      = "minutes"
    refresh_token = "days"
  }
}
