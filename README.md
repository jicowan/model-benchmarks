# AccelBench

A self-hosted benchmarking platform for LLM inference on AWS accelerated instances. Deploy models from HuggingFace onto GPU and Neuron instances, run standardized load tests, and compare latency, throughput, and cost across configurations.

## Features

- **Catalog browser** — Browse pre-computed benchmark results filterable by model, instance family, and accelerator type
- **On-demand benchmarks** — Run benchmarks against any HuggingFace model on any supported instance type with configurable parameters
- **Configuration recommender** — Auto-suggest tensor parallelism, quantization, context length, and concurrency based on model architecture and GPU memory
- **Pricing comparison** — Compare benchmark results side-by-side with on-demand and reserved instance pricing across 9 AWS regions
- **Job management** — Monitor, cancel, and delete running benchmarks

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌──────────────┐
│  React SPA  │────▶│  Go API     │────▶│  Aurora       │
│  (nginx)    │     │  Server     │     │  PostgreSQL   │
└─────────────┘     └──────┬──────┘     └──────────────┘
                           │
                    ┌──────▼──────┐
                    │ Orchestrator │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │  vLLM    │ │ Load     │ │ Karpenter │
        │  Model   │ │ Generator│ │ (scale)   │
        └──────────┘ └──────────┘ └──────────┘
```

The API server orchestrates the full benchmark lifecycle:

1. **Deploy** — Create a Kubernetes Deployment running vLLM with the target model
2. **Ready** — Wait for the model to load and pass health checks (up to 15 min)
3. **Load test** — Launch a Python load generator Job that sends concurrent streaming requests
4. **Collect** — Read the load generator's JSON output from pod logs
5. **Persist** — Compute p50/p90/p95/p99 percentiles and store metrics in the database
6. **Teardown** — Delete model Deployment, Service, and load generator Job; Karpenter scales down the node

## Supported Instance Types

| Family | Instances | Accelerator | GPUs | Memory |
|--------|-----------|-------------|------|--------|
| G5 | g5.xlarge – g5.48xlarge | NVIDIA A10G | 1–8 | 24–192 GiB |
| G6 | g6.xlarge – g6.48xlarge | NVIDIA L4 | 1–8 | 24–192 GiB |
| G6e | g6e.xlarge – g6e.48xlarge | NVIDIA L40S | 1–8 | 48–384 GiB |
| P4d | p4d.24xlarge | NVIDIA A100 | 8 | 320 GiB |
| P5 | p5.48xlarge | NVIDIA H100 | 8 | 640 GiB |
| P5e | p5e.48xlarge | NVIDIA H200 | 8 | 1128 GiB |
| Inf2 | inf2.xlarge – inf2.48xlarge | AWS Inferentia2 | 1–12 | 32–384 GiB |
| Trn1 | trn1.2xlarge – trn1.32xlarge | AWS Trainium | 1–16 | 32–512 GiB |
| Trn2 | trn2.48xlarge | AWS Trainium2 | 16 | 512 GiB |

## Tech Stack

| Component | Technology |
|-----------|------------|
| API server | Go 1.23, net/http, pgx/v5, client-go |
| Frontend | React 18, TypeScript, Tailwind CSS, Recharts, Vite |
| Load generator | Python 3.12, aiohttp |
| Inference | vLLM (GPU), vLLM-Neuron (Inferentia/Trainium) |
| Database | Aurora PostgreSQL |
| Infrastructure | Terraform, Helm, Karpenter |
| Container runtime | EKS with managed and Karpenter-provisioned nodes |

## Prerequisites

- AWS account with access to accelerated instance types
- [Terraform](https://www.terraform.io/) >= 1.5
- [Helm](https://helm.sh/) >= 3.0
- [kubectl](https://kubernetes.io/docs/tasks/tools/) configured for your cluster
- [Docker](https://www.docker.com/) for building images
- An ECR registry (or other container registry)

## Deployment

### 1. Infrastructure (Terraform)

The Terraform configuration creates a VPC, EKS cluster, Aurora PostgreSQL database, and supporting resources.

```bash
cd terraform
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars with your settings

terraform init
terraform plan
terraform apply
```

**Terraform modules:**
- `vpc` — VPC with public/private subnets across 3 AZs
- `eks` — EKS cluster with managed node group for system workloads
- `aurora` — Aurora PostgreSQL Serverless v2 cluster
- `karpenter` — Karpenter for provisioning accelerated nodes on demand

### 2. Container Images

Build and push the four container images:

```bash
# Set your registry
REGISTRY=<account-id>.dkr.ecr.<region>.amazonaws.com

# API server (also includes pricingrefresh binary)
docker buildx build --platform linux/amd64 \
  -f docker/Dockerfile.api \
  -t $REGISTRY/accelbench-api:latest --push .

# Web frontend
docker buildx build --platform linux/amd64 \
  -f docker/Dockerfile.web \
  -t $REGISTRY/accelbench-web:latest --push .

# Load generator
docker buildx build --platform linux/amd64 \
  -f docker/Dockerfile.loadgen \
  -t $REGISTRY/accelbench-loadgen:latest --push .

# Database migration
docker buildx build --platform linux/amd64 \
  -f docker/Dockerfile.migration \
  -t $REGISTRY/accelbench-migration:latest --push .
```

### 3. Application (Helm)

```bash
cd helm/accelbench

helm install accelbench . \
  --namespace accelbench \
  --create-namespace \
  --set image.api.repository=$REGISTRY/accelbench-api \
  --set image.web.repository=$REGISTRY/accelbench-web \
  --set image.migration.repository=$REGISTRY/accelbench-migration \
  --set database.existingSecret=accelbench-db \
  --set ingress.host=your-domain.example.com
```

The Helm chart deploys:
- API server (2 replicas)
- Web frontend (2 replicas)
- Database migration Job (runs on install/upgrade)
- Pricing refresh CronJob (daily)
- Catalog refresh CronJob (weekly)
- ALB Ingress
- RBAC for the API server to manage benchmark workloads

### 4. IAM (Pod Identity)

The pricing refresh CronJob needs `pricing:GetProducts` permission. Create an IAM role with a Pod Identity trust policy:

```bash
# Create IAM role and pod identity association
aws iam create-role --role-name accelbench-api \
  --assume-role-policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Principal": {"Service": "pods.eks.amazonaws.com"},
      "Action": ["sts:AssumeRole", "sts:TagSession"]
    }]
  }'

aws iam put-role-policy --role-name accelbench-api \
  --policy-name pricing-access \
  --policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Action": "pricing:GetProducts",
      "Resource": "*"
    }]
  }'

aws eks create-pod-identity-association \
  --cluster-name <cluster-name> \
  --namespace accelbench \
  --service-account accelbench-api \
  --role-arn arn:aws:iam::<account-id>:role/accelbench-api
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/catalog` | List catalog entries (filterable by model, instance family, accelerator type) |
| `POST` | `/api/v1/runs` | Create a new benchmark run |
| `GET` | `/api/v1/runs/{id}` | Get run details |
| `GET` | `/api/v1/runs/{id}/metrics` | Get metrics for a completed run |
| `GET` | `/api/v1/jobs` | List all benchmark runs |
| `POST` | `/api/v1/runs/{id}/cancel` | Cancel a running benchmark |
| `DELETE` | `/api/v1/runs/{id}` | Delete a benchmark run |
| `GET` | `/api/v1/pricing?region=us-east-2` | Get instance pricing for a region |
| `GET` | `/api/v1/recommend?model=...&instance_type=...` | Get configuration recommendations |

## Configuration Recommender

The `/api/v1/recommend` endpoint provides a deterministic configuration recommendation based on:

- **Model metadata** from HuggingFace (parameter count, attention heads, KV heads, hidden size, context length, dtype)
- **Instance specs** from the database (GPU count, GPU memory, GPU type)
- **Memory estimation** — model weights + KV cache + 10% overhead for CUDA context/activations

It recommends tensor parallel degree, quantization level, max context length, and concurrency. When a model doesn't fit on the selected instance, it suggests alternatives (quantization on the current instance or a larger instance type).

For gated models, pass the `X-HF-Token` header.

## Metrics Collected

Each benchmark run produces per-request measurements aggregated into percentiles:

| Metric | Description |
|--------|-------------|
| TTFT (p50/p90/p95/p99) | Time to first token |
| E2E Latency (p50/p90/p95/p99) | End-to-end request latency |
| ITL (p50/p90/p95/p99) | Inter-token latency |
| Throughput (per-request) | Average tokens/second per request |
| Throughput (aggregate) | Total tokens/second across all concurrent requests |
| Requests/second | Completed requests per second |

## Project Structure

```
.
├── cmd/
│   ├── server/          # API server entrypoint
│   ├── cli/             # CLI tool for headless operation
│   ├── loadgen/         # Python load generator
│   └── pricingrefresh/  # Pricing CronJob binary
├── internal/
│   ├── api/             # HTTP handlers and routing
│   ├── database/        # PostgreSQL repository layer
│   ├── manifest/        # Kubernetes YAML templates
│   ├── metrics/         # Loadgen output parsing and percentile computation
│   ├── orchestrator/    # Benchmark lifecycle management
│   └── recommend/       # Configuration recommender engine
├── frontend/            # React/TypeScript SPA
├── helm/accelbench/     # Helm chart
├── terraform/           # Infrastructure as code
├── db/migrations/       # SQL migration files
├── docker/              # Dockerfiles
└── scripts/             # Operational scripts
```

## Development

### API Server

```bash
# Run locally (requires DATABASE_URL and kubeconfig)
export DATABASE_URL="postgres://user:pass@localhost:5432/accelbench?sslmode=disable"
go run ./cmd/server
```

### Frontend

```bash
cd frontend
npm install
npm run dev    # Starts on http://localhost:5173, proxies /api to localhost:8080
```

### Tests

```bash
# Go tests
go test ./...

# Frontend tests
cd frontend && npm test
```

## License

This project is provided as-is for benchmarking and evaluation purposes.
