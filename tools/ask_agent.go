package tools

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// AgentMessenger handles inter-agent communication.
type AgentMessenger interface {
	SendMessage(ctx context.Context, targetRole, query, requestType string) (string, error)
}

// AskAgentTool allows an agent to send a question to another agent by role.
type AskAgentTool struct {
	messenger AgentMessenger
}

// NewAskAgent creates a new AskAgentTool.
func NewAskAgent() *AskAgentTool {
	return &AskAgentTool{}
}

// SetMessenger sets the agent messenger.
func (t *AskAgentTool) SetMessenger(messenger AgentMessenger) {
	t.messenger = messenger
}

func (t *AskAgentTool) Name() string        { return "ask_agent" }
func (t *AskAgentTool) Description() string { return "Send a question to another agent by role and get a response." }

func (t *AskAgentTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"target_role": {
					Type:        genai.TypeString,
					Description: "The role of the agent to ask (e.g., 'planner', 'explorer', 'reviewer')",
				},
				"query": {
					Type:        genai.TypeString,
					Description: "The question or request to send",
				},
				"request_type": {
					Type:        genai.TypeString,
					Description: "Type of request: question, review, analysis, suggestion",
					Enum:        []string{"question", "review", "analysis", "suggestion"},
				},
				"response_format": {
					Type:        genai.TypeString,
					Description: "Expected response format: text, json, list",
					Enum:        []string{"text", "json", "list"},
				},
			},
			Required: []string{"target_role", "query"},
		},
	}
}

func (t *AskAgentTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	if t.messenger == nil {
		return sdk.NewErrorResult("ask_agent: no messenger configured"), nil
	}

	targetRole, ok := sdk.GetString(args, "target_role")
	if !ok || targetRole == "" {
		return sdk.NewErrorResult("target_role is required"), nil
	}

	query, ok := sdk.GetString(args, "query")
	if !ok || query == "" {
		return sdk.NewErrorResult("query is required"), nil
	}

	requestType := sdk.GetStringDefault(args, "request_type", "question")
	responseFormat := sdk.GetStringDefault(args, "response_format", "text")

	// Build the full query with metadata
	var fullQuery strings.Builder
	fullQuery.WriteString(fmt.Sprintf("[%s request", requestType))
	if responseFormat != "text" {
		fullQuery.WriteString(fmt.Sprintf(", format: %s", responseFormat))
	}
	fullQuery.WriteString("] ")
	fullQuery.WriteString(query)

	response, err := t.messenger.SendMessage(ctx, targetRole, fullQuery.String(), requestType)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("failed to get response from %s: %s", targetRole, err)), nil
	}

	return sdk.NewSuccessResult(response), nil
}
