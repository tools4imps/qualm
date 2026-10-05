package jev

import (
	"fmt"
	"sync"
)

// Budget caps what a run may spend. Workers share one, so every method takes the lock.
type Budget struct {
	mu       sync.Mutex
	limit    float64
	requests int
	tokens   int
	cost     float64
}

// NewBudget returns a budget of limit dollars.
func NewBudget(limit float64) *Budget { return &Budget{limit: limit} }

// Allow returns an error once the spend has reached the limit. A request already in flight can
// overshoot it by its own cost, which is only known after the reply.
func (b *Budget) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cost >= b.limit {
		return fmt.Errorf("budget reached: spent $%.4f of $%.4f", b.cost, b.limit)
	}
	return nil
}

// Spend records one request and what its reply cost.
func (b *Budget) Spend(r Reply) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.requests++
	b.tokens += r.InputTokens
	b.cost += r.Cost
}

// Spent returns the requests made, the input tokens sent and the dollars spent so far.
func (b *Budget) Spent() (requests, inputTokens int, cost float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.requests, b.tokens, b.cost
}
