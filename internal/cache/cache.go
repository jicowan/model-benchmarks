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

func (c *TTLCache) Set(key string, data []byte) {
	c.mu.Lock()
	c.items[key] = &entry{
		data:      data,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
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
			c.mu.RLock()
			size := len(c.items)
			c.mu.RUnlock()
			log.Printf("[cache] hits=%d misses=%d entries=%d",
				c.hits.Load(), c.misses.Load(), size)
		case <-c.stopLog:
			return
		}
	}
}
