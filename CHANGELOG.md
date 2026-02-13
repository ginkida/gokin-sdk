# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.8.0] - 2025-06-01

### Added

- Multi-provider support: Gemini, Anthropic (Claude), Ollama
- Agent framework with tool use and agentic loop
- 29 built-in tools: bash, file I/O, git, grep, web search, and more
- Multi-agent execution with Runner and Coordinator
- Planning engine with beam search, MCTS, and A* strategies
- Smart routing with adaptive strategy learning
- Reflector middleware for self-correcting agents
- MCP (Model Context Protocol) client for external tool servers
- Session persistence with auto-save/restore
- Context management with auto-summarization
- Permission system with per-tool rules
- Security: command sandboxing, path validation, secret redaction
- Rate limiting and caching
- Audit logging
- Hooks system for pre/post tool execution
- Configuration via YAML, environment variables, and per-project overrides
- 7 runnable examples (simple, anthropic, multi_agent, planner, smart_router, mcp, session)
