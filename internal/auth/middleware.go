package auth

import (
	"encoding/json"
	"errors"
	"net/http"
)

// AccessCookieName is the HttpOnly cookie carrying the access token.
const AccessCookieName = "accelbench_access"

// Middleware returns an HTTP middleware that enforces authentication
// on every wrapped request. Flow:
//
//  1. If cfg.Disabled is true, inject a synthetic principal and call next.
//  2. Otherwise read the `accelbench_access` cookie, verify the token as
//     an access token, and attach the resulting *Principal to the
//     request context for the handler chain.
//
// Missing/invalid/expired token → 401 with a JSON body. Any downstream
// handler that wants the caller can read it with PrincipalFromContext.
func Middleware(cfg Config, verifier *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.Disabled {
				p := &Principal{Sub: "local-dev", Email: "dev@local", Role: "admin"}
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
				return
			}

			cookie, err := r.Cookie(AccessCookieName)
			if err != nil {
				writeUnauthorized(w, "missing_token")
				return
			}
			principal, err := verifier.VerifyToken(r.Context(), cookie.Value, "access")
			if err != nil {
				if errors.Is(err, ErrInvalidToken) {
					writeUnauthorized(w, "invalid_token")
				} else {
					writeUnauthorized(w, "auth_error")
				}
				return
			}

			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
		})
	}
}

func writeUnauthorized(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
