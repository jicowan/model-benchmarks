package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/scenario"
	"github.com/accelbench/accelbench/internal/secrets"
)

// SecretsStore is the subset of *secrets.Manager that the API handlers and
// auto-injection sites use. Defined here so tests can stub it out without
// touching AWS.
type SecretsStore interface {
	Describe(ctx context.Context, id string) (secrets.Metadata, error)
	GetHFToken(ctx context.Context) (string, error)
	PutHFToken(ctx context.Context, token string) error
	DeleteHFToken(ctx context.Context) error
	PutDockerHub(ctx context.Context, username, accessToken string) error
	DeleteDockerHub(ctx context.Context) error
}

// --- GET /api/config/credentials -------------------------------------------

type credentialsStatus struct {
	HFToken        secrets.Metadata `json:"hf_token"`
	DockerHubToken secrets.Metadata `json:"dockerhub_token"`
}

func (s *Server) handleGetCredentials(w http.ResponseWriter, r *http.Request) {
	if s.secrets == nil {
		writeError(w, http.StatusInternalServerError, "secrets manager not configured")
		return
	}
	hf, err := s.secrets.Describe(r.Context(), secrets.HFSecretID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "describe hf-token: "+err.Error())
		return
	}
	dh, err := s.secrets.Describe(r.Context(), secrets.DockerHubSecretID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "describe dockerhub-token: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, credentialsStatus{HFToken: hf, DockerHubToken: dh})
}

// --- PUT /api/config/credentials/hf-token ----------------------------------

type putHFTokenRequest struct {
	Token string `json:"token"`
}

func (s *Server) handlePutHFToken(w http.ResponseWriter, r *http.Request) {
	if s.secrets == nil {
		writeError(w, http.StatusInternalServerError, "secrets manager not configured")
		return
	}
	var req putHFTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	if err := s.secrets.PutHFToken(r.Context(), req.Token); err != nil {
		writeError(w, http.StatusInternalServerError, "store hf-token: "+err.Error())
		return
	}
	s.audit(r.Context(), "PUT /api/v1/config/credentials/hf-token", "rotated")
	w.WriteHeader(http.StatusNoContent)
}

// --- DELETE /api/config/credentials/hf-token -------------------------------

func (s *Server) handleDeleteHFToken(w http.ResponseWriter, r *http.Request) {
	if s.secrets == nil {
		writeError(w, http.StatusInternalServerError, "secrets manager not configured")
		return
	}
	if err := s.secrets.DeleteHFToken(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "delete hf-token: "+err.Error())
		return
	}
	s.audit(r.Context(), "DELETE /api/v1/config/credentials/hf-token", "cleared")
	w.WriteHeader(http.StatusNoContent)
}

// --- PUT /api/config/credentials/dockerhub-token ---------------------------

type putDockerHubRequest struct {
	Username    string `json:"username"`
	AccessToken string `json:"access_token"`
}

func (s *Server) handleDeleteDockerHubToken(w http.ResponseWriter, r *http.Request) {
	if s.secrets == nil {
		writeError(w, http.StatusInternalServerError, "secrets manager not configured")
		return
	}
	if err := s.secrets.DeleteDockerHub(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "delete dockerhub-token: "+err.Error())
		return
	}
	s.audit(r.Context(), "DELETE /api/v1/config/credentials/dockerhub-token", "cleared")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePutDockerHubToken(w http.ResponseWriter, r *http.Request) {
	if s.secrets == nil {
		writeError(w, http.StatusInternalServerError, "secrets manager not configured")
		return
	}
	var req putDockerHubRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.AccessToken = strings.TrimSpace(req.AccessToken)
	if req.Username == "" || req.AccessToken == "" {
		writeError(w, http.StatusBadRequest, "username and access_token are required")
		return
	}
	if err := s.secrets.PutDockerHub(r.Context(), req.Username, req.AccessToken); err != nil {
		writeError(w, http.StatusInternalServerError, "store dockerhub-token: "+err.Error())
		return
	}
	s.audit(r.Context(), "PUT /api/v1/config/credentials/dockerhub-token", "rotated")
	w.WriteHeader(http.StatusNoContent)
}

// audit is a best-effort logger. Failures are logged to stderr but never
// block the caller — the audit log is not a preventive control.
func (s *Server) audit(ctx context.Context, action, summary string) {
	if err := s.repo.InsertAuditLog(ctx, action, summary, nil); err != nil {
		// Use fmt to avoid pulling log here; handlers.go already has log import.
		fmt.Fprintf(os.Stderr, "audit log insert failed (%s): %v\n", action, err)
	}
}

// ============================================================================
// /api/v1/config/catalog-matrix
// ============================================================================

type catalogMatrixResponse struct {
	Defaults      database.CatalogSeedDefaults   `json:"defaults"`
	Models        []database.CatalogModel        `json:"models"`
	InstanceTypes []database.CatalogInstanceType `json:"instance_types"`
	// Version is max(updated_at) across the three tables. Clients echo this
	// back on PUT for optimistic concurrency.
	Version time.Time `json:"version"`
}

func (s *Server) handleGetCatalogMatrix(w http.ResponseWriter, r *http.Request) {
	m, err := s.repo.LoadCatalogMatrix(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load matrix: "+err.Error())
		return
	}
	resp := catalogMatrixResponse{
		Defaults:      m.Defaults,
		Models:        m.Models,
		InstanceTypes: m.InstanceTypes,
		Version:       latestVersion(m),
	}
	if resp.Models == nil {
		resp.Models = []database.CatalogModel{}
	}
	if resp.InstanceTypes == nil {
		resp.InstanceTypes = []database.CatalogInstanceType{}
	}
	writeJSON(w, http.StatusOK, resp)
}

type putCatalogMatrixRequest struct {
	Defaults      database.CatalogSeedDefaults   `json:"defaults"`
	Models        []database.CatalogModel        `json:"models"`
	InstanceTypes []database.CatalogInstanceType `json:"instance_types"`
	Version       time.Time                      `json:"version"`
}

func (s *Server) handlePutCatalogMatrix(w http.ResponseWriter, r *http.Request) {
	var req putCatalogMatrixRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateCatalogMatrix(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	m := &database.CatalogMatrix{
		Defaults:      req.Defaults,
		Models:        req.Models,
		InstanceTypes: req.InstanceTypes,
	}
	if err := s.repo.PutCatalogMatrix(r.Context(), m, req.Version); err != nil {
		if err == database.ErrStaleVersion {
			writeError(w, http.StatusConflict, "catalog matrix has been modified by another editor — reload and retry")
			return
		}
		writeError(w, http.StatusInternalServerError, "write matrix: "+err.Error())
		return
	}
	s.audit(r.Context(), "PUT /api/v1/config/catalog-matrix",
		fmt.Sprintf("updated (%d models, %d instance types)", len(req.Models), len(req.InstanceTypes)))

	// Return the fresh state so the UI can refresh the version.
	fresh, _ := s.repo.LoadCatalogMatrix(r.Context())
	writeJSON(w, http.StatusOK, catalogMatrixResponse{
		Defaults:      fresh.Defaults,
		Models:        fresh.Models,
		InstanceTypes: fresh.InstanceTypes,
		Version:       latestVersion(fresh),
	})
}

func latestVersion(m *database.CatalogMatrix) time.Time {
	v := m.Defaults.UpdatedAt
	for _, x := range m.Models {
		if x.UpdatedAt.After(v) {
			v = x.UpdatedAt
		}
	}
	for _, x := range m.InstanceTypes {
		if x.UpdatedAt.After(v) {
			v = x.UpdatedAt
		}
	}
	return v
}

func validateCatalogMatrix(req *putCatalogMatrixRequest) error {
	if req.Defaults.FrameworkVersion == "" {
		return fmt.Errorf("defaults.framework_version is required")
	}
	if req.Defaults.Scenario == "" {
		return fmt.Errorf("defaults.scenario is required")
	}
	if req.Defaults.Dataset == "" {
		return fmt.Errorf("defaults.dataset is required")
	}
	if req.Defaults.MinDurationSeconds < 10 || req.Defaults.MinDurationSeconds > 3600 {
		return fmt.Errorf("defaults.min_duration_seconds must be in [10, 3600]")
	}
	seen := map[string]bool{}
	for _, m := range req.Models {
		if m.HfID == "" {
			return fmt.Errorf("model hf_id is required")
		}
		if seen[m.HfID] {
			return fmt.Errorf("duplicate model hf_id: %s", m.HfID)
		}
		seen[m.HfID] = true
	}
	seen = map[string]bool{}
	for _, it := range req.InstanceTypes {
		if it.Name == "" {
			return fmt.Errorf("instance type name is required")
		}
		if seen[it.Name] {
			return fmt.Errorf("duplicate instance type: %s", it.Name)
		}
		seen[it.Name] = true
	}
	return nil
}

// ============================================================================
// /api/v1/config/scenario-overrides
// ============================================================================

// scenarioOverrideEntry bundles the code-defined defaults with the current
// override (if any) so the UI can render placeholders.
type scenarioOverrideEntry struct {
	ScenarioID string `json:"scenario_id"`
	Name       string `json:"name"`
	Defaults   struct {
		NumWorkers int  `json:"num_workers"`
		Streaming  bool `json:"streaming"`
		InputMean  int  `json:"input_mean"`
		OutputMean int  `json:"output_mean"`
	} `json:"defaults"`
	Override  *database.ScenarioOverride `json:"override,omitempty"`
	UpdatedAt *time.Time                 `json:"updated_at,omitempty"`
}

func (s *Server) handleListScenarioOverrides(w http.ResponseWriter, r *http.Request) {
	overrides, err := s.repo.ListScenarioOverrides(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list overrides: "+err.Error())
		return
	}
	byID := map[string]*database.ScenarioOverride{}
	for i := range overrides {
		byID[overrides[i].ScenarioID] = &overrides[i]
	}
	all := scenario.List()
	out := make([]scenarioOverrideEntry, 0, len(all))
	for _, sc := range all {
		e := scenarioOverrideEntry{ScenarioID: sc.ID, Name: sc.Name}
		e.Defaults.NumWorkers = sc.NumWorkers
		e.Defaults.Streaming = sc.Streaming
		e.Defaults.InputMean = sc.Input.Mean
		e.Defaults.OutputMean = sc.Output.Mean
		if ov, ok := byID[sc.ID]; ok {
			e.Override = ov
			t := ov.UpdatedAt
			e.UpdatedAt = &t
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

type putScenarioOverrideRequest struct {
	NumWorkers *int  `json:"num_workers"`
	Streaming  *bool `json:"streaming"`
	InputMean  *int  `json:"input_mean"`
	OutputMean *int  `json:"output_mean"`
}

func (s *Server) handlePutScenarioOverride(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if scenario.Get(id) == nil {
		writeError(w, http.StatusNotFound, "unknown scenario: "+id)
		return
	}
	var req putScenarioOverrideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.NumWorkers != nil && (*req.NumWorkers < 1 || *req.NumWorkers > 64) {
		writeError(w, http.StatusBadRequest, "num_workers must be in [1, 64]")
		return
	}
	if req.InputMean != nil && *req.InputMean < 1 {
		writeError(w, http.StatusBadRequest, "input_mean must be > 0")
		return
	}
	if req.OutputMean != nil && *req.OutputMean < 1 {
		writeError(w, http.StatusBadRequest, "output_mean must be > 0")
		return
	}
	ov := &database.ScenarioOverride{
		ScenarioID: id,
		NumWorkers: req.NumWorkers,
		Streaming:  req.Streaming,
		InputMean:  req.InputMean,
		OutputMean: req.OutputMean,
	}
	if err := s.repo.UpsertScenarioOverride(r.Context(), ov); err != nil {
		writeError(w, http.StatusInternalServerError, "upsert override: "+err.Error())
		return
	}
	s.audit(r.Context(), "PUT /api/v1/config/scenario-overrides/"+id, summarizeOverride(ov))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteScenarioOverride(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.repo.DeleteScenarioOverride(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete override: "+err.Error())
		return
	}
	s.audit(r.Context(), "DELETE /api/v1/config/scenario-overrides/"+id, "cleared")
	w.WriteHeader(http.StatusNoContent)
}

func summarizeOverride(o *database.ScenarioOverride) string {
	parts := []string{}
	if o.NumWorkers != nil {
		parts = append(parts, fmt.Sprintf("num_workers=%d", *o.NumWorkers))
	}
	if o.Streaming != nil {
		parts = append(parts, fmt.Sprintf("streaming=%v", *o.Streaming))
	}
	if o.InputMean != nil {
		parts = append(parts, fmt.Sprintf("input_mean=%d", *o.InputMean))
	}
	if o.OutputMean != nil {
		parts = append(parts, fmt.Sprintf("output_mean=%d", *o.OutputMean))
	}
	if len(parts) == 0 {
		return "inherit all"
	}
	return "set " + strings.Join(parts, ", ")
}

// ============================================================================
// /api/v1/config/audit-log
// ============================================================================

func (s *Server) handleListAuditLog(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	entries, err := s.repo.ListAuditLog(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list audit log: "+err.Error())
		return
	}
	if entries == nil {
		entries = []database.ConfigAuditEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
