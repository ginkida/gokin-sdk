package context

import (
	"fmt"
	"strings"
)

// PromptBuilder constructs system prompts for agents.
type PromptBuilder struct {
	workDir     string
	toolGuides  map[string]ToolUsageGuide
	customParts []string
}

// ToolUsageGuide provides guidance for using a specific tool.
type ToolUsageGuide struct {
	Name           string
	WhenToUse      string
	CommonMistakes []string
	Examples       []string
}

// ToolChainPattern describes a sequence of tools for a common task.
type ToolChainPattern struct {
	Name        string
	Description string
	Steps       []string
}

// NewPromptBuilder creates a new prompt builder.
func NewPromptBuilder(workDir string) *PromptBuilder {
	return &PromptBuilder{
		workDir:    workDir,
		toolGuides: defaultToolGuides(),
	}
}

// AddCustomSection adds a custom section to the system prompt.
func (pb *PromptBuilder) AddCustomSection(section string) {
	pb.customParts = append(pb.customParts, section)
}

// Build constructs the full system prompt.
func (pb *PromptBuilder) Build() string {
	var builder strings.Builder

	// Base prompt
	builder.WriteString("You are a helpful AI coding assistant with access to tools.\n\n")

	// Working directory
	builder.WriteString(fmt.Sprintf("Working directory: %s\n\n", pb.workDir))

	// Tool guidance
	builder.WriteString("## Tool Usage Guidelines\n\n")
	builder.WriteString("- Read files before editing them\n")
	builder.WriteString("- Use glob to find files by pattern, grep to search content\n")
	builder.WriteString("- Use bash for commands that don't have dedicated tools\n")
	builder.WriteString("- Prefer dedicated tools (read, edit, write) over bash equivalents\n")
	builder.WriteString("- Execute multiple independent tool calls in parallel\n\n")

	// Tool chain patterns
	builder.WriteString("## Common Patterns\n\n")
	for _, pattern := range defaultChainPatterns() {
		builder.WriteString(fmt.Sprintf("**%s**: %s\n", pattern.Name, pattern.Description))
		for i, step := range pattern.Steps {
			builder.WriteString(fmt.Sprintf("  %d. %s\n", i+1, step))
		}
		builder.WriteString("\n")
	}

	// Custom sections
	for _, part := range pb.customParts {
		builder.WriteString(part)
		builder.WriteString("\n\n")
	}

	return builder.String()
}

// BuildForAgentType constructs a system prompt tailored to the agent type.
func (pb *PromptBuilder) BuildForAgentType(agentType string) string {
	var builder strings.Builder

	switch agentType {
	case "explore":
		builder.WriteString("You are an exploration agent. Your job is to search and read files to understand code.\n")
		builder.WriteString("Available tools: read, glob, grep, tree, list_dir\n")
		builder.WriteString("Focus on finding relevant files and understanding their content.\n\n")
	case "bash":
		builder.WriteString("You are a command execution agent. Your job is to run commands and report results.\n")
		builder.WriteString("Available tools: bash, read, glob\n")
		builder.WriteString("Focus on executing commands correctly and interpreting output.\n\n")
	case "plan":
		builder.WriteString("You are a planning agent. Your job is to analyze problems and create step-by-step plans.\n")
		builder.WriteString("Available tools: read, glob, grep, tree, list_dir, bash\n")
		builder.WriteString("Focus on understanding the full picture before proposing solutions.\n\n")
	default:
		builder.WriteString("You are a general-purpose coding assistant with access to all tools.\n\n")
	}

	builder.WriteString(fmt.Sprintf("Working directory: %s\n\n", pb.workDir))

	for _, part := range pb.customParts {
		builder.WriteString(part)
		builder.WriteString("\n\n")
	}

	return builder.String()
}

// GetToolGuide returns the usage guide for a specific tool.
func (pb *PromptBuilder) GetToolGuide(toolName string) (ToolUsageGuide, bool) {
	guide, ok := pb.toolGuides[toolName]
	return guide, ok
}

func defaultToolGuides() map[string]ToolUsageGuide {
	return map[string]ToolUsageGuide{
		"read": {
			Name:      "read",
			WhenToUse: "When you need to see the contents of a specific file",
			CommonMistakes: []string{
				"Reading a file without checking if it exists first",
				"Not using offset/limit for large files",
			},
		},
		"grep": {
			Name:      "grep",
			WhenToUse: "When searching for patterns across multiple files",
			CommonMistakes: []string{
				"Using too broad a pattern that returns too many results",
				"Forgetting to use glob filter for specific file types",
			},
		},
		"glob": {
			Name:      "glob",
			WhenToUse: "When finding files by name pattern",
			CommonMistakes: []string{
				"Using grep when glob would be more appropriate for finding files",
			},
		},
		"edit": {
			Name:      "edit",
			WhenToUse: "When making targeted changes to existing files",
			CommonMistakes: []string{
				"Not reading the file first to see current content",
				"Providing old_string that matches multiple locations",
			},
		},
		"bash": {
			Name:      "bash",
			WhenToUse: "When running commands, tests, builds, or other system operations",
			CommonMistakes: []string{
				"Using bash for file operations when dedicated tools exist",
				"Running long-running commands without timeout consideration",
			},
		},
	}
}

func defaultChainPatterns() []ToolChainPattern {
	return []ToolChainPattern{
		{
			Name:        "Explore Code",
			Description: "Find and understand code",
			Steps:       []string{"glob to find files", "read to see content", "analyze structure"},
		},
		{
			Name:        "Find Usage",
			Description: "Find where something is used",
			Steps:       []string{"grep for pattern", "read context of matches", "explain findings"},
		},
		{
			Name:        "Debug Error",
			Description: "Investigate and fix an error",
			Steps:       []string{"read error file", "grep for related code", "read dependencies", "explain root cause"},
		},
		{
			Name:        "Implement Feature",
			Description: "Add new functionality",
			Steps:       []string{"glob + read to understand existing code", "plan changes", "edit/write files", "bash to test", "summarize changes"},
		},
	}
}
