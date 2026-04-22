package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/database"

	"k8s.io/client-go/kubernetes/fake"
)

// ============================================================================
// Catalog matrix endpoints (PRD-32)
// ============================================================================

func setupPRD32Server(fs *fakeSecrets) (*database.MockRepo, *http.ServeMux) {
	repo := database.NewMockRepo()
	srv := NewServer(repo, fake.NewSimpleClientset())
	if fs != nil {
		srv.SetSecretsStore(fs)
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return repo, mux
}

func TestCatalogMatrix_Get(t *testing.T) {
	repo, mux := setupPRD32Server(nil)
	repo.SeedCatalogMatrix(&database.CatalogMatrix{
		Defaults: database.CatalogSeedDefaults{
			FrameworkVersion: "v0.19.0", Scenario: "chatbot", Dataset: "synthetic",
			MinDurationSeconds: 180, UpdatedAt: time.Now(),
		},
		Models: []database.CatalogModel{
			{HfID: "meta-llama/Llama-3.1-8B-Instruct", Enabled: true, UpdatedAt: time.Now()},
		},
	})

	req := httptest.NewRequest("GET", "/api/v1/config/catalog-matrix", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp catalogMatrixResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Defaults.FrameworkVersion != "v0.19.0" {
		t.Errorf("framework_version = %q", resp.Defaults.FrameworkVersion)
	}
	if len(resp.Models) != 1 {
		t.Errorf("models len = %d", len(resp.Models))
	}
	if resp.Version.IsZero() {
		t.Error("version should be populated")
	}
}

func TestCatalogMatrix_PutValidation(t *testing.T) {
	_, mux := setupPRD32Server(nil)

	body := strings.NewReader(`{"defaults":{"framework_version":"","scenario":"chatbot","dataset":"synthetic","min_duration_seconds":180}}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/catalog-matrix", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestCatalogMatrix_PutDuplicateModel(t *testing.T) {
	_, mux := setupPRD32Server(nil)

	body := strings.NewReader(`{
		"defaults":{"framework_version":"v0.19.0","scenario":"chatbot","dataset":"synthetic","min_duration_seconds":180},
		"models":[
			{"hf_id":"meta-llama/Llama-3.1-8B","enabled":true},
			{"hf_id":"meta-llama/Llama-3.1-8B","enabled":true}
		]
	}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/catalog-matrix", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d", w.Code)
	}
}

// ============================================================================
// Scenario overrides endpoints (PRD-32)
// ============================================================================

func TestScenarioOverrides_ListExposesCodeDefaults(t *testing.T) {
	_, mux := setupPRD32Server(nil)

	req := httptest.NewRequest("GET", "/api/v1/config/scenario-overrides", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var entries []scenarioOverrideEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) == 0 {
		t.Fatal("should list all code-defined scenarios")
	}
	for _, e := range entries {
		if e.Defaults.NumWorkers < 1 {
			t.Errorf("scenario %s has bad default num_workers=%d", e.ScenarioID, e.Defaults.NumWorkers)
		}
		// No overrides seeded — all should have Override == nil.
		if e.Override != nil {
			t.Errorf("scenario %s unexpectedly has an override", e.ScenarioID)
		}
	}
}

func TestScenarioOverrides_PutUnknownScenario404(t *testing.T) {
	_, mux := setupPRD32Server(nil)

	body := strings.NewReader(`{"num_workers":8}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/scenario-overrides/not-a-real-scenario", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.Code)
	}
}

func TestScenarioOverrides_PutNumWorkersRange(t *testing.T) {
	_, mux := setupPRD32Server(nil)

	body := strings.NewReader(`{"num_workers":999}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/scenario-overrides/chatbot", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestScenarioOverrides_PutAndList(t *testing.T) {
	repo, mux := setupPRD32Server(nil)

	body := strings.NewReader(`{"num_workers":16,"input_mean":1024}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/scenario-overrides/chatbot", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT status=%d body=%s", w.Code, w.Body.String())
	}

	overrides, _ := repo.ListScenarioOverrides(context.Background())
	if len(overrides) != 1 || overrides[0].ScenarioID != "chatbot" {
		t.Fatalf("unexpected overrides: %+v", overrides)
	}
	if overrides[0].NumWorkers == nil || *overrides[0].NumWorkers != 16 {
		t.Errorf("num_workers not stored: %+v", overrides[0].NumWorkers)
	}
	if overrides[0].InputMean == nil || *overrides[0].InputMean != 1024 {
		t.Errorf("input_mean not stored: %+v", overrides[0].InputMean)
	}
	if overrides[0].Streaming != nil {
		t.Errorf("streaming should be nil (inherit), got %+v", overrides[0].Streaming)
	}
}

func TestScenarioOverrides_Delete(t *testing.T) {
	repo, mux := setupPRD32Server(nil)
	ten := 10
	repo.UpsertScenarioOverride(context.Background(), &database.ScenarioOverride{
		ScenarioID: "batch",
		NumWorkers: &ten,
	})

	req := httptest.NewRequest("DELETE", "/api/v1/config/scenario-overrides/batch", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status=%d", w.Code)
	}
	overrides, _ := repo.ListScenarioOverrides(context.Background())
	if len(overrides) != 0 {
		t.Errorf("overrides still present after delete: %+v", overrides)
	}
}

// ============================================================================
// Audit log (PRD-32)
// ============================================================================

func TestAuditLog_CredentialsRotationWritesEntry(t *testing.T) {
	fs := &fakeSecrets{}
	repo, mux := setupPRD32Server(fs)

	body := strings.NewReader(`{"token":"hf_new"}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/credentials/hf-token", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT hf-token status=%d", w.Code)
	}

	entries, _ := repo.ListAuditLog(context.Background(), 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Action, "hf-token") {
		t.Errorf("audit action wrong: %q", entries[0].Action)
	}
	if strings.Contains(entries[0].Summary, "hf_new") {
		t.Error("audit summary leaks token value")
	}
}

func TestAuditLog_ListReverseChronological(t *testing.T) {
	repo, mux := setupPRD32Server(nil)

	repo.InsertAuditLog(context.Background(), "first", "first one", nil)
	time.Sleep(5 * time.Millisecond)
	repo.InsertAuditLog(context.Background(), "second", "second one", nil)

	req := httptest.NewRequest("GET", "/api/v1/config/audit-log", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var entries []database.ConfigAuditEntry
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Action != "second" {
		t.Errorf("first entry should be newest; got action=%s", entries[0].Action)
	}
}

// ============================================================================
// Registry card (PRD-32) — env-gated path only; ECR path requires AWS creds.
// ============================================================================

func TestRegistry_DisabledWhenEnvUnset(t *testing.T) {
	t.Setenv("PULL_THROUGH_REGISTRY", "")
	// Blow away any cached value from other tests.
	registryCacheMu.Lock()
	registryCached = nil
	registryCacheMu.Unlock()

	_, mux := setupPRD32Server(nil)

	req := httptest.NewRequest("GET", "/api/v1/config/registry", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp registryResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Enabled {
		t.Error("expected enabled=false when env unset")
	}
	if resp.HelmHint == "" {
		t.Error("expected helm_hint when disabled")
	}
}
