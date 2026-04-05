package scenario

// BuiltinScenarios contains the predefined benchmark scenarios.
var BuiltinScenarios = map[string]Scenario{
	"chatbot": {
		ID:          "chatbot",
		Name:        "Chatbot",
		Description: "Interactive chat - low latency, short exchanges, streaming",
		LoadType:    "constant",
		Stages:      []LoadStage{{Rate: 5, Duration: 120}},
		NumWorkers:  4,
		Input:       Distribution{Mean: 256, StdDev: 64, Min: 128, Max: 512},
		Output:      Distribution{Mean: 128, StdDev: 32, Min: 64, Max: 256},
		Streaming:   true,
		Dataset:     "synthetic",
	},
	"batch": {
		ID:          "batch",
		Name:        "Batch Processing",
		Description: "High throughput offline processing",
		LoadType:    "constant",
		Stages:      []LoadStage{{Rate: 20, Duration: 120}},
		NumWorkers:  8,
		Input:       Distribution{Mean: 512, StdDev: 128, Min: 256, Max: 1024},
		Output:      Distribution{Mean: 512, StdDev: 128, Min: 256, Max: 1024},
		Streaming:   false,
		Dataset:     "synthetic",
	},
	"stress": {
		ID:          "stress",
		Name:        "Stress Test",
		Description: "Ramping load to find saturation point",
		LoadType:    "constant",
		Stages: []LoadStage{
			{Rate: 2, Duration: 60},
			{Rate: 5, Duration: 60},
			{Rate: 10, Duration: 60},
			{Rate: 20, Duration: 60},
		},
		NumWorkers: 8,
		Input:      Distribution{Mean: 256, StdDev: 64, Min: 128, Max: 512},
		Output:     Distribution{Mean: 128, StdDev: 32, Min: 64, Max: 256},
		Streaming:  true,
		Dataset:    "synthetic",
	},
	"production": {
		ID:          "production",
		Name:        "Production Traffic",
		Description: "Realistic mixed traffic with Poisson arrivals",
		LoadType:    "poisson",
		Stages:      []LoadStage{{Rate: 10, Duration: 180}},
		NumWorkers:  8,
		Input:       Distribution{Mean: 512, StdDev: 256, Min: 128, Max: 2048},
		Output:      Distribution{Mean: 256, StdDev: 128, Min: 64, Max: 1024},
		Streaming:   true,
		Dataset:     "synthetic",
	},
	"long-context": {
		ID:          "long-context",
		Name:        "Long Context (RAG)",
		Description: "Long prompts for RAG and document processing",
		LoadType:    "constant",
		Stages:      []LoadStage{{Rate: 3, Duration: 120}},
		NumWorkers:  4,
		Input:       Distribution{Mean: 4096, StdDev: 1024, Min: 2048, Max: 8192},
		Output:      Distribution{Mean: 256, StdDev: 64, Min: 128, Max: 512},
		Streaming:   true,
		Dataset:     "synthetic",
	},
}

// List returns all built-in scenarios as a slice, sorted by typical use order.
func List() []Scenario {
	order := []string{"chatbot", "batch", "stress", "production", "long-context"}
	result := make([]Scenario, 0, len(order))
	for _, id := range order {
		if s, ok := BuiltinScenarios[id]; ok {
			result = append(result, s)
		}
	}
	return result
}

// Get returns a scenario by ID, or nil if not found.
func Get(id string) *Scenario {
	if s, ok := BuiltinScenarios[id]; ok {
		return &s
	}
	return nil
}
