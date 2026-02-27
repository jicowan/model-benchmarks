package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/accelbench/accelbench/cmd/cli/format"
	"github.com/accelbench/accelbench/internal/database"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export benchmark results to JSON or CSV",
	Long: `Export catalog benchmark results in JSON or CSV format.

By default exports to stdout. Use --file to write to a file.

Examples:
  accelbench export -o json > results.json
  accelbench export -o csv --file results.csv
  accelbench export --model meta-llama/Llama-3.1-70B-Instruct -o csv`,
	RunE: runExport,
}

var (
	exportModel     string
	exportInstFamily string
	exportFile      string
)

func init() {
	exportCmd.Flags().StringVar(&exportModel, "model", "", "Filter by model HuggingFace ID")
	exportCmd.Flags().StringVar(&exportInstFamily, "instance-family", "", "Filter by instance family")
	exportCmd.Flags().StringVar(&exportFile, "file", "", "Output file path (default: stdout)")
	RootCmd.AddCommand(exportCmd)
}

func runExport(cmd *cobra.Command, args []string) error {
	c := newClient()

	entries, err := c.ListCatalog(context.Background(), database.CatalogFilter{
		ModelHfID:      exportModel,
		InstanceFamily: exportInstFamily,
		Limit:          500,
	})
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "No results to export.")
		return nil
	}

	// Determine output destination.
	out := os.Stdout
	if exportFile != "" {
		f, err := os.Create(exportFile)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer f.Close()
		out = f
	}

	switch getFormat() {
	case format.FormatCSV:
		return format.CSV(out, exportHeaders(), exportRows(entries))
	default:
		// Default to JSON for export.
		return format.JSONTo(out, entries)
	}
}

func exportHeaders() []string {
	return []string{
		"run_id", "model", "model_family", "instance_type", "instance_family",
		"accelerator_type", "accelerator_name", "accelerator_count", "accelerator_memory_gib",
		"framework", "framework_version", "tensor_parallel_degree", "quantization",
		"concurrency", "input_seq_len", "output_seq_len",
		"ttft_p50_ms", "ttft_p99_ms", "e2e_latency_p50_ms", "e2e_latency_p99_ms",
		"itl_p50_ms", "itl_p99_ms",
		"throughput_per_request_tps", "throughput_aggregate_tps", "requests_per_second",
		"accelerator_utilization_pct", "accelerator_memory_peak_gib",
	}
}

func exportRows(entries []database.CatalogEntry) [][]string {
	rows := make([][]string, len(entries))
	for i, e := range entries {
		modelFamily := ""
		if e.ModelFamily != nil {
			modelFamily = *e.ModelFamily
		}
		quant := ""
		if e.Quantization != nil {
			quant = *e.Quantization
		}
		rows[i] = []string{
			e.RunID,
			e.ModelHfID,
			modelFamily,
			e.InstanceTypeName,
			e.InstanceFamily,
			e.AcceleratorType,
			e.AcceleratorName,
			fmt.Sprintf("%d", e.AcceleratorCount),
			fmt.Sprintf("%d", e.AcceleratorMemoryGiB),
			e.Framework,
			e.FrameworkVersion,
			fmt.Sprintf("%d", e.TensorParallelDegree),
			quant,
			fmt.Sprintf("%d", e.Concurrency),
			fmt.Sprintf("%d", e.InputSequenceLength),
			fmt.Sprintf("%d", e.OutputSequenceLength),
			format.PtrF64(e.TTFTP50Ms, 2),
			format.PtrF64(e.TTFTP99Ms, 2),
			format.PtrF64(e.E2ELatencyP50Ms, 2),
			format.PtrF64(e.E2ELatencyP99Ms, 2),
			format.PtrF64(e.ITLP50Ms, 2),
			format.PtrF64(e.ITLP99Ms, 2),
			format.PtrF64(e.ThroughputPerRequestTPS, 2),
			format.PtrF64(e.ThroughputAggregateTPS, 2),
			format.PtrF64(e.RequestsPerSecond, 2),
			format.PtrF64(e.AcceleratorUtilizationPct, 2),
			format.PtrF64(e.AcceleratorMemoryPeakGiB, 2),
		}
	}
	return rows
}
