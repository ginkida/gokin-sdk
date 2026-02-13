package sdk

import (
	"context"

	"google.golang.org/genai"
)

// StreamResponse represents a streaming response from the model.
type StreamResponse struct {
	// Chunks is a channel that receives response chunks.
	Chunks <-chan ResponseChunk

	// Done is closed when the response is complete.
	Done <-chan struct{}
}

// ResponseChunk represents a single chunk in a streaming response.
type ResponseChunk struct {
	// Text contains any text content in this chunk.
	Text string

	// FunctionCalls contains any function calls in this chunk.
	FunctionCalls []*genai.FunctionCall

	// Parts contains the original parts from the response.
	Parts []*genai.Part

	// Error contains any error that occurred.
	Error error

	// Done indicates if this is the final chunk.
	Done bool

	// FinishReason indicates why the response finished.
	FinishReason genai.FinishReason

	// InputTokens from API usage metadata (if available).
	InputTokens int

	// OutputTokens from API usage metadata (if available).
	OutputTokens int
}

// Response represents a complete response from the model.
type Response struct {
	// Text is the accumulated text response.
	Text string

	// FunctionCalls contains all function calls from the response.
	FunctionCalls []*genai.FunctionCall

	// Parts contains all original parts from the response.
	Parts []*genai.Part

	// FinishReason indicates why the response finished.
	FinishReason genai.FinishReason

	// InputTokens from API usage metadata.
	InputTokens int

	// OutputTokens from API usage metadata.
	OutputTokens int
}

// Collect collects all chunks from a streaming response into a single Response.
func (sr *StreamResponse) Collect(ctx context.Context) (*Response, error) {
	resp := &Response{}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case chunk, ok := <-sr.Chunks:
			if !ok {
				return resp, nil
			}

			if chunk.Error != nil {
				return nil, chunk.Error
			}

			resp.Text += chunk.Text

			// Add function calls and their corresponding parts
			for _, fc := range chunk.FunctionCalls {
				resp.FunctionCalls = append(resp.FunctionCalls, fc)
				resp.Parts = append(resp.Parts, &genai.Part{FunctionCall: fc})
			}

			// Add non-FunctionCall parts
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
				return resp, nil
			}
		}
	}
}
