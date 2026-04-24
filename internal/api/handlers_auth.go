package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/accelbench/accelbench/internal/auth"

	"github.com/aws/aws-sdk-go-v2/aws"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	smithy "github.com/aws/smithy-go"
	"github.com/golang-jwt/jwt/v5"
)

// CognitoIDP is the minimal surface of cognitoidentityprovider.Client
// that our auth handlers call. Defining it here lets tests pass a
// lightweight mock instead of a real AWS client.
type CognitoIDP interface {
	InitiateAuth(ctx context.Context, in *cip.InitiateAuthInput, optFns ...func(*cip.Options)) (*cip.InitiateAuthOutput, error)
	GlobalSignOut(ctx context.Context, in *cip.GlobalSignOutInput, optFns ...func(*cip.Options)) (*cip.GlobalSignOutOutput, error)
}

// Cookie names carrying the three tokens.
const (
	accessCookieName  = "accelbench_access"
	idCookieName      = "accelbench_id"
	refreshCookieName = "accelbench_refresh"
)

const refreshCookieMaxAge = 30 * 24 * 60 * 60 // 30 days in seconds

// ---------- Handlers ----------

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authMeResponse struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// handleAuthLogin authenticates a user with USER_PASSWORD_AUTH and sets
// three HttpOnly cookies (ID, access, refresh). Returns {email, role}
// in the body for immediate UI use.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.cognitoIDP == nil {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "missing_credentials")
		return
	}

	out, err := s.cognitoIDP.InitiateAuth(r.Context(), &cip.InitiateAuthInput{
		AuthFlow: types.AuthFlowTypeUserPasswordAuth,
		ClientId: aws.String(s.authConfig.ClientID),
		AuthParameters: map[string]string{
			"USERNAME": req.Email,
			"PASSWORD": req.Password,
		},
	})
	if err != nil {
		mapCognitoAuthError(w, err)
		return
	}
	if out.AuthenticationResult == nil {
		// Challenge flows (NEW_PASSWORD_REQUIRED, SMS_MFA, etc.) land here.
		// Out of scope for PRD-43; operator resets via console.
		writeError(w, http.StatusForbidden, "challenge_required")
		return
	}

	setAuthCookies(w, out.AuthenticationResult)

	// Decode the ID token (without verification — we just got it back
	// from Cognito over TLS, we trust it) to extract email + role for
	// the response body.
	email, role := decodeIDTokenClaims(aws.ToString(out.AuthenticationResult.IdToken))
	writeJSON(w, http.StatusOK, authMeResponse{Email: email, Role: role})
}

// handleAuthLogout clears cookies and calls GlobalSignOut to invalidate
// the refresh token server-side (new refreshes will fail pool-wide).
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(accessCookieName); err == nil && s.cognitoIDP != nil {
		// Best-effort; log + continue on error.
		if _, err := s.cognitoIDP.GlobalSignOut(r.Context(), &cip.GlobalSignOutInput{
			AccessToken: aws.String(c.Value),
		}); err != nil {
			log.Printf("[auth] GlobalSignOut failed: %v", err)
		}
	}
	clearAuthCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// handleAuthRefresh exchanges a refresh token for a fresh access + ID
// token. Refresh tokens are not rotated by Cognito by default.
func (s *Server) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	if s.cognitoIDP == nil {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	c, err := r.Cookie(refreshCookieName)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "missing_refresh_token")
		return
	}
	out, err := s.cognitoIDP.InitiateAuth(r.Context(), &cip.InitiateAuthInput{
		AuthFlow: types.AuthFlowTypeRefreshTokenAuth,
		ClientId: aws.String(s.authConfig.ClientID),
		AuthParameters: map[string]string{
			"REFRESH_TOKEN": c.Value,
		},
	})
	if err != nil {
		mapCognitoAuthError(w, err)
		return
	}
	if out.AuthenticationResult == nil {
		writeError(w, http.StatusUnauthorized, "refresh_failed")
		return
	}

	// REFRESH_TOKEN_AUTH returns access + id but not a new refresh token
	// (Cognito reuses the existing one). Set only what we got back.
	setAuthCookies(w, out.AuthenticationResult)
	w.WriteHeader(http.StatusNoContent)
}

// handleAuthMe returns the caller's email + role. Middleware has already
// validated the token and attached the principal.
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		writeError(w, http.StatusUnauthorized, "no_principal")
		return
	}
	// Access tokens don't carry email/role, so if the context principal
	// came from one we need to read the ID token cookie for those claims.
	email, role := p.Email, p.Role
	if email == "" {
		if c, err := r.Cookie(idCookieName); err == nil {
			email, role = decodeIDTokenClaims(c.Value)
		}
	}
	writeJSON(w, http.StatusOK, authMeResponse{Email: email, Role: role})
}

// ---------- Helpers ----------

func setAuthCookies(w http.ResponseWriter, result *types.AuthenticationResultType) {
	accessTTL := int(result.ExpiresIn)
	if accessTTL <= 0 {
		accessTTL = 3600
	}
	setCookie(w, accessCookieName, aws.ToString(result.AccessToken), accessTTL)
	setCookie(w, idCookieName, aws.ToString(result.IdToken), accessTTL)
	if rt := aws.ToString(result.RefreshToken); rt != "" {
		setCookie(w, refreshCookieName, rt, refreshCookieMaxAge)
	}
}

func clearAuthCookies(w http.ResponseWriter) {
	setCookie(w, accessCookieName, "", -1)
	setCookie(w, idCookieName, "", -1)
	setCookie(w, refreshCookieName, "", -1)
}

func setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// mapCognitoAuthError translates Cognito errors to HTTP + opaque codes.
// We deliberately collapse several "exists but can't authenticate"
// cases to invalid_credentials so the response doesn't leak whether an
// account exists.
func mapCognitoAuthError(w http.ResponseWriter, err error) {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotAuthorizedException", "UserNotFoundException":
			writeError(w, http.StatusUnauthorized, "invalid_credentials")
			return
		case "UserNotConfirmedException":
			writeError(w, http.StatusForbidden, "user_not_confirmed")
			return
		case "PasswordResetRequiredException":
			writeError(w, http.StatusForbidden, "password_reset_required")
			return
		}
	}
	log.Printf("[auth] upstream error: %v", err)
	writeError(w, http.StatusBadGateway, "upstream_error")
}

// decodeIDTokenClaims returns (email, role) from the ID token's payload.
// Does NOT verify the signature — only used for tokens Cognito just
// handed us over TLS in an InitiateAuth response.
func decodeIDTokenClaims(idToken string) (email, role string) {
	if idToken == "" {
		return "", ""
	}
	var claims jwt.MapClaims
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(idToken, &claims); err != nil {
		return "", ""
	}
	if v, ok := claims["email"].(string); ok {
		email = v
	}
	if v, ok := claims["custom:role"].(string); ok {
		role = v
	}
	return email, role
}
