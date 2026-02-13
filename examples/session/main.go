// Package main demonstrates session persistence.
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
	"google.golang.org/genai"
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

	// Create a session
	session := sdk.NewSession("demo-session")
	fmt.Printf("Session ID: %s\n", session.ID())

	// Set up session store
	storeDir := filepath.Join(os.TempDir(), "gokin-sdk-sessions")
	store := sdk.NewSessionStore(storeDir)

	// Create an agent
	agent, err := sdk.NewAgent("assistant", client, registry,
		sdk.WithSystemPrompt("You are a helpful assistant. Keep your responses concise."),
		sdk.WithMaxTurns(10),
		sdk.WithOnText(func(text string) {
			fmt.Print(text)
		}),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create agent: %v\n", err)
		os.Exit(1)
	}

	// First conversation turn
	fmt.Println("\n--- Turn 1 ---")
	fmt.Println("User: What Go files are in the current directory?")
	fmt.Print("Assistant: ")
	result, err := agent.Run(ctx, "What Go files are in the current directory?")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Add to session history
	session.AddUserMessage("What Go files are in the current directory?")
	session.AddModelResponse(genai.NewContentFromText(result.Text, "model"))

	// Save session
	if err := store.Save(session); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving session: %v\n", err)
	} else {
		fmt.Printf("\n\nSession saved (%d messages)\n", session.Len())
	}

	// List saved sessions
	sessions, _ := store.List()
	fmt.Printf("Saved sessions: %v\n", sessions)

	// Load session back
	loaded, err := store.Load(session.ID())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading session: %v\n", err)
	} else {
		fmt.Printf("Loaded session: %s (%d messages)\n", loaded.ID(), loaded.Len())
		fmt.Printf("Summary: %s\n", loaded.Summary())
	}

	// Cleanup
	store.Delete(session.ID())

	fmt.Printf("\nCompleted in %v\n", result.Duration.Round(time.Millisecond))
}
