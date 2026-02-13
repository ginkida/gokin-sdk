package sdk

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/genai"
)

// Tool defines the interface for all tools.
type Tool interface {
	// Name returns the unique name of the tool.
	Name() string

	// Description returns a human-readable description.
	Description() string

	// Declaration returns the Gemini function declaration for this tool.
	Declaration() *genai.FunctionDeclaration

	// Execute runs the tool with the given arguments.
	Execute(ctx context.Context, args map[string]any) (*ToolResult, error)
}

// SafetyLevel indicates the risk level of a tool operation.
type SafetyLevel string

const (
	SafetyLevelSafe      SafetyLevel = "safe"
	SafetyLevelCaution   SafetyLevel = "caution"
	SafetyLevelDangerous SafetyLevel = "dangerous"
	SafetyLevelCritical  SafetyLevel = "critical"
)

// ExecutionSummary provides metadata about a tool execution for logging and approval flows.
type ExecutionSummary struct {
	ToolName         string        `json:"tool_name"`
	DisplayName      string        `json:"display_name"`
	Action           string        `json:"action"`
	Target           string        `json:"target"`
	ExpectedTime     time.Duration `json:"expected_time"`
	RiskLevel        SafetyLevel   `json:"risk_level"`
	UserVisible      bool          `json:"user_visible"`
	RequiresApproval bool          `json:"requires_approval"`
}

// MultimodalPart represents a non-text part of a tool result (e.g., image, binary).
type MultimodalPart struct {
	MimeType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	// Content is the main result content (usually text).
	Content string

	// Data holds structured data (e.g., parsed JSON, typed objects).
	Data any

	// Error contains an error message if the tool failed.
	Error string

	// Success indicates if the tool executed successfully.
	Success bool

	// ExecutionSummary provides metadata about the execution.
	ExecutionSummary *ExecutionSummary

	// SafetyLevel indicates the risk level of the operation performed.
	SafetyLevel SafetyLevel

	// Duration records how long the tool execution took.
	Duration string

	// MultimodalParts holds non-text result parts (images, binary data).
	MultimodalParts []*MultimodalPart
}

// ValidatingTool is an optional interface that tools can implement
// to validate arguments before execution.
type ValidatingTool interface {
	Tool
	Validate(args map[string]any) error
}

// ValidationError represents a validation failure for a specific field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s: %s", e.Field, e.Message)
}

// NewSuccessResult creates a successful tool result.
func NewSuccessResult(content string) *ToolResult {
	return &ToolResult{
		Content: content,
		Success: true,
	}
}

// NewErrorResult creates a failed tool result.
func NewErrorResult(errMsg string) *ToolResult {
	return &ToolResult{
		Error:   errMsg,
		Success: false,
	}
}

// ToMap converts the result to a map for Gemini function response.
func (r *ToolResult) ToMap() map[string]any {
	result := make(map[string]any)
	if r.Success {
		result["success"] = true
		if r.Content != "" {
			content := r.Content
			const maxChars = 100000
			if len(content) > maxChars {
				content = content[:maxChars] + fmt.Sprintf("\n... (output truncated: showing %d of %d characters)", maxChars, len(r.Content))
			}
			result["content"] = content
		}
		if r.Data != nil {
			result["data"] = r.Data
		}
	} else {
		result["success"] = false
		result["error"] = r.Error
		if r.Content != "" {
			result["content"] = r.Content
		}
	}
	return result
}

// Registry manages the collection of available tools.
type Registry struct {
	tools map[string]Tool
	mu    sync.RWMutex
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}

	r.tools[name] = tool
	return nil
}

// MustRegister adds a tool to the registry and panics on error.
func (r *Registry) MustRegister(tool Tool) {
	if err := r.Register(tool); err != nil {
		panic(err)
	}
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, ok := r.tools[name]
	return tool, ok
}

// List returns all registered tools.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// Names returns the names of all registered tools.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// GeminiTools returns the tools in Gemini format.
func (r *Registry) GeminiTools() []*genai.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	declarations := make([]*genai.FunctionDeclaration, 0, len(r.tools))
	for _, tool := range r.tools {
		declarations = append(declarations, tool.Declaration())
	}
	return []*genai.Tool{
		{
			FunctionDeclarations: declarations,
		},
	}
}

// GetString extracts a string argument from the args map.
func GetString(args map[string]any, key string) (string, bool) {
	val, ok := args[key]
	if !ok {
		return "", false
	}
	str, ok := val.(string)
	return str, ok
}

// GetStringDefault extracts a string argument with a default value.
func GetStringDefault(args map[string]any, key, defaultVal string) string {
	if val, ok := GetString(args, key); ok {
		return val
	}
	return defaultVal
}

// GetInt extracts an integer argument from the args map.
func GetInt(args map[string]any, key string) (int, bool) {
	val, ok := args[key]
	if !ok {
		return 0, false
	}
	switch v := val.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

// GetIntDefault extracts an integer argument with a default value.
func GetIntDefault(args map[string]any, key string, defaultVal int) int {
	if val, ok := GetInt(args, key); ok {
		return val
	}
	return defaultVal
}

// GetBool extracts a boolean argument from the args map.
func GetBool(args map[string]any, key string) (bool, bool) {
	val, ok := args[key]
	if !ok {
		return false, false
	}
	b, ok := val.(bool)
	return b, ok
}

// GetBoolDefault extracts a boolean argument with a default value.
func GetBoolDefault(args map[string]any, key string, defaultVal bool) bool {
	if val, ok := GetBool(args, key); ok {
		return val
	}
	return defaultVal
}
