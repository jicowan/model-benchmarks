terraform {
  required_providers {
    kubectl = {
      # See terraform/versions.tf — using gavinbunney/kubectl 1.x to
      # avoid 2.x's plan-time provider-config validation, which breaks
      # greenfield apply when cluster_endpoint is unknown.
      source  = "gavinbunney/kubectl"
      version = "~> 1.19"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.0"
    }
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    time = {
      source  = "hashicorp/time"
      version = "~> 0.9"
    }
  }
}
