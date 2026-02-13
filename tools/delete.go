package tools

import (
	"context"
	"fmt"
	"os"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// DeleteTool deletes files or directories.
type DeleteTool struct{}

// NewDelete creates a new DeleteTool.
func NewDelete() *DeleteTool {
	return &DeleteTool{}
}

func (t *DeleteTool) Name() string { return "delete" }

func (t *DeleteTool) Description() string {
	return "Deletes a file or directory. Requires recursive=true for non-empty directories."
}

func (t *DeleteTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"path": {
					Type:        genai.TypeString,
					Description: "Path to the file or directory to delete",
				},
				"recursive": {
					Type:        genai.TypeBoolean,
					Description: "Required for non-empty directories (default: false)",
				},
			},
			Required: []string{"path"},
		},
	}
}

func (t *DeleteTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	path, ok := sdk.GetString(args, "path")
	if !ok || path == "" {
		return sdk.NewErrorResult("path is required"), nil
	}

	recursive := sdk.GetBoolDefault(args, "recursive", false)

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sdk.NewErrorResult(fmt.Sprintf("path not found: %s", path)), nil
		}
		return sdk.NewErrorResult(fmt.Sprintf("error accessing path: %s", err)), nil
	}

	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return sdk.NewErrorResult(fmt.Sprintf("error reading directory: %s", err)), nil
		}

		if len(entries) > 0 && !recursive {
			return sdk.NewErrorResult(fmt.Sprintf("directory %s is not empty — set recursive=true to delete", path)), nil
		}

		if err := os.RemoveAll(path); err != nil {
			return sdk.NewErrorResult(fmt.Sprintf("error deleting directory: %s", err)), nil
		}
		return sdk.NewSuccessResult(fmt.Sprintf("Deleted directory: %s", path)), nil
	}

	if err := os.Remove(path); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error deleting file: %s", err)), nil
	}
	return sdk.NewSuccessResult(fmt.Sprintf("Deleted file: %s", path)), nil
}
