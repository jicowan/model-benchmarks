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
