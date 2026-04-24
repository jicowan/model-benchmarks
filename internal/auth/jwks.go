package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// JWKSFetcher fetches and caches Cognito's signing keys. The public
// JWKS endpoint returns a set of RSA public keys keyed by "kid" (key
// ID); each JWT carries its kid in the header. We cache all current
// keys for jwksCacheTTL and refresh on expiry. Also refreshes on a
// cache miss (token claims an unknown kid) — Cognito publishes both
// old and new keys during key rotation, so this is rare but possible.
type JWKSFetcher struct {
	url        string
	httpClient *http.Client

	mu         sync.RWMutex
	keys       map[string]*rsa.PublicKey // keyed by kid
	fetchedAt  time.Time
}

const (
	jwksCacheTTL     = 1 * time.Hour
	jwksFetchTimeout = 10 * time.Second
)

// NewJWKSFetcher returns a fetcher targeting cfg.JWKSURL(). Keys are
// lazy-loaded on the first verification so startup doesn't block on a
// remote call.
func NewJWKSFetcher(cfg Config) *JWKSFetcher {
	return &JWKSFetcher{
		url:        cfg.JWKSURL(),
		httpClient: &http.Client{Timeout: jwksFetchTimeout},
		keys:       map[string]*rsa.PublicKey{},
	}
}

// KeyForKid returns the RSA public key for the given kid, refreshing
// the cache if needed.
func (f *JWKSFetcher) KeyForKid(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	f.mu.RLock()
	key, ok := f.keys[kid]
	fresh := time.Since(f.fetchedAt) < jwksCacheTTL
	f.mu.RUnlock()
	if ok && fresh {
		return key, nil
	}

	if err := f.refresh(ctx); err != nil {
		return nil, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()
	key, ok = f.keys[kid]
	if !ok {
		return nil, fmt.Errorf("no JWKS key for kid %q", kid)
	}
	return key, nil
}

// SetHTTPClient lets tests inject a client pointing at a test server.
func (f *JWKSFetcher) SetHTTPClient(c *http.Client) {
	f.httpClient = c
}

// SetURL lets tests override the JWKS URL.
func (f *JWKSFetcher) SetURL(u string) {
	f.url = u
}

type jwksResponse struct {
	Keys []jwksKey `json:"keys"`
}

type jwksKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Use string `json:"use"`
}

func (f *JWKSFetcher) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return fmt.Errorf("build JWKS request: %w", err)
	}
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS: status %d", resp.StatusCode)
	}
	var body jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(body.Keys))
	for _, k := range body.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := rsaPublicKey(k.N, k.E)
		if err != nil {
			// Skip malformed keys rather than failing the whole refresh.
			continue
		}
		keys[k.Kid] = pub
	}

	f.mu.Lock()
	f.keys = keys
	f.fetchedAt = time.Now()
	f.mu.Unlock()
	return nil
}

// rsaPublicKey reconstructs an *rsa.PublicKey from the base64url-encoded
// modulus + exponent that a JWKS entry carries.
func rsaPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("decode N: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("decode E: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	var e int
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}
