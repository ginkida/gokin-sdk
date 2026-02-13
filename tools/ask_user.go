package tools

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// QuestionHandler handles user questions and returns their response.
type QuestionHandler func(ctx context.Context, question string, options []string, defaultOption string) (string, error)

// AskUserTool allows the agent to ask the user a question.
type AskUserTool struct {
	handler QuestionHandler
}

// NewAskUser creates a new AskUserTool.
func NewAskUser() *AskUserTool {
	return &AskUserTool{}
}

// SetHandler sets the question handler.
func (t *AskUserTool) SetHandler(handler QuestionHandler) {
	t.handler = handler
}

func (t *AskUserTool) Name() string        { return "ask_user" }
func (t *AskUserTool) Description() string { return "Ask the user a question and wait for their response." }

func (t *AskUserTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"question": {
					Type:        genai.TypeString,
					Description: "The question to ask the user",
				},
				"options": {
					Type:        genai.TypeString,
					Description: "Comma-separated list of options for the user to choose from (optional)",
				},
				"default": {
					Type:        genai.TypeString,
					Description: "Default option if the user doesn't respond (optional)",
				},
			},
			Required: []string{"question"},
		},
	}
}

func (t *AskUserTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	if t.handler == nil {
		return sdk.NewErrorResult("ask_user: no question handler configured"), nil
	}

	question, ok := sdk.GetString(args, "question")
	if !ok || question == "" {
		return sdk.NewErrorResult("question is required"), nil
	}

	var options []string
	if optStr, ok := sdk.GetString(args, "options"); ok && optStr != "" {
		for _, opt := range strings.Split(optStr, ",") {
			if trimmed := strings.TrimSpace(opt); trimmed != "" {
				options = append(options, trimmed)
			}
		}
	}

	defaultOpt := sdk.GetStringDefault(args, "default", "")

	response, err := t.handler(ctx, question, options, defaultOpt)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("failed to get user response: %s", err)), nil
	}

	return sdk.NewSuccessResult(response), nil
}
