package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/manifest"
	"github.com/accelbench/accelbench/internal/metrics"
	"github.com/accelbench/accelbench/internal/testsuite"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExecuteSuite runs all scenarios in a test suite sequentially.
// The model is deployed once and reused for all scenarios.
func (o *Orchestrator) ExecuteSuite(ctx context.Context, suiteRunID string, req database.SuiteRunRequest) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Register the cancel function so CancelRun can stop this suite.
	o.mu.Lock()
	o.cancels[suiteRunID] = cancel
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.cancels, suiteRunID)
		o.mu.Unlock()
	}()

	suite := testsuite.Get(req.SuiteID)
	if suite == nil {
		log.Printf("[suite %s] unknown suite: %s", suiteRunID[:8], req.SuiteID)
		return
	}

	// Get scenario results to update
	results, err := o.repo.GetScenarioResults(ctx, suiteRunID)
	if err != nil {
		log.Printf("[suite %s] failed to get scenario results: %v", suiteRunID[:8], err)
		return
	}

	// Build a map of scenario ID to result ID
	resultMap := make(map[string]string)
	for _, r := range results {
		resultMap[r.ScenarioID] = r.ID
	}

	// Mark suite as running
	firstScenario := suite.Scenarios[0]
	if err := o.repo.UpdateSuiteRunStatus(ctx, suiteRunID, "running", &firstScenario); err != nil {
		log.Printf("[suite %s] failed to update status: %v", suiteRunID[:8], err)
		return
	}

	log.Printf("[suite %s] starting suite %q with %d scenarios", suiteRunID[:8], suite.Name, len(suite.Scenarios))

	// Get model and instance type for deployment
	suiteRun, err := o.repo.GetTestSuiteRun(ctx, suiteRunID)
	if err != nil || suiteRun == nil {
		log.Printf("[suite %s] failed to get suite run: %v", suiteRunID[:8], err)
		o.repo.UpdateSuiteRunStatus(ctx, suiteRunID, "failed", nil)
		o.persistSuiteCost(ctx, suiteRunID)
		return
	}

	ns := defaultNamespace
	modelName := fmt.Sprintf("suite-%s", suiteRunID[:8])

	// Build config for model deployment
	model, _ := o.repo.GetModelByHfID(ctx, req.ModelHfID, req.ModelHfRevision)
	instType, _ := o.repo.GetInstanceTypeByName(ctx, req.InstanceTypeName)
	if model == nil || instType == nil {
		log.Printf("[suite %s] model or instance type not found", suiteRunID[:8])
		o.repo.UpdateSuiteRunStatus(ctx, suiteRunID, "failed", nil)
		o.persistSuiteCost(ctx, suiteRunID)
		return
	}

	cfg := RunConfig{
		RunID:        suiteRunID,
		Model:        model,
		InstanceType: instType,
		Request: &database.RunRequest{
			ModelHfID:            req.ModelHfID,
			ModelHfRevision:      req.ModelHfRevision,
			InstanceTypeName:     req.InstanceTypeName,
			Framework:            req.Framework,
			FrameworkVersion:     req.FrameworkVersion,
			TensorParallelDegree: req.TensorParallelDegree,
			Quantization:         req.Quantization,
			MaxModelLen:          req.MaxModelLen,
			ModelS3URI:           req.ModelS3URI,
			HfToken:              req.HfToken,
		},
	}

	// Deploy model once for all scenarios
	log.Printf("[suite %s] deploying model %s on %s", suiteRunID[:8], req.ModelHfID, req.InstanceTypeName)
	if err := o.deployModel(ctx, ns, modelName, cfg); err != nil {
		log.Printf("[suite %s] failed to deploy model: %v", suiteRunID[:8], err)
		o.repo.UpdateSuiteRunStatus(ctx, suiteRunID, "failed", nil)
		o.persistSuiteCost(ctx, suiteRunID)
		return
	}
	defer o.teardownSuite(context.Background(), ns, modelName, suiteRunID)

	// Wait for model readiness
	log.Printf("[suite %s] waiting for model readiness", suiteRunID[:8])
	if err := o.waitForReady(ctx, ns, modelName, cfg); err != nil {
		log.Printf("[suite %s] model not ready: %v", suiteRunID[:8], err)
		o.repo.UpdateSuiteRunStatus(ctx, suiteRunID, "failed", nil)
		o.persistSuiteCost(ctx, suiteRunID)
		return
	}

	// GPU scraper config for per-scenario metrics
	isGPU := strings.EqualFold(instType.AcceleratorType, "gpu")
	totalMemGiB := float64(instType.AcceleratorMemoryGiB)
	var nodeIP string
	if isGPU {
		nodeIP = o.getModelPodNodeIP(ctx, ns, modelName)
		if nodeIP != "" {
			log.Printf("[suite %s] DCGM scraping enabled (node %s)", suiteRunID[:8], nodeIP)
		}
	}

	// Execute each scenario sequentially
	for i, scenarioID := range suite.Scenarios {
		resultID := resultMap[scenarioID]

		// Update current scenario
		if err := o.repo.UpdateSuiteRunStatus(ctx, suiteRunID, "running", &scenarioID); err != nil {
			log.Printf("[suite %s] failed to update current scenario: %v", suiteRunID[:8], err)
		}

		log.Printf("[suite %s] running scenario %d/%d: %s", suiteRunID[:8], i+1, len(suite.Scenarios), scenarioID)

		// Mark scenario as running
		o.repo.UpdateScenarioResult(ctx, &database.ScenarioResult{ID: resultID, Status: "running"})

		// Start GPU scraper for this scenario
		var gpuScraper *GPUScraper
		if isGPU {
			gpuScraper = NewGPUScraperWithDCGM(modelName, 8000, totalMemGiB, nodeIP)
			gpuScraper.Start(ctx)
		}

		// Run the scenario
		computed, configYAML, err := o.runScenario(ctx, ns, modelName, suiteRunID, scenarioID, cfg)

		// Stop GPU scraper and collect metrics for this scenario
		var gpuMetrics *GPUMetrics
		if gpuScraper != nil {
			gpuMetrics = gpuScraper.Stop()
			if gpuMetrics != nil {
				log.Printf("[suite %s] scenario %s GPU metrics: util=%.1f%% mem=%.1fGiB",
					suiteRunID[:8], scenarioID, gpuMetrics.UtilizationPeakPct, gpuMetrics.MemoryPeakGiB)
			}
		}

		if err != nil {
			log.Printf("[suite %s] scenario %s failed: %v", suiteRunID[:8], scenarioID, err)
			errMsg := err.Error()
			o.repo.UpdateScenarioResult(ctx, &database.ScenarioResult{
				ID:           resultID,
				Status:       "failed",
				ErrorMessage: &errMsg,
			})
			continue
		}

		// Build scenario result with metrics
		result := &database.ScenarioResult{
			ID:                 resultID,
			Status:             "completed",
			TTFTP50Ms:          computed.TTFTP50Ms,
			TTFTP90Ms:          computed.TTFTP90Ms,
			TTFTP95Ms:          computed.TTFTP95Ms,
			TTFTP99Ms:          computed.TTFTP99Ms,
			E2ELatencyP50Ms:    computed.E2ELatencyP50Ms,
			E2ELatencyP90Ms:    computed.E2ELatencyP90Ms,
			E2ELatencyP95Ms:    computed.E2ELatencyP95Ms,
			E2ELatencyP99Ms:    computed.E2ELatencyP99Ms,
			ITLP50Ms:           computed.ITLP50Ms,
			ITLP90Ms:           computed.ITLP90Ms,
			ITLP95Ms:           computed.ITLP95Ms,
			ITLP99Ms:           computed.ITLP99Ms,
			TPOTP50Ms:          computed.TPOTP50Ms,
			TPOTP90Ms:          computed.TPOTP90Ms,
			TPOTP99Ms:          computed.TPOTP99Ms,
			ThroughputTPS:      computed.ThroughputAggregateTPS,
			RequestsPerSecond:  computed.RequestsPerSecond,
			SuccessfulRequests: computed.SuccessfulRequests,
			FailedRequests:     computed.FailedRequests,
			LoadgenConfig:      &configYAML,
		}

		// Merge GPU metrics if available
		if gpuMetrics != nil {
			result.AcceleratorUtilizationPct = &gpuMetrics.UtilizationPeakPct
			result.AcceleratorUtilizationAvgPct = &gpuMetrics.UtilizationAvgPct
			result.AcceleratorMemoryPeakGiB = &gpuMetrics.MemoryPeakGiB
			result.AcceleratorMemoryAvgGiB = &gpuMetrics.MemoryAvgGiB
			result.WaitingRequestsMax = &gpuMetrics.WaitingRequestsMax
			// PRD-22: DCP metrics — pointers pass through nil when DCP unavailable.
			result.SMActiveAvgPct = gpuMetrics.SMActiveAvgPct
			result.SMActivePeakPct = gpuMetrics.SMActivePeakPct
			result.TensorActiveAvgPct = gpuMetrics.TensorActiveAvgPct
			result.TensorActivePeakPct = gpuMetrics.TensorActivePeakPct
			result.DRAMActiveAvgPct = gpuMetrics.DRAMActiveAvgPct
			result.DRAMActivePeakPct = gpuMetrics.DRAMActivePeakPct
		}

		o.repo.UpdateScenarioResult(ctx, result)

		log.Printf("[suite %s] scenario %s completed", suiteRunID[:8], scenarioID)
	}

	// Mark suite as completed
	o.repo.UpdateSuiteRunStatus(ctx, suiteRunID, "completed", nil)
	// PRD-35: freeze the suite's cost using its own started_at→completed_at
	// window (all scenarios share one EC2 node, so this is the full billable
	// lifetime).
	o.persistSuiteCost(ctx, suiteRunID)
	log.Printf("[suite %s] suite completed", suiteRunID[:8])
}

// runScenario executes a single scenario and returns metrics.
func (o *Orchestrator) runScenario(ctx context.Context, ns, modelSvc, suiteRunID, scenarioID string, cfg RunConfig) (*database.BenchmarkMetrics, string, error) {
	s := o.resolveScenario(ctx, scenarioID)
	if s == nil {
		return nil, "", fmt.Errorf("unknown scenario: %s", scenarioID)
	}

	loadgenName := fmt.Sprintf("loadgen-%s-%s", suiteRunID[:8], scenarioID[:4])
	configMapName := fmt.Sprintf("loadgen-config-%s-%s", suiteRunID[:8], scenarioID[:4])

	// Generate inference-perf config from scenario
	inferencePerfConfig := s.ToInferencePerfConfig(cfg.Request.ModelHfID, modelSvc, 8000)

	// When loading from S3, vLLM registers the model with the S3 URI as its name
	if cfg.Request.ModelS3URI != "" {
		inferencePerfConfig.ModelName = cfg.Request.ModelS3URI
	} else if cfg.Request.ModelHfID != "" {
		revision := cfg.Request.ModelHfRevision
		if revision == "" {
			revision = "main"
		}
		if cached, _ := o.repo.GetModelCacheByHfID(ctx, cfg.Request.ModelHfID, revision); cached != nil && cached.Status == "cached" {
			inferencePerfConfig.ModelName = cached.S3URI
		}
	}

	configYAML, err := manifest.RenderInferencePerfConfig(inferencePerfConfig)
	if err != nil {
		return nil, "", fmt.Errorf("render config: %w", err)
	}

	// Create ConfigMap
	if err := o.createConfigMap(ctx, ns, configMapName, "config.yml", configYAML); err != nil {
		return nil, "", fmt.Errorf("create configmap: %w", err)
	}
	defer o.client.CoreV1().ConfigMaps(ns).Delete(ctx, configMapName, metav1.DeleteOptions{})

	// Create loadgen job
	inferencePerfImage, err := o.resolveInferencePerfImage(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("resolve inference-perf image: %w", err)
	}

	resultsBucket := os.Getenv("RESULTS_S3_BUCKET")
	resultsKey := ""
	awsRegion := os.Getenv("AWS_REGION")
	if awsRegion == "" {
		awsRegion = "us-east-2"
	}
	if resultsBucket != "" {
		resultsKey = fmt.Sprintf("results/suite-%s-%s.json", suiteRunID, scenarioID)
	}

	yamlStr, err := manifest.RenderLoadgenJob(manifest.LoadgenJobParams{
		Name:               loadgenName,
		Namespace:          ns,
		InferencePerfImage: inferencePerfImage,
		ConfigMapName:      configMapName,
		ResultsS3Bucket:    resultsBucket,
		ResultsS3Key:       resultsKey,
		AWSRegion:          awsRegion,
		HfToken:            o.resolveHFToken(ctx, cfg.Request.HfToken),
	})
	if err != nil {
		return nil, "", fmt.Errorf("render loadgen job: %w", err)
	}

	if err := o.applyYAML(ctx, ns, yamlStr); err != nil {
		return nil, "", fmt.Errorf("create loadgen job: %w", err)
	}
	defer o.client.BatchV1().Jobs(ns).Delete(ctx, loadgenName, metav1.DeleteOptions{})

	// Wait for job and collect results
	logData, err := o.waitAndCollect(ctx, ns, loadgenName, fmt.Sprintf("suite-%s-%s", suiteRunID, scenarioID))
	if err != nil {
		return nil, configYAML, fmt.Errorf("collect results: %w", err)
	}

	// Parse metrics
	output, err := metrics.ParseLoadgenOutput(logData)
	if err != nil {
		return nil, configYAML, fmt.Errorf("parse output: %w", err)
	}

	computed := metrics.ComputeMetrics(output)
	return computed, configYAML, nil
}

// teardownSuite cleans up suite resources.
func (o *Orchestrator) teardownSuite(ctx context.Context, ns, modelName, suiteRunID string) {
	log.Printf("[suite %s] tearing down resources", suiteRunID[:8])
	o.client.CoreV1().Services(ns).Delete(ctx, modelName, metav1.DeleteOptions{})
	o.client.AppsV1().Deployments(ns).Delete(ctx, modelName, metav1.DeleteOptions{})
}

// CleanupSuiteResources forcibly cleans up Kubernetes resources for a suite run.
// This is called when cancelling a suite to ensure resources are deleted even if
// the goroutine is stuck.
func (o *Orchestrator) CleanupSuiteResources(suiteRunID string) {
	ns := defaultNamespace
	modelName := fmt.Sprintf("suite-%s", suiteRunID[:8])
	ctx := context.Background()

	log.Printf("[suite %s] force cleanup: deleting resources", suiteRunID[:8])

	// Delete any loadgen jobs for this suite
	jobs, err := o.client.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("suite-run-id=%s", suiteRunID[:8]),
	})
	if err == nil {
		for _, job := range jobs.Items {
			o.client.BatchV1().Jobs(ns).Delete(ctx, job.Name, metav1.DeleteOptions{})
		}
	}

	// Delete model service and deployment
	o.client.CoreV1().Services(ns).Delete(ctx, modelName, metav1.DeleteOptions{})
	o.client.AppsV1().Deployments(ns).Delete(ctx, modelName, metav1.DeleteOptions{})

	log.Printf("[suite %s] force cleanup completed", suiteRunID[:8])
}
