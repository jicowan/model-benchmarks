output "vpc_id" {
  description = "The ID of the VPC"
  value       = module.vpc.vpc_id
}

output "azs" {
  description = "Availability zones the VPC spans (one private subnet each)"
  value       = local.azs
}

output "private_subnets_by_az" {
  description = "Map of AZ name -> private subnet ID (subnets are created in azs order)."
  value       = { for i, az in local.azs : az => module.vpc.private_subnets[i] }
}

output "private_subnets" {
  description = "List of private subnet IDs"
  value       = module.vpc.private_subnets
}

output "public_subnets" {
  description = "List of public subnet IDs"
  value       = module.vpc.public_subnets
}

output "intra_subnets" {
  description = "List of intra subnet IDs"
  value       = module.vpc.intra_subnets
}

output "private_subnet_cidr_blocks" {
  description = "List of private subnet CIDR blocks"
  value       = module.vpc.private_subnets_cidr_blocks
}
