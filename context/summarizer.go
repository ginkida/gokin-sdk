package context

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// Summarizer compresses conversation history using an LLM.
type Summarizer struct {
	client sdk.Client
}

// NewSummarizer creates a new conversation summarizer.
func NewSummarizer(client sdk.Client) *Summarizer {
	return &Summarizer{client: client}
}

// Summarize condenses a message history into a single summary message.
func (s *Summarizer) Summarize(ctx context.Context, messages []*genai.Content) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	// Build a text representation of the conversation
	var conv strings.Builder
	for _, msg := range messages {
		role := msg.Role
		if role == "" {
			role = "unknown"
		}
		conv.WriteString(fmt.Sprintf("[%s]: ", role))
		for _, part := range msg.Parts {
			if part.Text != "" {
				conv.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				conv.WriteString(fmt.Sprintf("[called %s]", part.FunctionCall.Name))
			}
			if part.FunctionResponse != nil {
				conv.WriteString(fmt.Sprintf("[result from %s]", part.FunctionResponse.Name))
			}
		}
		conv.WriteString("\n")
	}

	prompt := fmt.Sprintf(`Summarize this conversation concisely, preserving:
1. File paths and function names mentioned
2. Error messages and their resolutions
3. Key decisions made
4. Current task status and next steps

Conversation:
%s

Provide a concise summary:`, conv.String())

	// Temporarily remove tools for summarization
	s.client.SetTools(nil)
	defer s.client.SetTools(nil) // restored by caller

	stream, err := s.client.SendMessage(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("summarization failed: %w", err)
	}

	resp, err := stream.Collect(ctx)
	if err != nil {
		return "", fmt.Errorf("collecting summary: %w", err)
	}

	return resp.Text, nil
}

// SummarizeToContent wraps the summary as a user message for history injection.
func (s *Summarizer) SummarizeToContent(ctx context.Context, messages []*genai.Content) (*genai.Content, error) {
	summary, err := s.Summarize(ctx, messages)
	if err != nil {
		return nil, err
	}

	return &genai.Content{
		Role: "user",
		Parts: []*genai.Part{
			genai.NewPartFromText(fmt.Sprintf("[Previous conversation summary]\n%s", summary)),
		},
	}, nil
}
