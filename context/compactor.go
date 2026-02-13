package context

import (
	"fmt"
	"strings"
)

// Compactor intelligently truncates tool output while preserving error information.
type Compactor struct {
	maxChars int
}

// NewCompactor creates a new Compactor with the given maximum character limit.
func NewCompactor(maxChars int) *Compactor {
	if maxChars <= 0 {
		maxChars = 10000
	}
	return &Compactor{maxChars: maxChars}
}

// Compact truncates content if it exceeds the limit, preserving error lines.
func (c *Compactor) Compact(content string) string {
	if len(content) <= c.maxChars {
		return content
	}

	if containsErrorIndicators(content) {
		return c.compactWithErrorPreservation(content)
	}

	return c.simpleCompact(content)
}

// CompactForTool applies tool-aware compaction.
func (c *Compactor) CompactForTool(toolName, content string) string {
	if len(content) <= c.maxChars {
		return content
	}

	switch toolName {
	case "bash", "run_tests":
		return c.compactCommandOutput(content)
	case "read":
		return c.compactFileContent(content)
	case "grep", "glob":
		return c.compactSearchResults(content)
	case "tree":
		return c.compactTreeOutput(content)
	case "list_dir":
		return c.compactFileList(content)
	default:
		return c.Compact(content)
	}
}

func (c *Compactor) simpleCompact(content string) string {
	half := c.maxChars / 2
	return content[:half] +
		fmt.Sprintf("\n\n... (%d characters omitted) ...\n\n", len(content)-c.maxChars) +
		content[len(content)-half:]
}

func (c *Compactor) compactWithErrorPreservation(content string) string {
	lines := strings.Split(content, "\n")

	var errorLines []string
	var normalLines []string

	for _, line := range lines {
		if isErrorLine(line) {
			errorLines = append(errorLines, line)
		} else {
			normalLines = append(normalLines, line)
		}
	}

	// Always keep all error lines
	errorContent := strings.Join(errorLines, "\n")
	remaining := c.maxChars - len(errorContent) - 50 // overhead

	if remaining <= 0 {
		// Only errors fit
		if len(errorContent) > c.maxChars {
			return errorContent[:c.maxChars]
		}
		return errorContent
	}

	// Add as many normal lines as fit
	var kept []string
	charCount := 0
	for _, line := range normalLines {
		if charCount+len(line)+1 > remaining {
			break
		}
		kept = append(kept, line)
		charCount += len(line) + 1
	}

	var builder strings.Builder
	if len(kept) > 0 {
		builder.WriteString(strings.Join(kept, "\n"))
		builder.WriteString("\n\n")
	}
	if len(kept) < len(normalLines) {
		builder.WriteString(fmt.Sprintf("... (%d lines omitted) ...\n\n", len(normalLines)-len(kept)))
	}
	builder.WriteString("=== Errors ===\n")
	builder.WriteString(errorContent)

	return builder.String()
}

func (c *Compactor) compactCommandOutput(content string) string {
	lines := strings.Split(content, "\n")

	if containsErrorIndicators(content) {
		// Bias toward tail where errors usually appear
		headLines := 3
		tailLines := 25

		if len(lines) <= headLines+tailLines {
			return c.Compact(content)
		}

		var builder strings.Builder
		for i := 0; i < headLines && i < len(lines); i++ {
			builder.WriteString(lines[i])
			builder.WriteString("\n")
		}
		builder.WriteString(fmt.Sprintf("\n... (%d lines omitted) ...\n\n", len(lines)-headLines-tailLines))
		for i := len(lines) - tailLines; i < len(lines); i++ {
			builder.WriteString(lines[i])
			builder.WriteString("\n")
		}
		return builder.String()
	}

	return c.simpleCompact(content)
}

func (c *Compactor) compactFileContent(content string) string {
	lines := strings.Split(content, "\n")

	// Keep function/type signatures
	var important []string
	var other []string

	for _, line := range lines {
		if isSignatureLine(line) {
			important = append(important, line)
		} else {
			other = append(other, line)
		}
	}

	var builder strings.Builder
	builder.WriteString("=== Key Declarations ===\n")
	for _, line := range important {
		builder.WriteString(line)
		builder.WriteString("\n")
	}

	remaining := c.maxChars - builder.Len() - 50
	if remaining > 0 {
		builder.WriteString("\n=== Content ===\n")
		charCount := 0
		added := 0
		for _, line := range other {
			if charCount+len(line)+1 > remaining {
				builder.WriteString(fmt.Sprintf("... (%d more lines)\n", len(other)-added))
				break
			}
			builder.WriteString(line)
			builder.WriteString("\n")
			charCount += len(line) + 1
			added++
		}
	}

	return builder.String()
}

func (c *Compactor) compactSearchResults(content string) string {
	lines := strings.Split(content, "\n")

	// Prioritize error-related matches
	var errorMatches []string
	var otherMatches []string

	for _, line := range lines {
		if isErrorLine(line) {
			errorMatches = append(errorMatches, line)
		} else {
			otherMatches = append(otherMatches, line)
		}
	}

	var builder strings.Builder
	charCount := 0

	// Add error matches first
	for _, line := range errorMatches {
		if charCount+len(line)+1 > c.maxChars {
			break
		}
		builder.WriteString(line)
		builder.WriteString("\n")
		charCount += len(line) + 1
	}

	// Fill with other matches, tracking how many we actually add
	added := 0
	for _, line := range otherMatches {
		if charCount+len(line)+1 > c.maxChars {
			remaining := len(otherMatches) - added
			if remaining > 0 {
				builder.WriteString(fmt.Sprintf("... (%d more matches)\n", remaining))
			}
			break
		}
		builder.WriteString(line)
		builder.WriteString("\n")
		charCount += len(line) + 1
		added++
	}

	return builder.String()
}

// compactTreeOutput compacts directory tree output by keeping head + summary.
func (c *Compactor) compactTreeOutput(content string) string {
	lines := strings.Split(content, "\n")

	if len(content) <= c.maxChars {
		return content
	}

	// Show first portion + count
	maxLines := 30
	if maxLines > len(lines) {
		maxLines = len(lines)
	}

	var builder strings.Builder
	for i := 0; i < maxLines; i++ {
		builder.WriteString(lines[i])
		builder.WriteString("\n")
	}
	if len(lines) > maxLines {
		builder.WriteString(fmt.Sprintf("\n... (%d more entries, %d total)\n", len(lines)-maxLines, len(lines)))
	}

	return builder.String()
}

// compactFileList compacts file listing output by showing a sample + total count.
func (c *Compactor) compactFileList(content string) string {
	lines := strings.Split(content, "\n")

	if len(content) <= c.maxChars {
		return content
	}

	// Show first and last portions
	headCount := 15
	tailCount := 5
	if headCount+tailCount >= len(lines) {
		return c.simpleCompact(content)
	}

	var builder strings.Builder
	for i := 0; i < headCount && i < len(lines); i++ {
		builder.WriteString(lines[i])
		builder.WriteString("\n")
	}
	builder.WriteString(fmt.Sprintf("\n... (%d more files) ...\n\n", len(lines)-headCount-tailCount))
	for i := len(lines) - tailCount; i < len(lines); i++ {
		builder.WriteString(lines[i])
		builder.WriteString("\n")
	}

	return builder.String()
}

func containsErrorIndicators(content string) bool {
	lower := strings.ToLower(content)
	indicators := []string{
		// Original indicators
		"error:", "error :", "panic:", "fatal:",
		"failed:", "traceback", "exception:",
		"stack trace", ".go:", "at line",
		// Additional indicators
		"error(", "err:", "err =",
		"stacktrace:",
		"undefined:", "cannot use",
		"--- fail", "fail:",
		"permission denied", "access denied",
		"syntax error", "parse error",
		"compilation failed", "build failed",
	}
	for _, ind := range indicators {
		if strings.Contains(lower, ind) {
			return true
		}
	}
	return false
}

func isErrorLine(line string) bool {
	lower := strings.ToLower(line)
	patterns := []string{
		"error", "panic", "fatal", "failed",
		"traceback", "exception", "stack trace",
		"at line", "undefined", "not found",
		"permission denied", "access denied",
		"syntax error", "parse error",
		"compilation failed", "build failed",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}

	// Go stack trace: file.go:123 or file.go:123:45:
	if strings.Contains(line, ".go:") && strings.ContainsAny(line, "0123456789") {
		return true
	}
	// Go runtime patterns
	if strings.HasPrefix(strings.TrimSpace(line), "goroutine ") ||
		strings.Contains(line, "runtime.") ||
		strings.Contains(line, "panic(") {
		return true
	}
	// Python stack traces: file.py:123
	if strings.Contains(line, ".py:") && strings.ContainsAny(line, "0123456789") {
		return true
	}
	// JavaScript/TypeScript stack traces: file.js:123 or file.ts:123
	if (strings.Contains(line, ".js:") || strings.Contains(line, ".ts:")) &&
		strings.ContainsAny(line, "0123456789") {
		return true
	}
	// Java stack traces: file.java:123
	if strings.Contains(line, ".java:") && strings.ContainsAny(line, "0123456789") {
		return true
	}

	return false
}

// isSignatureLine returns true if the line is a function/type/class signature.
func isSignatureLine(line string) bool {
	trimmed := strings.TrimSpace(line)

	prefixes := []string{
		"func ", "type ", "class ", "def ",
		"interface ", "struct ",
		"function ", "export ",
		"const ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	// Inline struct/interface declarations: "X struct {", "X interface {"
	if (strings.Contains(trimmed, " struct {") || strings.Contains(trimmed, " interface {")) &&
		!strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") {
		return true
	}

	return false
}
