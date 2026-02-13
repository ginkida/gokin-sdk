package context

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	sdk "github.com/ginkida/gokin-sdk"
	"google.golang.org/genai"
)

// SummaryCache is an LRU cache for generated summaries.
type SummaryCache struct {
	cache *sdk.LRUCache[string, string]
}

// NewSummaryCache creates a new summary cache with the given max entries.
func NewSummaryCache(maxSize int) *SummaryCache {
	return &SummaryCache{
		cache: sdk.NewLRUCache[string, string](maxSize, 1*time.Hour),
	}
}

// Get retrieves a cached summary by key.
func (sc *SummaryCache) Get(key string) (string, bool) {
	return sc.cache.Get(key)
}

// Set stores a summary in the cache.
func (sc *SummaryCache) Set(key, summary string) {
	sc.cache.Set(key, summary)
}

// HashMessages creates a deterministic hash key for a set of messages.
func HashMessages(messages []*genai.Content) string {
	h := sha256.New()
	for _, msg := range messages {
		h.Write([]byte(msg.Role))
		for _, part := range msg.Parts {
			if part.Text != "" {
				h.Write([]byte(part.Text))
			}
			if part.FunctionCall != nil {
				h.Write([]byte(part.FunctionCall.Name))
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Len returns the number of cached summaries.
func (sc *SummaryCache) Len() int {
	return sc.cache.Len()
}

// Close stops the background cleanup goroutine.
func (sc *SummaryCache) Close() {
	sc.cache.Close()
}
