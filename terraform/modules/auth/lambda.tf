# Pre-signup Lambda trigger. Enforces the allowed-email-domain policy so
# self-signup can be opened to the public without letting anyone in.

data "archive_file" "pre_signup" {
  type        = "zip"
  output_path = "${path.module}/lambda/pre_signup.zip"
  source_file = "${path.module}/lambda/pre_signup.py"
}

resource "aws_iam_role" "pre_signup" {
  count = length(var.allowed_email_domains) > 0 ? 1 : 0

  name = "${var.project_name}-cognito-pre-signup"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = var.tags
}

resource "aws_iam_role_policy_attachment" "pre_signup_basic" {
  count = length(var.allowed_email_domains) > 0 ? 1 : 0

  role       = aws_iam_role.pre_signup[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_lambda_function" "pre_signup" {
  count = length(var.allowed_email_domains) > 0 ? 1 : 0

  function_name = "${var.project_name}-cognito-pre-signup"
  role          = aws_iam_role.pre_signup[0].arn
  runtime       = "python3.12"
  handler       = "pre_signup.handler"
  timeout       = 5
  memory_size   = 128

  filename         = data.archive_file.pre_signup.output_path
  source_code_hash = data.archive_file.pre_signup.output_base64sha256

  environment {
    variables = {
      ALLOWED_EMAIL_DOMAINS = join(",", var.allowed_email_domains)
    }
  }

  tags = var.tags
}

resource "aws_lambda_permission" "cognito_invoke" {
  count = length(var.allowed_email_domains) > 0 ? 1 : 0

  statement_id  = "AllowCognitoInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.pre_signup[0].function_name
  principal     = "cognito-idp.amazonaws.com"
  source_arn    = aws_cognito_user_pool.this.arn
}
