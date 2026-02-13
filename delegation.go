package sdk

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DelegationDecision represents the result of evaluating whether to delegate.
type DelegationDecision struct {
	// ShouldDelegate indicates if delegation is recommended.
	ShouldDelegate bool

	// TargetType is the recommended agent type to delegate to.
	TargetType AgentType

	// Reason explains why delegation was recommended.
	Reason string

	// Query is the suggested prompt for the delegated agent.
	Query string
}

// DelegationContext provides information about the current agent state for evaluation.
type DelegationContext struct {
	// AgentType is the current agent's type.
	AgentType AgentType

	// CurrentTurn is how many turns the agent has taken.
	CurrentTurn int

	// LastToolName is the most recently used tool.
	LastToolName string

	// LastToolError is the error from the most recent tool call, if any.
	LastToolError string

	// StuckCount is how many turns without progress.
	StuckCount int

	// DelegationDepth is the current delegation nesting level.
	DelegationDepth int
}

// DelegationRule defines a rule for when delegation should occur.
type DelegationRule struct {
	// Name identifies this rule.
	Name string

	// FromType restricts to a specific agent type ("" = any).
	FromType AgentType

	// Condition returns true if this rule should fire.
	Condition func(ctx DelegationContext) bool

	// TargetType is the agent type to delegate to.
	TargetType AgentType

	// BuildQuery constructs the delegation prompt.
	BuildQuery func(ctx DelegationContext) string

	// Reason explains why this rule triggers.
	Reason string
}

// DelegationStrategy evaluates delegation rules and decides when to delegate.
type DelegationStrategy struct {
	rules    []DelegationRule
	metrics  *DelegationMetrics
	maxDepth int
}

// NewDelegationStrategy creates a new delegation strategy with default rules.
func NewDelegationStrategy() *DelegationStrategy {
	ds := &DelegationStrategy{
		maxDepth: 5,
	}
	ds.rules = defaultDelegationRules()
	return ds
}

// SetMetrics sets the delegation metrics for data-driven decisions.
func (ds *DelegationStrategy) SetMetrics(metrics *DelegationMetrics) {
	ds.metrics = metrics
}

// AddRule adds a custom delegation rule.
func (ds *DelegationStrategy) AddRule(rule DelegationRule) {
	ds.rules = append(ds.rules, rule)
}

// Evaluate checks all rules and returns a delegation decision.
func (ds *DelegationStrategy) Evaluate(ctx DelegationContext) DelegationDecision {
	if ctx.DelegationDepth >= ds.maxDepth {
		return DelegationDecision{ShouldDelegate: false}
	}

	for _, rule := range ds.rules {
		// Check type constraint
		if rule.FromType != "" && rule.FromType != ctx.AgentType {
			continue
		}

		if rule.Condition(ctx) {
			// Check metrics if available — skip delegation if historically unsuccessful
			if ds.metrics != nil {
				from := string(ctx.AgentType)
				to := string(rule.TargetType)
				contextType := rule.Name
				if !ds.metrics.ShouldUseDelegation(from, to, contextType) {
					continue // metrics say this path is not effective
				}
			}

			query := rule.Reason
			if rule.BuildQuery != nil {
				query = rule.BuildQuery(ctx)
			}

			return DelegationDecision{
				ShouldDelegate: true,
				TargetType:     rule.TargetType,
				Reason:         rule.Reason,
				Query:          query,
			}
		}
	}

	return DelegationDecision{ShouldDelegate: false}
}

// RecordOutcome records the result of a delegation for future optimization.
func (ds *DelegationStrategy) RecordOutcome(fromType, toType AgentType, ruleName string, success bool, duration time.Duration, errorType string) {
	if ds.metrics == nil {
		return
	}
	ds.metrics.RecordExecution(string(fromType), string(toType), ruleName, success, duration, errorType)
}

// Execute runs delegation using the provided runner.
func (ds *DelegationStrategy) Execute(ctx context.Context, runner *Runner, decision DelegationDecision) (*AgentResult, error) {
	if !decision.ShouldDelegate {
		return nil, fmt.Errorf("no delegation needed")
	}

	task := AgentTask{
		Prompt:      decision.Query,
		Type:        decision.TargetType,
		Description: decision.Reason,
	}

	_, result, err := runner.Spawn(ctx, task)
	return result, err
}

func defaultDelegationRules() []DelegationRule {
	return []DelegationRule{
		{
			Name:       "explore_needs_bash",
			FromType:   AgentTypeExplore,
			TargetType: AgentTypeBash,
			Reason:     "Exploration requires running a command",
			Condition: func(ctx DelegationContext) bool {
				return ctx.LastToolError != "" && strings.Contains(ctx.LastToolError, "unknown tool: bash")
			},
			BuildQuery: func(ctx DelegationContext) string {
				return fmt.Sprintf("Run the necessary command to help with: %s", ctx.LastToolError)
			},
		},
		{
			Name:       "bash_needs_context",
			FromType:   AgentTypeBash,
			TargetType: AgentTypeExplore,
			Reason:     "Command needs more file context",
			Condition: func(ctx DelegationContext) bool {
				return ctx.LastToolError != "" &&
					(strings.Contains(ctx.LastToolError, "compilation") ||
						strings.Contains(ctx.LastToolError, "undefined"))
			},
			BuildQuery: func(ctx DelegationContext) string {
				return fmt.Sprintf("Find relevant files and context for: %s", ctx.LastToolError)
			},
		},
		{
			Name:       "stuck_escalate_to_plan",
			FromType:   AgentTypeGeneral,
			TargetType: AgentTypePlan,
			Reason:     "Agent stuck, needs a plan",
			Condition: func(ctx DelegationContext) bool {
				return ctx.StuckCount >= 5
			},
			BuildQuery: func(ctx DelegationContext) string {
				return "Create a step-by-step plan to resolve the current blockers"
			},
		},
		{
			Name:       "plan_needs_exploration",
			FromType:   AgentTypePlan,
			TargetType: AgentTypeExplore,
			Reason:     "Plan needs file exploration",
			Condition: func(ctx DelegationContext) bool {
				return ctx.LastToolError != "" && strings.Contains(ctx.LastToolError, "not found")
			},
			BuildQuery: func(ctx DelegationContext) string {
				return fmt.Sprintf("Search the codebase for: %s", ctx.LastToolError)
			},
		},
		{
			Name:       "any_file_not_found",
			FromType:   "",
			TargetType: AgentTypeExplore,
			Reason:     "File not found, need to search",
			Condition: func(ctx DelegationContext) bool {
				return ctx.LastToolError != "" && strings.Contains(ctx.LastToolError, "file not found")
			},
			BuildQuery: func(ctx DelegationContext) string {
				return fmt.Sprintf("Find the correct file path for: %s", ctx.LastToolError)
			},
		},
		{
			Name:       "stuck_escalate_to_general",
			FromType:   "",
			TargetType: AgentTypeGeneral,
			Reason:     "Agent stuck for too long, escalating",
			Condition: func(ctx DelegationContext) bool {
				return ctx.StuckCount >= 7 && ctx.AgentType != AgentTypeGeneral
			},
			BuildQuery: func(ctx DelegationContext) string {
				return "The previous agent got stuck. Take a different approach to solve the task."
			},
		},
	}
}
