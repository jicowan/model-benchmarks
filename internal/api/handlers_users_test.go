package api

// PRD-45: tests for the user-management handlers. Each test wires a
// mockIDP to fake Cognito responses and a principal-stamping
// middleware to assert self-mutation guards.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/accelbench/accelbench/internal/auth"

	"github.com/aws/aws-sdk-go-v2/aws"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"

	"k8s.io/client-go/kubernetes/fake"
)

// setupUserServer builds a server whose /api/v1/* mux is wrapped in a
// principal stamper so tests can fake out the identity without doing
// JWT verification. `idp` is the mockIDP the tests drive responses on.
func setupUserServer(principal *auth.Principal) (*http.ServeMux, *mockIDP) {
	repo := seedRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client, "test-pod")
	idp := &mockIDP{}
	// Disabled=true lets upstream-stamped principals pass through without
	// JWT verification (see PRD-44 middleware handling); individual tests
	// stamp below. UserPoolID still flows to Cognito admin calls.
	srv.SetAuth(auth.Config{UserPoolID: "us-east-2_test", ClientID: "test-client", Disabled: true}, idp, nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	stamp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal == nil {
			mux.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
	})
	outer := http.NewServeMux()
	outer.Handle("/", stamp)
	return outer, idp
}

func strAttr(name, value string) types.AttributeType {
	return types.AttributeType{Name: aws.String(name), Value: aws.String(value)}
}

// ---------- list ----------

func TestListUsers_HappyPath(t *testing.T) {
	mux, idp := setupUserServer(&auth.Principal{Sub: "admin-sub", Role: "admin"})
	idp.listUsers = func(_ context.Context, in *cip.ListUsersInput) (*cip.ListUsersOutput, error) {
		if aws.ToString(in.UserPoolId) != "us-east-2_test" {
			t.Errorf("UserPoolId = %q", aws.ToString(in.UserPoolId))
		}
		now := time.Now().UTC()
		return &cip.ListUsersOutput{
			Users: []types.UserType{
				{
					Username: aws.String("sub-1"),
					Attributes: []types.AttributeType{
						strAttr("sub", "sub-1"),
						strAttr("email", "alice@example.com"),
						strAttr("custom:role", "admin"),
					},
					UserStatus:           types.UserStatusTypeConfirmed,
					Enabled:              true,
					UserCreateDate:       &now,
					UserLastModifiedDate: &now,
				},
				{
					Username: aws.String("sub-2"),
					Attributes: []types.AttributeType{
						strAttr("sub", "sub-2"),
						strAttr("email", "bob@example.com"),
					},
					UserStatus:     types.UserStatusTypeForceChangePassword,
					Enabled:        true,
					UserCreateDate: &now,
				},
			},
			PaginationToken: aws.String("next-page-token"),
		}, nil
	}

	req := httptest.NewRequest("GET", "/api/v1/users?limit=60", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp listUsersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	if len(resp.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(resp.Rows))
	}
	if resp.NextToken != "next-page-token" {
		t.Errorf("NextToken = %q", resp.NextToken)
	}
	if resp.Rows[0].Email != "alice@example.com" || resp.Rows[0].Role != "admin" {
		t.Errorf("row 0 = %+v", resp.Rows[0])
	}
	if resp.Rows[1].Role != "" {
		t.Errorf("row 1 role = %q, want empty", resp.Rows[1].Role)
	}
	if resp.Rows[1].Status != "FORCE_CHANGE_PASSWORD" {
		t.Errorf("row 1 status = %q", resp.Rows[1].Status)
	}
}

func TestListUsers_BadLimit_400(t *testing.T) {
	mux, _ := setupUserServer(&auth.Principal{Sub: "admin-sub", Role: "admin"})
	req := httptest.NewRequest("GET", "/api/v1/users?limit=999", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestListUsers_FilterPassedThrough(t *testing.T) {
	mux, idp := setupUserServer(&auth.Principal{Sub: "admin-sub", Role: "admin"})
	var gotFilter string
	idp.listUsers = func(_ context.Context, in *cip.ListUsersInput) (*cip.ListUsersOutput, error) {
		gotFilter = aws.ToString(in.Filter)
		return &cip.ListUsersOutput{}, nil
	}
	req := httptest.NewRequest("GET", "/api/v1/users?filter=ali", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if gotFilter != `email ^= "ali"` {
		t.Errorf("filter = %q", gotFilter)
	}
}

// ---------- create ----------

func TestCreateUser_Invite(t *testing.T) {
	mux, idp := setupUserServer(&auth.Principal{Sub: "admin-sub", Role: "admin"})
	var gotInput *cip.AdminCreateUserInput
	idp.adminCreateUser = func(_ context.Context, in *cip.AdminCreateUserInput) (*cip.AdminCreateUserOutput, error) {
		gotInput = in
		return &cip.AdminCreateUserOutput{
			User: &types.UserType{
				Username: aws.String("new-sub"),
				Attributes: []types.AttributeType{
					strAttr("sub", "new-sub"),
					strAttr("email", "charlie@example.com"),
					strAttr("custom:role", "user"),
				},
				UserStatus: types.UserStatusTypeForceChangePassword,
				Enabled:    true,
			},
		}, nil
	}

	body := `{"email":"charlie@example.com","role":"user"}`
	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotInput == nil {
		t.Fatal("AdminCreateUser not called")
	}
	if aws.ToString(gotInput.Username) != "charlie@example.com" {
		t.Errorf("Username = %q", aws.ToString(gotInput.Username))
	}
	if len(gotInput.DesiredDeliveryMediums) != 1 || gotInput.DesiredDeliveryMediums[0] != types.DeliveryMediumTypeEmail {
		t.Errorf("delivery mediums = %v", gotInput.DesiredDeliveryMediums)
	}
	// Expect email + email_verified + custom:role in the attributes.
	var gotEmail, gotRole, gotVerified string
	for _, a := range gotInput.UserAttributes {
		switch aws.ToString(a.Name) {
		case "email":
			gotEmail = aws.ToString(a.Value)
		case "email_verified":
			gotVerified = aws.ToString(a.Value)
		case "custom:role":
			gotRole = aws.ToString(a.Value)
		}
	}
	if gotEmail != "charlie@example.com" || gotRole != "user" || gotVerified != "true" {
		t.Errorf("attrs: email=%q role=%q verified=%q", gotEmail, gotRole, gotVerified)
	}
}

func TestCreateUser_BadEmail_400(t *testing.T) {
	mux, _ := setupUserServer(&auth.Principal{Sub: "admin-sub", Role: "admin"})
	body := `{"email":"not-an-email","role":"user"}`
	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestCreateUser_BadRole_400(t *testing.T) {
	mux, _ := setupUserServer(&auth.Principal{Sub: "admin-sub", Role: "admin"})
	body := `{"email":"x@example.com","role":"root"}`
	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

// ---------- role update + self-demote guard ----------

func TestUpdateUserRole_SelfDemote_400(t *testing.T) {
	mux, _ := setupUserServer(&auth.Principal{Sub: "self-sub", Email: "me@example.com", Role: "admin"})
	body := `{"role":"user"}`
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

func TestUpdateUserRole_OtherUser_200(t *testing.T) {
	mux, idp := setupUserServer(&auth.Principal{Sub: "admin-sub", Email: "admin@example.com", Role: "admin"})
	idp.adminGetUser = func(_ context.Context, _ *cip.AdminGetUserInput) (*cip.AdminGetUserOutput, error) {
		return &cip.AdminGetUserOutput{
			Username: aws.String("other-sub"),
			UserAttributes: []types.AttributeType{
				strAttr("sub", "other-sub"),
				strAttr("email", "target@example.com"),
				strAttr("custom:role", "user"),
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
	body := `{"role":"admin"}`
	req := httptest.NewRequest("PATCH", "/api/v1/users/other-sub", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotUpdate == nil || aws.ToString(gotUpdate.Username) != "other-sub" {
		t.Errorf("AdminUpdateUserAttributes not called with correct sub")
	}
	var attrVal string
	for _, a := range gotUpdate.UserAttributes {
		if aws.ToString(a.Name) == "custom:role" {
			attrVal = aws.ToString(a.Value)
		}
	}
	if attrVal != "admin" {
		t.Errorf("custom:role = %q", attrVal)
	}
}

// ---------- disable/enable self-guard ----------

func TestDisableUser_Self_400(t *testing.T) {
	mux, _ := setupUserServer(&auth.Principal{Sub: "self-sub", Role: "admin"})
	req := httptest.NewRequest("POST", "/api/v1/users/self-sub/disable", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cannot disable yourself") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestDisableUser_Other_200(t *testing.T) {
	mux, idp := setupUserServer(&auth.Principal{Sub: "admin-sub", Role: "admin"})
	called := false
	idp.adminDisableUser = func(_ context.Context, _ *cip.AdminDisableUserInput) (*cip.AdminDisableUserOutput, error) {
		called = true
		return &cip.AdminDisableUserOutput{}, nil
	}
	idp.adminGetUser = func(_ context.Context, _ *cip.AdminGetUserInput) (*cip.AdminGetUserOutput, error) {
		return &cip.AdminGetUserOutput{
			Username: aws.String("other-sub"),
			UserAttributes: []types.AttributeType{
				strAttr("email", "target@example.com"),
			},
			UserStatus: types.UserStatusTypeConfirmed,
			Enabled:    false,
		}, nil
	}
	req := httptest.NewRequest("POST", "/api/v1/users/other-sub/disable", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !called {
		t.Error("AdminDisableUser not called")
	}
}

// Enable-self is allowed (if you can call the API you're already enabled).
func TestEnableUser_Self_Allowed(t *testing.T) {
	mux, idp := setupUserServer(&auth.Principal{Sub: "self-sub", Role: "admin"})
	called := false
	idp.adminEnableUser = func(_ context.Context, _ *cip.AdminEnableUserInput) (*cip.AdminEnableUserOutput, error) {
		called = true
		return &cip.AdminEnableUserOutput{}, nil
	}
	idp.adminGetUser = func(_ context.Context, _ *cip.AdminGetUserInput) (*cip.AdminGetUserOutput, error) {
		return &cip.AdminGetUserOutput{
			Username: aws.String("self-sub"),
			UserAttributes: []types.AttributeType{
				strAttr("email", "me@example.com"),
			},
			UserStatus: types.UserStatusTypeConfirmed,
			Enabled:    true,
		}, nil
	}
	req := httptest.NewRequest("POST", "/api/v1/users/self-sub/enable", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !called {
		t.Errorf("status=%d called=%v body=%s", w.Code, called, w.Body.String())
	}
}

// ---------- delete + self-guard ----------

func TestDeleteUser_Self_400(t *testing.T) {
	mux, _ := setupUserServer(&auth.Principal{Sub: "self-sub", Role: "admin"})
	req := httptest.NewRequest("DELETE", "/api/v1/users/self-sub", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cannot delete yourself") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestDeleteUser_Other_204(t *testing.T) {
	mux, idp := setupUserServer(&auth.Principal{Sub: "admin-sub", Role: "admin"})
	idp.adminGetUser = func(_ context.Context, _ *cip.AdminGetUserInput) (*cip.AdminGetUserOutput, error) {
		return &cip.AdminGetUserOutput{
			UserAttributes: []types.AttributeType{
				strAttr("email", "goodbye@example.com"),
			},
		}, nil
	}
	called := false
	idp.adminDeleteUser = func(_ context.Context, in *cip.AdminDeleteUserInput) (*cip.AdminDeleteUserOutput, error) {
		if aws.ToString(in.Username) != "other-sub" {
			t.Errorf("Username = %q", aws.ToString(in.Username))
		}
		called = true
		return &cip.AdminDeleteUserOutput{}, nil
	}
	req := httptest.NewRequest("DELETE", "/api/v1/users/other-sub", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !called {
		t.Error("AdminDeleteUser not called")
	}
}

// ---------- RBAC regression: non-admin cannot hit /users/* ----------

func TestUsersRoutes_UserRole_403(t *testing.T) {
	mux := setupRBACServer("user")
	cases := []struct {
		method, path string
	}{
		{"GET", "/api/v1/users"},
		{"POST", "/api/v1/users"},
		{"PATCH", "/api/v1/users/someone"},
		{"POST", "/api/v1/users/someone/disable"},
		{"POST", "/api/v1/users/someone/enable"},
		{"POST", "/api/v1/users/someone/reset-password"},
		{"DELETE", "/api/v1/users/someone"},
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
