package orchestrator

import "fmt"

// PRD-68 P6: Kubernetes resource naming.
//
// Names used to embed runID[:8] — 32 bits of the UUID. The collision odds
// among concurrently live runs are small (~N²/2³³) but a collision fails the
// run with AlreadyExists and, worse, teardown of run A would delete run B's
// Deployment. 12 characters (44 bits) puts that below 1e-9 at 100 live runs
// while keeping names short enough for the 63-char label/DNS limits with the
// per-scenario suffixes the suite path appends.
//
// Every generated object also carries the FULL run id in the
// LabelRunID label so the leak reconciler can map objects back to rows
// without parsing names (see reconciler.go).

const (
	// LabelRunID carries the full benchmark_runs / test_suite_runs id.
	LabelRunID = "accelbench/run-id"
	// LabelRole is the existing role label (model | loadgen | loadgen-config |
	// cache-job | distributed-lock).
	LabelRole = "accelbench/role"
	// labelSuiteRunID is the legacy per-suite selector used by
	// CleanupSuiteResources. It was referenced but never SET before PRD-68,
	// so suite loadgen cleanup matched nothing; the loadgen template now
	// emits it.
	labelSuiteRunID = "suite-run-id"

	nameSuffixLen = 12
)

// resourceSuffix returns the run-id fragment embedded in resource names.
func resourceSuffix(id string) string {
	if len(id) > nameSuffixLen {
		return id[:nameSuffixLen]
	}
	return id
}

// Single-run resource names.
func modelNameFor(runID string) string     { return "bench-" + resourceSuffix(runID) }
func loadgenNameFor(runID string) string   { return "loadgen-" + resourceSuffix(runID) }
func loadgenCMNameFor(runID string) string { return "loadgen-config-" + resourceSuffix(runID) }

// Suite resource names. Scenario ids are short lower-case words; four
// characters disambiguates every shipped scenario.
func suiteModelNameFor(suiteRunID string) string { return "suite-" + resourceSuffix(suiteRunID) }
func suiteLoadgenNameFor(suiteRunID, scenarioID string) string {
	return fmt.Sprintf("loadgen-%s-%s", resourceSuffix(suiteRunID), scenarioPrefix(scenarioID))
}
func suiteLoadgenCMNameFor(suiteRunID, scenarioID string) string {
	return fmt.Sprintf("loadgen-config-%s-%s", resourceSuffix(suiteRunID), scenarioPrefix(scenarioID))
}

func scenarioPrefix(id string) string {
	if len(id) > 4 {
		return id[:4]
	}
	return id
}
