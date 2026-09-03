package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// PRD-68 P6: per-client rate limit for the unauthenticated auth endpoints.
//
// Cognito throttles credential attempts itself, so this is defence in
// depth: it stops one client from turning /auth/login into a
// credential-stuffing or Cognito-quota-burning loop before Cognito ever sees
// the requests, and it keeps the API's own error path cheap. Keyed by client
// IP (first X-Forwarded-For hop when behind the ALB, else RemoteAddr).

type ipLimiter struct {
	mu       sync.Mutex
	clients  map[string]*limiterEntry
	rps      rate.Limit
	burst    int
	lastSwp  time.Time
	sweepAge time.Duration
}

type limiterEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

// newIPLimiter allows `perMinute` sustained requests per client with a
// burst of `burst`.
func newIPLimiter(perMinute, burst int) *ipLimiter {
	return &ipLimiter{
		clients:  make(map[string]*limiterEntry),
		rps:      rate.Limit(float64(perMinute) / 60.0),
		burst:    burst,
		lastSwp:  time.Now(),
		sweepAge: 10 * time.Minute,
	}
}

func (l *ipLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSwp) > l.sweepAge {
		for k, e := range l.clients {
			if now.Sub(e.seen) > l.sweepAge {
				delete(l.clients, k)
			}
		}
		l.lastSwp = now
	}
	e, ok := l.clients[key]
	if !ok {
		e = &limiterEntry{lim: rate.NewLimiter(l.rps, l.burst)}
		l.clients[key] = e
	}
	e.seen = now
	return e.lim.Allow()
}

// middleware returns 429 when the client's bucket is empty.
func (l *ipLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate_limited")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP prefers the first X-Forwarded-For hop (set by the ALB), falling
// back to the TCP peer.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			xff = xff[:i]
		}
		if ip := strings.TrimSpace(xff); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
