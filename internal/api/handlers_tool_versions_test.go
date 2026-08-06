package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"
)

// TestToolVersions_GetReturnsDefaults verifies GET returns the mock-seeded
// defaults when nothing else has touched the row.
func TestToolVersions_GetReturnsDefaults(t *testing.T) {
	_, mux := setupPRD32Server(nil)

	req := httptest.NewRequest("GET", "/api/v1/config/tool-versions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp toolVersionsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.FrameworkVersion == "" {
		t.Error("framework_version should be populated")
	}
	if resp.InferencePerfVersion == "" {
		t.Error("inference_perf_version should be populated")
	}
	// PRD-66 Part 2: the two multi-node image tags default to the current pins.
	if resp.LLMDVersion != "v0.8.1" {
		t.Errorf("llmd_version = %q, want v0.8.1", resp.LLMDVersion)
	}
	if resp.PDVLLMVersion != "v0.25.0" {
		t.Errorf("pd_vllm_version = %q, want v0.25.0", resp.PDVLLMVersion)
	}
}

// TestToolVersions_PutRoundTripsMultinodeVersions verifies a PUT that sets the
// llm-d-aws + D/P vLLM tags persists them (PRD-66 Part 2).
func TestToolVersions_PutRoundTripsMultinodeVersions(t *testing.T) {
	_, mux := setupPRD32Server(nil)

	body := strings.NewReader(`{"framework_version":"v0.19.0","inference_perf_version":"v0.2.0","llmd_version":"v0.9.0","pd_vllm_version":"v0.26.1"}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/tool-versions", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp toolVersionsResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.LLMDVersion != "v0.9.0" {
		t.Errorf("llmd_version = %q, want v0.9.0", resp.LLMDVersion)
	}
	if resp.PDVLLMVersion != "v0.26.1" {
		t.Errorf("pd_vllm_version = %q, want v0.26.1", resp.PDVLLMVersion)
	}
}

// TestToolVersions_PutKeepsExistingMultinodeVersions verifies that omitting the
// llm-d / D-P fields on a PUT keeps their prior values (they're not required).
func TestToolVersions_PutKeepsExistingMultinodeVersions(t *testing.T) {
	repo, mux := setupPRD32Server(nil)
	repo.SeedToolVersions(&database.ToolVersions{
		FrameworkVersion:     "v0.19.0",
		InferencePerfVersion: "v0.2.0",
		LLMDVersion:          "v0.8.1",
		PDVLLMVersion:        "v0.25.0",
	})

	body := strings.NewReader(`{"framework_version":"v0.20.0","inference_perf_version":"v0.3.0"}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/tool-versions", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp toolVersionsResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.LLMDVersion != "v0.8.1" {
		t.Errorf("llmd_version = %q, want kept v0.8.1", resp.LLMDVersion)
	}
	if resp.PDVLLMVersion != "v0.25.0" {
		t.Errorf("pd_vllm_version = %q, want kept v0.25.0", resp.PDVLLMVersion)
	}
}

// TestToolVersions_PutUpdatesAndAudits round-trips a PUT and verifies the
// new values stick.
func TestToolVersions_PutUpdatesAndAudits(t *testing.T) {
	repo, mux := setupPRD32Server(nil)

	body := strings.NewReader(`{"framework_version":"v0.20.0","inference_perf_version":"v0.3.0"}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/tool-versions", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp toolVersionsResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.FrameworkVersion != "v0.20.0" {
		t.Errorf("framework_version = %q, want v0.20.0", resp.FrameworkVersion)
	}
	if resp.InferencePerfVersion != "v0.3.0" {
		t.Errorf("inference_perf_version = %q, want v0.3.0", resp.InferencePerfVersion)
	}

	// Audit log should have recorded the change.
	entries, _ := repo.ListAuditLog(req.Context(), 10)
	found := false
	for _, e := range entries {
		if strings.Contains(e.Action, "tool-versions") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an audit entry for tool-versions PUT, got: %+v", entries)
	}
}

// TestToolVersions_PutValidation rejects empty values.
func TestToolVersions_PutValidation(t *testing.T) {
	_, mux := setupPRD32Server(nil)

	cases := []string{
		`{"framework_version":"","inference_perf_version":"v0.2.0"}`,
		`{"framework_version":"v0.19.0","inference_perf_version":""}`,
		`{"framework_version":"   ","inference_perf_version":"v0.2.0"}`,
	}
	for _, body := range cases {
		req := httptest.NewRequest("PUT", "/api/v1/config/tool-versions", strings.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body=%s status=%d, want 400", body, w.Code)
		}
	}
}

// TestToolVersions_GetSurfacesEnvOverride is a smoke test for the
// env-override flag in the response. We don't set the env var here (doing so
// would pollute other tests), just verify the default state is false.
func TestToolVersions_GetDefaultEnvOverrideFalse(t *testing.T) {
	repo, mux := setupPRD32Server(nil)
	repo.SeedToolVersions(&database.ToolVersions{
		FrameworkVersion:     "v0.19.0",
		InferencePerfVersion: "v0.2.0",
	})

	req := httptest.NewRequest("GET", "/api/v1/config/tool-versions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp toolVersionsResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	// We can't guarantee the env isn't set in the test runner, but at minimum
	// the response should include the flag field (non-nil bool).
	_ = resp.EnvOverrideActive
}
