# Product Requirements Document: Inference Benchmark Platform

## 1. Overview

### 1.1 Product Name
*TBD* — working title: **AccelBench**

### 1.2 Problem Statement
Data scientists and systems engineers spend significant time and money benchmarking large language models across AWS accelerated instance types to find the optimal price-performance configuration for their inference workloads. This trial-and-error process delays production deployments, wastes compute spend, and requires deep expertise in both ML serving frameworks and cloud infrastructure.

### 1.3 Proposed Solution
A benchmarking platform that provides pre-computed inference performance data for popular open-source LLMs across AWS accelerated instance types, with the option to run on-demand benchmarks for custom configurations. Users can compare results side-by-side and make informed instance selection decisions in minutes instead of days.

### 1.4 Goal
Reduce the time from model selection to production-ready infrastructure decision from days/weeks to minutes.

---

## 2. Target Users

### 2.1 Primary Personas

| Persona | Role | Key Need |
|---|---|---|
| **Data Scientist** | Selects and fine-tunes models | Quickly identify which instance type delivers the best inference quality (latency, throughput) for a given model |
| **Systems Engineer / MLOps** | Deploys and operates inference infrastructure | Identify the most cost-effective instance type that meets SLA requirements (latency targets, throughput minimums) |

### 2.2 User Stories

1. **As a data scientist**, I want to select an open-source LLM (e.g., Llama 3.1 70B) and see inference performance metrics across available GPU instance types so that I can recommend an instance type to my ops team without running my own benchmarks.

2. **As a systems engineer**, I want to compare price-performance ratios across instance types for a given model so that I can minimize infrastructure cost while meeting latency SLAs.

3. **As a data scientist**, I want to run an on-demand benchmark with my specific configuration (batch size, sequence length, quantization) so that I can validate performance for my exact use case.

4. **As a systems engineer**, I want to export benchmark results so that I can include them in capacity planning documents and architecture reviews.

5. **As a data scientist**, I want to see how different quantization levels (FP16, INT8, INT4) affect performance on the same instance type so that I can make informed trade-offs between quality and cost.

---

## 3. Scope

### 3.1 In Scope (MVP)

- **Models**: Open-source LLMs available on Hugging Face (e.g., Llama 3.x, Mistral, Qwen, DeepSeek, Gemma)
- **Instance Types**: AWS accelerated instance families:
  - **G family**: g5 (A10G), g6 (L4), g6e (L40S)
  - **P family**: p4d (A100), p5 (H100), p5e (H200)
  - **Inf family**: inf2 (Inferentia2)
  - **Trn family**: trn1 (Trainium1), trn2 (Trainium2)
- **Inference Frameworks**:
  - **GPU instances**: vLLM
  - **Neuron instances (Inf2, Trn1, Trn2)**: vLLM with Neuron backend (transformers-neuronx)
- **Interfaces**: Web application and CLI tool
- **Execution Platform**: Amazon EKS (Kubernetes) with Karpenter for on-demand node provisioning
- **Deployment Model**: Free and open source; users deploy in their own AWS account and pay their own compute costs
- **Database**: Amazon Aurora PostgreSQL
- **Authentication**: No authentication required; users provide their own Hugging Face token for gated model access
- **Tensor Parallelism**: Multi-GPU and multi-NeuronCore configurations supported (required for 70B+ models)
- **Model Versioning**: Benchmark the latest model revision; retain historical results for previous revisions
- **Prompt Datasets**: Standardized dataset included; users may also supply custom datasets for on-demand runs
- **Karpenter NodePools**: Two NodePools — one for GPU instances (g + p families), one for Neuron instances (inf + trn families). Users may optionally configure a static node pool (Karpenter alpha feature) to avoid node provisioning startup time.
- **Data**: Pre-computed benchmark catalog plus on-demand benchmark execution

### 3.2 Out of Scope (MVP)

- Non-LLM models (diffusion, vision, embedding)
- Additional inference frameworks (TensorRT-LLM, TGI, Triton)
- Non-AWS cloud providers
- Fine-tuned or private model benchmarking
- Training workload benchmarking

### 3.3 Future Considerations

- Multi-framework support (TensorRT-LLM, TGI, Triton)
- Multi-cloud expansion (GCP, Azure)
- Custom/private model uploads for benchmarking
- Automated instance recommendation engine
- Cost estimation and budget-constrained optimization
- Diffusion and embedding model support

---

## 4. Functional Requirements

### 4.1 Pre-Computed Benchmark Catalog

| ID | Requirement | Priority |
|---|---|---|
| F-1 | System shall maintain a catalog of benchmark results for supported model and instance type combinations | P0 |
| F-2 | Catalog shall be refreshed on a defined cadence (e.g., weekly) and when new models or instance types are added | P0 |
| F-3 | Each catalog entry shall include the full set of inference metrics defined in Section 5 | P0 |
| F-4 | Users shall be able to filter the catalog by model, model size, instance type, and GPU type | P0 |
| F-5 | Users shall be able to sort results by any metric column | P1 |

### 4.2 On-Demand Benchmarking

| ID | Requirement | Priority |
|---|---|---|
| F-6 | Users shall be able to trigger a benchmark run for a specific model + instance type combination | P0 |
| F-7 | Users shall be able to configure benchmark parameters: concurrency level, input/output sequence lengths, quantization method, tensor parallelism degree, and prompt dataset (standardized or user-supplied) | P0 |
| F-8 | System shall deploy the model as a Kubernetes Deployment + Service (vLLM serving an OpenAI-compatible endpoint) on an EKS node with the required accelerator type. Karpenter shall provision the appropriate node if one is not already available. | P0 |
| F-8a | System shall wait for the model server to pass a readiness health check before starting the benchmark | P0 |
| F-8b | System shall launch a separate benchmark Job (load generator) that sends requests to the model's Service endpoint and collects inference metrics | P0 |
| F-8c | Upon benchmark completion, the system shall tear down the load generator Job, the model Deployment, and the Service. Karpenter shall reclaim the accelerated node after its configured TTL. | P0 |
| F-9 | Users shall be notified when their benchmark run completes | P1 |
| F-10 | On-demand results shall be persisted and associated with the user's account | P1 |

### 4.2.1 Benchmark Data Persistence

| ID | Requirement | Priority |
|---|---|---|
| F-24 | All benchmark results (pre-computed and on-demand) shall be persisted to a database | P0 |
| F-25 | The database shall store the full benchmark record: model metadata, instance type, framework version, benchmark configuration, all inference metrics (with percentiles), and a timestamp | P0 |
| F-26 | Benchmark results shall be retrievable by any combination of model, instance type, framework, date range, and user (for on-demand runs) | P0 |
| F-27 | The system shall support storing multiple benchmark runs for the same model+instance combination to track performance over time (e.g., across vLLM or Neuron SDK version upgrades) | P0 |
| F-28 | The system shall validate that benchmark results are successfully written to the database before reporting a run as complete | P0 |
| F-29 | The database shall enforce a schema that prevents partial or malformed benchmark records from being persisted | P1 |
| F-30 | Benchmark records shall be immutable once written; corrections shall be handled by marking records as superseded and inserting new records | P1 |

### 4.3 Comparison and Analysis

| ID | Requirement | Priority |
|---|---|---|
| F-11 | Users shall be able to compare up to 4 model+instance combinations side-by-side | P0 |
| F-12 | Comparison view shall display metrics in both tabular and chart formats | P0 |
| F-13 | Users shall be able to export comparison results as CSV or JSON | P1 |
| F-14 | System shall display estimated hourly cost per instance type alongside performance metrics, for both on-demand and reserved instance (1yr and 3yr) pricing tiers | P0 |
| F-15 | System shall calculate and display cost-per-token metrics (cost per 1M input tokens, cost per 1M output tokens) derived from hourly instance cost and benchmark throughput, for each pricing tier | P0 |
| F-15a | System shall calculate and display cost per request derived from hourly instance cost and requests-per-second throughput | P0 |
| F-15b | System shall display a monthly cost projection at a user-specified target request rate | P1 |
| F-15c | System shall retrieve instance pricing from the AWS Pricing API and refresh pricing data on a defined cadence (e.g., daily) | P0 |
| F-15d | Users shall be able to toggle between on-demand, 1-year reserved, and 3-year reserved pricing in all cost views | P0 |

### 4.4 Web Application

| ID | Requirement | Priority |
|---|---|---|
| F-16 | Web UI shall provide a model browser with search and filtering | P0 |
| F-17 | Web UI shall provide an instance type selector with accelerator specs (GPU/NeuronCore memory, count, interconnect) | P0 |
| F-18 | Web UI shall render interactive charts for metric visualization | P0 |
| F-19 | Web UI shall allow users to provide a Hugging Face token for accessing gated models | P0 |
| F-19a | Web UI shall support saving and browsing on-demand benchmark history (keyed by browser session or optional user identifier) | P1 |

### 4.5 CLI Tool

| ID | Requirement | Priority |
|---|---|---|
| F-20 | CLI shall support querying the pre-computed catalog (e.g., `accelbench query --model meta-llama/Llama-3.1-70B --instance p5.48xlarge`) | P0 |
| F-21 | CLI shall support triggering on-demand benchmarks (e.g., `accelbench run --model ... --instance ... --concurrency 16`) | P0 |
| F-22 | CLI shall output results in human-readable table format and machine-readable JSON | P0 |
| F-23 | CLI shall support comparing multiple results (e.g., `accelbench compare --model ... --instances p5.48xlarge,g6e.48xlarge`) | P1 |

---

## 5. Inference Metrics

The following metrics shall be captured for every benchmark run:

| Metric | Description | Unit |
|---|---|---|
| **Time to First Token (TTFT)** | Time from request submission to first token generated | milliseconds |
| **End-to-End Latency** | Total time from request submission to final token generated | milliseconds |
| **Inter-Token Latency (ITL)** | Average time between consecutive generated tokens | milliseconds |
| **Throughput (per request)** | Tokens generated per second for a single request | tokens/sec |
| **Throughput (aggregate)** | Total tokens generated per second across all concurrent requests | tokens/sec |
| **Requests per Second** | Number of completed requests per second at a given concurrency level | requests/sec |
| **Accelerator Utilization** | Average GPU or NeuronCore compute utilization during the benchmark | percentage |
| **Accelerator Memory Usage** | Peak GPU or NeuronCore memory consumption | GiB |
| **Token Input Length** | Number of input tokens used in the benchmark | tokens |
| **Token Output Length** | Number of output tokens generated per request | tokens |
| **Concurrency** | Number of simultaneous requests during the benchmark | count |

Metrics shall be reported with **p50, p90, p95, and p99 percentile** values where applicable (TTFT, latency, ITL).

### 5.1 Derived Cost Metrics

The following cost metrics shall be calculated by combining benchmark throughput with instance pricing data retrieved from the AWS Pricing API:

| Metric | Description | Unit |
|---|---|---|
| **Instance Hourly Cost** | Hourly cost of the instance type (on-demand, 1yr RI, 3yr RI) | USD/hr |
| **Cost per 1M Input Tokens** | (hourly_cost / input_throughput_tokens_per_sec / 3600) * 1,000,000 | USD |
| **Cost per 1M Output Tokens** | (hourly_cost / output_throughput_tokens_per_sec / 3600) * 1,000,000 | USD |
| **Cost per Request** | hourly_cost / requests_per_sec / 3600 | USD |
| **Monthly Cost Projection** | Estimated monthly cost at a user-specified request rate | USD/month |

Cost metrics shall be displayed for each pricing tier (on-demand, 1-year reserved, 3-year reserved). Users shall be able to toggle between pricing tiers in all views.

---

## 6. Non-Functional Requirements

| ID | Requirement | Priority |
|---|---|---|
| NF-1 | Pre-computed catalog queries shall return results in under 2 seconds | P0 |
| NF-2 | On-demand benchmarks shall begin execution within 15 minutes of submission (instance provisioning time) | P1 |
| NF-3 | System shall automatically terminate benchmark instances after run completion or a maximum timeout (e.g., 2 hours) | P0 |
| NF-4 | Web application shall be responsive and usable on desktop browsers (mobile is not required for MVP) | P1 |
| NF-5 | All benchmark runs shall use a standardized, reproducible methodology to ensure comparability across results | P0 |
| NF-6 | System shall be deployed in a single AWS region for MVP | P1 |
| NF-7 | Benchmark data shall be versioned to track changes in performance across vLLM and Neuron SDK versions | P1 |
| NF-8 | Database shall support concurrent reads (catalog queries) and writes (benchmark result ingestion) without contention | P0 |
| NF-9 | Database shall be backed up on a defined schedule with point-in-time recovery capability | P1 |
| NF-10 | Karpenter shall deprovision idle accelerated nodes after a configurable TTL (e.g., 10 minutes) to minimize cost | P0 |

---

## 7. Benchmark Methodology

To ensure results are comparable and reproducible:

1. **Service-based execution**: Each benchmark deploys the model as a Kubernetes Service with an OpenAI-compatible endpoint. A separate load generator Job sends requests to the Service endpoint, ensuring metrics reflect real serving conditions including HTTP/streaming overhead.
2. **Warm-up**: The load generator shall execute a warm-up phase (discarded from results) after the model server passes readiness checks, to account for JIT compilation and initial request overhead.
3. **Dataset**: A standardized prompt dataset shall be used (e.g., ShareGPT or a curated set) with defined input/output length distributions.
4. **Duration**: Each benchmark shall run for a minimum duration or minimum number of requests to produce statistically significant results.
5. **Resource isolation**: The load generator Job shall run on a separate (non-accelerated) node so that all accelerator resources are dedicated to model inference.
6. **Configuration capture**: The exact vLLM version (or Neuron SDK version), model revision (commit hash), quantization config, vLLM serving parameters, and instance metadata shall be recorded with every result.
7. **Reproducibility**: Users shall be able to view the exact configuration used for any benchmark result and reproduce it via the CLI.

---

## 8. High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      Users                              │
│              Web App    /    CLI                         │
└──────────┬──────────────────┬───────────────────────────┘
           │                  │
           ▼                  ▼
┌─────────────────────────────────────────────────────────┐
│                     API Gateway                         │
└──────────┬──────────────────────────────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────────────────────────────┐
│                        Amazon EKS Cluster                            │
│                                                                      │
│  ┌─────────────┐  ┌──────────────┐  ┌───────────────┐               │
│  │  Catalog API │  │ Benchmark    │  │  Comparison   │               │
│  │  (query,     │  │ Orchestrator │  │  Engine       │               │
│  │   filter,    │  │              │  │  (side-by-    │               │
│  │   sort)      │  │              │  │   side,       │               │
│  │              │  │              │  │   export)     │               │
│  └──────┬───────┘  └──────┬──────┘  └───────────────┘               │
│         │                 │                                           │
│         │                 │ 1. Deploy          ┌──────────────────┐  │
│         │                 ├────────────────────►│  Model Service   │  │
│         │                 │                     │  (Deployment +   │  │
│         │                 │                     │   K8s Service)   │  │
│         │                 │                     │                  │  │
│         │                 │                     │  vLLM / Neuron   │  │
│         │                 │                     │  OpenAI-compat   │  │
│         │                 │                     │  endpoint        │  │
│         │                 │                     └────────┬─────────┘  │
│         │                 │ 2. Wait for readiness        │            │
│         │                 │◄─────────────────────────────┘            │
│         │                 │                                           │
│         │                 │ 3. Launch           ┌──────────────────┐  │
│         │                 ├────────────────────►│  Load Generator  │  │
│         │                 │                     │  Job (K8s Job)   │  │
│         │                 │                     │                  │  │
│         │                 │                     │  Sends HTTP reqs │  │
│         │                 │                     │  to Model Service│  │
│         │                 │                     │  endpoint        │  │
│         │                 │                     └────────┬─────────┘  │
│         │                 │ 4. Collect results           │            │
│         │                 │◄─────────────────────────────┘            │
│         │                 │                                           │
│         │                 │ 5. Tear down Model Service + Job          │
│         │                 │                                           │
│         │                 │              ┌───────────────────────┐    │
│         │                 │              │  Karpenter            │    │
│         │                 │              │  (provisions nodes    │    │
│         │                 │              │   on demand per       │    │
│         │                 │              │   NodePool config,    │    │
│         │                 │              │   reclaims after TTL) │    │
│         │                 │              └───────────┬───────────┘    │
│         │                 │                          │                │
│         │                 │         Accelerated Nodes (on demand)     │
│         │                 │  ┌───────────────────────────────────┐    │
│         │                 │  │  GPU Nodes       │  Neuron Nodes  │    │
│         │                 │  │  (g5, g6, g6e,  │  (inf2, trn1,  │    │
│         │                 │  │   p4d, p5, p5e) │   trn2)        │    │
│         │                 │  └───────────────────────────────────┘    │
│         │                 │                                           │
└─────────┼─────────────────┼───────────────────────────────────────────┘
          │                 │
          ▼                 ▼
┌─────────────────────────────────┐
│         Results Database        │
│  (benchmark records, metrics,   │
│   configurations, user data)    │
│                                 │
│  - Write on benchmark complete  │
│  - Read for catalog queries     │
│  - Immutable records            │
│  - Point-in-time recovery       │
└─────────────────────────────────┘
```

---

## 9. Success Metrics

| Metric | Target |
|---|---|
| Time to instance decision (with pre-computed data) | < 15 minutes |
| Number of model+instance combinations in catalog at launch | >= 50 |
| On-demand benchmark completion time (excluding provisioning) | < 30 minutes |
| User satisfaction (post-launch survey) | >= 4.0 / 5.0 |
| Monthly active users (6 months post-launch) | TBD |

---

## 10. Design Decisions

| # | Question | Decision |
|---|---|---|
| 1 | **Pricing model** | Free and open source. No paid tiers. |
| 2 | **Authentication** | No authentication required. Users provide their own Hugging Face token for gated model access (e.g., Llama). |
| 3 | **Cost passthrough** | Users deploy the platform in their own AWS account and pay their own compute costs directly. |
| 4 | **Multi-GPU configurations** | Yes, required for MVP. Multi-GPU and multi-NeuronCore tensor parallel configurations are supported for models that require them (70B+). |
| 5 | **Model versioning** | Benchmark the latest revision on each run. Retain historical benchmark results so users can see performance across model revisions over time. |
| 6 | **Dataset customization** | Yes, for MVP. A standardized dataset is included, and users may also supply their own prompt datasets for on-demand benchmark runs. |
| 7 | **Database choice** | Amazon Aurora PostgreSQL. Strong fit for filtered queries, aggregations, time-series comparisons, and write-once bulk inserts. Managed backups and point-in-time recovery. |
| 8 | **Karpenter NodePool strategy** | Two NodePools organized by accelerator type: one for GPU instances (g + p families), one for Neuron instances (inf + trn families). Karpenter selects the specific instance type based on pod resource requests. |
| 9 | **Node warm pool** | No warm pool by default. Users may optionally configure a static node pool (Karpenter alpha feature, not enabled by default) to avoid node provisioning startup time. |

---

## 11. Milestones

| Phase | Deliverable | Description |
|---|---|---|
| **Phase 1** | EKS Platform & Database | Provision EKS cluster with Karpenter, configure NodePools for GPU and Neuron instance families, deploy results database with schema, backups, and point-in-time recovery |
| **Phase 2** | Benchmark Harness | Containerized benchmark execution pipeline: Kubernetes Job creates pod with vLLM (or Neuron backend), runs benchmark, persists metrics to database, verifies write, and exits (Karpenter handles node lifecycle) |
| **Phase 3** | Pre-Computed Catalog | Initial catalog of benchmark results for top 10-15 LLMs across supported instance types, stored in and served from the database |
| **Phase 4** | Web Application (MVP) | Catalog browser, filtering, comparison view, and metric charts — all backed by database queries |
| **Phase 5** | CLI Tool (MVP) | Query catalog, trigger on-demand runs, export results |
| **Phase 6** | On-Demand Benchmarking | User-triggered benchmarks with configurable parameters; Karpenter provisions required node, Job runs, results persisted to database |
| **Phase 7** | Iteration | Add models, instance types, price-performance analysis, and user-requested features |
