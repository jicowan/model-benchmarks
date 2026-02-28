# PRD-04: Benchmark Configuration Recommender

## Problem

The "Run Benchmark" form has 7 configurable parameters (tensor parallel degree, quantization, concurrency, max model length, input/output sequence lengths, dataset). Choosing correct values requires understanding the model's architecture, memory requirements, and the instance type's accelerator specs. Incorrect values lead to OOM errors, wasted GPU capacity, or failed runs.

Users must currently research these values manually — checking the model's parameter count on HuggingFace, calculating memory requirements, and matching against GPU specs. This is error-prone and creates a barrier for non-expert users.

## Goals

1. **"Suggest Config" button** on the Run page that auto-fills form fields with recommended values after the user selects a model and instance type
2. **Deterministic calculator** — no LLM needed; recommendations are derived from model metadata (HuggingFace) + instance type specs (our DB) + memory estimation formulas
3. **Explanation tooltip** showing why each value was chosen so users can learn and override with confidence
4. **Validation warning** when a user's manual config is likely to fail (e.g., model too large for selected instance)

## Non-Goals

- Conversational AI / chat-based recommendations (can be added later)
- Automatic instance type selection (user still picks the instance)
- Neuron-specific optimizations (focus on GPU/vLLM for v1; Neuron rules differ significantly). Suggest button shows "not yet supported" for Neuron instances.
- Training or fine-tuning configuration

## Data Sources

### 1. HuggingFace Model API

**Model summary** (no auth for public models):
```
GET https://huggingface.co/api/models/{model_id}?expand[]=safetensors
```

Returns:
- `safetensors.total` — exact parameter count (e.g., 7248023552)
- `safetensors.parameters` — dtype breakdown (e.g., `{"BF16": 7248023552}`)
- `config.model_type` — architecture family ("llama", "mistral", etc.)

**Model config** (may require auth for gated models):
```
GET https://huggingface.co/{model_id}/resolve/main/config.json
```

Returns:
- `hidden_size` — hidden dimension (e.g., 4096)
- `num_attention_heads` — attention head count (e.g., 32)
- `num_key_value_heads` — KV head count for GQA (e.g., 8)
- `num_hidden_layers` — layer count (e.g., 32)
- `max_position_embeddings` — max context length (e.g., 32768)
- `torch_dtype` — native precision ("bfloat16", "float16")
- `vocab_size` — vocabulary size
- `intermediate_size` — FFN intermediate dimension

### 2. Instance Types (our DB)

From the `instance_types` table:
- `accelerator_count` — number of GPUs (e.g., 1, 4, 8)
- `accelerator_memory_gib` — total GPU memory across all devices (e.g., 24, 192, 640)
- `accelerator_name` — GPU model ("A10G", "H100", etc.)

Per-device memory = `accelerator_memory_gib / accelerator_count`

## Recommendation Logic

### Memory Estimation

**Model weight memory (bytes):**
```
bytes_per_param = {FP32: 4, BF16/FP16: 2, FP8/INT8: 1, INT4: 0.5}
model_memory = parameter_count × bytes_per_param
```

**KV cache memory per token (bytes):**
```
head_dim = hidden_size / num_attention_heads
kv_cache_per_token = 2 × num_hidden_layers × num_key_value_heads × head_dim × 2  (FP16)
```

**Total GPU memory available:**
```
total_gpu_memory = accelerator_memory_gib × 1024³
usable_memory = total_gpu_memory × 0.90  (10% overhead for activations, CUDA context, etc.)
```

### Parameter Recommendations

#### Tensor Parallel Degree

```
min_gpus_needed = ceil(model_memory / per_device_memory × 0.85)
tp = max(min_gpus_needed, 1)
```

Constraints:
- TP must be ≤ `accelerator_count`
- TP must evenly divide `num_attention_heads`
- TP must evenly divide `num_key_value_heads`
- Round up to the nearest valid divisor that satisfies all constraints

If `min_gpus_needed > accelerator_count`, show both options: quantization on the current instance OR a larger instance without quantization (see Feasibility Warning).

#### Quantization

```
if model fits in native precision (BF16/FP16) with selected TP:
    quantization = None
elif model fits with FP8 and GPU supports FP8 (H100, L40S):
    quantization = FP8
elif model fits with INT8:
    quantization = INT8
else:
    quantization = INT4 (AWQ)
```

FP8 support by GPU: H100, H200, L40S. Others default to INT8/INT4 if quantization is needed.

#### Max Model Length

```
remaining_memory = usable_memory - model_memory_with_quantization
max_tokens_in_kv = remaining_memory / kv_cache_per_token
max_model_len = min(max_position_embeddings, max_tokens_in_kv)
```

Round down to nearest power of 2 or common value (2048, 4096, 8192, 16384, 32768).

#### Concurrency

```
memory_per_sequence = kv_cache_per_token × avg_sequence_length
max_concurrent = floor(remaining_memory_after_model / memory_per_sequence)
concurrency = min(max_concurrent, 64)  # cap at reasonable default
concurrency = max(concurrency, 1)
```

Use `input_sequence_length + output_sequence_length` as average sequence length.

#### Input/Output Sequence Lengths

Defaults based on common benchmark patterns:
- `input_sequence_length = 512` (standard for ShareGPT-style benchmarks)
- `output_sequence_length = 256` (standard for ShareGPT-style benchmarks)

These are workload parameters rather than model-derived, so keep current defaults and don't override unless the model's max context is too small.

#### Dataset

Keep current default: `sharegpt`. No recommendation logic needed.

## Feasibility Warning

If the model doesn't fit at native precision, show both options so the user can choose:

```
┌─────────────────────────────────────────────────────────────┐
│ ⚠ Model too large for native BF16 on g5.xlarge             │
│                                                             │
│ Option A: Use INT8 quantization on g5.xlarge                │
│   → Estimated memory: ~8 GiB (fits in 24 GiB)              │
│   [Apply INT8 Config]                                       │
│                                                             │
│ Option B: Switch to g5.12xlarge (4× A10G, 96 GiB)          │
│   → Full BF16 precision, no quality trade-off               │
└─────────────────────────────────────────────────────────────┘
```

If even INT4 doesn't fit, only show the larger instance option.

## API Design

### Backend: GET /api/v1/recommend

This endpoint runs the calculation server-side so we can use instance type data from the DB without exposing it all to the frontend.

**Request:**
```
GET /api/v1/recommend?model={hf_id}&instance_type={name}
```

Optional header: `X-HF-Token: hf_...` (forwarded to HuggingFace for gated model configs)

**Response:**
```json
{
  "tensor_parallel_degree": 4,
  "quantization": null,
  "max_model_len": 8192,
  "concurrency": 32,
  "input_sequence_length": 512,
  "output_sequence_length": 256,
  "explanation": {
    "tensor_parallel_degree": "Model requires ~15 GiB in BF16. Each A10G has 24 GiB. 1 GPU is sufficient, but TP=4 distributes KV cache for higher throughput on g5.12xlarge.",
    "quantization": "Model fits in native BF16 precision on this instance.",
    "max_model_len": "After loading model weights, ~81 GiB available for KV cache, supporting up to 8192 tokens.",
    "concurrency": "Based on available KV cache memory with 768-token average sequence length.",
    "feasible": true
  },
  "model_info": {
    "parameter_count": 7248023552,
    "native_dtype": "bfloat16",
    "max_position_embeddings": 32768,
    "architecture": "llama"
  },
  "instance_info": {
    "accelerator_count": 4,
    "accelerator_memory_gib": 96,
    "accelerator_name": "A10G"
  }
}
```

If the model is infeasible:
```json
{
  "explanation": {
    "feasible": false,
    "reason": "Model requires ~140 GiB in BF16. g5.xlarge has 24 GiB total.",
    "suggested_instance": "p5.48xlarge"
  }
}
```

### Error cases

- HuggingFace API unreachable → 502 with message
- Model not found on HF → 404
- Gated model without token → 403 with hint to provide HF token
- Instance type not found in DB → 404

## Frontend

### "Suggest Config" button

Placement: below the Instance Type / Framework row, spanning full width.

**States:**
- **Disabled** if model or instance type is empty
- **Loading** with spinner while API call is in flight
- **Success**: auto-fills form fields + shows explanation panel
- **Warning**: model is infeasible, show warning banner with suggested instance
- **Error**: API error, show inline error message

### Explanation panel

An explanation panel that appears **expanded by default** after clicking "Suggest Config":

```
┌─────────────────────────────────────────────────────────────┐
│ ✓ Recommended Configuration                          [Hide] │
├─────────────────────────────────────────────────────────────┤
│ Model: Mistral-7B-Instruct-v0.3 (7.2B params, BF16)       │
│ Instance: g5.12xlarge (4× A10G, 96 GiB total)             │
│                                                             │
│ • Tensor Parallel = 1 — model fits on a single A10G       │
│   (15 GiB weights < 24 GiB per GPU)                       │
│ • Quantization = None — BF16 fits without quantization     │
│ • Max Model Len = 8192 — limited by available KV cache     │
│   memory after model loading                               │
│ • Concurrency = 32 — based on 9 GiB available for          │
│   KV cache with 768-token average sequences                │
└─────────────────────────────────────────────────────────────┘
```

### Validation warning (optional, future)

When the user manually changes a recommended field to a risky value, show an inline warning:
- TP > accelerator_count → "This instance only has {N} GPUs"
- Model won't fit → "Estimated memory {X} GiB exceeds available {Y} GiB"

## Implementation Plan

### Step 1: Backend recommendation endpoint

New file: `internal/api/recommend.go`
- Fetch model metadata from HuggingFace (safetensors + config.json)
- Look up instance type from DB
- Run memory estimation and recommendation logic
- Return JSON response

Register route: `GET /api/v1/recommend`

### Step 2: Frontend API client + types

- Add `RecommendResponse` type to `types.ts`
- Add `getRecommendation(model, instanceType, hfToken?)` to `api.ts`

### Step 3: "Suggest Config" button + explanation panel

- Add button to `Run.tsx` between instance type row and config row
- On click, call the API, auto-fill form fields, show explanation
- Handle loading, error, and infeasible states

### Step 4: Gated model support

- Forward the user's HF token to the backend
- Backend forwards it to HuggingFace when fetching config.json
- If 401/403 from HF, return helpful error message

## Files to create/modify

| File | Action |
|------|--------|
| `internal/api/recommend.go` | **Create** — recommendation endpoint with memory estimation logic |
| `internal/api/handlers.go` | Edit — register GET /api/v1/recommend route |
| `frontend/src/types.ts` | Edit — add RecommendResponse interface |
| `frontend/src/api.ts` | Edit — add getRecommendation() function |
| `frontend/src/pages/Run.tsx` | Edit — add Suggest Config button + explanation panel |

No new Go dependencies needed (uses `net/http` to call HuggingFace, `encoding/json` to parse responses).

## Verification

1. Select `mistralai/Mistral-7B-Instruct-v0.3` + `g5.xlarge` → suggests TP=1, no quantization, reasonable max_model_len
2. Select a 70B model + `g5.xlarge` → shows infeasibility warning, suggests larger instance
3. Select a 70B model + `p5.48xlarge` → suggests TP=8, no quantization
4. Select a gated model without HF token → shows "provide HF token" error
5. Manually override TP to an invalid value → form still submits (no blocking, just advisory)
