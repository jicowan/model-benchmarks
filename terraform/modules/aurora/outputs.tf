output "cluster_endpoint" {
  description = "The cluster endpoint (writer)"
  value       = module.aurora.cluster_endpoint
}

output "cluster_reader_endpoint" {
  description = "The cluster reader endpoint"
  value       = module.aurora.cluster_reader_endpoint
}

output "cluster_port" {
  description = "The port of the Aurora cluster"
  value       = module.aurora.cluster_port
}

output "cluster_master_user_secret" {
  description = "The secret containing the master user credentials"
  value       = module.aurora.cluster_master_user_secret
}

output "cluster_database_name" {
  description = "Name of the default database"
  value       = module.aurora.cluster_database_name
}
