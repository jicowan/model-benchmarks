package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// authTest bundles everything a test needs: a running JWKS server,
// a signer, and a ready-to-use Config + Verifier.
type authTest struct {
	t        *testing.T
	key      *rsa.PrivateKey
	kid      string
	cfg      Config
	verifier *Verifier
	jwksSrv  *httptest.Server
}

func newAuthTest(t *testing.T) *authTest {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	kid := "test-kid"

	// Fake JWKS endpoint serving just our test key.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(bigEndianBytes(uint64(key.E)))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA", "alg": "RS256", "use": "sig",
				"kid": kid, "n": n, "e": e,
			}},
		})
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		UserPoolID: "us-east-2_test",
		ClientID:   "test-client",
		Region:     "us-east-2",
	}
	jwks := NewJWKSFetcher(cfg)
	jwks.SetURL(srv.URL)
	verifier := NewVerifier(cfg, jwks)

	return &authTest{t: t, key: key, kid: kid, cfg: cfg, verifier: verifier, jwksSrv: srv}
}

// signToken mints a test JWT with the given claims, signed by our test
// key and carrying our test kid in the header.
func (a *authTest) signToken(claims jwt.MapClaims) string {
	a.t.Helper()
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	t.Header["kid"] = a.kid
	s, err := t.SignedString(a.key)
	if err != nil {
		a.t.Fatalf("sign: %v", err)
	}
	return s
}

func (a *authTest) validAccessClaims() jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss":       a.cfg.Issuer(),
		"client_id": a.cfg.ClientID,
		"token_use": "access",
		"sub":       "user-sub-123",
		"exp":       now.Add(1 * time.Hour).Unix(),
		"iat":       now.Unix(),
	}
}

func bigEndianBytes(n uint64) []byte {
	if n == 0 {
		return []byte{0}
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte(n & 0xff)}, out...)
		n >>= 8
	}
	return out
}

// ---------- Middleware tests ----------

func TestMiddleware_ValidToken_Passes(t *testing.T) {
	a := newAuthTest(t)

	var gotPrincipal *Principal
	handler := Middleware(a.cfg, a.verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPrincipal = PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: AccessCookieName, Value: a.signToken(a.validAccessClaims())})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotPrincipal == nil || gotPrincipal.Sub != "user-sub-123" {
		t.Errorf("principal = %+v, want sub=user-sub-123", gotPrincipal)
	}
}

func TestMiddleware_MissingCookie_Returns401(t *testing.T) {
	a := newAuthTest(t)
	handler := Middleware(a.cfg, a.verifier)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not have been called")
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_ExpiredToken_Returns401(t *testing.T) {
	a := newAuthTest(t)
	claims := a.validAccessClaims()
	claims["exp"] = time.Now().Add(-1 * time.Hour).Unix()
	handler := Middleware(a.cfg, a.verifier)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called")
	}))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: AccessCookieName, Value: a.signToken(claims)})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_WrongAudience_Returns401(t *testing.T) {
	a := newAuthTest(t)
	claims := a.validAccessClaims()
	claims["client_id"] = "some-other-client"
	handler := Middleware(a.cfg, a.verifier)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called")
	}))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: AccessCookieName, Value: a.signToken(claims)})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_WrongIssuer_Returns401(t *testing.T) {
	a := newAuthTest(t)
	claims := a.validAccessClaims()
	claims["iss"] = "https://cognito-idp.us-east-2.amazonaws.com/us-east-2_OTHER"
	handler := Middleware(a.cfg, a.verifier)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called")
	}))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: AccessCookieName, Value: a.signToken(claims)})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// An ID token passed as the access cookie must fail — token_use mismatch.
func TestMiddleware_WrongTokenUse_Returns401(t *testing.T) {
	a := newAuthTest(t)
	claims := a.validAccessClaims()
	claims["token_use"] = "id"
	delete(claims, "client_id")
	claims["aud"] = a.cfg.ClientID
	handler := Middleware(a.cfg, a.verifier)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called")
	}))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: AccessCookieName, Value: a.signToken(claims)})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_AuthDisabled_SynthesizesPrincipal(t *testing.T) {
	cfg := Config{Disabled: true}
	var gotPrincipal *Principal
	handler := Middleware(cfg, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPrincipal = PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	// No cookie, no verifier — still passes.
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotPrincipal == nil || gotPrincipal.Email != "dev@local" || gotPrincipal.Role != "admin" {
		t.Errorf("principal = %+v, want {dev@local, admin}", gotPrincipal)
	}
}

// ---------- PRD-44: RequireRole + ID-cookie fallback ----------

// validIDClaims mirrors validAccessClaims but carries an email and
// custom:role, matching what a Cognito ID token actually contains.
func (a *authTest) validIDClaims(email, role string) jwt.MapClaims {
	now := time.Now()
	c := jwt.MapClaims{
		"iss":       a.cfg.Issuer(),
		"aud":       a.cfg.ClientID,
		"token_use": "id",
		"sub":       "user-sub-123",
		"email":     email,
		"exp":       now.Add(1 * time.Hour).Unix(),
		"iat":       now.Unix(),
	}
	if role != "" {
		c["custom:role"] = role
	}
	return c
}

// PRD-44: access tokens don't carry custom:role, so the middleware
// must fall back to the ID cookie to learn the caller's role.
func TestMiddleware_FallsBackToIDCookieForRole(t *testing.T) {
	a := newAuthTest(t)

	var gotPrincipal *Principal
	handler := Middleware(a.cfg, a.verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPrincipal = PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: AccessCookieName, Value: a.signToken(a.validAccessClaims())})
	req.AddCookie(&http.Cookie{Name: IDCookieName, Value: a.signToken(a.validIDClaims("jeremy@example.com", "admin"))})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotPrincipal == nil {
		t.Fatal("no principal on context")
	}
	if gotPrincipal.Email != "jeremy@example.com" {
		t.Errorf("email = %q, want jeremy@example.com", gotPrincipal.Email)
	}
	if gotPrincipal.Role != "admin" {
		t.Errorf("role = %q, want admin", gotPrincipal.Role)
	}
}

// Access token passes but no ID cookie is present → role defaults to "user".
func TestMiddleware_DefaultsMissingRoleToUser(t *testing.T) {
	a := newAuthTest(t)

	var gotPrincipal *Principal
	handler := Middleware(a.cfg, a.verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPrincipal = PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: AccessCookieName, Value: a.signToken(a.validAccessClaims())})
	// No ID cookie set.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotPrincipal == nil || gotPrincipal.Role != "user" {
		t.Errorf("role = %v, want user", gotPrincipal)
	}
}

func TestRequireRole_AdminPasses(t *testing.T) {
	var called bool
	h := RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/protected", nil)
	req = req.WithContext(WithPrincipal(req.Context(), &Principal{Role: "admin"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !called {
		t.Error("next handler was not invoked")
	}
}

func TestRequireRole_UserBlocked(t *testing.T) {
	h := RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called")
	}))
	req := httptest.NewRequest("GET", "/protected", nil)
	req = req.WithContext(WithPrincipal(req.Context(), &Principal{Role: "user"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "forbidden: admin required") {
		t.Errorf("body = %q, want contains 'forbidden: admin required'", rec.Body.String())
	}
}

func TestRequireRole_NoPrincipalBlocked(t *testing.T) {
	h := RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called")
	}))
	// No principal on context.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// Extra: ID-token verification path (used by the login handler).
func TestVerify_IDToken_Accepts(t *testing.T) {
	a := newAuthTest(t)
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":         a.cfg.Issuer(),
		"aud":         a.cfg.ClientID,
		"token_use":   "id",
		"sub":         "user-sub-123",
		"email":       "jeremy@example.com",
		"custom:role": "admin",
		"exp":         now.Add(1 * time.Hour).Unix(),
		"iat":         now.Unix(),
	}
	p, err := a.verifier.VerifyToken(t.Context(), a.signToken(claims), "id")
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if p.Email != "jeremy@example.com" || p.Role != "admin" {
		t.Errorf("principal = %+v, want email+role populated", p)
	}
}
