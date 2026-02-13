package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// MoveTool moves or renames files and directories.
type MoveTool struct{}

// NewMove creates a new MoveTool.
func NewMove() *MoveTool {
	return &MoveTool{}
}

func (t *MoveTool) Name() string { return "move" }

func (t *MoveTool) Description() string {
	return "Moves or renames a file or directory."
}

func (t *MoveTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"source": {
					Type:        genai.TypeString,
					Description: "Source file or directory path",
				},
				"destination": {
					Type:        genai.TypeString,
					Description: "Destination path",
				},
			},
			Required: []string{"source", "destination"},
		},
	}
}

func (t *MoveTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	source, ok := sdk.GetString(args, "source")
	if !ok || source == "" {
		return sdk.NewErrorResult("source is required"), nil
	}

	dest, ok := sdk.GetString(args, "destination")
	if !ok || dest == "" {
		return sdk.NewErrorResult("destination is required"), nil
	}

	if source == dest {
		return sdk.NewErrorResult("source and destination are the same"), nil
	}

	// Check source exists
	srcInfo, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return sdk.NewErrorResult(fmt.Sprintf("source not found: %s", source)), nil
		}
		return sdk.NewErrorResult(fmt.Sprintf("error accessing source: %s", err)), nil
	}

	// Check destination doesn't exist
	if _, err := os.Stat(dest); err == nil {
		return sdk.NewErrorResult(fmt.Sprintf("destination already exists: %s", dest)), nil
	}

	// Create destination directory if needed
	destDir := filepath.Dir(dest)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error creating destination directory: %s", err)), nil
	}

	if err := os.Rename(source, dest); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error moving: %s", err)), nil
	}

	kind := "file"
	if srcInfo.IsDir() {
		kind = "directory"
	}
	return sdk.NewSuccessResult(fmt.Sprintf("Moved %s: %s → %s", kind, source, dest)), nil
}
