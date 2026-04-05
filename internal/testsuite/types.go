package testsuite

// TestSuite defines a collection of scenarios to run sequentially.
type TestSuite struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Scenarios     []string `json:"scenarios"`      // scenario IDs in execution order
	TotalDuration int      `json:"total_duration"` // estimated total duration in seconds
}

// SuiteRunStatus represents the status of a test suite run.
type SuiteRunStatus string

const (
	SuiteStatusPending   SuiteRunStatus = "pending"
	SuiteStatusRunning   SuiteRunStatus = "running"
	SuiteStatusCompleted SuiteRunStatus = "completed"
	SuiteStatusFailed    SuiteRunStatus = "failed"
)

// ScenarioStatus represents the status of a scenario within a suite run.
type ScenarioStatus string

const (
	ScenarioStatusPending   ScenarioStatus = "pending"
	ScenarioStatusRunning   ScenarioStatus = "running"
	ScenarioStatusCompleted ScenarioStatus = "completed"
	ScenarioStatusFailed    ScenarioStatus = "failed"
	ScenarioStatusSkipped   ScenarioStatus = "skipped"
)
