// Package context provides context management for SDK agents including
// token counting, conversation summarization, and context optimization.
package context

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"unicode"

	"google.golang.org/genai"
)

// ContentType classifies content for token estimation.
type ContentType int

const (
	ContentTypeProse ContentType = iota
	ContentTypeCode
	ContentTypeJSON
	ContentTypeMixed
)

// TokenCounter estimates token counts for content and messages.
type TokenCounter struct {
	cache    map[string]int
	mu       sync.RWMutex
	maxCache int
}

// NewTokenCounter creates a new token counter with an LRU cache.
func NewTokenCounter() *TokenCounter {
	return &TokenCounter{
		cache:    make(map[string]int),
		maxCache: 1000,
	}
}

// Estimate returns an estimated token count for the given content.
func (tc *TokenCounter) Estimate(content string) int {
	if content == "" {
		return 0
	}

	// Check cache
	hash := hashContent(content)
	tc.mu.RLock()
	if count, ok := tc.cache[hash]; ok {
		tc.mu.RUnlock()
		return count
	}
	tc.mu.RUnlock()

	ct := DetectContentType(content)
	count := estimateByType(content, ct)

	// Cache result
	tc.mu.Lock()
	if len(tc.cache) >= tc.maxCache {
		// Simple eviction: clear half
		i := 0
		for k := range tc.cache {
			if i >= tc.maxCache/2 {
				break
			}
			delete(tc.cache, k)
			i++
		}
	}
	tc.cache[hash] = count
	tc.mu.Unlock()

	return count
}

// EstimateMessages returns an estimated token count for a list of messages.
func (tc *TokenCounter) EstimateMessages(messages []*genai.Content) int {
	total := 0
	for _, msg := range messages {
		// Per-message overhead (role, structure)
		total += 4

		for _, part := range msg.Parts {
			if part.Text != "" {
				total += tc.Estimate(part.Text)
			}
			if part.FunctionCall != nil {
				// Function call overhead
				total += 20
				for k, v := range part.FunctionCall.Args {
					total += tc.Estimate(k)
					if str, ok := v.(string); ok {
						total += tc.Estimate(str)
					} else {
						total += 10 // non-string arg estimate
					}
				}
			}
			if part.FunctionResponse != nil {
				total += 10 // response overhead
				for k, v := range part.FunctionResponse.Response {
					total += tc.Estimate(k)
					if str, ok := v.(string); ok {
						total += tc.Estimate(str)
					} else {
						total += 20
					}
				}
			}
		}
	}
	return total
}

// DetectContentType analyzes content and returns its type.
func DetectContentType(content string) ContentType {
	if len(content) == 0 {
		return ContentTypeProse
	}

	// Check for JSON
	trimmed := strings.TrimSpace(content)
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		return ContentTypeJSON
	}

	// Check for code indicators
	codeIndicators := []string{
		"func ", "if ", "for ", "return ", "import ",
		":=", "->", "=>", "def ", "class ",
		"const ", "var ", "let ", "package ",
		"#include", "public ", "private ",
	}

	codeScore := 0
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimLine := strings.TrimSpace(line)
		for _, indicator := range codeIndicators {
			if strings.Contains(trimLine, indicator) {
				codeScore++
				break
			}
		}
	}

	// Check for camelCase density
	if containsCamelCase(content) {
		codeScore += len(lines) / 5
	}

	codeRatio := float64(codeScore) / float64(len(lines))
	if codeRatio > 0.3 {
		return ContentTypeCode
	}
	if codeRatio > 0.1 {
		return ContentTypeMixed
	}

	return ContentTypeProse
}

func estimateByType(content string, ct ContentType) int {
	switch ct {
	case ContentTypeCode:
		// Code: ~3.2 chars per token
		return int(float64(len(content)) / 3.2)
	case ContentTypeJSON:
		// JSON: ~3 chars per token
		return int(float64(len(content)) / 3.0)
	case ContentTypeMixed:
		// Mixed: average of code and prose
		codeEst := float64(len(content)) / 3.2
		proseEst := estimateProse(content)
		return int((codeEst + float64(proseEst)) / 2)
	default:
		// Prose: word-based
		return estimateProse(content)
	}
}

func estimateProse(content string) int {
	words := len(strings.Fields(content))
	// ~1.3 tokens per word for English
	return int(float64(words) * 1.3)
}

func containsCamelCase(content string) bool {
	camelCount := 0
	total := 0
	inWord := false
	hasLower := false

	for _, r := range content {
		if unicode.IsLetter(r) {
			if !inWord {
				inWord = true
				hasLower = false
				total++
			}
			if unicode.IsLower(r) {
				hasLower = true
			} else if unicode.IsUpper(r) && hasLower {
				camelCount++
			}
		} else {
			inWord = false
		}
	}

	if total == 0 {
		return false
	}
	return float64(camelCount)/float64(total) > 0.2
}

func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:8])
}
