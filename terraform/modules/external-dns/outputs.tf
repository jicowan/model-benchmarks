output "role_arn" {
  description = "IAM role ARN assumed by external-dns"
  value       = aws_iam_role.this.arn
}
