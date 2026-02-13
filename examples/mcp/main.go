// Package main demonstrates MCP server integration.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	sdk "github.com/ginkida/gokin-sdk"
	"github.com/ginkida/gokin-sdk/mcp"
	"github.com/ginkida/gokin-sdk/provider/gemini"
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

	// Configure MCP servers
	manager := mcp.NewManager([]mcp.ServerConfig{
		{
			Name:        "filesystem",
			Type:        "stdio",
			Command:     "npx",
			Args:        []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			AutoConnect: true,
		},
	})

	// Connect to all servers
	fmt.Println("Connecting to MCP servers...")
	if err := manager.ConnectAll(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "MCP connection error: %v\n", err)
		fmt.Println("(Continuing without MCP tools)")
	} else {
		// Show connected servers
		for _, status := range manager.GetServerStatus() {
			fmt.Printf("Server %s: connected=%v, tools=%v\n",
				status.Name, status.Connected, status.Tools)
		}
	}
	defer manager.Shutdown(ctx)

	// Create registry and register MCP tools
	registry := sdk.NewRegistry()
	if err := manager.RegisterTools(registry); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	fmt.Printf("Registered tools: %v\n\n", registry.Names())

	// Create an agent with MCP tools
	agent, err := sdk.NewAgent("assistant", client, registry,
		sdk.WithSystemPrompt("You are an assistant with access to MCP tools for filesystem operations."),
		sdk.WithMaxTurns(10),
		sdk.WithOnText(func(text string) {
			fmt.Print(text)
		}),
		sdk.WithOnToolCall(func(name string, args map[string]any) {
			fmt.Printf("\n[MCP Tool: %s]\n", name)
		}),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create agent: %v\n", err)
		os.Exit(1)
	}

	message := "List the files in /tmp"
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
