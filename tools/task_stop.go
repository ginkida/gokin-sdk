package tools

import (
	"context"
	"fmt"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// AgentCanceller cancels running agents.
type AgentCanceller interface {
	Cancel(agentID string) error
}

// TaskStopTool stops a running background task.
type TaskStopTool struct {
	canceller AgentCanceller
}

// NewTaskStop creates a new TaskStopTool.
func NewTaskStop() *TaskStopTool {
	return &TaskStopTool{}
}

// SetCanceller sets the agent canceller.
func (t *TaskStopTool) SetCanceller(canceller AgentCanceller) {
	t.canceller = canceller
}

func (t *TaskStopTool) Name() string        { return "task_stop" }
func (t *TaskStopTool) Description() string { return "Stop a running background task by its ID." }

func (t *TaskStopTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"task_id": {
					Type:        genai.TypeString,
					Description: "The ID of the task to stop",
				},
				"reason": {
					Type:        genai.TypeString,
					Description: "Reason for stopping the task (optional)",
				},
			},
			Required: []string{"task_id"},
		},
	}
}

func (t *TaskStopTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	if t.canceller == nil {
		return sdk.NewErrorResult("task_stop: no canceller configured"), nil
	}

	taskID, ok := sdk.GetString(args, "task_id")
	if !ok || taskID == "" {
		return sdk.NewErrorResult("task_id is required"), nil
	}

	reason := sdk.GetStringDefault(args, "reason", "")

	if err := t.canceller.Cancel(taskID); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("failed to stop task %s: %s", taskID, err)), nil
	}

	msg := fmt.Sprintf("Task %s stopped.", taskID)
	if reason != "" {
		msg += fmt.Sprintf(" Reason: %s", reason)
	}
	return sdk.NewSuccessResult(msg), nil
}
