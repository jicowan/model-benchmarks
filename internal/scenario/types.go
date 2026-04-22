package scenario

import "github.com/accelbench/accelbench/internal/manifest"

// Scenario defines a benchmark workload pattern.
type Scenario struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	LoadType    string       `json:"load_type"`    // "constant" or "poisson"
	Stages      []LoadStage  `json:"stages"`       // rate and duration for each stage
	NumWorkers  int          `json:"num_workers"`
	Input       Distribution `json:"input_distribution"`
	Output      Distribution `json:"output_distribution"`
	Streaming   bool         `json:"streaming"`
	Dataset     string       `json:"dataset"` // "synthetic" or "sharegpt"
}

// LoadStage represents a load generation stage.
type LoadStage struct {
	Rate     int `json:"rate"`     // requests per second
	Duration int `json:"duration"` // seconds
}

// Distribution defines token count distribution parameters.
type Distribution struct {
	Mean   int `json:"mean"`
	StdDev int `json:"std_dev"`
	Min    int `json:"min"`
	Max    int `json:"max"`
}

// TotalDuration returns the sum of all stage durations in seconds.
func (s *Scenario) TotalDuration() int {
	total := 0
	for _, stage := range s.Stages {
		total += stage.Duration
	}
	return total
}

// TargetQPS returns the target requests per second for this scenario.
// For multi-stage scenarios, returns the final (peak) stage rate.
func (s *Scenario) TargetQPS() int {
	if len(s.Stages) == 0 {
		return 0
	}
	return s.Stages[len(s.Stages)-1].Rate
}

// ToInferencePerfConfig converts a Scenario to InferencePerfConfigParams.
// modelHfID and targetHost/Port are provided by the orchestrator.
func (s *Scenario) ToInferencePerfConfig(modelHfID, targetHost string, targetPort int) manifest.InferencePerfConfigParams {
	// Convert LoadStage to manifest.LoadStage
	stages := make([]manifest.LoadStage, len(s.Stages))
	for i, stage := range s.Stages {
		stages[i] = manifest.LoadStage{
			Rate:     stage.Rate,
			Duration: stage.Duration,
		}
	}

	apiType := inferAPIType(s.Dataset)

	return manifest.InferencePerfConfigParams{
		ModelHfID:    modelHfID,
		TargetHost:   targetHost,
		TargetPort:   targetPort,
		APIType:     apiType,
		Streaming:    s.Streaming,
		DatasetType:  s.Dataset,
		InputMean:    s.Input.Mean,
		InputStdDev:  s.Input.StdDev,
		InputMin:     s.Input.Min,
		InputMax:     s.Input.Max,
		OutputMean:   s.Output.Mean,
		OutputStdDev: s.Output.StdDev,
		OutputMin:    s.Output.Min,
		OutputMax:    s.Output.Max,
		LoadType:     s.LoadType,
		Stages:       stages,
		NumWorkers:   s.NumWorkers,
	}
}

// inferAPIType returns the appropriate API type based on dataset.
// Synthetic/random data has no chat structure, so use completion.
// Real datasets (sharegpt, etc.) have conversation format, so use chat.
func inferAPIType(dataset string) string {
	switch dataset {
	case "synthetic", "random":
		return "completion"
	default:
		return "chat"
	}
}

// Override is the subset of a Scenario that operators can tune from the
// Configuration page (PRD-32). All fields are pointers so nil means
// "inherit from the code-defined scenario".
type Override struct {
	NumWorkers *int
	Streaming  *bool
	InputMean  *int
	OutputMean *int
}

// Merge returns a copy of s with any non-nil fields from ov applied.
// When input_mean / output_mean are overridden, the std_dev/min/max
// bounds are re-derived from the new mean using the same formula the
// built-in scenarios use (std_dev = mean/4, min = mean/2, max = mean*2).
// This keeps the distribution bounds internally consistent with the
// new mean, and avoids giving operators four tightly-coupled knobs per
// dimension.
func (s *Scenario) Merge(ov *Override) *Scenario {
	out := *s // copy
	if ov == nil {
		return &out
	}
	if ov.NumWorkers != nil {
		out.NumWorkers = *ov.NumWorkers
	}
	if ov.Streaming != nil {
		out.Streaming = *ov.Streaming
	}
	if ov.InputMean != nil {
		out.Input = deriveDistribution(*ov.InputMean)
	}
	if ov.OutputMean != nil {
		out.Output = deriveDistribution(*ov.OutputMean)
	}
	return &out
}

func deriveDistribution(mean int) Distribution {
	if mean < 1 {
		mean = 1
	}
	return Distribution{
		Mean:   mean,
		StdDev: mean / 4,
		Min:    mean / 2,
		Max:    mean * 2,
	}
}
