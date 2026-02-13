package sdk

import (
	"context"

	"google.golang.org/genai"
)

// StreamHandler provides callbacks for processing streaming responses.
type StreamHandler struct {
	// OnText is called for each text chunk received.
	OnText func(text string)

	// OnToolCall is called for each function call received.
	OnToolCall func(fc *genai.FunctionCall)

	// OnToolResult is not called during stream processing itself,
	// but can be set for use in higher-level orchestration.
	OnToolResult func(name string, result string)

	// OnError is called when an error occurs during streaming.
	OnError func(err error)

	// OnDone is called when the response is complete.
	OnDone func(response *Response)
}

// NewStreamHandler creates a stream handler with the given callbacks.
// Nil callbacks are safely ignored during processing.
func NewStreamHandler(opts ...StreamHandlerOption) *StreamHandler {
	h := &StreamHandler{}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// StreamHandlerOption configures a StreamHandler.
type StreamHandlerOption func(*StreamHandler)

// WithStreamOnText sets the OnText callback.
func WithStreamOnText(fn func(string)) StreamHandlerOption {
	return func(h *StreamHandler) { h.OnText = fn }
}

// WithStreamOnToolCall sets the OnToolCall callback.
func WithStreamOnToolCall(fn func(*genai.FunctionCall)) StreamHandlerOption {
	return func(h *StreamHandler) { h.OnToolCall = fn }
}

// WithStreamOnError sets the OnError callback.
func WithStreamOnError(fn func(error)) StreamHandlerOption {
	return func(h *StreamHandler) { h.OnError = fn }
}

// WithStreamOnDone sets the OnDone callback.
func WithStreamOnDone(fn func(*Response)) StreamHandlerOption {
	return func(h *StreamHandler) { h.OnDone = fn }
}

// ProcessStream processes a streaming response with the given handler,
// accumulating results into a Response.
func ProcessStream(ctx context.Context, sr *StreamResponse, handler *StreamHandler) (*Response, error) {
	resp := &Response{}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case chunk, ok := <-sr.Chunks:
			if !ok {
				if handler.OnDone != nil {
					handler.OnDone(resp)
				}
				return resp, nil
			}

			if chunk.Error != nil {
				if handler.OnError != nil {
					handler.OnError(chunk.Error)
				}
				return nil, chunk.Error
			}

			if chunk.Text != "" {
				resp.Text += chunk.Text
				if handler.OnText != nil {
					handler.OnText(chunk.Text)
				}
			}

			for _, fc := range chunk.FunctionCalls {
				resp.FunctionCalls = append(resp.FunctionCalls, fc)
				resp.Parts = append(resp.Parts, &genai.Part{FunctionCall: fc})
				if handler.OnToolCall != nil {
					handler.OnToolCall(fc)
				}
			}

			// Accumulate non-FunctionCall parts (preserves ThoughtSignature etc.)
			for _, part := range chunk.Parts {
				if part != nil && part.FunctionCall == nil {
					resp.Parts = append(resp.Parts, part)
				}
			}

			if chunk.InputTokens > 0 {
				resp.InputTokens = chunk.InputTokens
			}
			if chunk.OutputTokens > 0 {
				resp.OutputTokens += chunk.OutputTokens
			}

			if chunk.Done {
				resp.FinishReason = chunk.FinishReason
				if handler.OnDone != nil {
					handler.OnDone(resp)
				}
				return resp, nil
			}
		}
	}
}

// CollectText is a convenience function that collects only text from a stream.
func CollectText(ctx context.Context, sr *StreamResponse) (string, error) {
	resp, err := ProcessStream(ctx, sr, &StreamHandler{})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}
