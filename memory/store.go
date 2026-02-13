// Package memory provides persistent memory storage for agent learning and context.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryType represents the scope of a memory entry.
type MemoryType string

const (
	MemorySession MemoryType = "session"
	MemoryProject MemoryType = "project"
	MemoryGlobal  MemoryType = "global"
)

// Entry represents a single memory entry.
type Entry struct {
	ID        string     `json:"id"`
	Key       string     `json:"key,omitempty"`
	Content   string     `json:"content"`
	Type      MemoryType `json:"type"`
	Tags      []string   `json:"tags,omitempty"`
	Timestamp time.Time  `json:"timestamp"`
	Project   string     `json:"project,omitempty"`
}

// NewEntry creates a new memory entry with auto-generated ID.
func NewEntry(content string, memType MemoryType) *Entry {
	return &Entry{
		ID:        generateID(content),
		Content:   content,
		Type:      memType,
		Timestamp: time.Now(),
	}
}

// WithKey sets the key for the entry.
func (e *Entry) WithKey(key string) *Entry {
	e.Key = key
	return e
}

// WithProject sets the project for the entry.
func (e *Entry) WithProject(project string) *Entry {
	e.Project = project
	return e
}

// WithTags sets the tags for the entry.
func (e *Entry) WithTags(tags []string) *Entry {
	e.Tags = tags
	return e
}

// HasTag returns true if the entry has the specified tag.
func (e *Entry) HasTag(tag string) bool {
	for _, t := range e.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// SearchQuery represents a query for searching memories.
type SearchQuery struct {
	Key         string
	Query       string
	Tags        []string
	ProjectOnly bool
	Project     string
	Limit       int
}

// Matches returns true if the entry matches the search query.
func (e *Entry) Matches(q SearchQuery) bool {
	if q.Key != "" && e.Key != q.Key {
		return false
	}
	if q.ProjectOnly && q.Project != "" && e.Project != q.Project {
		return false
	}
	for _, tag := range q.Tags {
		if !e.HasTag(tag) {
			return false
		}
	}
	return true
}

// Store manages persistent key-value memory with project/global scoping.
type Store struct {
	dir         string
	projectHash string
	maxEntries  int

	entries       map[string]*Entry
	globalEntries map[string]*Entry
	byKey         map[string]string

	dirty     bool
	saveTimer *time.Timer
	saveMu    sync.Mutex
	mu        sync.RWMutex
}

// Auto-tagging regex patterns.
var (
	reFilePath    = regexp.MustCompile(`(?:^|\s)(/[a-zA-Z0-9_.\-/]+)`)
	reFuncName    = regexp.MustCompile(`(?:func|function)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	rePackageName = regexp.MustCompile(`package\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
)

// NewStore creates a new memory store.
// dir is the storage directory (e.g. ~/.config/gokin-sdk/memory/).
// projectPath identifies the project for scoping.
func NewStore(dir string, projectPath string, maxEntries int) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create memory directory: %w", err)
	}

	s := &Store{
		dir:           dir,
		projectHash:   hashPath(projectPath),
		maxEntries:    maxEntries,
		entries:       make(map[string]*Entry),
		globalEntries: make(map[string]*Entry),
		byKey:         make(map[string]string),
	}

	if err := s.load(); err != nil {
		s.entries = make(map[string]*Entry)
		s.byKey = make(map[string]string)
	}

	return s, nil
}

// Get retrieves an entry by key.
func (s *Store) Get(key string) (*Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.byKey[key]
	if !ok {
		return nil, false
	}
	if entry, ok := s.entries[id]; ok {
		return entry, true
	}
	if entry, ok := s.globalEntries[id]; ok {
		return entry, true
	}
	return nil, false
}

// Set adds or updates a value in the store.
func (s *Store) Set(key, value string, tags []string) error {
	entry := NewEntry(value, MemoryProject).WithKey(key).WithTags(tags)
	return s.Add(entry)
}

// Add adds a new entry to the store.
func (s *Store) Add(entry *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	autoTag(entry)

	if entry.Key != "" {
		if oldID, ok := s.byKey[entry.Key]; ok {
			delete(s.entries, oldID)
			delete(s.globalEntries, oldID)
		}
		s.byKey[entry.Key] = entry.ID
	}

	if entry.Type == MemoryGlobal {
		s.globalEntries[entry.ID] = entry
	} else {
		if entry.Project == "" {
			entry.Project = s.projectHash
		}
		s.entries[entry.ID] = entry
	}

	if s.maxEntries > 0 && (len(s.entries)+len(s.globalEntries)) > s.maxEntries {
		s.pruneOldest()
	}

	s.dirty = true
	s.scheduleSave()
	return nil
}

// Search finds entries matching the query.
func (s *Store) Search(query SearchQuery) []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query.Project = s.projectHash

	type scored struct {
		entry *Entry
		score int
	}
	var results []scored

	scoreEntry := func(entry *Entry) int {
		sc := 0
		qLower := strings.ToLower(query.Query)
		if query.Query != "" && strings.EqualFold(entry.Key, query.Query) {
			sc += 10
		}
		if query.Query != "" {
			for _, tag := range entry.Tags {
				if strings.EqualFold(tag, query.Query) {
					sc += 5
				}
			}
		}
		if query.Query != "" && strings.Contains(strings.ToLower(entry.Content), qLower) {
			sc += 1
		}
		if query.Query == "" {
			sc = 1
		}
		return sc
	}

	for _, entry := range s.entries {
		if !entry.Matches(query) {
			continue
		}
		if sc := scoreEntry(entry); sc > 0 {
			results = append(results, scored{entry, sc})
		}
	}
	if !query.ProjectOnly {
		for _, entry := range s.globalEntries {
			if !entry.Matches(query) {
				continue
			}
			if sc := scoreEntry(entry); sc > 0 {
				results = append(results, scored{entry, sc})
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].entry.Timestamp.After(results[j].entry.Timestamp)
	})

	entries := make([]*Entry, len(results))
	for i, r := range results {
		entries[i] = r.entry
	}
	if query.Limit > 0 && len(entries) > query.Limit {
		entries = entries[:query.Limit]
	}
	return entries
}

// ListByTag returns all entries with the given tag.
func (s *Store) ListByTag(tag string) []*Entry {
	return s.Search(SearchQuery{Tags: []string{tag}})
}

// Remove removes an entry by ID or key.
func (s *Store) Remove(idOrKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry, ok := s.entries[idOrKey]; ok {
		delete(s.entries, idOrKey)
		if entry.Key != "" {
			delete(s.byKey, entry.Key)
		}
		_ = s.save()
		return true
	}
	if entry, ok := s.globalEntries[idOrKey]; ok {
		delete(s.globalEntries, idOrKey)
		if entry.Key != "" {
			delete(s.byKey, entry.Key)
		}
		_ = s.save()
		return true
	}
	if id, ok := s.byKey[idOrKey]; ok {
		delete(s.byKey, idOrKey)
		delete(s.entries, id)
		delete(s.globalEntries, id)
		_ = s.save()
		return true
	}
	return false
}

// Flush forces an immediate save.
func (s *Store) Flush() error {
	s.saveMu.Lock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.saveMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	err := s.save()
	if err == nil {
		s.dirty = false
	}
	return err
}

// Count returns the number of project entries.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// --- internal ---

func (s *Store) storagePath() string {
	return filepath.Join(s.dir, s.projectHash+".json")
}

func (s *Store) globalStoragePath() string {
	return filepath.Join(s.dir, "global.json")
}

func (s *Store) load() error {
	if err := s.loadFile(s.storagePath(), s.entries); err != nil {
		return err
	}
	if err := s.loadFile(s.globalStoragePath(), s.globalEntries); err != nil {
		return err
	}
	for _, entry := range s.entries {
		if entry.Key != "" {
			s.byKey[entry.Key] = entry.ID
		}
	}
	for _, entry := range s.globalEntries {
		if entry.Key != "" {
			s.byKey[entry.Key] = entry.ID
		}
	}
	return nil
}

func (s *Store) loadFile(path string, target map[string]*Entry) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var entries []*Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	for _, entry := range entries {
		target[entry.ID] = entry
	}
	return nil
}

func (s *Store) save() error {
	projectEntries := make([]*Entry, 0)
	for _, entry := range s.entries {
		if entry.Type == MemoryProject {
			projectEntries = append(projectEntries, entry)
		}
	}
	if err := s.saveFile(s.storagePath(), projectEntries); err != nil {
		return err
	}
	globalEntries := make([]*Entry, 0, len(s.globalEntries))
	for _, entry := range s.globalEntries {
		globalEntries = append(globalEntries, entry)
	}
	return s.saveFile(s.globalStoragePath(), globalEntries)
}

func (s *Store) saveFile(path string, entries []*Entry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (s *Store) pruneOldest() {
	total := len(s.entries) + len(s.globalEntries)
	if s.maxEntries <= 0 || total <= s.maxEntries {
		return
	}
	all := make([]*Entry, 0, total)
	for _, e := range s.entries {
		all = append(all, e)
	}
	for _, e := range s.globalEntries {
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp.Before(all[j].Timestamp)
	})
	toRemove := total - s.maxEntries
	for i := 0; i < toRemove; i++ {
		entry := all[i]
		delete(s.entries, entry.ID)
		delete(s.globalEntries, entry.ID)
		if entry.Key != "" {
			delete(s.byKey, entry.Key)
		}
	}
}

func (s *Store) scheduleSave() {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
	}
	s.saveTimer = time.AfterFunc(2*time.Second, func() {
		s.mu.Lock()
		if !s.dirty {
			s.mu.Unlock()
			return
		}
		err := s.save()
		if err == nil {
			s.dirty = false
		}
		s.mu.Unlock()
	})
}

func autoTag(entry *Entry) {
	extracted := extractContentTags(entry.Content)
	if len(extracted) == 0 {
		return
	}
	seen := make(map[string]bool)
	for _, t := range entry.Tags {
		seen[t] = true
	}
	for _, t := range extracted {
		if !seen[t] {
			entry.Tags = append(entry.Tags, t)
			seen[t] = true
		}
	}
}

func extractContentTags(content string) []string {
	seen := make(map[string]bool)
	var tags []string
	add := func(tag string) {
		if tag != "" && !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	for _, m := range reFilePath.FindAllStringSubmatch(content, -1) {
		add(m[1])
	}
	for _, m := range reFuncName.FindAllStringSubmatch(content, -1) {
		add(m[1])
	}
	for _, m := range rePackageName.FindAllStringSubmatch(content, -1) {
		add(m[1])
	}
	return tags
}

func generateID(content string) string {
	data := content + time.Now().String()
	hash := sha256.Sum256([]byte(data))
	return "mem_" + hex.EncodeToString(hash[:8])
}

func hashPath(path string) string {
	hash := sha256.Sum256([]byte(path))
	return hex.EncodeToString(hash[:8])
}
