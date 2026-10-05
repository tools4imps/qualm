package jev

import (
	"math"
	"strings"
	"sync"
	"testing"
)

// Contract: jev/J10
func TestBudgetRefusesAtTheLimit(t *testing.T) {
	b := NewBudget(1.00)
	if err := b.Allow(); err != nil {
		t.Fatal(err)
	}
	b.Spend(Reply{Cost: 0.99})
	if err := b.Allow(); err != nil {
		t.Errorf("under the limit: %v", err)
	}
	b.Spend(Reply{Cost: 0.01})
	err := b.Allow()
	if err == nil {
		t.Fatal("at the limit: want an error")
	}
	if !strings.Contains(err.Error(), "$1.00") || !strings.Contains(err.Error(), "spent") {
		t.Errorf("error = %v", err)
	}
	b.Spend(Reply{Cost: 1})
	if b.Allow() == nil {
		t.Error("over the limit: want an error")
	}
}

// Contract: jev/J10
func TestBudgetCountsFromManyGoroutines(t *testing.T) {
	b := NewBudget(1000)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = b.Allow()
				b.Spend(Reply{InputTokens: 10, Cost: 0.5})
			}
		}()
	}
	wg.Wait()
	requests, tokens, cost := b.Spent()
	if requests != 1000 || tokens != 10000 || math.Abs(cost-500) > 1e-9 {
		t.Errorf("spent = %d, %d, %v", requests, tokens, cost)
	}
}

// Contract: jev/J10
func TestBudgetErrorShowsASmallLimitInFull(t *testing.T) {
	b := NewBudget(0.0005)
	b.Spend(Reply{InputTokens: 100, Cost: 0.002})
	err := b.Allow()
	if err == nil {
		t.Fatal("Allow passed with the limit already spent")
	}
	for _, want := range []string{"$0.0020", "$0.0005"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should hold %s", err, want)
		}
	}
}
