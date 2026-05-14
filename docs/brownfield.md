# Brownfield EKS installation

AccelBench's default Terraform provisions an entire EKS cluster plus
every addon it needs — VPC, EKS, Karpenter, AWS Load Balancer
Controller, NVIDIA device plugin, Pod Identity agent, EBS CSI driver.

If your organization already runs an EKS cluster with most of those
addons in place, you can install AccelBench alongside your existing
workloads instead of standing up a second cluster.

This is **brownfield mode**. Terraform skips the cluster-level
pieces your platform team already owns, but still provisions
everything AccelBench-specific: Aurora Postgres, S3 buckets for
models + results, ECR repositories, Cognito (if auth is on), IAM
roles and Pod Identity associations, and the Karpenter NodePools +
EC2NodeClasses that benchmark pods actually schedule onto.

## Prerequisites

The cluster you're installing into must meet these requirements:

- **EKS ≥ 1.31.** Older versions may work but aren't tested.
- **Karpenter ≥ v1.9** if you plan to reuse the existing controller
  (`install_karpenter_controller = false`). Our NodePool manifests
  use `disruption.budgets`, `consolidationPolicy`, and
  `consolidateAfter` fields that landed through v1.9. Terraform
  enforces this with a `check` block at plan time — an older version
  will fail the plan with a clear error.
- **EKS Pod Identity agent running in the cluster.** AccelBench's
  IAM integration uses Pod Identity (not IRSA). Verify with:
  ```bash
  kubectl get ds -n kube-system | grep pod-identity-agent
  ```
- **EBS CSI driver installed** (used by Aurora-adjacent workloads and
  any Karpenter nodes that mount EBS). Check:
  ```bash
  kubectl get ds -n kube-system | grep ebs-csi-node
  ```
- **A VPC with private subnets that can reach the internet** (NAT,
  VPC endpoints, or egress proxy — AccelBench pulls container images
  and fetches models from HuggingFace / S3 / public ECR).

## What AccelBench always owns

Even in brownfield mode, the following are created by our Terraform
and managed by AccelBench:

- **Karpenter NodePools** (`general-purpose`, `gpu`, `neuron`) and
  their **EC2NodeClasses**. These carry a dedicated taint
  (`accelbench.io/dedicated=true:NoSchedule`) so benchmark pods only
  land on our nodes and your pods never do. `weight: 100` on each
  NodePool ensures Karpenter picks ours over any operator NodePools
  that happen to match AccelBench pod requirements.
- **Karpenter node IAM role** and its attached policies (S3, ECR,
  SSM). Needed regardless of whether we install the controller.
- **Aurora Postgres** cluster (`accelbench-db`) + its managed master
  secret.
- **S3 buckets**: `accelbench-models-<account>` and
  `accelbench-results-<account>`.
- **ECR repositories**: `accelbench-api`, `accelbench-web`,
  `accelbench-migration`, `accelbench-loadgen`, `accelbench-cache-job`,
  `accelbench-tools`.
- **Cognito user pool** + app client + bootstrap admin (when
  `auth_enabled = true`).
- **ACM certificate** + Route 53 records (when `ingress_mode =
  acm-route53`).
- **IAM roles + Pod Identity associations** for 4 service accounts:
  `accelbench-api`, `accelbench-loadgen`, `accelbench-model`,
  `accelbench-cache-job`.

## What you can opt out of

Six toggles skip cluster-level components when the operator's cluster
already has them:

| Variable | Default | Skip when |
|---|---|---|
| `manage_cluster` | `true` | You have an existing EKS cluster to install into. Set `false`. |
| `install_karpenter_controller` | `true` | Cluster already runs Karpenter ≥ v1.9. |
| `install_alb_controller` | `true` | Cluster already runs `aws-load-balancer-controller`. |
| `install_nvidia_device_plugin` | `true` | Cluster already runs the NVIDIA device plugin DaemonSet. |
| `manage_pull_through_cache` | `true` | You don't need a Docker Hub pull-through cache (e.g. you're using a public-ECR vLLM image via `image.vllm.repository` — see [`deployment.md`](deployment.md)). |
| `auth_enabled` | `true` | You want to run without Cognito. See [`deployment.md`](deployment.md#disabling-in-app-authentication). |

The Pod Identity agent and EBS CSI driver don't have dedicated
toggles — they come with our EKS module in greenfield and are
assumed pre-existing in brownfield (they're prerequisites).

## Setup

### 1. Prepare inputs

Identify:

- Your cluster name (e.g. `my-prod-eks`).
- The VPC ID the cluster runs in.
- Private subnet IDs where AccelBench's data plane should land (must
  be in the same VPC; Aurora and any Karpenter-provisioned nodes go
  here).

Run these to verify your cluster meets the prerequisites:

```bash
kubectl config current-context                          # confirm you're pointed at the right cluster
kubectl get daemonset -n kube-system                    # check for pod-identity-agent, ebs-csi-node, nvidia-device-plugin, aws-load-balancer-controller
kubectl get deployment -n kube-system karpenter -o \
  jsonpath='{.spec.template.spec.containers[0].image}'  # confirm Karpenter version >= 1.9
```

### 2. Write `terraform.tfvars`

Example for a cluster that has Karpenter, ALB controller, NVIDIA
plugin, Pod Identity, and EBS CSI already running:

```hcl
# terraform.tfvars
region              = "us-east-2"
project_name        = "accelbench"

# Brownfield: install into existing cluster
manage_cluster      = false
cluster_name        = "my-prod-eks"
vpc_id              = "vpc-abc123def456"
private_subnet_ids  = [
  "subnet-0a1b2c3d",
  "subnet-0e4f5g6h",
  "subnet-0i7j8k9l",
]

# Skip cluster-level components the operator already has
install_karpenter_controller = false
install_alb_controller       = false
install_nvidia_device_plugin = false

# Optional: skip the pull-through cache if you plan to use
# image.vllm.repository to point at a public-ECR vLLM image
manage_pull_through_cache    = false

# Docker Hub creds only needed when manage_pull_through_cache = true
# dockerhub_username     = ""
# dockerhub_access_token = ""

# Auth: leave enabled for multi-tenant, or set false for lab clusters
auth_enabled                 = true
admin_email                  = "your-admin@example.com"

# Ingress: three modes. See terraform/README.md.
ingress_mode                 = ""    # port-forward only; change to acm-route53 / acm-existing for public URL
```

### 3. Apply

```bash
cd terraform
terraform init
terraform plan -out=plan.out       # review the plan — AccelBench creates RDS + Cognito + IAM + NodePools, does NOT touch your cluster addons
terraform apply plan.out
```

The plan on a brownfield cluster is substantially shorter than
greenfield — you should see roughly:

- `module.aurora.*` (Aurora Postgres + subnet group)
- `module.karpenter.*` for the IAM role, interruption queue, and
  EC2NodeClasses + NodePools (but NOT the Helm release)
- `aws_s3_bucket.models`, `aws_s3_bucket.results`
- `aws_ecr_repository.*` (6 repos)
- `aws_cognito_user_pool.*` (when `auth_enabled = true`)
- `aws_iam_role.*` + `aws_eks_pod_identity_association.*` (4 SAs)
- `aws_ec2_tag.subnet_discovery["subnet-..."]` × 3 (tags the operator
  subnets for Karpenter's `karpenter.sh/discovery` selector)
- `aws_ec2_tag.cluster_sg_discovery` (same tag on the cluster SG)

### 4. Push images

Same as greenfield — push API, web, migration, loadgen, cache-job,
and tools images to the ECR repos Terraform created:

```bash
# See the main README.md "Container images" section for the full
# build-and-push recipe.
```

### 5. Helm install

```bash
helm install accelbench helm/accelbench \
  --namespace accelbench \
  --create-namespace \
  --set image.api.repository=$(terraform output -raw ecr_api_url) \
  --set image.web.repository=$(terraform output -raw ecr_web_url) \
  --set image.migration.repository=$(terraform output -raw ecr_migration_url) \
  --set image.tools.repository=$(terraform output -raw ecr_tools_url) \
  --set image.loadgen.repository=$(terraform output -raw ecr_loadgen_url) \
  --set image.cacheJob.repository=$(terraform output -raw ecr_cache_job_url) \
  --set results.s3Bucket=$(terraform output -raw results_s3_bucket) \
  --set models.s3Bucket=$(terraform output -raw models_s3_bucket) \
  --set cognito.userPoolId=$(terraform output -raw cognito_user_pool_id) \
  --set cognito.clientId=$(terraform output -raw cognito_client_id)
```

If you skipped the pull-through cache, add one of:

```bash
# Option A: use the public-ECR AWS DLC vLLM image
--set image.vllm.repository=public.ecr.aws/deep-learning-containers/vllm \
--set image.vllm.tag=0.20.1-gpu-py312-cu130-ubuntu22.04-ec2-v1.1-2026-05-07-17-46-11-soci

# Option B: use upstream Docker Hub directly (rate-limited!)
# No extra flags; the orchestrator falls back to vllm/vllm-openai
```

See [`deployment.md`](deployment.md#swapping-the-loadgen-and-vllm-images)
for the full image-override options.

### 6. Reach the app

Port-forward is the simplest option — works on any cluster, no DNS,
no cert, no ingress:

```bash
kubectl port-forward -n accelbench svc/accelbench-web 8080:80
# → http://localhost:8080
```

For a public URL, configure `ingress_mode` per the main
[`terraform/README.md`](../terraform/README.md).

## How the coexistence safety works

The three AccelBench NodePools apply a **dedicated taint**:

```yaml
taints:
  - key: accelbench.io/dedicated
    value: "true"
    effect: NoSchedule
```

Only our benchmark pods (vLLM model Deployment, inference-perf Job,
model-cache Job) carry a matching toleration. This means:

- Operator pods **never** land on AccelBench's nodes (they don't
  tolerate the taint).
- AccelBench pods **never** land on operator nodes (our pods have a
  specific nodeSelector + the operator nodes don't carry the taint
  key we're targeting).
- If your cluster has a GPU NodePool whose requirements happen to
  match AccelBench's GPU pod requirements, our `weight: 100` +
  dedicated taint prevent Karpenter from routing our pods to your
  pool.

**The API + web pods (from the Helm chart) are not tainted** — they
schedule on your cluster's general-purpose nodes like any other
platform workload. That's deliberate. Only the benchmark workloads
themselves need isolation.

### Your device plugins / daemonsets need the toleration too

Because AccelBench's NodePools carry the `accelbench.io/dedicated`
taint, **any DaemonSet that needs to run on AccelBench's GPU or
Neuron nodes must tolerate it**. In greenfield mode we ship the
three relevant daemonsets (nvidia-device-plugin, neuron-device-plugin,
dcgm-exporter) with the toleration baked in. In brownfield mode
**your cluster's existing daemonsets are the ones in play**, and
most off-the-shelf installs only tolerate the accelerator-specific
taint (e.g. `nvidia.com/gpu:NoSchedule`) — not ours.

Symptom if this isn't addressed: Karpenter provisions the GPU node
and the benchmark pod still sits `Pending` with `Insufficient
nvidia.com/gpu`. The node is up, but the device plugin DaemonSet
can't land on it, so `nvidia.com/gpu` is never advertised in the
node's allocatable resources.

Before your first benchmark, add the toleration to any daemonset
that targets the GPU / Neuron instance categories. Examples:

```bash
# NVIDIA device plugin (most common install)
kubectl patch ds -n kube-system nvidia-device-plugin-daemonset \
  --type=json -p '[{"op":"add","path":"/spec/template/spec/tolerations/-","value":{"key":"accelbench.io/dedicated","operator":"Exists","effect":"NoSchedule"}}]'

# Neuron device plugin (if you run inf2/trn1)
kubectl patch ds -n kube-system neuron-device-plugin-daemonset \
  --type=json -p '[{"op":"add","path":"/spec/template/spec/tolerations/-","value":{"key":"accelbench.io/dedicated","operator":"Exists","effect":"NoSchedule"}}]'

# dcgm-exporter (NVIDIA GPU metrics for Prometheus)
kubectl patch ds -n <ns> dcgm-exporter \
  --type=json -p '[{"op":"add","path":"/spec/template/spec/tolerations/-","value":{"key":"accelbench.io/dedicated","operator":"Exists","effect":"NoSchedule"}}]'
```

If you install the NVIDIA GPU Operator via Helm, add the toleration
via values:

```yaml
# values.yaml for gpu-operator
devicePlugin:
  tolerations:
    - key: accelbench.io/dedicated
      operator: Exists
      effect: NoSchedule
dcgmExporter:
  tolerations:
    - key: accelbench.io/dedicated
      operator: Exists
      effect: NoSchedule
```

**You don't need to add the toleration to DaemonSets that already
tolerate everything (`operator: Exists` with no key).** The EKS-
managed daemonsets (`aws-node`, `kube-proxy`, `eks-pod-identity-agent`,
`ebs-csi-node`) tolerate `*` and land on our nodes automatically.
Check yours with:

```bash
kubectl get ds -A -o json | jq -r '.items[] | select([(.spec.template.spec.tolerations // [])[] | select(.operator == "Exists" and (.key // "") == "")] | length == 0) | "\(.metadata.namespace)/\(.metadata.name)"'
```

That prints the daemonsets that *don't* tolerate everything — audit
each one and decide whether it needs to run on AccelBench's nodes.

## How capacity reservations work in brownfield mode

AccelBench's Capacity Reservations feature (attach an ODCR or
Capacity Block for ML to a NodePool, see the Configuration page)
targets our EC2NodeClasses via the
`capacityReservationSelectorTerms` field. This requires AccelBench
to own the NodeClasses, which it does in both greenfield and
brownfield modes. Operators don't need to — and can't — attach
reservations to their existing NodePools through AccelBench's UI.

## Troubleshooting

**`terraform plan` fails with "Karpenter version..."**
: Your installed Karpenter is older than v1.9. Either upgrade the
  operator's controller, or set `install_karpenter_controller = true`
  to have Terraform install a compatible version alongside.

**Pods stuck `Pending` with message "No matching taint toleration"**
: Most likely the NodePool didn't create the taint — re-check
  `terraform apply` output for the NodePool resources. Or the
  orchestrator image is from before PRD-53 and doesn't emit the new
  toleration. Rebuild and push the API image.

**Benchmark pod stuck `Pending` with "Insufficient nvidia.com/gpu"
(node is Ready)**
: The GPU node provisioned but the NVIDIA device plugin daemonset
  can't land on it, so the `nvidia.com/gpu` resource is never
  advertised. See ["Your device plugins / daemonsets need the
  toleration too"](#your-device-plugins--daemonsets-need-the-toleration-too)
  above — patch your device plugin daemonsets to tolerate
  `accelbench.io/dedicated`. Confirm with `kubectl describe node
  <gpu-node> | grep nvidia.com/gpu` — the allocatable block should
  show `nvidia.com/gpu: 1` (or higher for multi-GPU instances).

**Aurora + subnet conflict**
: The subnets you provided in `private_subnet_ids` must be in the
  same VPC (`vpc_id`). If they're in a different VPC, `terraform
  apply` fails at the Aurora subnet-group creation step.

**`karpenter.sh/discovery` tag missing on subnets**
: Terraform applies `aws_ec2_tag.subnet_discovery` for each subnet
  you list. If the tag didn't get applied, check the operator's IAM
  policy permits the caller to `ec2:CreateTags`.

**Pod Identity agent not running**
: Brownfield mode assumes it's already there. Install it via
  `aws eks create-addon --cluster-name <name> --addon-name
  eks-pod-identity-agent` and re-run `terraform apply`. IAM
  associations will succeed once the agent is available.
