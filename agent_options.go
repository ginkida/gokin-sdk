package sdk

import "time"

// AgentOption configures an Agent.
type AgentOption func(*Agent)

// WithSystemPrompt sets the system prompt for the agent.
func WithSystemPrompt(prompt string) AgentOption {
	return func(a *Agent) {
		a.config.SystemPrompt = prompt
	}
}

// WithMaxTurns sets the maximum number of turns (LLM round-trips) the agent can take.
func WithMaxTurns(n int) AgentOption {
	return func(a *Agent) {
		if n > 0 {
			a.config.MaxTurns = n
		}
	}
}

// WithAgentTimeout sets the overall timeout for the agent's Run() execution.
func WithAgentTimeout(d time.Duration) AgentOption {
	return func(a *Agent) {
		a.config.Timeout = d
	}
}

// WithOnText sets a callback that is called when the agent produces text output.
func WithOnText(fn func(string)) AgentOption {
	return func(a *Agent) {
		a.config.OnText = fn
	}
}

// WithOnToolCall sets a callback that is called when the agent invokes a tool.
func WithOnToolCall(fn func(string, map[string]any)) AgentOption {
	return func(a *Agent) {
		a.config.OnToolCall = fn
	}
}

// WithMemory attaches a SharedMemory instance to the agent for inter-agent communication.
func WithMemory(mem *SharedMemory) AgentOption {
	return func(a *Agent) {
		a.config.Memory = mem
	}
}

// WithToolTimeout sets the per-tool execution timeout.
func WithToolTimeout(d time.Duration) AgentOption {
	return func(a *Agent) {
		a.executor.timeout = d
	}
}

// WithReflector attaches an error reflector for automatic error analysis and recovery.
func WithReflector(r *Reflector) AgentOption {
	return func(a *Agent) {
		a.reflector = r
	}
}

// WithDelegation attaches a delegation strategy and runner for auto-delegation when stuck.
func WithDelegation(ds *DelegationStrategy, runner *Runner) AgentOption {
	return func(a *Agent) {
		a.delegation = ds
		a.runner = runner
	}
}

// WithProgressCallback sets a callback invoked on each turn with progress updates.
func WithProgressCallback(fn func(AgentProgress)) AgentOption {
	return func(a *Agent) {
		a.onProgress = fn
	}
}

// WithPinnedContext injects additional context into the agent's system prompt.
func WithPinnedContext(ctx string) AgentOption {
	return func(a *Agent) {
		a.pinnedContext = ctx
	}
}

// WithScratchpad sets initial scratchpad content for the agent.
func WithScratchpad(initial string) AgentOption {
	return func(a *Agent) {
		a.scratchpad = initial
	}
}

// WithPlanner attaches a planner for plan-driven execution.
func WithPlanner(p *Planner) AgentOption {
	return func(a *Agent) {
		a.planner = p
	}
}

// WithPlanApprovalCallback sets a callback for plan approval notifications.
func WithPlanApprovalCallback(fn func(string)) AgentOption {
	return func(a *Agent) {
		a.onPlanApproved = fn
	}
}
