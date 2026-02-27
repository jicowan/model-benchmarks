output "node_iam_role_name" {
  description = "The name of the Karpenter node IAM role"
  value       = module.karpenter.node_iam_role_name
}

output "node_iam_role_arn" {
  description = "The ARN of the Karpenter node IAM role"
  value       = module.karpenter.node_iam_role_arn
}

output "queue_name" {
  description = "The name of the Karpenter interruption queue"
  value       = module.karpenter.queue_name
}
