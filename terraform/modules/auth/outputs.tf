output "user_pool_id" {
  description = "Cognito User Pool ID (used by admin-create-user CLI)"
  value       = aws_cognito_user_pool.this.id
}

output "user_pool_arn" {
  description = "Cognito User Pool ARN (Helm: auth.userPoolArn)"
  value       = aws_cognito_user_pool.this.arn
}

output "user_pool_client_id" {
  description = "Cognito app client ID (Helm: auth.userPoolClientId)"
  value       = aws_cognito_user_pool_client.alb.id
}

output "user_pool_domain" {
  description = "Cognito hosted-UI domain (Helm: auth.userPoolDomain)"
  value       = "${aws_cognito_user_pool_domain.this.domain}.auth.${data.aws_region.current.name}.amazoncognito.com"
}

data "aws_region" "current" {}
