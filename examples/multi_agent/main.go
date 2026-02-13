// Package main demonstrates multi-agent task execution with the Runner and Coordinator.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	sdk "github.com/ginkida/gokin-sdk"
	"github.com/ginkida/gokin-sdk/provider/gemini"
	"github.com/ginkida/gokin-sdk/tools"
)

func main() {
	ctx := context.Background()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "GEMINI_API_KEY environment variable is required")
		os.Exit(1)
	}

	client, err := gemini.New(ctx, apiKey, "gemini-2.0-flash")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	workDir, _ := os.Getwd()
	registry := sdk.NewRegistry()
	registry.MustRegister(tools.NewBash(workDir))
	registry.MustRegister(tools.NewRead())
	registry.MustRegister(tools.NewGlob(workDir))
	registry.MustRegister(tools.NewGrep(workDir))
	registry.MustRegister(tools.NewTree(workDir))

	// Create a runner with callbacks
	runner := sdk.NewRunner(client, registry,
		sdk.WithRunnerMaxTurns(10),
		sdk.WithOnAgentStart(func(id string, task sdk.AgentTask) {
			fmt.Printf("[Agent %s] Started: %s (%s)\n", id, task.Description, task.Type)
		}),
		sdk.WithOnAgentComplete(func(id string, result *sdk.AgentResult) {
			fmt.Printf("[Agent %s] Completed in %v\n", id, result.Duration)
		}),
		sdk.WithOnAgentProgress(func(id string, text string) {
			fmt.Printf("[Agent %s] %s", id, text)
		}),
	)

	// Example 1: Spawn and wait for a single agent
	fmt.Println("=== Single Agent ===")
	id, result, err := runner.Spawn(ctx, sdk.AgentTask{
		Prompt:      "List the directory structure (max depth 2)",
		Type:        sdk.AgentTypeExplore,
		Description: "Directory exploration",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	} else {
		fmt.Printf("Agent %s result: %s\n\n", id, result.Text[:min(200, len(result.Text))])
	}

	// Example 2: Use Coordinator for parallel tasks
	fmt.Println("=== Parallel Tasks ===")
	coordinator := sdk.NewCoordinator(runner, 3)

	results, err := coordinator.RunParallel(ctx, []sdk.AgentTask{
		{
			Prompt:      "Count the number of Go files in the project",
			Type:        sdk.AgentTypeBash,
			Description: "Count Go files",
		},
		{
			Prompt:      "Find all TODO comments in the codebase",
			Type:        sdk.AgentTypeExplore,
			Description: "Find TODOs",
		},
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Coordinator error: %v\n", err)
	}

	for i, r := range results {
		if r != nil && r.Error == nil {
			text := r.Text
			if len(text) > 150 {
				text = text[:150] + "..."
			}
			fmt.Printf("Task %d: %s\n", i+1, text)
		}
	}

	fmt.Printf("\nAll tasks completed (%v total)\n", time.Since(time.Now()).Round(time.Millisecond))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
