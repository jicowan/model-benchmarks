package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/accelbench/accelbench/cmd/cli/format"
)

var statusCmd = &cobra.Command{
	Use:   "status <run-id>",
	Short: "Check the status of a benchmark run",
	Long: `Fetch the current status and metrics (if available) for a benchmark run.

Examples:
  accelbench status abc12345-6789-0000-1111-222233334444
  accelbench status abc12345 -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runStatus,
}

func init() {
	RootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	c := newClient()
	runID := args[0]

	run, err := c.GetRun(context.Background(), runID)
	if err != nil {
		return err
	}

	if getFormat() == format.FormatJSON {
		// Include metrics in JSON output if available.
		metrics, _ := c.GetMetrics(context.Background(), runID)
		return format.JSON(map[string]any{
			"run":     run,
			"metrics": metrics,
		})
	}

	// Table output.
	fmt.Printf("Run ID:     %s\n", run.ID)
	fmt.Printf("Status:     %s\n", run.Status)
	fmt.Printf("Framework:  %s %s\n", run.Framework, run.FrameworkVersion)
	fmt.Printf("TP Degree:  %d\n", run.TensorParallelDegree)
	fmt.Printf("Concurrency: %d\n", run.Concurrency)
	if run.Quantization != nil {
		fmt.Printf("Quantization: %s\n", *run.Quantization)
	}
	if run.StartedAt != nil {
		fmt.Printf("Started:    %s\n", run.StartedAt.Format("2006-01-02 15:04:05 UTC"))
	}
	if run.CompletedAt != nil {
		fmt.Printf("Completed:  %s\n", run.CompletedAt.Format("2006-01-02 15:04:05 UTC"))
	}

	if run.Status != "completed" {
		return nil
	}

	// Show key metrics if run is complete.
	metrics, err := c.GetMetrics(context.Background(), runID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: could not fetch metrics:", err)
		return nil
	}

	fmt.Println("\nKey Metrics:")
	format.Table(
		[]string{"Metric", "Value"},
		[][]string{
			{"TTFT p50", format.PtrF64(metrics.TTFTP50Ms, 1) + " ms"},
			{"TTFT p99", format.PtrF64(metrics.TTFTP99Ms, 1) + " ms"},
			{"E2E Latency p50", format.PtrF64(metrics.E2ELatencyP50Ms, 1) + " ms"},
			{"E2E Latency p99", format.PtrF64(metrics.E2ELatencyP99Ms, 1) + " ms"},
			{"ITL p50", format.PtrF64(metrics.ITLP50Ms, 1) + " ms"},
			{"Throughput (agg)", format.PtrF64(metrics.ThroughputAggregateTPS, 0) + " tok/s"},
			{"Throughput (per-req)", format.PtrF64(metrics.ThroughputPerRequestTPS, 1) + " tok/s"},
			{"Requests/sec", format.PtrF64(metrics.RequestsPerSecond, 2) + " rps"},
			{"GPU Utilization", format.PtrF64(metrics.AcceleratorUtilizationPct, 0) + " %"},
			{"Peak Memory", format.PtrF64(metrics.AcceleratorMemoryPeakGiB, 1) + " GiB"},
			{"Successful Requests", format.Ptr(metrics.SuccessfulRequests, "%d")},
			{"Failed Requests", format.Ptr(metrics.FailedRequests, "%d")},
			{"Duration", format.PtrF64(metrics.TotalDurationSeconds, 1) + " s"},
		},
	)
	return nil
}
