package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// ListDirTool lists directory contents.
type ListDirTool struct {
	workDir string
}

// NewListDir creates a new ListDirTool.
func NewListDir(workDir string) *ListDirTool {
	return &ListDirTool{workDir: workDir}
}

func (t *ListDirTool) Name() string { return "list_dir" }

func (t *ListDirTool) Description() string {
	return "Lists directory contents with file names. Appends '/' to directories."
}

func (t *ListDirTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"directory_path": {
					Type:        genai.TypeString,
					Description: "Directory to list. Defaults to working directory.",
				},
			},
		},
	}
}

func (t *ListDirTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	dirPath := sdk.GetStringDefault(args, "directory_path", t.workDir)
	if dirPath == "" {
		dirPath = t.workDir
	}
	if !filepath.IsAbs(dirPath) {
		dirPath = filepath.Join(t.workDir, dirPath)
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return sdk.NewErrorResult(fmt.Sprintf("directory not found: %s", dirPath)), nil
		}
		return sdk.NewErrorResult(fmt.Sprintf("error reading directory: %s", err)), nil
	}

	if len(entries) == 0 {
		return sdk.NewSuccessResult("(empty)"), nil
	}

	const maxEntries = 2000
	var builder strings.Builder
	count := 0

	for _, entry := range entries {
		if count >= maxEntries {
			builder.WriteString(fmt.Sprintf("... (output truncated: showing %d entries)\n", maxEntries))
			break
		}
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		builder.WriteString(name)
		builder.WriteString("\n")
		count++
	}

	return sdk.NewSuccessResult(builder.String()), nil
}
