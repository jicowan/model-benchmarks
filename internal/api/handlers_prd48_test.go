package api

// PRD-48: view-only user role. Tests that viewer-role principals can
// reach the reader allow-list (Dashboard + Catalog + Compare + run
// detail pages + exports) and are 403'd on everything else that falls
// under the `nonViewer` gate (submit run, cancel, model-cache, etc.).

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/auth"

	"github.com/aws/aws-sdk-go-v2/aws"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

// ---------- Reader allow-list: viewer is permitted ----------

func TestViewer_ReaderAllowList_NotGated(t *testing.T) {
	mux := setupRBACServer("viewer")

	cases := []struct {
		method, path string
	}{
		{"GET", "/api/v1/status"},
		{"GET", "/api/v1/catalog?limit=1"},
		{"GET", "/api/v1/pricing"},
		{"GET", "/api/v1/dashboard/stats"},
		{"GET", "/api/v1/auth/me"},
		// Dashboard's 14-day activity + Recent Runs table + suite cards
		// read from these list endpoints.
		{"GET", "/api/v1/jobs"},
		{"GET", "/api/v1/suite-runs"},
		// Detail + export endpoints — these may 404 on the seeded mock
		// repo, which is fine. We only assert they aren't 401/403.
		{"GET", "/api/v1/runs/any"},
		{"GET", "/api/v1/runs/any/metrics"},
		{"GET", "/api/v1/runs/any/export"},
		{"GET", "/api/v1/runs/any/csv"},
		{"GET", "/api/v1/suite-runs/any"},
		{"GET", "/api/v1/suite-runs/any/csv"},
		{"GET", "/api/v1/suite-runs/any/export"},
		{"GET", "/api/v1/compare/csv"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
				t.Errorf("status = %d, viewer should not be gated; body = %s", w.Code, w.Body.String())
			}
		})
	}
}

// ---------- nonViewer gate: viewer is 403'd ----------

func TestViewer_NonViewerRoutes_403(t *testing.T) {
	mux := setupRBACServer("viewer")

	cases := []struct {
		method, path string
	}{
		{"POST", "/api/v1/runs"},
		{"POST", "/api/v1/runs/any/cancel"},
		{"DELETE", "/api/v1/runs/any"},
		{"GET", "/api/v1/instance-types"},
		{"GET", "/api/v1/recommend"},
		{"GET", "/api/v1/estimate"},
		{"GET", "/api/v1/catalog/seed"},
		{"GET", "/api/v1/memory-breakdown"},
		{"GET", "/api/v1/oom-history"},
		{"GET", "/api/v1/scenarios"},
		{"GET", "/api/v1/test-suites"},
		{"POST", "/api/v1/suite-runs"},
		{"GET", "/api/v1/model-cache"},
		{"GET", "/api/v1/model-cache/stats"},
		{"GET", "/api/v1/model-cache/any"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 (body = %s)", w.Code, w.Body.String())
			}
		})
	}
}

// Admin-only routes must remain 403 for viewers too (not 200).
func TestViewer_AdminOnlyRoutes_403(t *testing.T) {
	mux := setupRBACServer("viewer")
	cases := []struct {
		method, path string
	}{
		{"PUT", "/api/v1/config/tool-versions"},
		{"GET", "/api/v1/config/catalog-matrix"},
		{"GET", "/api/v1/config/audit-log"},
		{"POST", "/api/v1/catalog/seed"},
		{"POST", "/api/v1/model-cache"},
		{"DELETE", "/api/v1/model-cache/any"},
		{"GET", "/api/v1/users"},
		{"POST", "/api/v1/users"},
		{"PATCH", "/api/v1/users/any"},
		{"DELETE", "/api/v1/users/any"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 (body = %s)", w.Code, w.Body.String())
			}
		})
	}
}

// ---------- Regression: user role still reaches nonViewer routes ----------

func TestUser_NonViewerRoutes_NotGated(t *testing.T) {
	mux := setupRBACServer("user")
	cases := []struct {
		method, path string
	}{
		{"GET", "/api/v1/jobs"},
		{"GET", "/api/v1/scenarios"},
		{"GET", "/api/v1/test-suites"},
		{"GET", "/api/v1/model-cache"},
		{"GET", "/api/v1/instance-types"},
		{"GET", "/api/v1/memory-breakdown"},
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

// ---------- Role validation: `viewer` accepted, bogus values rejected ----------

func TestCreateUser_ViewerRoleAccepted(t *testing.T) {
	mux, idp := setupUserServer(&auth.Principal{Sub: "admin-sub", Role: "admin"})
	idp.adminCreateUser = func(_ context.Context, _ *cip.AdminCreateUserInput) (*cip.AdminCreateUserOutput, error) {
		return &cip.AdminCreateUserOutput{
			User: &types.UserType{
				Username: aws.String("new-sub"),
				Attributes: []types.AttributeType{
					{Name: aws.String("sub"), Value: aws.String("new-sub")},
					{Name: aws.String("email"), Value: aws.String("stakeholder@example.com")},
					{Name: aws.String("custom:role"), Value: aws.String("viewer")},
				},
				UserStatus: types.UserStatusTypeForceChangePassword,
				Enabled:    true,
			},
		}, nil
	}

	body := `{"email":"stakeholder@example.com","role":"viewer"}`
	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestUpdateUserRole_ViewerAccepted(t *testing.T) {
	mux, idp := setupUserServer(&auth.Principal{Sub: "admin-sub", Role: "admin"})
	idp.adminGetUser = func(_ context.Context, _ *cip.AdminGetUserInput) (*cip.AdminGetUserOutput, error) {
		return &cip.AdminGetUserOutput{
			Username: aws.String("other-sub"),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("sub"), Value: aws.String("other-sub")},
				{Name: aws.String("email"), Value: aws.String("target@example.com")},
				{Name: aws.String("custom:role"), Value: aws.String("user")},
			},
			UserStatus: types.UserStatusTypeConfirmed,
			Enabled:    true,
		}, nil
	}
	var gotUpdate *cip.AdminUpdateUserAttributesInput
	idp.adminUpdateUserAttributes = func(_ context.Context, in *cip.AdminUpdateUserAttributesInput) (*cip.AdminUpdateUserAttributesOutput, error) {
		gotUpdate = in
		return &cip.AdminUpdateUserAttributesOutput{}, nil
	}

	body := `{"role":"viewer"}`
	req := httptest.NewRequest("PATCH", "/api/v1/users/other-sub", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotUpdate == nil {
		t.Fatal("AdminUpdateUserAttributes not called")
	}
	var attrVal string
	for _, a := range gotUpdate.UserAttributes {
		if aws.ToString(a.Name) == "custom:role" {
			attrVal = aws.ToString(a.Value)
		}
	}
	if attrVal != "viewer" {
		t.Errorf("custom:role = %q, want viewer", attrVal)
	}
}

// PRD-48 extends the self-demote guard: an admin can't demote themselves
// to *any* non-admin role, including viewer. Previously only user was
// blocked.
func TestUpdateUserRole_AdminSelfDemoteToViewer_400(t *testing.T) {
	mux, _ := setupUserServer(&auth.Principal{Sub: "self-sub", Email: "me@example.com", Role: "admin"})
	body := `{"role":"viewer"}`
	req := httptest.NewRequest("PATCH", "/api/v1/users/self-sub", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cannot demote yourself") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestCreateUser_InvalidRoleRejected(t *testing.T) {
	mux, _ := setupUserServer(&auth.Principal{Sub: "admin-sub", Role: "admin"})
	// Case-sensitive: validation accepts lowercase only, so "VIEWER" is rejected.
	body := `{"email":"x@example.com","role":"VIEWER"}`
	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body = %s)", w.Code, w.Body.String())
	}
}
