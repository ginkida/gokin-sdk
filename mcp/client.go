package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Client is a JSON-RPC 2.0 client for MCP servers.
type Client struct {
	transport  Transport
	serverInfo *ServerInfo
	nextID     atomic.Int64
	pending    map[int64]chan JSONRPCMessage
	mu         sync.Mutex
	closed     bool
	done       chan struct{}
}

// NewClient creates a new MCP client with the given transport.
func NewClient(transport Transport) *Client {
	c := &Client{
		transport: transport,
		pending:   make(map[int64]chan JSONRPCMessage),
		done:      make(chan struct{}),
	}

	go c.receiveLoop()
	return c
}

// NewClientFromConfig creates a client from a ServerConfig.
func NewClientFromConfig(cfg ServerConfig) (*Client, error) {
	var transport Transport
	var err error

	switch cfg.Type {
	case "stdio":
		transport, err = NewStdioTransport(cfg.Command, cfg.Args, cfg.Env)
	case "http":
		transport = NewHTTPTransport(cfg.URL, cfg.Headers)
	default:
		return nil, fmt.Errorf("unsupported transport type: %s", cfg.Type)
	}

	if err != nil {
		return nil, err
	}

	return NewClient(transport), nil
}

// Initialize performs the MCP initialization handshake.
func (c *Client) Initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo: ClientInfo{
			Name:    "gokin-sdk",
			Version: "0.2.0",
		},
	}

	data, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshaling init params: %w", err)
	}

	resp, err := c.call(ctx, "initialize", data)
	if err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}

	var result InitializeResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("parsing init result: %w", err)
	}

	c.serverInfo = &result.ServerInfo

	// Send initialized notification
	c.notify("notifications/initialized", nil)

	return nil
}

// ListTools returns all tools available from the server.
func (c *Client) ListTools(ctx context.Context) ([]MCPTool, error) {
	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("listing tools: %w", err)
	}

	var result ListToolsResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parsing tools: %w", err)
	}

	return result.Tools, nil
}

// CallTool invokes a tool on the server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	params := CallToolParams{
		Name:      name,
		Arguments: args,
	}

	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshaling call params: %w", err)
	}

	resp, err := c.call(ctx, "tools/call", data)
	if err != nil {
		return nil, fmt.Errorf("calling tool: %w", err)
	}

	var result CallToolResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parsing result: %w", err)
	}

	return &result, nil
}

// Ping checks if the server is responsive.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.call(ctx, "ping", nil)
	return err
}

// ServerInfo returns information about the connected server.
func (c *Client) ServerInfo() *ServerInfo {
	return c.serverInfo
}

// Close shuts down the client and transport.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true
	close(c.done)

	// Cancel pending requests
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}

	return c.transport.Close()
}

func (c *Client) call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}

	// Register pending request
	ch := make(chan JSONRPCMessage, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("client closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.transport.Send(msg); err != nil {
		return nil, err
	}

	// Wait for response
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("request cancelled")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("request timed out")
	}
}

func (c *Client) notify(method string, params json.RawMessage) {
	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	c.transport.Send(msg)
}

func (c *Client) receiveLoop() {
	for {
		select {
		case <-c.done:
			return
		default:
		}

		msg, err := c.transport.Receive()
		if err != nil {
			// Transport closed or error
			return
		}

		// Route response to pending request
		if msg.ID != nil {
			c.mu.Lock()
			ch, ok := c.pending[*msg.ID]
			c.mu.Unlock()

			if ok {
				ch <- msg
			}
		}
		// Notifications (no ID) are silently consumed
	}
}
