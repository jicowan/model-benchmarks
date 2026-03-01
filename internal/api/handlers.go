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
	hfClient *recommend.HFClient
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

// RegisterRoutes registers all API routes on the given mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
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
	mux.HandleFunc("POST /api/v1/catalog/seed", s.handleCatalogSeed)
	mux.HandleFunc("GET /api/v1/catalog/seed", s.handleCatalogSeedStatus)
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

	// Create the benchmark run record.
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
		DatasetName:          req.DatasetName,
		RunType:              req.RunType,
		MinDurationSeconds:   req.MinDurationSeconds,
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

	run, err := s.repo.GetBenchmarkRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if run == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Status != "pending" && run.Status != "running" {
		writeError(w, http.StatusConflict, fmt.Sprintf("cannot cancel run with status %q", run.Status))
		return
	}

	// Cancel the orchestrator goroutine if it's running.
	s.orch.CancelRun(runID)

	if err := s.repo.UpdateRunStatus(ctx, runID, "failed"); err != nil {
		writeError(w, http.StatusInternalServerError, "update status failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"id": runID, "status": "failed"})
}

func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	ctx := r.Context()

	run, err := s.repo.GetBenchmarkRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if run == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	// Cancel if still active — the deferred teardown in Execute will
	// clean up K8s resources automatically.
	if run.Status == "pending" || run.Status == "running" {
		s.orch.CancelRun(runID)
		_ = s.repo.UpdateRunStatus(ctx, runID, "failed")
	}

	if err := s.repo.DeleteRun(ctx, runID); err != nil {
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

	// Check if Neuron instance.
	if !strings.EqualFold(instType.AcceleratorType, "gpu") {
		writeJSON(w, http.StatusOK, map[string]any{
			"explanation": map[string]any{
				"feasible": false,
				"reason":   "Configuration suggestions are not yet available for Neuron instances.",
			},
		})
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
		})
	}

	inst := recommend.InstanceSpec{
		Name:                 instType.Name,
		AcceleratorType:      instType.AcceleratorType,
		AcceleratorName:      instType.AcceleratorName,
		AcceleratorCount:     instType.AcceleratorCount,
		AcceleratorMemoryGiB: instType.AcceleratorMemoryGiB,
	}

	rec := recommend.Recommend(*modelCfg, inst, allSpecs)
	writeJSON(w, http.StatusOK, rec)
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
