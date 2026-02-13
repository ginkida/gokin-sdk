// Package main demonstrates the Reflector for automatic error analysis and recovery.
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
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

	// Create a reflector without LLM semantic analysis or error store
	reflector := sdk.NewReflector(nil, nil)

	// Add a custom pattern for timeout errors
	reflector.AddPattern(
		regexp.MustCompile(`timeout|timed out`),
		"timeout",
		"Operation timed out. Retry with a smaller scope or different approach.",
		true,
		"",
	)

	workDir, _ := os.Getwd()
	registry := sdk.NewRegistry()
	registry.MustRegister(tools.NewBash(workDir))
	registry.MustRegister(tools.NewRead())
	registry.MustRegister(tools.NewGlob(workDir))
	registry.MustRegister(tools.NewGrep(workDir))

	// Create agent with reflector attached
	agent := sdk.NewAgent("reflector-demo", client, registry,
		sdk.WithSystemPrompt("You are a helpful assistant. Use tools to complete tasks."),
		sdk.WithMaxTurns(10),
		sdk.WithReflector(reflector),
		sdk.WithOnText(func(text string) {
			fmt.Print(text)
		}),
		sdk.WithOnToolCall(func(name string, args map[string]any) {
			fmt.Printf("\n[Tool: %s]\n", name)
		}),
	)

	// Run a task that may trigger error reflection (reading a non-existent file)
	message := "Read the file /tmp/nonexistent-gokin-test-file.txt and summarize its contents"
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
