package auth

// Config holds the runtime configuration needed by the auth middleware
// and verifier. Populated from env vars in cmd/server/main.go.
type Config struct {
	// UserPoolID is the Cognito User Pool ID (e.g. "us-east-2_abc123"). Used
	// to build the JWKS URL and to form the expected "iss" claim.
	UserPoolID string

	// ClientID is the Cognito App Client ID. Used as the expected "aud"
	// claim on ID tokens, and as the "client_id" claim on access tokens.
	ClientID string

	// Region is the AWS region hosting the User Pool. Forms the JWKS host.
	Region string

	// Disabled, when true, short-circuits the middleware: every request
	// passes through with a synthetic principal {Sub: "local-dev",
	// Email: "dev@local", Role: "admin"}. Set AUTH_DISABLED=1 to enable.
	// Never set this in production — the startup log flags the condition
	// loudly if it's on.
	Disabled bool
}

// Issuer returns the expected JWT "iss" claim for tokens issued by this
// User Pool.
func (c Config) Issuer() string {
	return "https://cognito-idp." + c.Region + ".amazonaws.com/" + c.UserPoolID
}

// JWKSURL returns the endpoint serving this User Pool's signing keys.
func (c Config) JWKSURL() string {
	return c.Issuer() + "/.well-known/jwks.json"
}
