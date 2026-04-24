package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken wraps any token-verification failure. Handlers map
// this to HTTP 401 without exposing details to the caller.
var ErrInvalidToken = errors.New("invalid token")

// Verifier checks a Cognito JWT's signature and claims.
type Verifier struct {
	cfg  Config
	jwks *JWKSFetcher
}

// NewVerifier returns a verifier bound to cfg, fetching keys via jwks.
func NewVerifier(cfg Config, jwks *JWKSFetcher) *Verifier {
	return &Verifier{cfg: cfg, jwks: jwks}
}

// VerifyToken validates tokenStr and, on success, returns a *Principal.
// expectedUse is the required "token_use" claim: "access" for access
// tokens, "id" for ID tokens. Access tokens carry "client_id" as the
// audience; ID tokens carry "aud".
func (v *Verifier) VerifyToken(ctx context.Context, tokenStr, expectedUse string) (*Principal, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.cfg.Issuer()),
		jwt.WithLeeway(30*time.Second),
	)

	var claims jwt.MapClaims
	_, err := parser.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("missing kid in token header")
		}
		return v.jwks.KeyForKid(ctx, kid)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	// token_use check: "access" vs "id".
	tokenUse, _ := claims["token_use"].(string)
	if tokenUse != expectedUse {
		return nil, fmt.Errorf("%w: token_use=%q, want %q", ErrInvalidToken, tokenUse, expectedUse)
	}

	// Audience check differs by token type:
	//   - ID tokens:    aud == client_id
	//   - access tokens: client_id claim == client_id
	var audOK bool
	switch expectedUse {
	case "id":
		if aud, _ := claims["aud"].(string); aud == v.cfg.ClientID {
			audOK = true
		}
	case "access":
		if cid, _ := claims["client_id"].(string); cid == v.cfg.ClientID {
			audOK = true
		}
	}
	if !audOK {
		return nil, fmt.Errorf("%w: audience/client_id mismatch", ErrInvalidToken)
	}

	p := &Principal{}
	if sub, ok := claims["sub"].(string); ok {
		p.Sub = sub
	}
	if email, ok := claims["email"].(string); ok {
		p.Email = email
	}
	// Cognito puts custom attributes in the ID token only, under
	// "custom:role". Access tokens don't carry them. PRD-44 will enforce
	// the role — this PRD just extracts it when available.
	if role, ok := claims["custom:role"].(string); ok {
		p.Role = role
	}

	return p, nil
}
