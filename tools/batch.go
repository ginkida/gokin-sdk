package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sdk "github.com/ginkida/gokin-sdk"

	"github.com/bmatcuk/doublestar/v4"
	"google.golang.org/genai"
)

// BatchProgressCallback is called to report progress during batch operations.
type BatchProgressCallback func(processed, total int, currentFile string, success bool)

// BatchTool performs batch operations on multiple files.
type BatchTool struct {
	workDir          string
	progressCallback BatchProgressCallback
	failureThreshold float64 // Stop if failure rate exceeds this (0.0 to 1.0, 0 = disabled)
}

// NewBatch creates a new BatchTool instance.
func NewBatch(workDir string) *BatchTool {
	return &BatchTool{
		workDir: workDir,
	}
}

// SetProgressCallback sets the progress callback for real-time updates.
func (t *BatchTool) SetProgressCallback(callback BatchProgressCallback) {
	t.progressCallback = callback
}

// SetFailureThreshold sets the failure threshold (0.0 to 1.0).
// When the failure rate exceeds this threshold, the batch operation stops.
// Set to 0 to disable (default).
func (t *BatchTool) SetFailureThreshold(threshold float64) {
	if threshold < 0 {
		threshold = 0
	}
	if threshold > 1 {
		threshold = 1
	}
	t.failureThreshold = threshold
}

func (t *BatchTool) Name() string {
	return "batch"
}

func (t *BatchTool) Description() string {
	return "Performs batch operations on multiple files matching a pattern. Supports replace, rename, and delete operations."
}

func (t *BatchTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"operation": {
					Type:        genai.TypeString,
					Description: "The operation to perform: 'replace', 'rename', 'delete'",
					Enum:        []string{"replace", "rename", "delete"},
				},
				"pattern": {
					Type:        genai.TypeString,
					Description: "Glob pattern to match files (e.g., '**/*.go', 'src/*.ts')",
				},
				"files": {
					Type:        genai.TypeArray,
					Description: "Explicit list of file paths (alternative to pattern)",
					Items:       &genai.Schema{Type: genai.TypeString},
				},
				"search": {
					Type:        genai.TypeString,
					Description: "Text to search for (required for replace operation)",
				},
				"replacement": {
					Type:        genai.TypeString,
					Description: "Replacement text (required for replace operation)",
				},
				"rename_from": {
					Type:        genai.TypeString,
					Description: "Pattern to match in filenames for rename (e.g., '.old')",
				},
				"rename_to": {
					Type:        genai.TypeString,
					Description: "Replacement for filenames (e.g., '.new')",
				},
				"dry_run": {
					Type:        genai.TypeBoolean,
					Description: "Preview changes without applying them (default: false)",
				},
				"parallel": {
					Type:        genai.TypeBoolean,
					Description: "Execute operations in parallel (default: true)",
				},
			},
			Required: []string{"operation"},
		},
	}
}

func (t *BatchTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	op := sdk.GetStringDefault(args, "operation", "")
	if op == "" {
		return sdk.NewErrorResult("operation is required"), nil
	}
	pattern, _ := sdk.GetString(args, "pattern")
	dryRun := sdk.GetBoolDefault(args, "dry_run", false)
	parallel := sdk.GetBoolDefault(args, "parallel", true)

	// Collect target files
	var files []string

	if pattern != "" {
		matched, err := t.matchFiles(pattern)
		if err != nil {
			return sdk.NewErrorResult(fmt.Sprintf("pattern error: %s", err)), nil
		}
		files = matched
	}

	// Add explicit files
	if fileList, ok := args["files"].([]interface{}); ok {
		for _, f := range fileList {
			if path, ok := f.(string); ok {
				files = append(files, path)
			}
		}
	}

	if len(files) == 0 {
		return sdk.NewErrorResult("no files matched the pattern or list"), nil
	}

	// Execute operation
	var result batchResult
	switch op {
	case "replace":
		search, _ := sdk.GetString(args, "search")
		if search == "" {
			return sdk.NewErrorResult("search is required for replace operation"), nil
		}
		replacement := sdk.GetStringDefault(args, "replacement", "")
		result = t.executeReplace(ctx, files, search, replacement, dryRun, parallel)

	case "rename":
		from, _ := sdk.GetString(args, "rename_from")
		to, _ := sdk.GetString(args, "rename_to")
		if from == "" || to == "" {
			return sdk.NewErrorResult("rename_from and rename_to are required for rename operation"), nil
		}
		result = t.executeRename(ctx, files, from, to, dryRun)

	case "delete":
		result = t.executeDelete(ctx, files, dryRun)

	default:
		return sdk.NewErrorResult(fmt.Sprintf("unknown operation: %s", op)), nil
	}

	return t.formatResult(op, result, dryRun), nil
}

// batchResult holds the results of a batch operation.
type batchResult struct {
	succeeded   []string
	failed      map[string]string
	skipped     []string
	totalFiles  int
	description string
}

// matchFiles matches files using glob pattern.
func (t *BatchTool) matchFiles(pattern string) ([]string, error) {
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(t.workDir, pattern)
	}

	matches, err := doublestar.FilepathGlob(pattern)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			files = append(files, match)
		}
	}

	return files, nil
}

// executeReplace performs search/replace on multiple files.
func (t *BatchTool) executeReplace(ctx context.Context, files []string, search, replacement string, dryRun, parallel bool) batchResult {
	result := batchResult{
		totalFiles:  len(files),
		failed:      make(map[string]string),
		description: fmt.Sprintf("replace '%s' with '%s'", search, replacement),
	}

	if parallel && len(files) > 1 {
		result = t.executeParallel(ctx, files, func(path string) error {
			return t.replaceInFile(path, search, replacement, dryRun)
		})
		result.description = fmt.Sprintf("replace '%s' with '%s'", search, replacement)
	} else {
		for _, path := range files {
			select {
			case <-ctx.Done():
				result.failed[path] = "cancelled"
				continue
			default:
			}

			err := t.replaceInFile(path, search, replacement, dryRun)
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					result.skipped = append(result.skipped, path)
				} else {
					result.failed[path] = err.Error()
				}
			} else {
				result.succeeded = append(result.succeeded, path)
			}
		}
	}

	return result
}

func (t *BatchTool) replaceInFile(path, search, replacement string, dryRun bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	content := string(data)
	if !strings.Contains(content, search) {
		return fmt.Errorf("search string not found")
	}

	if dryRun {
		return nil
	}

	newContent := strings.ReplaceAll(content, search, replacement)
	return os.WriteFile(path, []byte(newContent), 0644)
}

// executeRename renames multiple files.
func (t *BatchTool) executeRename(ctx context.Context, files []string, from, to string, dryRun bool) batchResult {
	result := batchResult{
		totalFiles:  len(files),
		failed:      make(map[string]string),
		description: fmt.Sprintf("rename '%s' to '%s'", from, to),
	}

	// Security: validate rename_to doesn't contain path traversal sequences
	if strings.Contains(to, "..") || strings.Contains(to, "/") || strings.Contains(to, string(filepath.Separator)) {
		result.failed["validation"] = "rename_to cannot contain path separators or '..'"
		return result
	}

	for _, path := range files {
		select {
		case <-ctx.Done():
			result.failed[path] = "cancelled"
			continue
		default:
		}

		dir := filepath.Dir(path)
		base := filepath.Base(path)

		if !strings.Contains(base, from) {
			result.skipped = append(result.skipped, path)
			continue
		}

		newBase := strings.ReplaceAll(base, from, to)
		newPath := filepath.Join(dir, newBase)

		newPath = filepath.Clean(newPath)
		if filepath.Dir(newPath) != filepath.Clean(dir) {
			result.failed[path] = "path traversal detected: new path escapes original directory"
			continue
		}

		if dryRun {
			result.succeeded = append(result.succeeded, fmt.Sprintf("%s -> %s", path, newPath))
			continue
		}

		if err := os.Rename(path, newPath); err != nil {
			result.failed[path] = err.Error()
		} else {
			result.succeeded = append(result.succeeded, fmt.Sprintf("%s -> %s", path, newPath))
		}
	}

	return result
}

// executeDelete deletes multiple files.
func (t *BatchTool) executeDelete(ctx context.Context, files []string, dryRun bool) batchResult {
	result := batchResult{
		totalFiles:  len(files),
		failed:      make(map[string]string),
		description: "delete files",
	}

	for _, path := range files {
		select {
		case <-ctx.Done():
			result.failed[path] = "cancelled"
			continue
		default:
		}

		if dryRun {
			result.succeeded = append(result.succeeded, path)
			continue
		}

		if err := os.Remove(path); err != nil {
			result.failed[path] = err.Error()
		} else {
			result.succeeded = append(result.succeeded, path)
		}
	}

	return result
}

// executeParallel runs operations in parallel with progress callbacks and failure threshold.
func (t *BatchTool) executeParallel(ctx context.Context, files []string, operation func(string) error) batchResult {
	result := batchResult{
		totalFiles: len(files),
		failed:     make(map[string]string),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var processedCount int
	var failureCount int
	var shouldStop bool

	semaphore := make(chan struct{}, 10)

	for _, path := range files {
		mu.Lock()
		if shouldStop {
			result.failed[path] = "stopped due to failure threshold"
			mu.Unlock()
			continue
		}
		mu.Unlock()

		select {
		case <-ctx.Done():
			mu.Lock()
			result.failed[path] = "cancelled"
			mu.Unlock()
			continue
		default:
		}

		wg.Add(1)
		semaphore <- struct{}{}

		go func(p string) {
			defer wg.Done()
			defer func() { <-semaphore }()

			mu.Lock()
			if shouldStop {
				result.failed[p] = "stopped due to failure threshold"
				mu.Unlock()
				return
			}
			mu.Unlock()

			select {
			case <-ctx.Done():
				mu.Lock()
				result.failed[p] = "cancelled"
				mu.Unlock()
				return
			default:
			}

			err := operation(p)
			mu.Lock()
			processedCount++
			success := true

			if err != nil {
				success = false
				if strings.Contains(err.Error(), "not found") {
					result.skipped = append(result.skipped, p)
				} else {
					result.failed[p] = err.Error()
					failureCount++

					if t.failureThreshold > 0 && processedCount >= 3 {
						currentFailureRate := float64(failureCount) / float64(processedCount)
						if currentFailureRate > t.failureThreshold {
							shouldStop = true
							result.failed["_threshold"] = fmt.Sprintf("failure rate %.1f%% exceeded threshold %.1f%%",
								currentFailureRate*100, t.failureThreshold*100)
						}
					}
				}
			} else {
				result.succeeded = append(result.succeeded, p)
			}

			if t.progressCallback != nil {
				t.progressCallback(processedCount, len(files), p, success)
			}

			mu.Unlock()
		}(path)
	}

	wg.Wait()
	return result
}

// formatResult formats the batch result for output.
func (t *BatchTool) formatResult(op string, result batchResult, dryRun bool) *sdk.ToolResult {
	var sb strings.Builder

	prefix := ""
	if dryRun {
		prefix = "[DRY RUN] "
	}

	sb.WriteString(fmt.Sprintf("%sBatch %s: %s\n\n", prefix, op, result.description))
	sb.WriteString(fmt.Sprintf("Total: %d files\n", result.totalFiles))
	sb.WriteString(fmt.Sprintf("Succeeded: %d\n", len(result.succeeded)))
	if len(result.skipped) > 0 {
		sb.WriteString(fmt.Sprintf("Skipped: %d\n", len(result.skipped)))
	}
	if len(result.failed) > 0 {
		sb.WriteString(fmt.Sprintf("Failed: %d\n", len(result.failed)))
	}

	if len(result.succeeded) > 0 && len(result.succeeded) <= 10 {
		sb.WriteString("\nSucceeded:\n")
		for _, path := range result.succeeded {
			sb.WriteString(fmt.Sprintf("  + %s\n", filepath.Base(path)))
		}
	}

	if len(result.failed) > 0 {
		sb.WriteString("\nFailed:\n")
		for path, err := range result.failed {
			sb.WriteString(fmt.Sprintf("  x %s: %s\n", filepath.Base(path), err))
		}
	}

	return sdk.NewSuccessResult(sb.String())
}
