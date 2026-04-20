package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/orchestrator"
	"github.com/accelbench/accelbench/internal/recommend"
	"github.com/accelbench/accelbench/internal/scenario"
	"github.com/accelbench/accelbench/internal/testsuite"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Server holds dependencies for API handlers.
type Server struct {
	repo     database.Repo
	orch     *orchestrator.Orchestrator
	client   kubernetes.Interface
	hfClient recommend.HFClientInterface
}

// NewServer creates a new API server.
func NewServer(repo database.Repo, client kubernetes.Interface) *Server {
	return &Server{
		repo:     repo,
		orch:     orchestrator.New(client, repo),
		client:   client,
		hfClient: recommend.NewHFClient(),
	}
}

// NewServerWithHFClient creates a new API server with a custom HFClient (for testing).
func NewServerWithHFClient(repo database.Repo, client kubernetes.Interface, hfClient recommend.HFClientInterface) *Server {
	return &Server{
		repo:     repo,
		orch:     orchestrator.New(client, repo),
		client:   client,
		hfClient: hfClient,
	}
}

// RecoverOrphanedRuns attempts to complete any runs that were left in "running"
// status due to an API restart. Call this on server startup.
func (s *Server) RecoverOrphanedRuns(ctx context.Context) {
	s.orch.RecoverOrphanedRuns(ctx)
}

// RegisterRoutes registers all API routes on the given mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/catalog", s.handleListCatalog)
	mux.HandleFunc("POST /api/v1/runs", s.handleCreateRun)
	mux.HandleFunc("GET /api/v1/runs/{id}", s.handleGetRun)
	mux.HandleFunc("GET /api/v1/runs/{id}/metrics", s.handleGetMetrics)
	mux.HandleFunc("GET /api/v1/jobs", s.handleListRuns)
	mux.HandleFunc("POST /api/v1/runs/{id}/cancel", s.handleCancelRun)
	mux.HandleFunc("DELETE /api/v1/runs/{id}", s.handleDeleteRun)
	mux.HandleFunc("GET /api/v1/instance-types", s.handleListInstanceTypes)
	mux.HandleFunc("GET /api/v1/pricing", s.handleListPricing)
	mux.HandleFunc("GET /api/v1/recommend", s.handleRecommend)
	mux.HandleFunc("GET /api/v1/estimate", s.handleEstimate)
	mux.HandleFunc("POST /api/v1/catalog/seed", s.handleCatalogSeed)
	mux.HandleFunc("GET /api/v1/catalog/seed", s.handleCatalogSeedStatus)
	mux.HandleFunc("POST /api/v1/admin/backfill-model-families", s.handleBackfillModelFamilies)
	// PRD-15: Memory breakdown and OOM history
	mux.HandleFunc("GET /api/v1/memory-breakdown", s.handleMemoryBreakdown)
	mux.HandleFunc("GET /api/v1/oom-history", s.handleOOMHistory)
	// Export Kubernetes manifest
	mux.HandleFunc("GET /api/v1/runs/{id}/export", s.handleExportManifest)
	// Export HTML report (PRD-16)
	mux.HandleFunc("GET /api/v1/runs/{id}/report", s.handleExportReport)
	// PRD-12/13: Scenarios and test suites
	mux.HandleFunc("GET /api/v1/scenarios", s.handleListScenarios)
	mux.HandleFunc("GET /api/v1/test-suites", s.handleListTestSuites)
	mux.HandleFunc("GET /api/v1/suite-runs", s.handleListSuiteRuns)
	mux.HandleFunc("POST /api/v1/suite-runs", s.handleCreateSuiteRun)
	mux.HandleFunc("GET /api/v1/suite-runs/{id}", s.handleGetSuiteRun)
	// PRD-20: Model cache management
	mux.HandleFunc("GET /api/v1/model-cache", s.handleListModelCache)
	mux.HandleFunc("POST /api/v1/model-cache", s.handleCreateModelCache)
	mux.HandleFunc("GET /api/v1/model-cache/{id}", s.handleGetModelCache)
	mux.HandleFunc("DELETE /api/v1/model-cache/{id}", s.handleDeleteModelCache)
	mux.HandleFunc("POST /api/v1/model-cache/register", s.handleRegisterCustomModel)
}

func (s *Server) handleListCatalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := database.CatalogFilter{
		ModelHfID:       q.Get("model"),
		ModelFamily:     q.Get("model_family"),
		InstanceFamily:  q.Get("instance_family"),
		AcceleratorType: q.Get("accelerator_type"),
		SortBy:          q.Get("sort"),
		SortDesc:        q.Get("order") == "desc",
	}
	if v := q.Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &f.Limit)
	}
	if v := q.Get("offset"); v != "" {
		fmt.Sscanf(v, "%d", &f.Offset)
	}

	entries, err := s.repo.ListCatalog(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog query failed")
		return
	}
	if entries == nil {
		entries = []database.CatalogEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req database.RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()

	// For S3-only models, derive model_hf_id from the S3 URI if not provided
	if req.ModelHfID == "" && req.ModelS3URI != "" {
		req.ModelHfID = req.ModelS3URI
	}

	if req.ModelHfID == "" {
		writeError(w, http.StatusBadRequest, "model_hf_id or model_s3_uri is required")
		return
	}

	// Look up or auto-register model.
	model, err := s.repo.EnsureModel(ctx, req.ModelHfID, req.ModelHfRevision)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ensure model failed")
		return
	}

	// Look up instance type.
	instType, err := s.repo.GetInstanceTypeByName(ctx, req.InstanceTypeName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup instance type failed")
		return
	}
	if instType == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("instance type %s not found", req.InstanceTypeName))
		return
	}

	// Default dataset from scenario if not provided
	datasetName := req.DatasetName
	scenarioID := req.ScenarioID
	if scenarioID == "" {
		// For backwards compatibility, check if RunType contains a scenario ID
		if scn := scenario.Get(req.RunType); scn != nil {
			scenarioID = req.RunType
			req.ScenarioID = scenarioID // Ensure orchestrator sees the scenario
			if datasetName == "" {
				datasetName = scn.Dataset
			}
		}
	} else if datasetName == "" {
		if scn := scenario.Get(scenarioID); scn != nil {
			datasetName = scn.Dataset
		}
	}
	if datasetName == "" {
		datasetName = "synthetic" // fallback default
	}

	// Determine run_type: 'catalog' for seeded runs, 'on_demand' for user-initiated
	runType := req.RunType
	if runType != "catalog" {
		runType = "on_demand"
	}

	// Create the benchmark run record.
	var scenarioPtr *string
	if scenarioID != "" {
		scenarioPtr = &scenarioID
	}
	run := &database.BenchmarkRun{
		ModelID:              model.ID,
		InstanceTypeID:       instType.ID,
		Framework:            req.Framework,
		FrameworkVersion:     req.FrameworkVersion,
		TensorParallelDegree: req.TensorParallelDegree,
		Quantization:         req.Quantization,
		Concurrency:          req.Concurrency,
		InputSequenceLength:  req.InputSequenceLength,
		OutputSequenceLength: req.OutputSequenceLength,
		DatasetName:          datasetName,
		RunType:              runType,
		ScenarioID:           scenarioPtr,
		MinDurationSeconds:   req.MinDurationSeconds,
		MaxModelLen:          req.MaxModelLen,
		Status:               "pending",
	}

	runID, err := s.repo.CreateBenchmarkRun(ctx, run)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create run failed")
		return
	}

	// Launch orchestration in the background with a detached context
	// so it isn't canceled when the HTTP response is sent.
	go func() {
		cfg := orchestrator.RunConfig{
			RunID:        runID,
			Model:        model,
			InstanceType: instType,
			Request:      &req,
		}
		if err := s.orch.Execute(context.Background(), cfg); err != nil {
			log.Printf("benchmark run %s failed: %v", runID, err)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{
		"id":     runID,
		"status": "pending",
	})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	run, err := s.repo.GetBenchmarkRun(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if run == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	m, err := s.repo.GetMetricsByRunID(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if m == nil {
		writeError(w, http.StatusNotFound, "metrics not found")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := database.RunFilter{
		Status:  q.Get("status"),
		ModelID: q.Get("model"),
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Offset = n
		}
	}

	items, err := s.repo.ListRuns(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list runs failed")
		return
	}
	if items == nil {
		items = []database.RunListItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	ctx := r.Context()

	// Try benchmark run first
	run, err := s.repo.GetBenchmarkRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	if run != nil {
		// Found a benchmark run
		if run.Status != "pending" && run.Status != "running" {
			writeError(w, http.StatusConflict, fmt.Sprintf("cannot cancel run with status %q", run.Status))
			return
		}
		s.orch.CancelRun(runID)
		if err := s.repo.UpdateRunStatus(ctx, runID, "failed"); err != nil {
			writeError(w, http.StatusInternalServerError, "update status failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": runID, "status": "failed"})
		return
	}

	// Try suite run
	suiteRun, err := s.repo.GetTestSuiteRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if suiteRun == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if suiteRun.Status != "pending" && suiteRun.Status != "running" {
		writeError(w, http.StatusConflict, fmt.Sprintf("cannot cancel suite run with status %q", suiteRun.Status))
		return
	}
	s.orch.CancelRun(runID)
	// Forcibly clean up Kubernetes resources in case the goroutine is stuck
	s.orch.CleanupSuiteResources(runID)
	if err := s.repo.UpdateSuiteRunStatus(ctx, runID, "failed", nil); err != nil {
		writeError(w, http.StatusInternalServerError, "update status failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": runID, "status": "failed"})
}

func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	ctx := r.Context()

	// Try benchmark run first
	run, err := s.repo.GetBenchmarkRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	if run != nil {
		// Cancel if still active
		if run.Status == "pending" || run.Status == "running" {
			s.orch.CancelRun(runID)
			_ = s.repo.UpdateRunStatus(ctx, runID, "failed")
		}
		if err := s.repo.DeleteRun(ctx, runID); err != nil {
			writeError(w, http.StatusInternalServerError, "delete failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Try suite run
	suiteRun, err := s.repo.GetTestSuiteRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if suiteRun == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	// Cancel if still active
	if suiteRun.Status == "pending" || suiteRun.Status == "running" {
		s.orch.CancelRun(runID)
		_ = s.repo.UpdateSuiteRunStatus(ctx, runID, "failed", nil)
	}
	if err := s.repo.DeleteSuiteRun(ctx, runID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRecommend(w http.ResponseWriter, r *http.Request) {
	modelID := r.URL.Query().Get("model")
	instanceName := r.URL.Query().Get("instance_type")
	if modelID == "" || instanceName == "" {
		writeError(w, http.StatusBadRequest, "model and instance_type query parameters are required")
		return
	}

	hfToken := r.Header.Get("X-HF-Token")

	// Optional overrides from user
	var opts recommend.RecommendOptions
	if tpStr := r.URL.Query().Get("tp"); tpStr != "" {
		fmt.Sscanf(tpStr, "%d", &opts.TPOverride)
	}
	if overheadStr := r.URL.Query().Get("overhead_gib"); overheadStr != "" {
		fmt.Sscanf(overheadStr, "%f", &opts.OverheadGiB)
	}
	if maxMLStr := r.URL.Query().Get("max_model_len"); maxMLStr != "" {
		fmt.Sscanf(maxMLStr, "%d", &opts.MaxModelLenOverride)
	}

	// Look up instance type from DB.
	instType, err := s.repo.GetInstanceTypeByName(r.Context(), instanceName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "instance type lookup failed")
		return
	}
	if instType == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("instance type %s not found", instanceName))
		return
	}

	// Fetch model config from HuggingFace.
	modelCfg, err := s.hfClient.FetchModelConfig(modelID, hfToken)
	if err != nil {
		var hfErr *recommend.HFError
		if errors.As(err, &hfErr) {
			writeError(w, hfErr.StatusCode, hfErr.Message)
			return
		}
		writeError(w, http.StatusBadGateway, "failed to fetch model metadata from HuggingFace")
		return
	}

	// Get all GPU instances for suggesting alternatives.
	allInstTypes, err := s.repo.ListInstanceTypes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list instance types failed")
		return
	}
	var allSpecs []recommend.InstanceSpec
	for _, it := range allInstTypes {
		allSpecs = append(allSpecs, recommend.InstanceSpec{
			Name:                 it.Name,
			AcceleratorType:      it.AcceleratorType,
			AcceleratorName:      it.AcceleratorName,
			AcceleratorCount:     it.AcceleratorCount,
			AcceleratorMemoryGiB: it.AcceleratorMemoryGiB,
			MemoryGiB:            it.MemoryGiB,
		})
	}

	inst := recommend.InstanceSpec{
		Name:                 instType.Name,
		AcceleratorType:      instType.AcceleratorType,
		AcceleratorName:      instType.AcceleratorName,
		AcceleratorCount:     instType.AcceleratorCount,
		AcceleratorMemoryGiB: instType.AcceleratorMemoryGiB,
		MemoryGiB:            instType.MemoryGiB,
	}

	var rec *recommend.Recommendation
	if strings.EqualFold(instType.AcceleratorType, "neuron") {
		rec = recommend.RecommendNeuron(*modelCfg, inst)
	} else {
		rec = recommend.Recommend(*modelCfg, inst, allSpecs, opts)
	}

	// Add valid TP options for UI dropdown
	type responseWithOptions struct {
		*recommend.Recommendation
		ValidTPOptions []int `json:"valid_tp_options,omitempty"`
	}
	resp := responseWithOptions{
		Recommendation: rec,
		ValidTPOptions: recommend.ValidTPOptions(modelCfg.NumAttentionHeads, modelCfg.NumKeyValueHeads, instType.AcceleratorCount),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListInstanceTypes(w http.ResponseWriter, r *http.Request) {
	types, err := s.repo.ListInstanceTypes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list instance types failed")
		return
	}
	if types == nil {
		types = []database.InstanceType{}
	}
	writeJSON(w, http.StatusOK, types)
}

func (s *Server) handleListPricing(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	if region == "" {
		region = "us-east-2"
	}

	rows, err := s.repo.ListPricing(r.Context(), region)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pricing query failed")
		return
	}
	if rows == nil {
		rows = []database.PricingRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}

const (
	seedNamespace = "accelbench"
	seedLabelKey  = "accelbench/role"
	seedLabelVal  = "catalog-seed"
)

func (s *Server) handleCatalogSeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	toolsImage := os.Getenv("TOOLS_IMAGE")
	if toolsImage == "" {
		writeError(w, http.StatusInternalServerError, "TOOLS_IMAGE not configured")
		return
	}
	configMap := os.Getenv("CATALOG_CONFIGMAP")
	if configMap == "" {
		writeError(w, http.StatusInternalServerError, "CATALOG_CONFIGMAP not configured")
		return
	}

	// Check for active seed jobs.
	jobs, err := s.client.BatchV1().Jobs(seedNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: seedLabelKey + "=" + seedLabelVal,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list seed jobs")
		return
	}
	for _, j := range jobs.Items {
		if j.Status.Active > 0 {
			writeError(w, http.StatusConflict, fmt.Sprintf("A catalog seed job is already running: %s", j.Name))
			return
		}
	}

	// Build the Job spec.
	jobName := fmt.Sprintf("catalog-seed-%d", time.Now().Unix())
	backoffLimit := int32(1)
	ttl := int32(86400)

	env := []corev1.EnvVar{
		{
			Name:  "API_URL",
			Value: fmt.Sprintf("http://accelbench-api.%s.svc.cluster.local:8080", seedNamespace),
		},
	}
	if secretName := os.Getenv("HF_SECRET_NAME"); secretName != "" {
		env = append(env, corev1.EnvVar{
			Name: "HF_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
					Key:                  "HF_TOKEN",
					Optional:             boolPtr(true),
				},
			},
		})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: seedNamespace,
			Labels:    map[string]string{seedLabelKey: seedLabelVal},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "catalog-seed",
							Image:   toolsImage,
							Command: []string{"/bin/bash", "/scripts/seed-catalog.sh"},
							Env:     env,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "scripts", MountPath: "/scripts"},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "scripts",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: configMap},
								},
							},
						},
					},
					NodeSelector: map[string]string{"accelbench/node-type": "system"},
				},
			},
		},
	}

	created, err := s.client.BatchV1().Jobs(seedNamespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create seed job: %v", err))
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"job_name": created.Name,
		"status":   "active",
	})
}

func (s *Server) handleCatalogSeedStatus(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.client.BatchV1().Jobs(seedNamespace).List(r.Context(), metav1.ListOptions{
		LabelSelector: seedLabelKey + "=" + seedLabelVal,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list seed jobs")
		return
	}

	if len(jobs.Items) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "none"})
		return
	}

	// Sort by creation timestamp descending, pick most recent.
	sort.Slice(jobs.Items, func(i, j int) bool {
		return jobs.Items[i].CreationTimestamp.After(jobs.Items[j].CreationTimestamp.Time)
	})
	latest := jobs.Items[0]

	status := seedJobStatus(&latest)
	resp := map[string]any{
		"job_name": latest.Name,
		"status":   status,
	}
	if latest.Status.StartTime != nil {
		resp["started_at"] = latest.Status.StartTime.Format(time.RFC3339)
	}
	if latest.Status.CompletionTime != nil {
		resp["completed_at"] = latest.Status.CompletionTime.Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, resp)
}

func seedJobStatus(job *batchv1.Job) string {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return "succeeded"
		}
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return "failed"
		}
	}
	if job.Status.Active > 0 {
		return "active"
	}
	// Job exists but hasn't started yet — treat as active.
	return "active"
}

func boolPtr(b bool) *bool { return &b }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) handleBackfillModelFamilies(w http.ResponseWriter, r *http.Request) {
	// Check if repo supports backfill (real repo does, mock may not)
	type backfiller interface {
		BackfillModelFamilies(ctx context.Context) (int64, error)
	}
	bf, ok := s.repo.(backfiller)
	if !ok {
		writeError(w, http.StatusNotImplemented, "backfill not supported")
		return
	}

	updated, err := bf.BackfillModelFamilies(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"updated": updated,
		"message": fmt.Sprintf("Updated model_family for %d models", updated),
	})
}

// handleListScenarios returns all available benchmark scenarios.
func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	scenarios := scenario.List()

	// Build response with computed duration
	type scenarioResponse struct {
		ID              string               `json:"id"`
		Name            string               `json:"name"`
		Description     string               `json:"description"`
		DurationSeconds int                  `json:"duration_seconds"`
		LoadType        string               `json:"load_type"`
		Stages          []scenario.LoadStage `json:"stages"`
	}

	result := make([]scenarioResponse, 0, len(scenarios))
	for _, s := range scenarios {
		result = append(result, scenarioResponse{
			ID:              s.ID,
			Name:            s.Name,
			Description:     s.Description,
			DurationSeconds: s.TotalDuration(),
			LoadType:        s.LoadType,
			Stages:          s.Stages,
		})
	}

	writeJSON(w, http.StatusOK, result)
}

// handleListTestSuites returns all available test suites.
func (s *Server) handleListTestSuites(w http.ResponseWriter, r *http.Request) {
	suites := testsuite.List()

	type suiteResponse struct {
		ID              string   `json:"id"`
		Name            string   `json:"name"`
		Description     string   `json:"description"`
		Scenarios       []string `json:"scenarios"`
		TotalDuration   int      `json:"total_duration_seconds"`
	}

	result := make([]suiteResponse, 0, len(suites))
	for _, suite := range suites {
		result = append(result, suiteResponse{
			ID:            suite.ID,
			Name:          suite.Name,
			Description:   suite.Description,
			Scenarios:     suite.Scenarios,
			TotalDuration: suite.TotalDuration,
		})
	}

	writeJSON(w, http.StatusOK, result)
}

// handleListSuiteRuns returns a list of test suite runs.
func (s *Server) handleListSuiteRuns(w http.ResponseWriter, r *http.Request) {
	items, err := s.repo.ListSuiteRunsWithNames(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list suite runs failed")
		return
	}
	if items == nil {
		items = []database.SuiteRunListItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleCreateSuiteRun creates a new test suite run.
func (s *Server) handleCreateSuiteRun(w http.ResponseWriter, r *http.Request) {
	var req database.SuiteRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// For S3-only models, derive model_hf_id from the S3 URI if not provided
	if req.ModelHfID == "" && req.ModelS3URI != "" {
		req.ModelHfID = req.ModelS3URI
	}

	// Validate required fields
	if req.ModelHfID == "" || req.InstanceTypeName == "" {
		writeError(w, http.StatusBadRequest, "model_hf_id (or model_s3_uri) and instance_type_name are required")
		return
	}

	// Need either suite_id or scenario_ids
	if req.SuiteID == "" && len(req.ScenarioIDs) == 0 {
		writeError(w, http.StatusBadRequest, "either suite_id or scenario_ids is required")
		return
	}

	// Determine scenarios to run
	var scenarioIDs []string
	suiteID := req.SuiteID

	if len(req.ScenarioIDs) > 0 {
		// Custom scenario list
		scenarioIDs = req.ScenarioIDs
		suiteID = "custom"
	} else {
		// Predefined suite
		suite := testsuite.Get(req.SuiteID)
		if suite == nil {
			writeError(w, http.StatusBadRequest, "unknown suite: "+req.SuiteID)
			return
		}
		scenarioIDs = suite.Scenarios
	}

	// Validate all scenarios exist
	for _, scenarioID := range scenarioIDs {
		if scenario.Get(scenarioID) == nil {
			writeError(w, http.StatusBadRequest, "unknown scenario: "+scenarioID)
			return
		}
	}

	ctx := r.Context()

	// Ensure model exists
	model, err := s.repo.EnsureModel(ctx, req.ModelHfID, req.ModelHfRevision)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ensure model: "+err.Error())
		return
	}

	// Get instance type
	instType, err := s.repo.GetInstanceTypeByName(ctx, req.InstanceTypeName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get instance type: "+err.Error())
		return
	}
	if instType == nil {
		writeError(w, http.StatusBadRequest, "unknown instance type: "+req.InstanceTypeName)
		return
	}

	// Create suite run record
	suiteRun := &database.TestSuiteRun{
		ModelID:              model.ID,
		InstanceTypeID:       instType.ID,
		SuiteID:              suiteID,
		TensorParallelDegree: req.TensorParallelDegree,
		Quantization:         req.Quantization,
		MaxModelLen:          req.MaxModelLen,
		Status:               "pending",
	}

	suiteRunID, err := s.repo.CreateTestSuiteRun(ctx, suiteRun)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create suite run: "+err.Error())
		return
	}

	// Create scenario result records for each scenario
	for _, scenarioID := range scenarioIDs {
		result := &database.ScenarioResult{
			SuiteRunID: suiteRunID,
			ScenarioID: scenarioID,
			Status:     "pending",
		}
		if _, err := s.repo.CreateScenarioResult(ctx, result); err != nil {
			writeError(w, http.StatusInternalServerError, "create scenario result: "+err.Error())
			return
		}
	}

	// Update request with resolved scenario IDs for executor
	req.ScenarioIDs = scenarioIDs

	// Start suite execution in background
	go s.orch.ExecuteSuite(context.Background(), suiteRunID, req)

	// Return the created suite run
	suiteRun.ID = suiteRunID
	writeJSON(w, http.StatusAccepted, suiteRun)
}

// handleGetSuiteRun returns a test suite run with its scenario results.
func (s *Server) handleGetSuiteRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing suite run ID")
		return
	}

	ctx := r.Context()

	suiteRun, err := s.repo.GetTestSuiteRun(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get suite run: "+err.Error())
		return
	}
	if suiteRun == nil {
		writeError(w, http.StatusNotFound, "suite run not found")
		return
	}

	results, err := s.repo.GetScenarioResults(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get scenario results: "+err.Error())
		return
	}

	// Build response with progress info
	type scenarioProgress struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	type progressInfo struct {
		Completed int                `json:"completed"`
		Total     int                `json:"total"`
		Scenarios []scenarioProgress `json:"scenarios"`
	}
	type scenarioDefinition struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		TargetQPS       int    `json:"target_qps"`
		DurationSeconds int    `json:"duration_seconds"`
		LoadType        string `json:"load_type"`
	}
	type suiteRunResponse struct {
		*database.TestSuiteRun
		Progress            progressInfo              `json:"progress"`
		Results             []database.ScenarioResult `json:"results"`
		ScenarioDefinitions []scenarioDefinition      `json:"scenario_definitions"`
	}

	completed := 0
	scenarios := make([]scenarioProgress, 0, len(results))
	for _, r := range results {
		scenarios = append(scenarios, scenarioProgress{ID: r.ScenarioID, Status: r.Status})
		if r.Status == "completed" || r.Status == "failed" || r.Status == "skipped" {
			completed++
		}
	}

	// Build scenario definitions with target QPS for charts
	scenarioDefs := make([]scenarioDefinition, 0, len(results))
	for _, r := range results {
		if sc := scenario.Get(r.ScenarioID); sc != nil {
			scenarioDefs = append(scenarioDefs, scenarioDefinition{
				ID:              sc.ID,
				Name:            sc.Name,
				TargetQPS:       sc.TargetQPS(),
				DurationSeconds: sc.TotalDuration(),
				LoadType:        sc.LoadType,
			})
		}
	}

	writeJSON(w, http.StatusOK, suiteRunResponse{
		TestSuiteRun: suiteRun,
		Progress: progressInfo{
			Completed: completed,
			Total:     len(results),
			Scenarios: scenarios,
		},
		Results:             results,
		ScenarioDefinitions: scenarioDefs,
	})
}


