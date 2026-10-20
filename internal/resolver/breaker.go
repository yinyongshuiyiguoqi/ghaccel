package resolver

import (
	"sync"
	"time"
)

type breakerState struct {
	failures  int
	openUntil time.Time
}

type Breaker struct {
	mu        sync.Mutex
	state     map[string]*breakerState
	threshold int
	openFor   time.Duration
}

func NewBreaker(threshold int, openFor time.Duration) *Breaker {
	if threshold < 1 {
		threshold = 1
	}
	return &Breaker{
		state:     make(map[string]*breakerState),
		threshold: threshold,
		openFor:   openFor,
	}
}

func (b *Breaker) Allow(name string) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.state[name]
	if s == nil {
		return true
	}
	if !s.openUntil.IsZero() && time.Now().After(s.openUntil) {
		s.openUntil = time.Time{}
		s.failures = 0
		return true
	}
	return s.openUntil.IsZero()
}

func (b *Breaker) Success(name string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.state[name]
	if s == nil {
		s = &breakerState{}
		b.state[name] = s
	}
	s.failures = 0
	s.openUntil = time.Time{}
}

func (b *Breaker) Failure(name string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.state[name]
	if s == nil {
		s = &breakerState{}
		b.state[name] = s
	}
	s.failures++
	if s.failures >= b.threshold {
		s.openUntil = time.Now().Add(b.openFor)
	}
}

func (b *Breaker) IsOpen(name string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.state[name]
	return s != nil && !s.openUntil.IsZero() && time.Now().Before(s.openUntil)
}
