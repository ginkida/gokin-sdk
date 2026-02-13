package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ginkida/gokin-sdk/memory"
)

// ErrorPattern defines a pattern-based error classification rule.
type ErrorPattern struct {
	Pattern          *regexp.Regexp
	Category         string
	Suggestion       string
	ShouldRetry      bool
	Alternative      string
	ShouldRetryWithFix bool
	SuggestedFix     string
}

// PredictedFile represents a file predicted by the file predictor.
type PredictedFile struct {
	Path       string
	Confidence float64
}

// FilePredictor predicts related files based on access patterns.
type FilePredictor interface {
	PredictFiles(currentFile string, limit int) []PredictedFile
}

// ReflectionResult contains the analysis of an error.
type ReflectionResult struct {
	Category       string
	Suggestion     string
	RootCause      string
	ShouldRetry    bool
	PredictedFiles []string
	Alternative    string
	Matched        bool
}

// Reflector analyzes errors with regex patterns and optional LLM semantic fallback.
type Reflector struct {
	client     Client
	errorStore *memory.ErrorStore
	predictor  FilePredictor
	patterns   []ErrorPattern
}

// NewReflector creates a new error reflector.
// client is optional — if nil, semantic analysis is disabled.
// errorStore is optional — if nil, learning is disabled.
func NewReflector(client Client, errorStore *memory.ErrorStore) *Reflector {
	return &Reflector{
		client:     client,
		errorStore: errorStore,
		patterns:   defaultErrorPatterns(),
	}
}

// SetPredictor sets the file predictor for file_not_found suggestions.
func (r *Reflector) SetPredictor(predictor FilePredictor) {
	r.predictor = predictor
}

// Analyze classifies an error and returns suggestions.
// Order: 1) learned errors, 2) pattern matching, 3) semantic analysis (+ learning), 4) fallback.
func (r *Reflector) Analyze(ctx context.Context, toolName string, args map[string]any, errorMsg string) *ReflectionResult {
	lowerErr := strings.ToLower(errorMsg)

	// 1. Try learned errors first (fast lookup from past experience)
	if r.errorStore != nil {
		matches := r.errorStore.FindSolution(errorMsg)
		if len(matches) > 0 {
			best := matches[0]
			return &ReflectionResult{
				Category:    best.ErrorType,
				Suggestion:  best.Solution,
				ShouldRetry: false,
				Matched:     true,
			}
		}
	}

	// 2. Try pattern-based matching
	for _, p := range r.patterns {
		if p.Pattern.MatchString(lowerErr) {
			result := &ReflectionResult{
				Category:    p.Category,
				Suggestion:  p.Suggestion,
				ShouldRetry: p.ShouldRetry,
				Alternative: p.Alternative,
				Matched:     true,
			}

			// Apply fix suggestion if available
			if p.ShouldRetryWithFix && p.SuggestedFix != "" {
				result.ShouldRetry = true
				result.Suggestion += "\nSuggested fix: " + p.SuggestedFix
			}

			if p.Category == "file_not_found" {
				result.PredictedFiles = r.predictFiles(errorMsg, args)
			}
			return result
		}
	}

	// 3. Try semantic analysis via LLM (+ persistent learning)
	if r.client != nil {
		if result := r.semanticAnalyze(ctx, toolName, errorMsg); result != nil {
			return result
		}
	}

	// 4. Generic fallback
	return &ReflectionResult{
		Category:   "unknown",
		Suggestion: buildGenericIntervention(toolName, errorMsg),
		Matched:    false,
	}
}

// predictFiles uses the file predictor and path extraction to suggest files.
func (r *Reflector) predictFiles(errorMsg string, args map[string]any) []string {
	var predictions []string

	// Extract path from error/args
	if fp := extractFilePathFromError(errorMsg, args); fp != "" {
		predictions = append(predictions, fp)

		// Use predictor for additional suggestions
		if r.predictor != nil {
			predicted := r.predictor.PredictFiles(fp, 3)
			for _, pf := range predicted {
				predictions = append(predictions, pf.Path)
			}
		}
	}

	return predictions
}

// AddPattern adds a custom error pattern.
func (r *Reflector) AddPattern(pattern *regexp.Regexp, category, suggestion string, shouldRetry bool, alternative string) {
	r.patterns = append(r.patterns, ErrorPattern{
		Pattern:     pattern,
		Category:    category,
		Suggestion:  suggestion,
		ShouldRetry: shouldRetry,
		Alternative: alternative,
	})
}

// LearnFromError records an error pattern in the error store.
func (r *Reflector) LearnFromError(errorType, pattern, solution string, tags []string) error {
	if r.errorStore == nil {
		return nil
	}
	return r.errorStore.RecordError(errorType, pattern, solution, tags)
}

// semanticAnalyze uses the LLM to classify an unrecognized error.
// After successful analysis, it records the result in the error store for future lookups.
func (r *Reflector) semanticAnalyze(ctx context.Context, toolName, errorMsg string) *ReflectionResult {
	prompt := fmt.Sprintf(`Analyze this error and provide a JSON response:
Tool: %s
Error: %s

Respond with JSON only:
{"category": "...", "suggestion": "...", "root_cause": "...", "should_retry": false}`, toolName, errorMsg)

	sr, err := r.client.SendMessage(ctx, prompt)
	if err != nil {
		return nil
	}

	resp, err := sr.Collect(ctx)
	if err != nil || resp.Text == "" {
		return nil
	}

	// Parse JSON from response
	text := resp.Text
	if idx := strings.Index(text, "{"); idx >= 0 {
		if end := strings.LastIndex(text, "}"); end > idx {
			text = text[idx : end+1]
		}
	}

	var parsed struct {
		Category    string `json:"category"`
		Suggestion  string `json:"suggestion"`
		RootCause   string `json:"root_cause"`
		ShouldRetry bool   `json:"should_retry"`
	}

	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil
	}

	// Persistent learning: record this analysis for future lookups
	if r.errorStore != nil {
		tags := []string{toolName, parsed.Category}
		r.errorStore.RecordError(parsed.Category, errorMsg, parsed.Suggestion, tags)
	}

	return &ReflectionResult{
		Category:    parsed.Category,
		Suggestion:  parsed.Suggestion,
		RootCause:   parsed.RootCause,
		ShouldRetry: parsed.ShouldRetry,
		Matched:     true,
	}
}

// BuildIntervention creates a formatted intervention message.
func (r *Reflector) BuildIntervention(toolName string, args map[string]any, result *ReflectionResult, errorMsg string) string {
	var sb strings.Builder

	sb.WriteString("Let me reflect on what went wrong.\n\n")
	sb.WriteString("**Error Analysis:**\n")
	sb.WriteString("- Tool: " + toolName + "\n")
	sb.WriteString("- Category: " + result.Category + "\n")
	sb.WriteString("- Error: " + errorMsg + "\n\n")
	sb.WriteString("**My Assessment:**\n")
	sb.WriteString(result.Suggestion + "\n\n")

	if result.Alternative != "" {
		sb.WriteString("**Alternative Approach:**\n")
		sb.WriteString("I should try using " + result.Alternative + " instead.\n\n")
	}

	if len(result.PredictedFiles) > 0 {
		sb.WriteString("**Predicted Files:**\n")
		for _, f := range result.PredictedFiles {
			sb.WriteString("- " + f + "\n")
		}
		sb.WriteString("\n")
	}

	if result.ShouldRetry {
		sb.WriteString("I'll retry with the suggested modifications.\n")
	} else {
		sb.WriteString("I need to take a different approach to achieve the goal.\n")
	}

	return sb.String()
}

func extractFilePathFromError(errorMsg string, args map[string]any) string {
	if args != nil {
		for _, key := range []string{"path", "file_path", "filepath", "file", "filename"} {
			if v, ok := args[key]; ok {
				if s, ok := v.(string); ok && s != "" {
					return s
				}
			}
		}
	}
	pathPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:file|path|directory)\s+['"]?([^\s'"]+?)['"]?\s+(?:not found|does not exist)`),
		regexp.MustCompile(`no such file or directory:\s*['"]?([^\s'"]+)`),
	}
	for _, re := range pathPatterns {
		if matches := re.FindStringSubmatch(errorMsg); len(matches) > 1 {
			return matches[1]
		}
	}
	return ""
}

func buildGenericIntervention(toolName string, errorMsg string) string {
	var sb strings.Builder
	sb.WriteString("I encountered an unexpected error.\n\n")
	sb.WriteString("**Error Details:**\n")
	sb.WriteString("- Tool: " + toolName + "\n")
	sb.WriteString("- Error: " + errorMsg + "\n\n")
	sb.WriteString("**Next Steps:**\n")
	sb.WriteString("1. Check if my arguments are correct\n")
	sb.WriteString("2. Verify the target exists and is accessible\n")
	sb.WriteString("3. Consider an alternative approach\n")
	return sb.String()
}

func defaultErrorPatterns() []ErrorPattern {
	return []ErrorPattern{
		{Pattern: regexp.MustCompile(`(file|path|directory).*not (found|exist)|no such file|enoent`), Category: "file_not_found", Suggestion: "The file or directory doesn't exist. Use glob to search for similar files.", Alternative: "glob"},
		{Pattern: regexp.MustCompile(`cannot find|could not find|unable to (find|locate)`), Category: "file_not_found", Suggestion: "The target wasn't found. Try using glob with a broader pattern.", Alternative: "glob"},
		{Pattern: regexp.MustCompile(`permission denied|access denied|eacces|eperm|not permitted`), Category: "permission_denied", Suggestion: "Permission was denied. Check file permissions or consider an alternative path."},
		{Pattern: regexp.MustCompile(`read.?only|cannot (write|modify)`), Category: "permission_denied", Suggestion: "The target is read-only. Check file permissions or work with a copy."},
		{Pattern: regexp.MustCompile(`command not found|executable.*not found|unknown command|not recognized`), Category: "command_not_found", Suggestion: "The command doesn't exist in PATH. Check spelling or install the required package."},
		{Pattern: regexp.MustCompile(`no such (program|command|binary)`), Category: "command_not_found", Suggestion: "The program is not installed. Install the required binary or use an alternative."},
		{Pattern: regexp.MustCompile(`timeout|timed out|deadline exceeded|context deadline`), Category: "timeout", Suggestion: "The operation took too long. Try breaking the task into smaller parts.", ShouldRetry: true},
		{Pattern: regexp.MustCompile(`connection refused|network unreachable|host not found|dns|econnrefused`), Category: "network_error", Suggestion: "Network connection failed. Check if the service is running or try again later.", ShouldRetry: true},
		{Pattern: regexp.MustCompile(`connection reset|broken pipe|econnreset`), Category: "network_error", Suggestion: "Connection was interrupted. This might be temporary - retry the request.", ShouldRetry: true},
		{Pattern: regexp.MustCompile(`syntax error|parse error`), Category: "syntax_error", Suggestion: "There's a syntax or format error. Review the content for typos or missing brackets."},
		{Pattern: regexp.MustCompile(`invalid (syntax|json|yaml|format)`), Category: "syntax_error", Suggestion: "Invalid format detected. Check the content structure.", ShouldRetryWithFix: true, SuggestedFix: "Validate the input format before retrying."},
		{Pattern: regexp.MustCompile(`unexpected (token|character|end|eof)`), Category: "syntax_error", Suggestion: "Parsing failed due to unexpected content. Check for unclosed quotes or brackets."},
		{Pattern: regexp.MustCompile(`compilation (failed|error)|build (failed|error)`), Category: "compilation_error", Suggestion: "Code compilation failed. Read the error message carefully and fix the referenced line."},
		{Pattern: regexp.MustCompile(`undefined:|cannot use`), Category: "compilation_error", Suggestion: "Type error or undefined symbol. Check imports and type compatibility."},
		{Pattern: regexp.MustCompile(`compilation failed|build failed`), Category: "compilation_error", Suggestion: "Build failed. Check the build output for specific errors."},
		{Pattern: regexp.MustCompile(`undeclared|undefined reference|unknown (type|identifier|symbol)`), Category: "compilation_error", Suggestion: "Missing declaration or import. Check if the identifier is imported correctly."},
		{Pattern: regexp.MustCompile(`invalid argument|illegal argument|bad (parameter|argument|input)`), Category: "invalid_args", Suggestion: "Invalid argument provided. Check the function signature and argument types.", ShouldRetryWithFix: true, SuggestedFix: "Review the expected argument types and adjust."},
		{Pattern: regexp.MustCompile(`test(s)? failed|--- fail`), Category: "test_failure", Suggestion: "Tests are failing. Read the test output to understand expected vs actual."},
		{Pattern: regexp.MustCompile(`fail:`), Category: "test_failure", Suggestion: "A test assertion failed. Check the test output."},
		{Pattern: regexp.MustCompile(`assertion (failed|error)|expected .* got`), Category: "test_failure", Suggestion: "An assertion failed. Compare expected and actual values."},
		{Pattern: regexp.MustCompile(`out of memory|memory limit|oom|enomem`), Category: "resource_error", Suggestion: "Ran out of memory. Process smaller chunks or optimize memory usage."},
		{Pattern: regexp.MustCompile(`disk (full|space)|no space left|enospc`), Category: "resource_error", Suggestion: "Disk is full. Free up space or write to a different location."},
		{Pattern: regexp.MustCompile(`not a git repository|fatal: not a git`), Category: "git_error", Suggestion: "Not in a git repository. Make sure you're in the correct directory."},
		{Pattern: regexp.MustCompile(`detached head|not on any branch`), Category: "git_error", Suggestion: "HEAD is detached. Create or checkout a branch before making changes."},
		{Pattern: regexp.MustCompile(`merge conflict|automatic merge failed`), Category: "git_error", Suggestion: "There's a merge conflict. Read the conflicting files and resolve manually.", Alternative: "read"},
		{Pattern: regexp.MustCompile(`rate limit|too many requests|429|throttl`), Category: "rate_limit", Suggestion: "Hit rate limits. Wait a moment before retrying.", ShouldRetry: true},
		{Pattern: regexp.MustCompile(`unauthorized|authentication failed|401|invalid (token|credentials)`), Category: "auth_error", Suggestion: "Authentication failed. Check credentials or API key validity."},
		{Pattern: regexp.MustCompile(`already exists|file exists|eexist|duplicate`), Category: "already_exists", Suggestion: "The target already exists. Use a different name or update it instead."},
	}
}
