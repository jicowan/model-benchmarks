terraform {
  required_version = ">= 1.7.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.0"
    }
    kubectl = {
      # Use gavinbunney/kubectl (the original upstream) for the 1.x
      # line. alekc/kubectl 2.x validates provider config at plan time,
      # which fails on greenfield apply because cluster_endpoint is
      # unknown before module.eks runs. The 1.x line defers validation
      # to apply, matching how the helm/kubernetes providers behave, so
      # a single `terraform apply` works without `-target=module.eks`.
      # alekc/kubectl never published a 1.x release, so the source
      # switches with the version pin. Pre-existing state created with
      # alekc/kubectl needs a one-time:
      #   terraform state replace-provider \
      #     registry.terraform.io/alekc/kubectl \
      #     registry.terraform.io/gavinbunney/kubectl
      source  = "gavinbunney/kubectl"
      version = "~> 1.19"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.0"
    }
    time = {
      source  = "hashicorp/time"
      version = "~> 0.9"
    }
    http = {
      source  = "hashicorp/http"
      version = "~> 3.4"
    }
  }
}
