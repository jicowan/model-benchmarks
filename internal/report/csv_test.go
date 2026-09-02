package report

import (
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"
)

func floatP(v float64) *float64 { return &v }
func intP(v int) *int           { return &v }

func TestGenerateRunCSV_AllMetricsPresent(t *testing.T) {
	run := &database.BenchmarkRun{
		ID:                   "run-123",
		Framework:            "vllm",
		FrameworkVersion:     "v0.6.0",
		TensorParallelDegree: 2,
		Concurrency:          16,
		InputSequenceLength:  512,
		OutputSequenceLength: 256,
		DatasetName:          "sharegpt",
		Status:               "completed",
		TotalCostUSD:         floatP(1.23),
	}
	metrics := &database.BenchmarkMetrics{
		TTFTP50Ms:              floatP(42.0),
		TTFTP95Ms:              floatP(80.0),
		ThroughputAggregateTPS: floatP(1234.5),
		SuccessfulRequests:     intP(1000),
		FailedRequests:         intP(0),
	}
	details := &database.RunExportDetails{
		ModelHfID:        "meta-llama/Llama-3.1-8B",
		InstanceTypeName: "g5.xlarge",
		AcceleratorType:  "gpu",
		AcceleratorName:  "A10G",
		AcceleratorCount: 1,
	}
	hourly := floatP(3.06)

	data, err := GenerateRunCSV(run, metrics, details, hourly)
	if err != nil {
		t.Fatalf("GenerateRunCSV: %v", err)
	}
	s := string(data)
	for _, want := range []string{
		"run_id,run-123",
		"model_hf_id,meta-llama/Llama-3.1-8B",
		"instance_type,g5.xlarge",
		"framework,vllm",
		"ttft_p50_ms,42.00",
		"throughput_aggregate_tps,1234.50",
		"successful_requests,1000",
		"hourly_usd,3.0600",
		"total_cost_usd,1.2300",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("CSV missing %q; got:\n%s", want, s)
		}
	}
}

func TestGenerateRunCSV_NilMetricsRenderAsEmpty(t *testing.T) {
	run := &database.BenchmarkRun{
		ID: "run-456", Framework: "vllm", FrameworkVersion: "v0.6.0",
		TensorParallelDegree: 1, Concurrency: 8,
		InputSequenceLength: 256, OutputSequenceLength: 128,
		DatasetName: "sharegpt", Status: "failed",
	}
	data, err := GenerateRunCSV(run, nil, nil, nil)
	if err != nil {
		t.Fatalf("GenerateRunCSV: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "run_id,run-456") {
		t.Error("missing run_id")
	}
	// No metrics fields should appear at all (since metrics is nil).
	if strings.Contains(s, "ttft_p50_ms") {
		t.Error("nil metrics should not emit metric rows")
	}
	// hourly_usd row is always emitted (even with nil value).
	if !strings.Contains(s, "hourly_usd,") {
		t.Error("hourly_usd row should always be present")
	}
}

func TestGenerateSuiteCSV_OneRowPerScenario(t *testing.T) {
	suite := &database.TestSuiteRun{ID: "suite-1", SuiteID: "standard"}
	model := &database.Model{HfID: "meta-llama/Llama-3.1-8B"}
	instance := &database.InstanceType{Name: "g5.xlarge"}
	results := []database.ScenarioResult{
		{
			ScenarioID: "steady-low", Status: "completed",
			TTFTP50Ms: floatP(30.0), ThroughputTPS: floatP(100.0),
			SuccessfulRequests: intP(500),
		},
		{
			ScenarioID: "steady-medium", Status: "completed",
			TTFTP50Ms: floatP(50.0), ThroughputTPS: floatP(400.0),
			SuccessfulRequests: intP(2000),
		},
	}
	data, err := GenerateSuiteCSV(suite, results, model, instance)
	if err != nil {
		t.Fatalf("GenerateSuiteCSV: %v", err)
	}
	s := string(data)

	// Header comment
	if !strings.Contains(s, "# suite_run_id: suite-1") {
		t.Error("missing suite_run_id comment")
	}
	if !strings.Contains(s, "# model: meta-llama/Llama-3.1-8B") {
		t.Error("missing model comment")
	}

	// Column header
	if !strings.Contains(s, "scenario_id,scenario_name") {
		t.Error("missing column header")
	}

	// Data rows
	lines := strings.Split(strings.TrimSpace(s), "\n")
	dataLineCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "steady-") {
			dataLineCount++
		}
	}
	if dataLineCount != 2 {
		t.Errorf("expected 2 scenario rows, got %d (csv:\n%s)", dataLineCount, s)
	}
}

func TestGenerateSuiteCSV_FailedScenarioHasEmptyMetrics(t *testing.T) {
	suite := &database.TestSuiteRun{ID: "suite-x"}
	results := []database.ScenarioResult{
		{ScenarioID: "failing", Status: "failed"},
	}
	data, err := GenerateSuiteCSV(suite, results, nil, nil)
	if err != nil {
		t.Fatalf("GenerateSuiteCSV: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "failing") {
		t.Error("failed scenario should still appear in CSV")
	}
	if !strings.Contains(s, ",failed,") {
		t.Error("status column should contain 'failed'")
	}
}

// Compare CSV regression — confirm the refactor didn't break it.
func TestGenerateCompareCSV_SmokeTest(t *testing.T) {
	entries := []database.CatalogEntry{
		{
			RunID:                "run-a",
			ModelHfID:            "meta-llama/Llama-3.1-8B",
			InstanceTypeName:     "g5.xlarge",
			AcceleratorCount:     1,
			Framework:            "vllm",
			FrameworkVersion:     "v0.6.0",
			TensorParallelDegree: 1,
			Concurrency:          16,
			InputSequenceLength:  512,
			OutputSequenceLength: 256,
			TTFTP50Ms:            floatP(42.0),
			ThroughputAggregateTPS: floatP(1000.0),
			RequestsPerSecond:    floatP(16.0),
		},
	}
	hourly := func(_ string) *float64 { v := 3.06; return &v }
	data, err := GenerateCompareCSV(entries, hourly, "on-demand", "us-east-2")
	if err != nil {
		t.Fatalf("GenerateCompareCSV: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "run-a") {
		t.Error("missing run_id")
	}
	if !strings.Contains(s, "ttft_p50_ms") {
		t.Error("missing ttft header")
	}
}

// TestGenerateCompareCSV_CPUvsGPU (PRD-67 §12): a CPU row (accelerator_count=0,
// no DCGM metrics) compared against a GPU row. The cross-tier signals (cost,
// aggregate throughput, TTFT) must populate for the CPU row, while the
// GPU-centric columns (per-GPU throughput, DCGM sm/tensor/dram active) render
// BLANK (not 0) — no divide-by-zero, and the CPU row isn't implied to have
// "failed" those metrics.
func TestGenerateCompareCSV_CPUvsGPU(t *testing.T) {
	entries := []database.CatalogEntry{
		{
			RunID: "cpu-run", ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct",
			InstanceTypeName: "r8g.16xlarge", AcceleratorCount: 0, // CPU: no discrete accelerator
			Framework: "vllm-cpu", FrameworkVersion: "v0.27.0", TensorParallelDegree: 1,
			Concurrency: 16, InputSequenceLength: 512, OutputSequenceLength: 256,
			TTFTP50Ms: floatP(217.0), ThroughputAggregateTPS: floatP(586.0), RequestsPerSecond: floatP(5.0),
			// DCGM fields intentionally nil (never scraped on CPU).
		},
		{
			RunID: "gpu-run", ModelHfID: "Qwen/Qwen2.5-1.5B-Instruct",
			InstanceTypeName: "g6.2xlarge", AcceleratorCount: 1,
			Framework: "vllm", FrameworkVersion: "v0.19.0", TensorParallelDegree: 1,
			Concurrency: 16, InputSequenceLength: 512, OutputSequenceLength: 256,
			TTFTP50Ms: floatP(45.0), ThroughputAggregateTPS: floatP(632.0), RequestsPerSecond: floatP(5.0),
			SMActiveAvgPct: floatP(88.0),
		},
	}
	// Distinct hourly rates so cost columns are computable for both tiers.
	hourly := func(name string) *float64 {
		v := map[string]float64{"r8g.16xlarge": 3.85, "g6.2xlarge": 0.98}[name]
		return &v
	}
	data, err := GenerateCompareCSV(entries, hourly, "on-demand", "us-east-2")
	if err != nil {
		t.Fatalf("GenerateCompareCSV: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	// Find the CPU row (starts with cpu-run) and split its columns.
	var cpuCols, hdr []string
	for _, ln := range lines {
		cols := strings.Split(ln, ",")
		if len(cols) > 0 && cols[0] == "run_id" {
			hdr = cols
		}
		if len(cols) > 0 && cols[0] == "cpu-run" {
			cpuCols = cols
		}
	}
	if hdr == nil || cpuCols == nil {
		t.Fatalf("missing header or cpu-run row; got:\n%s", string(data))
	}
	col := func(name string) string {
		for i, h := range hdr {
			if h == name && i < len(cpuCols) {
				return cpuCols[i]
			}
		}
		t.Fatalf("no such column %q", name)
		return ""
	}
	// Cross-tier signals populate for CPU.
	if col("throughput_tps") != "586.00" {
		t.Errorf("CPU throughput_tps = %q, want 586.00", col("throughput_tps"))
	}
	if col("cost_per_1m_tokens_usd") == "" {
		t.Error("CPU cost_per_1m_tokens_usd must populate (has hourly + throughput)")
	}
	if col("ttft_p50_ms") != "217.00" {
		t.Errorf("CPU ttft_p50_ms = %q, want 217.00", col("ttft_p50_ms"))
	}
	// GPU-centric columns render BLANK for the CPU row (not 0).
	if col("throughput_per_gpu_tps") != "" {
		t.Errorf("CPU throughput_per_gpu_tps = %q, want blank (count 0)", col("throughput_per_gpu_tps"))
	}
	if col("sm_active_avg_pct") != "" {
		t.Errorf("CPU sm_active_avg_pct = %q, want blank (no DCGM on CPU)", col("sm_active_avg_pct"))
	}
	if col("accelerator_count") != "0" {
		t.Errorf("CPU accelerator_count = %q, want 0", col("accelerator_count"))
	}
}
