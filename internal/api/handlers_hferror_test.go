package api

// Regression tests for the gated-model login-bounce bug.
//
// When a user selected a gated HuggingFace model on the New Benchmark
// (or Estimate) page, /recommend, /estimate, and /memory-breakdown
// fetched the model config from HuggingFace, which returned 401. The
// handlers relayed that 401 verbatim, and the frontend's fetchJSON
// treats any 401 as an expired session — bouncing the user to /login.
// The symptom only appeared with auth disabled, because /auth/refresh
// returns 503 there so the silent-refresh retry always failed.
//
// The fix: an HF 401/403 (gated model, missing/expired platform token,
// or HF rate-limiting) is remapped to 422 so it never collides with the
// app's own auth 401. See writeHFError in handlers.go.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/accelbench/accelbench/internal/recommend"

	"k8s.io/client-go/kubernetes/fake"
)

// hfErrorServer builds a server whose HF client always fails with the
// given HFError, mirroring a gated-model response. Auth is disabled
// (NewServerWithHFClient default), matching the reported environment.
func hfErrorServer(hfErr error) *http.ServeMux {
	repo := seedRepo()
	client := fake.NewSimpleClientset()
	hf := &recommend.MockHFClient{
		FetchModelConfigFunc: func(modelID, hfToken string) (*recommend.ModelConfig, error) {
			return nil, hfErr
		},
	}
	srv := NewServerWithHFClient(repo, client, hf, "test-pod")
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return mux
}

func TestHFGatedError_NotRelayedAs401(t *testing.T) {
	// The three endpoints that fetch model config from HuggingFace.
	// meta-llama/Llama-3.1-8B is seeded by seedRepo(); the mock HF
	// client rejects it as gated regardless.
	endpoints := []string{
		"/api/v1/recommend?model=meta-llama/Llama-3.1-8B&instance_type=g5.xlarge",
		"/api/v1/estimate?model=meta-llama/Llama-3.1-8B",
		"/api/v1/memory-breakdown?model=meta-llama/Llama-3.1-8B&instance_type=g5.xlarge",
	}

	// Both statuses HuggingFace uses for gated/unauthorized models.
	for _, hfStatus := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		mux := hfErrorServer(&recommend.HFError{
			StatusCode: hfStatus,
			Message:    "model is gated — provide an HF token with access",
		})
		for _, ep := range endpoints {
			t.Run(http.StatusText(hfStatus)+" "+ep, func(t *testing.T) {
				req := httptest.NewRequest("GET", ep, nil)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)

				if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
					t.Fatalf("status = %d; a gated-model HF %d must not surface as the app's own 401/403 (that bounces the user to /login). body = %s",
						w.Code, hfStatus, w.Body.String())
				}
				if w.Code != http.StatusUnprocessableEntity {
					t.Errorf("status = %d, want 422; body = %s", w.Code, w.Body.String())
				}
				// The original, actionable message must be preserved.
				if body := w.Body.String(); !contains(body, "gated") {
					t.Errorf("body = %q, want it to preserve the gated-model message", body)
				}
			})
		}
	}
}

// A non-HFError failure from the HF fetch (e.g. network error) should be
// a 502, never a 401.
func TestHFGenericError_Is502(t *testing.T) {
	mux := hfErrorServer(errString("connection refused"))
	req := httptest.NewRequest("GET", "/api/v1/recommend?model=meta-llama/Llama-3.1-8B&instance_type=g5.xlarge", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502; body = %s", w.Code, w.Body.String())
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
