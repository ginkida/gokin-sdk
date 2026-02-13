package sdk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/genai"
)

// SessionStore provides file-based persistence for sessions.
type SessionStore struct {
	dir string
}

// NewSessionStore creates a new store that saves sessions in the given directory.
func NewSessionStore(dir string) *SessionStore {
	return &SessionStore{dir: dir}
}

// sessionState is the JSON-serializable session format.
type sessionState struct {
	ID        string             `json:"id"`
	CreatedAt time.Time          `json:"created_at"`
	Messages  []serializedContent `json:"messages"`
}

type serializedContent struct {
	Role  string           `json:"role"`
	Parts []serializedPart `json:"parts"`
}

type serializedPart struct {
	Type             string         `json:"type"` // "text", "function_call", "function_response"
	Text             string         `json:"text,omitempty"`
	FunctionName     string         `json:"function_name,omitempty"`
	FunctionArgs     map[string]any `json:"function_args,omitempty"`
	FunctionID       string         `json:"function_id,omitempty"`
	FunctionResponse map[string]any `json:"function_response,omitempty"`
}

// Save persists a session to disk.
func (ss *SessionStore) Save(session *Session) error {
	if err := os.MkdirAll(ss.dir, 0700); err != nil {
		return fmt.Errorf("creating session directory: %w", err)
	}

	state := sessionState{
		ID:        session.ID(),
		CreatedAt: session.CreatedAt(),
	}

	history := session.GetHistory()
	for _, msg := range history {
		sc := serializeContent(msg)
		state.Messages = append(state.Messages, sc)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}

	path := filepath.Join(ss.dir, session.ID()+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}

	return nil
}

// Load reads a session from disk.
func (ss *SessionStore) Load(id string) (*Session, error) {
	path := filepath.Join(ss.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session not found: %s", id)
		}
		return nil, fmt.Errorf("reading session file: %w", err)
	}

	var state sessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshaling session: %w", err)
	}

	session := NewSession(state.ID)
	session.createdAt = state.CreatedAt

	for _, sc := range state.Messages {
		content := deserializeContent(sc)
		session.AddContent(content)
	}

	return session, nil
}

// List returns all saved session IDs.
func (ss *SessionStore) List() ([]string, error) {
	entries, err := os.ReadDir(ss.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading session directory: %w", err)
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}

	return ids, nil
}

// Delete removes a session from disk.
func (ss *SessionStore) Delete(id string) error {
	path := filepath.Join(ss.dir, id+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func serializeContent(content *genai.Content) serializedContent {
	sc := serializedContent{
		Role: content.Role,
	}

	for _, part := range content.Parts {
		sp := serializedPart{}

		if part.Text != "" {
			sp.Type = "text"
			sp.Text = part.Text
		} else if part.FunctionCall != nil {
			sp.Type = "function_call"
			sp.FunctionName = part.FunctionCall.Name
			sp.FunctionArgs = part.FunctionCall.Args
			sp.FunctionID = part.FunctionCall.ID
		} else if part.FunctionResponse != nil {
			sp.Type = "function_response"
			sp.FunctionName = part.FunctionResponse.Name
			sp.FunctionID = part.FunctionResponse.ID
			sp.FunctionResponse = part.FunctionResponse.Response
		} else {
			sp.Type = "text"
			sp.Text = " " // preserve empty parts
		}

		sc.Parts = append(sc.Parts, sp)
	}

	return sc
}

func deserializeContent(sc serializedContent) *genai.Content {
	content := &genai.Content{
		Role: sc.Role,
	}

	for _, sp := range sc.Parts {
		switch sp.Type {
		case "text":
			content.Parts = append(content.Parts, genai.NewPartFromText(sp.Text))
		case "function_call":
			content.Parts = append(content.Parts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					Name: sp.FunctionName,
					Args: sp.FunctionArgs,
					ID:   sp.FunctionID,
				},
			})
		case "function_response":
			content.Parts = append(content.Parts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     sp.FunctionName,
					ID:       sp.FunctionID,
					Response: sp.FunctionResponse,
				},
			})
		}
	}

	return content
}
