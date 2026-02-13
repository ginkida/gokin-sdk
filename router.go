package sdk

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// TaskType represents the type of task.
type TaskType string

const (
	TaskTypeQuestion    TaskType = "question"
	TaskTypeSingleTool  TaskType = "single_tool"
	TaskTypeMultiTool   TaskType = "multi_tool"
	TaskTypeExploration TaskType = "exploration"
	TaskTypeRefactoring TaskType = "refactoring"
	TaskTypeComplex     TaskType = "complex"
	TaskTypeBackground  TaskType = "background"
)

// ExecutionStrategy determines how to execute the task.
type ExecutionStrategy string

const (
	StrategyDirect     ExecutionStrategy = "direct"
	StrategySingleTool ExecutionStrategy = "single_tool"
	StrategyExecutor   ExecutionStrategy = "executor"
	StrategySubAgent   ExecutionStrategy = "sub_agent"
)

// TaskComplexity represents the complexity analysis of a task.
type TaskComplexity struct {
	Score     int               `json:"score"`
	Type      TaskType          `json:"type"`
	Strategy  ExecutionStrategy `json:"strategy"`
	Reasoning string            `json:"reasoning"`
}

// RouteDecision represents the routing decision for a task.
type RouteDecision struct {
	Analysis     *TaskComplexity   `json:"analysis"`
	Message      string            `json:"message"`
	Handler      string            `json:"handler"` // "direct", "executor", "sub_agent"
	SubAgentType string            `json:"sub_agent_type,omitempty"`
	Background   bool              `json:"background"`
	Reasoning    string            `json:"reasoning"`
	SuggestedModel string          `json:"suggested_model,omitempty"`
	ThinkingBudget int32           `json:"thinking_budget,omitempty"`
}

// routingRecord stores a routing decision and its outcome for learning.
type routingRecord struct {
	message   string
	taskType  TaskType
	strategy  ExecutionStrategy
	success   bool
	timestamp time.Time
}

// PlanChecker allows the router to check if a plan is actively executing.
type PlanChecker interface {
	IsActive() bool
}

// Router determines the optimal execution strategy for incoming tasks
// and routes them to the appropriate handler.
type Router struct {
	analyzer  *taskAnalyzer
	optimizer *StrategyOptimizer

	// Configuration
	enabled            bool
	decomposeThreshold int
	parallelThreshold  int
	fastModel          string

	// Learned routing
	routingHistory []routingRecord
	historyMu      sync.RWMutex

	// Context awareness
	recentErrors     int
	recentOps        int
	conversationMode string

	// Plan awareness
	planChecker PlanChecker
}

// NewRouter creates a new task router.
func NewRouter(opts ...RouterOption) *Router {
	r := &Router{
		enabled:            true,
		decomposeThreshold: 4,
		parallelThreshold:  7,
		routingHistory:     make([]routingRecord, 0, 100),
		conversationMode:   "exploring",
	}

	for _, opt := range opts {
		opt(r)
	}

	r.analyzer = newTaskAnalyzer(r.decomposeThreshold, r.parallelThreshold)

	return r
}

// SetPlanChecker sets a plan checker for plan-aware routing.
func (r *Router) SetPlanChecker(pc PlanChecker) {
	r.planChecker = pc
}

// Route determines the best execution strategy for a message.
func (r *Router) Route(message string) *RouteDecision {
	analysis := r.analyzer.analyze(message)

	// Plan-aware adjustments: reduce complexity during plan execution
	if r.planChecker != nil && r.planChecker.IsActive() {
		// During plan execution, prefer executor over sub-agents
		if analysis.Strategy == StrategySubAgent {
			analysis.Strategy = StrategyExecutor
			analysis.Reasoning += " (downgraded: plan active)"
		}
		// Reduce complexity score during plan execution
		if analysis.Score > 3 {
			analysis.Score = 3
		}
	}

	// Adjust strategy from historical learning
	r.adjustStrategyFromHistory(analysis)

	decision := &RouteDecision{
		Analysis:   analysis,
		Message:    message,
		Reasoning:  analysis.Reasoning,
	}

	switch analysis.Strategy {
	case StrategyDirect:
		decision.Handler = "direct"
		decision.SuggestedModel = r.fastModel
	case StrategySingleTool:
		decision.Handler = "executor"
		if analysis.Score <= 2 {
			decision.SuggestedModel = r.fastModel
		}
	case StrategyExecutor:
		decision.Handler = "executor"
		decision.ThinkingBudget = r.selectThinkingBudget(analysis)
	case StrategySubAgent:
		decision.Handler = "sub_agent"
		decision.SubAgentType = r.selectSubAgentType(analysis.Type)
		decision.ThinkingBudget = r.selectThinkingBudget(analysis)
		decision.Background = analysis.Type == TaskTypeBackground
	}

	return decision
}

// Analyze returns the task analysis without executing.
func (r *Router) Analyze(message string) *TaskComplexity {
	return r.analyzer.analyze(message)
}

// RecordOutcome records whether a routing decision was successful.
func (r *Router) RecordOutcome(message string, analysis *TaskComplexity, success bool) {
	r.historyMu.Lock()
	defer r.historyMu.Unlock()

	r.routingHistory = append(r.routingHistory, routingRecord{
		message:   message,
		taskType:  analysis.Type,
		strategy:  analysis.Strategy,
		success:   success,
		timestamp: time.Now(),
	})

	if len(r.routingHistory) > 100 {
		r.routingHistory = r.routingHistory[len(r.routingHistory)-100:]
	}

	// Record with strategy optimizer if available
	if r.optimizer != nil {
		r.optimizer.RecordOutcome(string(analysis.Type), string(analysis.Strategy), success, 0)
	}
}

// RecordTypedOutcome records an outcome with explicit task type and strategy.
func (r *Router) RecordTypedOutcome(message string, taskType TaskType, strategy ExecutionStrategy, success bool) {
	r.historyMu.Lock()
	defer r.historyMu.Unlock()

	r.routingHistory = append(r.routingHistory, routingRecord{
		message:   message,
		taskType:  taskType,
		strategy:  strategy,
		success:   success,
		timestamp: time.Now(),
	})

	if len(r.routingHistory) > 100 {
		r.routingHistory = r.routingHistory[len(r.routingHistory)-100:]
	}

	if r.optimizer != nil {
		r.optimizer.RecordOutcome(string(taskType), string(strategy), success, 0)
	}
}

// GetStrategySuccessRate returns the historical success rate for a strategy (exported).
func (r *Router) GetStrategySuccessRate(strategy ExecutionStrategy) float64 {
	return r.getStrategySuccessRate(strategy)
}

// TrackOperation records an operation outcome for context awareness.
func (r *Router) TrackOperation(toolName string, success bool) {
	r.historyMu.Lock()
	defer r.historyMu.Unlock()

	r.recentOps++
	if !success {
		r.recentErrors++
	}

	if r.recentOps >= 20 {
		r.recentOps = 0
		r.recentErrors = 0
	}

	r.updateConversationMode(toolName)
}

// GetConversationMode returns the current inferred conversation mode.
func (r *Router) GetConversationMode() string {
	r.historyMu.RLock()
	defer r.historyMu.RUnlock()

	if r.conversationMode == "" {
		return "exploring"
	}
	return r.conversationMode
}

// GetErrorRate returns the recent error rate.
func (r *Router) GetErrorRate() float64 {
	r.historyMu.RLock()
	defer r.historyMu.RUnlock()

	if r.recentOps == 0 {
		return 0
	}
	return float64(r.recentErrors) / float64(r.recentOps)
}

// --- internal ---

func (r *Router) selectSubAgentType(taskType TaskType) string {
	switch taskType {
	case TaskTypeExploration:
		return "explore"
	case TaskTypeBackground:
		return "bash"
	default:
		return "general"
	}
}

func (r *Router) selectThinkingBudget(analysis *TaskComplexity) int32 {
	switch analysis.Strategy {
	case StrategyDirect:
		return 0
	case StrategySingleTool:
		if analysis.Score <= 2 {
			return 0
		}
		return 1024
	case StrategyExecutor:
		if analysis.Score >= 5 {
			return 4096
		}
		return 1024
	case StrategySubAgent:
		return 8192
	}
	return 0
}

func (r *Router) adjustStrategyFromHistory(analysis *TaskComplexity) {
	currentRate := r.getStrategySuccessRate(analysis.Strategy)

	if currentRate < 0.3 && currentRate > 0 {
		switch analysis.Strategy {
		case StrategyDirect:
			if altRate := r.getStrategySuccessRate(StrategyExecutor); altRate > currentRate {
				analysis.Strategy = StrategyExecutor
			}
		case StrategyExecutor:
			if altRate := r.getStrategySuccessRate(StrategySubAgent); altRate > currentRate {
				analysis.Strategy = StrategySubAgent
			}
		}
	}
}

func (r *Router) getStrategySuccessRate(strategy ExecutionStrategy) float64 {
	r.historyMu.RLock()
	defer r.historyMu.RUnlock()

	total := 0
	successes := 0
	for _, rec := range r.routingHistory {
		if rec.strategy == strategy {
			total++
			if rec.success {
				successes++
			}
		}
	}

	if total < 3 {
		return 0.5
	}
	return float64(successes) / float64(total)
}

func (r *Router) updateConversationMode(toolName string) {
	switch {
	case toolName == "grep" || toolName == "glob" || toolName == "read" || toolName == "tree":
		r.conversationMode = "exploring"
	case toolName == "write" || toolName == "edit":
		r.conversationMode = "implementing"
	case toolName == "bash" && r.recentErrors > 2:
		r.conversationMode = "debugging"
	}
}

// --- Task Analyzer ---

type taskAnalyzer struct {
	decomposeThreshold int
	parallelThreshold  int

	questionPatterns    []*regexp.Regexp
	explorationPatterns []*regexp.Regexp
	refactoringPatterns []*regexp.Regexp
	backgroundPatterns  []*regexp.Regexp
	multiToolPatterns   []*regexp.Regexp
}

func newTaskAnalyzer(decomposeThreshold, parallelThreshold int) *taskAnalyzer {
	return &taskAnalyzer{
		decomposeThreshold:  decomposeThreshold,
		parallelThreshold:   parallelThreshold,
		questionPatterns:    compileRouterPatterns(questionRegexes),
		explorationPatterns: compileRouterPatterns(explorationRegexes),
		refactoringPatterns: compileRouterPatterns(refactoringRegexes),
		backgroundPatterns:  compileRouterPatterns(backgroundRegexes),
		multiToolPatterns:   compileRouterPatterns(multiToolRegexes),
	}
}

func (ta *taskAnalyzer) analyze(message string) *TaskComplexity {
	message = strings.TrimSpace(message)
	if message == "" {
		return &TaskComplexity{
			Score:     0,
			Type:      TaskTypeQuestion,
			Strategy:  StrategyDirect,
			Reasoning: "Empty message",
		}
	}

	score := ta.calculateScore(message)
	taskType := ta.determineTaskType(message, score)
	strategy := ta.determineStrategy(taskType, score)
	reasoning := ta.generateReasoning(taskType, score, strategy)

	return &TaskComplexity{
		Score:     score,
		Type:      taskType,
		Strategy:  strategy,
		Reasoning: reasoning,
	}
}

func (ta *taskAnalyzer) calculateScore(message string) int {
	score := 1

	wordCount := countRouterWords(message)
	switch {
	case wordCount > 100:
		score += 3
	case wordCount > 50:
		score += 2
	case wordCount > 20:
		score += 1
	}

	complexityKeywords := map[int][]string{
		3: {"analyze", "investigate", "how does"},
		2: {"create", "implement", "refactor", "optimize"},
		1: {"what", "where", "show", "list", "find"},
	}

	lowerMessage := strings.ToLower(message)
	for points, keywords := range complexityKeywords {
		for _, keyword := range keywords {
			if strings.Contains(lowerMessage, keyword) {
				score += points
				break
			}
		}
	}

	if hasMultipleRouterInstructions(message) {
		score += 2
	}

	if strings.Contains(lowerMessage, "git") || strings.Contains(lowerMessage, "diff") ||
		strings.Contains(lowerMessage, "merge") || strings.Contains(lowerMessage, "branch") {
		score += 1
	}

	if score > 10 {
		score = 10
	}

	return score
}

func (ta *taskAnalyzer) determineTaskType(message string, score int) TaskType {
	lowerMessage := strings.ToLower(message)

	if ta.matchesAny(lowerMessage, ta.backgroundPatterns) {
		return TaskTypeBackground
	}
	if ta.matchesAny(lowerMessage, ta.refactoringPatterns) {
		return TaskTypeRefactoring
	}
	if ta.matchesAny(lowerMessage, ta.explorationPatterns) {
		return TaskTypeExploration
	}
	if ta.matchesAny(lowerMessage, ta.multiToolPatterns) {
		return TaskTypeMultiTool
	}
	if ta.matchesAny(lowerMessage, ta.questionPatterns) || score <= 2 {
		return TaskTypeQuestion
	}

	if score >= 7 {
		return TaskTypeComplex
	}
	return TaskTypeSingleTool
}

func (ta *taskAnalyzer) determineStrategy(taskType TaskType, score int) ExecutionStrategy {
	switch taskType {
	case TaskTypeQuestion:
		if score <= 2 {
			return StrategyDirect
		}
		return StrategyExecutor
	case TaskTypeSingleTool:
		return StrategyExecutor
	case TaskTypeMultiTool:
		if score <= 4 {
			return StrategyExecutor
		}
		return StrategySubAgent
	case TaskTypeExploration:
		return StrategySubAgent
	case TaskTypeRefactoring:
		if score <= 5 {
			return StrategyExecutor
		}
		return StrategySubAgent
	case TaskTypeBackground:
		return StrategySubAgent
	case TaskTypeComplex:
		if score >= ta.parallelThreshold {
			return StrategySubAgent
		}
		if score >= ta.decomposeThreshold {
			return StrategySubAgent
		}
		return StrategyExecutor
	}
	return StrategyExecutor
}

func (ta *taskAnalyzer) generateReasoning(taskType TaskType, score int, strategy ExecutionStrategy) string {
	var reasoning string

	switch taskType {
	case TaskTypeQuestion:
		reasoning = "Simple question requiring direct answer"
	case TaskTypeSingleTool:
		reasoning = "Task can be completed with a single tool"
	case TaskTypeMultiTool:
		reasoning = "Requires multiple tools sequentially"
	case TaskTypeExploration:
		reasoning = "Code exploration requires analysis"
	case TaskTypeRefactoring:
		reasoning = "Code refactoring task"
	case TaskTypeBackground:
		reasoning = "Long-running task, better run in background"
	case TaskTypeComplex:
		reasoning = "Complex multi-step task"
	}

	reasoning += fmt.Sprintf(" (complexity: %d/10, strategy: %s)", score, strategy)
	return reasoning
}

func (ta *taskAnalyzer) matchesAny(message string, patterns []*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(message) {
			return true
		}
	}
	return false
}

// --- Helpers ---

func countRouterWords(s string) int {
	count := 0
	inWord := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if !inWord {
				count++
				inWord = true
			}
		} else {
			inWord = false
		}
	}
	return count
}

func hasMultipleRouterInstructions(message string) bool {
	delimiters := []string{". ", "! ", "? ", "\n", "; "}
	count := 0
	for _, delim := range delimiters {
		count += strings.Count(message, delim)
	}
	return count >= 2
}

func compileRouterPatterns(patterns []string) []*regexp.Regexp {
	var compiled []*regexp.Regexp
	for _, pattern := range patterns {
		regex, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			continue
		}
		compiled = append(compiled, regex)
	}
	return compiled
}

// --- Pattern definitions ---

var questionRegexes = []string{
	`\?`,
	`^what\s`,
	`^how\s+(do|does|much|many|can)\b`,
	`^where\s`,
	`^why\s`,
	`^which\s`,
	`^who\s`,
	`explain`,
	`show\s+me`,
	`what'?s\s`,
}

var explorationRegexes = []string{
	`explore`,
	`find\s+(all\s+)?(files|usages|where\s+used)`,
	`show\s+me\s+(the\s+)?(structure|architecture)`,
	`understand\s+(the\s+)?code`,
	`analyze\s+codebase`,
	`code\s+overview`,
	`what\s+does\s+this\s+code\s+do`,
}

var refactoringRegexes = []string{
	`refactor`,
	`optimize`,
	`rewrite`,
	`clean\s+up`,
	`improve\s+(the\s+)?code`,
	`fix\s+(style|formatting)`,
	`reorganize`,
	`extract\s+(function|method|class)`,
	`rename`,
}

var backgroundRegexes = []string{
	`run\s+in\s+background`,
	`background`,
	`long\s+running`,
	`test\s+(everything|all)`,
	`benchmark`,
	`compile\s+(everything|all)`,
}

var multiToolRegexes = []string{
	`create\s+(new\s+)?(feature|function|class|file)`,
	`implement`,
	`add\s+(new\s+)?(feature|functionality)`,
	`build\s+(application|system|module)`,
	`update\s+(multiple|several|all)\s+files`,
	`first\s+.*\s+then\s+`,
}
