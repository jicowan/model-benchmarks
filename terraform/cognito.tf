# PRD-43: Cognito User Pool for AccelBench authentication.
#
# Custom-branded React login page (served by accelbench-web) POSTs to
# /api/v1/auth/login. The backend calls InitiateAuth with USER_PASSWORD_AUTH
# and sets ID/access/refresh tokens as HttpOnly cookies. JWT verification
# middleware on all /api/v1/* routes.
#
# No Hosted UI, no OAuth domain — the app only talks to Cognito via
# server-to-server admin APIs.

resource "aws_cognito_user_pool" "accelbench" {
  name = "${var.project_name}-users"

  username_attributes      = ["email"]
  auto_verified_attributes = ["email"]

  password_policy {
    minimum_length    = 12
    require_lowercase = true
    require_numbers   = true
    require_symbols   = false
    require_uppercase = true
  }

  schema {
    name                     = "email"
    attribute_data_type      = "String"
    required                 = true
    mutable                  = true
    developer_only_attribute = false
    string_attribute_constraints {
      min_length = 1
      max_length = 256
    }
  }

  # PRD-44 enforces this; PRD-43 just declares it. Values: "admin" | "user".
  schema {
    name                     = "role"
    attribute_data_type      = "String"
    required                 = false
    mutable                  = true
    developer_only_attribute = false
    string_attribute_constraints {
      min_length = 1
      max_length = 32
    }
  }

  # Users cannot self-serve password recovery; admins reset via console
  # (or the PRD-45 user-management UI once that ships).
  account_recovery_setting {
    recovery_mechanism {
      name     = "admin_only"
      priority = 1
    }
  }

  tags = local.tags
}

resource "aws_cognito_user_pool_client" "accelbench_api" {
  name         = "${var.project_name}-api"
  user_pool_id = aws_cognito_user_pool.accelbench.id

  generate_secret = false

  explicit_auth_flows = [
    "ALLOW_USER_PASSWORD_AUTH",
    "ALLOW_REFRESH_TOKEN_AUTH",
  ]

  access_token_validity  = 60 # minutes
  id_token_validity      = 60 # minutes
  refresh_token_validity = 30 # days
  token_validity_units {
    access_token  = "minutes"
    id_token      = "minutes"
    refresh_token = "days"
  }

  # Do not leak whether an email is registered.
  prevent_user_existence_errors = "ENABLED"
}

# IAM: let the accelbench-api pod (already bound to aws_iam_role.api_pod
# via EKS Pod Identity) call the two Cognito actions we need.
resource "aws_iam_role_policy" "api_cognito" {
  name = "CognitoAuth"
  role = aws_iam_role.api_pod.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "cognito-idp:InitiateAuth",
        "cognito-idp:GlobalSignOut",
      ]
      Resource = aws_cognito_user_pool.accelbench.arn
    }]
  })
}

# PRD-44: bootstrap admin user. Gated on var.admin_email — leave the
# variable unset to skip (e.g., on clusters where the admin was created
# manually via the AWS console). Cognito emails a temporary password to
# the address; the user must set a permanent one on first login.
#
# `attributes` is NOT marked lifecycle-ignored because Cognito mutates
# email_verified and a couple of password-state sub-attributes on its
# own; ignoring them keeps `terraform plan` quiet after a login.
resource "aws_cognito_user" "bootstrap_admin" {
  count        = var.admin_email == "" ? 0 : 1
  user_pool_id = aws_cognito_user_pool.accelbench.id
  username     = var.admin_email

  attributes = {
    email          = var.admin_email
    email_verified = "true"
    "custom:role"  = "admin"
  }

  desired_delivery_mediums = ["EMAIL"]

  lifecycle {
    ignore_changes = [
      attributes["email_verified"],
    ]
  }
}
