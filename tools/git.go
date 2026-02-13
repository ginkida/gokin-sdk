package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// GitTool provides git operations as a single multi-command tool.
type GitTool struct {
	workDir string
}

// NewGit creates a new GitTool with the given working directory.
func NewGit(workDir string) *GitTool {
	return &GitTool{workDir: workDir}
}

func (t *GitTool) Name() string { return "git" }

func (t *GitTool) Description() string {
	return "Executes git operations. Supports commands: status, diff, add, commit, log, blame, branch."
}

func (t *GitTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"command": {
					Type:        genai.TypeString,
					Description: "Git command: status, diff, add, commit, log, blame, branch",
				},
				"args": {
					Type:        genai.TypeString,
					Description: "Additional arguments for the command (e.g., file paths, flags)",
				},
				"message": {
					Type:        genai.TypeString,
					Description: "Commit message (for commit command)",
				},
				"file": {
					Type:        genai.TypeString,
					Description: "File path (for diff, blame, log)",
				},
				"all": {
					Type:        genai.TypeBoolean,
					Description: "Stage all changes (for add/commit: -a flag)",
				},
				"count": {
					Type:        genai.TypeInteger,
					Description: "Number of entries (for log, default: 10)",
				},
				"staged": {
					Type:        genai.TypeBoolean,
					Description: "Show staged changes (for diff: --cached)",
				},
			},
			Required: []string{"command"},
		},
	}
}

func (t *GitTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	command, ok := sdk.GetString(args, "command")
	if !ok || command == "" {
		return sdk.NewErrorResult("command is required"), nil
	}

	switch command {
	case "status":
		return t.gitStatus(ctx, args)
	case "diff":
		return t.gitDiff(ctx, args)
	case "add":
		return t.gitAdd(ctx, args)
	case "commit":
		return t.gitCommit(ctx, args)
	case "log":
		return t.gitLog(ctx, args)
	case "blame":
		return t.gitBlame(ctx, args)
	case "branch":
		return t.gitBranch(ctx, args)
	default:
		return sdk.NewErrorResult(fmt.Sprintf("unknown git command: %s (supported: status, diff, add, commit, log, blame, branch)", command)), nil
	}
}

func (t *GitTool) gitStatus(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	gitArgs := []string{"status"}
	if extraArgs, _ := sdk.GetString(args, "args"); extraArgs != "" {
		gitArgs = append(gitArgs, strings.Fields(extraArgs)...)
	} else {
		gitArgs = append(gitArgs, "--short")
	}
	return t.runGit(ctx, gitArgs)
}

func (t *GitTool) gitDiff(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	gitArgs := []string{"diff"}

	staged := sdk.GetBoolDefault(args, "staged", false)
	if staged {
		gitArgs = append(gitArgs, "--cached")
	}

	if extraArgs, _ := sdk.GetString(args, "args"); extraArgs != "" {
		gitArgs = append(gitArgs, strings.Fields(extraArgs)...)
	}

	if file, _ := sdk.GetString(args, "file"); file != "" {
		relPath, err := filepath.Rel(t.workDir, file)
		if err == nil {
			file = relPath
		}
		gitArgs = append(gitArgs, "--", file)
	}

	result, err := t.runGit(ctx, gitArgs)
	if err != nil {
		return result, err
	}

	// Truncate large diffs
	if result.Success && len(result.Content) > 50000 {
		result = sdk.NewSuccessResult(result.Content[:50000] + "\n... (diff truncated)")
	}

	return result, nil
}

func (t *GitTool) gitAdd(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	gitArgs := []string{"add"}

	all := sdk.GetBoolDefault(args, "all", false)
	if all {
		gitArgs = append(gitArgs, "-A")
	} else if extraArgs, _ := sdk.GetString(args, "args"); extraArgs != "" {
		gitArgs = append(gitArgs, strings.Fields(extraArgs)...)
	} else if file, _ := sdk.GetString(args, "file"); file != "" {
		gitArgs = append(gitArgs, file)
	} else {
		return sdk.NewErrorResult("specify files via args/file or use all=true"), nil
	}

	result, err := t.runGit(ctx, gitArgs)
	if err != nil {
		return result, err
	}

	// Show status after adding
	statusResult, _ := t.runGit(ctx, []string{"status", "--short"})
	if statusResult != nil && statusResult.Success {
		return sdk.NewSuccessResult("Files staged.\n" + statusResult.Content), nil
	}

	return result, nil
}

func (t *GitTool) gitCommit(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	message, _ := sdk.GetString(args, "message")
	if message == "" {
		return sdk.NewErrorResult("message is required for commit"), nil
	}

	gitArgs := []string{"commit"}

	all := sdk.GetBoolDefault(args, "all", false)
	if all {
		gitArgs = append(gitArgs, "-a")
	}

	gitArgs = append(gitArgs, "-m", message)

	result, err := t.runGit(ctx, gitArgs)
	if err != nil {
		return result, err
	}

	// Get short hash
	hashResult, _ := t.runGit(ctx, []string{"rev-parse", "--short", "HEAD"})
	if hashResult != nil && hashResult.Success {
		hash := strings.TrimSpace(hashResult.Content)
		return sdk.NewSuccessResult(fmt.Sprintf("Committed %s: %s", hash, message)), nil
	}

	return result, nil
}

func (t *GitTool) gitLog(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	count := sdk.GetIntDefault(args, "count", 10)
	if count < 1 {
		count = 1
	}
	if count > 100 {
		count = 100
	}

	gitArgs := []string{"log", fmt.Sprintf("-n%d", count), "--oneline"}

	if extraArgs, _ := sdk.GetString(args, "args"); extraArgs != "" {
		gitArgs = append(gitArgs, strings.Fields(extraArgs)...)
	}

	if file, _ := sdk.GetString(args, "file"); file != "" {
		relPath, err := filepath.Rel(t.workDir, file)
		if err == nil {
			file = relPath
		}
		gitArgs = append(gitArgs, "--follow", "--", file)
	}

	return t.runGit(ctx, gitArgs)
}

func (t *GitTool) gitBlame(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	file, _ := sdk.GetString(args, "file")
	if file == "" {
		return sdk.NewErrorResult("file is required for blame"), nil
	}

	relPath, err := filepath.Rel(t.workDir, file)
	if err == nil {
		file = relPath
	}

	gitArgs := []string{"blame"}

	if extraArgs, _ := sdk.GetString(args, "args"); extraArgs != "" {
		gitArgs = append(gitArgs, strings.Fields(extraArgs)...)
	}

	gitArgs = append(gitArgs, "--", file)

	return t.runGit(ctx, gitArgs)
}

func (t *GitTool) gitBranch(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	gitArgs := []string{"branch"}

	if extraArgs, _ := sdk.GetString(args, "args"); extraArgs != "" {
		gitArgs = append(gitArgs, strings.Fields(extraArgs)...)
	} else {
		gitArgs = append(gitArgs, "-v")
	}

	return t.runGit(ctx, gitArgs)
}

func (t *GitTool) runGit(ctx context.Context, args []string) (*sdk.ToolResult, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = t.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		output := stdout.String()
		if stderr.Len() > 0 {
			if output != "" {
				output += "\n"
			}
			output += stderr.String()
		}
		if output == "" {
			output = err.Error()
		}
		return sdk.NewErrorResult(output), nil
	}

	output := stdout.String()
	if output == "" {
		output = "(no output)"
	}
	return sdk.NewSuccessResult(output), nil
}
