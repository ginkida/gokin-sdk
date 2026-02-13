package sdk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
)

// AgentConfig holds the agent's configuration.
type AgentConfig struct {
	SystemPrompt string
	MaxTurns     int
	Timeout      time.Duration
	OnText       func(text string)
	OnToolCall   func(name string, args map[string]any)
	Memory       *SharedMemory
}

// AgentProgress tracks agent execution progress.
type AgentProgress struct {
	AgentID            string
	AgentType          AgentType
	CurrentStep        int
	TotalSteps         int
	CurrentAction      string
	StartTime          time.Time
	Elapsed            time.Duration
	EstimatedRemaining time.Duration
	ToolsUsed          []string
	Status             AgentStatus
}

// AgentResult represents the result of an agent's execution.
type AgentResult struct {
	Text     string
	Turns    int
	Duration time.Duration
	Error    error
}

// Agent represents an AI agent that can use tools to accomplish tasks.
type Agent struct {
	name     string
	client   Client
	executor *Executor
	registry *Registry
	config   AgentConfig

	// Reflection: error analysis and recovery
	reflector *Reflector

	// Delegation: auto-delegate when stuck
	delegation *DelegationStrategy
	runner     *Runner // needed for delegation.Execute()

	// Progress tracking
	progress   AgentProgress
	progressMu sync.Mutex
	onProgress func(AgentProgress)

	// Enhanced loop detection
	callHistory   map[string]int
	callHistoryMu sync.Mutex
	loopIntervened bool
	broadHistory  map[string]int // tool name only (no args)

	// Context injection
	pinnedContext string

	// Scratchpad
	scratchpad string

	// Tools tracking
	toolsUsed []string
	toolsMu   sync.Mutex

	// Plan execution
	planner          *Planner
	activePlan       *PlanLifecycle
	planningMode     bool
	onPlanApproved   func(summary string)
}

// NewAgent creates a new agent with the given name, client, and tool registry.
func NewAgent(name string, client Client, registry *Registry, opts ...AgentOption) *Agent {
	if client == nil {
		panic("sdk.NewAgent: client must not be nil")
	}
	if registry == nil {
		panic("sdk.NewAgent: registry must not be nil")
	}

	a := &Agent{
		name:   name,
		client: client,
		config: AgentConfig{
			MaxTurns: 30,
			Timeout:  10 * time.Minute,
		},
		callHistory:  make(map[string]int),
		broadHistory: make(map[string]int),
	}
	a.executor = NewExecutor(registry)
	a.registry = registry

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// buildSystemPrompt constructs the full system prompt including pinned context and memory.
func (a *Agent) buildSystemPrompt() string {
	var parts []string

	if a.config.SystemPrompt != "" {
		parts = append(parts, a.config.SystemPrompt)
	}

	if a.pinnedContext != "" {
		parts = append(parts, "\n--- Pinned Context ---\n"+a.pinnedContext)
	}

	if a.config.Memory != nil {
		if memCtx := a.config.Memory.GetForContext(a.name, 50); memCtx != "" {
			parts = append(parts, "\n--- Shared Memory ---\n"+memCtx)
		}
	}

	return strings.Join(parts, "\n")
}

// Run executes the agent with the given message and returns the result.
func (a *Agent) Run(ctx context.Context, message string) (*AgentResult, error) {
	start := time.Now()

	// Configure client for this agent (deferred from NewAgent so each
	// cloned client gets its own tools/systemInstruction).
	if systemPrompt := a.buildSystemPrompt(); systemPrompt != "" {
		a.client.SetSystemInstruction(systemPrompt)
	}
	if a.registry != nil {
		a.client.SetTools(a.registry.GeminiTools())
	}

	// Initialize progress
	a.initProgress(start)

	// Plan-driven execution if planner is configured
	if a.planner != nil {
		return a.runWithPlan(ctx, message)
	}

	// Apply overall timeout
	if a.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.config.Timeout)
		defer cancel()
	}

	history := make([]*genai.Content, 0)
	turns := 0
	maxTurns := a.config.MaxTurns
	bonusTurns := 0
	stuckCount := 0
	lastToolError := ""
	lastToolName := ""

	// Initial message
	stream, err := a.client.SendMessageWithHistory(ctx, history, message)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	// Add user message to history
	history = append(history, genai.NewContentFromText(message, "user"))

	for turns < maxTurns+bonusTurns {
		turns++

		// Update progress
		a.updateProgress(turns, maxTurns+bonusTurns, lastToolName, start)

		// Collect the response
		resp, err := stream.Collect(ctx)
		if err != nil {
			return &AgentResult{
				Turns:    turns,
				Duration: time.Since(start),
				Error:    err,
			}, err
		}

		// Stream text to callback
		if resp.Text != "" && a.config.OnText != nil {
			a.config.OnText(resp.Text)
		}

		// Build model content for history
		modelContent := buildModelContent(resp)
		history = append(history, modelContent)

		// If no function calls, we're done
		if len(resp.FunctionCalls) == 0 {
			a.setProgressStatus(AgentStatusCompleted)
			return &AgentResult{
				Text:     resp.Text,
				Turns:    turns,
				Duration: time.Since(start),
			}, nil
		}

		// Dual-layer loop detection
		loopDetected := false
		for _, fc := range resp.FunctionCalls {
			// Track exact call (tool + args)
			exactKey := normalizeCallKey(fc.Name, fc.Args)
			a.callHistoryMu.Lock()
			a.callHistory[exactKey]++
			exactCount := a.callHistory[exactKey]
			a.callHistoryMu.Unlock()

			// Track broad call (tool only)
			a.callHistoryMu.Lock()
			a.broadHistory[fc.Name]++
			broadCount := a.broadHistory[fc.Name]
			a.callHistoryMu.Unlock()

			// Layer 1: Exact-match loop (same tool + same args 3+ times)
			if exactCount >= 3 {
				intervention := buildLoopRecoveryIntervention(fc.Name, exactCount)
				interventionContent := genai.NewContentFromText(intervention, "user")
				history = append(history, interventionContent)

				// Reset exact count
				a.callHistoryMu.Lock()
				a.callHistory[exactKey] = 0
				a.callHistoryMu.Unlock()

				a.loopIntervened = true
				loopDetected = true

				// Grant bonus turns for recovery
				if bonusTurns < 3 {
					bonusTurns++
				}
				break
			}

			// Layer 2: Broad loop (same tool 8+ times with any args)
			if broadCount >= 8 {
				intervention := fmt.Sprintf(
					"BROAD LOOP DETECTED: Tool '%s' has been called %d times total. "+
						"You must try a completely different approach. Use a different tool "+
						"or report your findings so far.",
					fc.Name, broadCount,
				)
				interventionContent := genai.NewContentFromText(intervention, "user")
				history = append(history, interventionContent)

				// Reset broad count
				a.callHistoryMu.Lock()
				a.broadHistory[fc.Name] = 0
				a.callHistoryMu.Unlock()

				a.loopIntervened = true
				loopDetected = true
				break
			}

			// Notify callback
			if a.config.OnToolCall != nil {
				a.config.OnToolCall(fc.Name, fc.Args)
			}
		}

		if loopDetected {
			// Get new response after intervention
			stream, err = a.client.SendMessageWithHistory(ctx, history, "")
			if err != nil {
				return &AgentResult{
					Turns:    turns,
					Duration: time.Since(start),
					Error:    err,
				}, err
			}
			continue
		}

		// Reset loopIntervened when a turn passes without a loop
		if a.loopIntervened {
			a.loopIntervened = false
		}

		// Execute tools
		results, err := a.executor.Execute(ctx, resp.FunctionCalls)
		if err != nil {
			return &AgentResult{
				Turns:    turns,
				Duration: time.Since(start),
				Error:    err,
			}, err
		}

		// Track tool usage
		for _, fc := range resp.FunctionCalls {
			a.trackToolUsed(fc.Name)
		}

		// Check for tool errors and run reflection
		toolHadError := false
		for _, funcResp := range results {
			if errMsg := extractErrorFromFuncResponse(funcResp); errMsg != "" {
				toolHadError = true
				lastToolError = errMsg
				lastToolName = funcResp.Name
				stuckCount++

				// Reflection integration
				if a.reflector != nil {
					args := map[string]any{}
					for _, fc := range resp.FunctionCalls {
						if fc.Name == funcResp.Name {
							args = fc.Args
							break
						}
					}
					reflection := a.reflector.Analyze(ctx, funcResp.Name, args, errMsg)
					if reflection.Matched {
						intervention := a.reflector.BuildIntervention(funcResp.Name, args, reflection, errMsg)
						interventionContent := genai.NewContentFromText(intervention, "user")
						history = append(history, interventionContent)
					}
				}
			}
		}

		if !toolHadError {
			stuckCount = 0
			lastToolError = ""
		}

		// Add function results to history
		funcResultParts := make([]*genai.Part, len(results))
		for j, result := range results {
			funcResultParts[j] = genai.NewPartFromFunctionResponse(result.Name, result.Response)
			funcResultParts[j].FunctionResponse.ID = result.ID
		}
		funcResultContent := &genai.Content{
			Role:  "user",
			Parts: funcResultParts,
		}
		history = append(history, funcResultContent)

		// Delegation check: if stuck for too long, try delegating
		if a.delegation != nil && a.runner != nil && stuckCount >= 3 {
			delCtx := DelegationContext{
				CurrentTurn:  turns,
				LastToolName: lastToolName,
				LastToolError: lastToolError,
				StuckCount:   stuckCount,
			}
			decision := a.delegation.Evaluate(delCtx)
			if decision.ShouldDelegate {
				delegationStart := time.Now()
				delegationResult, delegationErr := a.delegation.Execute(ctx, a.runner, decision)
				delegationDuration := time.Since(delegationStart)
				success := delegationErr == nil && delegationResult != nil && delegationResult.Error == nil

				a.delegation.RecordOutcome("", decision.TargetType, decision.Reason, success, delegationDuration, "")

				if success && delegationResult.Text != "" {
					// Inject delegation result into history
					delegationMsg := fmt.Sprintf(
						"A %s agent was delegated to help. Their findings:\n%s",
						decision.TargetType, delegationResult.Text,
					)
					history = append(history, genai.NewContentFromText(delegationMsg, "user"))
					stuckCount = 0
				}
			}
		}

		// Send function responses back to the model
		stream, err = a.client.SendFunctionResponse(ctx, history, results)
		if err != nil {
			return &AgentResult{
				Turns:    turns,
				Duration: time.Since(start),
				Error:    err,
			}, err
		}
	}

	a.setProgressStatus(AgentStatusFailed)
	return &AgentResult{
		Text:     "Max turns reached",
		Turns:    turns,
		Duration: time.Since(start),
		Error:    fmt.Errorf("agent reached maximum turn limit (%d)", a.config.MaxTurns),
	}, nil
}

// --- Plan execution ---

// runWithPlan executes the agent using plan-driven mode.
// It builds a plan tree, iterates through ready nodes, and handles replanning on failure.
func (a *Agent) runWithPlan(ctx context.Context, message string) (*AgentResult, error) {
	start := time.Now()

	// Apply overall timeout
	if a.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.config.Timeout)
		defer cancel()
	}

	// Build plan
	tree, err := a.planner.BuildPlan(ctx, PlanGoal{Description: message})
	if err != nil {
		a.setProgressStatus(AgentStatusFailed)
		return nil, fmt.Errorf("failed to build plan: %w", err)
	}

	// Create lifecycle and transition: draft → approved → executing
	lifecycle := NewPlanLifecycle(generateID(), tree)
	a.activePlan = lifecycle

	if err := lifecycle.TransitionTo(PlanStateApproved); err != nil {
		return nil, fmt.Errorf("plan approval failed: %w", err)
	}

	if a.onPlanApproved != nil {
		a.onPlanApproved(a.planner.Summary(tree))
	}

	if err := lifecycle.TransitionTo(PlanStateExecuting); err != nil {
		return nil, fmt.Errorf("plan execution start failed: %w", err)
	}

	const maxReplans = 3
	replans := 0
	var outputs []string

	for {
		readyNodes := a.planner.GetReadyNodes(tree)
		if len(readyNodes) == 0 {
			break
		}

		replanNeeded := false
		for _, node := range readyNodes {
			node.Status = PlanNodeRunning

			result := a.executeNode(ctx, node)
			a.planner.RecordResult(tree, node.ID, result)

			if result.Success {
				if result.Output != "" {
					outputs = append(outputs, result.Output)
				}
				continue
			}

			// Node failed — attempt reflection and replan
			if a.reflector != nil {
				reflection := a.reflector.Analyze(ctx, "plan_node", nil, result.Error)
				if reflection.Matched {
					outputs = append(outputs, fmt.Sprintf("[Recovery: %s]", reflection.Suggestion))
				}
			}

			if replans < maxReplans {
				if err := lifecycle.TransitionTo(PlanStateFailed); err != nil {
					break // can't transition — skip replan
				}
				_ = lifecycle.RequestReplan(result.Error)
				_ = lifecycle.TransitionTo(PlanStateApproved)
				_ = lifecycle.TransitionTo(PlanStateExecuting)

				newTree, rerr := a.planner.BuildPlan(ctx, PlanGoal{
					Description: message + "\nPrevious error: " + result.Error,
				})
				if rerr == nil {
					tree = newTree
					lifecycle.Tree = newTree
				}
				replans++
				replanNeeded = true
				break // restart loop with fresh ready nodes
			}

			// Exhausted replans
			a.setProgressStatus(AgentStatusFailed)
			_ = lifecycle.TransitionTo(PlanStateFailed)
			return &AgentResult{
				Text:     strings.Join(outputs, "\n"),
				Turns:    replans,
				Duration: time.Since(start),
				Error:    fmt.Errorf("plan node %s failed after %d replans: %s", node.ID, maxReplans, result.Error),
			}, nil
		}

		if !replanNeeded {
			// No replan triggered — check if we consumed all ready nodes
			// Next iteration will re-fetch ready nodes (newly unblocked children)
			nextReady := a.planner.GetReadyNodes(tree)
			if len(nextReady) == 0 {
				break
			}
		}
	}

	_ = lifecycle.TransitionTo(PlanStateCompleted)
	a.setProgressStatus(AgentStatusCompleted)

	return &AgentResult{
		Text:     strings.Join(outputs, "\n"),
		Turns:    replans,
		Duration: time.Since(start),
	}, nil
}

// executeNode runs a single plan node and returns its result.
func (a *Agent) executeNode(ctx context.Context, node *PlanNode) *PlanResult {
	if node.Action == nil {
		return &PlanResult{Error: "no action defined", Success: false}
	}

	// Delegation path: spawn a sub-agent via runner
	if node.Action.Type == ActionDelegate && a.runner != nil {
		task := AgentTask{
			Prompt: node.Action.Prompt,
			Type:   node.Action.AgentType,
		}
		_, result, err := a.runner.Spawn(ctx, task)
		if err != nil {
			return &PlanResult{Error: err.Error(), Success: false}
		}
		if result.Error != nil {
			return &PlanResult{Output: result.Text, Error: result.Error.Error(), Success: false}
		}
		return &PlanResult{Output: result.Text, Success: true}
	}

	// Direct execution: send prompt to LLM and handle tool calls
	stream, err := a.client.SendMessage(ctx, node.Action.Prompt)
	if err != nil {
		return &PlanResult{Error: err.Error(), Success: false}
	}

	resp, err := stream.Collect(ctx)
	if err != nil {
		return &PlanResult{Error: err.Error(), Success: false}
	}

	// If no function calls, return text response
	if len(resp.FunctionCalls) == 0 {
		return &PlanResult{Output: resp.Text, Success: true}
	}

	// Execute function calls
	results, err := a.executor.Execute(ctx, resp.FunctionCalls)
	if err != nil {
		return &PlanResult{Error: err.Error(), Success: false}
	}

	// Check results for errors
	var toolOutputs []string
	for _, fr := range results {
		if errMsg := extractErrorFromFuncResponse(fr); errMsg != "" {
			return &PlanResult{Error: errMsg, Success: false}
		}
		if fr.Response != nil {
			if out, ok := fr.Response["output"]; ok {
				toolOutputs = append(toolOutputs, fmt.Sprintf("%v", out))
			}
		}
	}

	output := resp.Text
	if len(toolOutputs) > 0 {
		if output != "" {
			output += "\n"
		}
		output += strings.Join(toolOutputs, "\n")
	}

	return &PlanResult{Output: output, Success: true}
}

// --- Progress tracking ---

func (a *Agent) initProgress(start time.Time) {
	a.progressMu.Lock()
	defer a.progressMu.Unlock()
	a.progress = AgentProgress{
		AgentID:   a.name,
		StartTime: start,
		Status:    AgentStatusRunning,
	}
}

func (a *Agent) updateProgress(turn, totalTurns int, currentAction string, start time.Time) {
	a.progressMu.Lock()
	defer a.progressMu.Unlock()

	a.progress.CurrentStep = turn
	a.progress.TotalSteps = totalTurns
	a.progress.CurrentAction = currentAction
	a.progress.Elapsed = time.Since(start)

	if turn > 1 {
		avgPerTurn := a.progress.Elapsed / time.Duration(turn)
		remaining := totalTurns - turn
		a.progress.EstimatedRemaining = avgPerTurn * time.Duration(remaining)
	}

	a.toolsMu.Lock()
	a.progress.ToolsUsed = make([]string, len(a.toolsUsed))
	copy(a.progress.ToolsUsed, a.toolsUsed)
	a.toolsMu.Unlock()

	if a.onProgress != nil {
		a.onProgress(a.progress)
	}
}

func (a *Agent) setProgressStatus(status AgentStatus) {
	a.progressMu.Lock()
	defer a.progressMu.Unlock()
	a.progress.Status = status
	if a.onProgress != nil {
		a.onProgress(a.progress)
	}
}

func (a *Agent) trackToolUsed(name string) {
	a.toolsMu.Lock()
	defer a.toolsMu.Unlock()
	a.toolsUsed = append(a.toolsUsed, name)
}

// GetProgress returns the current agent progress (thread-safe).
func (a *Agent) GetProgress() AgentProgress {
	a.progressMu.Lock()
	defer a.progressMu.Unlock()
	return a.progress
}

// SetScratchpad updates the agent's scratchpad.
func (a *Agent) SetScratchpad(content string) {
	a.scratchpad = content
}

// GetScratchpad returns the agent's scratchpad contents.
func (a *Agent) GetScratchpad() string {
	return a.scratchpad
}

// --- Loop detection helpers ---

// normalizeCallKey creates a stable key by filtering zero-value arguments.
func normalizeCallKey(name string, args map[string]any) string {
	if len(args) == 0 {
		return name + ":{}"
	}

	var parts []string
	for k, v := range args {
		// Skip zero-value args for stable keys
		if v == nil || v == "" || v == 0 || v == false {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}

	return fmt.Sprintf("%s:{%s}", name, strings.Join(parts, ","))
}

// buildLoopRecoveryIntervention generates tool-specific suggestions.
func buildLoopRecoveryIntervention(toolName string, count int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"LOOP DETECTED: Tool '%s' called %d times with same arguments. "+
			"You MUST try a different approach.\n\n",
		toolName, count,
	))

	sb.WriteString("Suggestions based on tool:\n")

	switch toolName {
	case "read":
		sb.WriteString("- Use 'glob' to find the correct file path\n")
		sb.WriteString("- Use 'grep' to search for content instead\n")
		sb.WriteString("- Check if the file exists with 'list_dir'\n")
	case "grep":
		sb.WriteString("- Use 'glob' to find files by name pattern\n")
		sb.WriteString("- Try a different search pattern\n")
		sb.WriteString("- Broaden or narrow your search scope\n")
	case "edit":
		sb.WriteString("- Use 'read' to verify the current file content\n")
		sb.WriteString("- Check if the old_string matches exactly\n")
		sb.WriteString("- Try 'write' to replace the entire file\n")
	case "write":
		sb.WriteString("- Use 'read' to check the current content\n")
		sb.WriteString("- Verify the file path is correct\n")
		sb.WriteString("- Try 'edit' for targeted changes\n")
	case "bash":
		sb.WriteString("- Check if the command exists and is installed\n")
		sb.WriteString("- Try a different command or approach\n")
		sb.WriteString("- Verify working directory and permissions\n")
	case "glob":
		sb.WriteString("- Try 'grep' to search file contents instead\n")
		sb.WriteString("- Use 'list_dir' for directory listings\n")
		sb.WriteString("- Broaden your glob pattern\n")
	default:
		sb.WriteString("- Use a different tool entirely\n")
		sb.WriteString("- Change your arguments\n")
		sb.WriteString("- Report what you've found so far\n")
	}

	return sb.String()
}

// extractErrorFromFuncResponse checks if a function response indicates an error.
func extractErrorFromFuncResponse(resp *genai.FunctionResponse) string {
	if resp.Response == nil {
		return ""
	}

	// Check for explicit error field
	if errVal, ok := resp.Response["error"]; ok {
		if errStr, ok := errVal.(string); ok && errStr != "" {
			return errStr
		}
	}

	// Check for success=false
	if success, ok := resp.Response["success"]; ok {
		if b, ok := success.(bool); ok && !b {
			if output, ok := resp.Response["output"]; ok {
				if s, ok := output.(string); ok {
					return s
				}
			}
			return "tool returned success=false"
		}
	}

	return ""
}

// buildModelContent creates a Content from a Response for history.
func buildModelContent(resp *Response) *genai.Content {
	if len(resp.Parts) > 0 {
		return &genai.Content{
			Role:  "model",
			Parts: resp.Parts,
		}
	}

	var parts []*genai.Part
	if resp.Text != "" {
		parts = append(parts, genai.NewPartFromText(resp.Text))
	}
	for _, fc := range resp.FunctionCalls {
		parts = append(parts, &genai.Part{FunctionCall: fc})
	}
	if len(parts) == 0 {
		parts = append(parts, genai.NewPartFromText(" "))
	}

	return &genai.Content{
		Role:  "model",
		Parts: parts,
	}
}

// generateID generates a short random hex ID.
func generateID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
