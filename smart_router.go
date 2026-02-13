package sdk

// SmartRouter extends Router with strategy optimizer-based learning.
type SmartRouter struct {
	Router
	optimizer *StrategyOptimizer
}

// NewSmartRouter creates a router with built-in learning via StrategyOptimizer.
func NewSmartRouter(optimizer *StrategyOptimizer, opts ...RouterOption) *SmartRouter {
	sr := &SmartRouter{
		optimizer: optimizer,
	}

	// Apply router options
	sr.Router = *NewRouter(append(opts, WithRouterOptimizer(optimizer))...)

	return sr
}

// Route overrides Router.Route with optimizer-informed strategy selection.
func (sr *SmartRouter) Route(message string) *RouteDecision {
	decision := sr.Router.Route(message)

	if sr.optimizer == nil {
		return decision
	}

	// Consult optimizer for a potentially better strategy
	taskType := string(decision.Analysis.Type)
	best := sr.optimizer.GetBestStrategy(taskType)

	// Only override if optimizer has sufficient data and suggests something different
	strategies := sr.optimizer.GetStrategies()
	if m, ok := strategies[best]; ok {
		total := m.SuccessCount + m.FailureCount
		if total >= 5 && m.SuccessRate() > 0.7 {
			suggested := strategyFromString(best)
			if suggested != "" && suggested != decision.Analysis.Strategy {
				decision.Analysis.Strategy = suggested
				decision.Reasoning += " (optimizer override: " + best + ")"
				sr.applyStrategyToDecision(decision)
			}
		}
	}

	return decision
}

// GetAdaptiveStats returns debug information about the optimizer's learned strategies.
func (sr *SmartRouter) GetAdaptiveStats() map[string]*StrategyMetrics {
	if sr.optimizer == nil {
		return nil
	}
	return sr.optimizer.GetStrategies()
}

// applyStrategyToDecision updates decision fields to match the strategy.
func (sr *SmartRouter) applyStrategyToDecision(decision *RouteDecision) {
	switch decision.Analysis.Strategy {
	case StrategyDirect:
		decision.Handler = "direct"
		decision.SuggestedModel = sr.fastModel
	case StrategySingleTool:
		decision.Handler = "executor"
	case StrategyExecutor:
		decision.Handler = "executor"
		decision.ThinkingBudget = sr.selectThinkingBudget(decision.Analysis)
	case StrategySubAgent:
		decision.Handler = "sub_agent"
		decision.SubAgentType = sr.selectSubAgentType(decision.Analysis.Type)
		decision.ThinkingBudget = sr.selectThinkingBudget(decision.Analysis)
	}
}

// strategyFromString converts a strategy name string back to ExecutionStrategy.
func strategyFromString(s string) ExecutionStrategy {
	switch ExecutionStrategy(s) {
	case StrategyDirect:
		return StrategyDirect
	case StrategySingleTool:
		return StrategySingleTool
	case StrategyExecutor:
		return StrategyExecutor
	case StrategySubAgent:
		return StrategySubAgent
	default:
		return ""
	}
}
