package sdk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// APIError represents an error from an LLM provider API.
type APIError struct {
	StatusCode int
	Message    string
	Provider   string
	Retryable  bool
}

func (e *APIError) Error() string {
	if e.Provider != "" {
		return fmt.Sprintf("%s API error (status %d): %s", e.Provider, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("API error (status %d): %s", e.StatusCode, e.Message)
}

// NewAPIError creates a new APIError.
func NewAPIError(statusCode int, message, provider string) *APIError {
	return &APIError{
		StatusCode: statusCode,
		Message:    message,
		Provider:   provider,
		Retryable:  isRetryableStatus(statusCode),
	}
}

// IsRetryableError checks if an error is retryable (rate limits, transient failures).
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check typed API errors
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable
	}

	// Check context errors
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Check network errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	// String-based fallback for untyped third-party errors
	msg := strings.ToLower(err.Error())
	retryablePatterns := []string{
		"rate limit",
		"too many requests",
		"eof",
		"tls handshake",
		"connection reset",
		"connection refused",
		"no such host",
		"temporary failure",
		"service unavailable",
		"bad gateway",
		"gateway timeout",
		"resource_exhausted",
		"unavailable",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}

	return false
}

// IsRateLimitError checks if an error is specifically a rate limit error.
func IsRateLimitError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 429
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "rate limit") || strings.Contains(msg, "too many requests")
}

func isRetryableStatus(code int) bool {
	switch code {
	case 429, 502, 503, 504:
		return true
	}
	return code >= 500
}
