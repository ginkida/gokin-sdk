package tools

import (
	"context"
	"fmt"
	"os"
	"strconv"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// MkdirTool creates directories.
type MkdirTool struct {
	workDir       string
	pathValidator *sdk.PathValidator
}

// NewMkdir creates a new MkdirTool instance.
func NewMkdir(workDir string) *MkdirTool {
	return &MkdirTool{
		workDir:       workDir,
		pathValidator: sdk.NewPathValidator([]string{workDir}),
	}
}

// SetAllowedDirs sets additional allowed directories for path validation.
func (t *MkdirTool) SetAllowedDirs(dirs []string) {
	allDirs := append([]string{t.workDir}, dirs...)
	t.pathValidator = sdk.NewPathValidator(allDirs)
}

func (t *MkdirTool) Name() string {
	return "mkdir"
}

func (t *MkdirTool) Description() string {
	return "Creates a directory. By default creates parent directories as needed (like mkdir -p)."
}

func (t *MkdirTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"path": {
					Type:        genai.TypeString,
					Description: "The path of the directory to create",
				},
				"parents": {
					Type:        genai.TypeBoolean,
					Description: "If true (default), create parent directories as needed",
				},
				"mode": {
					Type:        genai.TypeString,
					Description: "Directory permissions in octal format (default: 0755)",
				},
			},
			Required: []string{"path"},
		},
	}
}

func (t *MkdirTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	path, ok := sdk.GetString(args, "path")
	if !ok || path == "" {
		return sdk.NewErrorResult("path is required"), nil
	}
	parents := sdk.GetBoolDefault(args, "parents", true)
	modeStr := sdk.GetStringDefault(args, "mode", "0755")

	// Validate path
	if t.pathValidator != nil {
		validPath, err := t.pathValidator.Validate(path)
		if err != nil {
			return sdk.NewErrorResult(fmt.Sprintf("path validation failed: %s", err)), nil
		}
		path = validPath
	}

	// Parse mode
	modeVal, err := strconv.ParseUint(modeStr, 8, 32)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("invalid mode: %s", err)), nil
	}
	mode := os.FileMode(modeVal)

	// Check if path already exists
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return sdk.NewSuccessResult(fmt.Sprintf("Directory already exists: %s", path)), nil
		}
		return sdk.NewErrorResult(fmt.Sprintf("path exists but is not a directory: %s", path)), nil
	}

	// Create directory
	if parents {
		err = os.MkdirAll(path, mode)
	} else {
		err = os.Mkdir(path, mode)
	}

	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("failed to create directory: %s", err)), nil
	}

	return sdk.NewSuccessResult(fmt.Sprintf("Created directory: %s", path)), nil
}
