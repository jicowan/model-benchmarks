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
	"github.com/accelbench/accelbench/internal/secrets"

	"k8s.io/client-go/kubernetes/fake"
)

// fakeSecrets is an in-memory SecretsStore for tests.
type fakeSecrets struct {
	hfToken   string
	hfSet     bool
	hfUpdated time.Time
	dhSet     bool
	dhUpdated time.Time
	ghSet     bool
	ghUpdated time.Time
}

func (f *fakeSecrets) Describe(_ context.Context, id string) (secrets.Metadata, error) {
	switch id {
	case secrets.HFSecretID:
		if !f.hfSet {
			return secrets.Metadata{Set: false}, nil
		}
		t := f.hfUpdated
		return secrets.Metadata{Set: true, UpdatedAt: &t}, nil
	case secrets.DockerHubSecretID:
		if !f.dhSet {
			return secrets.Metadata{Set: false}, nil
		}
		t := f.dhUpdated
		return secrets.Metadata{Set: true, UpdatedAt: &t}, nil
	case secrets.GHCRSecretID:
		if !f.ghSet {
			return secrets.Metadata{Set: false}, nil
		}
		t := f.ghUpdated
		return secrets.Metadata{Set: true, UpdatedAt: &t}, nil
	}
	return secrets.Metadata{}, nil
}
func (f *fakeSecrets) GetHFToken(_ context.Context) (string, error) { return f.hfToken, nil }
func (f *fakeSecrets) PutHFToken(_ context.Context, tok string) error {
	f.hfToken = tok
	f.hfSet = true
	f.hfUpdated = time.Now()
	return nil
}
func (f *fakeSecrets) DeleteHFToken(_ context.Context) error {
	f.hfToken = ""
	f.hfSet = false
	return nil
}
func (f *fakeSecrets) PutDockerHub(_ context.Context, _, _ string) error {
	f.dhSet = true
	f.dhUpdated = time.Now()
	return nil
}
func (f *fakeSecrets) DeleteDockerHub(_ context.Context) error {
	f.dhSet = false
	return nil
}
func (f *fakeSecrets) PutGHCR(_ context.Context, _, _ string) error {
	f.ghSet = true
	f.ghUpdated = time.Now()
	return nil
}
func (f *fakeSecrets) DeleteGHCR(_ context.Context) error {
	f.ghSet = false
	return nil
}

func setupConfigServer(fs *fakeSecrets) *http.ServeMux {
	srv := NewServer(database.NewMockRepo(), fake.NewSimpleClientset(), "test-pod")
	if fs != nil {
		srv.SetSecretsStore(fs)
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return mux
}

func TestCredentials_GetReturnsSetStatus(t *testing.T) {
	fs := &fakeSecrets{hfSet: true, hfUpdated: time.Now(), dhSet: false}
	mux := setupConfigServer(fs)

	req := httptest.NewRequest("GET", "/api/v1/config/credentials", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp credentialsStatus
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.HFToken.Set {
		t.Error("hf_token should be set")
	}
	if resp.DockerHubToken.Set {
		t.Error("dockerhub_token should not be set")
	}
}

func TestCredentials_GetNeverReturnsTokenValue(t *testing.T) {
	fs := &fakeSecrets{hfToken: "hf_secret_value_do_not_leak", hfSet: true, hfUpdated: time.Now()}
	mux := setupConfigServer(fs)

	req := httptest.NewRequest("GET", "/api/v1/config/credentials", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "hf_secret_value_do_not_leak") {
		t.Errorf("response leaks token value: %s", body)
	}
}

func TestCredentials_PutHFToken(t *testing.T) {
	fs := &fakeSecrets{}
	mux := setupConfigServer(fs)

	body := strings.NewReader(`{"token":"hf_new_token"}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/credentials/hf-token", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if fs.hfToken != "hf_new_token" {
		t.Errorf("token = %q, want hf_new_token", fs.hfToken)
	}
}

func TestCredentials_PutHFToken_RejectsEmpty(t *testing.T) {
	mux := setupConfigServer(&fakeSecrets{})

	body := strings.NewReader(`{"token":""}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/credentials/hf-token", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCredentials_DeleteHFToken(t *testing.T) {
	fs := &fakeSecrets{hfToken: "hf_x", hfSet: true, hfUpdated: time.Now()}
	mux := setupConfigServer(fs)

	req := httptest.NewRequest("DELETE", "/api/v1/config/credentials/hf-token", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	if fs.hfSet {
		t.Error("hf token should be deleted")
	}
}

func TestCredentials_PutDockerHubRequiresBothFields(t *testing.T) {
	mux := setupConfigServer(&fakeSecrets{})

	body := strings.NewReader(`{"username":"jicowan","access_token":""}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/credentials/dockerhub-token", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// PRD-66 Part 2a: GHCR credential round-trips like Docker Hub.
func TestCredentials_GHCRRoundTrip(t *testing.T) {
	fs := &fakeSecrets{}
	mux := setupConfigServer(fs)

	// PUT sets it.
	body := strings.NewReader(`{"username":"jicowan","access_token":"ghp_abc"}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/credentials/ghcr-token", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 204", w.Code)
	}

	// GET reflects set.
	req = httptest.NewRequest("GET", "/api/v1/config/credentials", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var resp credentialsStatus
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.GHCRToken.Set {
		t.Error("ghcr_token should be set after PUT")
	}

	// DELETE clears it.
	req = httptest.NewRequest("DELETE", "/api/v1/config/credentials/ghcr-token", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", w.Code)
	}
	if fs.ghSet {
		t.Error("ghcr_token should be cleared after DELETE")
	}
}

func TestCredentials_PutGHCRRequiresBothFields(t *testing.T) {
	mux := setupConfigServer(&fakeSecrets{})
	body := strings.NewReader(`{"username":"jicowan","access_token":""}`)
	req := httptest.NewRequest("PUT", "/api/v1/config/credentials/ghcr-token", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCredentials_NoSecretsStore500(t *testing.T) {
	mux := setupConfigServer(nil) // no SetSecretsStore

	req := httptest.NewRequest("GET", "/api/v1/config/credentials", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}
