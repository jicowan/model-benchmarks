# AccelBench

A self-hosted benchmarking platform for LLM inference on AWS accelerated instances. Deploy any HuggingFace model onto GPU or Neuron instances, run standardized load tests, and compare latency, throughput, GPU utilization, and cost across configurations.

## Features

- **Benchmarks catalog** — Browse and compare pre-computed results, filterable by model, instance family, and accelerator type. Side-by-side comparison of up to 4 runs.
- **On-demand benchmarks** — Run against any HuggingFace model on any supported accelerated instance type. Pick a scenario (chatbot, batch, stress, production, long-context) or customize parameters.
- **Test suites** — Run a series of scenarios against one model+instance in a single deployment. The model stays loaded; each scenario runs sequentially with its own load profile.
- **Configuration recommender** — Deterministic recommendations for tensor parallelism, quantization, `max_model_len`, and concurrency based on model architecture and accelerator memory. Surfaces OOM history and memory breakdown explanations.
- **Estimate page** — Predicted TTFT, throughput, and cost for a (model × instance × scenario) combination before you run it.
- **Model cache** — Pre-cache HuggingFace models to S3. Cached models load on GPU via [Run:ai Streamer](https://github.com/run-ai/runai-model-streamer) instead of downloading from HF on every run, cutting deploy time for large models.
- **Seed automation** — Matrix-seed the Benchmarks catalog from the Configuration page. The seeder walks model × instance pairs, dedups against existing runs, and dispatches runs in-process (no bash, no ConfigMap).
- **Configuration page** — UI for credentials (HF + Docker Hub in AWS Secrets Manager), the seeding matrix, per-scenario inference-perf overrides, registry/pull-through state, capacity reservations (ODCR + Capacity Block), and an audit log of config changes.
- **Capacity reservations** — Attach EC2 ODCRs or Capacity Blocks for ML to the GPU/Neuron Karpenter NodeClasses so benchmarks can target reserved capacity when on-demand is tight.
- **Exports** — Export a completed run's vLLM Kubernetes manifest; export single-run HTML reports or comparison reports as HTML/CSV.
- **Pricing comparison** — Benchmark results joined with on-demand and reserved pricing across 9 AWS regions.
- **Job management** — Monitor, cancel, and delete running benchmarks; view rendered inference-perf config and vLLM logs.

## Architecture

```
┌──────────────┐                       ┌───────────────────────┐
│  React SPA   │                       │  Aurora PostgreSQL    │
│  (nginx)     │                       │  runs, metrics,       │
└──────┬───────┘                       │  scenarios, overrides │
       │                               │  cache, audit log     │
       ▼                               └───────────▲───────────┘
┌──────────────┐        client-go      ┌───────────┴───────────┐
│  Go API      │──────────────────────▶│  Orchestrator         │
│  Server      │                       │  (in-process)         │
│              │◀────────────────────  └───────────┬───────────┘
└──┬───┬───────┘                                   │
   │   │                                           │
   │   │    ┌────────────────────────┐      ┌──────▼────────┐
   │   └───▶│ AWS Secrets Manager    │      │ Karpenter +   │
   │        │ HF + Docker Hub tokens │      │ SOCI parallel │
   │        └────────────────────────┘      │ pull on NVMe  │
   │                                        └───────┬───────┘
   │                                                │
   │   ┌─────────────┐   ┌─────────────┐   ┌────────▼────────┐
   │   │ S3: results │   │ S3: weights │   │ GPU / Neuron    │
   └──▶│ (per run)   │   │ (model cache│◀─▶│ nodes running   │
       └─────────────┘   │via Streamer)|   │ vLLM + loadgen  │
                         └─────────────┘   └─────────────────┘
```

Benchmark lifecycle:

1. **Recommend / submit** — User picks model + instance + scenario from the Run form, or POSTs `/api/v1/runs` directly.
2. **Deploy** — Orchestrator renders a Deployment + Service running vLLM (weights from HF or, for cached models, from S3 via Run:ai Streamer).
3. **Ready** — Wait for the model to load and pass `/health` (up to 25 min — long for p5.48xlarge with 70B models).
4. **Load test** — Launch a Job running [inference-perf](https://github.com/intel/inference-perf) with a per-scenario config rendered into a ConfigMap.
5. **Collect** — Load generator uploads JSON results to S3 (`accelbench-results-<account>`); orchestrator downloads and parses percentiles.
6. **Persist** — Metrics written to Postgres; DCGM / OOM events scraped if relevant.
7. **Teardown** — Deployment, Service, and Job deleted; Karpenter consolidates the node.

## Supported instance families

| Category | Families | Notes |
|----------|----------|-------|
| NVIDIA GPU (Ampere) | g5, p4d, p4de | A10G / A100 |
| NVIDIA GPU (Ada/Hopper/Blackwell) | g6, g6e, g7e, gr6, p5, p5e, p5en, p6-b200, p6-b300 | L4, L40S, H100, H200, B200, B300 |
| AWS Neuron | inf2, trn1, trn1n, trn2 | Inferentia2 / Trainium |

Instance selection in the Run form pulls live pricing and filters by accelerator type.

## Tech stack

| Component | Technology |
|-----------|------------|
| API server | Go 1.24, stdlib `net/http`, `jackc/pgx/v5`, k8s `client-go` (typed + dynamic), AWS SDK v2 (Secrets Manager, EC2, ECR, S3, Pricing) |
| Frontend | React 18, TypeScript, Tailwind CSS, Vite, Recharts |
| Load generator | [inference-perf](https://github.com/intel/inference-perf) (Python 3.12) |
| Inference | vLLM (GPU), vLLM-Neuron (Inferentia/Trainium) |
| Database | Aurora PostgreSQL Serverless v2 |
| Infrastructure | Terraform, Helm, Karpenter 1.9 (SOCI parallel-pull, NVMe instance store, reserved-capacity beta) |
| Cluster | EKS 1.31, AL2023 NVIDIA-optimized AMIs for GPU nodes |

## Prerequisites

- AWS account with quota for accelerated instance types
- [Terraform](https://www.terraform.io/) >= 1.5
- [Helm](https://helm.sh/) >= 3.0
- [kubectl](https://kubernetes.io/docs/tasks/tools/) configured for your cluster
- [Docker](https://www.docker.com/) for building images (BuildKit required — cache mounts are used)
- A Docker Hub account with an access token (needed for the ECR pull-through cache that mirrors the vLLM image; settable after install via the Configuration page)

## Deployment

### 1. Infrastructure (Terraform)

```bash
cd terraform
terraform init
terraform apply
```

`terraform.tfvars` is **optional for a barebones install**. Default apply creates the EKS cluster, Aurora, ECR repos, and the AWS Load Balancer Controller — but no public URL. You reach the UI via `kubectl port-forward` (see step 6). Set Docker Hub credentials here (or via the Configuration page after install) and, if you want a public HTTPS URL, one of three ingress modes documented in `terraform/README.md` (PRD-43a). `cp terraform.tfvars.example terraform.tfvars` to see annotated examples.

The Terraform config creates:

- VPC with public/private subnets across 3 AZs
- EKS 1.31 cluster with a managed `system` node group + Karpenter for accelerated workloads
- Aurora PostgreSQL Serverless v2
- Karpenter `EC2NodeClass` + `NodePool` for `gpu` and `neuron` (SOCI parallel-pull + NVMe RAID0 on GPU; `capacity-type: [reserved, on-demand]` so attached reservations are consumed)
- ECR repos for all app images + a pull-through cache rule at `<account>.dkr.ecr.<region>.amazonaws.com/dockerhub/*` pointing at Docker Hub
- S3 buckets for results and cached model weights
- IAM roles for API/loadgen/cache-job/model pods via EKS Pod Identity (Secrets Manager, EC2 describe, ECR describe, S3, Pricing)
- AWS Load Balancer Controller (chart v3.2.2) in `kube-system` via Pod Identity — provisions ALBs for any `Ingress` with `ingressClassName: alb`. Skip via `install_alb_controller=false` if your cluster already has it.
- Optionally: an ACM certificate + Route 53 alias for a public HTTPS URL. Off by default. See `terraform/README.md` for the three modes (`acm-route53` / `acm-existing` / `none`).

### 2. Container images

Six images — five app images plus the tools image used by CLI operations. GPU nodes pull the vLLM image directly from the pull-through cache, not from this registry.

```bash
export REGION=us-east-2
export ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
export REGISTRY=${ACCOUNT_ID}.dkr.ecr.${REGION}.amazonaws.com

aws ecr get-login-password --region $REGION | docker login --username AWS --password-stdin $REGISTRY

# accelbench-tools is not managed by Terraform — create it first:
aws ecr describe-repositories --repository-names accelbench-tools --region $REGION >/dev/null 2>&1 \
  || aws ecr create-repository --repository-name accelbench-tools --region $REGION --image-tag-mutability MUTABLE

# Build + push
for svc in api web loadgen migration cache-job tools; do
  docker build --platform linux/amd64 \
    -t $REGISTRY/accelbench-${svc}:latest \
    -f docker/Dockerfile.${svc} .
  docker push $REGISTRY/accelbench-${svc}:latest
done
```

> **BuildKit cache mounts** — `Dockerfile.api` uses `RUN --mount=type=cache` for the Go build + module caches. The first build populates the caches (~5 min); subsequent builds compile incrementally (~30-60s).

### 3. Database secret

Terraform creates the `accelbench` namespace and the `accelbench-db` Kubernetes secret (with a URL-encoded `DATABASE_URL` built from the Aurora master user secret) automatically as part of `terraform apply`. No manual steps.

If you ever change the Aurora password out of band (RDS-managed rotation, manual reset), re-run `terraform apply` to refresh the Kubernetes secret.

On an **existing cluster** where the namespace was created manually (pre-this change), set `-var manage_accelbench_namespace=false` to avoid conflicts, or `terraform import kubernetes_namespace.accelbench[0] accelbench && terraform import kubernetes_secret.accelbench_db[0] accelbench/accelbench-db` to take ownership.

### 4. Helm install

```bash
helm install accelbench helm/accelbench \
  --namespace accelbench \
  --set image.api.repository=$REGISTRY/accelbench-api \
  --set image.web.repository=$REGISTRY/accelbench-web \
  --set image.loadgen.repository=$REGISTRY/accelbench-loadgen \
  --set image.migration.repository=$REGISTRY/accelbench-migration \
  --set image.cacheJob.repository=$REGISTRY/accelbench-cache-job \
  --set image.tools.repository=$REGISTRY/accelbench-tools \
  --set database.existingSecret=accelbench-db \
  --set results.s3Bucket=accelbench-results-${ACCOUNT_ID} \
  --set models.s3Bucket=accelbench-models-${ACCOUNT_ID} \
  --set registry.pullThroughEnabled=true \
  --set registry.pullThroughURI=$REGISTRY
```

The chart deploys:

- API server (2 replicas) with Secrets Manager + EC2/ECR describe + Karpenter CRD patch permissions
- Web frontend (2 replicas)
- Database migration Job (runs as a Helm pre-upgrade hook on every `helm upgrade`)
- Pricing refresh CronJob (daily)
- Catalog refresh CronJob (weekly — `curl`s `/api/v1/catalog/seed`)

No public `Ingress` is rendered by default. See `terraform/README.md` to opt in to a public HTTPS URL.

### 5. Access the app

**Port-forward (default, works immediately):**

```bash
kubectl port-forward -n accelbench svc/accelbench-web 8080:80
# → http://localhost:8080
```

**Public HTTPS URL (optional):** add the ingress variables to `terraform.tfvars` (see `terraform/README.md`), run `terraform apply`, then re-run `helm upgrade` with the ingress flags shown there. After a second `terraform apply` the app is reachable at your configured hostname.

**No-auth / port-forward-only deployments:** see [`docs/deployment.md`](docs/deployment.md) for the `auth_enabled=false` path — skips Cognito + ACM + public ingress, suitable for labs and bring-up clusters. Requires `helm install --set cognito.authDisabled=true` and access via `kubectl port-forward` only.

**Alternate vLLM / loadgen images:** `docs/deployment.md` also covers how to point AccelBench at different container images — e.g. the AWS-published vLLM DLC image, or the included inference-perf fork that adds sentencepiece for Mistral / older-Llama / T5 tokenizers.

The migration Job applies every SQL file in `db/migrations/` on startup. Migrations are idempotent (`CREATE TABLE IF NOT EXISTS`, `ON CONFLICT DO NOTHING`), so re-running them is safe.

### 6. Platform configuration (UI)

Once the cluster is up, open the app (via port-forward or your public hostname) and open **Configuration** (left nav, gear icon). This is where operators set up the runtime knobs that aren't baked into the Helm chart:

**Credentials** — save an HF token once (for gated models like `meta-llama/*`) and a Docker Hub access token (the pull-through cache needs this to hydrate new images). If you skipped the Docker Hub tfvars at install time, set it here first — the secret entry exists but is empty until someone writes to it. Tokens go to AWS Secrets Manager (`accelbench/config/hf-token`, `ecr-pullthroughcache/dockerhub`) and auto-inject into every benchmark run, model-cache job, and catalog seed. Values are never shown after save.

**Seeding Matrix** — edit the models × instance types the "Seed Benchmarks" button explores. Models use a HuggingFace autocomplete; instance types are a dropdown populated from `/api/v1/instance-types`. Presence in the list = enabled.

**Scenario Overrides** — scenarios (chatbot, batch, stress, production, long-context) are code-defined in `internal/scenario/builtin.go`. This card lets operators override a scenario's `num_workers`, `streaming`, `input_mean`, or `output_mean` per scenario without a rebuild. Empty = inherit from code. Overriding `input_mean` or `output_mean` re-derives `std_dev/min/max` via the same formula scenarios use.

**Registry** — read-only view of the Docker Hub pull-through cache. Shows each `dockerhub/*` repo's size and last-pulled timestamp. When disabled, shows a `helm upgrade` snippet to turn it on.

**Capacity Reservations** — attach existing ODCRs or Capacity Blocks for ML to the GPU/Neuron Karpenter `EC2NodeClass`. Validates against EC2 live state (AZ match, instance family match, not cancelled/expired). Capacity Blocks show a drain warning ~40 min before end (when Karpenter pre-empts). Karpenter prioritizes reserved capacity and falls back to on-demand when reservations are exhausted.

**Audit Log** — last 50 write operations under `/api/v1/config/*`. Only action + short summary are stored; no token values.

### 7. Pre-cache popular models (recommended)

Cached models (1) skip the HF download on every benchmark and (2) bypass the HF gated-model check entirely since `config.json` and weights come from S3. Visit the **Models** page to queue a cache job. The job runs on a `system` node and uploads the full HF snapshot to `accelbench-models-<account>/models/<org>/<model>`.

## API endpoints

Core workflow:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/v1/status` | Component health |
| `GET` | `/api/v1/catalog` | Benchmarks catalog (filters: model, instance_family, accelerator_type, sort) |
| `GET` | `/api/v1/jobs` | List runs (alias kept for back-compat) |
| `POST` | `/api/v1/runs` | Submit a new run |
| `GET` | `/api/v1/runs/{id}` | Run details + metrics |
| `GET` | `/api/v1/runs/{id}/metrics` | Metrics only |
| `POST` | `/api/v1/runs/{id}/cancel` | Cancel |
| `DELETE` | `/api/v1/runs/{id}` | Delete |
| `GET` | `/api/v1/runs/{id}/export` | Export rendered K8s manifest |
| `GET` | `/api/v1/runs/{id}/report` | HTML report |

Planning / exploration:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/v1/instance-types` | All known accelerated instance types + specs |
| `GET` | `/api/v1/pricing?region=us-east-2` | On-demand + reserved pricing |
| `GET` | `/api/v1/recommend?model=...&instance_type=...` | Config recommendation (TP, quant, concurrency, max_model_len) |
| `GET` | `/api/v1/estimate` | Predicted TTFT / throughput / cost for a run shape |
| `GET` | `/api/v1/memory-breakdown` | Per-component memory for a (model × instance × TP × quant) |
| `GET` | `/api/v1/oom-history` | Past OOM events for a (model × instance) pair |
| `GET` | `/api/v1/compare/report` | Comparison report (HTML) across up to 4 runs |
| `GET` | `/api/v1/compare/csv` | Comparison report (CSV) |

Test suites + scenarios:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/v1/scenarios` | Built-in scenarios + effective (merged-with-override) values |
| `GET` | `/api/v1/test-suites` | Built-in test suites |
| `GET` | `/api/v1/suite-runs` | List suite runs |
| `POST` | `/api/v1/suite-runs` | Submit a suite run |
| `GET` | `/api/v1/suite-runs/{id}` | Suite run + per-scenario results |

Model cache:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/v1/model-cache` | List cached models |
| `POST` | `/api/v1/model-cache` | Trigger a cache job (HF → S3) |
| `POST` | `/api/v1/model-cache/register` | Register a pre-existing S3 model (no job) |
| `GET` | `/api/v1/model-cache/{id}` | Cache entry status |
| `DELETE` | `/api/v1/model-cache/{id}` | Remove entry (also deletes S3 prefix) |

Catalog seeding:

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/v1/catalog/seed` | Start a seed run (in-process goroutine). `?dry_run=true` to preview |
| `GET` | `/api/v1/catalog/seed` | Latest seed status |

Configuration (all covered by the Configuration page; writes audit-logged):

| Method | Path | Purpose |
|--------|------|---------|
| `GET` / `PUT` / `DELETE` | `/api/v1/config/credentials/hf-token` | HF token (write-only; describe only returns set + timestamp) |
| `PUT` / `DELETE` | `/api/v1/config/credentials/dockerhub-token` | Docker Hub token |
| `GET` | `/api/v1/config/credentials` | `{hf_token: {set, updated_at}, dockerhub_token: {set, updated_at}}` |
| `GET` / `PUT` | `/api/v1/config/catalog-matrix` | Seeding matrix (optimistic concurrency via `version`) |
| `GET` | `/api/v1/config/scenario-overrides` | Scenarios + effective defaults + overrides |
| `PUT` / `DELETE` | `/api/v1/config/scenario-overrides/{id}` | Upsert / clear a scenario override |
| `GET` | `/api/v1/config/registry` | Pull-through cache enabled + cached repos |
| `GET` / `POST` | `/api/v1/config/capacity-reservations` | List / attach |
| `DELETE` | `/api/v1/config/capacity-reservations/{node_class}/{reservation_id}` | Detach |
| `GET` | `/api/v1/config/audit-log` | Last 50 config writes |

For gated HF models when the platform token isn't set: pass `X-HF-Token` on `/recommend`, `/estimate`, and use the per-run HF token field. The platform token always wins the fallback race.

## Configuration recommender

Given a model and instance, the recommender deterministically outputs tensor parallelism, quantization, `max_model_len`, and max concurrency.

Inputs:

- **Model metadata** — parameter count, num_attention_heads, num_kv_heads, hidden_size, `max_position_embeddings`, `torch_dtype`. Fetched from HF, or read from `config.json` in S3 when the model is cached.
- **Instance specs** — GPU count, GPU memory, GPU type (from the database).
- **Memory model** — weights (adjusted for quantization) + KV cache (f(context, batch, num_kv_heads)) + 10% CUDA/activation overhead. Cross-checks against the `benchmark_run_oom_events` table for empirical overrides.

Output is either a concrete recommendation or, if the model doesn't fit, alternatives (quantization drops on the current instance; larger instance suggestions).

## Metrics collected

| Metric | Description |
|--------|-------------|
| TTFT p50/p90/p95/p99 | Time to first token |
| E2E latency p50/p90/p95/p99 | End-to-end request latency |
| ITL p50/p90/p95/p99 | Inter-token latency |
| TPOT p50/p90/p99 | Time per output token |
| Throughput per request | Tokens/second per request |
| Throughput aggregate | Tokens/second across concurrent requests |
| Requests/second | Completed requests per second |
| GPU utilization peak/avg | From DCGM (`DCGM_FI_DEV_GPU_UTIL`) |
| SM active peak/avg | From DCGM DCP (`DCGM_FI_PROF_SM_ACTIVE`) |
| Tensor pipe active peak/avg | From DCGM DCP (`DCGM_FI_PROF_PIPE_TENSOR_ACTIVE`) |
| DRAM active peak/avg | From DCGM DCP (`DCGM_FI_PROF_DRAM_ACTIVE`) |
| GPU memory peak/avg | From DCGM (`DCGM_FI_DEV_FB_USED`) |
| Waiting requests max | From vLLM scheduler (via DCGM pipe custom counter) |

## Project structure

```
.
├── cmd/
│   ├── server/          # API server entrypoint
│   ├── cli/             # CLI tool for headless operation
│   ├── loadgen/         # Python loadgen (inference-perf wrapper)
│   └── pricingrefresh/  # Pricing CronJob binary
├── internal/
│   ├── api/             # HTTP handlers + routing (incl. PRD-30..33 config)
│   ├── database/        # Postgres repo (pgx/v5, dynamic CRD access for Karpenter)
│   ├── manifest/        # K8s YAML templates (deployment, loadgen job, cache job)
│   ├── metrics/         # Loadgen JSON parsing + percentile computation
│   ├── oom/             # OOM event detection (scans pod events + DCGM)
│   ├── orchestrator/    # Benchmark lifecycle state machine
│   ├── pricing/         # EC2 pricing API integration
│   ├── recommend/       # Deterministic configuration recommender
│   ├── report/          # HTML report / comparison report generation
│   ├── scenario/        # Built-in scenarios + Override merger
│   ├── secrets/         # AWS Secrets Manager wrapper (HF + Docker Hub)
│   ├── seed/            # In-process catalog seeder (replaces bash)
│   └── testsuite/       # Built-in test suites
├── frontend/            # React/TypeScript SPA
├── helm/accelbench/     # Helm chart
├── terraform/           # VPC, EKS, Karpenter, Aurora, ECR, pull-through
├── db/migrations/       # SQL migration files (idempotent)
├── docker/              # Dockerfiles (api, web, loadgen, migration, cache-job, tools)
└── scripts/             # Operational scripts + CronJobs (refresh-catalog.yaml)
```

## Development

### API server

```bash
# Requires DATABASE_URL and a kubeconfig. For AWS-dependent endpoints
# (credentials, registry, capacity reservations) you also need AWS creds.
export DATABASE_URL="postgres://user:pass@localhost:5432/accelbench?sslmode=disable"
go run ./cmd/server
```

### Frontend

```bash
cd frontend
npm install
npm run dev    # http://localhost:5173, proxies /api to localhost:8080
```

### Tests

```bash
go test ./...        # all packages
cd frontend && npm test
```

Full validation (matches CI):

```bash
go build ./... && go test ./... && \
  terraform -chdir=terraform validate && \
  helm lint helm/accelbench && \
  (cd frontend && npm run build)
```

## License

This project is provided as-is for benchmarking and evaluation purposes.
