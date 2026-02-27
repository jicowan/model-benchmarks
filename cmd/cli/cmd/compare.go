package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/accelbench/accelbench/cmd/cli/format"
	"github.com/accelbench/accelbench/internal/database"
)

var compareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Compare benchmark results across instance types",
	Long: `Compare benchmark results for a model across multiple instance types.

Examples:
  accelbench compare --model meta-llama/Llama-3.1-70B-Instruct --instances p5.48xlarge,g6e.48xlarge
  accelbench compare --model meta-llama/Llama-3.1-70B-Instruct -o json`,
	RunE: runCompare,
}

var (
	compareModel     string
	compareInstances string
)

func init() {
	compareCmd.Flags().StringVar(&compareModel, "model", "", "Model HuggingFace ID (required)")
	compareCmd.Flags().StringVar(&compareInstances, "instances", "", "Comma-separated instance type names to compare")
	_ = compareCmd.MarkFlagRequired("model")
	RootCmd.AddCommand(compareCmd)
}

func runCompare(cmd *cobra.Command, args []string) error {
	c := newClient()

	// Fetch catalog entries for the model.
	entries, err := c.ListCatalog(context.Background(), database.CatalogFilter{
		ModelHfID: compareModel,
		Limit:     500,
	})
	if err != nil {
		return err
	}

	// Filter to requested instances if specified.
	if compareInstances != "" {
		wanted := make(map[string]bool)
		for _, inst := range strings.Split(compareInstances, ",") {
			wanted[strings.TrimSpace(inst)] = true
		}
		var filtered []database.CatalogEntry
		for _, e := range entries {
			if wanted[e.InstanceTypeName] {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "No matching results found for comparison.")
		return nil
	}

	switch getFormat() {
	case format.FormatJSON:
		return format.JSON(entries)
	case format.FormatCSV:
		return format.CSV(os.Stdout, compareHeaders(), compareRows(entries))
	default:
		format.Table(compareHeaders(), compareRows(entries))
		fmt.Fprintf(os.Stderr, "\n%d configuration(s) compared\n", len(entries))
		return nil
	}
}

func compareHeaders() []string {
	return []string{
		"Instance", "Accel", "TP", "Quant",
		"TTFT p50", "E2E p50", "ITL p50",
		"Tput(agg)", "Tput(req)", "RPS",
		"GPU%", "Mem(GiB)",
	}
}

func compareRows(entries []database.CatalogEntry) [][]string {
	rows := make([][]string, len(entries))
	for i, e := range entries {
		quant := "-"
		if e.Quantization != nil {
			quant = *e.Quantization
		}
		rows[i] = []string{
			e.InstanceTypeName,
			e.AcceleratorName,
			fmt.Sprintf("%d", e.TensorParallelDegree),
			quant,
			format.PtrF64(e.TTFTP50Ms, 1),
			format.PtrF64(e.E2ELatencyP50Ms, 1),
			format.PtrF64(e.ITLP50Ms, 1),
			format.PtrF64(e.ThroughputAggregateTPS, 0),
			format.PtrF64(e.ThroughputPerRequestTPS, 1),
			format.PtrF64(e.RequestsPerSecond, 2),
			format.PtrF64(e.AcceleratorUtilizationPct, 0),
			format.PtrF64(e.AcceleratorMemoryPeakGiB, 1),
		}
	}
	return rows
}
