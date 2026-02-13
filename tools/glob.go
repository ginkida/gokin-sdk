package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"github.com/bmatcuk/doublestar/v4"
	"google.golang.org/genai"
)

// GlobTool finds files matching a glob pattern.
type GlobTool struct {
	workDir string
}

// NewGlob creates a new GlobTool with the given working directory.
func NewGlob(workDir string) *GlobTool {
	return &GlobTool{workDir: workDir}
}

func (t *GlobTool) Name() string { return "glob" }

func (t *GlobTool) Description() string {
	return "Finds files matching a glob pattern. Returns file paths sorted by modification time (newest first)."
}

func (t *GlobTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"pattern": {
					Type:        genai.TypeString,
					Description: "The glob pattern to match (e.g., '**/*.go', 'src/**/*.ts')",
				},
				"path": {
					Type:        genai.TypeString,
					Description: "The directory to search in. Defaults to working directory.",
				},
			},
			Required: []string{"pattern"},
		},
	}
}

func (t *GlobTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	pattern, ok := sdk.GetString(args, "pattern")
	if !ok || pattern == "" {
		return sdk.NewErrorResult("pattern is required"), nil
	}

	searchPath := sdk.GetStringDefault(args, "path", t.workDir)
	if !filepath.IsAbs(searchPath) {
		searchPath = filepath.Join(t.workDir, searchPath)
	}

	if _, err := os.Stat(searchPath); err != nil {
		if os.IsNotExist(err) {
			return sdk.NewErrorResult(fmt.Sprintf("path not found: %s", searchPath)), nil
		}
		return sdk.NewErrorResult(fmt.Sprintf("error accessing path: %s", err)), nil
	}

	fullPattern := filepath.Join(searchPath, pattern)
	matches, err := doublestar.FilepathGlob(fullPattern)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("invalid pattern: %s", err)), nil
	}

	type fileInfo struct {
		path    string
		modTime int64
	}
	var files []fileInfo

	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, fileInfo{
			path:    match,
			modTime: info.ModTime().Unix(),
		})
	}

	// Sort by modification time (newest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime > files[j].modTime
	})

	const maxResults = 1000
	if len(files) > maxResults {
		files = files[:maxResults]
	}

	if len(files) == 0 {
		return sdk.NewSuccessResult("(no matches)"), nil
	}

	var builder strings.Builder
	for _, f := range files {
		relPath, err := filepath.Rel(t.workDir, f.path)
		if err != nil {
			relPath = f.path
		}
		builder.WriteString(relPath)
		builder.WriteString("\n")
	}

	return sdk.NewSuccessResult(builder.String()), nil
}
