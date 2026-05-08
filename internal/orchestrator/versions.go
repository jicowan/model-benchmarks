package orchestrator

import (
	"context"
	"fmt"
	"os"
)

// resolveInferencePerfImage returns the full image URI for the loadgen Job.
// Precedence:
//  1. INFERENCE_PERF_IMAGE env var (full URI, used as-is) — the local-dev
//     escape hatch and the deployment's primary wiring. In production,
//     Helm sets this to our ECR fork (see docker/Dockerfile.inference-perf):
//     upstream inference-perf plus sentencepiece, needed for Mistral /
//     older-Llama / T5-family SentencePiece tokenizers that fail loadgen
//     otherwise. When set, Configuration UI shows a warning banner so
//     operators know UI edits to the version tag have no effect.
//  2. DB tool_versions.inference_perf_version — composed into the upstream
//     quay.io URI as a fallback for dev clusters that haven't pushed our
//     fork yet.
//
// There is no hardcoded default tag; the migration guarantees the DB row
// exists.
func (o *Orchestrator) resolveInferencePerfImage(ctx context.Context) (string, error) {
	if envImage := os.Getenv("INFERENCE_PERF_IMAGE"); envImage != "" {
		return envImage, nil
	}
	tv, err := o.repo.GetToolVersions(ctx)
	if err != nil {
		return "", fmt.Errorf("load tool_versions: %w", err)
	}
	return fmt.Sprintf("quay.io/inference-perf/inference-perf:%s", tv.InferencePerfVersion), nil
}
