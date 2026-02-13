package sdk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/genai"
)

// AgentCheckpoint captures a full snapshot of agent state for save/restore.
type AgentCheckpoint struct {
	ID            string                    `json:"id"`
	AgentState    *SerializedAgentState     `json:"agent_state"`
	SharedMemory  map[string]*SharedEntry   `json:"shared_memory,omitempty"`
	PlanState     *PlanTree                 `json:"plan_state,omitempty"`
	Timestamp     time.Time                 `json:"timestamp"`
	TriggerReason string                    `json:"trigger_reason"`
	TurnNumber    int                       `json:"turn_number"`
}

// SerializedAgentState holds the serializable parts of agent execution state.
type SerializedAgentState struct {
	History   []SerializedContent `json:"history"`
	MaxTurns  int                 `json:"max_turns"`
	TurnCount int                 `json:"turn_count"`
	ToolsUsed []string            `json:"tools_used,omitempty"`
	Scratchpad string             `json:"scratchpad,omitempty"`
}

// CheckpointConfig configures automatic checkpointing behavior.
type CheckpointConfig struct {
	// Enabled controls whether auto-checkpointing is active.
	Enabled bool

	// Interval is the number of turns between auto-checkpoints.
	Interval int

	// Directory is where checkpoint files are saved.
	Directory string

	// MaxCheckpoints limits the number of retained checkpoints per agent.
	MaxCheckpoints int
}

// DefaultCheckpointConfig returns sensible defaults for checkpointing.
func DefaultCheckpointConfig() CheckpointConfig {
	return CheckpointConfig{
		Enabled:        false,
		Interval:       5,
		Directory:      "",
		MaxCheckpoints: 10,
	}
}

// SaveAgentCheckpoint creates a checkpoint from the current agent state.
func SaveAgentCheckpoint(
	agentName string,
	history []*genai.Content,
	turnCount int,
	maxTurns int,
	toolsUsed []string,
	scratchpad string,
	memory *SharedMemory,
	planTree *PlanTree,
	reason string,
	dir string,
) (*AgentCheckpoint, error) {
	cp := &AgentCheckpoint{
		ID: fmt.Sprintf("%s-%s", agentName, generateID()),
		AgentState: &SerializedAgentState{
			History:    SerializeHistory(history),
			MaxTurns:   maxTurns,
			TurnCount:  turnCount,
			ToolsUsed:  toolsUsed,
			Scratchpad: scratchpad,
		},
		PlanState:     planTree,
		Timestamp:     time.Now(),
		TriggerReason: reason,
		TurnNumber:    turnCount,
	}

	// Snapshot shared memory
	if memory != nil {
		entries := memory.ReadAll()
		cp.SharedMemory = make(map[string]*SharedEntry, len(entries))
		for _, entry := range entries {
			cp.SharedMemory[entry.Key] = entry
		}
	}

	// Save to disk if directory specified
	if dir != "" {
		if err := saveCheckpointToDisk(cp, dir); err != nil {
			return cp, fmt.Errorf("failed to save checkpoint to disk: %w", err)
		}
	}

	return cp, nil
}

// RestoreFromAgentCheckpoint restores agent state from a checkpoint.
func RestoreFromAgentCheckpoint(cp *AgentCheckpoint) ([]*genai.Content, error) {
	if cp.AgentState == nil {
		return nil, fmt.Errorf("checkpoint has no agent state")
	}

	history, err := DeserializeHistory(cp.AgentState.History)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize history: %w", err)
	}

	return history, nil
}

// LoadAgentCheckpoint loads a checkpoint from disk.
func LoadAgentCheckpoint(path string) (*AgentCheckpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint: %w", err)
	}

	var cp AgentCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal checkpoint: %w", err)
	}

	return &cp, nil
}

// ListCheckpoints lists all checkpoint files in a directory.
func ListCheckpoints(dir string) ([]string, error) {
	pattern := filepath.Join(dir, "checkpoint-*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to list checkpoints: %w", err)
	}
	return matches, nil
}

// CleanupOldCheckpoints removes old checkpoints keeping only the most recent maxKeep.
func CleanupOldCheckpoints(dir string, maxKeep int) error {
	files, err := ListCheckpoints(dir)
	if err != nil {
		return err
	}

	if len(files) <= maxKeep {
		return nil
	}

	// Remove oldest (files are sorted by glob, oldest first)
	toRemove := files[:len(files)-maxKeep]
	for _, f := range toRemove {
		if err := os.Remove(f); err != nil {
			return fmt.Errorf("failed to remove old checkpoint %s: %w", f, err)
		}
	}

	return nil
}

// --- internal ---

func saveCheckpointToDisk(cp *AgentCheckpoint, dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	filename := fmt.Sprintf("checkpoint-%s.json", cp.ID)
	path := filepath.Join(dir, filename)

	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}
