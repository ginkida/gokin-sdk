package sdk

import (
	"context"

	"google.golang.org/genai"
)

// Client defines the interface for AI model interactions.
// Provider implementations (Gemini, Anthropic, Ollama) implement this interface.
type Client interface {
	// SendMessage sends a message and returns a streaming response.
	SendMessage(ctx context.Context, message string) (*StreamResponse, error)

	// SendMessageWithHistory sends a message with conversation history.
	SendMessageWithHistory(ctx context.Context, history []*genai.Content, message string) (*StreamResponse, error)

	// SendFunctionResponse sends function call results back to the model.
	SendFunctionResponse(ctx context.Context, history []*genai.Content, results []*genai.FunctionResponse) (*StreamResponse, error)

	// SetTools sets the tools available for the model to use.
	SetTools(tools []*genai.Tool)

	// SetSystemInstruction sets the system-level instruction for the model.
	SetSystemInstruction(instruction string)

	// GetModel returns the model name.
	GetModel() string

	// Close closes the client connection.
	Close() error

	// Clone returns an independent copy of the client that shares the
	// underlying HTTP/gRPC connection but has its own tools and system
	// instruction state. This is required for concurrent agent usage.
	Clone() Client
}
