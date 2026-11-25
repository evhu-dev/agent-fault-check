package proxy

import (
	"sync"
	"time"
)

const DefaultMaxTraceEvents = 10000

// EventType classifies lifecycle stages of a proxied request.
type EventType string

const (
	EventAdmitted         EventType = "admitted"
	EventForwarded        EventType = "forwarded"
	EventUpstreamOK       EventType = "upstream_ok"
	EventUpstreamNonMatch EventType = "upstream_non_match"
	EventUpstreamError    EventType = "upstream_error"
	EventFaultInjected    EventType = "fault_injected"
	EventFaultUnfired     EventType = "fault_unfired"
	EventCompleted        EventType = "completed"
	EventBudgetAborted    EventType = "budget_aborted"
)

// TraceEvent is a sanitized, bounded record of a single proxy lifecycle step.
type TraceEvent struct {
	Seq               int               `json:"seq"`
	Timestamp         time.Time         `json:"timestamp"`
	RunID             string            `json:"run_id"`
	ScenarioID        string            `json:"scenario_id"`
	Method            string            `json:"method"`
	Path              string            `json:"path"`
	CallIndex         int               `json:"call_index"`
	FaultID           string            `json:"fault_id,omitempty"`
	Event             EventType         `json:"event"`
	Status            int               `json:"status,omitempty"`
	CorrelationValues map[string]string `json:"correlation_values,omitempty"`
}

// TraceLog is a mutex-protected, bounded in-memory trace log.
type TraceLog struct {
	mu         sync.Mutex
	maxEvents  int
	events     []TraceEvent
	truncated  bool
	nextSeq    int
	runID      string
	scenarioID string
}

// NewTraceLog creates a bounded TraceLog for a scenario run.
func NewTraceLog(runID, scenarioID string, maxEvents int) *TraceLog {
	if maxEvents <= 0 {
		maxEvents = DefaultMaxTraceEvents
	}
	return &TraceLog{
		maxEvents:  maxEvents,
		runID:      runID,
		scenarioID: scenarioID,
	}
}

// Record appends a sanitized event to the trace log.
func (t *TraceLog) Record(ev TraceEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.nextSeq++
	if len(t.events) >= t.maxEvents {
		t.truncated = true
		return
	}
	ev.Seq = t.nextSeq
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	ev.RunID = t.runID
	ev.ScenarioID = t.scenarioID
	if len(ev.CorrelationValues) > 0 {
		copied := make(map[string]string, len(ev.CorrelationValues))
		for k, v := range ev.CorrelationValues {
			copied[k] = v
		}
		ev.CorrelationValues = copied
	}
	t.events = append(t.events, ev)
}

// Snapshot returns a copy of all recorded events and whether truncation occurred.
func (t *TraceLog) Snapshot() (events []TraceEvent, truncated bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]TraceEvent, len(t.events))
	for i, ev := range t.events {
		if len(ev.CorrelationValues) > 0 {
			copied := make(map[string]string, len(ev.CorrelationValues))
			for k, v := range ev.CorrelationValues {
				copied[k] = v
			}
			ev.CorrelationValues = copied
		}
		out[i] = ev
	}
	return out, t.truncated
}
