// Package sdk provides a Go framework for building AI-powered agents with tool use.
//
// The SDK supports multiple LLM providers (Gemini, Anthropic, Ollama) through a
// unified Client interface, and provides a tool system for extending agent capabilities.
//
// Basic usage:
//
//	client, _ := gemini.New(ctx, os.Getenv("GEMINI_API_KEY"), "gemini-2.0-flash")
//	registry := sdk.NewRegistry()
//	registry.Register(tools.NewBash("/workspace"))
//	agent := sdk.NewAgent("assistant", client, registry,
//	    sdk.WithSystemPrompt("You are a helpful coding assistant."),
//	)
//	result, _ := agent.Run(ctx, "Find all Go files")
package sdk

// Version is the current SDK version.
const Version = "0.8.0"
