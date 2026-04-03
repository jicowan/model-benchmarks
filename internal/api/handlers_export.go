package api

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"text/template"

	"github.com/accelbench/accelbench/internal/database"
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
	InstanceType         string
	FrameworkVersion     string
	TensorParallelDegree int
	Quantization         string
	MaxModelLen          int
	AcceleratorType      string
	AcceleratorCount     int
	CPURequest           string
	MemoryRequest        string
	ShmSize              string
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
# Instance: {{ .InstanceType }}
# Tensor Parallel: {{ .TensorParallelDegree }}
# Max Model Length: {{ .MaxModelLen }}
{{- if .Quantization }}
# Quantization: {{ .Quantization }}
{{- end }}
#
# Prerequisites:
# 1. Create the HuggingFace token secret:
#    kubectl create secret generic hf-token --from-literal=token=<YOUR_HF_TOKEN>
#
# 2. Ensure your cluster has nodes with the required instance type:
#    {{ .InstanceType }}
#
# Apply with:
#    kubectl apply -f <this-file>.yaml
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
      serviceAccountName: default
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
          image: vllm/vllm-openai:{{ .FrameworkVersion }}
{{- else }}
          image: public.ecr.aws/neuron/pytorch-inference-vllm-neuronx:0.13.0-neuronx-py312-sdk2.28.0-ubuntu24.04
{{- end }}
          ports:
            - name: http
              containerPort: 8000
              protocol: TCP
          env:
            - name: HF_TOKEN
              valueFrom:
                secretKeyRef:
                  name: hf-token
                  key: token
                  optional: true
{{- if and (eq .AcceleratorType "gpu") (gt .TensorParallelDegree 1) }}
            - name: NCCL_DEBUG
              value: "INFO"
            - name: NCCL_P2P_LEVEL
              value: "NVL"
{{- end }}
{{- if eq .AcceleratorType "gpu" }}
          args:
            - "--model"
            - "{{ .ModelHfID }}"
            - "--port"
            - "8000"
            - "--tensor-parallel-size"
            - "{{ .TensorParallelDegree }}"
            - "--trust-remote-code"
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
{{- if gt .MaxModelLen 0 }}
            - "--max-model-len"
            - "{{ .MaxModelLen }}"
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
