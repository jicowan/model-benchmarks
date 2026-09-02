package cache

import (
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Cache is the interface satisfied by both TTLCache and NopCache.
type Cache interface {
	Get(key string) []byte
	Set(key string, data []byte)
	Invalidate(keys ...string)
}

type entry struct {
	data      []byte
	expiresAt time.Time
}

// TTLCache is a concurrent in-memory cache with a fixed TTL for all entries.
// Values are pre-serialized JSON bodies so cache hits skip both the DB query
// and the JSON encode.
type TTLCache struct {
	mu      sync.RWMutex
	items   map[string]*entry
	ttl     time.Duration
	hits    atomic.Uint64
	misses  atomic.Uint64
	stopLog chan struct{}
}

// New creates a TTLCache with the given TTL and starts a background
// goroutine that logs hit/miss/size every 60 seconds.
func New(ttl time.Duration) *TTLCache {
	c := &TTLCache{
		items:   make(map[string]*entry),
		ttl:     ttl,
		stopLog: make(chan struct{}),
	}
	go c.logLoop()
	return c
}

func (c *TTLCache) Get(key string) []byte {
	c.mu.RLock()
	e, ok := c.items[key]
	c.mu.RUnlock()

	if !ok {
		c.misses.Add(1)
		return nil
	}
	if time.Now().After(e.expiresAt) {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		c.misses.Add(1)
		return nil
	}
	c.hits.Add(1)
	return e.data
}

// maxEntries bounds the map (PRD-68 P5). Keys are otherwise constants, but
// a handful are derived from request parameters; when the cap is hit,
// expired entries are swept and — if it's still full — the new entry is
// simply not cached.
const maxEntries = 10_000

func (c *TTLCache) Set(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[key]; !exists && len(c.items) >= maxEntries {
		c.sweepLocked()
		if len(c.items) >= maxEntries {
			return
		}
	}
	c.items[key] = &entry{
		data:      data,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// sweepLocked deletes expired entries. Caller holds c.mu.
func (c *TTLCache) sweepLocked() {
	now := time.Now()
	for k, e := range c.items {
		if now.After(e.expiresAt) {
			delete(c.items, k)
		}
	}
}

func (c *TTLCache) Invalidate(keys ...string) {
	c.mu.Lock()
	for _, k := range keys {
		delete(c.items, k)
	}
	c.mu.Unlock()
}

// Stop shuts down the background stats logger.
func (c *TTLCache) Stop() {
	close(c.stopLog)
}

func (c *TTLCache) logLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// PRD-68 P5: expired entries used to linger until re-read; sweep
			// them on the same 60s tick as the stats line.
			c.mu.Lock()
			c.sweepLocked()
			size := len(c.items)
			c.mu.Unlock()
			log.Printf("[cache] hits=%d misses=%d entries=%d",
				c.hits.Load(), c.misses.Load(), size)
		case <-c.stopLog:
			return
		}
	}
}
