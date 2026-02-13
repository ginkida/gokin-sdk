// Package anthropic provides an Anthropic-compatible client implementation for the SDK.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

// Option configures an AnthropicClient.
type Option func(*AnthropicClient)

// WithBaseURL sets a custom base URL (for compatible APIs like GLM, DeepSeek).
func WithBaseURL(url string) Option {
	return func(c *AnthropicClient) {
		c.baseURL = url
	}
}

// WithMaxTokens sets the maximum output tokens.
func WithMaxTokens(n int32) Option {
	return func(c *AnthropicClient) {
		c.maxTokens = n
	}
}

// WithTemperature sets the temperature for generation.
func WithTemperature(t float32) Option {
	return func(c *AnthropicClient) {
		c.temperature = t
	}
}

// WithMaxRetries sets the maximum number of retries.
func WithMaxRetries(n int) Option {
	return func(c *AnthropicClient) {
		c.maxRetries = n
	}
}

// AnthropicClient implements sdk.Client for the Anthropic Messages API.
type AnthropicClient struct {
	apiKey            string
	baseURL           string
	model             string
	maxTokens         int32
	temperature       float32
	maxRetries        int
	retryDelay        time.Duration
	httpClient        *http.Client
	tools             []*genai.Tool
	systemInstruction string
	mu                sync.RWMutex
}

// New creates a new Anthropic-compatible client.
func New(apiKey string, model string, opts ...Option) (*AnthropicClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	if model == "" {
		return nil, fmt.Errorf("model name is required")
	}

	c := &AnthropicClient{
		apiKey:     apiKey,
		baseURL:    "https://api.anthropic.com",
		model:      model,
		maxTokens:  4096,
		maxRetries: 3,
		retryDelay: 1 * time.Second,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// SendMessage sends a message and returns a streaming response.
func (c *AnthropicClient) SendMessage(ctx context.Context, message string) (*sdk.StreamResponse, error) {
	return c.SendMessageWithHistory(ctx, nil, message)
}

// SendMessageWithHistory sends a message with conversation history.
func (c *AnthropicClient) SendMessageWithHistory(ctx context.Context, history []*genai.Content, message string) (*sdk.StreamResponse, error) {
	c.mu.RLock()
	sysInstruction := c.systemInstruction
	c.mu.RUnlock()

	messages := convertHistoryToMessages(history, message)

	requestBody := map[string]interface{}{
		"model":      c.model,
		"max_tokens": c.maxTokens,
		"messages":   messages,
		"stream":     true,
	}

	if sysInstruction != "" {
		requestBody["system"] = sysInstruction
	}
	if c.temperature > 0 {
		requestBody["temperature"] = c.temperature
	}

	c.mu.RLock()
	if len(c.tools) > 0 {
		requestBody["tools"] = convertToolsToAnthropic(c.tools)
	}
	c.mu.RUnlock()

	return c.streamRequest(ctx, requestBody)
}

// SendFunctionResponse sends function call results back to the model.
func (c *AnthropicClient) SendFunctionResponse(ctx context.Context, history []*genai.Content, results []*genai.FunctionResponse) (*sdk.StreamResponse, error) {
	c.mu.RLock()
	sysInstruction := c.systemInstruction
	c.mu.RUnlock()

	messages := convertHistoryWithResults(history, results)

	requestBody := map[string]interface{}{
		"model":      c.model,
		"max_tokens": c.maxTokens,
		"messages":   messages,
		"stream":     true,
	}

	if sysInstruction != "" {
		requestBody["system"] = sysInstruction
	}
	if c.temperature > 0 {
		requestBody["temperature"] = c.temperature
	}

	c.mu.RLock()
	if len(c.tools) > 0 {
		requestBody["tools"] = convertToolsToAnthropic(c.tools)
	}
	c.mu.RUnlock()

	return c.streamRequest(ctx, requestBody)
}

// SetTools sets the tools available for function calling.
func (c *AnthropicClient) SetTools(tools []*genai.Tool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = tools
}

// SetSystemInstruction sets the system-level instruction.
func (c *AnthropicClient) SetSystemInstruction(instruction string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.systemInstruction = instruction
}

// GetModel returns the model name.
func (c *AnthropicClient) GetModel() string {
	return c.model
}

// Clone returns an independent copy that shares the underlying http.Client
// but has its own tools and systemInstruction state.
func (c *AnthropicClient) Clone() sdk.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	clone := &AnthropicClient{
		apiKey:      c.apiKey,
		baseURL:     c.baseURL,
		model:       c.model,
		maxTokens:   c.maxTokens,
		temperature: c.temperature,
		maxRetries:  c.maxRetries,
		retryDelay:  c.retryDelay,
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
func (c *AnthropicClient) Close() error {
	return nil
}

// streamRequest performs a streaming request with retry logic.
func (c *AnthropicClient) streamRequest(ctx context.Context, requestBody map[string]interface{}) (*sdk.StreamResponse, error) {
	var lastErr error
	maxDelay := 30 * time.Second

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoff(c.retryDelay, attempt-1, maxDelay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		response, err := c.doStreamRequest(ctx, requestBody)
		if err == nil {
			return response, nil
		}

		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("max retries (%d) exceeded: %w", c.maxRetries, lastErr)
}

// doStreamRequest performs a single streaming request.
func (c *AnthropicClient) doStreamRequest(ctx context.Context, requestBody map[string]interface{}) (*sdk.StreamResponse, error) {
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := strings.TrimSuffix(c.baseURL, "/") + "/v1/messages"

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	chunks := make(chan sdk.ResponseChunk, 10)
	done := make(chan struct{})

	go func() {
		defer close(chunks)
		defer close(done)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		acc := &toolCallAccumulator{
			completedCalls: make([]*genai.FunctionCall, 0),
		}

		for scanner.Scan() {
			line := scanner.Text()

			var data string
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
			} else if strings.HasPrefix(line, "data:") {
				data = strings.TrimPrefix(line, "data:")
			} else {
				continue
			}

			if data == "[DONE]" {
				if len(acc.completedCalls) > 0 {
					chunks <- sdk.ResponseChunk{FunctionCalls: acc.completedCalls, Done: true}
				} else {
					chunks <- sdk.ResponseChunk{Done: true}
				}
				return
			}

			var event map[string]interface{}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			chunk := processStreamEvent(event, acc)
			if chunk.Text != "" || chunk.Done || len(chunk.FunctionCalls) > 0 {
				select {
				case chunks <- chunk:
				case <-ctx.Done():
					return
				}
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

// toolCallAccumulator tracks tool call state during streaming.
type toolCallAccumulator struct {
	currentToolID   string
	currentToolName string
	currentInput    strings.Builder
	completedCalls  []*genai.FunctionCall
	currentBlockType string
}

// processStreamEvent converts an Anthropic stream event to a ResponseChunk.
func processStreamEvent(event map[string]interface{}, acc *toolCallAccumulator) sdk.ResponseChunk {
	chunk := sdk.ResponseChunk{}

	eventType, _ := event["type"].(string)

	switch eventType {
	case "content_block_start":
		if cb, ok := event["content_block"].(map[string]interface{}); ok {
			blockType, _ := cb["type"].(string)
			acc.currentBlockType = blockType
			if blockType == "tool_use" {
				if name, ok := cb["name"].(string); ok {
					acc.currentToolName = name
				}
				if id, ok := cb["id"].(string); ok && id != "" {
					acc.currentToolID = id
				} else {
					acc.currentToolID = randomID()
				}
				acc.currentInput.Reset()
			}
		}

	case "content_block_delta":
		if delta, ok := event["delta"].(map[string]interface{}); ok {
			deltaType, _ := delta["type"].(string)
			if deltaType == "text_delta" {
				if text, ok := delta["text"].(string); ok {
					chunk.Text = text
				}
			}
			if deltaType == "input_json_delta" {
				if partial, ok := delta["partial_json"].(string); ok {
					acc.currentInput.WriteString(partial)
				}
			}
		}

	case "content_block_stop":
		if acc.currentToolID != "" && acc.currentToolName != "" {
			inputJSON := acc.currentInput.String()
			var args map[string]interface{}
			if inputJSON != "" {
				if err := json.Unmarshal([]byte(inputJSON), &args); err != nil {
					args = make(map[string]interface{})
				}
			} else {
				args = make(map[string]interface{})
			}

			acc.completedCalls = append(acc.completedCalls, &genai.FunctionCall{
				ID:   acc.currentToolID,
				Name: acc.currentToolName,
				Args: args,
			})

			acc.currentToolID = ""
			acc.currentToolName = ""
			acc.currentInput.Reset()
		}
		acc.currentBlockType = ""

	case "message_delta":
		if delta, ok := event["delta"].(map[string]interface{}); ok {
			if stopReason, ok := delta["stop_reason"].(string); ok {
				chunk.Done = true
				switch stopReason {
				case "end_turn":
					chunk.FinishReason = genai.FinishReasonStop
				case "max_tokens":
					chunk.FinishReason = genai.FinishReasonMaxTokens
				case "tool_use":
					if len(acc.completedCalls) > 0 {
						chunk.FunctionCalls = acc.completedCalls
					}
					chunk.FinishReason = genai.FinishReasonStop
				}
			}
		}

	case "message_stop":
		chunk.Done = true
		if len(acc.completedCalls) > 0 {
			chunk.FunctionCalls = acc.completedCalls
		}
	}

	return chunk
}

// convertHistoryToMessages converts genai history to Anthropic messages format.
func convertHistoryToMessages(history []*genai.Content, newMessage string) []map[string]interface{} {
	messages := make([]map[string]interface{}, 0)

	for _, content := range history {
		if content.Role == "user" {
			messages = append(messages, buildUserMessage(content.Parts))
		} else if content.Role == "model" {
			messages = append(messages, buildAssistantMessage(content.Parts))
		}
	}

	if newMessage == "" {
		newMessage = "Continue."
	}
	messages = append(messages, map[string]interface{}{
		"role":    "user",
		"content": []map[string]interface{}{{"type": "text", "text": newMessage}},
	})

	return messages
}

// convertHistoryWithResults converts history with function results to messages.
func convertHistoryWithResults(history []*genai.Content, results []*genai.FunctionResponse) []map[string]interface{} {
	messages := make([]map[string]interface{}, 0)

	for _, content := range history {
		if content.Role == "user" {
			messages = append(messages, buildUserMessage(content.Parts))
		} else if content.Role == "model" {
			messages = append(messages, buildAssistantMessage(content.Parts))
		}
	}

	resultContents := make([]map[string]interface{}, 0)
	for _, result := range results {
		toolUseID := result.ID
		if toolUseID == "" {
			toolUseID = result.Name
		}

		var contentStr string
		if result.Response != nil {
			if c, ok := result.Response["content"].(string); ok {
				contentStr = c
			}
			if errStr, ok := result.Response["error"].(string); ok && errStr != "" {
				contentStr = "Error: " + errStr
			}
		}
		if contentStr == "" {
			contentStr = "Operation completed"
		}

		resultContents = append(resultContents, map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": toolUseID,
			"content":     contentStr,
		})
	}

	messages = append(messages, map[string]interface{}{
		"role":    "user",
		"content": resultContents,
	})

	return messages
}

// buildUserMessage builds a user message from parts.
func buildUserMessage(parts []*genai.Part) map[string]interface{} {
	content := make([]map[string]interface{}, 0)

	for _, part := range parts {
		if part.Text != "" {
			content = append(content, map[string]interface{}{
				"type": "text",
				"text": part.Text,
			})
		}
		if part.FunctionResponse != nil {
			toolUseID := part.FunctionResponse.ID
			if toolUseID == "" {
				toolUseID = part.FunctionResponse.Name
			}

			var contentStr string
			if part.FunctionResponse.Response != nil {
				if c, ok := part.FunctionResponse.Response["content"].(string); ok {
					contentStr = c
				}
				if errStr, ok := part.FunctionResponse.Response["error"].(string); ok && errStr != "" {
					contentStr = "Error: " + errStr
				}
			}
			if contentStr == "" {
				contentStr = "Operation completed"
			}

			content = append(content, map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": toolUseID,
				"content":     contentStr,
			})
		}
	}

	if len(content) == 0 {
		content = append(content, map[string]interface{}{
			"type": "text",
			"text": "Continue.",
		})
	}

	return map[string]interface{}{
		"role":    "user",
		"content": content,
	}
}

// buildAssistantMessage builds an assistant message from parts.
func buildAssistantMessage(parts []*genai.Part) map[string]interface{} {
	content := make([]map[string]interface{}, 0)

	for _, part := range parts {
		if part.Text != "" {
			content = append(content, map[string]interface{}{
				"type": "text",
				"text": part.Text,
			})
		}
		if part.FunctionCall != nil {
			toolID := part.FunctionCall.ID
			if toolID == "" {
				toolID = part.FunctionCall.Name
			}
			content = append(content, map[string]interface{}{
				"type":  "tool_use",
				"id":    toolID,
				"name":  part.FunctionCall.Name,
				"input": part.FunctionCall.Args,
			})
		}
	}

	if len(content) == 0 {
		content = append(content, map[string]interface{}{
			"type": "text",
			"text": " ",
		})
	}

	return map[string]interface{}{
		"role":    "assistant",
		"content": content,
	}
}

// convertToolsToAnthropic converts genai tools to Anthropic format.
func convertToolsToAnthropic(tools []*genai.Tool) []map[string]interface{} {
	result := make([]map[string]interface{}, 0)

	for _, tool := range tools {
		for _, decl := range tool.FunctionDeclarations {
			inputSchema := convertSchemaToJSON(decl.Parameters)
			result = append(result, map[string]interface{}{
				"name":         decl.Name,
				"description":  decl.Description,
				"input_schema": inputSchema,
			})
		}
	}

	return result
}

// convertSchemaToJSON converts a genai.Schema to Anthropic-compatible JSON Schema.
func convertSchemaToJSON(schema *genai.Schema) map[string]interface{} {
	if schema == nil {
		return nil
	}

	result := make(map[string]interface{})

	if schema.Type != "" {
		result["type"] = strings.ToLower(string(schema.Type))
	}
	if schema.Description != "" {
		result["description"] = schema.Description
	}
	if len(schema.Enum) > 0 {
		result["enum"] = schema.Enum
	}
	if len(schema.Properties) > 0 {
		props := make(map[string]interface{})
		for name, propSchema := range schema.Properties {
			props[name] = convertSchemaToJSON(propSchema)
		}
		result["properties"] = props
	}
	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}
	if schema.Items != nil {
		result["items"] = convertSchemaToJSON(schema.Items)
	}

	return result
}

// randomID generates a unique ID for tool_use.
func randomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("toolu_%d", time.Now().UnixNano())
	}
	return "toolu_" + hex.EncodeToString(b)
}

// isRetryable returns true if the error should trigger a retry.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	patterns := []string{"429", "500", "502", "503", "504", "timeout", "connection refused", "connection reset"}
	for _, p := range patterns {
		if strings.Contains(errStr, p) {
			return true
		}
	}
	return false
}

// backoff calculates exponential backoff delay.
func backoff(base time.Duration, attempt int, max time.Duration) time.Duration {
	delay := base
	for i := 0; i < attempt; i++ {
		delay *= 2
	}
	if delay > max {
		delay = max
	}
	return delay
}
