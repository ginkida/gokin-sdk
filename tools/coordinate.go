package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// CoordinateTask represents a single task within a coordinated execution.
type CoordinateTask struct {
	ID        string   `json:"id"`
	Prompt    string   `json:"prompt"`
	AgentType string   `json:"agent_type"`
	Priority  int      `json:"priority"`
	DependsOn []string `json:"depends_on"`
}

// TaskCoordinator coordinates multi-task execution with dependencies.
type TaskCoordinator interface {
	AddTask(task CoordinateTask) error
	RunAll(ctx context.Context) (map[string]*sdk.AgentResult, error)
}

// CoordinateTool orchestrates multiple agent tasks with dependency management.
type CoordinateTool struct {
	coordinator TaskCoordinator
}

// NewCoordinate creates a new CoordinateTool.
func NewCoordinate() *CoordinateTool {
	return &CoordinateTool{}
}

// SetCoordinator sets the task coordinator.
func (t *CoordinateTool) SetCoordinator(coordinator TaskCoordinator) {
	t.coordinator = coordinator
}

func (t *CoordinateTool) Name() string { return "coordinate" }
func (t *CoordinateTool) Description() string {
	return "Orchestrate multiple agent tasks with dependency management. Tasks run in parallel where possible."
}

func (t *CoordinateTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"tasks": {
					Type:        genai.TypeString,
					Description: `JSON array of tasks. Each task: {"id":"unique_id", "prompt":"task description", "agent_type":"general|explore|bash|plan", "priority":1, "depends_on":["other_task_id"]}`,
				},
			},
			Required: []string{"tasks"},
		},
	}
}

func (t *CoordinateTool) Validate(args map[string]any) error {
	tasksJSON, ok := sdk.GetString(args, "tasks")
	if !ok || tasksJSON == "" {
		return &sdk.ValidationError{Field: "tasks", Message: "tasks JSON is required"}
	}

	var tasks []CoordinateTask
	if err := json.Unmarshal([]byte(tasksJSON), &tasks); err != nil {
		return &sdk.ValidationError{Field: "tasks", Message: fmt.Sprintf("invalid JSON: %s", err)}
	}

	if len(tasks) == 0 {
		return &sdk.ValidationError{Field: "tasks", Message: "at least one task is required"}
	}

	// Validate no duplicate IDs
	ids := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		if task.ID == "" {
			return &sdk.ValidationError{Field: "tasks", Message: "each task must have an id"}
		}
		if task.Prompt == "" {
			return &sdk.ValidationError{Field: "tasks", Message: fmt.Sprintf("task %s must have a prompt", task.ID)}
		}
		if ids[task.ID] {
			return &sdk.ValidationError{Field: "tasks", Message: fmt.Sprintf("duplicate task id: %s", task.ID)}
		}
		ids[task.ID] = true
	}

	// Validate dependencies exist and no cycles
	for _, task := range tasks {
		for _, dep := range task.DependsOn {
			if !ids[dep] {
				return &sdk.ValidationError{Field: "tasks", Message: fmt.Sprintf("task %s depends on unknown task %s", task.ID, dep)}
			}
		}
	}

	if hasCycle(tasks) {
		return &sdk.ValidationError{Field: "tasks", Message: "circular dependency detected"}
	}

	return nil
}

func (t *CoordinateTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	if t.coordinator == nil {
		return sdk.NewErrorResult("coordinate: no coordinator configured"), nil
	}

	tasksJSON, ok := sdk.GetString(args, "tasks")
	if !ok {
		return sdk.NewErrorResult("tasks is required"), nil
	}

	var tasks []CoordinateTask
	if err := json.Unmarshal([]byte(tasksJSON), &tasks); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("invalid tasks JSON: %s", err)), nil
	}

	// Add all tasks
	for _, task := range tasks {
		if err := t.coordinator.AddTask(task); err != nil {
			return sdk.NewErrorResult(fmt.Sprintf("failed to add task %s: %s", task.ID, err)), nil
		}
	}

	// Run all tasks
	results, err := t.coordinator.RunAll(ctx)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("coordination failed: %s", err)), nil
	}

	// Format results
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Coordinated execution complete (%d tasks):\n\n", len(results)))

	succeeded := 0
	for id, result := range results {
		if result.Error == nil {
			succeeded++
			sb.WriteString(fmt.Sprintf("## Task %s (success, %d turns)\n%s\n\n", id, result.Turns, result.Text))
		} else {
			sb.WriteString(fmt.Sprintf("## Task %s (failed)\nError: %s\n\n", id, result.Error))
		}
	}

	sb.WriteString(fmt.Sprintf("Summary: %d/%d tasks succeeded", succeeded, len(results)))

	return &sdk.ToolResult{
		Content: sb.String(),
		Success: succeeded == len(results),
		Data:    map[string]int{"succeeded": succeeded, "total": len(results)},
	}, nil
}

// hasCycle detects circular dependencies using DFS.
func hasCycle(tasks []CoordinateTask) bool {
	graph := make(map[string][]string, len(tasks))
	for _, task := range tasks {
		graph[task.ID] = task.DependsOn
	}

	const (
		white = 0 // unvisited
		gray  = 1 // in progress
		black = 2 // done
	)

	colors := make(map[string]int, len(tasks))

	var dfs func(node string) bool
	dfs = func(node string) bool {
		colors[node] = gray
		for _, dep := range graph[node] {
			if colors[dep] == gray {
				return true // back edge = cycle
			}
			if colors[dep] == white {
				if dfs(dep) {
					return true
				}
			}
		}
		colors[node] = black
		return false
	}

	for _, task := range tasks {
		if colors[task.ID] == white {
			if dfs(task.ID) {
				return true
			}
		}
	}
	return false
}
