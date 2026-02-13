// Package mcp provides Model Context Protocol (MCP) client support
// for integrating external tool servers with the SDK.
package mcp

import "encoding/json"

// ProtocolVersion is the supported MCP protocol version.
const ProtocolVersion = "2024-11-05"

// JSONRPCMessage is a JSON-RPC 2.0 message.
type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC error.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *JSONRPCError) Error() string {
	return e.Message
}

// ServerInfo describes an MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerCapabilities describes what the server supports.
type ServerCapabilities struct {
	Tools     *struct{} `json:"tools,omitempty"`
	Resources *struct{} `json:"resources,omitempty"`
	Prompts   *struct{} `json:"prompts,omitempty"`
}

// ClientInfo identifies the MCP client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeParams is sent to the server during initialization.
type InitializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    struct{}   `json:"capabilities"`
	ClientInfo      ClientInfo `json:"clientInfo"`
}

// InitializeResult is returned by the server after initialization.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
}

// MCPTool describes a tool provided by an MCP server.
type MCPTool struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	InputSchema JSONSchema `json:"inputSchema"`
}

// JSONSchema is a simplified JSON Schema representation.
type JSONSchema struct {
	Type        string                `json:"type"`
	Properties  map[string]*JSONSchema `json:"properties,omitempty"`
	Required    []string              `json:"required,omitempty"`
	Description string                `json:"description,omitempty"`
	Enum        []string              `json:"enum,omitempty"`
	Items       *JSONSchema           `json:"items,omitempty"`
}

// ListToolsResult is the response to tools/list.
type ListToolsResult struct {
	Tools []MCPTool `json:"tools"`
}

// CallToolParams is sent to invoke a tool.
type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// CallToolResult is returned after tool execution.
type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent represents a piece of tool output.
type ToolContent struct {
	Type string `json:"type"` // "text", "image", "resource"
	Text string `json:"text,omitempty"`
}

// Resource represents an MCP resource.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ServerConfig describes how to connect to an MCP server.
type ServerConfig struct {
	// Name identifies the server.
	Name string `json:"name" yaml:"name"`

	// Type is "stdio" or "http".
	Type string `json:"type" yaml:"type"`

	// Command is the command to execute (for stdio transport).
	Command string `json:"command,omitempty" yaml:"command,omitempty"`

	// Args are command arguments (for stdio transport).
	Args []string `json:"args,omitempty" yaml:"args,omitempty"`

	// Env are environment variables for the server process.
	Env map[string]string `json:"env,omitempty" yaml:"env,omitempty"`

	// URL is the server URL (for HTTP transport).
	URL string `json:"url,omitempty" yaml:"url,omitempty"`

	// Headers are HTTP headers (for HTTP transport).
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`

	// AutoConnect indicates the server should connect on startup.
	AutoConnect bool `json:"auto_connect,omitempty" yaml:"auto_connect,omitempty"`
}
