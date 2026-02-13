// Package main demonstrates the SmartRouter for adaptive task routing.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

import sdk "github.com/ginkida/gokin-sdk"

func main() {
	// Create strategy optimizer for learning-based routing
	storePath := filepath.Join(os.TempDir(), "gokin-router-strategies.json")
	optimizer := sdk.NewStrategyOptimizer(storePath)

	// Create smart router with optimizer
	router := sdk.NewSmartRouter(optimizer)

	// Route messages of varying complexity
	messages := []string{
		"What is Go?",
		"Find all TODO comments in the project",
		"Refactor the auth module to use JWT tokens",
		"Run all tests in the background",
		"Create a new REST endpoint for user profiles with validation and tests",
	}

	fmt.Println("=== SmartRouter Demo ===")
	fmt.Println()

	for _, msg := range messages {
		decision := router.Route(msg)
		fmt.Printf("Message:  %s\n", msg)
		fmt.Printf("  Type:     %s\n", decision.Analysis.Type)
		fmt.Printf("  Strategy: %s\n", decision.Analysis.Strategy)
		fmt.Printf("  Handler:  %s\n", decision.Handler)
		fmt.Printf("  Score:    %d/10\n", decision.Analysis.Score)
		if decision.SubAgentType != "" {
			fmt.Printf("  SubAgent: %s\n", decision.SubAgentType)
		}
		if decision.ThinkingBudget > 0 {
			fmt.Printf("  Thinking: %d tokens\n", decision.ThinkingBudget)
		}
		fmt.Printf("  Reason:   %s\n\n", decision.Reasoning)
	}

	// Simulate recording outcomes for adaptive learning
	fmt.Println("=== Recording Outcomes ===")
	fmt.Println()

	router.RecordTypedOutcome("What is Go?", sdk.TaskTypeQuestion, sdk.StrategyDirect, true)
	router.RecordTypedOutcome("Find all TODO comments", sdk.TaskTypeExploration, sdk.StrategySubAgent, true)
	router.RecordTypedOutcome("Refactor auth module", sdk.TaskTypeRefactoring, sdk.StrategySubAgent, false)
	router.RecordTypedOutcome("Refactor auth module", sdk.TaskTypeRefactoring, sdk.StrategyExecutor, true)

	// Show adaptive stats
	stats := router.GetAdaptiveStats()
	if len(stats) > 0 {
		fmt.Println("Learned Strategy Stats:")
		for name, m := range stats {
			fmt.Printf("  %s: success=%.0f%% (%d/%d)\n",
				name, m.SuccessRate()*100,
				m.SuccessCount, m.SuccessCount+m.FailureCount)
		}
	}

	fmt.Printf("\nStrategy store: %s\n", storePath)
}
