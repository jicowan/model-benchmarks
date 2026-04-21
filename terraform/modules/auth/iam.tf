# Extra permissions required for ALB OIDC (Cognito) on EKS Auto Mode.
# The Auto Mode load-balancing controller assumes the cluster role and must be
# able to: (a) resolve the Cognito app client, and (b) mutate ALBs + listener
# rules it creates. The ModifyRule / ModifyListener grants are specifically
# needed so the controller can attach the authenticate-cognito action to
# listener rules at apply time; without them the Ingress reconcile loops with
# "AccessDenied: ModifyRule" and users see 401 on /oauth2/idpresponse.
resource "aws_iam_role_policy" "cluster_alb_oidc" {
  name = "${var.project_name}-alb-oidc"
  role = var.cluster_iam_role_name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "CognitoRead"
        Effect   = "Allow"
        Action   = ["cognito-idp:DescribeUserPoolClient"]
        Resource = "*"
      },
      {
        Sid    = "ELBMutate"
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:SetSubnets",
          "elasticloadbalancing:SetSecurityGroups",
          "elasticloadbalancing:ModifyLoadBalancerAttributes",
          "elasticloadbalancing:ModifyRule",
          "elasticloadbalancing:ModifyListener",
          "elasticloadbalancing:ModifyTargetGroup",
          "elasticloadbalancing:ModifyTargetGroupAttributes",
        ]
        Resource = "*"
      },
    ]
  })
}
