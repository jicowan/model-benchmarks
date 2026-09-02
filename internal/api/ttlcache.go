package api

import (
	"context"
	"sync"
	"time"

	"github.com/accelbench/accelbench/internal/recommend"
)

// PRD-68 P5: small typed TTL caches for the recommender hot path.
//
// /recommend, /estimate and /memory-breakdown each re-fetched the model's
// config.json (S3 GetObject or two HF round-trips), the platform HF token
// (a Secrets Manager call) and the host-memory calibration (a
// percentile_cont over every completed run) on EVERY call — and the Run page
// fires them from four debounced effects while the user drags sliders.
// Model configs are immutable per revision and the calibration only moves
// when a run completes, so short TTLs remove almost all of that work.

type ttlEntry[T any] struct {
	v   T
	exp time.Time
}

// ttlMap is a tiny generic TTL cache. Expired entries are dropped lazily on
// read and swept when the map grows past maxEntries.
type ttlMap[T any] struct {
	mu         sync.Mutex
	items      map[string]ttlEntry[T]
	ttl        time.Duration
	maxEntries int
}

func newTTLMap[T any](ttl time.Duration, maxEntries int) *ttlMap[T] {
	return &ttlMap[T]{items: make(map[string]ttlEntry[T]), ttl: ttl, maxEntries: maxEntries}
}

func (m *ttlMap[T]) get(key string) (T, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[key]
	if !ok {
		var zero T
		return zero, false
	}
	if time.Now().After(e.exp) {
		delete(m.items, key)
		var zero T
		return zero, false
	}
	return e.v, true
}

func (m *ttlMap[T]) set(key string, v T) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) >= m.maxEntries {
		now := time.Now()
		for k, e := range m.items {
			if now.After(e.exp) {
				delete(m.items, k)
			}
		}
		if len(m.items) >= m.maxEntries {
			return // still full of live entries; skip caching this one
		}
	}
	m.items[key] = ttlEntry[T]{v: v, exp: time.Now().Add(m.ttl)}
}

func (m *ttlMap[T]) invalidate(key string) {
	m.mu.Lock()
	delete(m.items, key)
	m.mu.Unlock()
}

const (
	modelConfigTTL = 10 * time.Minute
	calibrationTTL = 60 * time.Second
)

// hotCaches groups the per-Server caches so tests can construct a Server
// without wiring each one.
type hotCaches struct {
	modelConfig *ttlMap[*recommend.ModelConfig]
	calibration *ttlMap[map[string]float64]
}

func newHotCaches() *hotCaches {
	return &hotCaches{
		modelConfig: newTTLMap[*recommend.ModelConfig](modelConfigTTL, 2000),
		calibration: newTTLMap[map[string]float64](calibrationTTL, 1),
	}
}

// hostMemCalibration returns the recommender's per-family calibration map,
// cached for calibrationTTL. Errors are returned uncached so a transient DB
// failure is retried on the next call.
func (s *Server) hostMemCalibration(ctx context.Context) (map[string]float64, error) {
	if v, ok := s.hot.calibration.get("all"); ok {
		return v, nil
	}
	calib, err := s.repo.GetHostMemCalibration(ctx)
	if err != nil {
		return nil, err
	}
	s.hot.calibration.set("all", calib)
	return calib, nil
}
