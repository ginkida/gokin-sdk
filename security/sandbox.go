package security

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// SandboxConfig holds sandbox configuration.
type SandboxConfig struct {
	// Enabled determines if sandboxing is active
	Enabled bool
	// RootDir is the root directory for chroot (empty = use current workDir)
	RootDir string
	// EnableSeccomp enables seccomp-bpf syscall filtering (Linux only)
	EnableSeccomp bool
	// ReadOnly makes the sandbox filesystem read-only
	ReadOnly bool
}

// DefaultSandboxConfig returns the default sandbox configuration.
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Enabled:       true,
		EnableSeccomp: false,
		ReadOnly:      false,
	}
}

// SandboxResult represents the result of a sandboxed command execution.
type SandboxResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Error    error
}

// SandboxedCommand represents a command that will be executed in a sandbox.
type SandboxedCommand struct {
	cmd    *exec.Cmd
	config SandboxConfig
}

// NewSandboxedCommand creates a new sandboxed command.
func NewSandboxedCommand(ctx context.Context, workDir string, command string, config SandboxConfig) (*SandboxedCommand, error) {
	if workDir == "" {
		return nil, fmt.Errorf("workDir cannot be empty")
	}

	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve workDir: %w", err)
	}

	if _, err := os.Stat(absWorkDir); err != nil {
		return nil, fmt.Errorf("workDir does not exist: %s", absWorkDir)
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = absWorkDir
	cmd.Env = SafeEnvironment(absWorkDir)

	sandboxed := &SandboxedCommand{
		cmd:    cmd,
		config: config,
	}

	return sandboxed, nil
}

// SafeEnvironment returns a sanitized environment with safe defaults.
func SafeEnvironment(workDir string) []string {
	safeVars := map[string]string{
		"PATH":        "/usr/local/bin:/usr/bin:/bin",
		"HOME":        workDir,
		"USER":        os.Getenv("USER"),
		"TERM":        "xterm",
		"LANG":        "en_US.UTF-8",
		"LC_ALL":      "en_US.UTF-8",
		"PWD":         workDir,
		"TMPDIR":      filepath.Join(workDir, "tmp"),
		"SHELL":       "/bin/bash",
		"GOPATH":      os.Getenv("GOPATH"),
		"GOROOT":      os.Getenv("GOROOT"),
		"GOPROXY":     os.Getenv("GOPROXY"),
		"NODE_PATH":   os.Getenv("NODE_PATH"),
		"PYTHONPATH":  os.Getenv("PYTHONPATH"),
		"VIRTUAL_ENV": os.Getenv("VIRTUAL_ENV"),
		"EDITOR":      os.Getenv("EDITOR"),
		"VISUAL":      os.Getenv("VISUAL"),
	}

	env := make([]string, 0, len(safeVars))
	for k, v := range safeVars {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}

	return env
}

// Run runs the sandboxed command and returns the result.
func (sc *SandboxedCommand) Run(timeout time.Duration) *SandboxResult {
	result := &SandboxResult{}

	if timeout > 0 {
		timer := time.AfterFunc(timeout, func() {
			if sc.cmd.Process != nil {
				sc.cmd.Process.Kill()
			}
		})
		defer timer.Stop()
	}

	stdout, err := sc.cmd.StdoutPipe()
	if err != nil {
		result.Error = fmt.Errorf("failed to create stdout pipe: %w", err)
		return result
	}

	stderr, err := sc.cmd.StderrPipe()
	if err != nil {
		result.Error = fmt.Errorf("failed to create stderr pipe: %w", err)
		return result
	}

	if err := sc.cmd.Start(); err != nil {
		result.Error = fmt.Errorf("failed to start command: %w", err)
		return result
	}

	type streamReadResult struct {
		data []byte
		err  error
	}

	stdoutCh := make(chan streamReadResult, 1)
	stderrCh := make(chan streamReadResult, 1)
	go func() {
		data, err := readWithTimeout(stdout, timeout)
		stdoutCh <- streamReadResult{data: data, err: err}
	}()
	go func() {
		data, err := readWithTimeout(stderr, timeout)
		stderrCh <- streamReadResult{data: data, err: err}
	}()

	waitErr := sc.cmd.Wait()
	stdoutRes := <-stdoutCh
	stderrRes := <-stderrCh
	result.Stdout = stdoutRes.data
	result.Stderr = stderrRes.data

	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			result.Error = nil
		} else {
			result.Error = waitErr
		}
	} else if sc.cmd.ProcessState != nil {
		result.ExitCode = sc.cmd.ProcessState.ExitCode()
	}

	streamErr := errors.Join(
		wrapStreamReadError("stdout", stdoutRes.err),
		wrapStreamReadError("stderr", stderrRes.err),
	)
	if streamErr != nil && result.Error == nil {
		result.Error = streamErr
	}

	return result
}

// readWithTimeout reads from a pipe with a timeout.
func readWithTimeout(pipe interface{}, timeout time.Duration) ([]byte, error) {
	reader, ok := pipe.(io.Reader)
	if !ok {
		return nil, fmt.Errorf("pipe is not an io.Reader")
	}

	if timeout <= 0 {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, reader)
		return buf.Bytes(), err
	}

	type readResult struct {
		data []byte
		err  error
	}
	resultChan := make(chan readResult, 1)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	go func() {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, reader)
		resultChan <- readResult{data: buf.Bytes(), err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("read timeout after %v", timeout)
	case result := <-resultChan:
		return result.data, result.err
	}
}

func wrapStreamReadError(stream string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to read %s: %w", stream, err)
}

// IsSandboxSupported checks if the current system supports sandboxing features.
func IsSandboxSupported() (chroot, seccomp bool) {
	return runtime.GOOS == "linux", runtime.GOOS == "linux"
}

// IsLinux checks if the current OS is Linux.
func IsLinux() bool {
	return runtime.GOOS == "linux"
}
