module "aurora" {
  source  = "terraform-aws-modules/rds-aurora/aws"
  version = "~> 9.0"

  name            = var.name
  engine          = "aurora-postgresql"
  engine_version  = "16.4"
  master_username = "accelbench"

  manage_master_user_password = true

  serverlessv2_scaling_configuration = {
    min_capacity = var.min_capacity
    max_capacity = var.max_capacity
  }

  instance_class = "db.serverless"
  instances = {
    writer = {}
  }

  vpc_id               = var.vpc_id
  db_subnet_group_name = aws_db_subnet_group.this.name
  security_group_rules = {
    eks_ingress = {
      source_security_group_id = var.eks_node_security_group_id
      description              = "Allow access from EKS nodes"
    }
  }

  storage_encrypted   = true
  apply_immediately   = true
  skip_final_snapshot = var.skip_final_snapshot

  tags = var.tags
}

resource "aws_db_subnet_group" "this" {
  name       = "${var.name}-subnet-group"
  subnet_ids = var.private_subnet_ids

  tags = var.tags
}
