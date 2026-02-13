// Package main demonstrates plan-driven agent execution with the Planner.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

	// Create strategy optimizer for learning-based plan refinement
	storePath := filepath.Join(os.TempDir(), "gokin-planner-strategies.json")
	optimizer := sdk.NewStrategyOptimizer(storePath)

	// Create planner with beam search strategy
	planner := sdk.NewPlanner(client, optimizer).
		WithSearchStrategy(sdk.SearchBeam)

	workDir, _ := os.Getwd()
	registry := sdk.NewRegistry()
	registry.MustRegister(tools.NewBash(workDir))
	registry.MustRegister(tools.NewRead())
	registry.MustRegister(tools.NewWrite())
	registry.MustRegister(tools.NewGlob(workDir))
	registry.MustRegister(tools.NewGrep(workDir))
	registry.MustRegister(tools.NewTree(workDir))

	// Create agent with planner for plan-driven execution
	agent := sdk.NewAgent("planner-demo", client, registry,
		sdk.WithSystemPrompt("You are a coding assistant that plans before acting."),
		sdk.WithMaxTurns(30),
		sdk.WithPlanner(planner),
		sdk.WithPlanApprovalCallback(func(summary string) {
			fmt.Printf("\n--- Plan ---\n%s\n--- End Plan ---\n\n", summary)
		}),
		sdk.WithOnText(func(text string) {
			fmt.Print(text)
		}),
		sdk.WithOnToolCall(func(name string, args map[string]any) {
			fmt.Printf("\n[Tool: %s]\n", name)
		}),
	)

	// Run a multi-step task that benefits from planning
	message := "Find all Go files in this directory, count total lines, and list the top 3 largest files"
	if len(os.Args) > 1 {
		message = os.Args[1]
	}

	fmt.Printf("User: %s\n\nAssistant: ", message)
	result, err := agent.Run(ctx, message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n\nCompleted (%d replans, %v)\n", result.Turns, result.Duration.Round(time.Millisecond))
	fmt.Printf("Strategy store: %s\n", storePath)
}
