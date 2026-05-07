package orchestrator

import (
	"fmt"
	"math"
)

// maxLoadgenWorkers mirrors the UI cap enforced in the Configuration
// page's scenario-override form. Anything above this is treated as a
// clamp rather than a user intent; we still accept the value but size
// the container as if it were this cap, to avoid accidentally
// requesting multi-hundred-CPU loadgen pods.
const maxLoadgenWorkers = 128

// loadgenResources derives CPU and memory requests/limits for the
// inference-perf container based on its configured num_workers.
//
// The loadgen is async-HTTP driven and mostly IO-bound, but each worker
// still burns ~0.25 CPU at steady state for tokenization + aiohttp
// bookkeeping. A 2 CPU / 4 GiB request floor matches the historical
// defaults (num_workers=4 → cpu req=2). Limits are 2x requests with a
// 4 CPU / 8 GiB floor, matching the previous hardcoded limit shape.
//
// Memory scales linearly on top of the 4 GiB baseline at 100 MiB per
// worker — conservative; real per-worker growth is closer to the
// tokenizer cache + one HTTP connection pool. Linear is fine for
// scheduling purposes.
//
// Returned strings are already in the form Kubernetes expects on
// `resources.requests` / `resources.limits`.
func loadgenResources(numWorkers int) (cpuReq, cpuLim, memReq, memLim string) {
	w := numWorkers
	if w > maxLoadgenWorkers {
		w = maxLoadgenWorkers
	}
	if w < 1 {
		w = 1
	}

	cpuReqN := int(math.Ceil(float64(w) * 0.25))
	if cpuReqN < 2 {
		cpuReqN = 2
	}
	cpuLimN := cpuReqN * 2
	if cpuLimN < 4 {
		cpuLimN = 4
	}

	const baselineMiB = 4 * 1024
	const perWorkerMiB = 100
	memReqMi := baselineMiB + w*perWorkerMiB
	if memReqMi < baselineMiB {
		memReqMi = baselineMiB
	}
	memLimMi := memReqMi * 2

	return fmt.Sprintf("%d", cpuReqN),
		fmt.Sprintf("%d", cpuLimN),
		fmt.Sprintf("%dMi", memReqMi),
		fmt.Sprintf("%dMi", memLimMi)
}
