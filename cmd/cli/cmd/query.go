package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/accelbench/accelbench/cmd/cli/format"
	"github.com/accelbench/accelbench/internal/database"
)

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query the benchmark catalog",
	Long: `Query pre-computed benchmark results from the catalog.

Examples:
  accelbench query --model meta-llama/Llama-3.1-70B-Instruct
  accelbench query --instance-family p5 --sort throughput_aggregate --desc
  accelbench query --accelerator-type gpu -o json`,
	RunE: runQuery,
}

var (
	queryModel          string
	queryModelFamily    string
	queryInstanceFamily string
	queryAccelType      string
	querySort           string
	queryDesc           bool
	queryLimit          int
)

func init() {
	queryCmd.Flags().StringVar(&queryModel, "model", "", "Filter by model HuggingFace ID")
	queryCmd.Flags().StringVar(&queryModelFamily, "model-family", "", "Filter by model family (e.g. llama, mistral)")
	queryCmd.Flags().StringVar(&queryInstanceFamily, "instance-family", "", "Filter by instance family (e.g. p5, g6e, inf2)")
	queryCmd.Flags().StringVar(&queryAccelType, "accelerator-type", "", "Filter by accelerator type (gpu or neuron)")
	queryCmd.Flags().StringVar(&querySort, "sort", "", "Sort by column (e.g. throughput_aggregate, ttft_p50, e2e_latency_p50)")
	queryCmd.Flags().BoolVar(&queryDesc, "desc", false, "Sort descending")
	queryCmd.Flags().IntVar(&queryLimit, "limit", 0, "Max results to return")
	RootCmd.AddCommand(queryCmd)
}

func runQuery(cmd *cobra.Command, args []string) error {
	c := newClient()
	filter := database.CatalogFilter{
		ModelHfID:       queryModel,
		ModelFamily:     queryModelFamily,
		InstanceFamily:  queryInstanceFamily,
		AcceleratorType: queryAccelType,
		SortBy:          querySort,
		SortDesc:        queryDesc,
		Limit:           queryLimit,
	}

	entries, err := c.ListCatalog(context.Background(), filter)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "No results found.")
		return nil
	}

	switch getFormat() {
	case format.FormatJSON:
		return format.JSON(entries)
	case format.FormatCSV:
		return format.CSV(os.Stdout, catalogHeaders(), catalogRows(entries))
	default:
		format.Table(catalogHeaders(), catalogRows(entries))
		fmt.Fprintf(os.Stderr, "\n%d result(s)\n", len(entries))
		return nil
	}
}

func catalogHeaders() []string {
	return []string{
		"Model", "Instance", "Accel", "TP",
		"TTFT p50", "TTFT p99", "E2E p50", "E2E p99",
		"ITL p50", "Tput(agg)", "RPS",
	}
}

func catalogRows(entries []database.CatalogEntry) [][]string {
	rows := make([][]string, len(entries))
	for i, e := range entries {
		rows[i] = []string{
			e.ModelHfID,
			e.InstanceTypeName,
			e.AcceleratorName,
			fmt.Sprintf("%d", e.TensorParallelDegree),
			format.PtrF64(e.TTFTP50Ms, 1),
			format.PtrF64(e.TTFTP99Ms, 1),
			format.PtrF64(e.E2ELatencyP50Ms, 1),
			format.PtrF64(e.E2ELatencyP99Ms, 1),
			format.PtrF64(e.ITLP50Ms, 1),
			format.PtrF64(e.ThroughputAggregateTPS, 0),
			format.PtrF64(e.RequestsPerSecond, 2),
		}
	}
	return rows
}
