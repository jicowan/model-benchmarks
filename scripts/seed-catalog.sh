#!/usr/bin/env bash
# seed-catalog.sh — Read catalog-matrix.yaml and submit benchmark runs via the API.
# Uses the /api/v1/recommend endpoint to get optimal configuration for each
# model × instance combination.
#
# Usage:
#   ./scripts/seed-catalog.sh [--api-url URL] [--hf-token TOKEN] [--dry-run]
#
# Requires: yq (https://github.com/mikefarah/yq), jq

set -euo pipefail

API_URL="${API_URL:-http://localhost:8080}"
HF_TOKEN="${HF_TOKEN:-}"
DRY_RUN=false
MATRIX_FILE="$(dirname "$0")/catalog-matrix.yaml"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --api-url)  API_URL="$2"; shift 2;;
    --hf-token) HF_TOKEN="$2"; shift 2;;
    --dry-run)  DRY_RUN=true; shift;;
    *)          echo "Unknown arg: $1" >&2; exit 1;;
  esac
done

if ! command -v yq &>/dev/null; then
  echo "Error: yq is required. Install from https://github.com/mikefarah/yq" >&2
  exit 1
fi

if ! command -v jq &>/dev/null; then
  echo "Error: jq is required." >&2
  exit 1
fi

# Read defaults.
FW_VERSION=$(yq '.defaults.framework_version' "$MATRIX_FILE")
SCENARIO=$(yq '.defaults.scenario // "chatbot"' "$MATRIX_FILE")
DATASET=$(yq '.defaults.dataset // "synthetic"' "$MATRIX_FILE")
MIN_DURATION=$(yq '.defaults.min_duration_seconds // 180' "$MATRIX_FILE")

NUM_MODELS=$(yq '.models | length' "$MATRIX_FILE")
NUM_INSTANCES=$(yq '.instance_types | length' "$MATRIX_FILE")

echo "AccelBench Catalog Seeder"
echo "  API:       $API_URL"
echo "  Models:    $NUM_MODELS"
echo "  Instances: $NUM_INSTANCES"
echo "  Dry run:   $DRY_RUN"
echo ""

# ── Deduplication: fetch all existing runs and build a lookup set ──
echo "Fetching existing runs for deduplication..."
EXISTING_RUNS=$(curl -sf "$API_URL/api/v1/jobs?limit=10000" 2>/dev/null || echo "[]")

# Build a newline-separated list of "model_hf_id|instance_type_name" keys.
EXISTING_KEYS=$(echo "$EXISTING_RUNS" | jq -r '.[] | .model_hf_id + "|" + .instance_type_name' 2>/dev/null || echo "")
EXISTING_COUNT=$(echo "$EXISTING_KEYS" | grep -c . 2>/dev/null || echo "0")
echo "  Found $EXISTING_COUNT existing runs."
echo ""

submitted=0
skipped=0
infeasible=0

for mi in $(seq 0 $((NUM_MODELS - 1))); do
  MODEL_HF_ID=$(yq ".models[$mi].hf_id" "$MATRIX_FILE")

  for ii in $(seq 0 $((NUM_INSTANCES - 1))); do
    INSTANCE=$(yq ".instance_types[$ii]" "$MATRIX_FILE")

    # ── Skip if this model+instance combination already exists ──
    LOOKUP_KEY="${MODEL_HF_ID}|${INSTANCE}"
    if echo "$EXISTING_KEYS" | grep -qF "$LOOKUP_KEY"; then
      echo "  ✓ $MODEL_HF_ID on $INSTANCE — already exists, skipping"
      skipped=$((skipped + 1))
      continue
    fi

    # ── Call the recommendation API ──
    RECOMMEND_URL="${API_URL}/api/v1/recommend?model=$(printf '%s' "$MODEL_HF_ID" | jq -sRr @uri)&instance_type=$INSTANCE"

    RECOMMEND_RESP=$(curl -sf "$RECOMMEND_URL" \
      ${HF_TOKEN:+-H "X-HF-Token: $HF_TOKEN"} 2>&1) || {
        echo "  ✗ $MODEL_HF_ID on $INSTANCE — failed to get recommendation"
        continue
      }

    # Check if feasible.
    FEASIBLE=$(echo "$RECOMMEND_RESP" | jq -r '.explanation.feasible // false')
    if [[ "$FEASIBLE" != "true" ]]; then
      REASON=$(echo "$RECOMMEND_RESP" | jq -r '.explanation.reason // "unknown"')
      echo "  ✗ $MODEL_HF_ID on $INSTANCE — infeasible: $REASON"
      infeasible=$((infeasible + 1))
      continue
    fi

    # Extract recommended values.
    TP=$(echo "$RECOMMEND_RESP" | jq -r '.tensor_parallel_degree')
    QUANT=$(echo "$RECOMMEND_RESP" | jq -r '.quantization // empty')
    MAX_MODEL_LEN=$(echo "$RECOMMEND_RESP" | jq -r '.max_model_len')
    CONCURRENCY=$(echo "$RECOMMEND_RESP" | jq -r '.concurrency')
    INPUT_SEQ=$(echo "$RECOMMEND_RESP" | jq -r '.input_sequence_length')
    OUTPUT_SEQ=$(echo "$RECOMMEND_RESP" | jq -r '.output_sequence_length')

    # Determine framework from instance family.
    FAMILY=$(echo "$INSTANCE" | sed 's/\([a-z]*[0-9]*[a-z]*\)\..*/\1/')
    case "$FAMILY" in
      inf2|trn1|trn2) FRAMEWORK="vllm-neuron";;
      *)              FRAMEWORK="vllm";;
    esac

    echo "  → $MODEL_HF_ID on $INSTANCE (tp=$TP, quant=${QUANT:-none}, concurrency=$CONCURRENCY, max_model_len=$MAX_MODEL_LEN)"

    if [[ "$DRY_RUN" == "true" ]]; then
      continue
    fi

    # Get latest revision from HF API.
    HF_REVISION=$(curl -sf "https://huggingface.co/api/models/${MODEL_HF_ID}" \
      ${HF_TOKEN:+-H "Authorization: Bearer $HF_TOKEN"} \
      | jq -r '.sha // "main"' 2>/dev/null || echo "main")

    # Build the payload with recommended values.
    PAYLOAD=$(jq -n \
      --arg model_hf_id "$MODEL_HF_ID" \
      --arg model_hf_revision "$HF_REVISION" \
      --arg instance_type_name "$INSTANCE" \
      --arg framework "$FRAMEWORK" \
      --arg framework_version "$FW_VERSION" \
      --argjson tensor_parallel_degree "$TP" \
      --argjson concurrency "$CONCURRENCY" \
      --argjson input_sequence_length "$INPUT_SEQ" \
      --argjson output_sequence_length "$OUTPUT_SEQ" \
      --arg dataset_name "$DATASET" \
      --arg scenario_id "$SCENARIO" \
      --argjson min_duration_seconds "$MIN_DURATION" \
      --argjson max_model_len "$MAX_MODEL_LEN" \
      --arg hf_token "$HF_TOKEN" \
      '{
        model_hf_id: $model_hf_id,
        model_hf_revision: $model_hf_revision,
        instance_type_name: $instance_type_name,
        framework: $framework,
        framework_version: $framework_version,
        tensor_parallel_degree: $tensor_parallel_degree,
        concurrency: $concurrency,
        input_sequence_length: $input_sequence_length,
        output_sequence_length: $output_sequence_length,
        dataset_name: $dataset_name,
        scenario_id: $scenario_id,
        min_duration_seconds: $min_duration_seconds,
        run_type: "catalog",
        max_model_len: $max_model_len,
        hf_token: $hf_token
      }')

    # Add quantization if recommended.
    if [[ -n "$QUANT" ]]; then
      PAYLOAD=$(echo "$PAYLOAD" | jq --arg quant "$QUANT" '. + {quantization: $quant}')
    fi

    RESP=$(curl -sf -X POST "$API_URL/api/v1/runs" \
      -H "Content-Type: application/json" \
      -d "$PAYLOAD" 2>&1) || {
        echo "    FAILED: $RESP" >&2
        continue
      }

    RUN_ID=$(echo "$RESP" | jq -r '.id // "unknown"')
    echo "    Submitted: $RUN_ID"
    submitted=$((submitted + 1))
  done
done

echo ""
echo "Done. Submitted: $submitted, Skipped (existing): $skipped, Infeasible: $infeasible"
