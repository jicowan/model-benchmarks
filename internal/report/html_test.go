package report

import (
	"strings"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"
)

func TestGenerateRunReport(t *testing.T) {
	now := time.Now()
	quant := "bfloat16"
	ttft := 45.5
	e2e := 1200.0
	itl := 12.5
	tps := 150.0
	rps := 2.5
	util := 85.0
	mem := 70.5
	success := 100
	failed := 0
	dur := 60.0

	run := &database.BenchmarkRun{
		ID:                   "test-run-123",
		Framework:            "vllm",
		FrameworkVersion:     "v0.4.0",
		TensorParallelDegree: 8,
		Quantization:         &quant,
		Concurrency:          10,
		InputSequenceLength:  256,
		OutputSequenceLength: 128,
		DatasetName:          "synthetic",
		RunType:              "benchmark",
		Status:               "completed",
		CompletedAt:          &now,
	}

	metrics := &database.BenchmarkMetrics{
		TTFTP50Ms:                 &ttft,
		TTFTP90Ms:                 &ttft,
		TTFTP99Ms:                 &ttft,
		E2ELatencyP50Ms:           &e2e,
		E2ELatencyP90Ms:           &e2e,
		E2ELatencyP99Ms:           &e2e,
		ITLP50Ms:                  &itl,
		ITLP90Ms:                  &itl,
		ITLP99Ms:                  &itl,
		ThroughputAggregateTPS:    &tps,
		RequestsPerSecond:         &rps,
		AcceleratorUtilizationPct: &util,
		AcceleratorMemoryPeakGiB:  &mem,
		SuccessfulRequests:        &success,
		FailedRequests:            &failed,
		TotalDurationSeconds:      &dur,
	}

	details := &database.RunExportDetails{
		RunID:                "test-run-123",
		ModelHfID:            "meta-llama/Llama-3.1-70B-Instruct",
		InstanceTypeName:     "p5.48xlarge",
		Framework:            "vllm",
		FrameworkVersion:     "v0.4.0",
		TensorParallelDegree: 8,
		Quantization:         &quant,
		MaxModelLen:          32768,
	}

	hourly := 31.46
	html, err := GenerateRunReport(run, metrics, details, &hourly)
	if err != nil {
		t.Fatalf("GenerateRunReport failed: %v", err)
	}

	// Check that the HTML contains expected content
	content := string(html)

	checks := []string{
		"<!DOCTYPE html>",
		"meta-llama/Llama-3.1-70B-Instruct",
		"p5.48xlarge",
		"TP Degree",
		"45.5",  // TTFT
		"150.0", // Throughput
		"AccelBench",
	}

	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("HTML report missing expected content: %q", check)
		}
	}

	// Check that HTML is self-contained (has embedded styles)
	if !strings.Contains(content, "<style>") {
		t.Error("HTML report should have embedded styles")
	}
}

func TestFormatFloat_Nil(t *testing.T) {
	result := formatFloat(nil)
	if result != "—" {
		t.Errorf("formatFloat(nil) = %q, want %q", result, "—")
	}
}

func TestFormatFloat_Value(t *testing.T) {
	v := 123.456
	result := formatFloat(&v)
	if result != "123.5" {
		t.Errorf("formatFloat(123.456) = %q, want %q", result, "123.5")
	}
}

func TestFormatInt_Nil(t *testing.T) {
	result := formatInt(nil)
	if result != "—" {
		t.Errorf("formatInt(nil) = %q, want %q", result, "—")
	}
}

func TestDeref(t *testing.T) {
	v := 42.5
	if deref(&v) != 42.5 {
		t.Error("deref should return value")
	}
	if deref(nil) != 0 {
		t.Error("deref(nil) should return 0")
	}
}
