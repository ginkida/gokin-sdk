package sdk

import "math"

// NodeScore represents a multi-component score for a plan node.
type NodeScore struct {
	// SuccessProb is the estimated probability of success (0.0 - 1.0).
	SuccessProb float64

	// CostEstimate is the estimated cost (tokens/time) normalized to 0.0 - 1.0.
	CostEstimate float64

	// GoalProgress is how much progress this node represents toward the goal (0.0 - 1.0).
	GoalProgress float64

	// Composite is the weighted sum of all components.
	Composite float64
}

// ScoringWeights configures the relative importance of scoring components.
type ScoringWeights struct {
	Success  float64
	Cost     float64
	Progress float64
}

// DefaultScoringWeights returns the default scoring weights.
func DefaultScoringWeights() ScoringWeights {
	return ScoringWeights{
		Success:  0.4,
		Cost:     0.3,
		Progress: 0.3,
	}
}

// ScoreNode evaluates a plan node with multi-component scoring.
func (p *Planner) ScoreNode(node *PlanNode, goal PlanGoal) NodeScore {
	score := NodeScore{}

	// Success probability from optimizer or MCTS statistics
	score.SuccessProb = p.estimateSuccessProb(node)

	// Cost estimate based on action type and depth
	score.CostEstimate = p.estimateCost(node)

	// Goal progress based on node's position and completion
	score.GoalProgress = p.estimateGoalProgress(node, goal)

	// Compute composite score
	weights := p.config.Weights
	score.Composite = weights.Success*score.SuccessProb +
		weights.Cost*(1.0-score.CostEstimate) + // invert: lower cost = better
		weights.Progress*score.GoalProgress

	// Depth penalty: deeper nodes get slightly lower scores
	depth := p.nodeDepth(node)
	score.Composite *= math.Pow(0.95, float64(depth))

	return score
}

// estimateSuccessProb estimates the probability of a node succeeding.
func (p *Planner) estimateSuccessProb(node *PlanNode) float64 {
	// If node has MCTS stats, use those
	if node.Visits > 0 {
		return node.TotalReward / float64(node.Visits)
	}

	// If optimizer available, use historical success rate
	if p.optimizer != nil && node.Action != nil {
		taskType := classifyTaskType(node.Action.Prompt)
		best := p.optimizer.GetBestStrategy(taskType)
		strategies := p.optimizer.GetStrategies()
		if m, ok := strategies[best]; ok {
			return m.SuccessRate()
		}
	}

	// Default: 50% probability for unknown
	return 0.5
}

// estimateCost returns a normalized cost estimate (0.0 = cheap, 1.0 = expensive).
func (p *Planner) estimateCost(node *PlanNode) float64 {
	if node.Action == nil {
		return 0.5
	}

	switch node.Action.Type {
	case ActionToolCall:
		return 0.2 // single tool call is cheap
	case ActionVerify:
		return 0.3 // verification is moderately cheap
	case ActionDelegate:
		return 0.6 // delegation involves spawning agents
	case ActionDecompose:
		return 0.8 // decomposition is expensive (multiple sub-tasks)
	default:
		return 0.5
	}
}

// estimateGoalProgress estimates how much a node contributes toward the goal.
func (p *Planner) estimateGoalProgress(node *PlanNode, goal PlanGoal) float64 {
	if node.Status == PlanNodeCompleted {
		return 1.0
	}
	if node.Status == PlanNodeFailed {
		return 0.0
	}

	// For pending/running: estimate based on position in tree
	if goal.MaxDepth > 0 {
		depth := p.nodeDepth(node)
		// Nodes closer to max depth are closer to completion
		return float64(depth) / float64(goal.MaxDepth)
	}

	return 0.3 // default partial progress
}

// nodeDepth calculates how deep a node is in the tree.
func (p *Planner) nodeDepth(node *PlanNode) int {
	if p.currentTree == nil {
		return 0
	}
	p.currentTree.mu.RLock()
	defer p.currentTree.mu.RUnlock()

	depth := 0
	currentID := node.ParentID
	for currentID != "" {
		depth++
		parent, ok := p.currentTree.nodeIndex[currentID]
		if !ok {
			break
		}
		currentID = parent.ParentID
	}
	return depth
}
