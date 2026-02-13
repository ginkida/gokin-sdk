package sdk

import (
	"container/list"
	"sync"
	"time"
)

// cacheEntry represents a cache entry with value and expiration.
type cacheEntry[K comparable, V any] struct {
	key       K
	value     V
	expiresAt time.Time
	element   *list.Element
}

// LRUCache is a generic LRU cache with TTL support and background cleanup.
type LRUCache[K comparable, V any] struct {
	capacity  int
	ttl       time.Duration
	entries   map[K]*cacheEntry[K, V]
	evictList *list.List
	mu        sync.RWMutex

	cleanupStop chan struct{}
	closeOnce   sync.Once
}

// NewLRUCache creates a new LRU cache with the given capacity and TTL.
// A background goroutine periodically removes expired entries.
// Call Close() to stop the background cleanup goroutine.
func NewLRUCache[K comparable, V any](capacity int, ttl time.Duration) *LRUCache[K, V] {
	if capacity < 1 {
		capacity = 1
	}
	c := &LRUCache[K, V]{
		capacity:    capacity,
		ttl:         ttl,
		entries:     make(map[K]*cacheEntry[K, V]),
		evictList:   list.New(),
		cleanupStop: make(chan struct{}),
	}

	go c.cleanupLoop()

	return c
}

// cleanupLoop periodically removes expired entries.
func (c *LRUCache[K, V]) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.Cleanup()
		case <-c.cleanupStop:
			return
		}
	}
}

// Close stops the background cleanup goroutine and releases resources.
func (c *LRUCache[K, V]) Close() {
	c.closeOnce.Do(func() {
		close(c.cleanupStop)
	})
}

// Get retrieves a value from the cache.
// Returns the value and true if found and not expired, zero value and false otherwise.
func (c *LRUCache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var zero V
	e, ok := c.entries[key]
	if !ok {
		return zero, false
	}

	if time.Now().After(e.expiresAt) {
		c.removeEntry(e)
		return zero, false
	}

	c.evictList.MoveToFront(e.element)
	return e.value, true
}

// Set adds or updates a value in the cache.
func (c *LRUCache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[key]; ok {
		e.value = value
		e.expiresAt = time.Now().Add(c.ttl)
		c.evictList.MoveToFront(e.element)
		return
	}

	e := &cacheEntry[K, V]{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
	e.element = c.evictList.PushFront(e)
	c.entries[key] = e

	for c.evictList.Len() > c.capacity {
		c.evictOldest()
	}
}

// Delete removes a key from the cache.
func (c *LRUCache[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[key]; ok {
		c.removeEntry(e)
	}
}

// Clear removes all entries from the cache.
func (c *LRUCache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[K]*cacheEntry[K, V])
	c.evictList = list.New()
}

// Len returns the number of entries in the cache.
func (c *LRUCache[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Keys returns all non-expired keys in the cache.
func (c *LRUCache[K, V]) Keys() []K {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	keys := make([]K, 0, len(c.entries))

	for key, e := range c.entries {
		if !now.After(e.expiresAt) {
			keys = append(keys, key)
		}
	}

	return keys
}

// evictOldest removes the oldest entry from the cache.
func (c *LRUCache[K, V]) evictOldest() {
	elem := c.evictList.Back()
	if elem == nil {
		return
	}
	e := elem.Value.(*cacheEntry[K, V])
	c.removeEntry(e)
}

// removeEntry removes an entry from the cache.
func (c *LRUCache[K, V]) removeEntry(e *cacheEntry[K, V]) {
	c.evictList.Remove(e.element)
	delete(c.entries, e.key)
}

// Cleanup removes expired entries. Returns the number removed.
func (c *LRUCache[K, V]) Cleanup() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	removed := 0

	for key, e := range c.entries {
		if now.After(e.expiresAt) {
			c.evictList.Remove(e.element)
			delete(c.entries, key)
			removed++
		}
	}

	return removed
}
