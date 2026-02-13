package tools

import (
	"context"
	"fmt"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// AgentRunner spawns and manages sub-agents.
type AgentRunner interface {
	Spawn(ctx context.Context, task sdk.AgentTask) (string, *sdk.AgentResult, error)
	SpawnAsync(ctx context.Context, task sdk.AgentTask) (string, error)
	GetResult(agentID string) (*sdk.AgentResult, bool)
}

// TaskTool spawns a sub-agent to handle a task.
type TaskTool struct {
	runner AgentRunner
}

// NewTask creates a new TaskTool.
func NewTask() *TaskTool {
	return &TaskTool{}
}

// SetRunner sets the agent runner.
func (t *TaskTool) SetRunner(runner AgentRunner) {
	t.runner = runner
}

func (t *TaskTool) Name() string        { return "task" }
func (t *TaskTool) Description() string { return "Launch a sub-agent to handle a complex task autonomously." }

func (t *TaskTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"prompt": {
					Type:        genai.TypeString,
					Description: "The task instruction for the sub-agent",
				},
				"subagent_type": {
					Type:        genai.TypeString,
					Description: "Type of agent to spawn: general, explore, bash, plan",
					Enum:        []string{"general", "explore", "bash", "plan"},
				},
				"run_in_background": {
					Type:        genai.TypeBoolean,
					Description: "If true, spawn asynchronously and return the agent ID immediately",
				},
				"max_turns": {
					Type:        genai.TypeInteger,
					Description: "Maximum number of turns for the sub-agent (default: 30)",
				},
				"description": {
					Type:        genai.TypeString,
					Description: "Short description of the task (3-5 words)",
				},
			},
			Required: []string{"prompt", "subagent_type"},
		},
	}
}

func (t *TaskTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	if t.runner == nil {
		return sdk.NewErrorResult("task: no agent runner configured"), nil
	}

	prompt, ok := sdk.GetString(args, "prompt")
	if !ok || prompt == "" {
		return sdk.NewErrorResult("prompt is required"), nil
	}

	agentTypeStr := sdk.GetStringDefault(args, "subagent_type", "general")
	background := sdk.GetBoolDefault(args, "run_in_background", false)
	maxTurns := sdk.GetIntDefault(args, "max_turns", 0)
	description := sdk.GetStringDefault(args, "description", "sub-task")

	task := sdk.AgentTask{
		Prompt:      prompt,
		Type:        sdk.ParseAgentType(agentTypeStr),
		Background:  background,
		Description: description,
		MaxTurns:    maxTurns,
	}

	if background {
		agentID, err := t.runner.SpawnAsync(ctx, task)
		if err != nil {
			return sdk.NewErrorResult(fmt.Sprintf("failed to spawn async agent: %s", err)), nil
		}
		return &sdk.ToolResult{
			Content: fmt.Sprintf("Agent spawned in background with ID: %s\nUse task_output to check results.", agentID),
			Data:    map[string]string{"agent_id": agentID},
			Success: true,
		}, nil
	}

	agentID, result, err := t.runner.Spawn(ctx, task)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("agent failed: %s", err)), nil
	}

	content := fmt.Sprintf("Agent %s completed in %d turns (%s).\n\n%s",
		agentID, result.Turns, result.Duration, result.Text)
	if result.Error != nil {
		content += fmt.Sprintf("\n\nError: %s", result.Error)
	}

	return &sdk.ToolResult{
		Content: content,
		Data:    map[string]string{"agent_id": agentID},
		Success: result.Error == nil,
	}, nil
}
