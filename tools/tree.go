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

// TreeTool displays a recursive directory tree.
type TreeTool struct {
	workDir string
}

// NewTree creates a new TreeTool.
func NewTree(workDir string) *TreeTool {
	return &TreeTool{workDir: workDir}
}

func (t *TreeTool) Name() string { return "tree" }

func (t *TreeTool) Description() string {
	return "Displays a recursive directory tree with depth limiting and pattern filtering."
}

func (t *TreeTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"path": {
					Type:        genai.TypeString,
					Description: "Directory to display. Defaults to working directory.",
				},
				"depth": {
					Type:        genai.TypeInteger,
					Description: "Maximum recursion depth (default: 3)",
				},
				"pattern": {
					Type:        genai.TypeString,
					Description: "Glob filter for files (e.g., '*.go')",
				},
				"show_hidden": {
					Type:        genai.TypeBoolean,
					Description: "Include dotfiles (default: false)",
				},
				"dirs_only": {
					Type:        genai.TypeBoolean,
					Description: "Show only directories (default: false)",
				},
			},
		},
	}
}

func (t *TreeTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	rootPath := sdk.GetStringDefault(args, "path", t.workDir)
	if !filepath.IsAbs(rootPath) {
		rootPath = filepath.Join(t.workDir, rootPath)
	}

	maxDepth := sdk.GetIntDefault(args, "depth", 3)
	if maxDepth < 1 {
		maxDepth = 1
	}
	pattern := sdk.GetStringDefault(args, "pattern", "")
	showHidden := sdk.GetBoolDefault(args, "show_hidden", false)
	dirsOnly := sdk.GetBoolDefault(args, "dirs_only", false)

	info, err := os.Stat(rootPath)
	if err != nil {
		if os.IsNotExist(err) {
			return sdk.NewErrorResult(fmt.Sprintf("path not found: %s", rootPath)), nil
		}
		return sdk.NewErrorResult(fmt.Sprintf("error accessing path: %s", err)), nil
	}
	if !info.IsDir() {
		return sdk.NewErrorResult(fmt.Sprintf("not a directory: %s", rootPath)), nil
	}

	var builder strings.Builder
	builder.WriteString(rootPath + "\n")

	var dirCount, fileCount int
	buildTree(ctx, rootPath, "", maxDepth, 0, pattern, showHidden, dirsOnly, &builder, &dirCount, &fileCount)

	builder.WriteString(fmt.Sprintf("\n%d directories, %d files\n", dirCount, fileCount))

	return sdk.NewSuccessResult(builder.String()), nil
}

func buildTree(ctx context.Context, dir, prefix string, maxDepth, currentDepth int, pattern string, showHidden, dirsOnly bool, builder *strings.Builder, dirCount, fileCount *int) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	if currentDepth >= maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Sort: directories first, then alphabetical
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return entries[i].Name() < entries[j].Name()
	})

	// Filter entries
	var filtered []os.DirEntry
	for _, entry := range entries {
		name := entry.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if dirsOnly && !entry.IsDir() {
			continue
		}
		if pattern != "" && !entry.IsDir() {
			matched, _ := doublestar.Match(pattern, name)
			if !matched {
				continue
			}
		}
		filtered = append(filtered, entry)
	}

	for i, entry := range filtered {
		isLast := i == len(filtered)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}

		builder.WriteString(prefix + connector + entry.Name())
		if entry.IsDir() {
			builder.WriteString("/\n")
			*dirCount++
			buildTree(ctx, filepath.Join(dir, entry.Name()), childPrefix, maxDepth, currentDepth+1, pattern, showHidden, dirsOnly, builder, dirCount, fileCount)
		} else {
			builder.WriteString("\n")
			*fileCount++
		}
	}
}
