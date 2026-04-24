package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// etagOf computes a weak ETag from pre-serialized JSON bytes.
// Uses SHA-256 truncated to 16 hex characters — enough entropy to
// avoid collisions in practice, short enough to not bloat headers.
func etagOf(data []byte) string {
	h := sha256.Sum256(data)
	return `W/"` + hex.EncodeToString(h[:8]) + `"`
}

// checkETag compares the request's If-None-Match header against etag.
// If they match, it writes 304 Not Modified and returns true — the
// caller should return immediately. Otherwise it sets the ETag header
// (so the client can cache it) and returns false.
func checkETag(w http.ResponseWriter, r *http.Request, etag string) bool {
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}
