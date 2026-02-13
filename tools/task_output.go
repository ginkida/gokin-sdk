package tools

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// AgentLister lists running and completed agents.
type AgentLister interface {
	ListRunning() []string
	GetResult(agentID string) (*sdk.AgentResult, bool)
	Wait(ctx context.Context, agentID string) (*sdk.AgentResult, error)
}

// TaskOutputTool retrieves results from background agents.
type TaskOutputTool struct {
	lister AgentLister
}

// NewTaskOutput creates a new TaskOutputTool.
func NewTaskOutput() *TaskOutputTool {
	return &TaskOutputTool{}
}

// SetLister sets the agent lister.
func (t *TaskOutputTool) SetLister(lister AgentLister) {
	t.lister = lister
}

func (t *TaskOutputTool) Name() string        { return "task_output" }
func (t *TaskOutputTool) Description() string { return "Get the output from a running or completed background task." }

func (t *TaskOutputTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"action": {
					Type:        genai.TypeString,
					Description: "Action to perform: get (get result for a task), list (list all tasks)",
					Enum:        []string{"get", "list"},
				},
				"task_id": {
					Type:        genai.TypeString,
					Description: "The agent/task ID to get output from (required for 'get' action)",
				},
				"block": {
					Type:        genai.TypeBoolean,
					Description: "If true, wait for the task to complete before returning (default: true)",
				},
			},
			Required: []string{"action"},
		},
	}
}

func (t *TaskOutputTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	if t.lister == nil {
		return sdk.NewErrorResult("task_output: no agent lister configured"), nil
	}

	action, ok := sdk.GetString(args, "action")
	if !ok {
		return sdk.NewErrorResult("action is required"), nil
	}

	switch action {
	case "get":
		return t.executeGet(ctx, args)
	case "list":
		return t.executeList()
	default:
		return sdk.NewErrorResult(fmt.Sprintf("unknown action: %s (use get, list)", action)), nil
	}
}

func (t *TaskOutputTool) executeGet(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	taskID, ok := sdk.GetString(args, "task_id")
	if !ok || taskID == "" {
		return sdk.NewErrorResult("task_id is required for get action"), nil
	}

	block := sdk.GetBoolDefault(args, "block", true)

	// Try non-blocking first
	result, found := t.lister.GetResult(taskID)
	if found {
		return formatAgentResult(taskID, result), nil
	}

	if !block {
		return sdk.NewSuccessResult(fmt.Sprintf("Task %s is still running. Use block=true to wait.", taskID)), nil
	}

	// Block until completion
	result, err := t.lister.Wait(ctx, taskID)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("failed to wait for task %s: %s", taskID, err)), nil
	}

	return formatAgentResult(taskID, result), nil
}

func (t *TaskOutputTool) executeList() (*sdk.ToolResult, error) {
	running := t.lister.ListRunning()
	if len(running) == 0 {
		return sdk.NewSuccessResult("No running tasks."), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Running tasks (%d):\n", len(running)))
	for _, id := range running {
		sb.WriteString(fmt.Sprintf("- %s\n", id))
	}
	return sdk.NewSuccessResult(sb.String()), nil
}

func formatAgentResult(taskID string, result *sdk.AgentResult) *sdk.ToolResult {
	if result == nil {
		return sdk.NewErrorResult(fmt.Sprintf("no result for task %s", taskID))
	}

	content := fmt.Sprintf("Task %s completed in %d turns (%s).\n\n%s",
		taskID, result.Turns, result.Duration, result.Text)
	if result.Error != nil {
		content += fmt.Sprintf("\n\nError: %s", result.Error)
		return &sdk.ToolResult{Content: content, Success: false}
	}
	return sdk.NewSuccessResult(content)
}
