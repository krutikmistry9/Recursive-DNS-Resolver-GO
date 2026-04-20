package cache

import (
	"fmt"
	"sync"
	"time"
)

// Entry represents a cached DNS record.
type Entry struct {
	Records   []Record
	ExpiresAt time.Time
	HitCount  int
}

// Record holds a single DNS resource record value.
type Record struct {
	Type  string // A, AAAA, CNAME, NS, MX, etc.
	Value string
	TTL   uint32
}

// Cache is a thread-safe TTL-based DNS cache.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	stats   Stats
}

// Stats tracks cache performance metrics.
type Stats struct {
	Hits   int64
	Misses int64
	Total  int64
}

// New creates and returns a new Cache with background TTL expiry cleanup.
func New() *Cache {
	c := &Cache{
		entries: make(map[string]*Entry),
	}
	go c.cleanupLoop()
	return c
}

func cacheKey(domain, qtype string) string {
	return fmt.Sprintf("%s|%s", domain, qtype)
}

// Get retrieves records from the cache if they exist and are not expired.
func (c *Cache) Get(domain, qtype string) ([]Record, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := cacheKey(domain, qtype)
	entry, ok := c.entries[key]
	if !ok {
		c.stats.Misses++
		c.stats.Total++
		return nil, false
	}
	if time.Now().After(entry.ExpiresAt) {
		c.stats.Misses++
		c.stats.Total++
		return nil, false
	}
	entry.HitCount++
	c.stats.Hits++
	c.stats.Total++
	return entry.Records, true
}

// Set stores DNS records in the cache using the minimum TTL across all records.
func (c *Cache) Set(domain, qtype string, records []Record) {
	if len(records) == 0 {
		return
	}
	minTTL := records[0].TTL
	for _, r := range records[1:] {
		if r.TTL < minTTL {
			minTTL = r.TTL
		}
	}
	if minTTL == 0 {
		minTTL = 1
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[cacheKey(domain, qtype)] = &Entry{
		Records:   records,
		ExpiresAt: time.Now().Add(time.Duration(minTTL) * time.Second),
	}
}

// Delete removes a specific entry from the cache.
func (c *Cache) Delete(domain, qtype string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, cacheKey(domain, qtype))
}

// Flush clears all cache entries and resets stats.
func (c *Cache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*Entry)
	c.stats = Stats{}
}

// Stats returns a snapshot of cache performance statistics.
func (c *Cache) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stats
}

// Snapshot returns all currently-live cache entries for inspection.
func (c *Cache) Snapshot() map[string]*Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	out := make(map[string]*Entry, len(c.entries))
	for k, v := range c.entries {
		if now.Before(v.ExpiresAt) {
			out[k] = v
		}
	}
	return out
}

// Size returns the number of live (non-expired) cache entries.
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	n := 0
	for _, v := range c.entries {
		if now.Before(v.ExpiresAt) {
			n++
		}
	}
	return n
}

// cleanupLoop periodically removes expired entries.
func (c *Cache) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.ExpiresAt) {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}
