package sdk

import (
	"fmt"
	"sync"
	"time"
)

// ClientPool manages a pool of Client connections keyed by provider:model.
type ClientPool struct {
	clients  map[string]*poolEntry
	maxSize  int
	mu       sync.Mutex
	closed   bool
}

type poolEntry struct {
	client   Client
	lastUsed time.Time
}

// NewClientPool creates a new client pool with the given maximum size.
func NewClientPool(maxSize int) *ClientPool {
	if maxSize <= 0 {
		maxSize = 5
	}
	return &ClientPool{
		clients: make(map[string]*poolEntry),
		maxSize: maxSize,
	}
}

// Get retrieves a client from the pool. Returns nil if not found.
func (p *ClientPool) Get(provider, model string) Client {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}

	key := poolKey(provider, model)
	entry, ok := p.clients[key]
	if !ok {
		return nil
	}

	entry.lastUsed = time.Now()
	return entry.client
}

// Put stores a client in the pool. Evicts the least recently used client if full.
func (p *ClientPool) Put(provider, model string, client Client) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}

	key := poolKey(provider, model)

	// If already exists, update
	if entry, ok := p.clients[key]; ok {
		entry.client = client
		entry.lastUsed = time.Now()
		return
	}

	// Evict oldest if full
	if len(p.clients) >= p.maxSize {
		p.evictOldest()
	}

	p.clients[key] = &poolEntry{
		client:   client,
		lastUsed: time.Now(),
	}
}

// Size returns the number of clients in the pool.
func (p *ClientPool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.clients)
}

// Cleanup removes clients that have been idle for longer than the given duration.
func (p *ClientPool) Cleanup(maxIdle time.Duration) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := 0
	cutoff := time.Now().Add(-maxIdle)

	for key, entry := range p.clients {
		if entry.lastUsed.Before(cutoff) {
			entry.client.Close()
			delete(p.clients, key)
			count++
		}
	}

	return count
}

// Close closes all clients in the pool.
func (p *ClientPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	var lastErr error
	for key, entry := range p.clients {
		if err := entry.client.Close(); err != nil {
			lastErr = err
		}
		delete(p.clients, key)
	}
	return lastErr
}

func (p *ClientPool) evictOldest() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range p.clients {
		if oldestKey == "" || entry.lastUsed.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.lastUsed
		}
	}

	if oldestKey != "" {
		p.clients[oldestKey].client.Close()
		delete(p.clients, oldestKey)
	}
}

func poolKey(provider, model string) string {
	return fmt.Sprintf("%s:%s", provider, model)
}
