package sdk

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"google.golang.org/genai"
)

// ExecutorOption configures the Executor.
type ExecutorOption func(*Executor)

// WithTimeout sets the per-tool execution timeout.
func WithTimeout(d time.Duration) ExecutorOption {
	return func(e *Executor) {
		e.timeout = d
	}
}

// WithOnToolStart sets a callback invoked when a tool starts executing.
func WithOnToolStart(fn func(name string, args map[string]any)) ExecutorOption {
	return func(e *Executor) {
		e.onStart = fn
	}
}

// WithOnToolEnd sets a callback invoked when a tool finishes executing.
func WithOnToolEnd(fn func(name string, result *ToolResult)) ExecutorOption {
	return func(e *Executor) {
		e.onEnd = fn
	}
}

// Executor handles parallel execution of tool calls.
type Executor struct {
	registry *Registry
	timeout  time.Duration
	onStart  func(name string, args map[string]any)
	onEnd    func(name string, result *ToolResult)
}

// NewExecutor creates a new tool executor.
func NewExecutor(registry *Registry, opts ...ExecutorOption) *Executor {
	e := &Executor{
		registry: registry,
		timeout:  2 * time.Minute,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

const (
	// MaxFunctionCallsPerResponse limits the number of function calls processed per response.
	MaxFunctionCallsPerResponse = 10
	// MaxConcurrentToolExecutions limits parallel goroutines for tool execution.
	MaxConcurrentToolExecutions = 5
)

// Execute processes a list of function calls and returns function responses.
func (e *Executor) Execute(ctx context.Context, calls []*genai.FunctionCall) ([]*genai.FunctionResponse, error) {
	if len(calls) > MaxFunctionCallsPerResponse {
		calls = calls[:MaxFunctionCallsPerResponse]
	}

	results := make([]*genai.FunctionResponse, len(calls))

	// For a single tool, execute directly
	if len(calls) == 1 {
		result := e.executeTool(ctx, calls[0])
		results[0] = &genai.FunctionResponse{
			ID:       calls[0].ID,
			Name:     calls[0].Name,
			Response: result.ToMap(),
		}
		return results, nil
	}

	// For multiple tools, execute in parallel with semaphore
	var wg sync.WaitGroup
	var mu sync.Mutex
	semaphore := make(chan struct{}, MaxConcurrentToolExecutions)

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, fc *genai.FunctionCall) {
			defer wg.Done()

			// Acquire semaphore slot
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				mu.Lock()
				results[idx] = &genai.FunctionResponse{
					ID:       fc.ID,
					Name:     fc.Name,
					Response: NewErrorResult("cancelled").ToMap(),
				}
				mu.Unlock()
				return
			}

			// Recover from panics
			defer func() {
				if r := recover(); r != nil {
					stack := make([]byte, 4096)
					length := runtime.Stack(stack, false)
					_ = length

					mu.Lock()
					results[idx] = &genai.FunctionResponse{
						ID:       fc.ID,
						Name:     fc.Name,
						Response: NewErrorResult(fmt.Sprintf("panic: %v", r)).ToMap(),
					}
					mu.Unlock()
				}
			}()

			result := e.executeTool(ctx, fc)

			mu.Lock()
			results[idx] = &genai.FunctionResponse{
				ID:       fc.ID,
				Name:     fc.Name,
				Response: result.ToMap(),
			}
			mu.Unlock()
		}(i, call)
	}

	wg.Wait()
	return results, nil
}

// executeTool executes a single tool call.
func (e *Executor) executeTool(ctx context.Context, call *genai.FunctionCall) *ToolResult {
	tool, ok := e.registry.Get(call.Name)
	if !ok {
		return NewErrorResult(fmt.Sprintf("unknown tool: %s", call.Name))
	}

	// Run validation if the tool supports it
	if vt, ok := tool.(ValidatingTool); ok {
		if err := vt.Validate(call.Args); err != nil {
			return NewErrorResult(fmt.Sprintf("validation failed: %s", err))
		}
	}

	if e.onStart != nil {
		e.onStart(call.Name, call.Args)
	}

	execCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	start := time.Now()
	result, err := tool.Execute(execCtx, call.Args)
	if err != nil {
		result = NewErrorResult(err.Error())
	}
	if result.Duration == "" {
		result.Duration = time.Since(start).String()
	}

	if e.onEnd != nil {
		e.onEnd(call.Name, result)
	}

	return result
}
