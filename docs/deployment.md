# Deployment Guide

This doc covers two operational paths most operators eventually need:

1. **[Disabling in-app authentication](#disabling-in-app-authentication)**
   for lab / bring-up / port-forward-only deployments where access
   control is handled upstream.
2. **[Swapping the loadgen and vLLM images](#swapping-the-loadgen-and-vllm-images)**
   to use alternate builds (ECR mirrors, forks with extra packages,
   pre-built public images).

The default deployment uses Cognito auth and pulls vLLM from Docker
Hub — both paths below are opt-in changes to `values.yaml` and (in
the auth case) a Terraform variable.

> **Installing into an existing EKS cluster?** See [`brownfield.md`](brownfield.md)
> for the `manage_cluster=false` path — skips VPC / EKS / selected
> cluster addons your platform team already owns.

## Disabling in-app authentication

AccelBench's default deployment requires a Cognito user pool and an
ACM certificate + public DNS name. That's the right shape for a
shared, long-running platform.

For environments where those aren't practical — a lab cluster with
no TLS cert, a bring-up cluster the admin reaches over VPN, a CI
environment, or any single-tenant setup where access control is
handled upstream of AccelBench — you can disable in-app auth
entirely. Every request is then handled as a synthetic admin
principal (`dev@local`).

**Never combine disabled auth with a publicly-reachable ingress.**
There is no request-level access control at all. All GPU-spending
endpoints are wide open to anyone who can reach the API pod. The
Terraform configuration below physically prevents the unsafe
combination by forcing ClusterIP-only ingress when auth is disabled.

### When to use it

- Lab or evaluation cluster, single operator, reached via
  `kubectl port-forward`.
- Existing corporate SSO terminates upstream (e.g. a private ALB
  behind a VPN that already authenticates users).
- CI / integration tests that need a working API without the Cognito
  bootstrap dance.

### When NOT to use it

- Any cluster with a public LoadBalancer or ALB.
- Multi-user / multi-tenant deployments where audit fidelity matters.
  All mutation rows attribute to `dev@local` — you'll know *something*
  changed but not *who*.
- Production-adjacent environments where different operators need
  different role-level access (admin / user / viewer). Disabled auth
  gives everyone admin.

### Setup

Three steps:

**1. Terraform apply with `auth_enabled=false`.** Skips the Cognito
user pool, the ACM certificate, and all Route 53 / public-ingress
resources. `ingress_mode` is internally forced to empty, so a
`-var ingress_mode=...` override has no effect.

```bash
cd terraform
terraform apply -var 'auth_enabled=false'
```

The Cognito outputs (`cognito_user_pool_id`, `cognito_client_id`,
`cognito_user_pool_arn`) will come back as empty strings. That's
expected — Helm doesn't need them in this mode.

**2. Helm install with `cognito.authDisabled=true`.** Emits
`AUTH_DISABLED=1` on the api pod; skips the `COGNITO_*` env vars
entirely.

```bash
helm install accelbench helm/accelbench \
  --namespace accelbench \
  --create-namespace \
  --set cognito.authDisabled=true \
  --set image.api.repository=<ecr-url>/accelbench-api \
  --set image.web.repository=<ecr-url>/accelbench-web \
  --set image.migration.repository=<ecr-url>/accelbench-migration \
  --set image.tools.repository=<ecr-url>/accelbench-tools \
  --set results.s3Bucket=<results-bucket> \
  --set models.s3Bucket=<models-bucket>
```

On boot, the api pod's log will print a loud banner:

```
╔══════════════════════════════════════════════════════════╗
║ AUTH_DISABLED=1 — every request handled as admin         ║
║ This is INSECURE on any publicly-reachable ingress.      ║
║ Access control must be handled upstream (VPN / RBAC).    ║
╚══════════════════════════════════════════════════════════╝
```

If you ever find yourself asking "is this cluster in disabled-auth
mode?", either check `kubectl logs deploy/accelbench-api | grep
AUTH_DISABLED` or look for the "AUTH DISABLED" chip in the top-right
corner of the UI.

**3. Port-forward to reach the UI.**

```bash
kubectl port-forward -n accelbench service/accelbench-web 8080:80
```

Open http://localhost:8080. The login page is skipped automatically;
the user-badge slot shows an amber "AUTH DISABLED" chip instead of
a logout button; the Users nav entry is hidden (the Cognito admin
APIs it depends on aren't available).

### UX differences

| Feature | Default | Disabled auth |
|---|---|---|
| Login page | Required | Auto-redirects to Dashboard |
| User badge | Email + Logout | "AUTH DISABLED" chip |
| Users nav entry | Admin-visible | Hidden |
| Admin-only pages (Configuration, Users) | Admin-visible | Configuration visible, Users hidden |
| Audit log | Row per mutation with real email | All rows `dev@local` |
| Role distinctions (admin/user/viewer) | Enforced | All requests = admin |
| Public ingress | Supported (ACM + Route 53) | Blocked by Terraform |

## Swapping the loadgen and vLLM images

AccelBench ships with two container-image overrides. Both are
per-cluster settings (operator-level), not per-run.

### vLLM image (PRD-49)

By default, the orchestrator deploys `vllm/vllm-openai:<framework_version>`
from Docker Hub (or from your ECR pull-through cache if you've
configured one). Set `image.vllm.repository` + `image.vllm.tag` to
force a different image, passed verbatim as the model container
image.

When to use this:
- **AWS public ECR mirrors** — avoid Docker Hub rate limits, pull
  faster from inside AWS.
- **Vendored builds** — your org maintains a patched vLLM.
- **Pre-baked DLC images** with additional dependencies (e.g. the
  AWS Deep Learning Containers vLLM).

Example — the AWS-maintained DLC vLLM image:

```yaml
# values.yaml or via --set
image:
  vllm:
    repository: public.ecr.aws/deep-learning-containers/vllm
    tag: "0.20.1-gpu-py312-cu130-ubuntu22.04-ec2-v1.1-2026-05-07-17-46-11-soci"
```

When set, the api pod gets an env var:

```
VLLM_IMAGE=public.ecr.aws/deep-learning-containers/vllm:0.20.1-...-soci
```

The orchestrator then uses this image for every benchmark run
regardless of the `tool_versions.framework_version` setting. The
Configuration → Tool Versions page shows an "VLLM_IMAGE ENV OVERRIDE
ACTIVE" banner so admins can tell the override is in effect.

**Compatibility check** — when picking an alternate vLLM image, verify:

- Entrypoint runs `python3 -m vllm.entrypoints.openai.api_server "$@"`
  (or equivalent). The orchestrator passes all vLLM flags as `args:`,
  so the image must accept them as-is.
- `--port 8000` works (the Service + readiness probe hit that port).
- Supports your model's loader: `runai_streamer` for S3-cached
  models, or vLLM's default loader for HuggingFace downloads.

The AWS DLC vLLM image above is known drop-in compatible (verified
on Mistral-7B, Llama-3.1-8B, g5 and g6e instance types).

### Loadgen image / inference-perf fork (PRD-34, PRD-46 #4)

By default the loadgen Job uses `quay.io/inference-perf/inference-perf`
at the tag set in `tool_versions.inference_perf_version`. Our ECR
fork (`docker/Dockerfile.inference-perf`) adds sentencepiece so
Mistral, older Llama, and T5-family models can tokenize — upstream
inference-perf doesn't ship it.

Example:

```yaml
image:
  loadgen:
    repository: <account-id>.dkr.ecr.<region>.amazonaws.com/accelbench-loadgen
    tag: latest
```

When set, the api pod gets:

```
INFERENCE_PERF_IMAGE=<account-id>.dkr.ecr.<region>.amazonaws.com/accelbench-loadgen:latest
```

The orchestrator uses this image for every loadgen pod regardless of
the `tool_versions.inference_perf_version` setting. The Configuration
→ Tool Versions page shows an "INFERENCE_PERF_IMAGE ENV OVERRIDE
ACTIVE" banner when the env var is present.

**Building the fork yourself**:

```bash
docker build --platform linux/amd64 \
  -t <account-id>.dkr.ecr.<region>.amazonaws.com/accelbench-loadgen:latest \
  -f docker/Dockerfile.inference-perf .
docker push <account-id>.dkr.ecr.<region>.amazonaws.com/accelbench-loadgen:latest
```

Then `helm upgrade --reuse-values --set image.loadgen.repository=<url>
--set image.loadgen.tag=latest` and restart the api pods so the
orchestrator picks up the new env var.

### Override precedence

For both images:

- **Env var set (Helm flags above) → used verbatim.** Per-run
  version settings (`tool_versions.framework_version` /
  `tool_versions.inference_perf_version`) are ignored at runtime but
  still saved on the run row for audit.
- **Env var unset → `tool_versions.*` composed into a Docker Hub
  (vLLM) or Quay (inference-perf) URI.** The Tool Versions page on
  the Configuration screen edits these.
- **`tool_versions.*` empty → vLLM default (`latest`) / inference-perf
  error.** Never actually empty in practice — the migration seeds
  both.
