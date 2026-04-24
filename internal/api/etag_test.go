package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestETagOf_Deterministic(t *testing.T) {
	data := []byte(`{"id":"abc","status":"completed"}`)
	a := etagOf(data)
	b := etagOf(data)
	if a != b {
		t.Errorf("non-deterministic: %q vs %q", a, b)
	}
	if a == "" {
		t.Error("empty etag")
	}
}

func TestETagOf_DifferentBytes(t *testing.T) {
	a := etagOf([]byte(`{"a":1}`))
	b := etagOf([]byte(`{"a":2}`))
	if a == b {
		t.Error("different bodies should produce different etags")
	}
}

func TestCheckETag_Match(t *testing.T) {
	etag := etagOf([]byte("body"))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()

	if !checkETag(w, r, etag) {
		t.Fatal("expected match (true)")
	}
	if w.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", w.Code)
	}
	if w.Header().Get("ETag") != etag {
		t.Errorf("ETag header = %q, want %q", w.Header().Get("ETag"), etag)
	}
}

func TestCheckETag_NoMatch(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("If-None-Match", `W/"stale"`)
	w := httptest.NewRecorder()

	etag := etagOf([]byte("fresh"))
	if checkETag(w, r, etag) {
		t.Fatal("expected no match (false)")
	}
	if w.Header().Get("ETag") != etag {
		t.Error("ETag header should still be set on miss")
	}
}

func TestCheckETag_NoHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	if checkETag(w, r, etagOf([]byte("x"))) {
		t.Fatal("no If-None-Match should return false")
	}
}
