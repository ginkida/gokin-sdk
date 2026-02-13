package sdk

import (
	"context"
	"fmt"
	"time"
)

// ToolExecuteFunc is the function signature for tool execution.
type ToolExecuteFunc func(ctx context.Context, name string, args map[string]any) (*ToolResult, error)

// Middleware wraps a ToolExecuteFunc with additional behavior.
type Middleware func(next ToolExecuteFunc) ToolExecuteFunc

// LoggingMiddleware logs tool calls and their results.
func LoggingMiddleware(logger func(string)) Middleware {
	return func(next ToolExecuteFunc) ToolExecuteFunc {
		return func(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
			logger(fmt.Sprintf("[tool] %s called", name))
			result, err := next(ctx, name, args)
			if err != nil {
				logger(fmt.Sprintf("[tool] %s error: %v", name, err))
			} else if result != nil && !result.Success {
				logger(fmt.Sprintf("[tool] %s failed: %s", name, result.Error))
			} else {
				logger(fmt.Sprintf("[tool] %s completed", name))
			}
			return result, err
		}
	}
}

// TimingMiddleware measures tool execution duration.
func TimingMiddleware(onDuration func(name string, d time.Duration)) Middleware {
	return func(next ToolExecuteFunc) ToolExecuteFunc {
		return func(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
			start := time.Now()
			result, err := next(ctx, name, args)
			onDuration(name, time.Since(start))
			return result, err
		}
	}
}

// RetryMiddleware retries failed tool calls.
func RetryMiddleware(config RetryConfig) Middleware {
	return func(next ToolExecuteFunc) ToolExecuteFunc {
		return func(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
			var lastResult *ToolResult
			var lastErr error

			for attempt := 0; attempt <= config.MaxRetries; attempt++ {
				result, err := next(ctx, name, args)
				if err == nil && (result == nil || result.Success) {
					return result, nil
				}

				lastResult = result
				lastErr = err

				if attempt < config.MaxRetries {
					delay := CalculateBackoff(config, attempt)
					select {
					case <-ctx.Done():
						return lastResult, ctx.Err()
					case <-time.After(delay):
					}
				}
			}

			return lastResult, lastErr
		}
	}
}

// ValidationMiddleware validates tool arguments before execution.
func ValidationMiddleware(validators map[string]func(args map[string]any) error) Middleware {
	return func(next ToolExecuteFunc) ToolExecuteFunc {
		return func(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
			if validator, ok := validators[name]; ok {
				if err := validator(args); err != nil {
					return NewErrorResult(fmt.Sprintf("validation error: %s", err)), nil
				}
			}
			return next(ctx, name, args)
		}
	}
}

// ChainMiddleware composes multiple middleware into a single middleware.
// Middleware is applied in order: first middleware is outermost.
func ChainMiddleware(middlewares ...Middleware) Middleware {
	return func(next ToolExecuteFunc) ToolExecuteFunc {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}
