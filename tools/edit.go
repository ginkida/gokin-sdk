package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// EditTool performs string replacement editing on files.
type EditTool struct{}

// NewEdit creates a new EditTool.
func NewEdit() *EditTool {
	return &EditTool{}
}

func (t *EditTool) Name() string { return "edit" }

func (t *EditTool) Description() string {
	return "Performs exact string replacement in files. Replaces old_string with new_string."
}

func (t *EditTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"file_path": {
					Type:        genai.TypeString,
					Description: "The absolute path to the file to edit",
				},
				"old_string": {
					Type:        genai.TypeString,
					Description: "The exact text to find and replace",
				},
				"new_string": {
					Type:        genai.TypeString,
					Description: "The replacement text",
				},
				"replace_all": {
					Type:        genai.TypeBoolean,
					Description: "Replace all occurrences (default: false, replaces first only)",
				},
			},
			Required: []string{"file_path", "old_string", "new_string"},
		},
	}
}

func (t *EditTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	filePath, ok := sdk.GetString(args, "file_path")
	if !ok || filePath == "" {
		return sdk.NewErrorResult("file_path is required"), nil
	}

	oldString, ok := sdk.GetString(args, "old_string")
	if !ok {
		return sdk.NewErrorResult("old_string is required"), nil
	}

	newString, ok := sdk.GetString(args, "new_string")
	if !ok {
		return sdk.NewErrorResult("new_string is required"), nil
	}

	if oldString == newString {
		return sdk.NewErrorResult("old_string and new_string must be different"), nil
	}

	replaceAll := sdk.GetBoolDefault(args, "replace_all", false)

	// Read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return sdk.NewErrorResult(fmt.Sprintf("file not found: %s", filePath)), nil
		}
		return sdk.NewErrorResult(fmt.Sprintf("error reading file: %s", err)), nil
	}

	content := string(data)

	// Check that old_string exists
	if !strings.Contains(content, oldString) {
		return sdk.NewErrorResult(fmt.Sprintf("old_string not found in %s", filePath)), nil
	}

	// Check for uniqueness when not replacing all
	if !replaceAll {
		count := strings.Count(content, oldString)
		if count > 1 {
			return sdk.NewErrorResult(fmt.Sprintf(
				"old_string matches %d times in %s. Provide more context to make it unique, or use replace_all=true",
				count, filePath,
			)), nil
		}
	}

	// Perform replacement
	var newContent string
	var replacements int
	if replaceAll {
		replacements = strings.Count(content, oldString)
		newContent = strings.ReplaceAll(content, oldString, newString)
	} else {
		replacements = 1
		newContent = strings.Replace(content, oldString, newString, 1)
	}

	// Write back
	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error writing file: %s", err)), nil
	}

	return sdk.NewSuccessResult(fmt.Sprintf("Replaced %d occurrence(s) in %s", replacements, filePath)), nil
}
