package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// PlanNodeStatus represents the status of a plan node.
type PlanNodeStatus string

const (
	PlanNodePending   PlanNodeStatus = "pending"
	PlanNodeRunning   PlanNodeStatus = "running"
	PlanNodeCompleted PlanNodeStatus = "completed"
	PlanNodeFailed    PlanNodeStatus = "failed"
	PlanNodeSkipped   PlanNodeStatus = "skipped"
)

// ActionType represents the type of a planned action.
type ActionType string

const (
	ActionToolCall  ActionType = "tool_call"
	ActionDelegate  ActionType = "delegate"
	ActionDecompose ActionType = "decompose"
	ActionVerify    ActionType = "verify"
)

// PlanNode represents a node in the plan tree.
type PlanNode struct {
	ID       string         `json:"id"`
	ParentID string         `json:"parent_id,omitempty"`
	Action   *PlannedAction `json:"action"`
	Status   PlanNodeStatus `json:"status"`
	Score    float64        `json:"score"`
	Children []*PlanNode    `json:"children,omitempty"`
	Result   *PlanResult    `json:"result,omitempty"`

	// MCTS statistics
	Visits      int     `json:"visits"`
	TotalReward float64 `json:"total_reward"`
}

// PlannedAction describes what a plan step should do.
type PlannedAction struct {
	Type          ActionType     `json:"type"`
	AgentType     AgentType      `json:"agent_type,omitempty"`
	Prompt        string         `json:"prompt"`
	ToolName      string         `json:"tool_name,omitempty"`
	ToolArgs      map[string]any `json:"tool_args,omitempty"`
	NodeID        string         `json:"node_id"`
	Prerequisites []string       `json:"prerequisites,omitempty"`
}

// PlanResult captures the output of executing a plan node.
type PlanResult struct {
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
	Success bool   `json:"success"`
}

// PlanTree is the tree of plan nodes.
type PlanTree struct {
	Root       *PlanNode            `json:"root"`
	BestPath   []*PlanNode          `json:"-"`
	TotalNodes int                  `json:"total_nodes"`
	nodeIndex  map[string]*PlanNode `json:"-"`
	mu         sync.RWMutex         `json:"-"`
}

// PlanGoal defines the objective for planning.
type PlanGoal struct {
	Description     string   `json:"description"`
	SuccessCriteria []string `json:"success_criteria"`
	MaxDepth        int      `json:"max_depth"`
}

// SearchStrategy defines how the planner explores the plan tree.
type SearchStrategy string

const (
	SearchBeam  SearchStrategy = "beam"
	SearchMCTS  SearchStrategy = "mcts"
	SearchAStar SearchStrategy = "astar"
)

// PlannerConfig holds configuration for the planner.
type PlannerConfig struct {
	// MCTSIterations is the number of MCTS simulation iterations.
	MCTSIterations int

	// ExplorationC is the UCB1 exploration constant (default: 1.414).
	ExplorationC float64

	// MaxTreeDepth limits the depth of the plan tree.
	MaxTreeDepth int

	// MaxTreeNodes limits the total number of nodes in the plan tree.
	MaxTreeNodes int

	// Weights for multi-component scoring.
	Weights ScoringWeights
}

// DefaultPlannerConfig returns sensible defaults for the planner.
func DefaultPlannerConfig() PlannerConfig {
	return PlannerConfig{
		MCTSIterations: 50,
		ExplorationC:   1.414,
		MaxTreeDepth:   5,
		MaxTreeNodes:   100,
		Weights:        DefaultScoringWeights(),
	}
}

// Planner builds and manages plan trees using LLM-based action generation.
type Planner struct {
	client      Client
	optimizer   *StrategyOptimizer
	strategy    SearchStrategy
	beamWidth   int
	config      PlannerConfig
	currentTree *PlanTree // used by scoring to access tree index
}

// NewPlanner creates a new planner.
func NewPlanner(client Client, optimizer *StrategyOptimizer) *Planner {
	return &Planner{
		client:    client,
		optimizer: optimizer,
		strategy:  SearchBeam,
		beamWidth: 3,
		config:    DefaultPlannerConfig(),
	}
}

// WithSearchStrategy sets the search strategy.
func (p *Planner) WithSearchStrategy(strategy SearchStrategy) *Planner {
	p.strategy = strategy
	return p
}

// WithPlannerConfig sets the planner configuration.
func (p *Planner) WithPlannerConfig(config PlannerConfig) *Planner {
	p.config = config
	return p
}

// BuildPlan generates a plan tree for the given goal and optionally applies search.
func (p *Planner) BuildPlan(ctx context.Context, goal PlanGoal) (*PlanTree, error) {
	if goal.MaxDepth == 0 {
		goal.MaxDepth = p.config.MaxTreeDepth
	}

	tree := &PlanTree{
		nodeIndex: make(map[string]*PlanNode),
	}

	// Generate root actions via LLM
	actions, err := p.generateActions(ctx, goal.Description, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate initial actions: %w", err)
	}

	if len(actions) == 0 {
		return nil, fmt.Errorf("no actions generated for goal: %s", goal.Description)
	}

	// Create root node
	root := &PlanNode{
		ID:     "root",
		Action: actions[0],
		Status: PlanNodePending,
	}
	tree.Root = root
	tree.nodeIndex["root"] = root
	tree.TotalNodes = 1

	// Add alternative actions as children of root
	for i := 1; i < len(actions) && i < p.beamWidth; i++ {
		child := &PlanNode{
			ID:       fmt.Sprintf("alt-%d", i),
			ParentID: "root",
			Action:   actions[i],
			Status:   PlanNodePending,
		}
		root.Children = append(root.Children, child)
		tree.nodeIndex[child.ID] = child
		tree.TotalNodes++
	}

	// If optimizer available, reorder by historical success
	if p.optimizer != nil {
		p.reorderBySuccess(root, goal.Description)
	}

	// Apply search algorithm to refine the plan
	p.currentTree = tree
	bestPath, err := p.Search(ctx, tree, goal)
	if err == nil && len(bestPath) > 0 {
		tree.BestPath = bestPath
	} else {
		tree.BestPath = []*PlanNode{root}
	}

	return tree, nil
}

// ExpandNode generates child actions for a node.
func (p *Planner) ExpandNode(ctx context.Context, tree *PlanTree, nodeID string, context string) error {
	tree.mu.Lock()
	node, ok := tree.nodeIndex[nodeID]
	tree.mu.Unlock()

	if !ok {
		return fmt.Errorf("node not found: %s", nodeID)
	}

	actions, err := p.generateActions(ctx, context, node)
	if err != nil {
		return err
	}

	tree.mu.Lock()
	defer tree.mu.Unlock()

	for i, action := range actions {
		child := &PlanNode{
			ID:       fmt.Sprintf("%s-%d", nodeID, i),
			ParentID: nodeID,
			Action:   action,
			Status:   PlanNodePending,
		}
		node.Children = append(node.Children, child)
		tree.nodeIndex[child.ID] = child
		tree.TotalNodes++
	}

	return nil
}

// RecordResult records the outcome of executing a plan node.
func (p *Planner) RecordResult(tree *PlanTree, nodeID string, result *PlanResult) {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	node, ok := tree.nodeIndex[nodeID]
	if !ok {
		return
	}

	node.Result = result
	if result.Success {
		node.Status = PlanNodeCompleted
		node.Score = 1.0
	} else {
		node.Status = PlanNodeFailed
		node.Score = 0.0
	}

	// Backpropagate reward
	p.backpropagate(tree, nodeID, node.Score)
}

// GetReadyNodes returns nodes that are ready to execute.
// A node is ready if it's pending, its parent is completed (or it's root),
// and all its prerequisites are completed.
func (p *Planner) GetReadyNodes(tree *PlanTree) []*PlanNode {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	var ready []*PlanNode
	for _, node := range tree.nodeIndex {
		if node.Status != PlanNodePending {
			continue
		}

		// Check parent is completed (or node is root)
		if node.ParentID != "" {
			parent, ok := tree.nodeIndex[node.ParentID]
			if !ok || parent.Status != PlanNodeCompleted {
				continue
			}
		}

		// Check prerequisites are all completed
		if node.Action != nil && len(node.Action.Prerequisites) > 0 {
			allMet := true
			for _, prereqID := range node.Action.Prerequisites {
				prereq, ok := tree.nodeIndex[prereqID]
				if !ok || prereq.Status != PlanNodeCompleted {
					allMet = false
					break
				}
			}
			if !allMet {
				continue
			}
		}

		ready = append(ready, node)
	}
	return ready
}

// Summary returns a text summary of the plan tree.
func (p *Planner) Summary(tree *PlanTree) string {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	if tree.Root == nil {
		return "Empty plan"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Plan Tree (%d nodes):\n", tree.TotalNodes))
	p.printNode(&sb, tree.Root, 0)
	return sb.String()
}

// --- internal ---

func (p *Planner) generateActions(ctx context.Context, goal string, parent *PlanNode) ([]*PlannedAction, error) {
	prompt := fmt.Sprintf(`Generate a plan for this goal. Return JSON array of actions.
Goal: %s`, goal)

	if parent != nil && parent.Result != nil {
		prompt += fmt.Sprintf("\nPrevious step result: %s", parent.Result.Output)
		if parent.Result.Error != "" {
			prompt += fmt.Sprintf("\nPrevious step error: %s", parent.Result.Error)
		}
	}

	prompt += `

Return JSON array:
[{"type": "tool_call|delegate|verify", "prompt": "...", "tool_name": "...", "tool_args": {}, "prerequisites": []}]`

	sr, err := p.client.SendMessage(ctx, prompt)
	if err != nil {
		return nil, err
	}

	resp, err := sr.Collect(ctx)
	if err != nil {
		return nil, err
	}

	// Parse actions from response
	text := resp.Text
	if idx := strings.Index(text, "["); idx >= 0 {
		if end := strings.LastIndex(text, "]"); end > idx {
			text = text[idx : end+1]
		}
	}

	var rawActions []struct {
		Type          string         `json:"type"`
		Prompt        string         `json:"prompt"`
		ToolName      string         `json:"tool_name"`
		ToolArgs      map[string]any `json:"tool_args"`
		Prerequisites []string       `json:"prerequisites"`
	}

	if err := json.Unmarshal([]byte(text), &rawActions); err != nil {
		// Fallback: create a single action from the goal
		return []*PlannedAction{{
			Type:   ActionToolCall,
			Prompt: goal,
		}}, nil
	}

	actions := make([]*PlannedAction, len(rawActions))
	for i, ra := range rawActions {
		at := ActionToolCall
		switch ra.Type {
		case "delegate":
			at = ActionDelegate
		case "verify":
			at = ActionVerify
		case "decompose":
			at = ActionDecompose
		}
		actions[i] = &PlannedAction{
			Type:          at,
			Prompt:        ra.Prompt,
			ToolName:      ra.ToolName,
			ToolArgs:      ra.ToolArgs,
			Prerequisites: ra.Prerequisites,
		}
	}

	return actions, nil
}

func (p *Planner) reorderBySuccess(root *PlanNode, taskDesc string) {
	if len(root.Children) == 0 {
		return
	}
	taskType := classifyTaskType(taskDesc)
	best := p.optimizer.GetBestStrategy(taskType)

	// Move matching strategy to front
	for i, child := range root.Children {
		if child.Action != nil && child.Action.ToolName == best {
			root.Children[0], root.Children[i] = root.Children[i], root.Children[0]
			break
		}
	}
}

func (p *Planner) backpropagate(tree *PlanTree, nodeID string, reward float64) {
	node, ok := tree.nodeIndex[nodeID]
	if !ok {
		return
	}
	node.Visits++
	node.TotalReward += reward

	if node.ParentID != "" {
		p.backpropagate(tree, node.ParentID, reward*0.9) // Decay
	}
}

func (p *Planner) printNode(sb *strings.Builder, node *PlanNode, depth int) {
	indent := strings.Repeat("  ", depth)
	status := string(node.Status)
	action := ""
	if node.Action != nil {
		action = node.Action.Prompt
		if len(action) > 60 {
			action = action[:60] + "..."
		}
	}
	sb.WriteString(fmt.Sprintf("%s[%s] %s: %s\n", indent, status, node.ID, action))
	for _, child := range node.Children {
		p.printNode(sb, child, depth+1)
	}
}

func classifyTaskType(desc string) string {
	lower := strings.ToLower(desc)
	switch {
	case strings.Contains(lower, "fix") || strings.Contains(lower, "bug") || strings.Contains(lower, "error"):
		return "bugfix"
	case strings.Contains(lower, "refactor") || strings.Contains(lower, "clean"):
		return "refactoring"
	case strings.Contains(lower, "add") || strings.Contains(lower, "implement") || strings.Contains(lower, "create"):
		return "implementation"
	case strings.Contains(lower, "test"):
		return "testing"
	case strings.Contains(lower, "explore") || strings.Contains(lower, "find") || strings.Contains(lower, "search"):
		return "exploration"
	default:
		return "general"
	}
}

// Ensure time is used by generateActions
var _ = time.Now
