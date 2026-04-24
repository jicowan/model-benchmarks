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

# ---------- Public ingress (PRD-43a) ----------

output "certificate_arn" {
  description = "ACM certificate ARN for the app host. Empty unless ingress_mode = acm-route53."
  value       = length(aws_acm_certificate.app) > 0 ? aws_acm_certificate.app[0].arn : ""
}

output "app_host" {
  description = "Public hostname for the app. Empty unless ingress_mode is set."
  value       = var.app_host
}

output "app_url" {
  description = "Public app URL. Empty unless ingress is configured."
  value       = var.app_host == "" ? "" : (var.ingress_mode == "none" ? "http://${var.app_host}" : "https://${var.app_host}")
}

# ---------- Cognito auth (PRD-43) ----------

output "cognito_user_pool_id" {
  description = "Cognito User Pool ID. Set on the accelbench-api pod as COGNITO_USER_POOL_ID."
  value       = aws_cognito_user_pool.accelbench.id
}

output "cognito_client_id" {
  description = "Cognito App Client ID. Set on the accelbench-api pod as COGNITO_CLIENT_ID."
  value       = aws_cognito_user_pool_client.accelbench_api.id
}

output "cognito_user_pool_arn" {
  description = "Cognito User Pool ARN (for cross-account references or debugging)."
  value       = aws_cognito_user_pool.accelbench.arn
}
