package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"
)

// TestHandleCreateRun_RejectsMissingScenario verifies the PRD-42 contract:
// POST /api/v1/runs without a scenario returns 400.
func TestHandleCreateRun_RejectsMissingScenario(t *testing.T) {
	_, mux := setupServer()

	body := database.RunRequest{
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
		// no ScenarioID
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/runs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "scenario_id is required") {
		t.Errorf("body = %q, want scenario_id is required", w.Body.String())
	}
}

// TestHandleCreateRun_AcceptsValidScenario verifies a run with scenario_id
// set is still accepted (202).
func TestHandleCreateRun_AcceptsValidScenario(t *testing.T) {
	_, mux := setupServer()

	body := database.RunRequest{
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
		ScenarioID:           "chatbot",
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/runs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202; body: %s", w.Code, w.Body.String())
	}
}

// TestHandleCreateRun_BackwardsCompat_RunTypeAsScenario verifies the existing
// shim (handlers.go:331-339) that treats a RunType matching a scenario ID as
// a scenario fallback still works after the new validation.
func TestHandleCreateRun_BackwardsCompat_RunTypeAsScenario(t *testing.T) {
	_, mux := setupServer()

	body := database.RunRequest{
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
		// ScenarioID intentionally empty; RunType carries the scenario ID.
		RunType: "chatbot",
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/runs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202 (shim should resolve scenario); body: %s",
			w.Code, w.Body.String())
	}
}
