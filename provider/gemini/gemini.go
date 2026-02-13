// Package gemini provides a Gemini client implementation for the SDK.
package gemini

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// Option configures a GeminiClient.
type Option func(*GeminiClient)

// WithTemperature sets the temperature for generation.
func WithTemperature(t float32) Option {
	return func(c *GeminiClient) {
		c.temperature = &t
	}
}

// WithMaxTokens sets the maximum output tokens.
func WithMaxTokens(n int32) Option {
	return func(c *GeminiClient) {
		c.maxOutputTokens = n
	}
}

// WithMaxRetries sets the maximum number of retries.
func WithMaxRetries(n int) Option {
	return func(c *GeminiClient) {
		c.maxRetries = n
	}
}

// GeminiClient implements sdk.Client using Google's Gemini API.
type GeminiClient struct {
	mu                sync.RWMutex
	client            *genai.Client
	model             string
	tools             []*genai.Tool
	systemInstruction string
	temperature       *float32
	maxOutputTokens   int32
	maxRetries        int
	retryDelay        time.Duration
}

// New creates a new Gemini client.
func New(ctx context.Context, apiKey string, model string, opts ...Option) (*GeminiClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	if model == "" {
		return nil, fmt.Errorf("model name is required")
	}

	clientConfig := &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
		APIKey:  apiKey,
	}

	client, err := genai.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	c := &GeminiClient{
		client:     client,
		model:      model,
		maxRetries: 3,
		retryDelay: 1 * time.Second,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// SendMessage sends a user message and returns a streaming response.
func (c *GeminiClient) SendMessage(ctx context.Context, message string) (*sdk.StreamResponse, error) {
	return c.SendMessageWithHistory(ctx, nil, message)
}

// SendMessageWithHistory sends a message with conversation history.
func (c *GeminiClient) SendMessageWithHistory(ctx context.Context, history []*genai.Content, message string) (*sdk.StreamResponse, error) {
	contents := make([]*genai.Content, len(history)+1)
	copy(contents, history)
	contents[len(contents)-1] = genai.NewContentFromText(message, "user")

	return c.generateStream(ctx, contents)
}

// SendFunctionResponse sends function call results back to the model.
func (c *GeminiClient) SendFunctionResponse(ctx context.Context, history []*genai.Content, results []*genai.FunctionResponse) (*sdk.StreamResponse, error) {
	var parts []*genai.Part
	for _, result := range results {
		part := genai.NewPartFromFunctionResponse(result.Name, result.Response)
		part.FunctionResponse.ID = result.ID
		parts = append(parts, part)
	}

	if len(parts) == 0 {
		parts = append(parts, genai.NewPartFromText(" "))
	}

	funcContent := &genai.Content{
		Role:  "user",
		Parts: parts,
	}

	contents := make([]*genai.Content, len(history)+1)
	copy(contents, history)
	contents[len(contents)-1] = funcContent

	return c.generateStream(ctx, contents)
}

// SetTools sets the tools available for function calling.
func (c *GeminiClient) SetTools(tools []*genai.Tool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = tools
}

// SetSystemInstruction sets the system-level instruction.
func (c *GeminiClient) SetSystemInstruction(instruction string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.systemInstruction = instruction
}

// GetModel returns the model name.
func (c *GeminiClient) GetModel() string {
	return c.model
}

// Clone returns an independent copy that shares the underlying genai.Client
// but has its own tools and systemInstruction state.
func (c *GeminiClient) Clone() sdk.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	clone := &GeminiClient{
		client:          c.client,
		model:           c.model,
		temperature:     c.temperature,
		maxOutputTokens: c.maxOutputTokens,
		maxRetries:      c.maxRetries,
		retryDelay:      c.retryDelay,
	}
	if c.tools != nil {
		clone.tools = make([]*genai.Tool, len(c.tools))
		copy(clone.tools, c.tools)
	}
	clone.systemInstruction = c.systemInstruction
	return clone
}

// Close closes the client connection.
func (c *GeminiClient) Close() error {
	return nil
}

// generateStream handles streaming content generation with retry logic.
func (c *GeminiClient) generateStream(ctx context.Context, contents []*genai.Content) (*sdk.StreamResponse, error) {
	// Sanitize contents
	contents = sanitizeContents(contents)

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

		response, err := c.doGenerateStream(ctx, contents)
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

// doGenerateStream performs a single streaming request.
func (c *GeminiClient) doGenerateStream(ctx context.Context, contents []*genai.Content) (*sdk.StreamResponse, error) {
	// Snapshot mutable state under read lock
	c.mu.RLock()
	tools := c.tools
	sysInstruction := c.systemInstruction
	c.mu.RUnlock()

	config := &genai.GenerateContentConfig{}
	if c.temperature != nil {
		config.Temperature = c.temperature
	}
	if c.maxOutputTokens > 0 {
		config.MaxOutputTokens = ptr(c.maxOutputTokens)
	}
	if sysInstruction != "" {
		config.SystemInstruction = genai.NewContentFromText(sysInstruction, "user")
	}
	if len(tools) > 0 {
		config.Tools = tools
	}

	iter := c.client.Models.GenerateContentStream(ctx, c.model, contents, config)

	chunks := make(chan sdk.ResponseChunk, 10)
	done := make(chan struct{})

	go func() {
		defer close(chunks)
		defer close(done)

		for resp, err := range iter {
			if err != nil {
				select {
				case chunks <- sdk.ResponseChunk{Error: err, Done: true}:
				case <-ctx.Done():
				}
				return
			}

			if resp == nil {
				continue
			}

			chunk := processResponse(resp)

			select {
			case chunks <- chunk:
			case <-ctx.Done():
				select {
				case chunks <- sdk.ResponseChunk{Error: ctx.Err(), Done: true}:
				default:
				}
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

// processResponse converts a Gemini response to a ResponseChunk.
func processResponse(resp *genai.GenerateContentResponse) sdk.ResponseChunk {
	chunk := sdk.ResponseChunk{}

	if resp.UsageMetadata != nil {
		if resp.UsageMetadata.PromptTokenCount != nil {
			chunk.InputTokens = int(*resp.UsageMetadata.PromptTokenCount)
		}
		if resp.UsageMetadata.CandidatesTokenCount != nil {
			chunk.OutputTokens = int(*resp.UsageMetadata.CandidatesTokenCount)
		}
	}

	if len(resp.Candidates) == 0 {
		chunk.Done = true
		return chunk
	}

	candidate := resp.Candidates[0]
	chunk.FinishReason = candidate.FinishReason

	if candidate.Content != nil {
		chunk.Parts = candidate.Content.Parts

		for _, part := range candidate.Content.Parts {
			if part.Thought {
				continue // Skip thinking parts for SDK simplicity
			}
			if part.Text != "" {
				chunk.Text += part.Text
			}
			if part.FunctionCall != nil {
				chunk.FunctionCalls = append(chunk.FunctionCalls, part.FunctionCall)
			}
		}
	}

	if candidate.FinishReason != "" {
		chunk.Done = true
	}

	return chunk
}

// sanitizeContents validates and fixes contents before sending.
func sanitizeContents(contents []*genai.Content) []*genai.Content {
	var result []*genai.Content

	for _, content := range contents {
		if content == nil {
			continue
		}

		var validParts []*genai.Part
		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionCall != nil || part.FunctionResponse != nil || part.Text != "" || part.InlineData != nil {
				validParts = append(validParts, part)
			}
		}

		if len(validParts) == 0 {
			validParts = []*genai.Part{genai.NewPartFromText(" ")}
		}

		result = append(result, &genai.Content{
			Role:  content.Role,
			Parts: validParts,
		})
	}

	if len(result) == 0 {
		result = []*genai.Content{{
			Role:  "user",
			Parts: []*genai.Part{genai.NewPartFromText(" ")},
		}}
	}

	return result
}

// isRetryable returns true if the error should trigger a retry.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	retryablePatterns := []string{"429", "500", "502", "503", "504", "connection refused", "timeout", "UNAVAILABLE", "RESOURCE_EXHAUSTED"}
	for _, pattern := range retryablePatterns {
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(pattern)) {
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

// ptr returns a pointer to the given value.
func ptr[T any](v T) *T {
	return &v
}
