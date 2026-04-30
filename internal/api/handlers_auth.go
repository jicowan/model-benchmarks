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
// that our handlers call. Defining it here lets tests pass a
// lightweight mock instead of a real AWS client. PRD-43 added the two
// auth methods; PRD-45 added the eight admin-user-management methods.
type CognitoIDP interface {
	InitiateAuth(ctx context.Context, in *cip.InitiateAuthInput, optFns ...func(*cip.Options)) (*cip.InitiateAuthOutput, error)
	RespondToAuthChallenge(ctx context.Context, in *cip.RespondToAuthChallengeInput, optFns ...func(*cip.Options)) (*cip.RespondToAuthChallengeOutput, error)
	GlobalSignOut(ctx context.Context, in *cip.GlobalSignOutInput, optFns ...func(*cip.Options)) (*cip.GlobalSignOutOutput, error)

	ListUsers(ctx context.Context, in *cip.ListUsersInput, optFns ...func(*cip.Options)) (*cip.ListUsersOutput, error)
	AdminGetUser(ctx context.Context, in *cip.AdminGetUserInput, optFns ...func(*cip.Options)) (*cip.AdminGetUserOutput, error)
	AdminCreateUser(ctx context.Context, in *cip.AdminCreateUserInput, optFns ...func(*cip.Options)) (*cip.AdminCreateUserOutput, error)
	AdminUpdateUserAttributes(ctx context.Context, in *cip.AdminUpdateUserAttributesInput, optFns ...func(*cip.Options)) (*cip.AdminUpdateUserAttributesOutput, error)
	AdminDisableUser(ctx context.Context, in *cip.AdminDisableUserInput, optFns ...func(*cip.Options)) (*cip.AdminDisableUserOutput, error)
	AdminEnableUser(ctx context.Context, in *cip.AdminEnableUserInput, optFns ...func(*cip.Options)) (*cip.AdminEnableUserOutput, error)
	AdminResetUserPassword(ctx context.Context, in *cip.AdminResetUserPasswordInput, optFns ...func(*cip.Options)) (*cip.AdminResetUserPasswordOutput, error)
	AdminDeleteUser(ctx context.Context, in *cip.AdminDeleteUserInput, optFns ...func(*cip.Options)) (*cip.AdminDeleteUserOutput, error)
}

// Cookie names: re-exported from the auth package so it owns the source
// of truth. See internal/auth/middleware.go.
const (
	accessCookieName  = auth.AccessCookieName
	idCookieName      = auth.IDCookieName
	refreshCookieName = auth.RefreshCookieName
)

const refreshCookieMaxAge = 30 * 24 * 60 * 60 // 30 days in seconds

// ---------- Handlers ----------

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authMeResponse struct {
	Sub   string `json:"sub,omitempty"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// loginChallengeResponse is returned by /auth/login when Cognito requires
// a follow-up challenge (e.g. NEW_PASSWORD_REQUIRED for invited users).
// The client presents the right form, then posts to /auth/respond-challenge
// with session + the challenge-specific inputs.
type loginChallengeResponse struct {
	Challenge string `json:"challenge"`
	Session   string `json:"session"`
	Email     string `json:"email"`
}

type respondChallengeRequest struct {
	Challenge   string `json:"challenge"`
	Session     string `json:"session"`
	Email       string `json:"email"`
	NewPassword string `json:"new_password"`
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
		// Challenge flows. The only one AccelBench supports end-to-end is
		// NEW_PASSWORD_REQUIRED — invited users hit this on first login with
		// their temporary password. Other challenges (SMS_MFA, etc.) fall
		// through to a generic error; Cognito isn't configured to emit them.
		if out.ChallengeName == types.ChallengeNameTypeNewPasswordRequired {
			writeJSON(w, http.StatusOK, loginChallengeResponse{
				Challenge: "new_password_required",
				Session:   aws.ToString(out.Session),
				Email:     req.Email,
			})
			return
		}
		writeError(w, http.StatusForbidden, "challenge_required")
		return
	}

	setAuthCookies(w, out.AuthenticationResult)

	// Decode the ID token (without verification — we just got it back
	// from Cognito over TLS, we trust it) to extract sub + email + role
	// for the response body.
	sub, email, role := decodeIDTokenClaims(aws.ToString(out.AuthenticationResult.IdToken))
	writeJSON(w, http.StatusOK, authMeResponse{Sub: sub, Email: email, Role: role})
}

// handleAuthRespondChallenge finishes a login flow that /auth/login left in
// a challenge state. Only NEW_PASSWORD_REQUIRED is supported. On success,
// behaves identically to a normal login: cookies + {sub, email, role}.
func (s *Server) handleAuthRespondChallenge(w http.ResponseWriter, r *http.Request) {
	if s.cognitoIDP == nil {
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	var req respondChallengeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if req.Challenge != "new_password_required" {
		writeError(w, http.StatusBadRequest, "unsupported_challenge")
		return
	}
	if req.Session == "" || req.Email == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "missing_fields")
		return
	}

	out, err := s.cognitoIDP.RespondToAuthChallenge(r.Context(), &cip.RespondToAuthChallengeInput{
		ClientId:      aws.String(s.authConfig.ClientID),
		ChallengeName: types.ChallengeNameTypeNewPasswordRequired,
		Session:       aws.String(req.Session),
		ChallengeResponses: map[string]string{
			"USERNAME":     req.Email,
			"NEW_PASSWORD": req.NewPassword,
		},
	})
	if err != nil {
		mapCognitoAuthError(w, err)
		return
	}
	if out.AuthenticationResult == nil {
		// Multi-step challenges aren't expected for AccelBench's pool
		// config. Fail loudly rather than silently handing the client
		// another session.
		writeError(w, http.StatusForbidden, "challenge_required")
		return
	}

	setAuthCookies(w, out.AuthenticationResult)
	sub, email, role := decodeIDTokenClaims(aws.ToString(out.AuthenticationResult.IdToken))
	writeJSON(w, http.StatusOK, authMeResponse{Sub: sub, Email: email, Role: role})
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
	sub, email, role := p.Sub, p.Email, p.Role
	if email == "" {
		if c, err := r.Cookie(idCookieName); err == nil {
			idSub, idEmail, idRole := decodeIDTokenClaims(c.Value)
			if sub == "" {
				sub = idSub
			}
			email, role = idEmail, idRole
		}
	}
	writeJSON(w, http.StatusOK, authMeResponse{Sub: sub, Email: email, Role: role})
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
		case "InvalidPasswordException", "InvalidParameterException":
			// Surfaces when NEW_PASSWORD_REQUIRED gets a password that
			// violates the pool's policy (length, symbols, etc.).
			writeError(w, http.StatusBadRequest, "invalid_password")
			return
		}
	}
	log.Printf("[auth] upstream error: %v", err)
	writeError(w, http.StatusBadGateway, "upstream_error")
}

// decodeIDTokenClaims returns (sub, email, role) from the ID token's
// payload. Does NOT verify the signature — callers must only pass
// tokens whose provenance is already trusted:
//   - /auth/login and /auth/respond-challenge pass the ID token
//     returned by Cognito over TLS in the same response, so signature
//     verification would be belt-and-braces.
//   - /auth/me reads the ID token from the HttpOnly+Secure cookie only
//     after auth.Middleware has verified the ACCESS token and attached
//     a Principal to the request context; the ID token is consumed for
//     display claims only, never for authorization.
//
// Returning empty strings on parse failure is safe: handleAuthMe falls
// back to the (verified) Principal from the middleware. CodeQL's
// go/missing-jwt-signature-check rule flags this function; the alert
// should be dismissed as a false positive in the Security tab with a
// reference to this docstring.
func decodeIDTokenClaims(idToken string) (sub, email, role string) {
	if idToken == "" {
		return "", "", ""
	}
	var claims jwt.MapClaims
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(idToken, &claims); err != nil {
		return "", "", ""
	}
	if v, ok := claims["sub"].(string); ok {
		sub = v
	}
	if v, ok := claims["email"].(string); ok {
		email = v
	}
	if v, ok := claims["custom:role"].(string); ok {
		role = v
	}
	return sub, email, role
}
