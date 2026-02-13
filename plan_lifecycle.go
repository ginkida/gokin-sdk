package sdk

import (
	"fmt"
	"sync"
	"time"
)

// PlanLifecycleState represents the state of a plan lifecycle.
type PlanLifecycleState string

const (
	PlanStateDraft     PlanLifecycleState = "draft"
	PlanStateApproved  PlanLifecycleState = "approved"
	PlanStateExecuting PlanLifecycleState = "executing"
	PlanStateCompleted PlanLifecycleState = "completed"
	PlanStateFailed    PlanLifecycleState = "failed"
	PlanStatePaused    PlanLifecycleState = "paused"
)

// validTransitions defines the allowed state transitions.
var validTransitions = map[PlanLifecycleState][]PlanLifecycleState{
	PlanStateDraft:     {PlanStateApproved, PlanStateFailed},
	PlanStateApproved:  {PlanStateExecuting, PlanStateFailed},
	PlanStateExecuting: {PlanStateCompleted, PlanStateFailed, PlanStatePaused},
	PlanStatePaused:    {PlanStateExecuting, PlanStateFailed},
	PlanStateFailed:    {PlanStateDraft}, // allow replan
	PlanStateCompleted: {},               // terminal
}

// CanTransitionTo checks if a state transition is valid.
func CanTransitionTo(from, to PlanLifecycleState) bool {
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// IsTerminal checks if a state is terminal (no further transitions allowed).
func IsTerminal(state PlanLifecycleState) bool {
	allowed, ok := validTransitions[state]
	if !ok {
		return true
	}
	return len(allowed) == 0
}

// PlanLifecycle manages the lifecycle of a plan from draft to completion.
type PlanLifecycle struct {
	PlanID    string             `json:"plan_id"`
	State     PlanLifecycleState `json:"state"`
	Tree      *PlanTree          `json:"tree"`
	Version   int                `json:"version"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`

	// Replan tracking
	ReplanCount  int    `json:"replan_count"`
	ReplanReason string `json:"replan_reason,omitempty"`

	mu sync.Mutex
}

// NewPlanLifecycle creates a new plan lifecycle in draft state.
func NewPlanLifecycle(planID string, tree *PlanTree) *PlanLifecycle {
	now := time.Now()
	return &PlanLifecycle{
		PlanID:    planID,
		State:     PlanStateDraft,
		Tree:      tree,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// TransitionTo moves the plan to a new lifecycle state.
func (lc *PlanLifecycle) TransitionTo(state PlanLifecycleState) error {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	if !CanTransitionTo(lc.State, state) {
		return fmt.Errorf("invalid transition from %s to %s", lc.State, state)
	}

	lc.State = state
	lc.Version++
	lc.UpdatedAt = time.Now()
	return nil
}

// RequestReplan marks the plan for replanning by transitioning to draft.
func (lc *PlanLifecycle) RequestReplan(reason string) error {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	if lc.State != PlanStateFailed && lc.State != PlanStatePaused {
		return fmt.Errorf("can only replan from failed or paused state, current: %s", lc.State)
	}

	lc.State = PlanStateDraft
	lc.ReplanCount++
	lc.ReplanReason = reason
	lc.Version++
	lc.UpdatedAt = time.Now()
	return nil
}

// IsActive returns true if the plan is in an active (non-terminal) state.
func (lc *PlanLifecycle) IsActive() bool {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.State == PlanStateExecuting || lc.State == PlanStateApproved
}

// GetState returns the current state (thread-safe).
func (lc *PlanLifecycle) GetState() PlanLifecycleState {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.State
}

// Summary returns a brief summary of the plan lifecycle.
func (lc *PlanLifecycle) Summary() string {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	nodes := 0
	if lc.Tree != nil {
		nodes = lc.Tree.TotalNodes
	}

	return fmt.Sprintf("Plan %s: state=%s, version=%d, nodes=%d, replans=%d",
		lc.PlanID, lc.State, lc.Version, nodes, lc.ReplanCount)
}
