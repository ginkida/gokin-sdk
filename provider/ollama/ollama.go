// Package ollama provides an Ollama client implementation for the SDK.
//
// This is a simplified implementation that uses Ollama's HTTP API directly,
// avoiding the dependency on github.com/ollama/ollama/api.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// Option configures an OllamaClient.
type Option func(*OllamaClient)

// WithBaseURL sets the Ollama server URL (default: http://localhost:11434).
func WithBaseURL(url string) Option {
	return func(c *OllamaClient) {
		c.baseURL = url
	}
}

// WithTemperature sets the temperature for generation.
func WithTemperature(t float32) Option {
	return func(c *OllamaClient) {
		c.temperature = t
	}
}

// WithMaxTokens sets the maximum output tokens.
func WithMaxTokens(n int32) Option {
	return func(c *OllamaClient) {
		c.maxTokens = n
	}
}

// OllamaClient implements sdk.Client for Ollama's HTTP API.
type OllamaClient struct {
	baseURL           string
	model             string
	temperature       float32
	maxTokens         int32
	httpClient        *http.Client
	tools             []*genai.Tool
	systemInstruction string
	mu                sync.RWMutex
}

// New creates a new Ollama client.
func New(model string, opts ...Option) (*OllamaClient, error) {
	if model == "" {
		return nil, fmt.Errorf("model name is required")
	}

	c := &OllamaClient{
		baseURL:    "http://localhost:11434",
		model:      model,
		maxTokens:  8192,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// chatMessage represents a message in the Ollama chat API.
type chatMessage struct {
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	ToolCalls []ollamaToolCall  `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	Function ollamaFunction `json:"function"`
}

type ollamaFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
	Tools    []any          `json:"tools,omitempty"`
}

type chatResponse struct {
	Message       chatMessage `json:"message"`
	Done          bool        `json:"done"`
	PromptEvalCount int       `json:"prompt_eval_count,omitempty"`
	EvalCount       int       `json:"eval_count,omitempty"`
}

// SendMessage sends a message and returns a streaming response.
func (c *OllamaClient) SendMessage(ctx context.Context, message string) (*sdk.StreamResponse, error) {
	return c.SendMessageWithHistory(ctx, nil, message)
}

// SendMessageWithHistory sends a message with conversation history.
func (c *OllamaClient) SendMessageWithHistory(ctx context.Context, history []*genai.Content, message string) (*sdk.StreamResponse, error) {
	messages := c.convertHistory(history)

	c.mu.RLock()
	if c.systemInstruction != "" {
		messages = append([]chatMessage{{Role: "system", Content: c.systemInstruction}}, messages...)
	}
	c.mu.RUnlock()

	if message != "" {
		messages = append(messages, chatMessage{Role: "user", Content: message})
	}

	return c.doChat(ctx, messages)
}

// SendFunctionResponse sends function call results back to the model.
func (c *OllamaClient) SendFunctionResponse(ctx context.Context, history []*genai.Content, results []*genai.FunctionResponse) (*sdk.StreamResponse, error) {
	messages := c.convertHistory(history)

	c.mu.RLock()
	if c.systemInstruction != "" {
		messages = append([]chatMessage{{Role: "system", Content: c.systemInstruction}}, messages...)
	}
	c.mu.RUnlock()

	// Add tool results as user messages
	for _, result := range results {
		var contentStr string
		if result.Response != nil {
			if val, ok := result.Response["content"].(string); ok {
				contentStr = val
			}
			if errStr, ok := result.Response["error"].(string); ok && errStr != "" {
				contentStr = "Error: " + errStr
			}
		}
		if contentStr == "" {
			contentStr = "Operation completed"
		}

		messages = append(messages, chatMessage{
			Role:    "user",
			Content: fmt.Sprintf("Tool result for %s:\n%s", result.Name, contentStr),
		})
	}

	return c.doChat(ctx, messages)
}

// SetTools sets the tools available for function calling.
func (c *OllamaClient) SetTools(tools []*genai.Tool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = tools
}

// SetSystemInstruction sets the system-level instruction.
func (c *OllamaClient) SetSystemInstruction(instruction string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.systemInstruction = instruction
}

// GetModel returns the model name.
func (c *OllamaClient) GetModel() string {
	return c.model
}

// Clone returns an independent copy that shares the underlying http.Client
// but has its own tools and systemInstruction state.
func (c *OllamaClient) Clone() sdk.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	clone := &OllamaClient{
		baseURL:     c.baseURL,
		model:       c.model,
		temperature: c.temperature,
		maxTokens:   c.maxTokens,
		httpClient:  c.httpClient,
	}
	if c.tools != nil {
		clone.tools = make([]*genai.Tool, len(c.tools))
		copy(clone.tools, c.tools)
	}
	clone.systemInstruction = c.systemInstruction
	return clone
}

// Close closes the client.
func (c *OllamaClient) Close() error {
	return nil
}

// doChat performs a streaming chat request.
func (c *OllamaClient) doChat(ctx context.Context, messages []chatMessage) (*sdk.StreamResponse, error) {
	req := chatRequest{
		Model:    c.model,
		Messages: messages,
		Stream:   true,
		Options:  map[string]any{"num_predict": c.maxTokens},
	}

	if c.temperature > 0 {
		req.Options["temperature"] = c.temperature
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := strings.TrimSuffix(c.baseURL, "/") + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("Ollama error (status %d): %s", resp.StatusCode, string(body))
	}

	chunks := make(chan sdk.ResponseChunk, 10)
	done := make(chan struct{})

	go func() {
		defer close(chunks)
		defer close(done)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var chatResp chatResponse
			if err := json.Unmarshal([]byte(line), &chatResp); err != nil {
				continue
			}

			chunk := sdk.ResponseChunk{}

			if chatResp.Message.Content != "" {
				chunk.Text = chatResp.Message.Content
			}

			// Convert tool calls
			for _, tc := range chatResp.Message.ToolCalls {
				chunk.FunctionCalls = append(chunk.FunctionCalls, &genai.FunctionCall{
					Name: tc.Function.Name,
					Args: tc.Function.Arguments,
				})
			}

			if chatResp.Done {
				chunk.Done = true
				chunk.FinishReason = genai.FinishReasonStop
				chunk.InputTokens = chatResp.PromptEvalCount
				chunk.OutputTokens = chatResp.EvalCount
			}

			select {
			case chunks <- chunk:
			case <-ctx.Done():
				return
			}

			if chunk.Done {
				return
			}
		}
	}()

	return &sdk.StreamResponse{
		Chunks: chunks,
		Done:   done,
	}, nil
}

// convertHistory converts genai history to Ollama messages.
func (c *OllamaClient) convertHistory(history []*genai.Content) []chatMessage {
	messages := make([]chatMessage, 0, len(history))

	for _, content := range history {
		msg := chatMessage{}

		switch content.Role {
		case "user":
			msg.Role = "user"
		case "model":
			msg.Role = "assistant"
		default:
			msg.Role = string(content.Role)
		}

		var textParts []string
		for _, part := range content.Parts {
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			}
			if part.FunctionCall != nil {
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				textParts = append(textParts, fmt.Sprintf(
					"Tool call: %s(%s)", part.FunctionCall.Name, string(argsJSON)))
			}
			if part.FunctionResponse != nil {
				var contentStr string
				if val, ok := part.FunctionResponse.Response["content"].(string); ok {
					contentStr = val
				} else {
					jsonBytes, _ := json.Marshal(part.FunctionResponse.Response)
					contentStr = string(jsonBytes)
				}
				textParts = append(textParts, fmt.Sprintf(
					"Tool result for %s: %s", part.FunctionResponse.Name, contentStr))
			}
		}

		msg.Content = strings.Join(textParts, "\n")
		if msg.Content != "" {
			messages = append(messages, msg)
		}
	}

	return messages
}
