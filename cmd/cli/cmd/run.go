package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/accelbench/accelbench/cmd/cli/format"
	"github.com/accelbench/accelbench/internal/database"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Submit an on-demand benchmark run",
	Long: `Submit a new benchmark run against a model on a specific instance type.

Examples:
  accelbench run --model meta-llama/Llama-3.1-70B-Instruct --instance p5.48xlarge --concurrency 16
  accelbench run --model mistralai/Mixtral-8x7B-Instruct-v0.1 --instance g6e.12xlarge --tp 4`,
	RunE: runBenchmark,
}

var (
	runModel         string
	runRevision      string
	runInstance      string
	runFramework     string
	runFrameworkVer  string
	runTP            int
	runQuantization  string
	runConcurrency   int
	runInputSeqLen   int
	runOutputSeqLen  int
	runDataset       string
)

func init() {
	runCmd.Flags().StringVar(&runModel, "model", "", "Model HuggingFace ID (required)")
	runCmd.Flags().StringVar(&runRevision, "revision", "main", "Model HuggingFace revision")
	runCmd.Flags().StringVar(&runInstance, "instance", "", "Instance type name (required)")
	runCmd.Flags().StringVar(&runFramework, "framework", "vllm", "Serving framework (vllm or vllm-neuron)")
	runCmd.Flags().StringVar(&runFrameworkVer, "framework-version", "latest", "Framework version")
	runCmd.Flags().IntVar(&runTP, "tp", 1, "Tensor parallel degree")
	runCmd.Flags().StringVar(&runQuantization, "quantization", "", "Quantization method (e.g. fp16, int8, int4)")
	runCmd.Flags().IntVar(&runConcurrency, "concurrency", 1, "Concurrent request count")
	runCmd.Flags().IntVar(&runInputSeqLen, "input-seq-len", 1024, "Input sequence length")
	runCmd.Flags().IntVar(&runOutputSeqLen, "output-seq-len", 512, "Output sequence length")
	runCmd.Flags().StringVar(&runDataset, "dataset", "sharegpt", "Dataset name")
	_ = runCmd.MarkFlagRequired("model")
	_ = runCmd.MarkFlagRequired("instance")
	RootCmd.AddCommand(runCmd)
}

func runBenchmark(cmd *cobra.Command, args []string) error {
	c := newClient()

	req := database.RunRequest{
		ModelHfID:            runModel,
		ModelHfRevision:      runRevision,
		InstanceTypeName:     runInstance,
		Framework:            runFramework,
		FrameworkVersion:     runFrameworkVer,
		TensorParallelDegree: runTP,
		Concurrency:          runConcurrency,
		InputSequenceLength:  runInputSeqLen,
		OutputSequenceLength: runOutputSeqLen,
		DatasetName:          runDataset,
		RunType:              "on_demand",
	}
	if runQuantization != "" {
		req.Quantization = &runQuantization
	}

	id, status, err := c.CreateRun(context.Background(), req)
	if err != nil {
		return err
	}

	switch getFormat() {
	case format.FormatJSON:
		return format.JSON(map[string]string{"id": id, "status": status})
	default:
		fmt.Printf("Run submitted: %s (status: %s)\n", id, status)
		fmt.Printf("Track progress: accelbench status %s\n", id)
		return nil
	}
}
