# Contributing to gokin-sdk

Thank you for your interest in contributing!

## Reporting Bugs

Open an issue with:
- Go version (`go version`)
- OS and architecture
- Steps to reproduce
- Expected vs actual behavior

## Suggesting Features

Open an issue describing:
- The problem you're trying to solve
- Your proposed solution
- Alternatives you've considered

## Pull Requests

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/my-change`)
3. Write your code and tests
4. Run checks:
   ```bash
   go build ./...
   go vet ./...
   go test ./...
   ```
5. Commit with a clear message
6. Push and open a pull request

## Code Style

- Format with `gofmt` (or `goimports`)
- Pass `golangci-lint run`
- Follow existing patterns in the codebase

## Project Structure

| Directory | Purpose |
|-----------|---------|
| `provider/` | LLM provider implementations |
| `tools/` | Built-in tool implementations |
| `plan/` | Planning engine |
| `config/` | Configuration loading |
| `context/` | Context management |
| `security/` | Sandboxing and validation |
| `examples/` | Usage examples |
