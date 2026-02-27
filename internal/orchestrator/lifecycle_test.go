package orchestrator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/metrics"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func testRunConfig(runID string) RunConfig {
	return RunConfig{
		RunID: runID,
		Model: &database.Model{
			ID:     "model-001",
			HfID:   "meta-llama/Llama-3.1-8B",
			HfRevision: "abc123",
		},
		InstanceType: &database.InstanceType{
			ID:              "inst-001",
			Name:            "g5.xlarge",
			Family:          "g5",
			AcceleratorType: "gpu",
			AcceleratorName: "A10G",
			AcceleratorCount: 1,
			AcceleratorMemoryGiB: 24,
			VCPUs:           4,
			MemoryGiB:       16,
		},
		Request: &database.RunRequest{
			ModelHfID:            "meta-llama/Llama-3.1-8B",
			ModelHfRevision:      "abc123",
			InstanceTypeName:     "g5.xlarge",
			Framework:            "vllm",
			FrameworkVersion:     "v0.6.0",
			TensorParallelDegree: 1,
			Concurrency:          16,
			InputSequenceLength:  512,
			OutputSequenceLength: 256,
			DatasetName:          "sharegpt",
			RunType:              "on_demand",
		},
	}
}

func TestNew(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := database.NewMockRepo()
	o := New(client, repo)
	if o == nil {
		t.Fatal("New returned nil")
	}
}

func TestDeployModel_CreatesResources(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := database.NewMockRepo()
	o := New(client, repo)

	cfg := testRunConfig("12345678-abcd-1234-abcd-1234567890ab")
	ctx := context.Background()

	err := o.deployModel(ctx, "default", "bench-12345678", cfg)
	if err != nil {
		t.Fatalf("deployModel: %v", err)
	}

	// Verify Deployment was created.
	deps, err := client.AppsV1().Deployments("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(deps.Items) == 0 {
		t.Error("expected at least 1 deployment")
	}
}

func TestWaitForReady_AlreadyReady(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := database.NewMockRepo()
	o := New(client, repo)

	cfg := testRunConfig("12345678-abcd-1234-abcd-1234567890ab")
	ctx := context.Background()

	// Deploy first.
	if err := o.deployModel(ctx, "default", "bench-12345678", cfg); err != nil {
		t.Fatalf("deployModel: %v", err)
	}

	// Simulate readiness by updating the deployment status.
	dep, _ := client.AppsV1().Deployments("default").Get(ctx, "bench-12345678", metav1.GetOptions{})
	dep.Status.ReadyReplicas = 1
	client.AppsV1().Deployments("default").UpdateStatus(ctx, dep, metav1.UpdateOptions{})

	err := o.waitForReady(ctx, "default", "bench-12345678")
	if err != nil {
		t.Fatalf("waitForReady: %v", err)
	}
}

func TestWaitForReady_ContextCancelled(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := database.NewMockRepo()
	o := New(client, repo)

	cfg := testRunConfig("12345678-abcd-1234-abcd-1234567890ab")
	ctx := context.Background()

	if err := o.deployModel(ctx, "default", "bench-12345678", cfg); err != nil {
		t.Fatalf("deployModel: %v", err)
	}

	// Cancel context immediately.
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()

	err := o.waitForReady(cancelCtx, "default", "bench-12345678")
	if err == nil {
		t.Error("expected error when context cancelled")
	}
}

func TestLaunchLoadgen_CreatesJob(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := database.NewMockRepo()
	o := New(client, repo)

	cfg := testRunConfig("12345678-abcd-1234-abcd-1234567890ab")
	ctx := context.Background()

	err := o.launchLoadgen(ctx, "default", "loadgen-12345678", "bench-12345678", cfg)
	if err != nil {
		t.Fatalf("launchLoadgen: %v", err)
	}

	jobs, err := client.BatchV1().Jobs("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) == 0 {
		t.Error("expected at least 1 job")
	}
}

func TestWaitAndCollect_JobFailed(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := database.NewMockRepo()
	o := New(client, repo)

	ctx := context.Background()
	cfg := testRunConfig("12345678-abcd-1234-abcd-1234567890ab")

	// Create the job first.
	if err := o.launchLoadgen(ctx, "default", "loadgen-12345678", "bench-12345678", cfg); err != nil {
		t.Fatalf("launchLoadgen: %v", err)
	}

	// Mark job as failed.
	job, _ := client.BatchV1().Jobs("default").Get(ctx, "loadgen-12345678", metav1.GetOptions{})
	job.Status.Conditions = append(job.Status.Conditions, batchv1.JobCondition{
		Type:    batchv1.JobFailed,
		Status:  corev1.ConditionTrue,
		Message: "OOM killed",
	})
	client.BatchV1().Jobs("default").UpdateStatus(ctx, job, metav1.UpdateOptions{})

	_, err := o.waitAndCollect(ctx, "default", "loadgen-12345678")
	if err == nil {
		t.Fatal("expected error for failed job")
	}
	if got := err.Error(); got != "loadgen job failed: OOM killed" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestTeardown(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := database.NewMockRepo()
	o := New(client, repo)

	ctx := context.Background()
	cfg := testRunConfig("12345678-abcd-1234-abcd-1234567890ab")

	// Deploy resources.
	o.deployModel(ctx, "default", "bench-12345678", cfg)
	o.launchLoadgen(ctx, "default", "loadgen-12345678", "bench-12345678", cfg)

	// Teardown.
	o.teardown(ctx, "default", "bench-12345678", "loadgen-12345678")

	// Verify deployment deleted.
	deps, _ := client.AppsV1().Deployments("default").List(ctx, metav1.ListOptions{})
	if len(deps.Items) != 0 {
		t.Errorf("expected 0 deployments after teardown, got %d", len(deps.Items))
	}

	// Verify job deleted.
	jobs, _ := client.BatchV1().Jobs("default").List(ctx, metav1.ListOptions{})
	if len(jobs.Items) != 0 {
		t.Errorf("expected 0 jobs after teardown, got %d", len(jobs.Items))
	}
}

func TestMarkFailed(t *testing.T) {
	repo := database.NewMockRepo()
	client := fake.NewSimpleClientset()
	o := New(client, repo)

	// Seed a run.
	run := &database.BenchmarkRun{
		ModelID: "m1", InstanceTypeID: "i1",
		Framework: "vllm", FrameworkVersion: "v0.6.0",
		Concurrency: 1, InputSequenceLength: 512,
		OutputSequenceLength: 256, DatasetName: "sharegpt",
		RunType: "on_demand", Status: "pending",
	}
	runID, _ := repo.CreateBenchmarkRun(context.Background(), run)

	o.markFailed(context.Background(), runID)

	if got := repo.GetRunStatus(runID); got != "failed" {
		t.Errorf("status = %s, want failed", got)
	}
}

func TestDerefStr(t *testing.T) {
	if got := derefStr(nil); got != "" {
		t.Errorf("derefStr(nil) = %q, want empty", got)
	}
	s := "hello"
	if got := derefStr(&s); got != "hello" {
		t.Errorf("derefStr(&hello) = %q, want hello", got)
	}
}

func TestLaunchLoadgen_HighConcurrency(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := database.NewMockRepo()
	o := New(client, repo)

	cfg := testRunConfig("12345678-abcd-1234-abcd-1234567890ab")
	cfg.Request.Concurrency = 64 // > 32, should use concurrency*10 = 640

	ctx := context.Background()
	err := o.launchLoadgen(ctx, "default", "loadgen-hi", "bench-12345678", cfg)
	if err != nil {
		t.Fatalf("launchLoadgen: %v", err)
	}
}

// makeLoadgenJSON creates valid loadgen output JSON for testing.
func makeLoadgenJSON() []byte {
	out := metrics.LoadgenOutput{
		Requests: []metrics.RequestResult{
			{TTFTMs: 10, E2ELatencyMs: 100, ITLMs: 5, OutputTokens: 50, InputTokens: 20, DurationSeconds: 1.0, Success: true},
		},
		Summary: metrics.Summary{
			TotalDurationSeconds:   5.0,
			TotalRequests:          1,
			SuccessfulRequests:     1,
			FailedRequests:         0,
			ThroughputAggregateTPS: 10.0,
			RequestsPerSecond:      0.2,
		},
	}
	data, _ := json.Marshal(out)
	return data
}

func TestDeployModel_LargeInstance(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := database.NewMockRepo()
	o := New(client, repo)

	cfg := testRunConfig("12345678-abcd-1234-abcd-1234567890ab")
	cfg.InstanceType.MemoryGiB = 512 // > 256, should use larger CPU/mem

	ctx := context.Background()
	err := o.deployModel(ctx, "default", "bench-large", cfg)
	if err != nil {
		t.Fatalf("deployModel: %v", err)
	}
}

func TestDeployModel_NeuronInstance(t *testing.T) {
	client := fake.NewSimpleClientset()
	repo := database.NewMockRepo()
	o := New(client, repo)

	cfg := testRunConfig("12345678-abcd-1234-abcd-1234567890ab")
	cfg.InstanceType.AcceleratorType = "neuron"
	cfg.InstanceType.Family = "inf2"
	cfg.Request.Framework = "vllm-neuron"

	ctx := context.Background()
	err := o.deployModel(ctx, "default", "bench-neuron", cfg)
	if err != nil {
		t.Fatalf("deployModel: %v", err)
	}
}

// Suppress log output during tests.
func init() {
	_ = time.Now // ensure time is imported
}
