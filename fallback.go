package sdk

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/genai"
)

// FallbackClient wraps multiple Clients and automatically fails over to the next one on error.
type FallbackClient struct {
	clients []Client
	current int
	mu      sync.RWMutex
}

// NewFallbackClient creates a Client that tries each provided client in order on failure.
// At least one client is required.
func NewFallbackClient(clients ...Client) (Client, error) {
	if len(clients) == 0 {
		return nil, fmt.Errorf("at least one client is required")
	}
	return &FallbackClient{
		clients: clients,
	}, nil
}

func (f *FallbackClient) SendMessage(ctx context.Context, message string) (*StreamResponse, error) {
	return f.tryAll(func(c Client) (*StreamResponse, error) {
		return c.SendMessage(ctx, message)
	})
}

func (f *FallbackClient) SendMessageWithHistory(ctx context.Context, history []*genai.Content, message string) (*StreamResponse, error) {
	return f.tryAll(func(c Client) (*StreamResponse, error) {
		return c.SendMessageWithHistory(ctx, history, message)
	})
}

func (f *FallbackClient) SendFunctionResponse(ctx context.Context, history []*genai.Content, results []*genai.FunctionResponse) (*StreamResponse, error) {
	return f.tryAll(func(c Client) (*StreamResponse, error) {
		return c.SendFunctionResponse(ctx, history, results)
	})
}

func (f *FallbackClient) SetTools(tools []*genai.Tool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, c := range f.clients {
		c.SetTools(tools)
	}
}

func (f *FallbackClient) SetSystemInstruction(instruction string) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, c := range f.clients {
		c.SetSystemInstruction(instruction)
	}
}

func (f *FallbackClient) GetModel() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.current < len(f.clients) {
		return f.clients[f.current].GetModel()
	}
	return f.clients[0].GetModel()
}

func (f *FallbackClient) Clone() Client {
	f.mu.RLock()
	defer f.mu.RUnlock()
	clones := make([]Client, len(f.clients))
	for i, c := range f.clients {
		clones[i] = c.Clone()
	}
	return &FallbackClient{
		clients: clones,
		current: f.current,
	}
}

func (f *FallbackClient) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var lastErr error
	for _, c := range f.clients {
		if err := c.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (f *FallbackClient) tryAll(fn func(Client) (*StreamResponse, error)) (*StreamResponse, error) {
	f.mu.RLock()
	startIdx := f.current
	clients := f.clients
	f.mu.RUnlock()

	var lastErr error

	for i := 0; i < len(clients); i++ {
		idx := (startIdx + i) % len(clients)
		resp, err := fn(clients[idx])
		if err == nil {
			// Update current to this working client
			f.mu.Lock()
			f.current = idx
			f.mu.Unlock()
			return resp, nil
		}

		lastErr = err

		// Only try next if the error is retryable
		if !IsRetryableError(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("all fallback clients exhausted: %w", lastErr)
}
