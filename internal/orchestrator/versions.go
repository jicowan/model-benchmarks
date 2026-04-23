package orchestrator

import (
	"context"
	"fmt"
	"os"
)

// inferencePerfImageRepo is the registry+repo portion of the inference-perf
// image URI. The tag comes from the DB tool_versions row.
const inferencePerfImageRepo = "quay.io/inference-perf/inference-perf"

// resolveInferencePerfImage returns the full image URI for the loadgen Job.
// Precedence:
//  1. INFERENCE_PERF_IMAGE env var (full URI, used as-is) — local-dev escape
//     hatch. When set, Configuration UI shows a warning banner so operators
//     know UI edits have no effect.
//  2. DB tool_versions.inference_perf_version — composed into
//     "quay.io/inference-perf/inference-perf:<tag>".
//
// There is no hardcoded fallback; the migration guarantees the DB row exists.
func (o *Orchestrator) resolveInferencePerfImage(ctx context.Context) (string, error) {
	if envImage := os.Getenv("INFERENCE_PERF_IMAGE"); envImage != "" {
		return envImage, nil
	}
	tv, err := o.repo.GetToolVersions(ctx)
	if err != nil {
		return "", fmt.Errorf("load tool_versions: %w", err)
	}
	return fmt.Sprintf("%s:%s", inferencePerfImageRepo, tv.InferencePerfVersion), nil
}
