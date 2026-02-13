package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	sdk "github.com/ginkida/gokin-sdk"

	"github.com/bmatcuk/doublestar/v4"
	"google.golang.org/genai"
)

// GrepTool searches for patterns in files.
type GrepTool struct {
	workDir string
}

// NewGrep creates a new GrepTool with the given working directory.
func NewGrep(workDir string) *GrepTool {
	return &GrepTool{workDir: workDir}
}

func (t *GrepTool) Name() string { return "grep" }

func (t *GrepTool) Description() string {
	return "Searches for a regex pattern in files. Returns matching lines with file paths and line numbers."
}

func (t *GrepTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"pattern": {
					Type:        genai.TypeString,
					Description: "The regex pattern to search for",
				},
				"path": {
					Type:        genai.TypeString,
					Description: "File or directory to search in. Defaults to working directory.",
				},
				"glob": {
					Type:        genai.TypeString,
					Description: "Glob pattern to filter files (e.g., '*.go', '**/*.ts')",
				},
				"case_insensitive": {
					Type:        genai.TypeBoolean,
					Description: "If true, search is case-insensitive",
				},
			},
			Required: []string{"pattern"},
		},
	}
}

func (t *GrepTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	pattern, ok := sdk.GetString(args, "pattern")
	if !ok || pattern == "" {
		return sdk.NewErrorResult("pattern is required"), nil
	}

	searchPath := sdk.GetStringDefault(args, "path", t.workDir)
	globPattern := sdk.GetStringDefault(args, "glob", "")
	caseInsensitive := sdk.GetBoolDefault(args, "case_insensitive", false)

	if !filepath.IsAbs(searchPath) {
		searchPath = filepath.Join(t.workDir, searchPath)
	}

	regexPattern := pattern
	if caseInsensitive {
		regexPattern = "(?i)" + pattern
	}

	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("invalid regex: %s", err)), nil
	}

	files, err := getSearchFiles(searchPath, globPattern)
	if err != nil {
		return sdk.NewErrorResult(err.Error()), nil
	}

	const maxMatches = 500
	fileMatches := searchParallel(ctx, files, re)

	var results strings.Builder
	matchCount := 0
	fileCount := 0

	for _, fm := range fileMatches {
		if matchCount >= maxMatches {
			break
		}

		fileCount++
		relPath, _ := filepath.Rel(t.workDir, fm.path)
		if relPath == "" {
			relPath = fm.path
		}

		for _, match := range fm.matches {
			if matchCount >= maxMatches {
				break
			}
			results.WriteString(fmt.Sprintf("%s:%d: %s\n", relPath, match.lineNum, match.line))
			matchCount++
		}
	}

	if matchCount == 0 {
		return sdk.NewSuccessResult("No matches found."), nil
	}

	summary := fmt.Sprintf("Found %d match(es) in %d file(s):\n\n", matchCount, fileCount)
	if matchCount >= maxMatches {
		summary = fmt.Sprintf("Found %d+ match(es) in %d file(s) (capped at %d):\n\n", matchCount, fileCount, maxMatches)
	}
	return sdk.NewSuccessResult(summary + results.String()), nil
}

type grepMatch struct {
	lineNum int
	line    string
}

type fileMatch struct {
	path    string
	matches []grepMatch
}

func getSearchFiles(searchPath, globPattern string) ([]string, error) {
	info, err := os.Stat(searchPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("path not found: %s", searchPath)
		}
		return nil, fmt.Errorf("error accessing path: %w", err)
	}

	if !info.IsDir() {
		return []string{searchPath}, nil
	}

	if globPattern == "" {
		globPattern = "**/*"
	}
	fullPattern := filepath.Join(searchPath, globPattern)

	matches, err := doublestar.FilepathGlob(fullPattern)
	if err != nil {
		return nil, fmt.Errorf("invalid glob pattern: %w", err)
	}

	var files []string
	for _, match := range matches {
		info, err := os.Stat(match)
		if err == nil && !info.IsDir() && info.Size() < 10*1024*1024 && !isBinaryFile(match) {
			files = append(files, match)
		}
	}

	return files, nil
}

func searchParallel(ctx context.Context, files []string, re *regexp.Regexp) []fileMatch {
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make([]fileMatch, 0)
	semaphore := make(chan struct{}, 10)

	for _, file := range files {
		select {
		case <-ctx.Done():
			break
		default:
		}

		wg.Add(1)
		semaphore <- struct{}{}

		go func(f string) {
			defer wg.Done()
			defer func() { <-semaphore }()

			matches := searchFile(f, re)
			if len(matches) > 0 {
				mu.Lock()
				results = append(results, fileMatch{path: f, matches: matches})
				mu.Unlock()
			}
		}(file)
	}

	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		return results[i].path < results[j].path
	})

	return results
}

func searchFile(filePath string, re *regexp.Regexp) []grepMatch {
	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	var matches []grepMatch
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			if len(line) > 500 {
				line = line[:500] + "..."
			}
			matches = append(matches, grepMatch{lineNum: lineNum, line: line})
		}
	}

	return matches
}

func isBinaryFile(path string) bool {
	binaryExts := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
		".pdf": true, ".zip": true, ".tar": true, ".gz": true, ".rar": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true,
		".bin": true, ".dat": true, ".db": true, ".sqlite": true,
		".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	}
	ext := strings.ToLower(filepath.Ext(path))
	return binaryExts[ext]
}
