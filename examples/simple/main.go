// Package main provides a simple example of using the gokin-sdk.
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

	// Create a Gemini client
	client, err := gemini.New(ctx, apiKey, "gemini-2.0-flash")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	// Create a tool registry with built-in tools
	workDir, _ := os.Getwd()
	registry := sdk.NewRegistry()
	registry.MustRegister(tools.NewBash(workDir))
	registry.MustRegister(tools.NewRead())
	registry.MustRegister(tools.NewWrite())
	registry.MustRegister(tools.NewEdit())
	registry.MustRegister(tools.NewGlob(workDir))
	registry.MustRegister(tools.NewGrep(workDir))
	registry.MustRegister(tools.NewTree(workDir))
	registry.MustRegister(tools.NewListDir(workDir))
	registry.MustRegister(tools.NewGit(workDir))

	// Create an agent
	agent := sdk.NewAgent("assistant", client, registry,
		sdk.WithSystemPrompt("You are a helpful coding assistant. Use tools to help the user."),
		sdk.WithMaxTurns(20),
		sdk.WithOnText(func(text string) {
			fmt.Print(text)
		}),
		sdk.WithOnToolCall(func(name string, args map[string]any) {
			fmt.Printf("\n[Tool: %s]\n", name)
		}),
	)

	// Run the agent
	message := "Find all Go files in the current directory and count total lines of code"
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
