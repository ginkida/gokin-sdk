package sdk

import (
	"container/heap"
	"context"
	"fmt"
	"math"
	"sort"
)

// Search dispatches to the configured search algorithm and returns the best path.
func (p *Planner) Search(ctx context.Context, tree *PlanTree, goal PlanGoal) ([]*PlanNode, error) {
	p.currentTree = tree

	switch p.strategy {
	case SearchBeam:
		return p.beamSearch(ctx, tree, goal)
	case SearchMCTS:
		return p.mctsSearch(ctx, tree, goal)
	case SearchAStar:
		return p.astarSearch(ctx, tree, goal)
	default:
		return p.beamSearch(ctx, tree, goal)
	}
}

// --- Beam Search ---

// beamSearch maintains the top-K nodes by score at each depth level.
func (p *Planner) beamSearch(ctx context.Context, tree *PlanTree, goal PlanGoal) ([]*PlanNode, error) {
	if tree.Root == nil {
		return nil, fmt.Errorf("empty plan tree")
	}

	beamWidth := p.beamWidth
	if beamWidth <= 0 {
		beamWidth = 3
	}

	maxDepth := goal.MaxDepth
	if maxDepth <= 0 {
		maxDepth = p.config.MaxTreeDepth
	}
	if maxDepth <= 0 {
		maxDepth = 5
	}

	// Start with root's children (or root itself if no children)
	beam := []*PlanNode{tree.Root}
	if len(tree.Root.Children) > 0 {
		beam = tree.Root.Children
	}

	var bestPath []*PlanNode

	for depth := 0; depth < maxDepth; depth++ {
		select {
		case <-ctx.Done():
			return bestPath, ctx.Err()
		default:
		}

		// Score all nodes in current beam
		type scoredNode struct {
			node  *PlanNode
			score NodeScore
		}
		var scored []scoredNode
		for _, node := range beam {
			s := p.ScoreNode(node, goal)
			node.Score = s.Composite
			scored = append(scored, scoredNode{node: node, score: s})
		}

		// Sort by composite score (descending)
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].score.Composite > scored[j].score.Composite
		})

		// Keep top-K
		if len(scored) > beamWidth {
			scored = scored[:beamWidth]
		}

		// Track best path (best node at this depth)
		if len(scored) > 0 {
			bestPath = append(bestPath, scored[0].node)
		}

		// Expand best nodes to get next beam
		var nextBeam []*PlanNode
		for _, sn := range scored {
			// Expand if node has no children yet
			if len(sn.node.Children) == 0 && sn.node.Action != nil {
				expandCtx := sn.node.Action.Prompt
				if sn.node.Result != nil {
					expandCtx += "\nResult: " + sn.node.Result.Output
				}
				_ = p.ExpandNode(ctx, tree, sn.node.ID, expandCtx)
			}
			nextBeam = append(nextBeam, sn.node.Children...)
		}

		if len(nextBeam) == 0 {
			break // No more nodes to explore
		}

		beam = nextBeam
	}

	return bestPath, nil
}

// --- MCTS (Monte Carlo Tree Search) ---

// mctsSearch uses UCB1-based selection, expansion, simulation, and backpropagation.
func (p *Planner) mctsSearch(ctx context.Context, tree *PlanTree, goal PlanGoal) ([]*PlanNode, error) {
	if tree.Root == nil {
		return nil, fmt.Errorf("empty plan tree")
	}

	iterations := p.config.MCTSIterations
	if iterations <= 0 {
		iterations = 50
	}

	explorationC := p.config.ExplorationC
	if explorationC <= 0 {
		explorationC = 1.414
	}

	maxNodes := p.config.MaxTreeNodes
	if maxNodes <= 0 {
		maxNodes = 100
	}

	for i := 0; i < iterations; i++ {
		select {
		case <-ctx.Done():
			return p.extractBestPath(tree), ctx.Err()
		default:
		}

		if tree.TotalNodes >= maxNodes {
			break
		}

		// 1. Selection: walk down tree using UCB1
		selected := p.mctsSelect(tree.Root, explorationC)

		// 2. Expansion: add children to selected node
		if len(selected.Children) == 0 && selected.Status == PlanNodePending && selected.Action != nil {
			expandCtx := selected.Action.Prompt
			_ = p.ExpandNode(ctx, tree, selected.ID, expandCtx)
		}

		// 3. Simulation: estimate reward
		reward := p.mctsSimulate(selected, goal)

		// 4. Backpropagation: update visits and rewards up the tree
		tree.mu.Lock()
		p.backpropagate(tree, selected.ID, reward)
		tree.mu.Unlock()
	}

	return p.extractBestPath(tree), nil
}

// mctsSelect walks down the tree using UCB1 formula.
func (p *Planner) mctsSelect(node *PlanNode, c float64) *PlanNode {
	for len(node.Children) > 0 {
		// Find unexplored child first
		for _, child := range node.Children {
			if child.Visits == 0 {
				return child
			}
		}

		// All children explored: use UCB1
		var bestChild *PlanNode
		bestUCB := math.Inf(-1)

		parentVisits := float64(node.Visits)
		if parentVisits == 0 {
			parentVisits = 1
		}

		for _, child := range node.Children {
			exploitation := child.TotalReward / float64(child.Visits)
			exploration := c * math.Sqrt(math.Log(parentVisits)/float64(child.Visits))
			ucb := exploitation + exploration

			if ucb > bestUCB {
				bestUCB = ucb
				bestChild = child
			}
		}

		if bestChild == nil {
			return node
		}
		node = bestChild
	}

	return node
}

// mctsSimulate estimates the reward for a node based on scoring.
func (p *Planner) mctsSimulate(node *PlanNode, goal PlanGoal) float64 {
	score := p.ScoreNode(node, goal)
	return score.Composite
}

// extractBestPath finds the highest-reward path from root to leaf.
func (p *Planner) extractBestPath(tree *PlanTree) []*PlanNode {
	if tree.Root == nil {
		return nil
	}

	var path []*PlanNode
	node := tree.Root
	path = append(path, node)

	for len(node.Children) > 0 {
		bestChild := node.Children[0]
		bestScore := float64(-1)

		for _, child := range node.Children {
			var score float64
			if child.Visits > 0 {
				score = child.TotalReward / float64(child.Visits)
			}
			if score > bestScore {
				bestScore = score
				bestChild = child
			}
		}

		path = append(path, bestChild)
		node = bestChild
	}

	return path
}

// --- A* Search ---

// astarNode wraps a PlanNode for A* priority queue.
type astarNode struct {
	node   *PlanNode
	g      float64 // actual cost (depth + failed attempts)
	h      float64 // heuristic (1 - estimated success probability)
	f      float64 // g + h
	parent *astarNode
	index  int // heap index
}

// astarHeap implements heap.Interface for A* priority queue.
type astarHeap []*astarNode

func (h astarHeap) Len() int           { return len(h) }
func (h astarHeap) Less(i, j int) bool { return h[i].f < h[j].f }
func (h astarHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *astarHeap) Push(x any) {
	n := x.(*astarNode)
	n.index = len(*h)
	*h = append(*h, n)
}

func (h *astarHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[:n-1]
	return item
}

// astarSearch uses A* with f(n) = g(n) + h(n).
func (p *Planner) astarSearch(ctx context.Context, tree *PlanTree, goal PlanGoal) ([]*PlanNode, error) {
	if tree.Root == nil {
		return nil, fmt.Errorf("empty plan tree")
	}

	maxNodes := p.config.MaxTreeNodes
	if maxNodes <= 0 {
		maxNodes = 100
	}

	maxDepth := goal.MaxDepth
	if maxDepth <= 0 {
		maxDepth = p.config.MaxTreeDepth
	}
	if maxDepth <= 0 {
		maxDepth = 5
	}

	// Initialize with root
	rootScore := p.ScoreNode(tree.Root, goal)
	startNode := &astarNode{
		node: tree.Root,
		g:    0,
		h:    1.0 - rootScore.SuccessProb,
	}
	startNode.f = startNode.g + startNode.h

	openSet := &astarHeap{startNode}
	heap.Init(openSet)

	visited := make(map[string]bool)
	nodesExplored := 0

	var bestGoalNode *astarNode

	for openSet.Len() > 0 {
		select {
		case <-ctx.Done():
			break
		default:
		}

		if nodesExplored >= maxNodes {
			break
		}

		current := heap.Pop(openSet).(*astarNode)
		nodesExplored++

		if visited[current.node.ID] {
			continue
		}
		visited[current.node.ID] = true

		// Check if this is a goal (completed node or leaf at max depth)
		depth := int(current.g)
		if current.node.Status == PlanNodeCompleted || depth >= maxDepth {
			if bestGoalNode == nil || current.f < bestGoalNode.f {
				bestGoalNode = current
			}
			continue
		}

		// Expand node if no children
		if len(current.node.Children) == 0 && current.node.Action != nil {
			expandCtx := current.node.Action.Prompt
			_ = p.ExpandNode(ctx, tree, current.node.ID, expandCtx)
		}

		// Add children to open set
		for _, child := range current.node.Children {
			if visited[child.ID] {
				continue
			}

			childScore := p.ScoreNode(child, goal)
			g := current.g + 1.0 // depth + 1
			if child.Status == PlanNodeFailed {
				g += 2.0 // penalty for failed nodes
			}
			h := 1.0 - childScore.SuccessProb

			childAstar := &astarNode{
				node:   child,
				g:      g,
				h:      h,
				f:      g + h,
				parent: current,
			}
			heap.Push(openSet, childAstar)
		}
	}

	// Reconstruct path
	if bestGoalNode == nil {
		return []*PlanNode{tree.Root}, nil
	}

	var path []*PlanNode
	for n := bestGoalNode; n != nil; n = n.parent {
		path = append([]*PlanNode{n.node}, path...)
	}

	return path, nil
}
