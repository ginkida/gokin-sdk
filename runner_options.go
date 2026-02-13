package sdk

import "time"

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithRunnerSystemPrompt sets the default system prompt for spawned agents.
func WithRunnerSystemPrompt(prompt string) RunnerOption {
	return func(r *Runner) {
		r.config.SystemPrompt = prompt
	}
}

// WithRunnerMaxTurns sets the default max turns for spawned agents.
func WithRunnerMaxTurns(n int) RunnerOption {
	return func(r *Runner) {
		if n > 0 {
			r.config.DefaultMaxTurns = n
		}
	}
}

// WithRunnerTimeout sets the default timeout for spawned agents.
func WithRunnerTimeout(d time.Duration) RunnerOption {
	return func(r *Runner) {
		r.config.DefaultTimeout = d
	}
}

// WithMaxAgents sets the maximum number of concurrent agents.
func WithMaxAgents(n int) RunnerOption {
	return func(r *Runner) {
		r.config.MaxAgents = n
	}
}

// WithOnAgentStart sets a callback invoked when an agent starts.
func WithOnAgentStart(fn func(agentID string, task AgentTask)) RunnerOption {
	return func(r *Runner) {
		r.config.OnAgentStart = fn
	}
}

// WithOnAgentComplete sets a callback invoked when an agent completes.
func WithOnAgentComplete(fn func(agentID string, result *AgentResult)) RunnerOption {
	return func(r *Runner) {
		r.config.OnAgentComplete = fn
	}
}

// WithOnAgentProgress sets a callback invoked for agent text output.
func WithOnAgentProgress(fn func(agentID string, text string)) RunnerOption {
	return func(r *Runner) {
		r.config.OnAgentProgress = fn
	}
}

// WithSharedMemory sets a custom SharedMemory instance for the runner.
func WithSharedMemory(mem *SharedMemory) RunnerOption {
	return func(r *Runner) {
		r.memory = mem
	}
}

// WithRunnerReflector sets a reflector to propagate to spawned agents.
func WithRunnerReflector(ref *Reflector) RunnerOption {
	return func(r *Runner) {
		r.reflector = ref
	}
}

// WithRunnerDelegation sets a delegation strategy to propagate to spawned agents.
func WithRunnerDelegation(ds *DelegationStrategy) RunnerOption {
	return func(r *Runner) {
		r.delegation = ds
	}
}
