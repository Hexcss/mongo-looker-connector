package gateway

import (
	"sync"
	"time"
)

// Entries are immutable after insertion. Key material includes live authorization
// state; expiry bounds data staleness, independently of revocation or publication.
const (
	cacheTTL        = time.Minute
	cacheMaxEntries = 64
	cacheMaxBytes   = 8 << 20
)

type cacheEntry struct {
	data    []byte
	expires time.Time
}
type resultCache struct {
	mu      sync.Mutex
	bytes   int
	entries map[[32]byte]cacheEntry
}

func (c *resultCache) get(key [32]byte) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil
	}
	if !time.Now().Before(entry.expires) {
		c.remove(key)
		return nil
	}
	return entry.data
}
func (c *resultCache) remove(key [32]byte) {
	c.bytes -= len(c.entries[key].data)
	delete(c.entries, key)
}
func (c *resultCache) put(key [32]byte, data []byte) {
	if len(data) > cacheMaxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[[32]byte]cacheEntry)
	}
	c.remove(key)
	for len(c.entries) >= cacheMaxEntries || c.bytes+len(data) > cacheMaxBytes {
		var oldest [32]byte
		var first time.Time
		for k, entry := range c.entries {
			if first.IsZero() || entry.expires.Before(first) {
				oldest, first = k, entry.expires
			}
		}
		c.remove(oldest)
	}
	c.entries[key] = cacheEntry{data, time.Now().Add(cacheTTL)}
	c.bytes += len(data)
}
