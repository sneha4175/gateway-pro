// Package circuitbreaker implements the classic three-state circuit breaker
// pattern (closed → open → half-open → closed) at the per-backend level.
package circuitbreaker

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sneha4175/gateway-pro/internal/config"
)

// ErrCircuitOpen is returned when the circuit is open and fast-failing.
var ErrCircuitOpen = errors.New("circuit breaker is open")

type state int

const (
	stateClosed   state = iota // Normal; requests go through
	stateOpen                  // Tripped; all requests fail fast
	stateHalfOpen              // Probe; limited requests go through
)

func (s state) String() string {
	switch s {
	case stateClosed:
		return "closed"
	case stateOpen:
		return "open"
	case stateHalfOpen:
		return "half-open"
	}
	return "unknown"
}

// Breaker is a single circuit breaker for one upstream backend.
type Breaker struct {
	mu sync.Mutex
	cfg config.CircuitBreakerConfig

	state     state
	openAt    time.Time

	// Rolling window for the closed state
	window []observation

	// Counters for half-open state
	halfOpenTotal    int
	halfOpenFailures int
}

type observation struct {
	at      time.Time
	success bool
}

const rollingWindow = 10 * time.Second

// New creates a Breaker from config. Returns nil (no-op) if cfg is nil.
func New(cfg *config.CircuitBreakerConfig) *Breaker {
	if cfg == nil {
		return nil
	}
	if cfg.MinRequests == 0 {
		cfg.MinRequests = 20
	}
	if cfg.FailureThreshold == 0 {
		cfg.FailureThreshold = 50
	}
	if cfg.OpenDurationSeconds == 0 {
		cfg.OpenDurationSeconds = 30
	}
	if cfg.HalfOpenRequests == 0 {
		cfg.HalfOpenRequests = 5
	}
	return &Breaker{cfg: *cfg}
}

// Allow returns nil if a request should proceed, ErrCircuitOpen otherwise.
func (b *Breaker) Allow() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case stateClosed:
		return nil
	case stateOpen:
		if time.Since(b.openAt) > time.Duration(b.cfg.OpenDurationSeconds)*time.Second {
			b.transitionTo(stateHalfOpen)
			return nil
		}
		return ErrCircuitOpen
	case stateHalfOpen:
		if b.halfOpenTotal < b.cfg.HalfOpenRequests {
			b.halfOpenTotal++
			return nil
		}
		return ErrCircuitOpen
	}
	return nil
}

// RecordSuccess must be called when an upstream request succeeds.
func (b *Breaker) RecordSuccess() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case stateClosed:
		b.record(true)
	case stateHalfOpen:
		// All probes succeeded → close the circuit
		if b.halfOpenTotal-b.halfOpenFailures >= b.cfg.HalfOpenRequests {
			b.transitionTo(stateClosed)
		}
	}
}

// RecordFailure must be called when an upstream request fails.
func (b *Breaker) RecordFailure() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case stateClosed:
		b.record(false)
		b.maybeTrip()
	case stateHalfOpen:
		b.halfOpenFailures++
		// Any failure in half-open sends us back to open
		b.transitionTo(stateOpen)
	}
}

// State returns a human-readable state string.
func (b *Breaker) State() string {
	if b == nil {
		return "disabled"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state.String()
}

func (b *Breaker) record(success bool) {
	now := time.Now()
	b.window = append(b.window, observation{at: now, success: success})
	// Evict observations outside the rolling window
	cutoff := now.Add(-rollingWindow)
	i := 0
	for i < len(b.window) && b.window[i].at.Before(cutoff) {
		i++
	}
	b.window = b.window[i:]
}

func (b *Breaker) maybeTrip() {
	total := len(b.window)
	if total < b.cfg.MinRequests {
		return
	}
	failures := 0
	for _, o := range b.window {
		if !o.success {
			failures++
		}
	}
	pct := failures * 100 / total
	if pct >= b.cfg.FailureThreshold {
		b.transitionTo(stateOpen)
	}
}

func (b *Breaker) transitionTo(s state) {
	_ = fmt.Sprintf("circuit breaker: %s → %s", b.state, s) // keep fmt import happy
	b.state = s
	if s == stateOpen {
		b.openAt = time.Now()
		b.window = b.window[:0]
	}
	if s == stateHalfOpen {
		b.halfOpenTotal = 0
		b.halfOpenFailures = 0
	}
	if s == stateClosed {
		b.window = b.window[:0]
	}
}
