package sdk

// AgentType defines the type of agent, which determines available tools.
type AgentType string

const (
	// AgentTypeGeneral has access to all tools.
	AgentTypeGeneral AgentType = "general"

	// AgentTypeExplore has access to read-only exploration tools.
	AgentTypeExplore AgentType = "explore"

	// AgentTypeBash has access to bash and basic file tools.
	AgentTypeBash AgentType = "bash"

	// AgentTypePlan has access to exploration and planning tools.
	AgentTypePlan AgentType = "plan"
)

// AllowedTools returns the tool names this agent type can use.
// Returns nil for general type, meaning all tools are allowed.
func (at AgentType) AllowedTools() []string {
	switch at {
	case AgentTypeExplore:
		return []string{"read", "glob", "grep", "tree", "list_dir"}
	case AgentTypeBash:
		return []string{"bash", "read", "glob"}
	case AgentTypePlan:
		return []string{"read", "glob", "grep", "tree", "list_dir", "bash"}
	case AgentTypeGeneral:
		return nil // all tools
	default:
		return nil
	}
}

// String returns the string representation of the agent type.
func (at AgentType) String() string {
	return string(at)
}

// ParseAgentType parses a string into an AgentType.
func ParseAgentType(s string) AgentType {
	switch s {
	case "explore":
		return AgentTypeExplore
	case "bash":
		return AgentTypeBash
	case "plan":
		return AgentTypePlan
	default:
		return AgentTypeGeneral
	}
}

// AgentStatus represents the current status of an agent.
type AgentStatus string

const (
	AgentStatusPending   AgentStatus = "pending"
	AgentStatusRunning   AgentStatus = "running"
	AgentStatusCompleted AgentStatus = "completed"
	AgentStatusFailed    AgentStatus = "failed"
	AgentStatusCancelled AgentStatus = "cancelled"
)

// AgentTask describes a task to be executed by an agent.
type AgentTask struct {
	// Prompt is the task instruction.
	Prompt string

	// Type determines which tools are available.
	Type AgentType

	// Background indicates the task should run asynchronously.
	Background bool

	// Description is a short human-readable description.
	Description string

	// MaxTurns overrides the default max turns (0 = use default).
	MaxTurns int
}
