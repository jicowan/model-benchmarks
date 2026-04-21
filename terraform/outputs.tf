output "vpc_id" {
  description = "The ID of the VPC"
  value       = module.vpc.vpc_id
}

output "eks_cluster_name" {
  description = "The name of the EKS cluster"
  value       = module.eks.cluster_name
}

output "eks_cluster_endpoint" {
  description = "The endpoint for the EKS cluster API server"
  value       = module.eks.cluster_endpoint
}

output "aurora_cluster_endpoint" {
  description = "The Aurora cluster writer endpoint"
  value       = module.aurora.cluster_endpoint
}

output "aurora_cluster_port" {
  description = "The Aurora cluster port"
  value       = module.aurora.cluster_port
}

output "aurora_master_user_secret" {
  description = "The Secrets Manager secret containing Aurora master credentials"
  value       = module.aurora.cluster_master_user_secret
}

output "ecr_api_url" {
  description = "ECR repository URL for the API image"
  value       = aws_ecr_repository.api.repository_url
}

output "ecr_web_url" {
  description = "ECR repository URL for the web image"
  value       = aws_ecr_repository.web.repository_url
}

output "ecr_migration_url" {
  description = "ECR repository URL for the migration image"
  value       = aws_ecr_repository.migration.repository_url
}

output "ecr_loadgen_url" {
  description = "ECR repository URL for the loadgen image"
  value       = aws_ecr_repository.loadgen.repository_url
}

output "results_s3_bucket" {
  description = "S3 bucket name for benchmark results"
  value       = aws_s3_bucket.results.id
}

output "update_kubeconfig_command" {
  description = "AWS CLI command to update kubeconfig for the EKS cluster"
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${module.eks.cluster_name}"
}

output "models_s3_bucket" {
  description = "S3 bucket name for model weights"
  value       = aws_s3_bucket.models.id
}

output "ecr_cache_job_url" {
  description = "ECR repository URL for the cache job image"
  value       = aws_ecr_repository.cache_job.repository_url
}

# ----------------------------------------------------------------------------
# Auth outputs (populated only when auth_enabled = true)
# ----------------------------------------------------------------------------

output "cognito_user_pool_id" {
  description = "Cognito User Pool ID (pass to aws cognito-idp admin-create-user)"
  value       = try(module.auth[0].user_pool_id, "")
}

output "cognito_user_pool_arn" {
  description = "Cognito User Pool ARN (pass to Helm as auth.userPoolArn)"
  value       = try(module.auth[0].user_pool_arn, "")
}

output "cognito_user_pool_client_id" {
  description = "Cognito app client ID (Helm: auth.userPoolClientId)"
  value       = try(module.auth[0].user_pool_client_id, "")
}

output "cognito_user_pool_domain" {
  description = "Cognito hosted-UI domain (Helm: auth.userPoolDomain)"
  value       = try(module.auth[0].user_pool_domain, "")
}

output "acm_certificate_arn" {
  description = "ACM certificate ARN (echoed from input for convenience)"
  value       = var.acm_certificate_arn
}

output "ingress_host" {
  description = "Domain name for the UI ingress (Helm: ingress.host)"
  value       = var.domain_name
}
