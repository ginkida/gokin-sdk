// Package tools provides built-in tool implementations for the SDK.
package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// BashTool executes bash commands.
type BashTool struct {
	workDir string
	timeout time.Duration
}

// NewBash creates a new BashTool with the given working directory.
func NewBash(workDir string) *BashTool {
	return &BashTool{
		workDir: workDir,
		timeout: 30 * time.Second,
	}
}

// NewBashWithTimeout creates a new BashTool with a custom timeout.
func NewBashWithTimeout(workDir string, timeout time.Duration) *BashTool {
	return &BashTool{
		workDir: workDir,
		timeout: timeout,
	}
}

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Description() string {
	return "Executes a bash command and returns the output. Use for system operations, running tests, builds, etc."
}

func (t *BashTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"command": {
					Type:        genai.TypeString,
					Description: "The bash command to execute",
				},
			},
			Required: []string{"command"},
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	command, ok := sdk.GetString(args, "command")
	if !ok || command == "" {
		return sdk.NewErrorResult("command is required"), nil
	}

	execCtx := ctx
	if t.timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(execCtx, "bash", "-c", command)
	cmd.Dir = t.workDir

	// Use safe environment
	cmd.Env = buildSafeEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if execCtx.Err() == context.DeadlineExceeded {
		return sdk.NewErrorResult(fmt.Sprintf("command timed out after %v", t.timeout)), nil
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			output := stdout.String()
			if stderr.Len() > 0 {
				output += "\nSTDERR:\n" + stderr.String()
			}
			return &sdk.ToolResult{
				Content: output,
				Error:   fmt.Sprintf("command exited with code %d", exitErr.ExitCode()),
				Success: false,
			}, nil
		}
		return sdk.NewErrorResult(fmt.Sprintf("command failed: %s", err)), nil
	}

	var output strings.Builder
	if stdout.Len() > 0 {
		output.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString("STDERR:\n")
		output.WriteString(stderr.String())
	}

	result := output.String()
	const maxLen = 30000
	if len(result) > maxLen {
		result = result[:maxLen] + fmt.Sprintf("\n... (output truncated: showing %d of %d characters)", maxLen, len(output.String()))
	}

	if result == "" {
		result = "(no output)"
	}

	return sdk.NewSuccessResult(result), nil
}

// buildSafeEnv creates a sanitized environment for command execution.
func buildSafeEnv() []string {
	safeVars := []string{
		"PATH", "HOME", "USER", "SHELL", "TERM", "LANG", "LC_ALL", "LC_CTYPE",
		"TMPDIR", "EDITOR", "GOPATH", "GOROOT", "GOPROXY", "GOPRIVATE",
		"NODE_PATH", "PYTHONPATH", "VIRTUAL_ENV",
	}

	env := make([]string, 0, len(safeVars))
	for _, key := range safeVars {
		if val := os.Getenv(key); val != "" {
			env = append(env, key+"="+val)
		}
	}

	// Ensure PATH is always set
	hasPath := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
			break
		}
	}
	if !hasPath {
		env = append(env, "PATH=/usr/local/bin:/usr/bin:/bin")
	}

	return env
}
