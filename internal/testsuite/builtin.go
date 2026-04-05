package testsuite

// BuiltinSuites contains the predefined test suite configurations.
var BuiltinSuites = map[string]TestSuite{
	"quick": {
		ID:            "quick",
		Name:          "Quick Validation",
		Description:   "Fast validation with chatbot scenario",
		Scenarios:     []string{"chatbot"},
		TotalDuration: 120, // 2 min
	},
	"throughput": {
		ID:            "throughput",
		Name:          "Throughput Test",
		Description:   "Batch processing throughput test",
		Scenarios:     []string{"batch"},
		TotalDuration: 120, // 2 min
	},
	"stress": {
		ID:            "stress",
		Name:          "Stress Test",
		Description:   "Ramping load to find saturation point",
		Scenarios:     []string{"stress"},
		TotalDuration: 240, // 4 min
	},
	"standard": {
		ID:            "standard",
		Name:          "Standard Characterization",
		Description:   "Balanced test covering chat, batch, and production workloads",
		Scenarios:     []string{"chatbot", "batch", "production"},
		TotalDuration: 420, // 7 min (2 + 2 + 3)
	},
	"comprehensive": {
		ID:            "comprehensive",
		Name:          "Comprehensive Characterization",
		Description:   "Full characterization across all workload types",
		Scenarios:     []string{"chatbot", "batch", "stress", "production", "long-context"},
		TotalDuration: 780, // 13 min (2 + 2 + 4 + 3 + 2)
	},
}

// List returns all built-in test suites as a slice, sorted by duration.
func List() []TestSuite {
	order := []string{"quick", "throughput", "stress", "standard", "comprehensive"}
	result := make([]TestSuite, 0, len(order))
	for _, id := range order {
		if s, ok := BuiltinSuites[id]; ok {
			result = append(result, s)
		}
	}
	return result
}

// Get returns a test suite by ID, or nil if not found.
func Get(id string) *TestSuite {
	if s, ok := BuiltinSuites[id]; ok {
		return &s
	}
	return nil
}
