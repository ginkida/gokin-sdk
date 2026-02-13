// Package main demonstrates using the Anthropic provider.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	sdk "github.com/ginkida/gokin-sdk"
	"github.com/ginkida/gokin-sdk/provider/anthropic"
	"github.com/ginkida/gokin-sdk/tools"
)

func main() {
	ctx := context.Background()

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY environment variable is required")
		os.Exit(1)
	}

	// Create an Anthropic client
	client, err := anthropic.New(apiKey, "claude-sonnet-4-5-20250929")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	// Create a registry with tools
	workDir, _ := os.Getwd()
	registry := sdk.NewRegistry()
	registry.MustRegister(tools.NewBash(workDir))
	registry.MustRegister(tools.NewRead())
	registry.MustRegister(tools.NewGlob(workDir))
	registry.MustRegister(tools.NewGrep(workDir))
	registry.MustRegister(tools.NewEdit())
	registry.MustRegister(tools.NewWrite())

	// Create an agent
	agent := sdk.NewAgent("claude-assistant", client, registry,
		sdk.WithSystemPrompt("You are a helpful coding assistant powered by Claude."),
		sdk.WithMaxTurns(15),
		sdk.WithOnText(func(text string) {
			fmt.Print(text)
		}),
		sdk.WithOnToolCall(func(name string, args map[string]any) {
			fmt.Printf("\n[Tool: %s]\n", name)
		}),
	)

	message := "What Go files are in the current directory? Briefly describe their purpose."
	if len(os.Args) > 1 {
		message = os.Args[1]
	}

	fmt.Printf("User: %s\n\nAssistant: ", message)
	result, err := agent.Run(ctx, message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n\nCompleted in %d turns (%v)\n", result.Turns, result.Duration.Round(time.Millisecond))
}
