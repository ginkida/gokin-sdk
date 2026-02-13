package tools

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// PlanStatus represents the current status of a plan.
type PlanStatus struct {
	ID            string `json:"id"`
	Status        string `json:"status"` // created, executing, completed, failed, cancelled
	TotalSteps    int    `json:"total_steps"`
	CompletedSteps int   `json:"completed_steps"`
	CurrentStep   string `json:"current_step"`
	Error         string `json:"error,omitempty"`
}

// PlanExecutor manages plan lifecycle.
type PlanExecutor interface {
	Create(ctx context.Context, description string, steps []string) (string, error)
	Execute(ctx context.Context, planID string) error
	GetStatus(planID string) (*PlanStatus, error)
	Cancel(planID string) error
}

// PlanModeTool manages plan creation and execution.
type PlanModeTool struct {
	executor PlanExecutor
}

// NewPlanMode creates a new PlanModeTool.
func NewPlanMode() *PlanModeTool {
	return &PlanModeTool{}
}

// SetExecutor sets the plan executor.
func (t *PlanModeTool) SetExecutor(executor PlanExecutor) {
	t.executor = executor
}

func (t *PlanModeTool) Name() string        { return "plan_mode" }
func (t *PlanModeTool) Description() string { return "Create, execute, monitor, or cancel structured plans." }

func (t *PlanModeTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"action": {
					Type:        genai.TypeString,
					Description: "Action to perform: create, execute, status, cancel",
					Enum:        []string{"create", "execute", "status", "cancel"},
				},
				"plan_id": {
					Type:        genai.TypeString,
					Description: "The plan ID (required for execute, status, cancel)",
				},
				"description": {
					Type:        genai.TypeString,
					Description: "Plan description (required for create)",
				},
				"steps": {
					Type:        genai.TypeString,
					Description: "Newline-separated list of plan steps (required for create)",
				},
			},
			Required: []string{"action"},
		},
	}
}

func (t *PlanModeTool) Validate(args map[string]any) error {
	action, ok := sdk.GetString(args, "action")
	if !ok || action == "" {
		return &sdk.ValidationError{Field: "action", Message: "action is required"}
	}

	switch action {
	case "create":
		desc, _ := sdk.GetString(args, "description")
		if desc == "" {
			return &sdk.ValidationError{Field: "description", Message: "description is required for create"}
		}
		steps, _ := sdk.GetString(args, "steps")
		if steps == "" {
			return &sdk.ValidationError{Field: "steps", Message: "steps are required for create"}
		}
	case "execute", "status", "cancel":
		planID, _ := sdk.GetString(args, "plan_id")
		if planID == "" {
			return &sdk.ValidationError{Field: "plan_id", Message: fmt.Sprintf("plan_id is required for %s", action)}
		}
	default:
		return &sdk.ValidationError{Field: "action", Message: fmt.Sprintf("unknown action: %s", action)}
	}
	return nil
}

func (t *PlanModeTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	if t.executor == nil {
		return sdk.NewErrorResult("plan_mode: no executor configured"), nil
	}

	action, _ := sdk.GetString(args, "action")

	switch action {
	case "create":
		return t.executeCreate(ctx, args)
	case "execute":
		return t.executeExecute(ctx, args)
	case "status":
		return t.executeStatus(args)
	case "cancel":
		return t.executeCancel(args)
	default:
		return sdk.NewErrorResult(fmt.Sprintf("unknown action: %s", action)), nil
	}
}

func (t *PlanModeTool) executeCreate(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	description, _ := sdk.GetString(args, "description")
	stepsStr, _ := sdk.GetString(args, "steps")

	var steps []string
	for _, step := range strings.Split(stepsStr, "\n") {
		if trimmed := strings.TrimSpace(step); trimmed != "" {
			steps = append(steps, trimmed)
		}
	}

	if len(steps) == 0 {
		return sdk.NewErrorResult("at least one step is required"), nil
	}

	planID, err := t.executor.Create(ctx, description, steps)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("failed to create plan: %s", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Plan created: %s\n\n", planID))
	sb.WriteString(fmt.Sprintf("Description: %s\n\n", description))
	sb.WriteString("Steps:\n")
	for i, step := range steps {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, step))
	}
	sb.WriteString(fmt.Sprintf("\nUse plan_mode with action=execute and plan_id=%s to start execution.", planID))

	return &sdk.ToolResult{
		Content: sb.String(),
		Data:    map[string]string{"plan_id": planID},
		Success: true,
	}, nil
}

func (t *PlanModeTool) executeExecute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	planID, _ := sdk.GetString(args, "plan_id")

	if err := t.executor.Execute(ctx, planID); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("plan execution failed: %s", err)), nil
	}

	status, err := t.executor.GetStatus(planID)
	if err != nil {
		return sdk.NewSuccessResult(fmt.Sprintf("Plan %s execution completed.", planID)), nil
	}

	return &sdk.ToolResult{
		Content: fmt.Sprintf("Plan %s execution completed. %d/%d steps succeeded.",
			planID, status.CompletedSteps, status.TotalSteps),
		Data:    status,
		Success: status.Status == "completed",
	}, nil
}

func (t *PlanModeTool) executeStatus(args map[string]any) (*sdk.ToolResult, error) {
	planID, _ := sdk.GetString(args, "plan_id")

	status, err := t.executor.GetStatus(planID)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("failed to get plan status: %s", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Plan %s: %s\n", planID, status.Status))
	sb.WriteString(fmt.Sprintf("Progress: %d/%d steps completed\n", status.CompletedSteps, status.TotalSteps))
	if status.CurrentStep != "" {
		sb.WriteString(fmt.Sprintf("Current step: %s\n", status.CurrentStep))
	}
	if status.Error != "" {
		sb.WriteString(fmt.Sprintf("Error: %s\n", status.Error))
	}

	return &sdk.ToolResult{
		Content: sb.String(),
		Data:    status,
		Success: true,
	}, nil
}

func (t *PlanModeTool) executeCancel(args map[string]any) (*sdk.ToolResult, error) {
	planID, _ := sdk.GetString(args, "plan_id")

	if err := t.executor.Cancel(planID); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("failed to cancel plan: %s", err)), nil
	}

	return sdk.NewSuccessResult(fmt.Sprintf("Plan %s cancelled.", planID)), nil
}
