package api

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/template"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/report"
)

// handleExportManifest generates a Kubernetes manifest YAML for deploying
// the model configuration from a completed benchmark run.
func (s *Server) handleExportManifest(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")

	// First check if the run exists and is completed.
	run, err := s.repo.GetBenchmarkRun(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if run == nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Status != "completed" {
		writeError(w, http.StatusBadRequest, "can only export completed runs")
		return
	}

	// Get the full export details with joined model/instance info.
	details, err := s.repo.GetRunExportDetails(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query export details failed")
		return
	}
	if details == nil {
		writeError(w, http.StatusNotFound, "run details not found")
		return
	}

	// Generate the manifest.
	manifest, err := generateManifest(details)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate manifest failed: %v", err))
		return
	}

	// Return as downloadable YAML file.
	filename := fmt.Sprintf("vllm-%s.yaml", sanitizeFilename(details.ModelHfID))
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(manifest))
}

// sanitizeFilename converts a model ID to a safe filename.
func sanitizeFilename(modelID string) string {
	// Replace slashes and other problematic characters.
	s := strings.ReplaceAll(modelID, "/", "-")
	s = strings.ReplaceAll(s, ":", "-")
	s = strings.ToLower(s)
	return s
}

// manifestData holds the template data for generating the Kubernetes manifest.
type manifestData struct {
	Name                 string
	ModelHfID            string
	ModelS3URI           string // non-empty when the run loaded weights from S3
	InstanceType         string
	FrameworkVersion     string
	TensorParallelDegree int
	Quantization         string
	MaxModelLen          int
	MaxNumBatchedTokens  int // 0 = use vLLM default
	AcceleratorType      string
	AcceleratorCount     int
	CPURequest           string
	MemoryRequest        string
	ShmSize              string
	PullThroughRegistry  string // ECR pull-through cache host (empty = direct Docker Hub)
}

func generateManifest(d *database.RunExportDetails) (string, error) {
	data := manifestData{
		Name:                 "vllm-" + sanitizeFilename(d.ModelHfID),
		ModelHfID:            d.ModelHfID,
		InstanceType:         d.InstanceTypeName,
		FrameworkVersion:     d.FrameworkVersion,
		TensorParallelDegree: d.TensorParallelDegree,
		MaxModelLen:          d.MaxModelLen,
		AcceleratorType:      d.AcceleratorType,
		AcceleratorCount:     d.AcceleratorCount,
		CPURequest:           fmt.Sprintf("%d", max(d.VCPUs/2, 4)),
		MemoryRequest:        fmt.Sprintf("%dGi", max(d.MemoryGiB/2, 16)),
		ShmSize:              "16Gi",
		PullThroughRegistry:  os.Getenv("PULL_THROUGH_REGISTRY"),
	}
	if d.MaxNumBatchedTokens != nil {
		data.MaxNumBatchedTokens = *d.MaxNumBatchedTokens
	}
	if d.ModelS3URI != nil && *d.ModelS3URI != "" {
		data.ModelS3URI = *d.ModelS3URI
	}

	// Handle quantization.
	if d.Quantization != nil {
		data.Quantization = *d.Quantization
	}

	var buf bytes.Buffer
	if err := manifestTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

var manifestFuncs = template.FuncMap{
	"div": func(a, b int) int { return a / b },
}

var manifestTemplate = template.Must(template.New("manifest").Funcs(manifestFuncs).Parse(`# Kubernetes manifest for vLLM model deployment
# Generated from AccelBench benchmark run
#
# Model: {{ .ModelHfID }}
{{- if .ModelS3URI }}
# Weights: {{ .ModelS3URI }} (loaded via Run:ai Model Streamer)
{{- end }}
# Instance: {{ .InstanceType }}
# Tensor Parallel: {{ .TensorParallelDegree }}
# Max Model Length: {{ .MaxModelLen }}
{{- if gt .MaxNumBatchedTokens 0 }}
# Max Num Batched Tokens: {{ .MaxNumBatchedTokens }}
{{- end }}
{{- if .Quantization }}
# Quantization: {{ .Quantization }}
{{- end }}
#
# Prerequisites:
{{- if .ModelS3URI }}
# 1. Pod must have read access to the S3 bucket holding the model weights.
#    The template uses a ServiceAccount named 'accelbench-model' that assumes
#    an IAM role via EKS Pod Identity. If you're deploying outside the
#    AccelBench cluster, replace this with your own SA + IAM binding.
{{- else }}
# 1. Create the HuggingFace token secret:
#    kubectl create secret generic hf-token --from-literal=token=<YOUR_HF_TOKEN>
{{- end }}
#
# 2. Ensure your cluster has nodes with the required instance type:
#    {{ .InstanceType }}
#
# Apply with:
#    kubectl apply -f <this-file>.yaml
{{- if not .ModelS3URI }}
---
apiVersion: v1
kind: Secret
metadata:
  name: hf-token
  labels:
    app.kubernetes.io/name: {{ .Name }}
type: Opaque
stringData:
  token: "<YOUR_HF_TOKEN>"
{{- end }}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Name }}
  labels:
    app.kubernetes.io/name: {{ .Name }}
    app.kubernetes.io/component: model-server
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Name }}
  template:
    metadata:
      labels:
        app.kubernetes.io/name: {{ .Name }}
        app.kubernetes.io/component: model-server
    spec:
      serviceAccountName: {{ if .ModelS3URI }}accelbench-model{{ else }}default{{ end }}
      terminationGracePeriodSeconds: 30
      tolerations:
{{- if eq .AcceleratorType "gpu" }}
        - key: nvidia.com/gpu
          operator: Exists
          effect: NoSchedule
{{- else }}
        - key: aws.amazon.com/neuron
          operator: Exists
          effect: NoSchedule
{{- end }}
      nodeSelector:
        node.kubernetes.io/instance-type: {{ .InstanceType }}
      containers:
        - name: vllm
{{- if eq .AcceleratorType "gpu" }}
          image: {{ if .PullThroughRegistry }}{{ .PullThroughRegistry }}/dockerhub/{{ end }}vllm/vllm-openai:{{ .FrameworkVersion }}
{{- else }}
          image: public.ecr.aws/neuron/pytorch-inference-vllm-neuronx:0.13.0-neuronx-py312-sdk2.28.0-ubuntu24.04
{{- end }}
          ports:
            - name: http
              containerPort: 8000
              protocol: TCP
          env:
{{- if .ModelS3URI }}
            - name: AWS_REGION
              value: "us-east-2"
{{- else }}
            - name: HF_TOKEN
              valueFrom:
                secretKeyRef:
                  name: hf-token
                  key: token
                  optional: true
{{- end }}
{{- if and (eq .AcceleratorType "gpu") (gt .TensorParallelDegree 1) }}
            - name: NCCL_DEBUG
              value: "INFO"
            - name: NCCL_P2P_LEVEL
              value: "NVL"
{{- end }}
{{- if eq .AcceleratorType "gpu" }}
          args:
{{- if .ModelS3URI }}
            - "--model"
            - "{{ .ModelS3URI }}"
            - "--load-format"
            - "runai_streamer"
            - "--model-loader-extra-config"
            - '{"concurrency":16}'
{{- else }}
            - "--model"
            - "{{ .ModelHfID }}"
{{- end }}
            - "--port"
            - "8000"
            - "--tensor-parallel-size"
            - "{{ .TensorParallelDegree }}"
            - "--trust-remote-code"
{{- if not .ModelS3URI }}
{{- if eq .Quantization "fp16" }}
            - "--dtype"
            - "float16"
{{- else if eq .Quantization "int8" }}
            - "--quantization"
            - "bitsandbytes"
            - "--load-format"
            - "bitsandbytes"
{{- else if eq .Quantization "int4" }}
            - "--quantization"
            - "gptq"
{{- end }}
{{- end }}
{{- if gt .MaxModelLen 0 }}
            - "--max-model-len"
            - "{{ .MaxModelLen }}"
{{- end }}
{{- if gt .MaxNumBatchedTokens 0 }}
            - "--max-num-batched-tokens"
            - "{{ .MaxNumBatchedTokens }}"
{{- end }}
{{- else }}
          command: ["vllm"]
          args:
            - "serve"
            - "{{ .ModelHfID }}"
            - "--port"
            - "8000"
            - "--tensor-parallel-size"
            - "{{ .TensorParallelDegree }}"
            - "--trust-remote-code"
            - "--block-size"
            - "32"
{{- if gt .MaxModelLen 0 }}
            - "--max-model-len"
            - "{{ .MaxModelLen }}"
{{- end }}
{{- if gt .MaxNumBatchedTokens 0 }}
            - "--max-num-batched-tokens"
            - "{{ .MaxNumBatchedTokens }}"
{{- end }}
{{- end }}
          resources:
            requests:
              cpu: {{ .CPURequest }}
              memory: {{ .MemoryRequest }}
{{- if eq .AcceleratorType "gpu" }}
              nvidia.com/gpu: "{{ .AcceleratorCount }}"
            limits:
              nvidia.com/gpu: "{{ .AcceleratorCount }}"
          volumeMounts:
            - name: shm
              mountPath: /dev/shm
{{- else }}
              aws.amazon.com/neuron: "{{ div .AcceleratorCount 2 }}"
            limits:
              aws.amazon.com/neuron: "{{ div .AcceleratorCount 2 }}"
{{- end }}
          readinessProbe:
            httpGet:
              path: /health
              port: http
            initialDelaySeconds: 30
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 60
          startupProbe:
            httpGet:
              path: /health
              port: http
            initialDelaySeconds: 30
            periodSeconds: 10
{{- if eq .AcceleratorType "gpu" }}
            failureThreshold: 120
{{- else }}
            # Neuron compilation can take 30-60+ minutes
            failureThreshold: 540
{{- end }}
          livenessProbe:
            httpGet:
              path: /health
              port: http
            periodSeconds: 30
            timeoutSeconds: 5
            failureThreshold: 3
{{- if eq .AcceleratorType "gpu" }}
      volumes:
        - name: shm
          emptyDir:
            medium: Memory
            sizeLimit: {{ .ShmSize }}
{{- end }}
---
apiVersion: v1
kind: Service
metadata:
  name: {{ .Name }}
  labels:
    app.kubernetes.io/name: {{ .Name }}
    app.kubernetes.io/component: model-server
spec:
  type: ClusterIP
  ports:
    - name: http
      port: 8000
      targetPort: http
      protocol: TCP
  selector:
    app.kubernetes.io/name: {{ .Name }}
`))

// handleExportRunCSV returns a CSV of a single benchmark run's metadata + metrics (PRD-41).
func (s *Server) handleExportRunCSV(w http.ResponseWriter, r *http.Request) {
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

	metrics, _ := s.repo.GetMetricsByRunID(r.Context(), runID)     // nil-safe inside generator
	details, _ := s.repo.GetRunExportDetails(r.Context(), runID)    // nil-safe inside generator

	// Best-effort pricing lookup.
	var hourlyRate *float64
	if details != nil {
		if rows, err := s.repo.ListPricing(r.Context(), "us-east-2"); err == nil {
			for _, row := range rows {
				if row.InstanceTypeName == details.InstanceTypeName {
					rate := row.OnDemandHourlyUSD
					hourlyRate = &rate
					break
				}
			}
		}
	}

	data, err := report.GenerateRunCSV(run, metrics, details, hourlyRate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate run csv: %v", err))
		return
	}

	model := "run"
	if details != nil {
		model = sanitizeFilename(details.ModelHfID)
	}
	shortID := runID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	filename := fmt.Sprintf("accelbench-run-%s-%s.csv", model, shortID)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleExportSuiteCSV returns a one-row-per-scenario CSV for a test suite run (PRD-41).
func (s *Server) handleExportSuiteCSV(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	suite, err := s.repo.GetTestSuiteRun(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if suite == nil {
		writeError(w, http.StatusNotFound, "suite run not found")
		return
	}

	results, err := s.repo.GetScenarioResults(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get scenario results: "+err.Error())
		return
	}

	model, _ := s.repo.GetModelByID(r.Context(), suite.ModelID)
	instance, _ := s.repo.GetInstanceTypeByID(r.Context(), suite.InstanceTypeID)

	data, err := report.GenerateSuiteCSV(suite, results, model, instance)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate suite csv: %v", err))
		return
	}

	name := "suite"
	if model != nil {
		name = sanitizeFilename(model.HfID)
	}
	shortID := id
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	filename := fmt.Sprintf("accelbench-suite-%s-%s.csv", name, shortID)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleExportSuiteManifest returns the vLLM Deployment+Service YAML for the
// suite's model. Mirrors handleExportManifest but sources its data from
// test_suite_runs + joined model/instance rows (PRD-41).
func (s *Server) handleExportSuiteManifest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	suite, err := s.repo.GetTestSuiteRun(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if suite == nil {
		writeError(w, http.StatusNotFound, "suite run not found")
		return
	}
	if suite.Status != "completed" && suite.Status != "failed" {
		writeError(w, http.StatusBadRequest, "can only export terminal suite runs")
		return
	}

	model, err := s.repo.GetModelByID(r.Context(), suite.ModelID)
	if err != nil || model == nil {
		writeError(w, http.StatusInternalServerError, "model lookup failed")
		return
	}
	instance, err := s.repo.GetInstanceTypeByID(r.Context(), suite.InstanceTypeID)
	if err != nil || instance == nil {
		writeError(w, http.StatusInternalServerError, "instance lookup failed")
		return
	}

	// Reconstruct a RunExportDetails so we can reuse generateManifest.
	details := &database.RunExportDetails{
		RunID:                suite.ID,
		ModelHfID:            model.HfID,
		ModelS3URI:           suite.ModelS3URI,
		InstanceTypeName:     instance.Name,
		TensorParallelDegree: suite.TensorParallelDegree,
		Quantization:         suite.Quantization,
		AcceleratorType:      instance.AcceleratorType,
		AcceleratorName:      instance.AcceleratorName,
		AcceleratorCount:     instance.AcceleratorCount,
		AcceleratorMemoryGiB: instance.AcceleratorMemoryGiB,
		VCPUs:                instance.VCPUs,
		MemoryGiB:            instance.MemoryGiB,
	}
	details.MaxModelLen = suite.MaxModelLen
	details.MaxNumBatchedTokens = suite.MaxNumBatchedTokens
	// Framework fields: use persisted values when available (suites
	// created after migration 026), else derive from accelerator type
	// as a safe fallback for historical rows.
	if suite.Framework != nil && *suite.Framework != "" {
		details.Framework = *suite.Framework
	} else if instance.AcceleratorType == "neuron" {
		details.Framework = "vllm-neuron"
	} else {
		details.Framework = "vllm"
	}
	if suite.FrameworkVersion != nil {
		details.FrameworkVersion = *suite.FrameworkVersion
	}

	manifest, err := generateManifest(details)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate manifest failed: %v", err))
		return
	}

	filename := fmt.Sprintf("vllm-%s-suite.yaml", sanitizeFilename(model.HfID))
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(manifest))
}

// --- Compare exports ---

// resolveCompareParams parses the shared query string format used by both
// compare export handlers and returns the fetched entries + pricing lookup.
func (s *Server) resolveCompareParams(
	r *http.Request,
) ([]database.CatalogEntry, func(string) *float64, string, string, error) {
	ids := strings.Split(r.URL.Query().Get("ids"), ",")
	var cleaned []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			cleaned = append(cleaned, id)
		}
	}
	if len(cleaned) < 2 {
		return nil, nil, "", "", fmt.Errorf("at least two run ids are required")
	}

	region := r.URL.Query().Get("region")
	if region == "" {
		region = "us-east-2"
	}
	tier := r.URL.Query().Get("tier")
	if tier == "" {
		tier = "on_demand"
	}

	// Fetch only the catalog rows for the selected ids (PRD-36).
	entries, _, err := s.repo.ListCatalog(r.Context(), database.CatalogFilter{
		RunIDs: cleaned,
		Limit:  len(cleaned),
	})
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("list catalog: %w", err)
	}
	if len(entries) < 2 {
		return nil, nil, "", "", fmt.Errorf("need at least two resolvable run ids")
	}

	// Build pricing lookup for the requested region + tier.
	priceByInstance := map[string]*float64{}
	if rows, err := s.repo.ListPricing(r.Context(), region); err == nil {
		for _, row := range rows {
			var v *float64
			switch tier {
			case "reserved_1yr":
				v = row.Reserved1YrHourlyUSD
			case "reserved_3yr":
				v = row.Reserved3YrHourlyUSD
			default:
				x := row.OnDemandHourlyUSD
				v = &x
			}
			if v != nil {
				priceByInstance[row.InstanceTypeName] = v
			}
		}
	}
	lookup := func(name string) *float64 { return priceByInstance[name] }
	return entries, lookup, tier, region, nil
}

func tierLabel(tier string) string {
	switch tier {
	case "reserved_1yr":
		return "Reserved 1yr"
	case "reserved_3yr":
		return "Reserved 3yr"
	default:
		return "On-demand"
	}
}

// handleExportCompareCSV returns a CSV of the comparison data.
func (s *Server) handleExportCompareCSV(w http.ResponseWriter, r *http.Request) {
	entries, lookup, tier, region, err := s.resolveCompareParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := report.GenerateCompareCSV(entries, lookup, tierLabel(tier), region)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generate compare csv: %v", err))
		return
	}
	filename := fmt.Sprintf("accelbench-compare-%d-runs.csv", len(entries))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
