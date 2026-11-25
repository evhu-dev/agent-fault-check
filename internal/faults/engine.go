package faults

import (
	"sync"

	"github.com/agent-fault-check/afc/internal/config"
)

type selectorKey struct {
	method string
	path   string
}

// Engine tracks per-(method, path) call indices and matches fault rules at admission.
type Engine struct {
	mu          sync.Mutex
	rules       map[selectorKey]map[int]*config.FaultRule
	counts      map[selectorKey]int
	totalCalls  int
	fired       map[string]bool
	unknownErr  error
	declaredIDs []string
}

// NewEngine creates a fault engine for a single scenario.
func NewEngine(rules []config.FaultRule) *Engine {
	e := &Engine{
		rules:  make(map[selectorKey]map[int]*config.FaultRule),
		counts: make(map[selectorKey]int),
		fired:  make(map[string]bool),
	}
	for i := range rules {
		r := &rules[i]
		e.declaredIDs = append(e.declaredIDs, r.ID)
		k := selectorKey{method: r.Match.Method, path: r.Match.Path}
		if e.rules[k] == nil {
			e.rules[k] = make(map[int]*config.FaultRule)
		}
		e.rules[k][r.Match.Call] = r
	}
	return e
}

// Admit increments the total call count and the per-(method, path) 1-based index,
// returning the assigned call index, total admitted calls, and any matching fault rule.
func (e *Engine) Admit(method, escapedPath string) (callIndex int, total int, rule *config.FaultRule) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.totalCalls++
	k := selectorKey{method: method, path: escapedPath}
	e.counts[k]++
	callIndex = e.counts[k]
	if byCall := e.rules[k]; byCall != nil {
		rule = byCall[callIndex]
	}
	return callIndex, e.totalCalls, rule
}

// MarkFired records that a fault rule successfully injected its fault.
func (e *Engine) MarkFired(faultID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fired[faultID] = true
}

// RecordUnknownCommit records an ambiguous upstream outcome on a targeted commit_then_fail call.
func (e *Engine) RecordUnknownCommit(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.unknownErr == nil {
		e.unknownErr = err
	}
}

// UnknownCommitError returns the first ambiguous commit error, if any.
func (e *Engine) UnknownCommitError() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.unknownErr
}

// UnfiredFaults returns the IDs of declared faults that never injected.
func (e *Engine) UnfiredFaults() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var missing []string
	for _, id := range e.declaredIDs {
		if !e.fired[id] {
			missing = append(missing, id)
		}
	}
	return missing
}

// CountFor returns the total admitted calls for a (method, path) selector.
func (e *Engine) CountFor(method, escapedPath string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counts[selectorKey{method: method, path: escapedPath}]
}
