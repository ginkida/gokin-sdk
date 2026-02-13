package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// CopyTool copies files or directories.
type CopyTool struct{}

// NewCopy creates a new CopyTool.
func NewCopy() *CopyTool {
	return &CopyTool{}
}

func (t *CopyTool) Name() string { return "copy" }

func (t *CopyTool) Description() string {
	return "Copies a file or directory to a new location."
}

func (t *CopyTool) Declaration() *genai.FunctionDeclaration {
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

func (t *CopyTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
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

	srcInfo, err := os.Lstat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return sdk.NewErrorResult(fmt.Sprintf("source not found: %s", source)), nil
		}
		return sdk.NewErrorResult(fmt.Sprintf("error accessing source: %s", err)), nil
	}

	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return sdk.NewErrorResult("cannot copy symlinks"), nil
	}

	if srcInfo.IsDir() {
		count, err := copyDir(source, dest, 0)
		if err != nil {
			return sdk.NewErrorResult(fmt.Sprintf("error copying directory: %s", err)), nil
		}
		return sdk.NewSuccessResult(fmt.Sprintf("Copied directory: %s → %s (%d files)", source, dest, count)), nil
	}

	if err := copyFile(source, dest, srcInfo.Mode()); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error copying file: %s", err)), nil
	}
	return sdk.NewSuccessResult(fmt.Sprintf("Copied file: %s → %s", source, dest)), nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

const maxCopyDepth = 50

func copyDir(src, dst string, depth int) (int, error) {
	if depth > maxCopyDepth {
		return 0, fmt.Errorf("maximum recursion depth exceeded")
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return 0, err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		// Skip symlinks
		info, err := os.Lstat(srcPath)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		if entry.IsDir() {
			n, err := copyDir(srcPath, dstPath, depth+1)
			if err != nil {
				return count, err
			}
			count += n
		} else {
			if err := copyFile(srcPath, dstPath, info.Mode()); err != nil {
				return count, err
			}
			count++
		}
	}

	return count, nil
}
