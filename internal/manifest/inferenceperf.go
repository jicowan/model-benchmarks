package manifest

// InferencePerfConfigParams holds values for rendering the inference-perf config YAML.
type InferencePerfConfigParams struct {
	// Server settings
	ModelHfID  string // HuggingFace ID (also used for tokenizer)
	ModelName  string // Model name for API requests (S3 URI when loading from S3; empty = use ModelHfID)
	TargetHost string
	TargetPort int

	// API settings
	APIType   string // "chat_completion" (default) or "completion"
	Streaming bool

	// Data distribution settings
	DatasetType  string // "synthetic" or "sharegpt"
	InputMean    int
	InputStdDev  int
	InputMin     int
	InputMax     int
	OutputMean   int
	OutputStdDev int
	OutputMin    int
	OutputMax    int

	// Load settings
	LoadType   string      // "constant" or "poisson"
	Stages     []LoadStage // rate and duration for each stage
	NumWorkers int
}

// LoadStage represents a load generation stage with rate and duration.
type LoadStage struct {
	Rate     int // requests per second
	Duration int // seconds
}

// RenderInferencePerfConfig renders the inference-perf configuration YAML.
// Translates the internal dataset identifier to the exact casing inference-perf's
// pydantic enum expects (e.g. "sharegpt" → "shareGPT"). The translation happens
// only at this boundary; the rest of the codebase keeps the snake_case form.
func RenderInferencePerfConfig(params InferencePerfConfigParams) (string, error) {
	params.DatasetType = toInferencePerfDatasetType(params.DatasetType)
	return renderTemplate("inferenceperf-config.yaml.tmpl", params)
}

// toInferencePerfDatasetType maps our internal dataset IDs to the exact enum
// values inference-perf accepts. Unknown values pass through unchanged so new
// datasets added upstream don't require a code change here unless their casing
// differs from ours.
func toInferencePerfDatasetType(ds string) string {
	switch ds {
	case "sharegpt":
		return "shareGPT"
	default:
		return ds
	}
}

// NewDefaultInferencePerfConfig creates a default config for a single-stage constant load.
// This provides sensible defaults for the "chatbot" scenario.
func NewDefaultInferencePerfConfig(modelHfID, targetHost string, targetPort int) InferencePerfConfigParams {
	return InferencePerfConfigParams{
		ModelHfID:    modelHfID,
		TargetHost:   targetHost,
		TargetPort:   targetPort,
		Streaming:    true,
		DatasetType:  "synthetic",
		InputMean:    256,
		InputStdDev:  64,
		InputMin:     128,
		InputMax:     512,
		OutputMean:   128,
		OutputStdDev: 32,
		OutputMin:    64,
		OutputMax:    256,
		LoadType:     "constant",
		Stages:       []LoadStage{{Rate: 5, Duration: 120}},
		NumWorkers:   4,
	}
}
