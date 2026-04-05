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

	return manifest.InferencePerfConfigParams{
		ModelHfID:    modelHfID,
		TargetHost:   targetHost,
		TargetPort:   targetPort,
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
