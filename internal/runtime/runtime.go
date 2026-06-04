package runtime

// Runtime encapsulates all per-framework behavior so the orchestrator,
// manifest renderer, and API validation layers never branch on a framework
// string.
type Runtime interface {
	Name() string
	ContainerName() string
	SupportedAccelerators() []string
	ResolveImageOverride() string
	DefaultImage(version, pullThroughRegistry string) string
	ResolveVersion(tv ToolVersions) string
	BuildArgs(p ContainerParams) (command []string, args []string)
	MapQuantization(quant string, useRunaiStreamer bool) []string
}

// ToolVersions is a minimal projection of database.ToolVersions to avoid
// importing the database package.
type ToolVersions struct {
	FrameworkVersion string
	SGLangVersion    string
}

// ContainerParams carries the knobs that BuildArgs needs.
type ContainerParams struct {
	ModelHfID              string
	ModelS3URI             string
	UseRunaiStreamer        bool
	TensorParallelDegree   int
	MaxModelLen            int
	MaxNumBatchedTokens    int
	MaxNumSeqs             int
	KVCacheDtype           string
	Quantization           string
	StreamerConcurrency    int
	StreamerMemoryLimitGiB int
	// SGLang-specific knobs.
	ChunkedPrefillSize  int
	MemFractionStatic   float64
	// AcceleratorName is the GPU model (e.g. "L4", "A10G", "H100"). Used to
	// select GPU-architecture-appropriate launch flags such as the SGLang
	// attention backend.
	AcceleratorName string
}
