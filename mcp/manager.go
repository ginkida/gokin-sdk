package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// Manager coordinates multiple MCP server connections and their tools.
type Manager struct {
	configs map[string]ServerConfig
	clients map[string]*Client
	tools   map[string]*wrappedTool // tool name → wrapper
	mu      sync.RWMutex
}

// ServerStatus describes the connection status of an MCP server.
type ServerStatus struct {
	Name      string
	Connected bool
	Tools     []string
	Error     string
}

// NewManager creates a new MCP manager from server configurations.
func NewManager(configs []ServerConfig) *Manager {
	m := &Manager{
		configs: make(map[string]ServerConfig),
		clients: make(map[string]*Client),
		tools:   make(map[string]*wrappedTool),
	}

	for _, cfg := range configs {
		m.configs[cfg.Name] = cfg
	}

	return m
}

// ConnectAll connects to all servers configured with auto_connect.
func (m *Manager) ConnectAll(ctx context.Context) error {
	var errs []string

	for name, cfg := range m.configs {
		if !cfg.AutoConnect {
			continue
		}
		if err := m.Connect(ctx, name); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("connection errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Connect connects to a specific server by name.
func (m *Manager) Connect(ctx context.Context, name string) error {
	cfg, ok := m.configs[name]
	if !ok {
		return fmt.Errorf("unknown server: %s", name)
	}

	client, err := NewClientFromConfig(cfg)
	if err != nil {
		return fmt.Errorf("creating client: %w", err)
	}

	if err := client.Initialize(ctx); err != nil {
		client.Close()
		return fmt.Errorf("initializing: %w", err)
	}

	// Discover tools
	mcpTools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return fmt.Errorf("listing tools: %w", err)
	}

	m.mu.Lock()
	m.clients[name] = client

	// Register tools
	for _, tool := range mcpTools {
		toolName := name + ":" + tool.Name
		m.tools[toolName] = &wrappedTool{
			mcpTool:    tool,
			client:     client,
			serverName: name,
			fullName:   toolName,
		}
	}
	m.mu.Unlock()

	return nil
}

// Disconnect closes a server connection and removes its tools.
func (m *Manager) Disconnect(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, ok := m.clients[name]
	if !ok {
		return fmt.Errorf("server not connected: %s", name)
	}

	// Remove tools from this server
	for toolName, tool := range m.tools {
		if tool.serverName == name {
			delete(m.tools, toolName)
		}
	}

	delete(m.clients, name)
	return client.Close()
}

// RegisterTools adds all MCP tools to an SDK registry.
func (m *Manager) RegisterTools(registry *sdk.Registry) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, tool := range m.tools {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("registering tool %s: %w", tool.fullName, err)
		}
	}
	return nil
}

// GetServerStatus returns the status of all servers.
func (m *Manager) GetServerStatus() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var statuses []ServerStatus
	for name := range m.configs {
		status := ServerStatus{Name: name}
		if _, ok := m.clients[name]; ok {
			status.Connected = true
			for toolName, tool := range m.tools {
				if tool.serverName == name {
					status.Tools = append(status.Tools, toolName)
				}
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// Shutdown closes all server connections.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for name, client := range m.clients {
		if err := client.Close(); err != nil {
			lastErr = err
		}
		delete(m.clients, name)
	}

	m.tools = make(map[string]*wrappedTool)
	return lastErr
}

// wrappedTool adapts an MCP tool to the SDK Tool interface.
type wrappedTool struct {
	mcpTool    MCPTool
	client     *Client
	serverName string
	fullName   string
}

func (t *wrappedTool) Name() string { return t.fullName }

func (t *wrappedTool) Description() string {
	desc := t.mcpTool.Description
	if desc == "" {
		desc = fmt.Sprintf("MCP tool from %s", t.serverName)
	}
	return desc
}

func (t *wrappedTool) Declaration() *genai.FunctionDeclaration {
	decl := &genai.FunctionDeclaration{
		Name:        t.fullName,
		Description: t.Description(),
	}

	// Convert JSON Schema to genai.Schema
	if t.mcpTool.InputSchema.Type != "" {
		decl.Parameters = jsonSchemaToGenai(t.mcpTool.InputSchema)
	}

	return decl
}

func (t *wrappedTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	result, err := t.client.CallTool(ctx, t.mcpTool.Name, args)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("MCP tool error: %s", err)), nil
	}

	if result.IsError {
		var errText string
		for _, c := range result.Content {
			if c.Text != "" {
				errText += c.Text
			}
		}
		return sdk.NewErrorResult(errText), nil
	}

	var text strings.Builder
	for _, c := range result.Content {
		if c.Text != "" {
			text.WriteString(c.Text)
		}
	}

	return sdk.NewSuccessResult(text.String()), nil
}

func jsonSchemaToGenai(schema JSONSchema) *genai.Schema {
	s := &genai.Schema{
		Description: schema.Description,
		Required:    schema.Required,
	}

	switch schema.Type {
	case "object":
		s.Type = genai.TypeObject
	case "string":
		s.Type = genai.TypeString
	case "integer":
		s.Type = genai.TypeInteger
	case "number":
		s.Type = genai.TypeNumber
	case "boolean":
		s.Type = genai.TypeBoolean
	case "array":
		s.Type = genai.TypeArray
	}

	if len(schema.Properties) > 0 {
		s.Properties = make(map[string]*genai.Schema)
		for name, prop := range schema.Properties {
			if prop != nil {
				s.Properties[name] = jsonSchemaToGenai(*prop)
			}
		}
	}

	if schema.Items != nil {
		s.Items = jsonSchemaToGenai(*schema.Items)
	}

	if len(schema.Enum) > 0 {
		s.Enum = schema.Enum
	}

	return s
}
