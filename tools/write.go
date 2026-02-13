package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// WriteTool writes content to files.
type WriteTool struct{}

// NewWrite creates a new WriteTool.
func NewWrite() *WriteTool {
	return &WriteTool{}
}

func (t *WriteTool) Name() string { return "write" }

func (t *WriteTool) Description() string {
	return "Writes content to a file. Creates the file if it doesn't exist, or overwrites if it does."
}

func (t *WriteTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"file_path": {
					Type:        genai.TypeString,
					Description: "The path to the file to write",
				},
				"content": {
					Type:        genai.TypeString,
					Description: "The content to write to the file",
				},
			},
			Required: []string{"file_path", "content"},
		},
	}
}

func (t *WriteTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	filePath, ok := sdk.GetString(args, "file_path")
	if !ok || filePath == "" {
		return sdk.NewErrorResult("file_path is required"), nil
	}

	content, ok := sdk.GetString(args, "content")
	if !ok {
		return sdk.NewErrorResult("content is required"), nil
	}

	// Create parent directories
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error creating directories: %s", err)), nil
	}

	// Check if file exists
	_, existErr := os.Stat(filePath)
	isNew := os.IsNotExist(existErr)

	// Write file
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error writing file: %s", err)), nil
	}

	if isNew {
		return sdk.NewSuccessResult(fmt.Sprintf("Created new file: %s (%d bytes)", filePath, len(content))), nil
	}
	return sdk.NewSuccessResult(fmt.Sprintf("Updated file: %s (%d bytes)", filePath, len(content))), nil
}
