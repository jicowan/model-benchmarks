package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
