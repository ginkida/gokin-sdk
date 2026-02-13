package sdk

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the circuit breaker is in the open state.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	// CircuitClosed allows all requests through.
	CircuitClosed CircuitState = iota
	// CircuitHalfOpen allows a single test request through.
	CircuitHalfOpen
	// CircuitOpen rejects all requests.
	CircuitOpen
)

// String returns the string representation of the circuit state.
func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitHalfOpen:
		return "half-open"
	case CircuitOpen:
		return "open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern for client calls.
type CircuitBreaker struct {
	mu           sync.RWMutex
	state        CircuitState
	failures     int
	threshold    int
	resetTimeout time.Duration
	lastFailure  time.Time
}

// NewCircuitBreaker creates a new circuit breaker.
// threshold is the number of consecutive failures before opening.
// resetTimeout is how long to wait before transitioning from open to half-open.
func NewCircuitBreaker(threshold int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold:    threshold,
		resetTimeout: resetTimeout,
		state:        CircuitClosed,
	}
}

// Execute runs fn if the circuit allows it, recording success or failure.
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() error) error {
	if !cb.allowRequest() {
		return ErrCircuitOpen
	}

	err := fn()
	if err != nil {
		cb.recordFailure()
		return err
	}

	cb.recordSuccess()
	return nil
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset resets the circuit breaker to the closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.failures = 0
}

// Failures returns the current failure count.
func (cb *CircuitBreaker) Failures() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failures
}

func (cb *CircuitBreaker) allowRequest() bool {
	cb.mu.RLock()
	state := cb.state
	expired := cb.state == CircuitOpen && time.Since(cb.lastFailure) > cb.resetTimeout
	cb.mu.RUnlock()

	switch state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if !expired {
			return false
		}
		// Upgrade to write lock for state transition
		cb.mu.Lock()
		if cb.state == CircuitOpen && time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = CircuitHalfOpen
		}
		cb.mu.Unlock()
		return true
	case CircuitHalfOpen:
		return true
	default:
		return false
	}
}

func (cb *CircuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = time.Now()

	if cb.state == CircuitHalfOpen || cb.failures >= cb.threshold {
		cb.state = CircuitOpen
	}
}

func (cb *CircuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == CircuitHalfOpen {
		cb.state = CircuitClosed
		cb.failures = 0
	} else if cb.state == CircuitClosed {
		cb.failures = 0
	}
}
