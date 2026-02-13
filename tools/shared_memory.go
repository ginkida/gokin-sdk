package tools

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// SharedMemoryAccess provides access to shared memory for the tool.
type SharedMemoryAccess interface {
	Write(key string, value any, entryType sdk.SharedEntryType, sourceAgent string)
	ReadEntry(key string) (*sdk.SharedEntry, bool)
	ReadByType(entryType sdk.SharedEntryType) []*sdk.SharedEntry
	ReadAll() []*sdk.SharedEntry
	Delete(key string)
	GetForContext(agentID string, maxEntries int) string
}

// SharedMemoryTool provides read/write access to shared inter-agent memory.
type SharedMemoryTool struct {
	memory  SharedMemoryAccess
	agentID string
}

// NewSharedMemoryTool creates a new SharedMemoryTool.
func NewSharedMemoryTool() *SharedMemoryTool {
	return &SharedMemoryTool{}
}

// SetMemory sets the shared memory instance.
func (t *SharedMemoryTool) SetMemory(memory SharedMemoryAccess) {
	t.memory = memory
}

// SetAgentID sets the agent ID for tracking writes.
func (t *SharedMemoryTool) SetAgentID(id string) {
	t.agentID = id
}

func (t *SharedMemoryTool) Name() string        { return "shared_memory" }
func (t *SharedMemoryTool) Description() string { return "Read, write, list, or delete entries in shared inter-agent memory." }

func (t *SharedMemoryTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"action": {
					Type:        genai.TypeString,
					Description: "Action to perform: read, write, list, delete",
					Enum:        []string{"read", "write", "list", "delete"},
				},
				"key": {
					Type:        genai.TypeString,
					Description: "The key to read, write, or delete",
				},
				"value": {
					Type:        genai.TypeString,
					Description: "The value to write (for write action)",
				},
				"entry_type": {
					Type:        genai.TypeString,
					Description: "Type of entry: fact, insight, file_state, decision",
					Enum:        []string{"fact", "insight", "file_state", "decision"},
				},
				"filter_type": {
					Type:        genai.TypeString,
					Description: "Filter entries by type when listing (optional)",
				},
			},
			Required: []string{"action"},
		},
	}
}

func (t *SharedMemoryTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	if t.memory == nil {
		return sdk.NewErrorResult("shared_memory: no memory instance configured"), nil
	}

	action, ok := sdk.GetString(args, "action")
	if !ok {
		return sdk.NewErrorResult("action is required"), nil
	}

	switch action {
	case "read":
		return t.executeRead(args)
	case "write":
		return t.executeWrite(args)
	case "list":
		return t.executeList(args)
	case "delete":
		return t.executeDelete(args)
	default:
		return sdk.NewErrorResult(fmt.Sprintf("unknown action: %s (use read, write, list, delete)", action)), nil
	}
}

func (t *SharedMemoryTool) executeRead(args map[string]any) (*sdk.ToolResult, error) {
	key, ok := sdk.GetString(args, "key")
	if !ok || key == "" {
		return sdk.NewErrorResult("key is required for read"), nil
	}

	entry, found := t.memory.ReadEntry(key)
	if !found {
		return sdk.NewErrorResult(fmt.Sprintf("key not found: %s", key)), nil
	}

	return sdk.NewSuccessResult(fmt.Sprintf("[%s] %s = %v (from: %s, at: %s)",
		entry.Type, entry.Key, entry.Value, entry.Source, entry.Timestamp.Format("15:04:05"))), nil
}

func (t *SharedMemoryTool) executeWrite(args map[string]any) (*sdk.ToolResult, error) {
	key, ok := sdk.GetString(args, "key")
	if !ok || key == "" {
		return sdk.NewErrorResult("key is required for write"), nil
	}

	value, ok := sdk.GetString(args, "value")
	if !ok || value == "" {
		return sdk.NewErrorResult("value is required for write"), nil
	}

	entryType := sdk.SharedEntryType(sdk.GetStringDefault(args, "entry_type", "fact"))

	t.memory.Write(key, value, entryType, t.agentID)
	return sdk.NewSuccessResult(fmt.Sprintf("wrote %s = %s (type: %s)", key, value, entryType)), nil
}

func (t *SharedMemoryTool) executeList(args map[string]any) (*sdk.ToolResult, error) {
	filterType, hasFilter := sdk.GetString(args, "filter_type")

	var entries []*sdk.SharedEntry
	if hasFilter && filterType != "" {
		entries = t.memory.ReadByType(sdk.SharedEntryType(filterType))
	} else {
		entries = t.memory.ReadAll()
	}

	if len(entries) == 0 {
		return sdk.NewSuccessResult("(no entries)"), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Shared memory entries (%d):\n", len(entries)))
	for _, entry := range entries {
		sb.WriteString(fmt.Sprintf("- [%s] %s = %v (from: %s)\n",
			entry.Type, entry.Key, entry.Value, entry.Source))
	}
	return sdk.NewSuccessResult(sb.String()), nil
}

func (t *SharedMemoryTool) executeDelete(args map[string]any) (*sdk.ToolResult, error) {
	key, ok := sdk.GetString(args, "key")
	if !ok || key == "" {
		return sdk.NewErrorResult("key is required for delete"), nil
	}

	t.memory.Delete(key)
	return sdk.NewSuccessResult(fmt.Sprintf("deleted key: %s", key)), nil
}
