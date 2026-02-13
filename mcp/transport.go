package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Transport is the interface for MCP communication.
type Transport interface {
	// Send sends a JSON-RPC message.
	Send(msg JSONRPCMessage) error

	// Receive returns the next incoming message.
	Receive() (JSONRPCMessage, error)

	// Close shuts down the transport.
	Close() error
}

// StdioTransport communicates with an MCP server via stdin/stdout.
type StdioTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	mu      sync.Mutex
	closed  bool
}

// NewStdioTransport creates a transport that spawns a subprocess.
func NewStdioTransport(command string, args []string, env map[string]string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)

	// Build safe environment
	cmdEnv := buildTransportEnv(env)
	cmd.Env = cmdEnv

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	// Capture stderr in background
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting process: %w", err)
	}

	// Drain stderr in background
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			// Could log this if needed
			_ = scanner.Text()
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	return &StdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		scanner: scanner,
	}, nil
}

func (t *StdioTransport) Send(msg JSONRPCMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return fmt.Errorf("transport closed")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}

	data = append(data, '\n')
	if _, err := t.stdin.Write(data); err != nil {
		return fmt.Errorf("writing message: %w", err)
	}

	return nil
}

func (t *StdioTransport) Receive() (JSONRPCMessage, error) {
	if !t.scanner.Scan() {
		err := t.scanner.Err()
		if err == nil {
			return JSONRPCMessage{}, io.EOF
		}
		return JSONRPCMessage{}, err
	}

	var msg JSONRPCMessage
	if err := json.Unmarshal(t.scanner.Bytes(), &msg); err != nil {
		return JSONRPCMessage{}, fmt.Errorf("unmarshaling message: %w", err)
	}

	return msg, nil
}

func (t *StdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true

	t.stdin.Close()

	// Graceful shutdown: wait 5 seconds then kill
	done := make(chan error, 1)
	go func() {
		done <- t.cmd.Wait()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.cmd.Process.Kill()
		<-done
	}

	return nil
}

// HTTPTransport communicates with an MCP server via HTTP.
type HTTPTransport struct {
	url     string
	headers map[string]string
	client  *http.Client
	respCh  chan JSONRPCMessage
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
}

// NewHTTPTransport creates a transport that communicates over HTTP.
func NewHTTPTransport(url string, headers map[string]string) *HTTPTransport {
	ctx, cancel := context.WithCancel(context.Background())
	_ = ctx

	return &HTTPTransport{
		url:     url,
		headers: headers,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		respCh: make(chan JSONRPCMessage, 10),
		cancel: cancel,
	}
}

func (t *HTTPTransport) Send(msg JSONRPCMessage) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return fmt.Errorf("transport closed")
	}
	t.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}

	req, err := http.NewRequest("POST", t.url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var respMsg JSONRPCMessage
	if err := json.NewDecoder(resp.Body).Decode(&respMsg); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	t.respCh <- respMsg
	return nil
}

func (t *HTTPTransport) Receive() (JSONRPCMessage, error) {
	msg, ok := <-t.respCh
	if !ok {
		return JSONRPCMessage{}, io.EOF
	}
	return msg, nil
}

func (t *HTTPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true
	t.cancel()
	close(t.respCh)
	return nil
}

func buildTransportEnv(extra map[string]string) []string {
	// Inherit safe environment variables
	safeVars := []string{
		"PATH", "HOME", "USER", "SHELL", "TERM", "LANG",
		"NODE_PATH", "PYTHONPATH", "GOPATH", "GOROOT",
	}

	env := make([]string, 0)
	for _, key := range safeVars {
		if val := os.Getenv(key); val != "" {
			env = append(env, key+"="+val)
		}
	}

	// Ensure PATH
	hasPath := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
			break
		}
	}
	if !hasPath {
		env = append(env, "PATH=/usr/local/bin:/usr/bin:/bin")
	}

	// Add extra env vars
	for k, v := range extra {
		env = append(env, k+"="+v)
	}

	return env
}
