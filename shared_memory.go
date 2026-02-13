package sdk

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SharedEntryType represents the type of shared memory entry.
type SharedEntryType string

const (
	SharedEntryTypeFact            SharedEntryType = "fact"
	SharedEntryTypeInsight         SharedEntryType = "insight"
	SharedEntryTypeFileState       SharedEntryType = "file_state"
	SharedEntryTypeDecision        SharedEntryType = "decision"
	SharedEntryTypeContextSnapshot SharedEntryType = "context_snapshot"

	// MaxSharedEntries is the maximum number of entries to keep in shared memory.
	MaxSharedEntries = 500
)

// ContextSnapshot captures key information for plan-to-execute transitions.
// This preserves critical context that would otherwise be lost during context compaction.
type ContextSnapshot struct {
	KeyFiles        map[string]string `json:"key_files"`
	Discoveries     []string          `json:"discoveries"`
	ErrorPatterns   map[string]string `json:"error_patterns"`
	CriticalResults []CriticalResult  `json:"critical_results"`
	Requirements    []string          `json:"requirements"`
	Decisions       []string          `json:"decisions"`
	CreatedAt       time.Time         `json:"created_at"`
	Source          string            `json:"source"`
}

// CriticalResult represents a tool result that should be preserved.
type CriticalResult struct {
	ToolName string `json:"tool_name"`
	Summary  string `json:"summary"`
	Details  string `json:"details"`
}

// SharedEntry represents a typed entry in shared memory.
type SharedEntry struct {
	Key       string          `json:"key"`
	Value     any             `json:"value"`
	Type      SharedEntryType `json:"type"`
	Source    string          `json:"source"`
	Timestamp time.Time       `json:"timestamp"`
	TTL       time.Duration   `json:"ttl"`
	Version   int             `json:"version"`
}

// IsExpired returns true if the entry has expired.
func (e *SharedEntry) IsExpired() bool {
	if e.TTL == 0 {
		return false
	}
	return time.Since(e.Timestamp) > e.TTL
}

// SharedMemory provides a shared memory space for inter-agent communication.
// It supports both simple key-value storage (Set/Get) and typed entries with
// pub/sub notifications (Write/Read/Subscribe).
type SharedMemory struct {
	entries     map[string]*SharedEntry
	byType      map[SharedEntryType][]string
	subscribers map[string]chan<- *SharedEntry
	closingCh   map[string]bool
	mu          sync.RWMutex

	droppedMessages atomic.Int64
}

// NewSharedMemory creates a new shared memory instance.
func NewSharedMemory() *SharedMemory {
	return &SharedMemory{
		entries:     make(map[string]*SharedEntry),
		byType:      make(map[SharedEntryType][]string),
		subscribers: make(map[string]chan<- *SharedEntry),
		closingCh:   make(map[string]bool),
	}
}

// Set stores a value with an optional TTL. Pass 0 for no expiration.
// This is a convenience method compatible with simple key-value usage.
func (sm *SharedMemory) Set(key string, value any, ttl time.Duration) {
	sm.WriteWithTTL(key, value, SharedEntryTypeFact, "", ttl)
}

// Get retrieves a value from shared memory.
// Returns the value and true if found and not expired, nil and false otherwise.
func (sm *SharedMemory) Get(key string) (any, bool) {
	entry, ok := sm.ReadEntry(key)
	if !ok {
		return nil, false
	}
	return entry.Value, true
}

// Keys returns all non-expired keys.
func (sm *SharedMemory) Keys() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	keys := make([]string, 0, len(sm.entries))
	for key, entry := range sm.entries {
		if !entry.IsExpired() {
			keys = append(keys, key)
		}
	}
	return keys
}

// Write writes a typed value to shared memory and notifies subscribers.
func (sm *SharedMemory) Write(key string, value any, entryType SharedEntryType, sourceAgent string) {
	sm.WriteWithTTL(key, value, entryType, sourceAgent, 0)
}

// WriteWithTTL writes a typed value with a time-to-live.
func (sm *SharedMemory) WriteWithTTL(key string, value any, entryType SharedEntryType, sourceAgent string, ttl time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.entries) >= MaxSharedEntries {
		sm.cleanupExpiredLocked()
		if len(sm.entries) >= MaxSharedEntries {
			sm.removeOldestLocked(MaxSharedEntries / 4)
		}
	}

	entry, exists := sm.entries[key]
	if exists {
		if entry.Type != entryType {
			sm.removeFromTypeIndexLocked(entry.Type, key)
			sm.byType[entryType] = append(sm.byType[entryType], key)
		}
		entry.Value = value
		entry.Type = entryType
		entry.Source = sourceAgent
		entry.Timestamp = time.Now()
		entry.TTL = ttl
		entry.Version++
	} else {
		entry = &SharedEntry{
			Key:       key,
			Value:     value,
			Type:      entryType,
			Source:    sourceAgent,
			Timestamp: time.Now(),
			TTL:       ttl,
			Version:   1,
		}
		sm.entries[key] = entry
		sm.byType[entryType] = append(sm.byType[entryType], key)
	}

	for subscriberID, ch := range sm.subscribers {
		if sm.closingCh[subscriberID] {
			continue
		}
		select {
		case ch <- entry:
		default:
			sm.droppedMessages.Add(1)
		}
	}
}

// ReadEntry reads a typed entry from shared memory.
func (sm *SharedMemory) ReadEntry(key string) (*SharedEntry, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	entry, ok := sm.entries[key]
	if !ok || entry.IsExpired() {
		return nil, false
	}
	return entry, true
}

// ReadByType returns all entries of a specific type.
func (sm *SharedMemory) ReadByType(entryType SharedEntryType) []*SharedEntry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	keys, ok := sm.byType[entryType]
	if !ok {
		return nil
	}

	var results []*SharedEntry
	for _, key := range keys {
		if entry, exists := sm.entries[key]; exists && !entry.IsExpired() {
			results = append(results, entry)
		}
	}
	return results
}

// ReadAll returns all non-expired entries.
func (sm *SharedMemory) ReadAll() []*SharedEntry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var results []*SharedEntry
	for _, entry := range sm.entries {
		if !entry.IsExpired() {
			results = append(results, entry)
		}
	}
	return results
}

// Delete removes an entry from shared memory.
func (sm *SharedMemory) Delete(key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry, ok := sm.entries[key]
	if !ok {
		return
	}

	sm.removeFromTypeIndexLocked(entry.Type, key)
	delete(sm.entries, key)
}

// Subscribe creates a subscription channel for an agent.
func (sm *SharedMemory) Subscribe(agentID string) <-chan *SharedEntry {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	ch := make(chan *SharedEntry, 100)
	sm.subscribers[agentID] = ch
	return ch
}

// Unsubscribe removes a subscription.
func (sm *SharedMemory) Unsubscribe(agentID string) {
	sm.mu.Lock()
	ch, ok := sm.subscribers[agentID]
	if !ok {
		sm.mu.Unlock()
		return
	}

	delete(sm.subscribers, agentID)
	delete(sm.closingCh, agentID)

	// Close under write lock so WriteWithTTL cannot send on a closed channel —
	// it won't see this subscriber in the map anymore.
	func() {
		defer func() { recover() }()
		close(ch)
	}()
	sm.mu.Unlock()
}

// GetForContext returns a formatted string of relevant entries for injection into prompts.
func (sm *SharedMemory) GetForContext(agentID string, maxEntries int) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if len(sm.entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n## Shared Memory Context\n")
	sb.WriteString("The following information has been shared by other agents:\n\n")

	count := 0
	for _, entry := range sm.entries {
		if entry.IsExpired() || entry.Source == agentID {
			continue
		}
		if count >= maxEntries {
			sb.WriteString(fmt.Sprintf("... and %d more entries\n", len(sm.entries)-count))
			break
		}
		sb.WriteString(fmt.Sprintf("- **%s** [%s from %s]: %v\n",
			entry.Key, entry.Type, entry.Source, entry.Value))
		count++
	}

	if count == 0 {
		return ""
	}

	sb.WriteString("\n")
	return sb.String()
}

func (sm *SharedMemory) removeFromTypeIndexLocked(entryType SharedEntryType, key string) {
	keys := sm.byType[entryType]
	for i, k := range keys {
		if k == key {
			sm.byType[entryType] = append(keys[:i], keys[i+1:]...)
			return
		}
	}
}

func (sm *SharedMemory) cleanupExpiredLocked() int {
	var expired []string
	for key, entry := range sm.entries {
		if entry.IsExpired() {
			expired = append(expired, key)
		}
	}
	for _, key := range expired {
		entry := sm.entries[key]
		sm.removeFromTypeIndexLocked(entry.Type, key)
		delete(sm.entries, key)
	}
	return len(expired)
}

func (sm *SharedMemory) removeOldestLocked(count int) {
	if count <= 0 || len(sm.entries) == 0 {
		return
	}

	type entryTime struct {
		key string
		ts  time.Time
	}
	var entries []entryTime
	for key, entry := range sm.entries {
		entries = append(entries, entryTime{key: key, ts: entry.Timestamp})
	}

	removed := 0
	for removed < count && len(entries) > 0 {
		oldestIdx := 0
		for i := 1; i < len(entries); i++ {
			if entries[i].ts.Before(entries[oldestIdx].ts) {
				oldestIdx = i
			}
		}
		key := entries[oldestIdx].key
		if entry, ok := sm.entries[key]; ok {
			sm.removeFromTypeIndexLocked(entry.Type, key)
			delete(sm.entries, key)
		}
		entries = append(entries[:oldestIdx], entries[oldestIdx+1:]...)
		removed++
	}
}

// CleanupExpired removes all expired entries.
func (sm *SharedMemory) CleanupExpired() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.cleanupExpiredLocked()
}

// Stats returns statistics about the shared memory.
func (sm *SharedMemory) Stats() SharedMemoryStats {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	stats := SharedMemoryStats{
		TotalEntries:    len(sm.entries),
		Subscribers:     len(sm.subscribers),
		ByType:          make(map[SharedEntryType]int),
		DroppedMessages: sm.droppedMessages.Load(),
	}
	for entryType, keys := range sm.byType {
		stats.ByType[entryType] = len(keys)
	}
	return stats
}

// SharedMemoryStats contains statistics about shared memory usage.
type SharedMemoryStats struct {
	TotalEntries    int
	Subscribers     int
	ByType          map[SharedEntryType]int
	DroppedMessages int64
}

// Clear removes all entries from shared memory.
func (sm *SharedMemory) Clear() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.entries = make(map[string]*SharedEntry)
	sm.byType = make(map[SharedEntryType][]string)
}

// SaveContextSnapshot saves a context snapshot for plan-to-execute transition.
func (sm *SharedMemory) SaveContextSnapshot(snapshot *ContextSnapshot, sourceAgent string) {
	if snapshot == nil {
		return
	}
	snapshot.CreatedAt = time.Now()
	snapshot.Source = sourceAgent
	sm.Write("context_snapshot", snapshot, SharedEntryTypeContextSnapshot, sourceAgent)
}

// GetContextSnapshot retrieves the latest context snapshot.
func (sm *SharedMemory) GetContextSnapshot() *ContextSnapshot {
	entry, ok := sm.ReadEntry("context_snapshot")
	if !ok {
		return nil
	}
	snapshot, ok := entry.Value.(*ContextSnapshot)
	if !ok {
		return nil
	}
	return snapshot
}

// GetContextSnapshotForPrompt returns a formatted context snapshot for injection into prompts.
func (sm *SharedMemory) GetContextSnapshotForPrompt() string {
	snapshot := sm.GetContextSnapshot()
	if snapshot == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n## Context From Planning Phase\n\n")

	if len(snapshot.KeyFiles) > 0 {
		sb.WriteString("**Key Files Analyzed:**\n")
		for path, summary := range snapshot.KeyFiles {
			sb.WriteString(fmt.Sprintf("- `%s`: %s\n", path, summary))
		}
		sb.WriteString("\n")
	}

	if len(snapshot.Discoveries) > 0 {
		sb.WriteString("**Key Discoveries:**\n")
		for _, discovery := range snapshot.Discoveries {
			sb.WriteString(fmt.Sprintf("- %s\n", discovery))
		}
		sb.WriteString("\n")
	}

	if len(snapshot.Requirements) > 0 {
		sb.WriteString("**Requirements:**\n")
		for _, req := range snapshot.Requirements {
			sb.WriteString(fmt.Sprintf("- %s\n", req))
		}
		sb.WriteString("\n")
	}

	if len(snapshot.Decisions) > 0 {
		sb.WriteString("**Architectural Decisions:**\n")
		for _, decision := range snapshot.Decisions {
			sb.WriteString(fmt.Sprintf("- %s\n", decision))
		}
		sb.WriteString("\n")
	}

	if len(snapshot.CriticalResults) > 0 {
		sb.WriteString("**Critical Tool Results:**\n")
		for _, result := range snapshot.CriticalResults {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", result.ToolName, result.Summary))
			if result.Details != "" && len(result.Details) < 500 {
				sb.WriteString(fmt.Sprintf("  Details: %s\n", result.Details))
			}
		}
		sb.WriteString("\n")
	}

	if len(snapshot.ErrorPatterns) > 0 {
		sb.WriteString("**Known Error Patterns:**\n")
		for pattern, solution := range snapshot.ErrorPatterns {
			sb.WriteString(fmt.Sprintf("- `%s`: %s\n", pattern, solution))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// NewContextSnapshot creates a new empty context snapshot.
func NewContextSnapshot() *ContextSnapshot {
	return &ContextSnapshot{
		KeyFiles:        make(map[string]string),
		Discoveries:     make([]string, 0),
		ErrorPatterns:   make(map[string]string),
		CriticalResults: make([]CriticalResult, 0),
		Requirements:    make([]string, 0),
		Decisions:       make([]string, 0),
		CreatedAt:       time.Now(),
	}
}

// AddKeyFile adds a key file with its summary to the snapshot.
func (cs *ContextSnapshot) AddKeyFile(path, summary string) {
	cs.KeyFiles[path] = summary
}

// AddDiscovery adds a discovery to the snapshot.
func (cs *ContextSnapshot) AddDiscovery(discovery string) {
	cs.Discoveries = append(cs.Discoveries, discovery)
}

// AddRequirement adds a requirement to the snapshot.
func (cs *ContextSnapshot) AddRequirement(requirement string) {
	cs.Requirements = append(cs.Requirements, requirement)
}

// AddDecision adds an architectural decision to the snapshot.
func (cs *ContextSnapshot) AddDecision(decision string) {
	cs.Decisions = append(cs.Decisions, decision)
}

// AddCriticalResult adds a critical tool result to the snapshot.
func (cs *ContextSnapshot) AddCriticalResult(toolName, summary, details string) {
	cs.CriticalResults = append(cs.CriticalResults, CriticalResult{
		ToolName: toolName,
		Summary:  summary,
		Details:  details,
	})
}

// AddErrorPattern adds an error pattern and its solution.
func (cs *ContextSnapshot) AddErrorPattern(pattern, solution string) {
	cs.ErrorPatterns[pattern] = solution
}
