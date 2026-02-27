package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"
)

func setupTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	// Point CLI at the test server.
	apiURL = srv.URL
	return srv
}

func captureOutput(t *testing.T, cmd func() error) (string, error) {
	t.Helper()
	old := RootCmd.OutOrStdout()
	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	err := cmd()
	RootCmd.SetOut(old)
	return buf.String(), err
}

func TestQueryCommand_Table(t *testing.T) {
	ttft := 15.0
	tput := 1200.0
	entries := []database.CatalogEntry{
		{
			RunID:                  "run-1",
			ModelHfID:              "meta-llama/Llama-3.1-70B",
			InstanceTypeName:       "p5.48xlarge",
			AcceleratorName:        "H100",
			TensorParallelDegree:   8,
			TTFTP50Ms:              &ttft,
			ThroughputAggregateTPS: &tput,
		},
	}

	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	}))

	outputFormat = "table"
	queryModel = "meta-llama/Llama-3.1-70B"
	queryModelFamily = ""
	queryInstanceFamily = ""
	queryAccelType = ""
	querySort = ""
	queryDesc = false
	queryLimit = 0

	err := runQuery(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueryCommand_JSON(t *testing.T) {
	entries := []database.CatalogEntry{
		{
			RunID:            "run-1",
			ModelHfID:        "test/model",
			InstanceTypeName: "g6e.12xlarge",
			AcceleratorName:  "L40S",
		},
	}

	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	}))

	outputFormat = "json"
	queryModel = ""

	err := runQuery(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueryCommand_NoResults(t *testing.T) {
	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]database.CatalogEntry{})
	}))

	outputFormat = "table"
	queryModel = "nonexistent"

	err := runQuery(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunCommand(t *testing.T) {
	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var req database.RunRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.ModelHfID != "test/model" {
			t.Errorf("unexpected model: %s", req.ModelHfID)
		}
		if req.InstanceTypeName != "p5.48xlarge" {
			t.Errorf("unexpected instance: %s", req.InstanceTypeName)
		}
		if req.Concurrency != 16 {
			t.Errorf("expected concurrency 16, got %d", req.Concurrency)
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"id": "run-new", "status": "pending"})
	}))

	outputFormat = "table"
	runModel = "test/model"
	runRevision = "main"
	runInstance = "p5.48xlarge"
	runFramework = "vllm"
	runFrameworkVer = "latest"
	runTP = 8
	runConcurrency = 16
	runInputSeqLen = 1024
	runOutputSeqLen = 512
	runDataset = "sharegpt"
	runQuantization = ""

	err := runBenchmark(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStatusCommand_Completed(t *testing.T) {
	now := time.Now()
	ttft := 12.0
	tput := 800.0
	succ := 100

	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/metrics"):
			json.NewEncoder(w).Encode(database.BenchmarkMetrics{
				RunID:                  "run-done",
				TTFTP50Ms:              &ttft,
				ThroughputAggregateTPS: &tput,
				SuccessfulRequests:     &succ,
			})
		default:
			json.NewEncoder(w).Encode(database.BenchmarkRun{
				ID:          "run-done",
				Status:      "completed",
				Framework:   "vllm",
				FrameworkVersion: "0.4.0",
				Concurrency: 8,
				TensorParallelDegree: 4,
				StartedAt:   &now,
				CompletedAt: &now,
				CreatedAt:   now,
			})
		}
	}))

	outputFormat = "table"
	err := runStatus(nil, []string{"run-done"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStatusCommand_Running(t *testing.T) {
	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(database.BenchmarkRun{
			ID:        "run-active",
			Status:    "running",
			Framework: "vllm",
			CreatedAt: time.Now(),
		})
	}))

	outputFormat = "table"
	err := runStatus(nil, []string{"run-active"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStatusCommand_JSON(t *testing.T) {
	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/metrics") {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}
		json.NewEncoder(w).Encode(database.BenchmarkRun{
			ID:     "run-1",
			Status: "pending",
			CreatedAt: time.Now(),
		})
	}))

	outputFormat = "json"
	err := runStatus(nil, []string{"run-1"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCompareCommand(t *testing.T) {
	ttft1 := 10.0
	ttft2 := 20.0
	entries := []database.CatalogEntry{
		{RunID: "r1", ModelHfID: "model/a", InstanceTypeName: "p5.48xlarge", AcceleratorName: "H100", TTFTP50Ms: &ttft1},
		{RunID: "r2", ModelHfID: "model/a", InstanceTypeName: "g6e.48xlarge", AcceleratorName: "L40S", TTFTP50Ms: &ttft2},
		{RunID: "r3", ModelHfID: "model/a", InstanceTypeName: "inf2.48xlarge", AcceleratorName: "Inferentia2"},
	}

	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	}))

	outputFormat = "table"
	compareModel = "model/a"
	compareInstances = "p5.48xlarge,g6e.48xlarge"

	err := runCompare(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCompareCommand_NoFilter(t *testing.T) {
	entries := []database.CatalogEntry{
		{RunID: "r1", InstanceTypeName: "p5.48xlarge"},
		{RunID: "r2", InstanceTypeName: "g6e.48xlarge"},
	}
	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	}))

	outputFormat = "table"
	compareModel = "model/a"
	compareInstances = ""

	err := runCompare(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExportCommand_JSON(t *testing.T) {
	entries := []database.CatalogEntry{
		{RunID: "r1", ModelHfID: "test/m", InstanceTypeName: "p5.48xlarge"},
	}
	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	}))

	outputFormat = "json"
	exportModel = ""
	exportInstFamily = ""
	exportFile = ""

	err := runExport(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExportCommand_CSV(t *testing.T) {
	entries := []database.CatalogEntry{
		{RunID: "r1", ModelHfID: "test/m", InstanceTypeName: "p5.48xlarge", Framework: "vllm", FrameworkVersion: "0.4"},
	}
	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	}))

	outputFormat = "csv"
	exportModel = ""
	exportInstFamily = ""
	exportFile = ""

	err := runExport(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExportCommand_NoResults(t *testing.T) {
	setupTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]database.CatalogEntry{})
	}))

	outputFormat = "json"
	exportModel = "nonexistent"

	err := runExport(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}
