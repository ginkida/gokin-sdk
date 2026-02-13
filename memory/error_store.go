package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrorEntry represents a learned error pattern with solution.
type ErrorEntry struct {
	ID          string    `json:"id"`
	ErrorType   string    `json:"error_type"`
	Pattern     string    `json:"pattern"`
	Solution    string    `json:"solution"`
	Tags        []string  `json:"tags"`
	SuccessRate float64   `json:"success_rate"`
	UseCount    int       `json:"use_count"`
	LastUsed    time.Time `json:"last_used"`
	Created     time.Time `json:"created"`
}

// ErrorStore manages persistent storage of learned error patterns and solutions.
type ErrorStore struct {
	dir     string
	entries map[string]*ErrorEntry
	byType  map[string][]string
	mu      sync.RWMutex
}

// NewErrorStore creates a new error store.
func NewErrorStore(dir string) (*ErrorStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create error store directory: %w", err)
	}

	es := &ErrorStore{
		dir:     dir,
		entries: make(map[string]*ErrorEntry),
		byType:  make(map[string][]string),
	}

	if err := es.load(); err != nil {
		es.entries = make(map[string]*ErrorEntry)
		es.byType = make(map[string][]string)
	}

	return es, nil
}

// RecordError stores a new error pattern with its solution.
func (es *ErrorStore) RecordError(errorType, pattern, solution string, tags []string) error {
	es.mu.Lock()
	defer es.mu.Unlock()

	for _, entry := range es.entries {
		if entry.ErrorType == errorType && entry.Pattern == pattern {
			entry.Solution = solution
			entry.Tags = tags
			entry.LastUsed = time.Now()
			return es.save()
		}
	}

	entry := &ErrorEntry{
		ID:          generateID(pattern + solution),
		ErrorType:   errorType,
		Pattern:     pattern,
		Solution:    solution,
		Tags:        tags,
		SuccessRate: 0.5,
		UseCount:    0,
		Created:     time.Now(),
		LastUsed:    time.Now(),
	}

	es.entries[entry.ID] = entry
	es.byType[errorType] = append(es.byType[errorType], entry.ID)

	return es.save()
}

// FindSolution finds error entries matching the given error message.
func (es *ErrorStore) FindSolution(errorMsg string) []*ErrorEntry {
	es.mu.RLock()
	defer es.mu.RUnlock()

	var matches []*ErrorEntry
	lowerError := strings.ToLower(errorMsg)

	for _, entry := range es.entries {
		if strings.Contains(lowerError, strings.ToLower(entry.Pattern)) {
			matches = append(matches, entry)
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].SuccessRate != matches[j].SuccessRate {
			return matches[i].SuccessRate > matches[j].SuccessRate
		}
		return matches[i].UseCount > matches[j].UseCount
	})

	return matches
}

// UpdateSuccess records that a learned solution was successful or not.
func (es *ErrorStore) UpdateSuccess(entryID string, success bool) error {
	es.mu.Lock()
	defer es.mu.Unlock()

	entry, ok := es.entries[entryID]
	if !ok {
		return fmt.Errorf("entry not found: %s", entryID)
	}

	entry.UseCount++
	entry.LastUsed = time.Now()

	alpha := 0.3
	if success {
		entry.SuccessRate = entry.SuccessRate*(1-alpha) + 1.0*alpha
	} else {
		entry.SuccessRate = entry.SuccessRate * (1 - alpha)
	}

	return es.save()
}

// GetErrorContext returns formatted context for injection into prompts.
func (es *ErrorStore) GetErrorContext(errorMsg string) string {
	matches := es.FindSolution(errorMsg)
	if len(matches) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n## Learned from Previous Errors\n\n")

	shown := 0
	for _, entry := range matches {
		if shown >= 3 {
			break
		}
		sb.WriteString(fmt.Sprintf("### %s (%.0f%% success rate)\n", entry.ErrorType, entry.SuccessRate*100))
		sb.WriteString(fmt.Sprintf("**Pattern:** %s\n", entry.Pattern))
		sb.WriteString(fmt.Sprintf("**Solution:** %s\n\n", entry.Solution))
		shown++
	}

	return sb.String()
}

// Count returns the number of learned error patterns.
func (es *ErrorStore) Count() int {
	es.mu.RLock()
	defer es.mu.RUnlock()
	return len(es.entries)
}

// PruneOldEntries removes entries not used in the specified duration with low success rate.
func (es *ErrorStore) PruneOldEntries(maxAge time.Duration) error {
	es.mu.Lock()
	defer es.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	var toDelete []string

	for id, entry := range es.entries {
		if entry.LastUsed.Before(cutoff) && entry.SuccessRate < 0.3 {
			toDelete = append(toDelete, id)
		}
	}

	for _, id := range toDelete {
		entry := es.entries[id]
		delete(es.entries, id)
		ids := es.byType[entry.ErrorType]
		for i, eid := range ids {
			if eid == id {
				es.byType[entry.ErrorType] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
	}

	return es.save()
}

// Clear removes all entries.
func (es *ErrorStore) Clear() error {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.entries = make(map[string]*ErrorEntry)
	es.byType = make(map[string][]string)
	return es.save()
}

// --- internal ---

func (es *ErrorStore) storagePath() string {
	return filepath.Join(es.dir, "errors.json")
}

func (es *ErrorStore) load() error {
	data, err := os.ReadFile(es.storagePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var entries []*ErrorEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}

	for _, entry := range entries {
		es.entries[entry.ID] = entry
		es.byType[entry.ErrorType] = append(es.byType[entry.ErrorType], entry.ID)
	}
	return nil
}

func (es *ErrorStore) save() error {
	entries := make([]*ErrorEntry, 0, len(es.entries))
	for _, entry := range es.entries {
		entries = append(entries, entry)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(es.storagePath(), data, 0644)
}
