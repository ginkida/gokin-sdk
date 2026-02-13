package sdk

import (
	"math"
	"math/rand"
	"time"
)

// RetryConfig configures retry behavior with exponential backoff.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts.
	MaxRetries int

	// InitialDelay is the base delay before the first retry.
	InitialDelay time.Duration

	// MaxDelay caps the maximum delay between retries.
	MaxDelay time.Duration

	// Multiplier is the exponential backoff multiplier (default: 2.0).
	Multiplier float64
}

// DefaultRetryConfig returns a sensible default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:   3,
		InitialDelay: 1 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
	}
}

// CalculateBackoff returns the delay for the given attempt using exponential backoff with jitter.
// Attempt is 0-indexed (0 = first retry).
func CalculateBackoff(config RetryConfig, attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}

	multiplier := config.Multiplier
	if multiplier <= 0 {
		multiplier = 2.0
	}

	// Calculate base delay: initialDelay * multiplier^attempt
	delay := float64(config.InitialDelay) * math.Pow(multiplier, float64(attempt))

	// Cap at max delay
	if delay > float64(config.MaxDelay) {
		delay = float64(config.MaxDelay)
	}

	// Add jitter: 0-25% of the delay
	jitter := delay * 0.25 * rand.Float64()
	delay += jitter

	return time.Duration(delay)
}

// ShouldRetry returns true if the error is retryable and we haven't exceeded max retries.
func ShouldRetry(config RetryConfig, attempt int, err error) bool {
	if attempt >= config.MaxRetries {
		return false
	}
	return IsRetryableError(err)
}
