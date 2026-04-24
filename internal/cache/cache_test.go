package cache

import (
	"sync"
	"testing"
	"time"
)

func TestTTLCache_SetGet(t *testing.T) {
	c := New(5 * time.Second)
	defer c.Stop()

	c.Set("k", []byte(`{"ok":true}`))
	got := c.Get("k")
	if got == nil {
		t.Fatal("expected hit, got miss")
	}
	if string(got) != `{"ok":true}` {
		t.Errorf("got %q", got)
	}
}

func TestTTLCache_Miss(t *testing.T) {
	c := New(5 * time.Second)
	defer c.Stop()

	if c.Get("nonexistent") != nil {
		t.Error("expected miss")
	}
}

func TestTTLCache_Expiry(t *testing.T) {
	c := New(10 * time.Millisecond)
	defer c.Stop()

	c.Set("k", []byte("v"))
	time.Sleep(30 * time.Millisecond)
	if c.Get("k") != nil {
		t.Error("expected expiry")
	}
}

func TestTTLCache_Invalidate(t *testing.T) {
	c := New(5 * time.Second)
	defer c.Stop()

	c.Set("a", []byte("1"))
	c.Set("b", []byte("2"))
	c.Invalidate("a")
	if c.Get("a") != nil {
		t.Error("expected miss after invalidate")
	}
	if c.Get("b") == nil {
		t.Error("unrelated key should still hit")
	}
}

func TestTTLCache_InvalidateMultiple(t *testing.T) {
	c := New(5 * time.Second)
	defer c.Stop()

	c.Set("x", []byte("1"))
	c.Set("y", []byte("2"))
	c.Set("z", []byte("3"))
	c.Invalidate("x", "y")
	if c.Get("x") != nil || c.Get("y") != nil {
		t.Error("invalidated keys should miss")
	}
	if c.Get("z") == nil {
		t.Error("non-invalidated key should hit")
	}
}

func TestTTLCache_Overwrite(t *testing.T) {
	c := New(5 * time.Second)
	defer c.Stop()

	c.Set("k", []byte("old"))
	c.Set("k", []byte("new"))
	if string(c.Get("k")) != "new" {
		t.Error("expected overwritten value")
	}
}

func TestTTLCache_ConcurrentAccess(t *testing.T) {
	c := New(50 * time.Millisecond)
	defer c.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			c.Set("k", []byte("v"))
		}()
		go func() {
			defer wg.Done()
			c.Get("k")
		}()
		go func() {
			defer wg.Done()
			c.Invalidate("k")
		}()
	}
	wg.Wait()
}

func TestTTLCache_HitMissCounters(t *testing.T) {
	c := New(5 * time.Second)
	defer c.Stop()

	c.Set("k", []byte("v"))
	c.Get("k")           // hit
	c.Get("k")           // hit
	c.Get("nonexistent") // miss

	if c.hits.Load() != 2 {
		t.Errorf("hits = %d, want 2", c.hits.Load())
	}
	if c.misses.Load() != 1 {
		t.Errorf("misses = %d, want 1", c.misses.Load())
	}
}

func TestNopCache_AlwaysMiss(t *testing.T) {
	var c NopCache
	c.Set("k", []byte("v"))
	if c.Get("k") != nil {
		t.Error("NopCache should always miss")
	}
	c.Invalidate("k") // no panic
}

var _ Cache = (*TTLCache)(nil)
var _ Cache = NopCache{}
