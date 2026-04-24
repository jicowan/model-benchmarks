package cache

// NopCache is a Cache that always returns miss. Used as the default
// in Server when SetCache is not called (e.g., in tests).
type NopCache struct{}

func (NopCache) Get(string) []byte    { return nil }
func (NopCache) Set(string, []byte)   {}
func (NopCache) Invalidate(...string) {}
