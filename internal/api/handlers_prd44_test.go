package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/auth"

	"k8s.io/client-go/kubernetes/fake"
)

// setupRBACServer builds a Server whose `p` subrouter is wrapped in a
// test middleware that stamps a Principal with the given role onto
// every request. This bypasses JWT verification entirely and lets us
// exercise the admin gate at the route level.
func setupRBACServer(role string) *http.ServeMux {
	repo := seedRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client, "test-pod")

	// Outer mux catches /healthz + /auth/*; protected subrouter is
	// built by RegisterRoutes. We stamp the principal on every
	// request by wrapping the outer mux.
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	stamp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := &auth.Principal{Sub: "test-sub", Email: "t@example.com", Role: role}
		mux.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})

	outer := http.NewServeMux()
	outer.Handle("/", stamp)
	return outer
}

// ---------- Admin-only routes ----------

func TestAdminOnlyRoute_UserRole_403(t *testing.T) {
	mux := setupRBACServer("user")

	cases := []struct {
		method, path string
	}{
		{"PUT", "/api/v1/config/tool-versions"},
		{"GET", "/api/v1/config/catalog-matrix"},
		{"GET", "/api/v1/config/audit-log"},
		{"POST", "/api/v1/catalog/seed"},
		{"POST", "/api/v1/model-cache"},
		{"DELETE", "/api/v1/model-cache/some-id"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 (body = %s)", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "forbidden: admin required") {
				t.Errorf("body = %q, want contains 'forbidden: admin required'", w.Body.String())
			}
		})
	}
}

func TestAdminOnlyRoute_AdminRole_Passes(t *testing.T) {
	mux := setupRBACServer("admin")

	// GETs that will succeed (they'll return 200/empty-list from the
	// seeded mock repo).
	cases := []struct {
		method, path string
	}{
		{"GET", "/api/v1/config/catalog-matrix"},
		{"GET", "/api/v1/config/audit-log"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			// Admin must NOT be 401/403. Any 2xx or 4xx-for-reasons-
			// other-than-RBAC is fine for this gate test.
			if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
				t.Errorf("status = %d, admin should not be gated; body = %s", w.Code, w.Body.String())
			}
		})
	}
}

// ---------- Regression: non-admin routes stay open ----------

func TestNonAdminRoute_UserRole_Allowed(t *testing.T) {
	mux := setupRBACServer("user")

	// These are routes that should still work for non-admin users.
	cases := []struct {
		method, path string
	}{
		{"GET", "/api/v1/catalog?limit=1"},
		{"GET", "/api/v1/scenarios"},
		{"GET", "/api/v1/test-suites"},
		{"GET", "/api/v1/model-cache"},
		{"GET", "/api/v1/dashboard/stats"},
		{"GET", "/api/v1/jobs"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
				t.Errorf("status = %d, user should not be gated; body = %s", w.Code, w.Body.String())
			}
		})
	}
}

// Run submission (the main non-admin workflow) explicitly — confirm
// user can POST /runs.
func TestCreateRun_UserRole_Allowed(t *testing.T) {
	mux := setupRBACServer("user")

	body := `{"model_hf_id":"meta-llama/Llama-3.1-8B","model_hf_revision":"abc123","instance_type_name":"g5.xlarge","framework":"vllm","framework_version":"v0.6.0","tensor_parallel_degree":1,"concurrency":16,"input_sequence_length":512,"output_sequence_length":256,"dataset_name":"sharegpt","run_type":"on_demand","scenario_id":"chatbot"}`
	req := httptest.NewRequest("POST", "/api/v1/runs", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202 (body = %s)", w.Code, w.Body.String())
	}
}
