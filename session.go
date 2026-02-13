package sdk

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/genai"
)

// Session provides thread-safe conversation history management.
type Session struct {
	id        string
	history   []*genai.Content
	version   atomic.Int64
	createdAt time.Time
	mu        sync.RWMutex

	maxMessages  int
	onChange     func(event ChangeEvent)
}

// ChangeEvent describes a change to the session history.
type ChangeEvent struct {
	Type    string // "add", "clear", "replace", "restore"
	Version int64
}

// NewSession creates a new session with the given ID.
func NewSession(id string) *Session {
	if id == "" {
		id = generateID()
	}
	return &Session{
		id:          id,
		history:     make([]*genai.Content, 0),
		createdAt:   time.Now(),
		maxMessages: 100,
	}
}

// ID returns the session's unique identifier.
func (s *Session) ID() string {
	return s.id
}

// AddUserMessage appends a user message to the history.
func (s *Session) AddUserMessage(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	content := genai.NewContentFromText(msg, "user")
	s.history = append(s.history, content)
	s.trimLocked()
	s.version.Add(1)

	if s.onChange != nil {
		go s.onChange(ChangeEvent{Type: "add", Version: s.version.Load()})
	}
}

// AddModelResponse appends a model response to the history.
func (s *Session) AddModelResponse(content *genai.Content) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if content.Role == "" {
		content.Role = "model"
	}
	s.history = append(s.history, content)
	s.trimLocked()
	s.version.Add(1)

	if s.onChange != nil {
		go s.onChange(ChangeEvent{Type: "add", Version: s.version.Load()})
	}
}

// AddContent appends any content to the history.
func (s *Session) AddContent(content *genai.Content) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, content)
	s.trimLocked()
	s.version.Add(1)

	if s.onChange != nil {
		go s.onChange(ChangeEvent{Type: "add", Version: s.version.Load()})
	}
}

// GetHistory returns a copy of the conversation history.
func (s *Session) GetHistory() []*genai.Content {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*genai.Content, len(s.history))
	copy(result, s.history)
	return result
}

// GetVersion returns the current version number.
func (s *Session) GetVersion() int64 {
	return s.version.Load()
}

// Len returns the number of messages in the history.
func (s *Session) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.history)
}

// Clear removes all messages from the history.
func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = make([]*genai.Content, 0)
	s.version.Add(1)

	if s.onChange != nil {
		go s.onChange(ChangeEvent{Type: "clear", Version: s.version.Load()})
	}
}

// ReplaceWithSummary replaces the history with a summary plus recent messages.
func (s *Session) ReplaceWithSummary(summary *genai.Content, recentMessages []*genai.Content) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = make([]*genai.Content, 0, len(recentMessages)+1)
	s.history = append(s.history, summary)
	s.history = append(s.history, recentMessages...)
	s.version.Add(1)

	if s.onChange != nil {
		go s.onChange(ChangeEvent{Type: "replace", Version: s.version.Load()})
	}
}

// SetOnChange sets a callback invoked when the history changes.
func (s *Session) SetOnChange(fn func(ChangeEvent)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

// SetMaxMessages sets the maximum number of messages before trimming.
func (s *Session) SetMaxMessages(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > 0 {
		s.maxMessages = n
	}
}

// CreatedAt returns when the session was created.
func (s *Session) CreatedAt() time.Time {
	return s.createdAt
}

// Summary returns a brief text summary of the session.
func (s *Session) Summary() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.history) == 0 {
		return "(empty session)"
	}

	// Get first user message for preview
	for _, msg := range s.history {
		if msg.Role == "user" {
			for _, part := range msg.Parts {
				if part.Text != "" {
					text := part.Text
					if len(text) > 80 {
						text = text[:80] + "..."
					}
					return fmt.Sprintf("%s (%d messages)", text, len(s.history))
				}
			}
		}
	}

	return fmt.Sprintf("(%d messages)", len(s.history))
}

func (s *Session) trimLocked() {
	if s.maxMessages <= 0 || len(s.history) <= s.maxMessages {
		return
	}
	// Keep most recent messages
	excess := len(s.history) - s.maxMessages
	s.history = s.history[excess:]
}
