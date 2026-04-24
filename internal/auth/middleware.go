package auth

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Cookie names for the three JWTs AccelBench issues on login. Owned by
// this package so both the middleware and the auth handlers reference a
// single source of truth.
const (
	AccessCookieName  = "accelbench_access"
	IDCookieName      = "accelbench_id"
	RefreshCookieName = "accelbench_refresh"
)

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
				// Respect any principal upstream already stamped (tests
				// do this to exercise RequireRole with specific roles).
				// Otherwise inject the synthetic local-dev admin.
				if existing := PrincipalFromContext(r.Context()); existing != nil {
					next.ServeHTTP(w, r)
					return
				}
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

			// PRD-44: access tokens don't carry `custom:role` or `email` —
			// those live on the ID token. If they're missing, verify the
			// ID cookie and copy them over so every downstream handler
			// (including RequireRole) sees a complete principal. Failure
			// to read/verify the ID cookie is non-fatal: the principal
			// just stays with whatever the access token provided, and
			// RequireRole will reject when appropriate.
			if principal.Role == "" || principal.Email == "" {
				if idCookie, err := r.Cookie(IDCookieName); err == nil {
					if idPrincipal, err := verifier.VerifyToken(r.Context(), idCookie.Value, "id"); err == nil {
						if principal.Role == "" {
							principal.Role = idPrincipal.Role
						}
						if principal.Email == "" {
							principal.Email = idPrincipal.Email
						}
					}
				}
			}

			// PRD-44: default missing role to "user" so accounts without an
			// explicit custom:role attribute get user-level access rather
			// than an empty role (which RequireRole treats as neither
			// admin nor user).
			if principal.Role == "" {
				principal.Role = "user"
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

func writeForbidden(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// RequireRole returns middleware that rejects requests whose principal
// doesn't match the given role. Meant to run *after* Middleware has
// placed a Principal on the request context; a missing principal is
// treated as 401 (defensive — should only happen if something bypassed
// the JWT middleware). Role mismatch returns 403 with a JSON body
// identifying the required role, matching the shape of the other
// middleware error bodies.
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := PrincipalFromContext(r.Context())
			if p == nil {
				writeUnauthorized(w, "no_principal")
				return
			}
			if p.Role != role {
				writeForbidden(w, "forbidden: "+role+" required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
