package sdk

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Runner manages multiple agents and their lifecycle.
type Runner struct {
	client   Client
	registry *Registry
	memory   *SharedMemory
	config   RunnerConfig

	// Subsystems propagated to spawned agents
	reflector  *Reflector
	delegation *DelegationStrategy

	agents  map[string]*runnerAgent
	results map[string]*AgentResult
	mu      sync.RWMutex
}

type runnerAgent struct {
	id     string
	task   AgentTask
	agent  *Agent
	status AgentStatus
	cancel context.CancelFunc
}

// RunnerConfig configures the Runner.
type RunnerConfig struct {
	// OnAgentStart is called when an agent begins executing.
	OnAgentStart func(agentID string, task AgentTask)

	// OnAgentComplete is called when an agent finishes.
	OnAgentComplete func(agentID string, result *AgentResult)

	// OnAgentProgress is called with text output from agents.
	OnAgentProgress func(agentID string, text string)

	// DefaultMaxTurns is the default max turns for spawned agents.
	DefaultMaxTurns int

	// DefaultTimeout is the default timeout for spawned agents.
	DefaultTimeout time.Duration

	// SystemPrompt is the default system prompt for spawned agents.
	SystemPrompt string

	// MaxAgents limits concurrent running agents (0 = unlimited).
	MaxAgents int
}

// NewRunner creates a new multi-agent runner.
func NewRunner(client Client, registry *Registry, opts ...RunnerOption) *Runner {
	r := &Runner{
		client:   client,
		registry: registry,
		memory:   NewSharedMemory(),
		config: RunnerConfig{
			DefaultMaxTurns: 30,
			DefaultTimeout:  10 * time.Minute,
		},
		agents:  make(map[string]*runnerAgent),
		results: make(map[string]*AgentResult),
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Spawn starts a new agent synchronously and returns the result.
func (r *Runner) Spawn(ctx context.Context, task AgentTask) (string, *AgentResult, error) {
	id := generateID()
	agent, _ := r.createAgent(id, task)

	ra := &runnerAgent{
		id:     id,
		task:   task,
		agent:  agent,
		status: AgentStatusRunning,
	}

	r.mu.Lock()
	r.agents[id] = ra
	r.mu.Unlock()

	if r.config.OnAgentStart != nil {
		r.config.OnAgentStart(id, task)
	}

	result, err := agent.Run(ctx, task.Prompt)
	r.mu.Lock()
	if err != nil {
		ra.status = AgentStatusFailed
		result = &AgentResult{Error: err}
	} else {
		ra.status = AgentStatusCompleted
	}
	r.results[id] = result
	r.mu.Unlock()

	if r.config.OnAgentComplete != nil {
		r.config.OnAgentComplete(id, result)
	}

	r.cleanupOld()
	return id, result, err
}

// SpawnAsync starts a new agent asynchronously and returns its ID.
func (r *Runner) SpawnAsync(ctx context.Context, task AgentTask) (string, error) {
	// Check agent limit
	if r.config.MaxAgents > 0 {
		r.mu.RLock()
		running := 0
		for _, a := range r.agents {
			if a.status == AgentStatusRunning {
				running++
			}
		}
		r.mu.RUnlock()

		if running >= r.config.MaxAgents {
			return "", fmt.Errorf("maximum concurrent agents reached (%d)", r.config.MaxAgents)
		}
	}

	id := generateID()
	agent, _ := r.createAgent(id, task)

	agentCtx, cancel := context.WithCancel(ctx)

	ra := &runnerAgent{
		id:     id,
		task:   task,
		agent:  agent,
		status: AgentStatusRunning,
		cancel: cancel,
	}

	r.mu.Lock()
	r.agents[id] = ra
	r.mu.Unlock()

	if r.config.OnAgentStart != nil {
		r.config.OnAgentStart(id, task)
	}

	go func() {
		defer cancel()
		result, err := agent.Run(agentCtx, task.Prompt)

		r.mu.Lock()
		if err != nil {
			ra.status = AgentStatusFailed
			if result == nil {
				result = &AgentResult{Error: err}
			}
		} else {
			ra.status = AgentStatusCompleted
		}
		r.results[id] = result
		r.mu.Unlock()

		if r.config.OnAgentComplete != nil {
			r.config.OnAgentComplete(id, result)
		}

		r.cleanupOld()
	}()

	return id, nil
}

// Wait blocks until the specified agent completes and returns its result.
func (r *Runner) Wait(ctx context.Context, agentID string) (*AgentResult, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		r.mu.RLock()
		result, ok := r.results[agentID]
		r.mu.RUnlock()

		if ok {
			return result, nil
		}

		// Check if agent exists
		r.mu.RLock()
		_, exists := r.agents[agentID]
		r.mu.RUnlock()

		if !exists {
			return nil, fmt.Errorf("agent not found: %s", agentID)
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// WaitAll blocks until all running agents complete and returns all results.
func (r *Runner) WaitAll(ctx context.Context) map[string]*AgentResult {
	for {
		select {
		case <-ctx.Done():
			return r.getAllResults()
		default:
		}

		if r.allDone() {
			return r.getAllResults()
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// GetResult returns the result for a specific agent.
func (r *Runner) GetResult(agentID string) (*AgentResult, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result, ok := r.results[agentID]
	return result, ok
}

// ListRunning returns IDs of currently running agents.
func (r *Runner) ListRunning() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var ids []string
	for id, a := range r.agents {
		if a.status == AgentStatusRunning {
			ids = append(ids, id)
		}
	}
	return ids
}

// Cancel cancels a running agent.
func (r *Runner) Cancel(agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	a, ok := r.agents[agentID]
	if !ok {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	if a.cancel != nil {
		a.cancel()
	}
	a.status = AgentStatusCancelled
	return nil
}

// Memory returns the shared memory instance.
func (r *Runner) Memory() *SharedMemory {
	return r.memory
}

func (r *Runner) createAgent(id string, task AgentTask) (*Agent, *Registry) {
	maxTurns := task.MaxTurns
	if maxTurns <= 0 {
		maxTurns = r.config.DefaultMaxTurns
	}

	timeout := r.config.DefaultTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}

	// Create a filtered registry based on agent type
	registry := r.filterRegistry(task.Type)

	prompt := r.config.SystemPrompt
	if prompt == "" {
		prompt = fmt.Sprintf("You are a %s agent. Complete the assigned task using available tools.", task.Type)
	}

	opts := []AgentOption{
		WithSystemPrompt(prompt),
		WithMaxTurns(maxTurns),
		WithAgentTimeout(timeout),
		WithMemory(r.memory),
	}

	// Propagate reflector if configured
	if r.reflector != nil {
		opts = append(opts, WithReflector(r.reflector))
	}

	// Propagate delegation if configured (pass self as runner)
	if r.delegation != nil {
		opts = append(opts, WithDelegation(r.delegation, r))
	}

	if r.config.OnAgentProgress != nil {
		agentID := id
		opts = append(opts, WithOnText(func(text string) {
			r.config.OnAgentProgress(agentID, text)
		}))
	}

	agentClient := r.client.Clone()
	agent := NewAgent(id, agentClient, registry, opts...)
	return agent, registry
}

func (r *Runner) filterRegistry(agentType AgentType) *Registry {
	allowed := agentType.AllowedTools()
	if allowed == nil {
		return r.registry // all tools
	}

	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}

	filtered := NewRegistry()
	for _, tool := range r.registry.List() {
		if allowedSet[tool.Name()] {
			filtered.Register(tool)
		}
	}
	return filtered
}

func (r *Runner) allDone() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, a := range r.agents {
		if a.status == AgentStatusRunning {
			return false
		}
	}
	return true
}

func (r *Runner) getAllResults() map[string]*AgentResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	results := make(map[string]*AgentResult, len(r.results))
	for k, v := range r.results {
		results[k] = v
	}
	return results
}

const maxRunnerResults = 100

func (r *Runner) cleanupOld() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.results) <= maxRunnerResults {
		return
	}

	// Remove oldest completed results
	for id, a := range r.agents {
		if len(r.results) <= maxRunnerResults {
			break
		}
		if a.status == AgentStatusCompleted || a.status == AgentStatusFailed {
			delete(r.results, id)
			delete(r.agents, id)
		}
	}
}
