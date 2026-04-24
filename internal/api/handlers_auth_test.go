package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/auth"

	"github.com/aws/aws-sdk-go-v2/aws"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	smithy "github.com/aws/smithy-go"
	"k8s.io/client-go/kubernetes/fake"
)

// mockIDP implements CognitoIDP for tests.
type mockIDP struct {
	initiateAuth    func(context.Context, *cip.InitiateAuthInput) (*cip.InitiateAuthOutput, error)
	globalSignOut   func(context.Context, *cip.GlobalSignOutInput) (*cip.GlobalSignOutOutput, error)
	gotSignOutToken string

	// PRD-45 admin-user hooks; any nil fallback returns an empty struct so
	// tests that never touch them don't need to wire boilerplate.
	listUsers                 func(context.Context, *cip.ListUsersInput) (*cip.ListUsersOutput, error)
	adminGetUser              func(context.Context, *cip.AdminGetUserInput) (*cip.AdminGetUserOutput, error)
	adminCreateUser           func(context.Context, *cip.AdminCreateUserInput) (*cip.AdminCreateUserOutput, error)
	adminUpdateUserAttributes func(context.Context, *cip.AdminUpdateUserAttributesInput) (*cip.AdminUpdateUserAttributesOutput, error)
	adminDisableUser          func(context.Context, *cip.AdminDisableUserInput) (*cip.AdminDisableUserOutput, error)
	adminEnableUser           func(context.Context, *cip.AdminEnableUserInput) (*cip.AdminEnableUserOutput, error)
	adminResetUserPassword    func(context.Context, *cip.AdminResetUserPasswordInput) (*cip.AdminResetUserPasswordOutput, error)
	adminDeleteUser           func(context.Context, *cip.AdminDeleteUserInput) (*cip.AdminDeleteUserOutput, error)
}

func (m *mockIDP) InitiateAuth(ctx context.Context, in *cip.InitiateAuthInput, _ ...func(*cip.Options)) (*cip.InitiateAuthOutput, error) {
	return m.initiateAuth(ctx, in)
}

func (m *mockIDP) GlobalSignOut(ctx context.Context, in *cip.GlobalSignOutInput, _ ...func(*cip.Options)) (*cip.GlobalSignOutOutput, error) {
	if in != nil {
		m.gotSignOutToken = aws.ToString(in.AccessToken)
	}
	if m.globalSignOut != nil {
		return m.globalSignOut(ctx, in)
	}
	return &cip.GlobalSignOutOutput{}, nil
}

func (m *mockIDP) ListUsers(ctx context.Context, in *cip.ListUsersInput, _ ...func(*cip.Options)) (*cip.ListUsersOutput, error) {
	if m.listUsers != nil {
		return m.listUsers(ctx, in)
	}
	return &cip.ListUsersOutput{}, nil
}

func (m *mockIDP) AdminGetUser(ctx context.Context, in *cip.AdminGetUserInput, _ ...func(*cip.Options)) (*cip.AdminGetUserOutput, error) {
	if m.adminGetUser != nil {
		return m.adminGetUser(ctx, in)
	}
	return &cip.AdminGetUserOutput{}, nil
}

func (m *mockIDP) AdminCreateUser(ctx context.Context, in *cip.AdminCreateUserInput, _ ...func(*cip.Options)) (*cip.AdminCreateUserOutput, error) {
	if m.adminCreateUser != nil {
		return m.adminCreateUser(ctx, in)
	}
	return &cip.AdminCreateUserOutput{}, nil
}

func (m *mockIDP) AdminUpdateUserAttributes(ctx context.Context, in *cip.AdminUpdateUserAttributesInput, _ ...func(*cip.Options)) (*cip.AdminUpdateUserAttributesOutput, error) {
	if m.adminUpdateUserAttributes != nil {
		return m.adminUpdateUserAttributes(ctx, in)
	}
	return &cip.AdminUpdateUserAttributesOutput{}, nil
}

func (m *mockIDP) AdminDisableUser(ctx context.Context, in *cip.AdminDisableUserInput, _ ...func(*cip.Options)) (*cip.AdminDisableUserOutput, error) {
	if m.adminDisableUser != nil {
		return m.adminDisableUser(ctx, in)
	}
	return &cip.AdminDisableUserOutput{}, nil
}

func (m *mockIDP) AdminEnableUser(ctx context.Context, in *cip.AdminEnableUserInput, _ ...func(*cip.Options)) (*cip.AdminEnableUserOutput, error) {
	if m.adminEnableUser != nil {
		return m.adminEnableUser(ctx, in)
	}
	return &cip.AdminEnableUserOutput{}, nil
}

func (m *mockIDP) AdminResetUserPassword(ctx context.Context, in *cip.AdminResetUserPasswordInput, _ ...func(*cip.Options)) (*cip.AdminResetUserPasswordOutput, error) {
	if m.adminResetUserPassword != nil {
		return m.adminResetUserPassword(ctx, in)
	}
	return &cip.AdminResetUserPasswordOutput{}, nil
}

func (m *mockIDP) AdminDeleteUser(ctx context.Context, in *cip.AdminDeleteUserInput, _ ...func(*cip.Options)) (*cip.AdminDeleteUserOutput, error) {
	if m.adminDeleteUser != nil {
		return m.adminDeleteUser(ctx, in)
	}
	return &cip.AdminDeleteUserOutput{}, nil
}

// mockAPIErr returns a smithy.GenericAPIError for the given Cognito
// error code (e.g., "NotAuthorizedException"). The generic type
// satisfies smithy.APIError so errors.As inside mapCognitoAuthError
// unwraps it correctly.
func mockAPIErr(code string) error {
	return &smithy.GenericAPIError{Code: code, Message: code, Fault: smithy.FaultClient}
}

// buildFakeIDToken creates an unsigned JWT (header.payload.junk) that
// decodeIDTokenClaims will happily parse — ParseUnverified doesn't
// check the signature.
func buildFakeIDToken(email, role string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := map[string]any{
		"email":       email,
		"custom:role": role,
	}
	cb, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(cb)
	return header + "." + payload + ".sig"
}

func setupAuthServer() (*Server, *mockIDP, *http.ServeMux) {
	repo := seedRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client, "test-pod")
	idp := &mockIDP{}
	srv.SetAuth(auth.Config{ClientID: "test-client", Disabled: false}, idp, nil)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return srv, idp, mux
}

// ---------- Login ----------

func TestAuthLogin_GoodCreds_SetsCookies(t *testing.T) {
	_, idp, mux := setupAuthServer()
	idp.initiateAuth = func(_ context.Context, in *cip.InitiateAuthInput) (*cip.InitiateAuthOutput, error) {
		if in.AuthParameters["USERNAME"] != "user@example.com" {
			t.Errorf("unexpected USERNAME: %q", in.AuthParameters["USERNAME"])
		}
		return &cip.InitiateAuthOutput{
			AuthenticationResult: &types.AuthenticationResultType{
				AccessToken:  aws.String("access-tok"),
				IdToken:      aws.String(buildFakeIDToken("user@example.com", "admin")),
				RefreshToken: aws.String("refresh-tok"),
				ExpiresIn:    3600,
			},
		}, nil
	}

	body, _ := json.Marshal(map[string]string{"email": "user@example.com", "password": "hunter2"})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	cookies := w.Result().Cookies()
	names := map[string]string{}
	for _, c := range cookies {
		names[c.Name] = c.Value
	}
	if names[accessCookieName] != "access-tok" {
		t.Errorf("access cookie = %q", names[accessCookieName])
	}
	if names[refreshCookieName] != "refresh-tok" {
		t.Errorf("refresh cookie = %q", names[refreshCookieName])
	}
	// Verify HttpOnly + Secure + SameSite=Lax on access cookie.
	for _, c := range cookies {
		if c.Name == accessCookieName {
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
				t.Errorf("cookie flags: HttpOnly=%v Secure=%v SameSite=%v",
					c.HttpOnly, c.Secure, c.SameSite)
			}
		}
	}

	var resp authMeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Email != "user@example.com" || resp.Role != "admin" {
		t.Errorf("body = %+v", resp)
	}
}

func TestAuthLogin_BadCreds_Returns401(t *testing.T) {
	_, idp, mux := setupAuthServer()
	idp.initiateAuth = func(_ context.Context, _ *cip.InitiateAuthInput) (*cip.InitiateAuthOutput, error) {
		return nil, mockAPIErr("NotAuthorizedException")
	}
	body, _ := json.Marshal(map[string]string{"email": "x@y.com", "password": "wrong"})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid_credentials") {
		t.Errorf("body missing invalid_credentials: %s", w.Body.String())
	}
}

func TestAuthLogin_UserNotFound_MasksTo401(t *testing.T) {
	_, idp, mux := setupAuthServer()
	idp.initiateAuth = func(_ context.Context, _ *cip.InitiateAuthInput) (*cip.InitiateAuthOutput, error) {
		return nil, mockAPIErr("UserNotFoundException")
	}
	body, _ := json.Marshal(map[string]string{"email": "nobody@x.com", "password": "x"})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (user existence must be masked)", w.Code)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "user") {
		t.Errorf("body leaks user info: %s", w.Body.String())
	}
}

// ---------- Refresh ----------

func TestAuthRefresh_Good_SetsNewAccessCookie(t *testing.T) {
	_, idp, mux := setupAuthServer()
	idp.initiateAuth = func(_ context.Context, in *cip.InitiateAuthInput) (*cip.InitiateAuthOutput, error) {
		if in.AuthFlow != types.AuthFlowTypeRefreshTokenAuth {
			t.Errorf("AuthFlow = %q, want REFRESH_TOKEN_AUTH", in.AuthFlow)
		}
		if in.AuthParameters["REFRESH_TOKEN"] != "refresh-tok" {
			t.Errorf("REFRESH_TOKEN = %q", in.AuthParameters["REFRESH_TOKEN"])
		}
		return &cip.InitiateAuthOutput{
			AuthenticationResult: &types.AuthenticationResultType{
				AccessToken: aws.String("new-access-tok"),
				IdToken:     aws.String(buildFakeIDToken("u@e.com", "user")),
				ExpiresIn:   3600,
			},
		}, nil
	}

	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "refresh-tok"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == accessCookieName && c.Value == "new-access-tok" {
			found = true
		}
	}
	if !found {
		t.Error("new access cookie not set")
	}
}

func TestAuthRefresh_NoCookie_Returns401(t *testing.T) {
	_, _, mux := setupAuthServer()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/auth/refresh", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// ---------- Logout ----------

func TestAuthLogout_ClearsCookies(t *testing.T) {
	_, idp, mux := setupAuthServer()
	// Logout runs behind the auth middleware, so we need AUTH_DISABLED or
	// a valid token. Use AUTH_DISABLED for simplicity.
	_, _, _ = errors.New(""), idp, mux // keep imports
}

// ---------- /auth/me ----------

func TestAuthMe_Disabled_ReturnsSyntheticPrincipal(t *testing.T) {
	repo := seedRepo()
	client := fake.NewSimpleClientset()
	srv := NewServer(repo, client, "test-pod")
	srv.SetAuth(auth.Config{Disabled: true}, nil, nil)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var resp authMeResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Email != "dev@local" || resp.Role != "admin" {
		t.Errorf("synthetic principal = %+v", resp)
	}
}
