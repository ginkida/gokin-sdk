package sdk

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"google.golang.org/genai"
)

// AgentState represents the serializable state of an agent for checkpointing.
type AgentState struct {
	Name      string              `json:"name"`
	Model     string              `json:"model,omitempty"`
	History   []SerializedContent `json:"history"`
	StartTime time.Time           `json:"start_time"`
	TurnCount int                 `json:"turn_count"`
	Metadata  map[string]any      `json:"metadata,omitempty"`
}

// Checkpoint wraps an AgentState with checkpoint metadata.
type Checkpoint struct {
	State        *AgentState `json:"state"`
	CheckpointID string      `json:"checkpoint_id"`
	Reason       string      `json:"reason"`
	Timestamp    time.Time   `json:"timestamp"`
}

// SerializedContent represents a serializable conversation content.
type SerializedContent struct {
	Role  string           `json:"role"`
	Parts []SerializedPart `json:"parts"`
}

// SerializedPart represents a serializable content part.
type SerializedPart struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	FunctionCall *SerializedFunc `json:"function_call,omitempty"`
	FunctionResp *SerializedFunc `json:"function_response,omitempty"`
}

// SerializedFunc represents a serializable function call or response.
type SerializedFunc struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Args     map[string]any `json:"args,omitempty"`
	Response map[string]any `json:"response,omitempty"`
}

// SaveState saves the agent state to a file.
func SaveState(state *AgentState, path string) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LoadState loads an agent state from a file.
func LoadState(path string) (*AgentState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}
	var state AgentState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}
	return &state, nil
}

// SaveCheckpoint saves a checkpoint to a file.
func SaveCheckpoint(cp *Checkpoint, path string) error {
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LoadCheckpoint loads a checkpoint from a file.
func LoadCheckpoint(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint: %w", err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal checkpoint: %w", err)
	}
	return &cp, nil
}

// SerializeHistory converts genai.Content history to serializable form.
func SerializeHistory(history []*genai.Content) []SerializedContent {
	result := make([]SerializedContent, len(history))
	for i, content := range history {
		result[i] = serializeContentState(content)
	}
	return result
}

// DeserializeHistory converts serialized history back to genai.Content.
func DeserializeHistory(serialized []SerializedContent) ([]*genai.Content, error) {
	result := make([]*genai.Content, len(serialized))
	for i, sc := range serialized {
		content, err := deserializeContentState(sc)
		if err != nil {
			return nil, fmt.Errorf("failed to deserialize content %d: %w", i, err)
		}
		result[i] = content
	}
	return result, nil
}

func serializeContentState(content *genai.Content) SerializedContent {
	parts := make([]SerializedPart, len(content.Parts))
	for i, part := range content.Parts {
		parts[i] = serializePartState(part)
	}
	return SerializedContent{
		Role:  string(content.Role),
		Parts: parts,
	}
}

func serializePartState(part *genai.Part) SerializedPart {
	if part.FunctionCall != nil {
		return SerializedPart{
			Type: "function_call",
			FunctionCall: &SerializedFunc{
				ID:   part.FunctionCall.ID,
				Name: part.FunctionCall.Name,
				Args: part.FunctionCall.Args,
			},
		}
	}
	if part.FunctionResponse != nil {
		return SerializedPart{
			Type: "function_response",
			FunctionResp: &SerializedFunc{
				ID:       part.FunctionResponse.ID,
				Name:     part.FunctionResponse.Name,
				Response: part.FunctionResponse.Response,
			},
		}
	}
	text := part.Text
	if text == "" {
		text = " "
	}
	return SerializedPart{Type: "text", Text: text}
}

func deserializeContentState(sc SerializedContent) (*genai.Content, error) {
	parts := make([]*genai.Part, len(sc.Parts))
	for i, sp := range sc.Parts {
		part, err := deserializePartState(sp)
		if err != nil {
			return nil, err
		}
		parts[i] = part
	}
	return &genai.Content{
		Role:  sc.Role,
		Parts: parts,
	}, nil
}

func deserializePartState(sp SerializedPart) (*genai.Part, error) {
	switch sp.Type {
	case "text":
		text := sp.Text
		if text == "" {
			text = " "
		}
		return genai.NewPartFromText(text), nil
	case "function_call":
		if sp.FunctionCall == nil {
			return genai.NewPartFromText(" "), nil
		}
		return &genai.Part{
			FunctionCall: &genai.FunctionCall{
				ID:   sp.FunctionCall.ID,
				Name: sp.FunctionCall.Name,
				Args: sp.FunctionCall.Args,
			},
		}, nil
	case "function_response":
		if sp.FunctionResp == nil {
			return genai.NewPartFromText(" "), nil
		}
		part := genai.NewPartFromFunctionResponse(sp.FunctionResp.Name, sp.FunctionResp.Response)
		part.FunctionResponse.ID = sp.FunctionResp.ID
		return part, nil
	default:
		text := sp.Text
		if text == "" {
			text = " "
		}
		return genai.NewPartFromText(text), nil
	}
}
