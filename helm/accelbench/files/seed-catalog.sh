#!/usr/bin/env bash
# seed-catalog.sh — Read catalog-matrix.yaml and submit benchmark runs via the API.
#
# Usage:
#   ./scripts/seed-catalog.sh [--api-url URL] [--hf-token TOKEN] [--dry-run]
#
# Requires: yq (https://github.com/mikefarah/yq)

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

# Read defaults.
FW_VERSION=$(yq '.defaults.framework_version' "$MATRIX_FILE")
CONCURRENCY=$(yq '.defaults.concurrency' "$MATRIX_FILE")
INPUT_SEQ=$(yq '.defaults.input_sequence_length' "$MATRIX_FILE")
OUTPUT_SEQ=$(yq '.defaults.output_sequence_length' "$MATRIX_FILE")
DATASET=$(yq '.defaults.dataset_name' "$MATRIX_FILE")

NUM_MODELS=$(yq '.models | length' "$MATRIX_FILE")
NUM_INSTANCES=$(yq '.instance_configs | length' "$MATRIX_FILE")

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
EXISTING_KEYS=$(echo "$EXISTING_RUNS" | yq -r '.[] | .model_hf_id + "|" + .instance_type_name' 2>/dev/null || echo "")
EXISTING_COUNT=$(echo "$EXISTING_KEYS" | grep -c . 2>/dev/null || echo "0")
echo "  Found $EXISTING_COUNT existing runs."
echo ""

submitted=0
skipped=0

for mi in $(seq 0 $((NUM_MODELS - 1))); do
  MODEL_HF_ID=$(yq ".models[$mi].hf_id" "$MATRIX_FILE")
  PARAM_COUNT=$(yq ".models[$mi].parameter_count" "$MATRIX_FILE")

  for ii in $(seq 0 $((NUM_INSTANCES - 1))); do
    INSTANCE=$(yq ".instance_configs[$ii].instance" "$MATRIX_FILE")
    TP=$(yq ".instance_configs[$ii].tp" "$MATRIX_FILE")
    MIN_PARAMS=$(yq ".instance_configs[$ii].min_params // 0" "$MATRIX_FILE")
    MAX_PARAMS=$(yq ".instance_configs[$ii].max_params // 999999999999" "$MATRIX_FILE")

    # Check if model fits this instance config.
    if [[ "$PARAM_COUNT" -lt "$MIN_PARAMS" ]] || [[ "$PARAM_COUNT" -gt "$MAX_PARAMS" ]]; then
      continue
    fi

    # Determine framework from instance family.
    FAMILY=$(echo "$INSTANCE" | sed 's/\([a-z]*[0-9]*[a-z]*\)\..*/\1/')
    case "$FAMILY" in
      inf2|trn1|trn2) FRAMEWORK="vllm-neuron";;
      *)              FRAMEWORK="vllm";;
    esac

    # ── Skip if this model+instance combination already exists ──
    LOOKUP_KEY="${MODEL_HF_ID}|${INSTANCE}"
    if echo "$EXISTING_KEYS" | grep -qF "$LOOKUP_KEY"; then
      echo "  ✓ $MODEL_HF_ID on $INSTANCE — already exists, skipping"
      skipped=$((skipped + 1))
      continue
    fi

    echo "  → $MODEL_HF_ID on $INSTANCE (tp=$TP, framework=$FRAMEWORK)"

    if [[ "$DRY_RUN" == "true" ]]; then
      skipped=$((skipped + 1))
      continue
    fi

    # Get latest revision from HF API.
    HF_REVISION=$(curl -sf "https://huggingface.co/api/models/${MODEL_HF_ID}" \
      ${HF_TOKEN:+-H "Authorization: Bearer $HF_TOKEN"} \
      | yq -r '.sha // "main"' 2>/dev/null || echo "main")

    PAYLOAD=$(cat <<EOF
{
  "model_hf_id": "$MODEL_HF_ID",
  "model_hf_revision": "$HF_REVISION",
  "instance_type_name": "$INSTANCE",
  "framework": "$FRAMEWORK",
  "framework_version": "$FW_VERSION",
  "tensor_parallel_degree": $TP,
  "concurrency": $CONCURRENCY,
  "input_sequence_length": $INPUT_SEQ,
  "output_sequence_length": $OUTPUT_SEQ,
  "dataset_name": "$DATASET",
  "run_type": "catalog",
  "hf_token": "$HF_TOKEN"
}
EOF
    )

    RESP=$(curl -sf -X POST "$API_URL/api/v1/runs" \
      -H "Content-Type: application/json" \
      -d "$PAYLOAD" 2>&1) || {
        echo "    FAILED: $RESP" >&2
        continue
      }

    RUN_ID=$(echo "$RESP" | yq -r '.id // "unknown"')
    echo "    Submitted: $RUN_ID"
    submitted=$((submitted + 1))
  done
done

echo ""
echo "Done. Submitted: $submitted, Skipped (existing): $skipped"
