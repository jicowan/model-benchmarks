package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

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
	w.WriteHeader(http.StatusNoContent)
}

// --- PUT /api/config/credentials/dockerhub-token ---------------------------

type putDockerHubRequest struct {
	Username    string `json:"username"`
	AccessToken string `json:"access_token"`
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
	w.WriteHeader(http.StatusNoContent)
}
