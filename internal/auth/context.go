// Package auth provides JWT verification, middleware, and authentication
// primitives for AccelBench (PRD-43).
//
// The middleware reads the `accelbench_access` cookie on every request,
// verifies it as a Cognito-issued access token (signature, iss, aud,
// token_use, exp), and attaches a *Principal to the request context.
// Handlers downstream call PrincipalFromContext to learn who made the
// request.
//
// When cfg.Disabled is true (AUTH_DISABLED=1 env), the middleware skips
// verification entirely and injects a synthetic principal. Intended only
// for local dev + CI — production should never enable this.
package auth

import "context"

// Principal identifies the authenticated user making a request. Fields
// come from Cognito JWT claims: Sub is the stable user UUID, Email comes
// from the ID token's "email" claim, and Role comes from the custom
// "custom:role" claim (enforced in PRD-44; present but unused in PRD-43).
type Principal struct {
	Sub   string
	Email string
	Role  string
}

// principalCtxKey is the unexported context key for the request principal.
// Using an unexported type prevents collisions with other packages.
type principalCtxKey struct{}

// WithPrincipal returns a child context carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFromContext returns the principal attached to ctx, or nil if
// the request was unauthenticated (which shouldn't happen on routes that
// go through Middleware — those always attach a principal or 401 first).
func PrincipalFromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalCtxKey{}).(*Principal)
	return p
}
