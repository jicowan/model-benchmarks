# AccelBench Terraform

This directory provisions the AWS infrastructure that backs AccelBench: EKS
cluster, Karpenter, Aurora PostgreSQL, ECR repos, S3 buckets, IAM roles, and
(optionally) a public HTTPS ingress.

All state is local by default. See `versions.tf` to add a backend if you want
remote state.

## Prerequisites

Before the first `terraform apply`:

- **Docker Hub credentials** (`dockerhub_username` + `dockerhub_access_token`
  in `terraform.tfvars`) are required **when** `manage_pull_through_cache = true`
  (the default). The ECR pull-through cache needs them to hydrate vLLM
  images. Create a read-only access token at
  [hub.docker.com/settings/security](https://hub.docker.com/settings/security).
  Missing them fails the apply with
  `SecretNotFoundException: The ARN of the secret specified in the pull through cache rule was not found`.
  **Skip this** if you set `manage_pull_through_cache = false` and plan to
  use `image.vllm.repository` in Helm values to pull from a public-ECR
  vLLM mirror — see [`docs/deployment.md`](../docs/deployment.md).
- **`region`** — defaults to `us-east-2`. If you want the cluster in a different
  region, set it in `terraform.tfvars` *and* ensure your AWS CLI can reach EKS
  there (the Terraform-invoked `aws eks get-token` calls pass `--region` from
  this variable, so no `AWS_REGION` env var is required).

## Two install modes

**Greenfield** (default): Terraform provisions a full VPC + EKS cluster + all
addons. Good for a dedicated AccelBench environment.

**Brownfield** (`manage_cluster = false`): Terraform installs into an
existing EKS cluster, skipping VPC + EKS + any cluster-level addons the
operator's cluster already runs. See [`docs/brownfield.md`](../docs/brownfield.md)
for the full walkthrough.

## Quick start (greenfield)

```bash
cd terraform
cp terraform.tfvars.example terraform.tfvars   # fill in dockerhub_* + region
terraform init
terraform apply
```

The default apply produces a working EKS cluster with no public URL. Access the
app via `kubectl port-forward` (see the root `README.md` for the deploy flow).

## Variables

See `variables.tf` for descriptions. Commonly set:

| Variable | Default | Notes |
|---|---|---|
| `region` | `us-east-2` | AWS region |
| `project_name` | `accelbench` | Prefix for all resources |
| `cluster_version` | `1.31` | EKS control-plane version |
| `dockerhub_username` / `dockerhub_access_token` | — | Required for ECR pull-through cache |
| `manage_accelbench_namespace` | `true` | Set false on clusters where the namespace exists already |
| `install_alb_controller` | `true` | Set false if the cluster already runs aws-load-balancer-controller |
| `ingress_mode` | `""` | See below. Empty = no public URL |
| `app_host` | `""` | Public hostname, e.g. `accelbench.example.com` |
| `hosted_zone_name` | `""` | Route 53 zone name, required for `acm-route53` |
| `existing_certificate_arn` | `""` | Pre-existing ACM cert ARN, required for `acm-existing` |
| `auth_enabled` | `true` | Set false to skip Cognito + ACM + public ingress entirely. See [`docs/deployment.md`](../docs/deployment.md) for the no-auth deployment walkthrough. |
| `manage_cluster` | `true` | Set false to install AccelBench into an existing EKS cluster. Requires `cluster_name`, `vpc_id`, `private_subnet_ids`. See [`docs/brownfield.md`](../docs/brownfield.md). |
| `install_karpenter_controller` | `true` | Set false when reusing an existing Karpenter install (>= v1.9). |
| `install_nvidia_device_plugin` | `true` | Set false when the cluster already runs the NVIDIA device plugin DaemonSet. |
| `manage_pull_through_cache` | `true` | Set false to skip the Docker Hub pull-through cache. Pair with `image.vllm.repository` in Helm values pointing at a public-ECR mirror. |

## Public HTTPS ingress (PRD-43a)

Ingress is **off by default**. To stand up a public URL, choose one of three
modes based on where your domain lives.

> **PRD-52 note:** `auth_enabled=false` forces `ingress_mode=""` internally
> regardless of what you set — public ingress + disabled auth would expose
> the GPU-spending control plane with zero access control. For no-auth
> deployments, use `kubectl port-forward`.

### Mode 1 — `acm-route53` (fully automated)

Your domain's hosted zone lives in this same AWS account. Terraform requests
an ACM cert, writes the DNS-validation records, and (after a second apply)
writes the public `A` alias to the ALB.

```hcl
# terraform.tfvars
ingress_mode     = "acm-route53"
app_host         = "accelbench.example.com"
hosted_zone_name = "example.com"
```

First-install flow is **two `terraform apply`s** because the ALB's DNS name
isn't known until after the Ingress is deployed by Helm:

```bash
# 1. Provision ALB controller, ACM cert + validation.
terraform apply

# 2. Preview the change first (dry run — no cluster change).
cd ..
helm upgrade accelbench helm/accelbench \
  --namespace accelbench \
  --reuse-values \
  --dry-run \
  --set ingress.enabled=true \
  --set ingress.host=$(terraform -chdir=terraform output -raw app_host) \
  --set ingress.tls.source=acm-route53 \
  --set ingress.tls.certificateArn=$(terraform -chdir=terraform output -raw certificate_arn)

# Re-run without --dry-run once the rendered Ingress looks right.
helm upgrade accelbench helm/accelbench \
  --namespace accelbench \
  --reuse-values \
  --set ingress.enabled=true \
  --set ingress.host=$(terraform -chdir=terraform output -raw app_host) \
  --set ingress.tls.source=acm-route53 \
  --set ingress.tls.certificateArn=$(terraform -chdir=terraform output -raw certificate_arn)

# 3. Wait ~90s for the ALB to reconcile.
kubectl describe ingress -n accelbench accelbench  # ADDRESS should populate

# 4. Flip ingress_deployed=true in terraform.tfvars, then apply to write
#    the public A-alias pointing your hostname at the ALB.
cd terraform
# edit terraform.tfvars: add `ingress_deployed = true`
terraform apply

# Verify
curl -I https://$(terraform output -raw app_host)/healthz   # → 200 OK
```

Subsequent changes are single-step.

### Mode 2 — `acm-existing` (bring your own cert)

You already have an ACM cert (e.g. wildcard cert for your zone, or a cert
validated via Cloudflare DNS). Terraform creates only the ALB controller.
DNS is your responsibility.

```hcl
# terraform.tfvars
ingress_mode             = "acm-existing"
app_host                 = "accelbench.example.com"
existing_certificate_arn = "arn:aws:acm:us-east-2:123456789012:certificate/..."
```

```bash
terraform apply

cd ..
helm upgrade accelbench helm/accelbench \
  --namespace accelbench \
  --reuse-values \
  --set ingress.enabled=true \
  --set ingress.host=accelbench.example.com \
  --set ingress.tls.source=acm-existing \
  --set ingress.tls.certificateArn=arn:aws:acm:us-east-2:...

# Then point your DNS (at your provider) at the ALB:
kubectl get ingress -n accelbench accelbench -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'
```

### Mode 3 — `none` (HTTP only)

For dev / CI clusters. Plaintext HTTP on port 80. No cert, no DNS automation.
Not suitable for auth (PRD-43 requires HTTPS).

```hcl
# terraform.tfvars
ingress_mode = "none"
app_host     = "accelbench.local"
```

```bash
terraform apply

cd ..
helm upgrade accelbench helm/accelbench \
  --namespace accelbench \
  --reuse-values \
  --set ingress.enabled=true \
  --set ingress.host=accelbench.local \
  --set ingress.tls.source=none
```

## AWS Load Balancer Controller

Terraform installs the controller (chart v3.2.2) in `kube-system` and wires
up IAM via Pod Identity. The IAM policy JSON is fetched at plan time from the
matching upstream git tag so it stays in lockstep with the controller
version.

To upgrade, bump two strings in `terraform/alb_controller.tf`:

```hcl
locals {
  alb_controller_version = "3.2.2"   # <- bump chart + app version together
}
data "http" "alb_controller_policy" {
  url = "https://.../aws-load-balancer-controller/v${local.alb_controller_version}/docs/install/iam_policy.json"
}
```

Then `terraform apply`.

If your cluster already runs the controller from another source, set
`install_alb_controller = false` in `terraform.tfvars` to skip the install.
