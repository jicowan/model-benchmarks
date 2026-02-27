package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/accelbench/accelbench/cmd/cli/client"
	"github.com/accelbench/accelbench/cmd/cli/format"
)

var (
	apiURL       string
	outputFormat string
)

// RootCmd is the top-level CLI command.
var RootCmd = &cobra.Command{
	Use:   "accelbench",
	Short: "AccelBench CLI — benchmark LLMs on AWS accelerated instances",
}

func init() {
	RootCmd.PersistentFlags().StringVar(&apiURL, "api-url", envOrDefault("ACCELBENCH_API_URL", "http://localhost:8080"), "AccelBench API base URL")
	RootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table, json, csv")
}

func newClient() *client.Client {
	return client.New(apiURL)
}

func getFormat() format.OutputFormat {
	switch outputFormat {
	case "json":
		return format.FormatJSON
	case "csv":
		return format.FormatCSV
	default:
		return format.FormatTable
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
